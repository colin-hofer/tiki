package tiki

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func cleanTags(tags []string) ([]string, error) {
	if len(tags) > 100 {
		return nil, invalid("at most 100 tags per operation")
	}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if !utf8.ValidString(tag) {
			return nil, invalid("tags must be valid UTF-8")
		}
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || utf8.RuneCountInString(tag) > 64 || strings.ContainsFunc(tag, unicode.IsControl) {
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
