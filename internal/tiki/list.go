package tiki

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type cursor struct {
	Priority   float64 `json:"p"`
	ID         ID      `json:"id"`
	Generation int64   `json:"g"`
	Filter     string  `json:"f"`
}

func (s *Store) List(ctx context.Context, f Filter) (Page, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback()
	return listItems(ctx, tx, f)
}

func listItems(ctx context.Context, tx *sql.Tx, f Filter) (Page, error) {
	out := Page{Items: []Item{}}
	if f.Limit == 0 {
		f.Limit = DefaultPageSize
	}
	if f.Limit < 1 || f.Limit > MaxPageSize {
		return out, invalid("limit must be between 1 and 200")
	}
	if f.Status != "" && !validStatus(f.Status) {
		return out, invalid("unknown status")
	}
	if f.Assignee < 0 || (f.Assignee != 0 && f.Unassigned) {
		return out, invalid("choose an assignee or unassigned")
	}
	tags, err := cleanTags(f.Tags)
	if err != nil {
		return out, err
	}
	fingerprint, _ := json.Marshal(struct {
		Tags       []string
		Status     Status
		Assignee   ID
		Unassigned bool
	}{tags, f.Status, f.Assignee, f.Unassigned})
	filterHash := sha256.Sum256(fingerprint)
	filterKey := hex.EncodeToString(filterHash[:])
	var c cursor
	if f.Cursor != "" {
		if len(f.Cursor) > 2048 {
			return out, invalid("invalid cursor")
		}
		b, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.ID <= 0 || c.Filter != filterKey {
			return out, invalid("cursor does not match this query")
		}
	}
	out.Items = make([]Item, 0, f.Limit+1)
	var generation int64
	if err := tx.QueryRowContext(ctx, "SELECT generation FROM ordering WHERE id=1").Scan(&generation); err != nil {
		return out, err
	}
	if f.Cursor != "" && c.Generation != generation {
		return out, &Error{Code: "cursor_expired", Message: "priorities were rebalanced; restart pagination"}
	}
	where, args := []string{"1=1"}, []any{}
	if f.Status != "" {
		where = append(where, "i.status=?")
		args = append(args, f.Status)
	}
	if f.Assignee != 0 {
		where = append(where, "EXISTS(SELECT 1 FROM item_assignees a WHERE a.item_id=i.id AND a.user_id=?)")
		args = append(args, f.Assignee)
	}
	if f.Unassigned {
		where = append(where, "NOT EXISTS(SELECT 1 FROM item_assignees a WHERE a.item_id=i.id)")
	}
	for _, tag := range tags {
		where = append(where, "EXISTS(SELECT 1 FROM item_tags it WHERE it.item_id=i.id AND it.tag_id=(SELECT id FROM tags WHERE name=?))")
		args = append(args, tag)
	}
	if f.Cursor != "" {
		where = append(where, "(i.priority,i.id)>(?,?)")
		args = append(args, c.Priority, c.ID)
	}
	args = append(args, f.Limit+1)
	rows, err := tx.QueryContext(ctx, "SELECT "+itemColumns+",'' FROM items i WHERE "+strings.Join(where, " AND ")+" ORDER BY i.priority,i.id LIMIT ?", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		i, err := scanItem(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, i)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > f.Limit {
		out.Items = out.Items[:f.Limit]
		last := out.Items[len(out.Items)-1]
		b, err := json.Marshal(cursor{Priority: last.Priority, ID: last.ID, Generation: generation, Filter: filterKey})
		if err != nil {
			return out, err
		}
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, nil
}

func (s *Store) Tags(ctx context.Context, after string, limit int) (TagPage, error) {
	out := TagPage{Tags: []string{}}
	if limit < 1 || limit > MaxPageSize {
		return out, invalid("limit must be between 1 and 200")
	}
	rows, err := s.read.QueryContext(ctx, "SELECT name FROM tags WHERE name>? ORDER BY name LIMIT ?", after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return out, err
		}
		if len(out.Tags) == limit {
			out.NextAfter = out.Tags[len(out.Tags)-1]
			break
		}
		out.Tags = append(out.Tags, name)
	}
	return out, rows.Err()
}
