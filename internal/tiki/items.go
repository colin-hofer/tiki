package tiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validStatus(status Status) bool {
	switch status {
	case StatusBacklog, StatusTodo, StatusInProgress, StatusCodeReview, StatusBlocked, StatusComplete, StatusVoid:
		return true
	}
	return false
}

func validateItem(i Item) error {
	if strings.TrimSpace(i.Title) == "" || utf8.RuneCountInString(i.Title) > 300 || !utf8.ValidString(i.Title) || strings.ContainsFunc(i.Title, unicode.IsControl) {
		return invalid("title must contain 1–300 characters without control characters")
	}
	if len(i.Description) > MaxDescriptionBytes || !utf8.ValidString(i.Description) {
		return invalid("description must be UTF-8 and at most 256 KiB")
	}
	if !validStatus(i.Status) {
		return invalid("unknown status")
	}
	if i.Type != ItemTypeBug && i.Type != ItemTypeFeature && i.Type != ItemTypeTask {
		return invalid("type must be bug, feature, or task")
	}
	if math.IsNaN(i.Priority) || math.IsInf(i.Priority, 0) {
		return invalid("priority must be finite")
	}
	return nil
}

func cleanTags(tags []string) ([]string, error) {
	if len(tags) > 100 {
		return nil, invalid("at most 100 tags per operation")
	}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || utf8.RuneCountInString(tag) > 64 || !utf8.ValidString(tag) || strings.ContainsFunc(tag, unicode.IsControl) {
			return nil, invalid("tags must contain 1–64 characters without control characters")
		}
		out = append(out, tag)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func memberships(ctx context.Context, tx *sql.Tx, id ID, addUsers, removeUsers []ID, addTags, removeTags []string) error {
	if len(addUsers)+len(removeUsers)+len(addTags)+len(removeTags) == 0 {
		return nil
	}
	if len(addUsers) > 100 || len(removeUsers) > 100 {
		return invalid("at most 100 assignees per operation")
	}
	for _, userID := range append(slices.Clone(addUsers), removeUsers...) {
		var removed int64
		err := tx.QueryRowContext(ctx, "SELECT removed_at FROM users WHERE id=?", userID).Scan(&removed)
		if errors.Is(err, sql.ErrNoRows) {
			return invalid("assignee does not exist: " + userID.String())
		}
		if err != nil {
			return err
		}
		if removed != 0 && slices.Contains(addUsers, userID) {
			return invalid("assignee has been removed: " + userID.String())
		}
	}
	for _, userID := range addUsers {
		if slices.Contains(removeUsers, userID) {
			return invalid("cannot add and remove the same assignee")
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO item_assignees VALUES(?,?)", id, userID); err != nil {
			return err
		}
	}
	for _, userID := range removeUsers {
		if _, err := tx.ExecContext(ctx, "DELETE FROM item_assignees WHERE item_id=? AND user_id=?", id, userID); err != nil {
			return err
		}
	}
	addTags, err := cleanTags(addTags)
	if err != nil {
		return err
	}
	removeTags, err = cleanTags(removeTags)
	if err != nil {
		return err
	}
	for _, tag := range addTags {
		if slices.Contains(removeTags, tag) {
			return invalid("cannot add and remove the same tag")
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO tags(name) VALUES(?)", tag); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO item_tags SELECT ?,id FROM tags WHERE name=?", id, tag); err != nil {
			return err
		}
	}
	for _, tag := range removeTags {
		if _, err = tx.ExecContext(ctx, "DELETE FROM item_tags WHERE item_id=? AND tag_id=(SELECT id FROM tags WHERE name=?)", id, tag); err != nil {
			return err
		}
	}
	var users, tags int
	if err = tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM item_assignees WHERE item_id=?),(SELECT count(*) FROM item_tags WHERE item_id=?)", id, id).Scan(&users, &tags); err != nil {
		return err
	}
	if users > 100 || tags > 100 {
		return invalid("an item may have at most 100 assignees and 100 tags")
	}
	return nil
}

// Both database/sql.Row and database/sql.Rows can scan the shared projection.
type scanner interface{ Scan(...any) error }

const itemColumns = `i.id,i.type,i.status,i.priority,i.title,i.created_by,i.created_at,i.updated_at,i.version,
 (SELECT json_group_array(CAST(user_id AS TEXT)) FROM (SELECT user_id FROM item_assignees WHERE item_id=i.id ORDER BY user_id)),
 (SELECT json_group_array(name) FROM (SELECT t.name FROM tags t JOIN item_tags it ON it.tag_id=t.id WHERE it.item_id=i.id ORDER BY t.name))`

func scanItem(row scanner) (Item, error) {
	var i Item
	var assignees, tags string
	err := row.Scan(&i.ID, &i.Type, &i.Status, &i.Priority, &i.Title, &i.CreatedBy, &i.CreatedAt, &i.UpdatedAt, &i.Version, &assignees, &tags, &i.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return i, missing()
	}
	if err != nil {
		return i, err
	}
	if err = json.Unmarshal([]byte(assignees), &i.Assignees); err != nil {
		return i, err
	}
	err = json.Unmarshal([]byte(tags), &i.Tags)
	return i, err
}

func getItem(ctx context.Context, tx *sql.Tx, id ID) (Item, error) {
	return scanItem(tx.QueryRowContext(ctx, "SELECT "+itemColumns+",i.description FROM items i WHERE i.id=?", id))
}

func (s *Store) Get(ctx context.Context, id ID) (Item, error) {
	return scanItem(s.read.QueryRowContext(ctx, "SELECT "+itemColumns+",i.description FROM items i WHERE i.id=?", id))
}

// Delete permanently removes an item and its activity, using the same version
// check as edits so a stale client cannot delete someone else's newer work.
func (s *Store) Delete(ctx context.Context, actor, id ID, version int64) error {
	if version < 1 {
		return invalid("version must be positive")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		item, err := getItem(ctx, tx, id)
		if err != nil {
			return err
		}
		if item.Version != version {
			return conflict(item.Version)
		}
		// Insert before removing history to keep the activity revision increasing.
		// A global event makes live clients refresh their board and pagination.
		if err := event(ctx, tx, actor, nil, "item.deleted", map[string]ID{"id": id}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM activity WHERE item_id=?", id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM items WHERE id=?", id)
		return err
	})
}

func (s *Store) Create(ctx context.Context, actor ID, in CreateItem) (Item, error) {
	var out Item
	if in.Type == "" {
		in.Type = ItemTypeTask
	}
	if in.Status == "" {
		in.Status = StatusBacklog
	}
	base := Item{Title: strings.TrimSpace(in.Title), Description: in.Description, Type: in.Type, Status: in.Status}
	if in.Priority != nil {
		base.Priority = *in.Priority
	}
	if err := validateItem(base); err != nil {
		return out, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		priority := base.Priority
		if in.Priority == nil {
			var err error
			priority, err = appendPriority(ctx, tx, actor)
			if err != nil {
				return err
			}
		}
		timestamp := now()
		result, err := tx.ExecContext(ctx, "INSERT INTO items(type,status,priority,title,description,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", base.Type, base.Status, priority, base.Title, base.Description, actor, timestamp, timestamp)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if err = memberships(ctx, tx, ID(id), in.Assignees, nil, in.Tags, nil); err != nil {
			return err
		}
		out, err = getItem(ctx, tx, ID(id))
		if err != nil {
			return err
		}
		return event(ctx, tx, actor, out.ID, "item.created", out)
	})
	return out, err
}

func (s *Store) Update(ctx context.Context, actor, id ID, in UpdateItem) (Item, error) {
	var out Item
	if in.Version <= 0 {
		return out, invalid("positive expected version required")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		i, err := getItem(ctx, tx, id)
		if err != nil {
			return err
		}
		if i.Version != in.Version {
			return conflict(i.Version)
		}
		if in.Title != nil {
			i.Title = strings.TrimSpace(*in.Title)
		}
		if in.Description != nil {
			i.Description = *in.Description
		}
		if in.Type != nil {
			i.Type = *in.Type
		}
		if in.Status != nil {
			i.Status = *in.Status
		}
		if in.Priority != nil {
			i.Priority = *in.Priority
		}
		if err = validateItem(i); err != nil {
			return err
		}
		if err = memberships(ctx, tx, id, in.AddAssignees, in.RemoveAssignees, in.AddTags, in.RemoveTags); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE items SET title=?,description=?,type=?,status=?,priority=?,updated_at=?,version=version+1 WHERE id=?", i.Title, i.Description, i.Type, i.Status, i.Priority, now(), id)
		if err != nil {
			return err
		}
		out, err = getItem(ctx, tx, id)
		if err != nil {
			return err
		}
		return event(ctx, tx, actor, id, "item.updated", map[string]any{"changes": in, "version": out.Version})
	})
	return out, err
}
