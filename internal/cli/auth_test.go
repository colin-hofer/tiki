package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tiki/internal/httpapi"
	"tiki/internal/tiki"
)

func TestEmailPasswordCLIFlow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TIKI_SERVER", "")
	const password = "testpass"
	db := filepath.Join(t.TempDir(), "tiki.db")
	var out, stderr bytes.Buffer
	invoke := func(want int, input string, args ...string) string {
		t.Helper()
		out.Reset()
		stderr.Reset()
		code := run(args, strings.NewReader(input), &out, &stderr)
		if code != want {
			t.Fatalf("%v: exit %d, want %d; %s", args, code, want, stderr.String())
		}
		if strings.Contains(out.String(), password) || strings.Contains(stderr.String(), password) {
			t.Fatal("password exposed in output")
		}
		if code != 0 && out.Len() != 0 {
			t.Fatal("error polluted stdout")
		}
		return out.String()
	}
	invoke(0, password+"\n", "init", "--if-needed", "--db", db, "--name", "Admin", "--email", "Admin@Example.test", "--password-stdin", "--json")
	if strings.Contains(out.String(), "session_token") || strings.Contains(out.String(), "password_hash") {
		t.Fatal("init exposed credentials")
	}
	// Repeated setup neither prompts nor replaces existing accounts/passwords.
	var existing tiki.User
	if err := json.Unmarshal([]byte(invoke(0, "", "init", "--if-needed", "--db", db, "--name", "Replacement", "--email", "other@example.test", "--json")), &existing); err != nil || existing.Email != "admin@example.test" {
		t.Fatalf("init changed the account or required a password: %+v, %v", existing, err)
	}
	store, err := tiki.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := httptest.NewServer(httpapi.Handler(store))
	defer server.Close()
	invoke(5, "", "--server", server.URL, "item", "list")
	invoke(2, "", "--server", server.URL, "auth", "login", "--email", "admin@example.test", "--json")
	invoke(5, "wrong password\n", "--server", server.URL, "auth", "login", "--email", "admin@example.test", "--password-stdin")
	invoke(0, password+"\n", "--server", server.URL+"/", "auth", "login", "--email", "admin@example.test", "--password-stdin", "--json")
	if strings.Contains(out.String(), "session_token") {
		t.Fatal("login printed session secret")
	}
	// Later invocations remember both the login and selected server.
	invoke(0, "", "item", "create", "--title", "Logged in", "--json")
	var user tiki.User
	if err = json.Unmarshal([]byte(invoke(0, "", "auth", "status", "--json")), &user); err != nil || user.Email != "admin@example.test" {
		t.Fatal("wrong signed-in identity")
	}
	path, err := sessionPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("session file is not private")
	}
	session, err := loadSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Authenticate(t.Context(), session.Session.Token); err != nil {
		t.Fatal(err)
	}
	invoke(2, "", "--server", "http://127.0.0.1:1", "auth", "status")
	invoke(2, "", "--server", "http://127.0.0.1:1", "auth", "logout")
	if _, err = loadSession(); err != nil {
		t.Fatal("wrong-server logout removed the saved session")
	}
	invoke(0, "", "auth", "logout")
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("logout kept local credentials")
	}
	if _, err = store.Authenticate(t.Context(), session.Session.Token); err == nil {
		t.Fatal("logout did not revoke remote session")
	}
	invoke(5, "", "--server", server.URL, "item", "list")
	invoke(0, password+"\n", "--server", server.URL, "auth", "login", "--email", "admin@example.test", "--password-stdin")
	session, err = loadSession()
	if err != nil {
		t.Fatal(err)
	}
	session.Session.ExpiresAt = time.Now().Add(-time.Second).Unix()
	if err = saveSession(session); err != nil {
		t.Fatal(err)
	}
	invoke(5, "", "auth", "status")
}

func TestClientRejectsInsecureRemoteServer(t *testing.T) {
	for _, address := range []string{"http://example.test", "https://user:secret@example.test", "https://example.test?token=secret"} {
		if _, err := serverURL(address); err == nil {
			t.Fatalf("accepted %s", address)
		}
	}
	for _, address := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080", "https://example.test"} {
		if _, err := serverURL(address); err != nil {
			t.Fatal(err)
		}
	}
}
