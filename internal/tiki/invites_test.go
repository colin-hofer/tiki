package tiki

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInviteLifecycle(t *testing.T) {
	s, admin := fixture(t)
	invite, err := s.CreateInvite(t.Context(), admin.ID, "viewer", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := s.read.QueryRow("SELECT hash FROM invites WHERE id=?", invite.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if len(invite.Token) != 43 || hash != hashSession(invite.Token) {
		t.Fatal("invite secret was not hashed")
	}
	inspected, err := s.Invite(t.Context(), invite.Token)
	if err != nil || inspected.Token != "" || inspected.Role != "viewer" {
		t.Fatalf("inspect: %+v, %v", inspected, err)
	}
	page, err := s.Invites(t.Context(), 0, 10)
	if err != nil || len(page.Invites) != 1 {
		t.Fatalf("list: %+v, %v", page, err)
	}
	data, _ := json.Marshal(page)
	if strings.Contains(string(data), invite.Token) || strings.Contains(string(data), hash) {
		t.Fatal("list exposed secret")
	}
	_, err = s.ClaimInvite(t.Context(), invite.Token, "New", "new@example.test", "short")
	requireCode(t, err, "validation")
	_, err = s.ClaimInvite(t.Context(), invite.Token, "Duplicate", admin.Email, testPassword)
	requireCode(t, err, "conflict")
	// Failed claims must roll back consuming the link.
	session, err := s.ClaimInvite(t.Context(), invite.Token, " New Person ", " NEW@EXAMPLE.TEST ", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if session.User.Name != "New Person" || session.User.Email != "new@example.test" || session.User.Role != "viewer" {
		t.Fatalf("bad user: %+v", session.User)
	}
	if user, err := s.Authenticate(t.Context(), session.Token); err != nil || user.ID != session.User.ID {
		t.Fatalf("claim did not sign in: %v", err)
	}
	if _, err = s.Login(t.Context(), session.User.Email, testPassword); err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimInvite(t.Context(), invite.Token, "Again", "again@example.test", testPassword)
	requireCode(t, err, "not_found")
	_, err = s.Invite(t.Context(), invite.Token)
	requireCode(t, err, "not_found")

	for _, mode := range []string{"expired", "revoked"} {
		invite, err := s.CreateInvite(t.Context(), admin.ID, "member", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "expired" {
			_, err = s.write.Exec("UPDATE invites SET expires_at=? WHERE id=?", time.Now().Unix(), invite.ID)
		} else {
			err = s.RevokeInvite(t.Context(), invite.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.ClaimInvite(t.Context(), invite.Token, "Blocked", mode+"@example.test", testPassword)
		requireCode(t, err, "not_found")
	}
	page, err = s.Invites(t.Context(), 0, 10)
	if err != nil || len(page.Invites) != 0 {
		t.Fatalf("inactive invites listed: %+v, %v", page, err)
	}
	_, err = s.CreateInvite(t.Context(), admin.ID, "owner", time.Hour)
	requireCode(t, err, "validation")
	_, err = s.CreateInvite(t.Context(), admin.ID, "member", 31*24*time.Hour)
	requireCode(t, err, "validation")
}

func TestConcurrentInviteClaimsAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	admin, err := first.Bootstrap(t.Context(), "Admin", "admin@example.test", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	invite, err := first.CreateInvite(t.Context(), admin.ID, "member", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i, store := range []*Store{first, second} {
		wg.Go(func() {
			<-start
			_, results[i] = store.ClaimInvite(t.Context(), invite.Token, "Person", []string{"a@example.test", "b@example.test"}[i], testPassword)
		})
	}
	close(start)
	wg.Wait()
	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
		} else {
			requireCode(t, err, "not_found")
		}
	}
	if winners != 1 {
		t.Fatalf("got %d successful claims", winners)
	}
	var users, sessions int
	if err := first.read.QueryRow("SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM sessions)").Scan(&users, &sessions); err != nil {
		t.Fatal(err)
	}
	if users != 2 || sessions != 1 {
		t.Fatalf("partial or duplicate claim: users=%d sessions=%d", users, sessions)
	}
}

func TestInviteMigrationPreservesVersion2Data(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO users(name,email,role,password_hash) VALUES(?,?,?,?)", "Existing", "existing@example.test", "admin", hashPassword(testPassword)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO sessions(user_id,hash,expires_at) VALUES(1,?,?)", hashSession(strings.Repeat("a", 43)), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.Authenticate(t.Context(), strings.Repeat("a", 43))
	if err != nil || user.Name != "Existing" {
		t.Fatalf("migration lost session: %v", err)
	}
	if _, err := s.Login(t.Context(), user.Email, testPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInvite(t.Context(), user.ID, "member", time.Hour); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.read.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatalf("schema version=%d, %v", version, err)
	}
}
