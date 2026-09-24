package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"tiki/internal/tiki"
)

func TestTagDeletionPermissionsAndValidation(t *testing.T) {
	s, admin := fixture(t)
	viewer, err := s.CreateUser(t.Context(), "Viewer", "viewer@tags.test", "viewer", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateUser(t.Context(), "Member", "member@tags.test", "member", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	readSession, err := s.Login(t.Context(), viewer.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	writeSession, err := s.Login(t.Context(), member.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(t.Context(), admin.ID, tiki.CreateItem{Title: "Keep", Tags: []string{"repo/tiki"}})
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s)
	for _, counts := range []bool{false, true} {
		path := "/api/v1/tags"
		if counts {
			path += "?usage=true"
		}
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+readSession.Token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var page tiki.TagPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Tags) != 1 {
			t.Fatalf("tag directory: %s: %v", w.Body.String(), err)
		}
		if (counts && page.Usage["repo/tiki"] != 1) || (!counts && page.Usage != nil) {
			t.Fatalf("unexpected usage counts: %+v", page)
		}
	}
	for _, tc := range []struct {
		token, body string
		status      int
	}{
		{"", `{"name":"repo/tiki"}`, 401},
		{readSession.Token, `{"name":"repo/tiki"}`, 403},
		{writeSession.Token, `{}`, 400},
		{writeSession.Token, `{"name":"bad\nname"}`, 400},
		{writeSession.Token, `{"name":"repo/tiki","force":true}`, 400},
		{writeSession.Token, `{"name":"missing"}`, 404},
		{writeSession.Token, `{"name":"repo/tiki"}`, 200},
	} {
		r := httptest.NewRequest("DELETE", "/api/v1/tags", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body.String())
		}
		if tc.status == 200 && !strings.Contains(w.Body.String(), `"removed_from":1`) {
			t.Fatal("missing affected ticket count", w.Body.String())
		}
	}
	current, err := s.Get(t.Context(), item.ID)
	if err != nil || len(current.Tags) != 0 || current.Title != "Keep" {
		t.Fatalf("ticket was not preserved: %+v: %v", current, err)
	}
}
