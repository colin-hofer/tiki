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
	Items []Item `json:"items,omitempty"`
	Users bool   `json:"users,omitempty"`
	Reset bool   `json:"reset,omitempty"`
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
	// Bound work independently of history size, and coalesce repeated edits.
	rows, err := tx.QueryContext(ctx, "SELECT item_id FROM activity WHERE id>? AND id<=? ORDER BY id LIMIT 65", after.Activity, until.Activity)
	if err != nil {
		return out, err
	}
	var ids []ID
	seen := make(map[ID]bool)
	count := 0
	for rows.Next() {
		var id sql.NullInt64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		count++
		if !id.Valid || count > 64 {
			out.Reset = true
		} else if !seen[ID(id.Int64)] {
			seen[ID(id.Int64)] = true
			ids = append(ids, ID(id.Int64))
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || out.Reset {
		return out, err
	}
	bytes := 0
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
			out.Reset = true
			return out, nil
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
