package cli

import (
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"tiki/internal/tiki"
)

func (a *app) tagCommand() *cobra.Command {
	root := &cobra.Command{Use: "tag", Short: "Browse generic workspace tags"}
	var after string
	var limit int
	list := &cobra.Command{Use: "list", Short: "List tags; tags are created when added to items", Args: cobra.NoArgs}
	list.Flags().StringVar(&after, "after", "", "Continue after this tag name")
	list.Flags().IntVar(&limit, "limit", tiki.DefaultPageSize, "Page size, maximum 200")
	list.RunE = func(cmd *cobra.Command, _ []string) error {
		q := url.Values{"limit": {strconv.Itoa(limit)}, "after": {after}}
		return printResponse[tiki.TagPage](a, cmd.Context(), "GET", "/api/v1/tags?"+q.Encode(), nil)
	}
	root.AddCommand(list)
	return root
}
