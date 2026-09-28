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

// Updates reads a bounded batch between revisions. Items reflect one current
// snapshot; a reset replaces batches that are too large or invalidate ordering.
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
	// Return current timeline entries, including replacements for text edits.
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

// Changes closes after a successful write. Subscribe before reading Revision so
// a commit between the read and the wait cannot be missed. Wakeups coalesce and
// never wait for consumers; Revision remains the durable source of truth.
func (s *Store) Changes() <-chan struct{} {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	return s.changed
}

func (s *Store) notify() {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	close(s.changed)
	s.changed = make(chan struct{})
}

// Revision identifies the current item/activity and user-directory state.
// Every item mutation appends activity; user-directory writes increment a
// durable revision so role changes and removals also reach live clients.
type Revision struct{ Activity, Users ID }

// Revision reads the latest durable activity and user-directory revisions.
func (s *Store) Revision(ctx context.Context) (Revision, error) {
	var revision Revision
	err := s.read.QueryRowContext(ctx, `SELECT
		(SELECT coalesce(max(id), 0) FROM activity),
		(SELECT revision FROM user_revision WHERE id=1)`).Scan(&revision.Activity, &revision.Users)
	return revision, err
}
