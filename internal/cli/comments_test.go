package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"tiki/internal/httpapi"
	"tiki/internal/tiki"
)

func TestCommentCLI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := tiki.Open(filepath.Join(t.TempDir(), "comments.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admin, err := s.Bootstrap(t.Context(), "Admin", "admin@example.test", "testpass")
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateUser(t.Context(), "Member", "member@example.test", "member", "testpass")
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(t.Context(), admin.ID, tiki.CreateItem{Title: "Discuss", Description: "Keep the task focused"})
	if err != nil {
		t.Fatal(err)
	}
	var loseReply atomic.Bool
	var posts atomic.Int64
	handler := httpapi.Handler(s)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/comments") {
			posts.Add(1)
			if loseReply.Swap(false) {
				// Commit the comment, then lose the response before the CLI sees it.
				reply := httptest.NewRecorder()
				handler.ServeHTTP(reply, r)
				if reply.Code != http.StatusOK {
					t.Errorf("comment did not commit: %s", reply.Body.String())
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Setenv("TIKI_SERVER", server.URL)
	login := func(email string) {
		t.Helper()
		session, err := s.Login(t.Context(), email, "testpass")
		if err != nil {
			t.Fatal(err)
		}
		if err := saveSession(clientSession{Server: server.URL, Session: session}); err != nil {
			t.Fatal(err)
		}
	}
	login(member.Email)
	invoke := func(want int, input string, args ...string) []byte {
		t.Helper()
		var out, stderr bytes.Buffer
		code := run(append([]string{"--json"}, args...), strings.NewReader(input), &out, &stderr)
		if code != want || (code == 0 && stderr.Len() != 0) || (code != 0 && out.Len() != 0) {
			t.Fatalf("%v: exit=%d want=%d stdout=%s stderr=%s", args, code, want, &out, &stderr)
		}
		if code != 0 {
			return stderr.Bytes()
		}
		return out.Bytes()
	}
	decode := func(data []byte, body string) tiki.Activity {
		t.Helper()
		var event tiki.Activity
		var content tiki.CreateComment
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(event.Data, &content); err != nil || content.Body != body || event.ID == 0 || event.ItemID == nil || *event.ItemID != item.ID || event.ActorID != member.ID || event.Kind != "comment.created" || event.ClientID == "" {
			t.Fatalf("wrong comment: %+v body=%q err=%v", event, content.Body, err)
		}
		return event
	}
	id := item.ID.String()
	first := decode(invoke(0, "", "item", "comment", id, "--body", "  Ready\nfor review  ", "--client-id", "first"), "Ready\nfor review")
	retry := decode(invoke(0, "", "item", "comment", id, "--body", "Ready\nfor review", "--client-id", "first"), "Ready\nfor review")
	if first.ClientID != "first" || !reflect.DeepEqual(first, retry) {
		t.Fatal("retry did not return the original comment")
	}
	invoke(4, "", "item", "comment", id, "--body", "Changed", "--client-id", "first")
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("  Notes from a file: café\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fromFile := decode(invoke(0, "", "item", "comment", id, "--body-file", file), "Notes from a file: café")
	fromStdin := decode(invoke(0, "Notes from stdin\n", "item", "comment", id, "--body-file", "-"), "Notes from stdin")
	if fromFile.ClientID == fromStdin.ClientID || fromFile.ClientID == "first" {
		t.Fatal("new comments reused a generated client ID")
	}
	maximum := strings.Repeat("é", tiki.MaxCommentBytes/2)
	decode(invoke(0, maximum, "item", "comment", id, "--body-file", "-"), maximum)
	for _, tc := range []struct {
		name  string
		input string
		flags []string
		code  int
	}{
		{"missing-body", "", nil, 2},
		{"both-inputs", "", []string{"--body", "text", "--body-file", "-"}, 2},
		{"blank", "", []string{"--body", " \n "}, 2},
		{"invalid-utf8", "\xff", []string{"--body-file", "-"}, 2},
		{"large-body", "", []string{"--body", maximum + "é"}, 2},
		{"large-stdin", maximum + "é", []string{"--body-file", "-"}, 2},
		{"empty-stdin", "", []string{"--body-file", "-"}, 2},
		{"missing-file", "", []string{"--body-file", file + ".missing"}, 1},
		{"empty-client-id", "", []string{"--body", "text", "--client-id", ""}, 2},
		{"invalid-client-id", "", []string{"--body", "text", "--client-id", "bad key"}, 2},
		{"no-version-flag", "", []string{"--body", "text", "--if-version", "1"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invoke(tc.code, tc.input, append([]string{"item", "comment", id}, tc.flags...)...)
		})
	}
	invoke(2, "", "item", "comment", "0", "--body", "text")
	invoke(3, "", "item", "comment", "999999", "--body", "text")
	// An uncertain request exposes its generated ID and never retries on its own.
	before := posts.Load()
	loseReply.Store(true)
	failed := invoke(6, "", "item", "comment", id, "--body", "Uncertain delivery")
	var failure struct {
		Error    tiki.Error `json:"error"`
		ClientID string     `json:"client_id"`
	}
	if err := json.Unmarshal(failed, &failure); err != nil || failure.Error.Code != "transport" || failure.ClientID == "" || posts.Load() != before+1 {
		t.Fatalf("lost retry ID or retried automatically: %s", failed)
	}
	decode(invoke(0, "", "item", "comment", id, "--body", "Uncertain delivery", "--client-id", failure.ClientID), "Uncertain delivery")
	// Output failures also retain the ID of the already committed comment.
	var stderr bytes.Buffer
	if code := run([]string{"item", "comment", id, "--body", "Output failed", "--client-id", "output-failed"}, strings.NewReader(""), brokenWriter{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "--client-id") || !strings.Contains(stderr.String(), "output-failed") {
		t.Fatalf("output failure lost retry instructions: exit=%d stderr=%s", code, &stderr)
	}
	decode(invoke(0, "", "item", "comment", id, "--body", "Output failed", "--client-id", "output-failed"), "Output failed")
	current, err := s.Get(t.Context(), item.ID)
	if err != nil || !reflect.DeepEqual(current, item) {
		t.Fatalf("comments changed ticket fields/version: %+v err=%v", current, err)
	}
	if _, err := s.CreateUser(t.Context(), "Viewer", "viewer@example.test", "viewer", "testpass"); err != nil {
		t.Fatal(err)
	}
	login("viewer@example.test")
	invoke(5, "", "item", "comment", id, "--body", "Not allowed")
	// Readers can page through every comment; retries add no duplicate events.
	count := 0
	after := "0"
	for {
		var page tiki.ActivityPage
		if err := json.Unmarshal(invoke(0, "", "item", "activity", id, "--after", after, "--limit", "2"), &page); err != nil {
			t.Fatal(err)
		}
		for _, event := range page.Activity {
			if event.Kind == "comment.created" {
				count++
			}
		}
		if page.NextAfter == 0 {
			break
		}
		after = page.NextAfter.String()
	}
	if count != 6 {
		t.Fatalf("wrong number of committed comments: %d, want 6", count)
	}
	if err := clearSession(); err != nil {
		t.Fatal(err)
	}
	invoke(5, "", "item", "comment", id, "--body", "Signed out")
}
