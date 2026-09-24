package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"tiki/internal/tiki"
)

func TestInviteHTTPPermissionsAndClaim(t *testing.T) {
	s, admin := fixture(t)
	adminSession, err := s.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s)
	request := func(method, path, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
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
	request("POST", "/invites", "", `{}`, 401)
	request("POST", "/invites", adminSession.Token, `{"expires_in":9223372036854775807}`, 400)
	w := request("POST", "/invites", adminSession.Token, `{"role":"viewer"}`, 200)
	var invite tiki.Invite
	if err := json.Unmarshal(w.Body.Bytes(), &invite); err != nil || invite.Token == "" {
		t.Fatal("missing invite")
	}
	body, _ := json.Marshal(map[string]string{"token": invite.Token})
	w = request("POST", "/auth/invite", "", string(body), 200)
	if strings.Contains(w.Body.String(), invite.Token) || strings.Contains(w.Body.String(), "created_by") {
		t.Fatal("public invite exposed private metadata")
	}
	// Neither a role nor user ID can be supplied when claiming a link.
	request("POST", "/auth/join", "", `{"token":"`+invite.Token+`","role":"admin"}`, 400)
	claim, _ := json.Marshal(map[string]string{"token": invite.Token, "name": "Invited", "email": "invited@example.test", "password": testPassword})
	w = request("POST", "/auth/join", "", string(claim), 200)
	var session tiki.Session
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil || session.User.Role != "viewer" {
		t.Fatal("wrong invited role")
	}
	request("GET", "/auth/me", session.Token, "", 200)
	request("POST", "/items", session.Token, `{"title":"No"}`, 403)
	request("POST", "/invites", session.Token, `{}`, 403)
	request("GET", "/invites", session.Token, "", 403)
	request("DELETE", "/invites/"+invite.ID.String(), session.Token, "", 403)
	request("POST", "/auth/join", "", string(claim), 404)
	w = request("POST", "/invites", adminSession.Token, `{}`, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &invite); err != nil || invite.Role != "member" {
		t.Fatal("default invite must be member")
	}
	w = request("GET", "/invites", adminSession.Token, "", 200)
	if strings.Contains(w.Body.String(), invite.Token) || strings.Contains(w.Body.String(), `"token"`) {
		t.Fatal("list exposed secret")
	}
	request("DELETE", "/invites/"+invite.ID.String(), adminSession.Token, "", 200)
	request("POST", "/auth/invite", "", `{"token":"`+invite.Token+`"}`, 404)
	request("GET", "/auth/join", "", "", 405)
	request("GET", "/auth/invite", "", "", 405)
	// Public inspection/claims share the bounded authentication limiter.
	for range 5 {
		request("POST", "/auth/join", "", `{}`, 404)
	}
	request("POST", "/auth/join", "", `{}`, 429)
}
