package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"tiki/internal/tiki"
)

func TestSavedServerPrecedenceAndLegacyLogout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TIKI_SERVER", "")
	a := &app{}
	check := func(want string) {
		t.Helper()
		if got, err := a.serverURL(); err != nil || got != want {
			t.Fatalf("server = %q, %v; want %q", got, err, want)
		}
	}
	check("http://127.0.0.1:8080")
	// An old session-only installation remains usable and keeps its server on logout.
	path, _ := sessionPath()
	if err := writeConfig(path, clientSession{Server: "https://old.example.test", Session: tiki.Session{User: tiki.User{ID: 1}}}); err != nil {
		t.Fatal(err)
	}
	check("https://old.example.test")
	if err := clearSession(); err != nil {
		t.Fatal(err)
	}
	check("https://old.example.test")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("logout retained session")
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"config", "set-server", "https://TIKI.example.test/"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	check("https://tiki.example.test")
	path, _ = configPath()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("config is not private")
	}
	data, _ := os.ReadFile(path)
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil || len(config) != 1 {
		t.Fatal("config contains more than server")
	}
	t.Setenv("TIKI_SERVER", "https://env.example.test")
	check("https://env.example.test")
	a.server = "https://flag.example.test/"
	check("https://flag.example.test")
	a.server = ""
	t.Setenv("TIKI_SERVER", "")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.serverURL(); err == nil {
		t.Fatal("silently ignored broken config")
	}
}
