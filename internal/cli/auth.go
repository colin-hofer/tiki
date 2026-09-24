package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"tiki/internal/tiki"
)

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
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", &tiki.Error{Code: "validation", Message: "server must be an HTTP(S) URL without credentials, query, or fragment"}
	}
	if base.Scheme == "http" {
		ip := net.ParseIP(base.Hostname())
		if base.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", &tiki.Error{Code: "validation", Message: "HTTPS is required for remote servers; HTTP is allowed only on loopback"}
		}
	}
	base.Host = strings.ToLower(base.Host)
	return strings.TrimRight(base.String(), "/"), nil
}

func (a *app) password(cmd *cobra.Command, stdin, confirm bool, prompt string) (string, error) {
	if stdin {
		data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1027))
		if err != nil {
			return "", err
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		if len(password) > 1024 {
			return "", &tiki.Error{Code: "validation", Message: "password must be at most 1024 bytes"}
		}
		return password, nil
	}
	input, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(input.Fd())) || a.json {
		message := "password input requires an interactive terminal"
		if cmd.Flags().Lookup("password-stdin") != nil {
			message += "; use --password-stdin for non-interactive commands"
		}
		return "", &tiki.Error{Code: "validation", Message: message}
	}
	read := func(label string) (string, error) {
		fmt.Fprint(a.err, label)
		data, err := term.ReadPassword(int(input.Fd()))
		fmt.Fprintln(a.err)
		return string(data), err
	}
	password, err := read(prompt)
	if err != nil {
		return "", err
	}
	if confirm {
		again, err := read("Confirm password: ")
		if err != nil {
			return "", err
		}
		if password != again {
			return "", &tiki.Error{Code: "validation", Message: "passwords do not match"}
		}
	}
	return password, nil
}

func (a *app) authCommand() *cobra.Command {
	root := &cobra.Command{Use: "auth", Short: "Sign in, inspect your session, change password, or sign out"}
	var email string
	var stdin bool
	login := &cobra.Command{Use: "login", Short: "Sign in with email and password and save the session", Args: cobra.NoArgs}
	login.Flags().StringVar(&email, "email", "", "Email address (required)")
	login.Flags().BoolVar(&stdin, "password-stdin", false, "Read password from stdin instead of prompting")
	login.RunE = func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(email) == "" {
			return &tiki.Error{Code: "validation", Message: "--email is required"}
		}
		password, err := a.password(cmd, stdin, false, "Password: ")
		if err != nil {
			return err
		}
		var session tiki.Session
		if err = a.request(cmd.Context(), "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password}, &session); err != nil {
			return err
		}
		server, err := a.serverURL()
		if err != nil {
			return err
		}
		if err = saveSession(clientSession{Server: server, Session: session}); err != nil {
			return fmt.Errorf("signed in but could not save session: %w", err)
		}
		return a.print(map[string]any{"user": session.User, "server": server, "expires_at": time.Unix(session.ExpiresAt, 0).UTC().Format(time.RFC3339)})
	}
	status := &cobra.Command{Use: "status", Short: "Check the saved session and show the current user", Args: cobra.NoArgs}
	status.RunE = func(cmd *cobra.Command, _ []string) error {
		var user tiki.User
		if err := a.request(cmd.Context(), "GET", "/api/v1/auth/me", nil, &user); err != nil {
			return err
		}
		return a.print(user)
	}
	logout := &cobra.Command{Use: "logout", Short: "Revoke the current session and remove the saved login", Args: cobra.NoArgs}
	logout.RunE = func(cmd *cobra.Command, _ []string) error {
		var out any
		err := a.request(cmd.Context(), "POST", "/api/v1/auth/logout", nil, &out)
		var api *tiki.Error
		if err != nil && (!errors.As(err, &api) || api.Code != "unauthorized") {
			return err
		}
		if err = clearSession(); err != nil {
			return err
		}
		return a.print(map[string]bool{"logged_out": true})
	}
	password := &cobra.Command{Use: "password", Short: "Change your password interactively and revoke all sessions", Args: cobra.NoArgs}
	password.RunE = func(cmd *cobra.Command, _ []string) error {
		current, err := a.password(cmd, false, false, "Current password: ")
		if err != nil {
			return err
		}
		replacement, err := a.password(cmd, false, true, "New password: ")
		if err != nil {
			return err
		}
		var out any
		if err = a.request(cmd.Context(), "POST", "/api/v1/auth/password", map[string]string{"current_password": current, "new_password": replacement}, &out); err != nil {
			return err
		}
		if err = clearSession(); err != nil {
			return err
		}
		return a.print(map[string]string{"message": "Password changed. All sessions revoked; run tiki auth login to sign in again."})
	}
	root.AddCommand(login, status, logout, password)
	return root
}
