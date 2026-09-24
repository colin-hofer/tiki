package tiki

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUserRoleRemovalAndReinvitation(t *testing.T) {
	s, admin := fixture(t)
	member, err := s.CreateUser(t.Context(), "Member", "member@example.test", "member", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Login(t.Context(), member.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(t.Context(), member.ID, CreateItem{Title: "Keep history", Assignees: []ID{member.ID}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Revision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeUserRole(t.Context(), member.ID, "viewer"); err != nil {
		t.Fatal(err)
	}
	current, err := s.Authenticate(t.Context(), session.Token)
	if err != nil || current.Role != "viewer" {
		t.Fatalf("role change left stale permissions: %+v %v", current, err)
	}
	after, err := s.Revision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	updates, err := s.Updates(t.Context(), before, after)
	if err != nil || !updates.Users || updates.Reset {
		t.Fatalf("role change did not update directory: %+v %v", updates, err)
	}
	_, err = s.ChangeUserRole(t.Context(), admin.ID, "member")
	requireCode(t, err, "conflict")
	_, err = s.RemoveUser(t.Context(), admin.ID)
	requireCode(t, err, "conflict")
	_, err = s.ChangeUserRole(t.Context(), member.ID, "owner")
	requireCode(t, err, "validation")
	removed, err := s.RemoveUser(t.Context(), member.ID)
	if err != nil || removed.RemovedAt == 0 {
		t.Fatalf("remove: %+v %v", removed, err)
	}
	_, err = s.Authenticate(t.Context(), session.Token)
	requireCode(t, err, "unauthorized")
	_, err = s.Login(t.Context(), member.Email, testPassword)
	requireCode(t, err, "unauthorized")
	_, err = s.ChangeUserRole(t.Context(), member.ID, "member")
	requireCode(t, err, "conflict")
	kept, err := s.Get(t.Context(), item.ID)
	if err != nil || kept.CreatedBy != member.ID || len(kept.Assignees) != 1 {
		t.Fatalf("removal lost attribution: %+v %v", kept, err)
	}
	_, err = s.Create(t.Context(), admin.ID, CreateItem{Title: "No new assignment", Assignees: []ID{member.ID}})
	requireCode(t, err, "validation")
	if _, err := s.Update(t.Context(), admin.ID, item.ID, UpdateItem{Version: item.Version, RemoveAssignees: []ID{member.ID}}); err != nil {
		t.Fatal(err)
	}
	page, err := s.Users(t.Context(), 0, 10)
	if err != nil || page.Users[1].RemovedAt == 0 {
		t.Fatalf("directory omitted removed identity: %+v %v", page, err)
	}
	invite, err := s.CreateInvite(t.Context(), admin.ID, "member", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rejoined, err := s.ClaimInvite(t.Context(), invite.Token, "Returning Member", member.Email, "new-password")
	if err != nil || rejoined.User.ID != member.ID || rejoined.User.RemovedAt != 0 || rejoined.User.Role != "member" {
		t.Fatalf("rejoin: %+v %v", rejoined.User, err)
	}
	if _, err = s.Authenticate(t.Context(), rejoined.Token); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(t.Context(), session.Token)
	requireCode(t, err, "unauthorized")
	_, err = s.Login(t.Context(), member.Email, testPassword)
	requireCode(t, err, "unauthorized")
	if _, err := s.ChangeUserRole(t.Context(), member.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	invite, err = s.CreateInvite(t.Context(), member.ID, "admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeUserRole(t.Context(), member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Invite(t.Context(), invite.Token)
	requireCode(t, err, "not_found")
}

func TestConcurrentChangesKeepAnAdministrator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admins.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	admin, err := first.Bootstrap(t.Context(), "First", "first@example.test", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	other, err := first.CreateUser(t.Context(), "Second", "second@example.test", "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	wg.Go(func() { <-start; _, results[0] = first.RemoveUser(t.Context(), admin.ID) })
	wg.Go(func() { <-start; _, results[1] = second.ChangeUserRole(t.Context(), other.ID, "member") })
	close(start)
	wg.Wait()
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			requireCode(t, err, "conflict")
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful changes", successes)
	}
	var count int
	if err := first.read.QueryRow("SELECT count(*) FROM users WHERE role='admin' AND removed_at=0").Scan(&count); err != nil || count != 1 {
		t.Fatalf("admins=%d, %v", count, err)
	}
}
