package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"tiki/internal/tiki"
)

func TestUserManagementPermissions(t *testing.T) {
	s, admin := fixture(t)
	member, err := s.CreateUser(t.Context(), "Member", "member@example.test", "member", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	adminSession, err := s.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	memberSession, err := s.Login(t.Context(), member.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s)
	request := func(method, path, token, body string, status int) {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, status, w.Body.String())
		}
	}
	path := "/users/" + member.ID.String()
	request("PATCH", path, memberSession.Token, `{"role":"admin"}`, 403)
	request("DELETE", path, memberSession.Token, "", 403)
	request("PATCH", "/users/"+admin.ID.String(), adminSession.Token, `{"role":"member"}`, 409)
	request("DELETE", "/users/"+admin.ID.String(), adminSession.Token, "", 409)
	request("PATCH", path, adminSession.Token, `{"role":"owner"}`, 400)
	request("PATCH", path, adminSession.Token, `{"role":"viewer","removed_at":0}`, 400)
	request("PATCH", path, adminSession.Token, `{"role":"viewer"}`, 200)
	request("POST", "/items", memberSession.Token, `{"title":"Denied now"}`, 403)
	request("PATCH", path, adminSession.Token, `{"role":"admin"}`, 200)
	request("POST", "/invites", memberSession.Token, `{}`, 200)
	request("DELETE", path, adminSession.Token, "", 200)
	request("GET", "/auth/me", memberSession.Token, "", 401)
	request("PATCH", path, adminSession.Token, `{"role":"member"}`, 409)
	request("DELETE", path, adminSession.Token, "", 200)
	request("PATCH", "/users/999", adminSession.Token, `{"role":"member"}`, 404)
	_, err = s.Login(t.Context(), member.Email, testPassword)
	if api, ok := err.(*tiki.Error); !ok || api.Code != "unauthorized" {
		t.Fatalf("removed user could log in: %v", err)
	}
}
