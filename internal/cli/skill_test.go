package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiki/skills"
)

func TestSkillPrint(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := run([]string{"skill"}, strings.NewReader(""), &out, &stderr); code != 0 || out.String() != skills.Instructions {
		t.Fatalf("skill: exit %d, stderr=%s", code, &stderr)
	}
	if !strings.Contains(skills.Tiki, "tiki skill") || !strings.Contains(out.String(), "--if-version") {
		t.Fatal("the installed entry point must load the full CLI instructions")
	}
	out.Reset()
	var result struct{ Name, Content string }
	if code := run([]string{"--json", "skill"}, strings.NewReader(""), &out, &stderr); code != 0 || json.Unmarshal(out.Bytes(), &result) != nil || result.Name != "tiki" || result.Content != skills.Instructions {
		t.Fatalf("skill --json: exit %d, stdout=%s", code, &out)
	}
	out.Reset()
	if run([]string{"--help"}, strings.NewReader(""), &out, &stderr); !strings.Contains(out.String(), "tiki skill") {
		t.Fatal("root help does not point agents to tiki skill")
	}
}

func TestSkillInstall(t *testing.T) {
	for _, mode := range []string{"home", "custom", "same-directory", "project", "worktree", "no-repository"} {
		t.Run(mode, func(t *testing.T) {
			home, repo := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Chdir(repo)
			directory := filepath.Join(home, ".agents", "skills")
			args := []string{"--json", "skill", "install"}
			switch mode {
			case "custom":
				directory = filepath.Join(repo, "other agent", "skills")
				args = append(args, "--dir", filepath.Join("other agent", "skills"))
			case "same-directory":
				args = append(args, "--dir", directory)
			case "project", "worktree", "no-repository":
				directory = filepath.Join(repo, ".agents", "skills")
				args = append(args, "--project")
				if mode != "no-repository" {
					git := filepath.Join(repo, ".git")
					var err error
					if mode == "project" {
						err = os.Mkdir(git, 0755)
					} else {
						err = os.WriteFile(git, []byte("gitdir: elsewhere"), 0644)
					}
					if err != nil {
						t.Fatal(err)
					}
					nested := filepath.Join(repo, "internal", "pkg")
					if err := os.MkdirAll(nested, 0755); err != nil {
						t.Fatal(err)
					}
					t.Chdir(nested)
				}
			}
			path := filepath.Join(directory, "tiki")
			file := filepath.Join(path, "SKILL.md")
			if mode == "same-directory" {
				// Reinstalling at the default destination used to create a self-link.
				if err := os.MkdirAll(path, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("---\nname: tiki\n---\nMy old instructions"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := range 2 {
				var out, stderr bytes.Buffer
				var result struct {
					Path    string `json:"path"`
					Updated bool   `json:"updated"`
				}
				if code := run(args, strings.NewReader(""), &out, &stderr); code != 0 || json.Unmarshal(out.Bytes(), &result) != nil || result.Path != path || result.Updated != (attempt == 0) || stderr.Len() != 0 {
					t.Fatalf("install: exit=%d stdout=%s stderr=%s", code, &out, &stderr)
				}
				content, err := os.ReadFile(file)
				if err != nil || string(content) != skills.Tiki {
					t.Fatalf("installed skill: %q, %v", content, err)
				}
				info, err := os.Lstat(path)
				if err != nil || !info.IsDir() {
					t.Fatalf("skill must be a real directory: %v", err)
				}
				entries, err := os.ReadDir(path)
				if err != nil || len(entries) != 1 {
					t.Fatalf("temporary files left behind: %v, %v", entries, err)
				}
			}
		})
	}
}

func TestSkillInstallErrors(t *testing.T) {
	for _, args := range [][]string{
		{"skill", "extra"},
		{"skill", "install", "extra"},
		{"skill", "install", "--dir", ""},
		{"skill", "install", "--project", "--dir", t.TempDir()},
	} {
		var out, stderr bytes.Buffer
		if code := run(append([]string{"--json"}, args...), strings.NewReader(""), &out, &stderr); code != 2 || out.Len() != 0 || !json.Valid(stderr.Bytes()) {
			t.Fatalf("%v: exit=%d stdout=%s stderr=%s", args, code, &out, &stderr)
		}
	}
	for _, linked := range []string{"directory", "file"} {
		t.Run(linked, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tiki")
			if _, err := writeSkill(dir); err != nil {
				t.Fatal(err)
			}
			link := dir
			if linked == "file" {
				link = filepath.Join(dir, "SKILL.md")
			}
			source := link + ".source"
			if err := os.Rename(link, source); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(source, link); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			if code := run([]string{"--json", "skill", "install", "--dir", filepath.Dir(dir)}, strings.NewReader(""), &out, &stderr); code != 1 || out.Len() != 0 || !json.Valid(stderr.Bytes()) || !strings.Contains(stderr.String(), "symbolic link") {
				t.Fatalf("linked skill: exit=%d stdout=%s stderr=%s", code, &out, &stderr)
			}
			if target, err := os.Readlink(link); err != nil || target != source {
				t.Fatal("changed the link")
			}
		})
	}
}
