package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"tiki/internal/tiki"
)

// ids parses user IDs; "me" resolves to the signed-in user with one request.
func (a *app) ids(ctx context.Context, values []string) ([]tiki.ID, error) {
	out := make([]tiki.ID, 0, len(values))
	var me tiki.ID
	for _, value := range values {
		if value == "me" {
			if me == 0 {
				var user tiki.User
				if err := a.request(ctx, "GET", "/api/v1/auth/me", nil, &user); err != nil {
					return nil, err
				}
				me = user.ID
			}
			out = append(out, me)
			continue
		}
		id, err := tiki.ParseID(value)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func bodyFile(path string, r io.Reader, maxBytes int) (string, error) {
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, int64(maxBytes)+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxBytes {
		return "", &tiki.Error{Code: "validation", Message: fmt.Sprintf("body must be at most %d KiB", maxBytes/1024)}
	}
	return string(b), nil
}

func (a *app) itemCommand() *cobra.Command {
	root := &cobra.Command{Use: "item", Short: "Create, assign, tag, and reorder work"}
	root.AddCommand(a.createItem(), a.listItems(), a.updateItem(), a.moveItem(), a.commentItem())
	get := &cobra.Command{Use: "get ID", Short: "Get an item including its description and version", Args: cobra.ExactArgs(1)}
	get.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		return printResponse[tiki.Item](a, cmd.Context(), "GET", "/api/v1/items/"+id.String(), nil)
	}
	var after string
	var limit int
	activity := &cobra.Command{Use: "activity ID", Short: "Read ticket changes and comments, oldest first", Args: cobra.ExactArgs(1)}
	activity.Flags().StringVar(&after, "after", "0", "Continue after activity ID")
	activity.Flags().IntVar(&limit, "limit", tiki.DefaultPageSize, "Page size, maximum 200")
	activity.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		q := url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}
		return printResponse[tiki.ActivityPage](a, cmd.Context(), "GET", "/api/v1/items/"+id.String()+"/activity?"+q.Encode(), nil)
	}
	root.AddCommand(get, activity)
	return root
}

func (a *app) commentItem() *cobra.Command {
	var body, file, clientID string
	c := &cobra.Command{Use: "comment ID", Short: "Post a comment without changing the ticket's version", Args: cobra.ExactArgs(1)}
	f := c.Flags()
	f.StringVar(&body, "body", "", "Plain-text comment, at most 16 KiB")
	f.StringVar(&file, "body-file", "", "Read comment from a file or - for stdin, at most 16 KiB")
	f.StringVar(&clientID, "client-id", "", "Unique message ID (generated if omitted); reuse with the same text when retrying")
	c.MarkFlagsOneRequired("body", "body-file")
	c.MarkFlagsMutuallyExclusive("body", "body-file")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		if f.Changed("body-file") {
			body, err = bodyFile(file, cmd.InOrStdin(), tiki.MaxCommentBytes)
			if err != nil {
				return err
			}
		}
		body = strings.TrimSpace(body)
		if body == "" || len(body) > tiki.MaxCommentBytes || !utf8.ValidString(body) {
			return &tiki.Error{Code: "validation", Message: "comment must contain UTF-8 text and be at most 16 KiB"}
		}
		if !f.Changed("client-id") {
			clientID = rand.Text()
		}
		// Retain the ID through request and output failures: the server may have
		// committed the comment even if the caller never receives its response.
		a.commentClientID = clientID
		in := tiki.CreateComment{Body: body, ClientID: clientID}
		return printResponse[tiki.Activity](a, cmd.Context(), "POST", "/api/v1/items/"+id.String()+"/comments", in)
	}
	return c
}

func (a *app) createItem() *cobra.Command {
	var title, description, file, link, typ, status string
	var tags, assignees []string
	var priority float64
	c := &cobra.Command{Use: "create", Short: "Create an item; defaults to the end of the priority order", Args: cobra.NoArgs}
	f := c.Flags()
	f.StringVar(&title, "title", "", "Item title (required)")
	f.StringVar(&description, "description", "", "Markdown description")
	f.StringVar(&file, "body-file", "", "Read description from a file or - for stdin")
	f.StringVar(&link, "link", "", "External http(s) link, such as a pull request")
	f.StringVar(&typ, "type", "task", "bug, feature, or task")
	f.StringVar(&status, "status", "backlog", "Initial status")
	f.Float64Var(&priority, "priority", 0, "Explicit finite rank; lower appears first")
	f.StringArrayVar(&tags, "tag", nil, "Tag; repeat to add several")
	f.StringArrayVar(&assignees, "assignee", nil, "User ID or me; repeat for multiple assignees")
	c.MarkFlagsMutuallyExclusive("description", "body-file")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		assigned, err := a.ids(cmd.Context(), assignees)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("body-file") {
			description, err = bodyFile(file, cmd.InOrStdin(), tiki.MaxDescriptionBytes)
			if err != nil {
				return err
			}
		}
		in := tiki.CreateItem{Title: title, Description: description, URL: link, Type: tiki.ItemType(typ), Status: tiki.Status(status), Tags: tags, Assignees: assigned}
		if cmd.Flags().Changed("priority") {
			in.Priority = &priority
		}
		return printResponse[tiki.Item](a, cmd.Context(), "POST", "/api/v1/items", in)
	}
	return c
}

func (a *app) listItems() *cobra.Command {
	var tags, queries []string
	var status, assignee, cursor string
	var limit int
	c := &cobra.Command{Use: "list", Short: "List by priority, with optional tag, status, assignee, and search filters", Args: cobra.NoArgs}
	c.Flags().StringArrayVar(&tags, "tag", nil, "Required tag; repeated tags use AND")
	c.Flags().StringVar(&status, "status", "", "Status filter")
	c.Flags().StringArrayVar(&queries, "query", nil, "Search title, description, tags, or ID; all words must match; repeat for OR (max 5 queries, 10 words/300 characters each)")
	c.Flags().StringVar(&assignee, "assignee", "", "User ID, me, or none for unassigned items")
	c.Flags().StringVar(&cursor, "cursor", "", "Next cursor from a previous page")
	c.Flags().IntVar(&limit, "limit", tiki.DefaultPageSize, "Page size, maximum 200")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		q := url.Values{"limit": {strconv.Itoa(limit)}}
		for _, tag := range tags {
			q.Add("tag", tag)
		}
		if status != "" {
			q.Set("status", status)
		}
		if assignee == "me" {
			me, err := a.ids(cmd.Context(), []string{assignee})
			if err != nil {
				return err
			}
			assignee = me[0].String()
		}
		if assignee != "" {
			q.Set("assignee", assignee)
		}
		for _, query := range queries {
			q.Add("query", query)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		return printResponse[tiki.Page](a, cmd.Context(), "GET", "/api/v1/items?"+q.Encode(), nil)
	}
	return c
}

func (a *app) expectedVersion(ctx context.Context, id tiki.ID, explicit bool, version int64) (int64, error) {
	if explicit {
		if version <= 0 {
			return 0, &tiki.Error{Code: "validation", Message: "--if-version must be positive"}
		}
		return version, nil
	}
	var item tiki.Item
	err := a.request(ctx, "GET", "/api/v1/items/"+id.String(), nil, &item)
	return item.Version, err
}

func (a *app) updateItem() *cobra.Command {
	var title, description, file, link, typ, status string
	var addTags, removeTags, addUsers, removeUsers []string
	var priority float64
	var version int64
	c := &cobra.Command{Use: "update ID", Short: "Edit fields and add/remove individual tags or assignees", Args: cobra.ExactArgs(1)}
	f := c.Flags()
	f.StringVar(&title, "title", "", "New title")
	f.StringVar(&description, "description", "", "New Markdown description (empty clears it)")
	f.StringVar(&file, "body-file", "", "Read description from a file or - for stdin")
	f.StringVar(&link, "link", "", "External http(s) link, such as a pull request (empty clears it)")
	f.StringVar(&typ, "type", "", "New type")
	f.StringVar(&status, "status", "", "New status")
	f.Float64Var(&priority, "priority", 0, "Explicit finite rank; prefer item move for relative ordering")
	f.StringArrayVar(&addTags, "add-tag", nil, "Add a tag (repeatable)")
	f.StringArrayVar(&removeTags, "remove-tag", nil, "Remove a tag (repeatable)")
	f.StringArrayVar(&addUsers, "add-assignee", nil, "Add a user ID or me (repeatable)")
	f.StringArrayVar(&removeUsers, "remove-assignee", nil, "Remove a user ID or me (repeatable)")
	f.Int64Var(&version, "if-version", 0, "Expected version; otherwise fetch immediately before editing")
	c.MarkFlagsMutuallyExclusive("description", "body-file")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		in := tiki.UpdateItem{AddTags: addTags, RemoveTags: removeTags}
		if in.AddAssignees, err = a.ids(cmd.Context(), addUsers); err != nil {
			return err
		}
		if in.RemoveAssignees, err = a.ids(cmd.Context(), removeUsers); err != nil {
			return err
		}
		if f.Changed("title") {
			in.Title = &title
		}
		if f.Changed("body-file") {
			description, err = bodyFile(file, cmd.InOrStdin(), tiki.MaxDescriptionBytes)
			if err != nil {
				return err
			}
		}
		if f.Changed("description") || f.Changed("body-file") {
			in.Description = &description
		}
		if f.Changed("link") {
			in.URL = &link
		}
		if f.Changed("type") {
			v := tiki.ItemType(typ)
			in.Type = &v
		}
		if f.Changed("status") {
			v := tiki.Status(status)
			in.Status = &v
		}
		if f.Changed("priority") {
			in.Priority = &priority
		}
		in.Version, err = a.expectedVersion(cmd.Context(), id, f.Changed("if-version"), version)
		if err != nil {
			return err
		}
		return printResponse[tiki.Item](a, cmd.Context(), "PATCH", "/api/v1/items/"+id.String(), in)
	}
	return c
}

func (a *app) moveItem() *cobra.Command {
	var before, after, status string
	var version int64
	c := &cobra.Command{Use: "move ID", Short: "Move before or after another item in workspace priority order", Args: cobra.ExactArgs(1)}
	c.Flags().StringVar(&before, "before", "", "Place immediately before this item ID")
	c.Flags().StringVar(&after, "after", "", "Place immediately after this item ID")
	c.Flags().StringVar(&status, "status", "", "Change status in the same operation")
	c.Flags().Int64Var(&version, "if-version", 0, "Expected version; otherwise fetch immediately before moving")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		if (before == "") == (after == "") {
			return &tiki.Error{Code: "validation", Message: "specify exactly one of --before or --after"}
		}
		in := tiki.MoveItem{}
		if cmd.Flags().Changed("status") {
			value := tiki.Status(status)
			in.Status = &value
		}
		if before != "" {
			in.Before, err = tiki.ParseID(before)
		} else {
			in.After, err = tiki.ParseID(after)
		}
		if err != nil {
			return err
		}
		in.Version, err = a.expectedVersion(cmd.Context(), id, cmd.Flags().Changed("if-version"), version)
		if err != nil {
			return err
		}
		return printResponse[tiki.Item](a, cmd.Context(), "POST", "/api/v1/items/"+id.String()+"/move", in)
	}
	return c
}
