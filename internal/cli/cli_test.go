package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"tiki/internal/httpapi"
	"tiki/internal/tiki"
)

func TestCLIThroughHTTP(t *testing.T) {
	s, err := tiki.Open(filepath.Join(t.TempDir(), "cli.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admin, err := s.Bootstrap(t.Context(), "Admin", "admin@example.test", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.Handler(s))
	defer server.Close()
	t.Setenv("TIKI_SERVER", server.URL)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	invoke := func(code int, args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		actual := run(append([]string{"--json"}, args...), strings.NewReader("correct horse battery staple\n"), &out, &stderr)
		if actual != code {
			t.Fatalf("%v: exit %d, want %d; stdout=%s stderr=%s", args, actual, code, out.String(), stderr.String())
		}
		if code == 0 && stderr.Len() != 0 {
			t.Fatalf("unexpected diagnostics: %s", stderr.String())
		}
		if code != 0 && out.Len() != 0 {
			t.Fatalf("error polluted stdout: %s", out.String())
		}
		return out.String()
	}
	invoke(0, "auth", "login", "--email", admin.Email, "--password-stdin")
	var user tiki.User
	if err = json.Unmarshal([]byte(invoke(0, "user", "create", "--name", "Bob", "--email", "bob@example.test", "--password-stdin")), &user); err != nil {
		t.Fatal(err)
	}
	var first, second tiki.Item
	if err = json.Unmarshal([]byte(invoke(0, "item", "create", "--title", "First", "--assignee", "1", "--assignee", user.ID.String(), "--tag", "Repo/Tiki")), &first); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(invoke(0, "item", "create", "--title", "Second")), &second); err != nil {
		t.Fatal(err)
	}
	invoke(0, "item", "move", second.ID.String(), "--before", first.ID.String(), "--status", "todo", "--if-version", "1")
	var page tiki.Page
	if err = json.Unmarshal([]byte(invoke(0, "item", "list")), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != second.ID || page.Items[0].Status != tiki.StatusTodo || page.Items[0].Version != 2 {
		t.Fatalf("move not reflected: %+v", page)
	}
	invoke(0, "item", "update", first.ID.String(), "--remove-assignee", "1", "--status", "in_progress", "--if-version", "1")
	invoke(4, "item", "update", first.ID.String(), "--title", "stale", "--if-version", "1")
	invoke(0, "item", "get", first.ID.String())
	invoke(0, "item", "activity", first.ID.String())
	invoke(0, "tag", "list")
	invoke(0, "user", "list")
	invoke(2, "item", "create", "--title", "bad float", "--priority", "NaN")
	invoke(2, "item", "move", first.ID.String(), "--before", second.ID.String(), "--after", second.ID.String())
	invoke(3, "item", "get", "999")
	var fromStdin tiki.Item
	if err := json.Unmarshal([]byte(invoke(0, "item", "create", "--title", "Stdin", "--body-file", "-")), &fromStdin); err != nil || fromStdin.Description != "correct horse battery staple\n" {
		t.Fatalf("create ignored supplied stdin: %+v, %v", fromStdin, err)
	}
	invoke(0, "item", "update", fromStdin.ID.String(), "--body-file", "-", "--if-version", "1")
	if err := clearSession(); err != nil {
		t.Fatal(err)
	}
	invoke(5, "item", "list")
	if help := invoke(0, "--help"); !strings.Contains(help, "item") {
		t.Fatal("help missing item command")
	}
}
