package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"tiki/internal/httpapi"
	"tiki/internal/tiki"
)

func (a *app) initCommand() *cobra.Command {
	var path, name, email string
	var stdin, ifNeeded bool
	c := &cobra.Command{Use: "init", Short: "Initialize a database with the first administrator", Args: cobra.NoArgs}
	c.Flags().StringVar(&path, "db", "tiki.db", "SQLite database path")
	c.Flags().StringVar(&name, "name", "", "Administrator name")
	c.Flags().StringVar(&email, "email", "", "Administrator email (required)")
	c.Flags().BoolVar(&stdin, "password-stdin", false, "Read password from stdin instead of prompting")
	c.Flags().BoolVar(&ifNeeded, "if-needed", false, "Reuse an initialized database without prompting or changing accounts")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(email) == "" {
			return &tiki.Error{Code: "validation", Message: "--name and --email are required"}
		}
		s, err := tiki.Open(path)
		if err != nil {
			return err
		}
		defer s.Close()
		if ifNeeded {
			users, err := s.Users(cmd.Context(), 0, 1)
			if err != nil {
				return err
			}
			if len(users.Users) != 0 {
				return a.print(users.Users[0])
			}
		}
		password, err := a.password(cmd, stdin, true, "Password: ")
		if err != nil {
			return err
		}
		out, err := s.Bootstrap(cmd.Context(), name, email, password)
		if err != nil {
			return err
		}
		if err = a.print(out); err != nil {
			return err
		}
		if !a.json && !ifNeeded {
			absolute, _ := filepath.Abs(path)
			fmt.Fprintf(a.err, "\nInitialized database: %s\nStart tiki serve with this same --db path, then run tiki auth login --email with your email address.\n", absolute)
		}
		return nil
	}
	return c
}

func (a *app) serveCommand() *cobra.Command {
	var path, listen string
	c := &cobra.Command{Use: "serve", Short: "Serve the web UI and HTTP API", Args: cobra.NoArgs}
	c.Flags().StringVar(&path, "db", "tiki.db", "SQLite database path")
	c.Flags().StringVar(&listen, "listen", "127.0.0.1:8080", "Listen address; use TLS at your reverse proxy")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		s, err := tiki.Open(path)
		if err != nil {
			return err
		}
		defer s.Close()
		users, err := s.Users(cmd.Context(), 0, 1)
		if err != nil {
			return err
		}
		if len(users.Users) == 0 {
			return &tiki.Error{Code: "validation", Message: "run tiki init for this database first"}
		}
		listener, err := net.Listen("tcp", listen)
		if err != nil {
			return err
		}
		server := &http.Server{Handler: httpapi.Handler(s), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
		server.BaseContext = func(net.Listener) context.Context { return cmd.Context() }
		done := make(chan struct{})
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			select {
			case <-cmd.Context().Done():
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := server.Shutdown(ctx); err != nil {
					_ = server.Close()
				}
			case <-done:
			}
		}()
		fmt.Fprintln(a.err, "Listening on", listener.Addr())
		absolute, _ := filepath.Abs(path)
		fmt.Fprintln(a.err, "Database:", absolute)
		err = server.Serve(listener)
		close(done)
		<-stopped
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	return c
}
