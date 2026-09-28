package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"tiki/internal/tiki"
	"tiki/skills"
)

func (a *app) skillCommand() *cobra.Command {
	c := &cobra.Command{
		Use: "skill", Short: "Print the agent instructions for using this CLI", Args: cobra.NoArgs,
		Long: "Print the agent instructions matching this CLI.\nAny agent that can run commands can read them; run `tiki skill` before ticket work.",
		RunE: func(*cobra.Command, []string) error {
			if a.json {
				return a.print(map[string]string{"name": "tiki", "content": skills.Instructions})
			}
			_, err := fmt.Fprint(a.out, skills.Instructions)
			return err
		},
	}
	var project bool
	var directory string
	install := &cobra.Command{
		Use: "install", Short: "Install the Tiki agent skill", Args: cobra.NoArgs,
		Long: "Install a small SKILL.md that tells your agent to run `tiki skill`.\nDefaults to ~/.agents/skills/tiki. Use --project for the repository's .agents/skills,\nor --dir for another skills directory. Existing SKILL.md files are replaced.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("dir") {
				root, err := os.UserHomeDir()
				if project {
					root, err = projectRoot()
				}
				if err != nil {
					return err
				}
				directory = filepath.Join(root, ".agents", "skills")
			}
			if directory == "" {
				return &tiki.Error{Code: "validation", Message: "dir must not be empty"}
			}
			path, err := filepath.Abs(filepath.Join(directory, "tiki"))
			if err != nil {
				return err
			}
			updated, err := writeSkill(path)
			if err != nil {
				return err
			}
			if a.json {
				return a.print(map[string]any{"path": path, "updated": updated})
			}
			verb := "Installed"
			if !updated {
				verb = "Up to date:"
			}
			_, err = fmt.Fprintf(a.out, "%s Tiki skill at %s\nAsk your agent about Tiki tickets; start a new session if the skill does not appear.\n", verb, terminalText(path))
			return err
		},
	}
	install.Flags().BoolVar(&project, "project", false, "Install into the current repository instead of your home directory")
	install.Flags().StringVar(&directory, "dir", "", "Install into this skills directory instead (creates tiki/SKILL.md)")
	install.MarkFlagsMutuallyExclusive("project", "dir")
	c.AddCommand(install)
	return c
}

// writeSkill atomically installs the entry point, leaving linked skills untouched.
func writeSkill(dir string) (bool, error) {
	file := filepath.Join(dir, "SKILL.md")
	for _, p := range []string{dir, file} {
		if info, err := os.Lstat(p); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return false, fmt.Errorf("the Tiki skill at %s is a symbolic link; remove the link before installing", p)
		}
	}
	if current, err := os.ReadFile(file); err == nil && string(current) == skills.Tiki {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(dir, ".tiki-install.*")
	if err != nil {
		return false, err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err := temporary.WriteString(skills.Tiki); err != nil {
		return false, err
	}
	if err := temporary.Chmod(0644); err != nil {
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporary.Name(), file); err != nil {
		return false, err
	}
	return true, nil
}

// projectRoot is the enclosing Git work tree, or the current directory.
func projectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return cwd, nil
		}
	}
}
