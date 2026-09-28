package tiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
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

const activityColumns = "id,item_id,actor_id,kind,created_at,data,coalesce(client_id,'')"

func scanActivity(row interface{ Scan(...any) error }) (Activity, error) {
	var event Activity
	var data string
	err := row.Scan(&event.ID, &event.ItemID, &event.ActorID, &event.Kind, &event.CreatedAt, &data, &event.ClientID)
	event.Data = json.RawMessage(data)
	return event, err
}

// Default to the newest history page. An explicit after cursor reads forward;
// before loads older history. Both return chronological events with one ID space.
func (s *Store) Activity(ctx context.Context, item, before ID, after *ID, limit int) (ActivityPage, error) {
	out := ActivityPage{Activity: []Activity{}}
	if limit < 1 || limit > MaxPageSize || before < 0 || after != nil && (*after < 0 || before != 0) {
		return out, invalid("invalid activity pagination")
	}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM items WHERE id=?)", item).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, missing()
	}
	query := "SELECT " + activityColumns + " FROM activity WHERE item_id=?"
	args := []any{item}
	if after != nil {
		query += " AND id>? ORDER BY id"
		args = append(args, *after)
	} else {
		if before != 0 {
			query += " AND id<?"
			args = append(args, before)
		}
		query += " ORDER BY id DESC"
	}
	rows, err := tx.QueryContext(ctx, query+" LIMIT ?", append(args, limit+1)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	bytes := 0
	for rows.Next() {
		event, err := scanActivity(rows)
		if err != nil {
			return out, err
		}
		// Descriptions in edit history can be large; bound encoded payloads as
		// well as row count, while always allowing one valid event to progress.
		if len(out.Activity) == limit || len(out.Activity) > 0 && bytes+len(event.Data) > 2<<20 {
			cursor := out.Activity[len(out.Activity)-1].ID
			if after != nil {
				out.NextAfter = cursor
			} else {
				out.NextBefore = cursor
			}
			break
		}
		out.Activity = append(out.Activity, event)
		bytes += len(event.Data)
	}
	if after == nil {
		slices.Reverse(out.Activity)
	}
	return out, rows.Err()
}
