package tiki

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const testPassword = "correct horse battery staple"

func fixture(t *testing.T) (*Store, User) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	admin, err := s.Bootstrap(t.Context(), "Admin", "admin@example.test", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	return s, admin
}

func addItem(t *testing.T, s *Store, actor ID, title string) Item {
	t.Helper()
	i, err := s.Create(t.Context(), actor, CreateItem{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var api *Error
	if !errors.As(err, &api) || api.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestMembershipsFiltersAndRollback(t *testing.T) {
	s, admin := fixture(t)
	bob, err := s.CreateUser(t.Context(), "Bob", "bob@example.test", "member", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.Create(t.Context(), admin.ID, CreateItem{Title: "First", Description: "large body", Assignees: []ID{admin.ID, bob.ID, bob.ID}, Tags: []string{" Repo/Tiki ", "frontend", "FRONTEND"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(i.Assignees, []ID{admin.ID, bob.ID}) || !slices.Equal(i.Tags, []string{"frontend", "repo/tiki"}) {
		t.Fatalf("unexpected memberships: %+v", i)
	}
	addItem(t, s, admin.ID, "Unassigned")
	p, err := s.List(t.Context(), Filter{Assignee: bob.ID, Tags: []string{"REPO/TIKI", "frontend"}})
	if err != nil || len(p.Items) != 1 || p.Items[0].Description != "" {
		t.Fatalf("filtered list: %+v, %v", p, err)
	}
	p, err = s.List(t.Context(), Filter{Unassigned: true})
	if err != nil || len(p.Items) != 1 {
		t.Fatalf("unassigned: %+v, %v", p, err)
	}
	i, err = s.Update(t.Context(), admin.ID, i.ID, UpdateItem{Version: i.Version, AddAssignees: []ID{bob.ID}, RemoveAssignees: []ID{admin.ID}, AddTags: []string{"backend"}, RemoveTags: []string{"frontend"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(i.Assignees, []ID{bob.ID}) || !slices.Equal(i.Tags, []string{"backend", "repo/tiki"}) {
		t.Fatalf("set edit lost memberships: %+v", i)
	}
	_, err = s.Update(t.Context(), admin.ID, i.ID, UpdateItem{Version: i.Version, AddAssignees: []ID{999}, RemoveTags: []string{"backend"}})
	requireCode(t, err, "validation")
	unchanged, err := s.Get(t.Context(), i.ID)
	if err != nil || unchanged.Version != i.Version || !slices.Equal(unchanged.Tags, i.Tags) {
		t.Fatalf("failed edit leaked: %+v, %v", unchanged, err)
	}
	_, err = s.Create(t.Context(), admin.ID, CreateItem{Title: "Must roll back", Assignees: []ID{999}})
	requireCode(t, err, "validation")
	p, err = s.List(t.Context(), Filter{})
	if err != nil || len(p.Items) != 2 {
		t.Fatalf("failed create persisted: %+v, %v", p, err)
	}
	entries, err := s.Activity(t.Context(), i.ID, 0, 50)
	if err != nil || len(entries.Activity) != 2 {
		t.Fatalf("activity not atomic: %+v, %v", entries, err)
	}
}

func TestActivityPaginationBoundsLargeDescriptions(t *testing.T) {
	s, admin := fixture(t)
	i := addItem(t, s, admin.ID, "History")
	description := strings.Repeat("<", 256*1024)
	for range 4 {
		var err error
		i, err = s.Update(t.Context(), admin.ID, i.ID, UpdateItem{Version: i.Version, Description: &description})
		if err != nil {
			t.Fatal(err)
		}
	}
	var after ID
	total := 0
	for {
		page, err := s.Activity(t.Context(), i.ID, after, 200)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(page)
		if err != nil || len(data) > 3<<20 {
			t.Fatalf("unbounded activity page: %d %v", len(data), err)
		}
		total += len(page.Activity)
		if page.NextAfter == 0 {
			break
		}
		if page.NextAfter <= after {
			t.Fatal("cursor failed to advance")
		}
		after = page.NextAfter
	}
	if total != 5 {
		t.Fatalf("lost activity: %d", total)
	}
}

func TestConcurrentEditsAndMoves(t *testing.T) {
	s, admin := fixture(t)
	i := addItem(t, s, admin.ID, "Contended")
	anchor := addItem(t, s, admin.ID, "Anchor")
	errors := make(chan error, 8)
	var wg sync.WaitGroup
	for n := range 8 {
		wg.Go(func() {
			var err error
			if n%2 == 0 {
				_, err = s.Move(t.Context(), admin.ID, i.ID, MoveItem{Version: i.Version, After: anchor.ID})
			} else {
				title := "edited"
				_, err = s.Update(t.Context(), admin.ID, i.ID, UpdateItem{Version: i.Version, Title: &title})
			}
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	success := 0
	for err := range errors {
		if err == nil {
			success++
		} else {
			requireCode(t, err, "conflict")
		}
	}
	if success != 1 {
		t.Fatalf("expected one winner, got %d", success)
	}
}

func TestReorderingRebalancesAndExpiresCursor(t *testing.T) {
	s, admin := fixture(t)
	first := addItem(t, s, admin.ID, "First")
	left := addItem(t, s, admin.ID, "Left")
	right := addItem(t, s, admin.ID, "Right")
	initial, err := s.List(t.Context(), Filter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for range 160 {
		current, err := s.Get(t.Context(), right.ID)
		if err != nil {
			t.Fatal(err)
		}
		moved, err := s.Move(t.Context(), admin.ID, current.ID, MoveItem{Version: current.Version, Before: left.ID})
		if err != nil {
			t.Fatal(err)
		}
		p, err := s.List(t.Context(), Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Items) != 3 || p.Items[0].ID != first.ID || p.Items[1].ID != moved.ID || p.Items[2].ID != left.ID || !(p.Items[0].Priority < p.Items[1].Priority && p.Items[1].Priority < p.Items[2].Priority) {
			t.Fatalf("order corrupted: %+v", p.Items)
		}
		left, right = moved, left
	}
	_, err = s.List(t.Context(), Filter{Limit: 1, Cursor: initial.NextCursor})
	requireCode(t, err, "cursor_expired")
	var resets int
	if err = s.write.QueryRow("SELECT count(*) FROM activity WHERE kind='ordering.reset'").Scan(&resets); err != nil || resets == 0 {
		t.Fatalf("missing rebalance event: %d %v", resets, err)
	}
}

func TestFloatEdgesAndPagination(t *testing.T) {
	s, admin := fixture(t)
	for _, priority := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := s.Create(t.Context(), admin.ID, CreateItem{Title: "invalid", Priority: &priority})
		requireCode(t, err, "validation")
	}
	rank := math.MaxFloat64
	first, err := s.Create(t.Context(), admin.ID, CreateItem{Title: "huge", Priority: &rank})
	if err != nil {
		t.Fatal(err)
	}
	second := addItem(t, s, admin.ID, "append triggers normalization")
	third, err := s.Create(t.Context(), admin.ID, CreateItem{Title: "tie", Priority: &second.Priority})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Move(t.Context(), admin.ID, first.ID, MoveItem{Version: 1, Before: second.ID})
	requireCode(t, err, "conflict") // The append renumbered existing versions.
	first, err = s.Get(t.Context(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Move(t.Context(), admin.ID, first.ID, MoveItem{Version: first.Version, Before: third.ID})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.List(t.Context(), Filter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	seen := []ID{}
	for {
		if len(page.Items) != 1 {
			t.Fatalf("bad page: %+v", page)
		}
		seen = append(seen, page.Items[0].ID)
		if page.NextCursor == "" {
			break
		}
		page, err = s.List(t.Context(), Filter{Limit: 1, Cursor: page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(seen, []ID{second.ID, first.ID, third.ID}) {
		t.Fatalf("wrong ordering/paging: %v", seen)
	}
	page, err = s.List(t.Context(), Filter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.List(t.Context(), Filter{Cursor: page.NextCursor, Status: StatusTodo})
	requireCode(t, err, "validation")
	_, err = s.Move(t.Context(), admin.ID, first.ID, MoveItem{Version: 99, Before: first.ID})
	requireCode(t, err, "validation")
}

func TestPersistenceCredentialsAndSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persistent.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.Bootstrap(t.Context(), "Admin", "admin@example.test", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	i := addItem(t, s, admin.ID, "Survives restart")
	session, err := s.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = s.write.QueryRow("SELECT hash FROM sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == session.Token || len(stored) != 64 {
		t.Fatal("session credential was not hashed")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(t.Context(), i.ID)
	if err != nil || got.Title != i.Title {
		t.Fatalf("lost data: %+v %v", got, err)
	}
	_, err = s.Bootstrap(t.Context(), "replacement", "other@example.test", testPassword)
	requireCode(t, err, "conflict")
	if _, err = s.Authenticate(t.Context(), session.Token); err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(t.Context(), session.Token); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(t.Context(), session.Token)
	requireCode(t, err, "unauthorized")
	if _, err = s.write.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(path); err == nil {
		t.Fatal("accepted future database schema")
	}
}

func TestIDsKeepIntegerPrecision(t *testing.T) {
	original := ID(math.MaxInt64)
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ID
	if err = json.Unmarshal(b, &decoded); err != nil || decoded != original {
		t.Fatalf("ID roundtrip: %s %d %v", b, decoded, err)
	}
	if err = json.Unmarshal([]byte(`123`), &decoded); err == nil {
		t.Fatal("accepted a numeric wire ID")
	}
}

func BenchmarkCreateAndMove(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "benchmark.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	admin, err := s.Bootstrap(context.Background(), "Bench", "bench@example.test", testPassword)
	if err != nil {
		b.Fatal(err)
	}
	anchor, err := s.Create(context.Background(), admin.ID, CreateItem{Title: "Anchor"})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		i, err := s.Create(context.Background(), admin.ID, CreateItem{Title: "Item", Tags: []string{"bench"}, Assignees: []ID{admin.ID}})
		if err != nil {
			b.Fatal(err)
		}
		if _, err = s.Move(context.Background(), admin.ID, i.ID, MoveItem{Version: i.Version, Before: anchor.ID}); err != nil {
			b.Fatal(err)
		}
	}
}
