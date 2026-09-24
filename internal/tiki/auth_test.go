package tiki

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordStorageAndSessionLifecycle(t *testing.T) {
	s, admin := fixture(t)
	var hash string
	if err := s.write.QueryRow("SELECT password_hash FROM users WHERE id=?", admin.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, passwordPrefix) || strings.Contains(hash, testPassword) {
		t.Fatal("password not hashed")
	}
	for _, email := range []string{admin.Email, "missing@example.test"} {
		_, err := s.Login(t.Context(), email, "wrong password")
		requireCode(t, err, "unauthorized")
		if err.Error() != "invalid email or password" {
			t.Fatal("login discloses whether email exists")
		}
	}
	_, err := s.CreateUser(t.Context(), "Duplicate", " ADMIN@EXAMPLE.TEST ", "member", testPassword)
	requireCode(t, err, "conflict")
	_, err = s.CreateUser(t.Context(), "Weak", "weak@example.test", "member", "1234567")
	requireCode(t, err, "validation")
	first, err := s.Login(t.Context(), " ADMIN@EXAMPLE.TEST ", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token {
		t.Fatal("login reused session")
	}
	if err = s.ChangePassword(t.Context(), admin.ID, "incorrect", "new-pass"); err == nil {
		t.Fatal("changed password without proof")
	}
	if _, err = s.Authenticate(t.Context(), first.Token); err != nil {
		t.Fatal("failed change revoked session")
	}
	err = s.ChangePassword(t.Context(), admin.ID, testPassword, "1234567")
	requireCode(t, err, "validation")
	if err = s.ChangePassword(t.Context(), admin.ID, testPassword, "new-pass"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first.Token, second.Token} {
		_, err := s.Authenticate(t.Context(), token)
		requireCode(t, err, "unauthorized")
	}
	_, err = s.Login(t.Context(), admin.Email, testPassword)
	requireCode(t, err, "unauthorized")
	session, err := s.Login(t.Context(), admin.Email, "new-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.write.Exec("UPDATE sessions SET expires_at=?", time.Now().Add(-time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(t.Context(), session.Token)
	requireCode(t, err, "unauthorized")
}
