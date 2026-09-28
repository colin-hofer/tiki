package tiki

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Updates carries authoritative items, so live clients need no follow-up GET.
// Reset asks for a new snapshot when ordering changed globally or a consumer
// fell too far behind. No history or database transaction is held by a stream.
type Updates struct {
	Items    []Item     `json:"items,omitempty"`
	Activity []Activity `json:"activity,omitempty"`
	Users    bool       `json:"users,omitempty"`
	Reset    bool       `json:"reset,omitempty"`
}

func (s *Store) Updates(ctx context.Context, after, until Revision) (Updates, error) {
	out := Updates{Users: after.Users != until.Users}
	if until.Activity < after.Activity {
		out.Reset = true
		return out, nil
	}
	if until.Activity == after.Activity {
		return out, nil
	}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	// Preserve all timeline events; only the current ticket snapshots coalesce.
	rows, err := tx.QueryContext(ctx, "SELECT "+activityColumns+" FROM activity WHERE id>? AND id<=? ORDER BY id LIMIT 65", after.Activity, until.Activity)
	if err != nil {
		return out, err
	}
	var ids []ID
	seen := make(map[ID]bool)
	bytes := 0
	for rows.Next() {
		event, err := scanActivity(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		data, err := json.Marshal(event)
		if err != nil {
			rows.Close()
			return out, err
		}
		bytes += len(data)
		if event.ItemID == nil || len(out.Activity) == 64 || bytes > 2<<20 {
			out.Reset = true
			break
		}
		out.Activity = append(out.Activity, event)
		id := *event.ItemID
		if event.Kind != "comment.created" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if out.Reset {
		out.Activity = nil
	}
	if err != nil || out.Reset {
		return out, err
	}
	for _, id := range ids {
		item, err := getItem(ctx, tx, id)
		if err != nil {
			return out, err
		}
		data, err := json.Marshal(item)
		if err != nil {
			return out, err
		}
		bytes += len(data)
		if bytes > 2<<20 {
			out.Items = nil
			out.Activity = nil
			out.Reset = true
			return out, nil
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
