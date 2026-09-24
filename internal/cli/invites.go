package cli

import (
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"
	"tiki/internal/tiki"
)

func (a *app) inviteCommand() *cobra.Command {
	root := &cobra.Command{Use: "invite", Short: "Create, list, or revoke single-use invite links (admin)"}
	var role string
	var lifetime time.Duration
	create := &cobra.Command{Use: "create", Short: "Create a link for someone to set up their own account", Args: cobra.NoArgs}
	create.Flags().StringVar(&role, "role", "member", "Role: admin, member, viewer")
	create.Flags().DurationVar(&lifetime, "expires-in", 7*24*time.Hour, "Link lifetime (1m to 720h)")
	create.RunE = func(cmd *cobra.Command, _ []string) error {
		if lifetime < time.Minute || lifetime > 30*24*time.Hour {
			return &tiki.Error{Code: "validation", Message: "invite lifetime must be between one minute and 30 days"}
		}
		var invite tiki.Invite
		if err := a.request(cmd.Context(), "POST", "/api/v1/invites", map[string]any{"role": role, "expires_in": int64(lifetime / time.Second)}, &invite); err != nil {
			return err
		}
		server, err := a.serverURL()
		if err != nil {
			return err
		}
		link := server + "/#invite=" + url.QueryEscape(invite.Token)
		expires := time.Unix(invite.ExpiresAt, 0).UTC().Format(time.RFC3339)
		if a.json {
			return a.print(map[string]any{"id": invite.ID, "url": link, "role": invite.Role, "expires_at": expires})
		}
		fmt.Fprintf(a.err, "Invite %s · %s · expires %s · single use\n", invite.ID, invite.Role, expires)
		_, err = fmt.Fprintln(a.out, link)
		return err
	}
	var after string
	var limit int
	list := &cobra.Command{Use: "list", Short: "List unclaimed, unexpired invites", Args: cobra.NoArgs}
	list.Flags().StringVar(&after, "after", "", "Continue after invite ID")
	list.Flags().IntVar(&limit, "limit", tiki.DefaultPageSize, "Page size (1–200)")
	list.RunE = func(cmd *cobra.Command, _ []string) error {
		query := url.Values{"limit": {fmt.Sprint(limit)}}
		if after != "" {
			query.Set("after", after)
		}
		var page tiki.InvitePage
		if err := a.request(cmd.Context(), "GET", "/api/v1/invites?"+query.Encode(), nil, &page); err != nil {
			return err
		}
		return a.print(page)
	}
	revoke := &cobra.Command{Use: "revoke ID", Short: "Disable an unused invite link", Args: cobra.ExactArgs(1)}
	revoke.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		var out any
		if err = a.request(cmd.Context(), "DELETE", "/api/v1/invites/"+id.String(), nil, &out); err != nil {
			return err
		}
		return a.print(out)
	}
	root.AddCommand(create, list, revoke)
	return root
}
