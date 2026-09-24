package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"tiki/internal/tiki"
)

func TestProfileUpdatesOnlyAuthenticatedUsersName(t *testing.T) {
	s, admin := fixture(t)
	h := Handler(s)
	for _, role := range []string{"viewer", "member", "admin"} {
		t.Run(role, func(t *testing.T) {
			person, err := s.CreateUser(t.Context(), "Original", role+"@profile.test", role, testPassword)
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.Login(t.Context(), person.Email, testPassword)
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.Revision(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			request := func(token, body string, status int) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest("PATCH", "/api/v1/auth/me", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != status {
					t.Fatalf("got %d, want %d: %s", w.Code, status, w.Body.String())
				}
				return w
			}
			request("", `{"name":"Anonymous"}`, 401)
			for _, body := range []string{`{}`, `{"name":"   "}`, `{"name":"bad\nname"}`, `{"name":"x","role":"admin"}`, `{"name":"x","id":"1"}`, `{"name":"x","email":"other@test.com"}`, `{"name":"` + strings.Repeat("界", 67) + `"}`} {
				request(session.Token, body, 400)
			}
			w := request(session.Token, `{"name":"  New Name  "}`, 200)
			var updated tiki.User
			if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil || updated.ID != person.ID || updated.Name != "New Name" || updated.Role != role || updated.Email != person.Email {
				t.Fatalf("unexpected profile: %+v: %v", updated, err)
			}
			current, err := s.Authenticate(t.Context(), session.Token)
			if err != nil || current.Name != "New Name" {
				t.Fatalf("profile/session did not persist: %+v: %v", current, err)
			}
			after, err := s.Revision(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			updates, err := s.Updates(t.Context(), before, after)
			if err != nil || !updates.Users {
				t.Fatalf("profile did not publish a directory update: %+v: %v", updates, err)
			}
		})
	}
	page, err := s.Users(t.Context(), 0, 200)
	if err != nil || page.Users[0].Name != admin.Name {
		t.Fatal("another user's profile was changed", err)
	}
}
