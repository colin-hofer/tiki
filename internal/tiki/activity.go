package tiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func event(ctx context.Context, tx *sql.Tx, actor ID, itemID *ID, kind string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO activity(item_id,actor_id,kind,created_at,data) VALUES(?,?,?,?,?)", itemID, actor, kind, now(), string(b))
	return err
}

type itemEdit struct {
	Changes UpdateItem `json:"changes"`
	Version int64      `json:"version"`
	GroupID ID         `json:"group_id,omitempty"`
}

// Only typing coalesces. Discrete changes (status, memberships, ordering, etc.)
// keep their own history, even when included alongside a text edit.
func textEditFields(in UpdateItem) int {
	if in.Status != nil || in.Type != nil || in.Priority != nil || len(in.AddTags)+len(in.RemoveTags)+len(in.AddAssignees)+len(in.RemoveAssignees) != 0 {
		return 0
	}
	fields := 0
	if in.Title != nil {
		fields |= 1
	}
	if in.Description != nil {
		fields |= 2
	}
	if in.URL != nil {
		fields |= 4
	}
	return fields
}

func editEvent(ctx context.Context, tx *sql.Tx, actor, item ID, in UpdateItem, version int64) error {
	data := itemEdit{Changes: in, Version: version}
	var replaced ID
	if fields := textEditFields(in); fields != 0 {
		last, err := scanActivity(tx.QueryRowContext(ctx, "SELECT "+activityColumns+" FROM activity WHERE item_id=? ORDER BY id DESC LIMIT 1", item))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		at, _ := time.Parse(time.RFC3339Nano, last.CreatedAt)
		age := time.Since(at)
		if err == nil && last.Kind == "item.updated" && last.ActorID == actor && age >= 0 && age < 5*time.Minute {
			var previous itemEdit
			if err := json.Unmarshal(last.Data, &previous); err != nil {
				return err
			}
			if textEditFields(previous.Changes) == fields {
				replaced = last.ID
				data.GroupID = previous.GroupID
				if data.GroupID == 0 {
					data.GroupID = last.ID
				}
			}
		}
	}
	// Keep a fresh, increasing ID for SSE and after-cursor catch-up. group_id
	// identifies the visible entry across replacements, including missed saves.
	if err := event(ctx, tx, actor, &item, "item.updated", data); err != nil {
		return err
	}
	if replaced != 0 {
		_, err := tx.ExecContext(ctx, "DELETE FROM activity WHERE id=?", replaced)
		return err
	}
	return nil
}

const activityColumns = "id,item_id,actor_id,kind,created_at,data,coalesce(client_id,'')"

func scanActivity(row scanner) (Activity, error) {
	var event Activity
	var data string
	err := row.Scan(&event.ID, &event.ItemID, &event.ActorID, &event.Kind, &event.CreatedAt, &data, &event.ClientID)
	event.Data = json.RawMessage(data)
	return event, err
}

// Activity defaults to the newest history page. An explicit after cursor reads forward;
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
