package cli

import (
	"context"
	"io"
	"net/url"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"tiki/internal/tiki"
)

func ids(values []string) ([]tiki.ID, error) {
	out := make([]tiki.ID, 0, len(values))
	for _, value := range values {
		id, err := tiki.ParseID(value)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func bodyFile(path string, r io.Reader) (string, error) {
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, tiki.MaxDescriptionBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > tiki.MaxDescriptionBytes {
		return "", &tiki.Error{Code: "validation", Message: "description must be at most 256 KiB"}
	}
	return string(b), nil
}

func (a *app) itemCommand() *cobra.Command {
	root := &cobra.Command{Use: "item", Short: "Create, assign, tag, and reorder work"}
	root.AddCommand(a.createItem(), a.listItems(), a.updateItem(), a.moveItem())
	get := &cobra.Command{Use: "get ID", Short: "Get an item including its description and version", Args: cobra.ExactArgs(1)}
	get.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		var out tiki.Item
		if err = a.request(cmd.Context(), "GET", "/api/v1/items/"+id.String(), nil, &out); err != nil {
			return err
		}
		return a.print(out)
	}
	var after string
	var limit int
	activity := &cobra.Command{Use: "activity ID", Short: "Read the item's durable change history", Args: cobra.ExactArgs(1)}
	activity.Flags().StringVar(&after, "after", "", "Continue after activity ID")
	activity.Flags().IntVar(&limit, "limit", tiki.DefaultPageSize, "Page size, maximum 200")
	activity.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		q := url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}
		var out any
		if err = a.request(cmd.Context(), "GET", "/api/v1/items/"+id.String()+"/activity?"+q.Encode(), nil, &out); err != nil {
			return err
		}
		return a.print(out)
	}
	root.AddCommand(get, activity)
	return root
}

func (a *app) createItem() *cobra.Command {
	var title, description, file, typ, status string
	var tags, assignees []string
	var priority float64
	c := &cobra.Command{Use: "create", Short: "Create an item; defaults to the end of the priority order", Args: cobra.NoArgs}
	f := c.Flags()
	f.StringVar(&title, "title", "", "Item title (required)")
	f.StringVar(&description, "description", "", "Markdown description")
	f.StringVar(&file, "body-file", "", "Read description from a file or - for stdin")
	f.StringVar(&typ, "type", "task", "bug, feature, or task")
	f.StringVar(&status, "status", "backlog", "Initial status")
	f.Float64Var(&priority, "priority", 0, "Explicit finite rank; lower appears first")
	f.StringArrayVar(&tags, "tag", nil, "Tag; repeat to add several")
	f.StringArrayVar(&assignees, "assignee", nil, "User ID; repeat for multiple assignees")
	c.MarkFlagsMutuallyExclusive("description", "body-file")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		assigned, err := ids(assignees)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("body-file") {
			description, err = bodyFile(file, cmd.InOrStdin())
			if err != nil {
				return err
			}
		}
		in := tiki.CreateItem{Title: title, Description: description, Type: tiki.Type(typ), Status: tiki.Status(status), Tags: tags, Assignees: assigned}
		if cmd.Flags().Changed("priority") {
			in.Priority = &priority
		}
		var out tiki.Item
		if err = a.request(cmd.Context(), "POST", "/api/v1/items", in, &out); err != nil {
			return err
		}
		return a.print(out)
	}
	return c
}

func (a *app) listItems() *cobra.Command {
	var tags []string
	var status, assignee, cursor string
	var limit int
	c := &cobra.Command{Use: "list", Short: "List by priority, with optional tag and assignee filters", Args: cobra.NoArgs}
	c.Flags().StringArrayVar(&tags, "tag", nil, "Required tag; repeated tags use AND")
	c.Flags().StringVar(&status, "status", "", "Status filter")
	c.Flags().StringVar(&assignee, "assignee", "", "User ID, or none for unassigned items")
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
		if assignee != "" {
			q.Set("assignee", assignee)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var out tiki.Page
		if err := a.request(cmd.Context(), "GET", "/api/v1/items?"+q.Encode(), nil, &out); err != nil {
			return err
		}
		return a.print(out)
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
	var title, description, file, typ, status string
	var addTags, removeTags, addUsers, removeUsers []string
	var priority float64
	var version int64
	c := &cobra.Command{Use: "update ID", Short: "Edit fields and add/remove individual tags or assignees", Args: cobra.ExactArgs(1)}
	f := c.Flags()
	f.StringVar(&title, "title", "", "New title")
	f.StringVar(&description, "description", "", "New Markdown description (empty clears it)")
	f.StringVar(&file, "body-file", "", "Read description from a file or - for stdin")
	f.StringVar(&typ, "type", "", "New type")
	f.StringVar(&status, "status", "", "New status")
	f.Float64Var(&priority, "priority", 0, "Explicit finite rank; prefer item move for relative ordering")
	f.StringArrayVar(&addTags, "add-tag", nil, "Add a tag (repeatable)")
	f.StringArrayVar(&removeTags, "remove-tag", nil, "Remove a tag (repeatable)")
	f.StringArrayVar(&addUsers, "add-assignee", nil, "Add a user ID (repeatable)")
	f.StringArrayVar(&removeUsers, "remove-assignee", nil, "Remove a user ID (repeatable)")
	f.Int64Var(&version, "if-version", 0, "Expected version; otherwise fetch immediately before editing")
	c.MarkFlagsMutuallyExclusive("description", "body-file")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := tiki.ParseID(args[0])
		if err != nil {
			return err
		}
		in := tiki.UpdateItem{AddTags: addTags, RemoveTags: removeTags}
		if in.AddAssignees, err = ids(addUsers); err != nil {
			return err
		}
		if in.RemoveAssignees, err = ids(removeUsers); err != nil {
			return err
		}
		if f.Changed("title") {
			in.Title = &title
		}
		if f.Changed("body-file") {
			description, err = bodyFile(file, cmd.InOrStdin())
			if err != nil {
				return err
			}
		}
		if f.Changed("description") || f.Changed("body-file") {
			in.Description = &description
		}
		if f.Changed("type") {
			v := tiki.Type(typ)
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
		var out tiki.Item
		if err = a.request(cmd.Context(), "PATCH", "/api/v1/items/"+id.String(), in, &out); err != nil {
			return err
		}
		return a.print(out)
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
		var out tiki.Item
		if err = a.request(cmd.Context(), "POST", "/api/v1/items/"+id.String()+"/move", in, &out); err != nil {
			return err
		}
		return a.print(out)
	}
	return c
}
