package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"tiki/internal/tiki"
)

func TestHTTPPermissionsAndContract(t *testing.T) {
	s, _ := fixture(t)
	viewerUser, err := s.CreateUser(t.Context(), "Viewer", "viewer@example.test", "viewer", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	memberUser, err := s.CreateUser(t.Context(), "Member", "member@example.test", "member", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.Login(t.Context(), viewerUser.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.Login(t.Context(), memberUser.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s)
	request := func(method, path, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	request("GET", "/api/v1/items", "", "", 401)
	request("POST", "/api/v1/items", viewer.Token, `{"title":"forbidden"}`, 403)
	request("POST", "/api/v1/users", member.Token, `{"name":"forbidden"}`, 403)
	w := request("POST", "/api/v1/items", member.Token, `{"title":"Shared","assignees":["1","2"],"tags":["repo/tiki"]}`, 200)
	var item tiki.Item
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.CreatedBy != member.User.ID || len(item.Assignees) != 2 {
		t.Fatalf("bad item: %+v", item)
	}
	path := "/api/v1/items/" + item.ID.String()
	request("GET", path, viewer.Token, "", 200)
	request("PATCH", path, member.Token, `{"version":1,"status":"code_review","add_assignees":["3"]}`, 200)
	request("PATCH", path, member.Token, `{"version":1,"title":"stale"}`, 409)
	request("PATCH", path, member.Token, `{"title":"missing version"}`, 400)
	request("POST", "/api/v1/items", member.Token, `{"title":"unknown","project":"no"}`, 400)
	request("POST", "/api/v1/items", member.Token, `{"title":"extra"}{}`, 400)
	request("POST", "/api/v1/items", member.Token, `{"title":"bad","priority":1e999}`, 400)
	request("GET", "/api/v1/items?limit=0", member.Token, "", 400)
	request("GET", "/api/v1/items/999", member.Token, "", 404)
	request("GET", "/api/v1/items?tag=repo%2Ftiki&assignee=2", viewer.Token, "", 200)
	request("GET", path+"/activity", viewer.Token, "", 200)
	request("DELETE", path, "", `{"version":2}`, 401)
	request("DELETE", path, viewer.Token, `{"version":2}`, 403)
	request("DELETE", path, member.Token, `{}`, 400)
	request("DELETE", path, member.Token, `{"version":0}`, 400)
	request("DELETE", path, member.Token, `{"version":2,"force":true}`, 400)
	request("DELETE", path, member.Token, `{"version":1}`, 409)
	request("GET", path, viewer.Token, "", 200)
	w = request("DELETE", path, member.Token, `{"version":2}`, 200)
	if !strings.Contains(w.Body.String(), `"deleted":true`) {
		t.Fatalf("unexpected delete response: %s", w.Body.String())
	}
	request("GET", path, viewer.Token, "", 404)
	request("GET", path+"/activity", viewer.Token, "", 404)
	request("DELETE", path, member.Token, `{"version":2}`, 404)
	request("POST", "/api/v1/auth/logout", member.Token, "", 200)
	request("GET", path, member.Token, "", 401)
	w = request("GET", "/healthz", "", "", 200)
	if !bytes.Contains(w.Body.Bytes(), []byte("ok")) {
		t.Fatal("bad health response")
	}
}

func TestAuthHTTPLoginLogoutAndThrottle(t *testing.T) {
	s, admin := fixture(t)
	handler := Handler(s)
	request := func(path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	data, _ := json.Marshal(map[string]string{"email": admin.Email, "password": testPassword})
	w := request("/api/v1/auth/login", "", string(data))
	if w.Code != 200 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var session tiki.Session
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil || session.Token == "" {
		t.Fatal("missing login session")
	}
	if strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), testPassword) {
		t.Fatal("login exposed password")
	}
	w = request("/api/v1/users", session.Token, `{"name":"Member","email":"member@example.test","password":"testpass"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "session_token") {
		t.Fatalf("provisioning failed: %d", w.Code)
	}
	w = request("/api/v1/auth/password", session.Token, `{"current_password":"correct horse battery staple","new_password":"a new password here"}`)
	if w.Code != 200 {
		t.Fatalf("password change failed: %d", w.Code)
	}
	w = request("/api/v1/auth/logout", session.Token, "")
	if w.Code != 401 {
		t.Fatal("password change left HTTP session active")
	}
	for range 8 {
		request("/api/v1/auth/login", "", "invalid json")
	}
	w = request("/api/v1/auth/login", "", string(data))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("sign-in not throttled")
	}
}

const testPassword = "correct horse battery staple"

func fixture(t *testing.T) (*tiki.Store, tiki.User) {
	t.Helper()
	s, err := tiki.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	user, err := s.Bootstrap(t.Context(), "Admin", "admin@example.test", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	return s, user
}
