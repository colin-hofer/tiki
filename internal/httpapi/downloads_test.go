package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"tiki/skills"
)

func TestCLIDownloadRoutes(t *testing.T) {
	mux := http.NewServeMux()
	registerDownloads(mux, fstest.MapFS{
		"linux-amd64.gz":     {Data: []byte("binary")},
		"linux-amd64.sha256": {Data: []byte("checksum")},
		"private":            {Data: []byte("not a download")},
	})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/cli", 200},
		{"GET", "/api/v1/cli/install.sh", 200},
		{"HEAD", "/api/v1/cli/install.sh", 200},
		{"GET", "/api/v1/cli/downloads/linux-amd64.gz", 200},
		{"HEAD", "/api/v1/cli/downloads/linux-amd64.gz", 200},
		{"GET", "/api/v1/cli/downloads/linux-amd64.sha256", 200},
		{"GET", "/api/v1/cli/downloads/darwin-arm64.gz", 404},
		{"GET", "/api/v1/cli/downloads/", 404},
		{"GET", "/api/v1/cli/downloads/private", 404},
		{"POST", "/api/v1/cli/install.sh", 405},
		{"GET", "/api/v1/skills/tiki/SKILL.md", 200},
		{"HEAD", "/api/v1/skills/tiki/SKILL.md", 200},
		{"GET", "/api/v1/skills/tiki/SKILL.md.sha256", 200},
		{"POST", "/api/v1/skills/tiki/SKILL.md", 405},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d, want %d", tc.method, tc.path, w.Code, tc.status)
		}
		if tc.method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD included a body")
		}
		if tc.path == "/api/v1/cli" && !strings.Contains(w.Body.String(), `"platforms":["linux-amd64"]`) {
			t.Fatalf("wrong available platforms: %s", w.Body.String())
		}
		if tc.path == "/api/v1/cli/install.sh" && tc.method == "GET" && w.Body.String() != installScript {
			t.Fatal("installer changed in transit")
		}
		if tc.path == "/api/v1/skills/tiki/SKILL.md" && tc.method == "GET" && w.Body.String() != skills.Tiki {
			t.Fatal("skill changed in transit")
		}
		if tc.path == "/api/v1/skills/tiki/SKILL.md.sha256" && w.Body.String() != fmt.Sprintf("%x\n", sha256.Sum256([]byte(skills.Tiki))) {
			t.Fatal("skill checksum does not match the bundled instructions")
		}
	}
}

func TestSkillInstaller(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX installer")
	}
	for _, command := range []string{"curl", "sh"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skip(command + " unavailable")
		}
	}
	for _, mode := range []string{"fresh", "update", "custom-home", "bad-checksum", "missing-download", "linked-directory", "linked-file"} {
		t.Run(mode, func(t *testing.T) {
			mux := http.NewServeMux()
			registerDownloads(mux, fstest.MapFS{}) // Skill installation needs no CLI binaries.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "bad-checksum" && strings.HasSuffix(r.URL.Path, ".sha256") {
					fmt.Fprint(w, strings.Repeat("0", 64))
					return
				}
				if mode == "missing-download" && strings.HasSuffix(r.URL.Path, "SKILL.md") {
					http.NotFound(w, r)
					return
				}
				mux.ServeHTTP(w, r)
			}))
			defer server.Close()
			home := t.TempDir()
			codexHome := ""
			directory := filepath.Join(home, ".codex", "skills", "tiki")
			if mode == "custom-home" {
				codexHome = filepath.Join(home, "custom codex home")
				directory = filepath.Join(codexHome, "skills", "tiki")
			}
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(directory, "SKILL.md")
			if mode != "fresh" {
				if err := os.WriteFile(target, []byte("existing skill"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(mode, "linked-") {
				link := directory
				if mode == "linked-file" {
					link = target
				}
				source := link + ".source"
				if err := os.Rename(link, source); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(source, link); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(t.Context(), "sh", "-s", "--", server.URL, "--skill")
			cmd.Stdin = strings.NewReader(installScript)
			cmd.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME="+codexHome)
			out, err := cmd.CombinedOutput()
			installed, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "fresh" || mode == "update" || mode == "custom-home" {
				if err != nil || string(installed) != skills.Tiki {
					t.Fatalf("skill install: %v\n%s", err, out)
				}
			} else if err == nil || string(installed) != "existing skill" {
				t.Fatalf("failed install changed existing skill: %v\n%s", err, out)
			}
			matches, err := filepath.Glob(filepath.Join(directory, ".tiki-install.*"))
			if err != nil || len(matches) != 0 {
				t.Fatal("temporary skill files were left behind")
			}
			if _, err := os.Stat(filepath.Join(home, ".local", "bin", "tiki")); !os.IsNotExist(err) {
				t.Fatal("skill-only install touched the CLI")
			}
		})
	}
}

func TestInstallerPreservesExistingBinaryOnFailure(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX installer")
	}
	for _, command := range []string{"curl", "gzip", "sh"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skip(command + " unavailable")
		}
	}
	// Stand in for the CLI so the shell flow can be checked without building
	// another executable. A separate smoke check exercises the actual binary.
	binary := []byte("#!/bin/sh\nset -eu\ntest \"$1\" = config\ntest \"$2\" = set-server\nprintf '%s' \"$3\" > \"$HOME/configured-server\"\n")
	var archive bytes.Buffer
	z := gzip.NewWriter(&archive)
	if _, err := z.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x\n", sha256.Sum256(archive.Bytes()))
	for _, mode := range []string{"success", "bad-checksum", "missing-download", "invalid-gzip"} {
		t.Run(mode, func(t *testing.T) {
			platform := runtime.GOOS + "-" + runtime.GOARCH
			files := fstest.MapFS{platform + ".gz": {Data: archive.Bytes()}, platform + ".sha256": {Data: []byte(checksum)}}
			switch mode {
			case "bad-checksum":
				files[platform+".sha256"].Data = []byte(strings.Repeat("0", 64))
			case "missing-download":
				delete(files, platform+".gz")
			case "invalid-gzip":
				files[platform+".gz"].Data = []byte("truncated")
				files[platform+".sha256"].Data = fmt.Appendf(nil, "%x", sha256.Sum256([]byte("truncated")))
			}
			mux := http.NewServeMux()
			registerDownloads(mux, files)
			server := httptest.NewServer(mux)
			defer server.Close()
			home := t.TempDir()
			directory := filepath.Join(home, "bin with spaces")
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(directory, "tiki")
			if err := os.WriteFile(target, []byte("old binary"), 0755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "sh", "-s", "--", server.URL)
			cmd.Stdin = strings.NewReader(installScript)
			cmd.Env = append(os.Environ(), "HOME="+home, "TIKI_INSTALL_DIR="+directory)
			out, err := cmd.CombinedOutput()
			installed, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "success" {
				if err != nil || !bytes.Equal(installed, binary) {
					t.Fatalf("install: %v\n%s", err, out)
				}
				configured, err := os.ReadFile(filepath.Join(home, "configured-server"))
				if err != nil || string(configured) != server.URL {
					t.Fatal("workspace was not configured")
				}
				info, err := os.Stat(target)
				if err != nil || info.Mode().Perm() != 0755 {
					t.Fatal("binary is not executable")
				}
			} else if err == nil || string(installed) != "old binary" {
				t.Fatalf("failed install replaced binary: %v\n%s", err, out)
			}
			matches, err := filepath.Glob(filepath.Join(directory, ".tiki-install.*"))
			if err != nil || len(matches) != 0 {
				t.Fatal("temporary install files were left behind")
			}
		})
	}
}
