package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"tiki/internal/tiki"
)

func TestCommentsPermissionsContractAndLiveDelivery(t *testing.T) {
	s, admin := fixture(t)
	member, err := s.CreateUser(t.Context(), "Member", "commenter@test.example", "member", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.CreateUser(t.Context(), "Viewer", "reader@test.example", "viewer", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	writer, _ := s.Login(t.Context(), member.Email, testPassword)
	reader, _ := s.Login(t.Context(), viewer.Email, testPassword)
	item, err := s.Create(t.Context(), admin.ID, tiki.CreateItem{Title: "Chat"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/items/" + item.ID.String() + "/comments"
	h := Handler(s)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	stream := openEvents(t, server, reader.Token)
	readEvent(t, stream, "ready")
	request := func(method, suffix, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		endpoint := path
		if method == "GET" {
			endpoint = strings.TrimSuffix(path, "/comments") + "/activity"
		}
		r := httptest.NewRequest(method, endpoint+suffix, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, suffix, w.Code, w.Body.String())
		}
		return w
	}
	request("GET", "", "", "", 401)
	request("POST", "", reader.Token, `{"body":"Forbidden","client_id":"first"}`, 403)
	request("POST", "", writer.Token, `{"body":"Spoof","client_id":"first","author_id":"1"}`, 400)
	for _, query := range []string{"?before=1&after=0", "?after=-1", "?after=", "?before=0", "?limit=201"} {
		request("GET", query, reader.Token, "", 400)
	}
	body := `{"body":"Live comment","client_id":"first"}`
	w := request("POST", "", writer.Token, body, 200)
	var comment tiki.Activity
	if err := json.Unmarshal(w.Body.Bytes(), &comment); err != nil || comment.ActorID != member.ID {
		t.Fatalf("author: %+v, %v", comment, err)
	}
	frame := readEvent(t, stream, "change")
	if !strings.Contains(frame, `"activity":[`) || !strings.Contains(frame, `"body":"Live comment"`) || strings.Contains(frame, `"items":`) {
		t.Fatal(frame)
	}
	if retry := request("POST", "", writer.Token, body, 200); retry.Body.String() != w.Body.String() {
		t.Fatal("retry duplicated message")
	}
	request("POST", "", writer.Token, `{"body":"Changed","client_id":"first"}`, 409)
	w = request("GET", "?after=0", reader.Token, "", 200)
	var page tiki.ActivityPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Activity) != 2 || !reflect.DeepEqual(page.Activity[1], comment) {
		t.Fatalf("history: %+v, %v", page, err)
	}
}
