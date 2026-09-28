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
	"unicode/utf8"
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

func TestTitleQuery(t *testing.T) {
	s, admin := fixture(t)
	for _, title := range []string{"First", "Unassigned", "Fix keyboard focus"} {
		addItem(t, s, admin.ID, title)
	}
	for query, want := range map[string]int{"  FOCUS keyboard ": 1, "first": 1, "focus first": 0, "": 3} {
		p, err := s.List(t.Context(), Filter{Queries: []string{query}})
		if err != nil || len(p.Items) != want {
			t.Fatalf("query %q: %+v, %v", query, p, err)
		}
	}
	_, err := s.List(t.Context(), Filter{Queries: []string{"a\tb"}})
	requireCode(t, err, "validation")
	// A cursor issued for one query must not continue another.
	p, err := s.List(t.Context(), Filter{Queries: []string{"f"}, Limit: 1})
	if err != nil || p.NextCursor == "" {
		t.Fatalf("paged query: %+v, %v", p, err)
	}
	_, err = s.List(t.Context(), Filter{Queries: []string{"first"}, Limit: 1, Cursor: p.NextCursor})
	requireCode(t, err, "validation")
}

func TestDescriptionAndAlternativeQueries(t *testing.T) {
	s, admin := fixture(t)
	first, err := s.Create(t.Context(), admin.ID, CreateItem{
		Title: "Keyboard focus", Status: StatusTodo, Tags: []string{"frontend"},
		Description: strings.Repeat("Context. ", 1000) + "Tab navigation, path/to/file.go 100% a_b [draft]",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(t.Context(), admin.ID, CreateItem{Title: "Mouse controls", Status: StatusTodo, Description: "Click pointer focus"})
	if err != nil {
		t.Fatal(err)
	}
	third := addItem(t, s, admin.ID, "Unrelated")
	for _, tc := range []struct {
		queries []string
		want    []ID
	}{
		{[]string{"NAVIGATION"}, []ID{first.ID}},
		{[]string{"keyboard navigation frontend"}, []ID{first.ID}},
		{[]string{"keyboard pointer"}, nil},
		{[]string{"navigation", "pointer"}, []ID{first.ID, second.ID}},
		{[]string{"keyboard missing", "pointer"}, []ID{second.ID}},
		{[]string{"focus", "controls"}, []ID{first.ID, second.ID}},
		{[]string{"navigation, path/to/file.go 100% a_b [draft]"}, []ID{first.ID}},
		{[]string{"navigation|pointer"}, nil},
		{[]string{"' OR 1=1 --"}, nil},
		{[]string{" ", "pointer"}, []ID{second.ID}},
		{[]string{" "}, []ID{first.ID, second.ID, third.ID}},
	} {
		page, err := s.List(t.Context(), Filter{Queries: tc.queries})
		if err != nil {
			t.Fatal(err)
		}
		var ids []ID
		for _, item := range page.Items {
			ids = append(ids, item.ID)
			if item.Description != "" || strings.Contains(item.Preview, "navigation") {
				t.Fatalf("search leaked full description: %+v", item)
			}
		}
		if !slices.Equal(ids, tc.want) {
			t.Fatalf("queries %q: got %v, want %v", tc.queries, ids, tc.want)
		}
	}
	// Filters constrain every OR alternative, including matches in descriptions.
	page, err := s.List(t.Context(), Filter{Queries: []string{"navigation", "pointer", "unrelated"}, Tags: []string{"frontend"}, Status: StatusTodo})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != first.ID {
		t.Fatalf("filtered alternatives: %+v, %v", page, err)
	}
	filter := Filter{Queries: []string{"focus", "controls"}, Limit: 1}
	page, err = s.List(t.Context(), filter)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != first.ID || page.NextCursor == "" {
		t.Fatalf("first page: %+v, %v", page, err)
	}
	filter.Cursor = page.NextCursor
	page, err = s.List(t.Context(), filter)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != second.ID || page.NextCursor != "" {
		t.Fatalf("next page: %+v, %v", page, err)
	}
	filter.Queries[1] = "mouse"
	_, err = s.List(t.Context(), filter)
	requireCode(t, err, "validation")
	for _, queries := range [][]string{
		{"1", "2", "3", "4", "5", "6"}, {strings.Repeat("x", 301)},
		{strings.Repeat("word ", 11)}, {"valid", "a\tb"}, {"\xff"},
	} {
		_, err := s.List(t.Context(), Filter{Queries: queries})
		requireCode(t, err, "validation")
	}
}

func TestSearchFindsTicketsBeyondTheFirstPage(t *testing.T) {
	s, admin := fixture(t)
	for range 25 {
		addItem(t, s, admin.ID, "Earlier ticket")
	}
	target, err := s.Create(t.Context(), admin.ID, CreateItem{Title: "Buried release work", Description: "Deployment checklist", Tags: []string{"repo/tiki"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"buried RELEASE", "deployment checklist", "release deployment", target.ID.String(), "#" + target.ID.String(), "TK-" + target.ID.String(), "repo/tiki", "release tiki"} {
		board, err := s.Board(t.Context(), Filter{Queries: []string{query}})
		if err != nil || len(board[StatusBacklog].Items) != 1 || board[StatusBacklog].Items[0].ID != target.ID {
			t.Fatalf("query %q missed buried ticket: %+v, %v", query, board, err)
		}
	}
	board, err := s.Board(t.Context(), Filter{Queries: []string{"earlier"}})
	if err != nil || len(board[StatusBacklog].Items) != 20 || board[StatusBacklog].NextCursor == "" {
		t.Fatalf("search is not paginated: %+v, %v", board, err)
	}
	page, err := s.List(t.Context(), Filter{Status: StatusBacklog, Queries: []string{"earlier"}, Cursor: board[StatusBacklog].NextCursor})
	if err != nil || len(page.Items) != 5 {
		t.Fatalf("search continuation: %+v, %v", page, err)
	}
	_, err = s.List(t.Context(), Filter{Status: StatusBacklog, Queries: []string{"release"}, Cursor: board[StatusBacklog].NextCursor})
	requireCode(t, err, "validation")
}

func TestItemLink(t *testing.T) {
	s, admin := fixture(t)
	i := addItem(t, s, admin.ID, "Linked")
	link := " https://github.com/accurise/tiki/pull/42 "
	i, err := s.Update(t.Context(), admin.ID, i.ID, UpdateItem{Version: i.Version, URL: &link})
	if err != nil || i.URL != "https://github.com/accurise/tiki/pull/42" {
		t.Fatalf("link not stored trimmed: %q, %v", i.URL, err)
	}
	page, err := s.List(t.Context(), Filter{})
	if err != nil || len(page.Items) != 1 || page.Items[0].URL != i.URL {
		t.Fatalf("link missing from list: %+v, %v", page, err)
	}
	for _, bad := range []string{"ftp://x", "github.com/pull/1", "javascript:alert(1)", "https://x/a\tb", "https://" + strings.Repeat("a", 2048)} {
		_, err = s.Update(t.Context(), admin.ID, i.ID, UpdateItem{Version: i.Version, URL: &bad})
		requireCode(t, err, "validation")
	}
	empty := ""
	if i, err = s.Update(t.Context(), admin.ID, i.ID, UpdateItem{Version: i.Version, URL: &empty}); err != nil || i.URL != "" {
		t.Fatalf("link not cleared: %q, %v", i.URL, err)
	}
	if _, err = s.Create(t.Context(), admin.ID, CreateItem{Title: "Bad", URL: "nope"}); err == nil {
		t.Fatal("create accepted an invalid link")
	}
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
	entries, err := s.Activity(t.Context(), i.ID, 0, nil, 50)
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
		// Include a discrete change so these remain separate history entries.
		status := StatusTodo
		if i.Status == status {
			status = StatusBacklog
		}
		i, err = s.Update(t.Context(), admin.ID, i.ID, UpdateItem{Version: i.Version, Description: &description, Status: &status})
		if err != nil {
			t.Fatal(err)
		}
	}
	var after ID
	total := 0
	for {
		page, err := s.Activity(t.Context(), i.ID, 0, &after, 200)
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

func TestDescriptionPreview(t *testing.T) {
	long := strings.Repeat("word ", 60)
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"  Plain   text\n\nacross\tlines ", "Plain text across lines"},
		{"# Heading\n- first\n* second\n1. third\n> quote\n- [ ] task", "Heading first second third quote task"},
		{"```go\nfmt.Println()\n```\n-5 degrees", "fmt.Println() -5 degrees"},
		{long, strings.TrimSpace(strings.Repeat("word ", 28)) + "…"},
	} {
		if got := descriptionPreview(tc.in); got != tc.want {
			t.Errorf("descriptionPreview(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := descriptionPreview(strings.Repeat("é", 500)); utf8.RuneCountInString(got) != PreviewRunes+1 || !utf8.ValidString(got) {
		t.Fatalf("unbroken text preview: %q", got)
	}
}

func TestListsCarryBoundedPreviews(t *testing.T) {
	s, admin := fixture(t)
	description := "## Goal\n\nKeep the board fast. " + strings.Repeat("Details that only the editor needs. ", 2000)
	i, err := s.Create(t.Context(), admin.ID, CreateItem{Title: "Previewed", Description: description})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(i.Preview, "Goal Keep the board fast.") || i.Description != description {
		t.Fatalf("created item: preview %q", i.Preview)
	}
	p, err := s.List(t.Context(), Filter{})
	if err != nil || len(p.Items) != 1 {
		t.Fatalf("list: %+v, %v", p, err)
	}
	if got := p.Items[0]; got.Description != "" || got.Preview != i.Preview {
		t.Fatalf("listed item carries %d description bytes and preview %q", len(got.Description), got.Preview)
	}
}
