package tiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func event(ctx context.Context, tx *sql.Tx, actor ID, itemID any, kind string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO activity(item_id,actor_id,kind,created_at,data) VALUES(?,?,?,?,?)", itemID, actor, kind, now(), string(b))
	return err
}

func (s *Store) Activity(ctx context.Context, id, after ID, limit int) (ActivityPage, error) {
	out := ActivityPage{Activity: []Activity{}}
	if after < 0 {
		return out, invalid("after must be a positive ID")
	}
	if limit < 1 || limit > MaxPageSize {
		return out, invalid("limit must be between 1 and 200")
	}
	var exists bool
	if err := s.read.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM items WHERE id=?)", id).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, missing()
	}
	rows, err := s.read.QueryContext(ctx, "SELECT id,item_id,actor_id,kind,created_at,data FROM activity WHERE item_id=? AND id>? ORDER BY id LIMIT ?", id, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	dataBytes := 0
	for rows.Next() {
		var a Activity
		var data string
		if err := rows.Scan(&a.ID, &a.ItemID, &a.ActorID, &a.Kind, &a.CreatedAt, &data); err != nil {
			return out, err
		}
		// Bound payload bytes as well as row count; descriptions can dominate history.
		if len(out.Activity) == limit || (len(out.Activity) > 0 && dataBytes+len(data) > 2<<20) {
			out.NextAfter = out.Activity[len(out.Activity)-1].ID
			break
		}
		a.Data = json.RawMessage(data)
		dataBytes += len(data)
		out.Activity = append(out.Activity, a)
	}
	return out, rows.Err()
}
