package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"tiki/internal/tiki"
)

type app struct {
	server    string
	json      bool
	timeout   time.Duration
	out       io.Writer
	err       io.Writer
	executing bool
}

func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, os.Stdin, stdout, stderr)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{out: stdout, err: stderr}
	root := a.command()
	a.trackExecution(root)
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var api *tiki.Error
	if !errors.As(err, &api) {
		api = &tiki.Error{Code: "failure", Message: err.Error()}
		if !a.executing {
			api.Code = "validation"
		}
	}
	if a.json {
		_ = json.NewEncoder(stderr).Encode(map[string]any{"error": api})
	} else {
		fmt.Fprintln(stderr, "Error:", terminalText(api.Message))
	}
	switch api.Code {
	case "validation", "too_large", "unsupported_media_type", "method_not_allowed":
		return 2
	case "not_found":
		return 3
	case "conflict", "cursor_expired":
		return 4
	case "unauthorized", "forbidden":
		return 5
	case "transport", "rate_limited", "timeout", "unavailable":
		return 6
	default:
		return 1
	}
}

// Cobra returns ordinary errors for usage failures. Mark entry into RunE so
// those failures remain distinct from I/O errors returned by the command.
func (a *app) trackExecution(cmd *cobra.Command) {
	// Command groups still validate unknown subcommands, after flags such as
	// --json have been parsed. With no arguments they display the usual help.
	if cmd.RunE == nil && cmd.Run == nil {
		cmd.Args = cobra.NoArgs
		cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	}
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			a.executing = true
			return run(cmd, args)
		}
	}
	for _, child := range cmd.Commands() {
		a.trackExecution(child)
	}
}

func (a *app) command() *cobra.Command {
	root := &cobra.Command{Use: "tiki", Short: "Fast shared work tracking for developers and agents", SilenceUsage: true, SilenceErrors: true, Version: "0.1.0-dev"}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &tiki.Error{Code: "validation", Message: err.Error()} })
	root.PersistentFlags().StringVar(&a.server, "server", "", "Server URL (overrides TIKI_SERVER and saved config)")
	root.PersistentFlags().BoolVar(&a.json, "json", false, "Emit JSON without prompts")
	root.PersistentFlags().DurationVar(&a.timeout, "timeout", 10*time.Second, "API request timeout")
	root.AddCommand(a.initCommand(), a.serveCommand(), a.authCommand(), a.configCommand(), a.inviteCommand(), a.userCommand(), a.itemCommand(), a.tagCommand())
	return root
}
