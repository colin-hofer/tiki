package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"tiki/internal/tiki"
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
	session, err := loadSession()
	if err == nil {
		return serverURL(session.Server)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
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

type clientSession struct {
	Server  string       `json:"server"`
	Session tiki.Session `json:"session"`
}

func sessionPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "tiki", "session.json"), nil
}

func loadSession() (clientSession, error) {
	var session clientSession
	path, err := sessionPath()
	if err != nil {
		return session, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return session, err
	}
	if err = json.Unmarshal(data, &session); err != nil {
		return session, fmt.Errorf("cannot read saved session; run tiki auth login again")
	}
	return session, nil
}

func saveSession(session clientSession) error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err = saveConfig(clientConfig{Server: session.Server}); err != nil {
		return err
	}
	return writeConfig(path, session)
}

func clearSession() error {
	// Preserve the address saved by older versions before removing credentials.
	if _, err := loadConfig(); errors.Is(err, os.ErrNotExist) {
		if session, err := loadSession(); err == nil {
			if err := saveConfig(clientConfig{Server: session.Server}); err != nil {
				return err
			}
		}
	}
	path, err := sessionPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func serverURL(value string) (string, error) {
	base, err := url.Parse(value)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.User != nil || base.ForceQuery || base.RawQuery != "" || base.Fragment != "" {
		return "", &tiki.Error{Code: "validation", Message: "server must be an HTTP(S) URL without credentials, query, or fragment"}
	}
	if base.Scheme == "http" {
		ip := net.ParseIP(base.Hostname())
		if !strings.EqualFold(base.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
			return "", &tiki.Error{Code: "validation", Message: "HTTPS is required for remote servers; HTTP is allowed only on loopback"}
		}
	}
	base.Host = strings.ToLower(base.Host)
	return strings.TrimRight(base.String(), "/"), nil
}
