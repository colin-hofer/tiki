package cli

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"tiki/internal/tiki"
)

func (a *app) userCommand() *cobra.Command {
	root := &cobra.Command{Use: "user", Short: "Manage workspace users"}
	var name, email, role string
	var stdin bool
	create := &cobra.Command{Use: "create", Short: "Create a user with an email and password (admin)", Args: cobra.NoArgs}
	create.Flags().StringVar(&name, "name", "", "Name")
	create.Flags().StringVar(&email, "email", "", "Email (required)")
	create.Flags().BoolVar(&stdin, "password-stdin", false, "Read initial password from stdin instead of prompting")
	create.Flags().StringVar(&role, "role", "member", "admin, member, or viewer")
	create.RunE = func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(email) == "" {
			return &tiki.Error{Code: "validation", Message: "--name and --email are required"}
		}
		password, err := a.password(cmd, stdin, true, "Initial password: ")
		if err != nil {
			return err
		}
		var out tiki.User
		if err := a.request(cmd.Context(), "POST", "/api/v1/users", map[string]string{"name": name, "email": email, "role": role, "password": password}, &out); err != nil {
			return err
		}
		return a.print(out)
	}
	var after string
	var limit int
	list := &cobra.Command{Use: "list", Short: "List users", Args: cobra.NoArgs}
	list.Flags().StringVar(&after, "after", "", "Continue after user ID")
	list.Flags().IntVar(&limit, "limit", tiki.DefaultPageSize, "Page size, maximum 200")
	list.RunE = func(cmd *cobra.Command, _ []string) error {
		var out any
		q := url.Values{"limit": {strconv.Itoa(limit)}, "after": {after}}
		if err := a.request(cmd.Context(), "GET", "/api/v1/users?"+q.Encode(), nil, &out); err != nil {
			return err
		}
		return a.print(out)
	}
	var newRole string
	changeRole := &cobra.Command{Use: "role ID --role ROLE", Short: "Change a user's role (admin)", Args: cobra.ExactArgs(1)}
	changeRole.Flags().StringVar(&newRole, "role", "", "admin, member, or viewer (required)")
	changeRole.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		if newRole == "" {
			return &tiki.Error{Code: "validation", Message: "--role is required"}
		}
		var user tiki.User
		if err := a.request(cmd.Context(), "PATCH", "/api/v1/users/"+id.String(), map[string]string{"role": newRole}, &user); err != nil {
			return err
		}
		return a.print(user)
	}
	remove := &cobra.Command{Use: "remove ID", Short: "Revoke a user's access and sessions, preserving ticket history (admin)", Args: cobra.ExactArgs(1)}
	remove.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		var user tiki.User
		if err := a.request(cmd.Context(), "DELETE", "/api/v1/users/"+id.String(), nil, &user); err != nil {
			return err
		}
		return a.print(user)
	}
	root.AddCommand(create, list, changeRole, remove)
	return root
}

func (a *app) tagCommand() *cobra.Command {
	root := &cobra.Command{Use: "tag", Short: "Browse generic workspace tags"}
	var after string
	var limit int
	list := &cobra.Command{Use: "list", Short: "List tags; tags are created when added to items", Args: cobra.NoArgs}
	list.Flags().StringVar(&after, "after", "", "Continue after this tag name")
	list.Flags().IntVar(&limit, "limit", tiki.DefaultPageSize, "Page size, maximum 200")
	list.RunE = func(cmd *cobra.Command, _ []string) error {
		var out any
		q := url.Values{"limit": {strconv.Itoa(limit)}, "after": {after}}
		if err := a.request(cmd.Context(), "GET", "/api/v1/tags?"+q.Encode(), nil, &out); err != nil {
			return err
		}
		return a.print(out)
	}
	root.AddCommand(list)
	return root
}
