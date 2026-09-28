package cli

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tiki/internal/tiki"
)

func TestCLIUpdate(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("updates support Linux and macOS")
	}
	saved := standaloneCLI
	standaloneCLI = "true"
	t.Cleanup(func() { standaloneCLI = saved })
	for _, tc := range []struct {
		name    string
		failure string
	}{
		{"updated", ""}, {"current", ""}, {"symlink", ""}, {"server-override", ""},
		{"bad-checksum", "checksum mismatch"}, {"invalid-checksum", "invalid CLI checksum"},
		{"invalid-gzip", "invalid CLI archive"}, {"truncated-gzip", "cannot unpack CLI"},
		{"empty", "unpacked CLI"}, {"missing", "404"},
		{"checksum-redirect", "307"}, {"download-redirect", "307"},
		{"timeout", "deadline exceeded"}, {"unwritable", "directory must be writable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "unwritable" && os.Geteuid() == 0 {
				t.Skip("root bypasses directory permissions")
			}
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("TIKI_SERVER", "")
			binary := []byte("new CLI binary")
			var archive bytes.Buffer
			z := gzip.NewWriter(&archive)
			if tc.name != "empty" {
				if _, err := z.Write(binary); err != nil {
					t.Fatal(err)
				}
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			data := archive.Bytes()
			if tc.name == "invalid-gzip" {
				data = []byte("invalid archive")
			}
			if tc.name == "truncated-gzip" {
				data = data[:len(data)-4]
			}
			checksum := fmt.Sprintf("%x\n", sha256.Sum256(data))
			if tc.name == "bad-checksum" {
				checksum = strings.Repeat("0", 64)
			}
			if tc.name == "invalid-checksum" {
				checksum = "not a checksum"
			}
			base := "/api/v1/cli/downloads/" + runtime.GOOS + "-" + runtime.GOARCH
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("update sent a session token")
				}
				if tc.name == "unwritable" {
					t.Error("downloaded before checking destination permissions")
				}
				if tc.name == "timeout" {
					<-r.Context().Done()
					return
				}
				switch r.URL.Path {
				case base + ".sha256":
					if tc.name == "checksum-redirect" {
						http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
						return
					}
					fmt.Fprint(w, checksum)
				case base + ".gz":
					if tc.name == "missing" {
						http.NotFound(w, r)
						return
					}
					if tc.name == "download-redirect" {
						http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
						return
					}
					w.Write(data)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			// Expired credentials must neither block updating nor be changed.
			if err := saveSession(clientSession{Server: server.URL, Session: tiki.Session{Token: "expired", ExpiresAt: 1}}); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			a := app{json: true, out: &out, timeout: time.Second}
			if tc.name == "server-override" {
				if err := saveConfig(clientConfig{Server: "https://saved.example.test"}); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TIKI_SERVER", "https://env.example.test")
				a.server = server.URL
			}
			configFile, _ := configPath()
			sessionFile, _ := sessionPath()
			configBefore, _ := os.ReadFile(configFile)
			sessionBefore, _ := os.ReadFile(sessionFile)
			directory := filepath.Join(t.TempDir(), "custom bin")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(directory, "tiki")
			before := []byte("existing CLI")
			if tc.name == "current" {
				before = binary
			}
			if err := os.WriteFile(target, before, 0751); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, 0751); err != nil {
				t.Fatal(err)
			}
			original, _ := os.Stat(target)
			path := target
			if tc.name == "symlink" {
				path = filepath.Join(t.TempDir(), "tiki-link")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "timeout" {
				a.timeout = 30 * time.Millisecond
			}
			if tc.name == "unwritable" {
				if err := os.Chmod(directory, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(directory, 0700) })
			}
			err := a.updateCLI(t.Context(), path)
			got, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			info, _ := os.Stat(target)
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) || !bytes.Equal(got, before) || !os.SameFile(original, info) || out.Len() != 0 {
					t.Fatalf("failed update: err=%v, binary=%q, output=%s", err, got, &out)
				}
			} else {
				var result struct {
					Updated bool   `json:"updated"`
					Path    string `json:"path"`
					Server  string `json:"server"`
				}
				updated := tc.name != "current"
				if err != nil || json.Unmarshal(out.Bytes(), &result) != nil || result.Updated != updated || result.Path != target || result.Server != server.URL || !bytes.Equal(got, binary) {
					t.Fatalf("update: err=%v, binary=%q, output=%s", err, got, &out)
				}
				if os.SameFile(original, info) == updated {
					t.Fatal("update must replace the inode only when contents change")
				}
			}
			if info.Mode().Perm() != 0751 {
				t.Fatal("update changed executable permissions")
			}
			if tc.name == "symlink" {
				if link, err := os.Readlink(path); err != nil || link != target {
					t.Fatal("update replaced the symlink")
				}
			}
			configAfter, _ := os.ReadFile(configFile)
			sessionAfter, _ := os.ReadFile(sessionFile)
			if !bytes.Equal(configBefore, configAfter) || !bytes.Equal(sessionBefore, sessionAfter) {
				t.Fatal("update changed config or credentials")
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 1 {
				t.Fatalf("update left temporary files: %v, %v", entries, err)
			}
		})
	}
}

func TestUpdateCommandGuards(t *testing.T) {
	for _, args := range [][]string{{"update"}, {"update", "extra"}} {
		var out, stderr bytes.Buffer
		code := run(append(args, "--json"), strings.NewReader(""), &out, &stderr)
		if code == 0 || out.Len() != 0 || (len(args) == 1 && !strings.Contains(stderr.String(), "cannot self-update")) || (len(args) == 2 && code != 2) {
			t.Fatalf("%v: exit=%d stdout=%s stderr=%s", args, code, &out, &stderr)
		}
	}
	for _, args := range [][]string{{"update"}, {"update", "--timeout", "2s"}, {"--timeout", "3s", "update"}} {
		a := app{}
		c := a.command()
		c.SetArgs(args)
		if err := c.ExecuteContext(t.Context()); err == nil {
			t.Fatal("server build allowed update")
		}
		want := 5 * time.Minute
		if len(args) > 1 {
			want = 2 * time.Second
			if args[0] == "--timeout" {
				want = 3 * time.Second
			}
		}
		if a.timeout != want {
			t.Fatalf("%v: timeout=%s, want %s", args, a.timeout, want)
		}
	}
}

func TestUpdateDownloadLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 65))
	}))
	defer server.Close()
	if _, err := downloadUpdate(t.Context(), server.URL, 64); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("accepted oversized download: %v", err)
	}
}
