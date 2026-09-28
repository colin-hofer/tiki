package tiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// DeleteTag removes a tag everywhere in one transaction. Version bumps keep
// drafts based on the old tag membership from silently overwriting this change.
func (s *Store) DeleteTag(ctx context.Context, actor ID, name string) (int64, error) {
	names, err := cleanTags([]string{name})
	if err != nil {
		return 0, err
	}
	name = names[0]
	var count int64
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var id ID
		err := tx.QueryRowContext(ctx, "SELECT id FROM tags WHERE name=?", name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return missing()
		}
		if err != nil {
			return err
		}
		at := now()
		result, err := tx.ExecContext(ctx, "UPDATE items SET version=version+1,updated_at=? WHERE id IN (SELECT item_id FROM item_tags WHERE tag_id=?)", at, id)
		if err != nil {
			return err
		}
		count, err = result.RowsAffected()
		if err != nil {
			return err
		}
		data, err := json.Marshal(map[string]string{"tag": name})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO activity(item_id,actor_id,kind,created_at,data) SELECT item_id,?,'tag.deleted',?,? FROM item_tags WHERE tag_id=?", actor, at, string(data), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM item_tags WHERE tag_id=?", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM tags WHERE id=?", id); err != nil {
			return err
		}
		// Also invalidate the directory when the deleted tag had no tickets.
		return event(ctx, tx, actor, nil, "tag.deleted", map[string]any{"tag": name, "removed_from": count})
	})
	return count, err
}

// Tags lists normalized tag names in lexical order, including unused tags.
func (s *Store) Tags(ctx context.Context, after string, limit int, includeUsage bool) (TagPage, error) {
	out := TagPage{Tags: []string{}}
	if limit < 1 || limit > MaxPageSize {
		return out, invalid("limit must be between 1 and 200")
	}
	countColumn := "0"
	if includeUsage {
		countColumn = "(SELECT count(*) FROM item_tags WHERE tag_id=t.id)"
		out.Usage = make(map[string]int64)
	}
	rows, err := s.read.QueryContext(ctx, "SELECT name,"+countColumn+" FROM tags t WHERE name>? ORDER BY name LIMIT ?", after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var count int64
		if err := rows.Scan(&name, &count); err != nil {
			return out, err
		}
		if len(out.Tags) == limit {
			out.NextAfter = out.Tags[len(out.Tags)-1]
			break
		}
		out.Tags = append(out.Tags, name)
		if includeUsage {
			out.Usage[name] = count
		}
	}
	return out, rows.Err()
}
