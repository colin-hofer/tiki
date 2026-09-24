package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

type clientConfig struct {
	Server string `json:"server"`
}

func configPath() (string, error) {
	path, err := sessionPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "config.json"), nil
}

func loadConfig() (clientConfig, error) {
	var config clientConfig
	path, err := configPath()
	if err != nil {
		return config, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}
	if err = json.Unmarshal(data, &config); err != nil || config.Server == "" {
		return config, fmt.Errorf("cannot read saved config; run tiki config set-server URL")
	}
	return config, nil
}

func saveConfig(config clientConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	return writeConfig(path, config)
}

// Replace config files atomically, with private permissions even if replacing
// an older file that had broader permissions.
func writeConfig(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (a *app) serverURL() (string, error) {
	if a.server != "" {
		return serverURL(a.server)
	}
	if server := os.Getenv("TIKI_SERVER"); server != "" {
		return serverURL(server)
	}
	config, err := loadConfig()
	if err == nil {
		return serverURL(config.Server)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	// Existing installations saved the server only alongside their session.
	if session, err := loadSession(); err == nil {
		return serverURL(session.Server)
	}
	return "http://127.0.0.1:8080", nil
}

func (a *app) configCommand() *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "Save or inspect the default server"}
	set := &cobra.Command{Use: "set-server URL", Short: "Save the default server (kept after logout)", Args: cobra.ExactArgs(1)}
	set.RunE = func(_ *cobra.Command, args []string) error {
		server, err := serverURL(args[0])
		if err != nil {
			return err
		}
		config := clientConfig{Server: server}
		if err = saveConfig(config); err != nil {
			return err
		}
		return a.print(config)
	}
	show := &cobra.Command{Use: "show", Short: "Show the effective server and config file", Args: cobra.NoArgs}
	show.RunE = func(_ *cobra.Command, _ []string) error {
		server, err := a.serverURL()
		if err != nil {
			return err
		}
		path, err := configPath()
		if err != nil {
			return err
		}
		return a.print(map[string]string{"server": server, "path": path})
	}
	root.AddCommand(set, show)
	return root
}
