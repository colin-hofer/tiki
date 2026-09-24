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
