package tiki

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

type cursor struct {
	Priority   float64 `json:"p"`
	ID         ID      `json:"id"`
	Generation int64   `json:"g"`
	Filter     string  `json:"f"`
}

// List reads a bounded page and its ordering generation from one snapshot.
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
	if f.Status != "" && !f.Status.Valid() {
		return out, invalid("unknown status")
	}
	if f.Assignee < 0 || (f.Assignee != 0 && f.Unassigned) {
		return out, invalid("choose an assignee or unassigned")
	}
	tags, err := cleanTags(f.Tags)
	if err != nil {
		return out, err
	}
	// ponytail: literal substring scans; use FTS5 if search volume needs an index.
	if len(f.Queries) > 5 {
		return out, invalid("at most 5 queries are allowed")
	}
	var queries [][]string
	for _, query := range f.Queries {
		words := strings.Fields(strings.ToLower(query))
		if len(words) > 10 || utf8.RuneCountInString(query) > 300 || !utf8.ValidString(query) || strings.ContainsFunc(query, unicode.IsControl) {
			return out, invalid("each query must be at most 300 characters and 10 words without control characters")
		}
		// Empty alternatives must not turn a search into an unfiltered list.
		if len(words) > 0 {
			queries = append(queries, words)
		}
	}
	fingerprint, _ := json.Marshal(struct {
		Tags       []string
		Status     Status
		Assignee   ID
		Unassigned bool
		Queries    [][]string
	}{tags, f.Status, f.Assignee, f.Unassigned, queries})
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
	where, args := []string{"1=1"}, []any{previewSource}
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
	var alternatives []string
	for _, words := range queries {
		var terms []string
		for _, word := range words {
			terms = append(terms, `(instr(lower(i.title),?)>0 OR instr(lower(i.description),?)>0 OR CAST(i.id AS TEXT)=? OR EXISTS(
				SELECT 1 FROM item_tags it JOIN tags t ON t.id=it.tag_id WHERE it.item_id=i.id AND instr(t.name,?)>0))`)
			args = append(args, word, word, strings.TrimPrefix(strings.TrimPrefix(word, "#"), "tk-"), word)
		}
		alternatives = append(alternatives, "("+strings.Join(terms, " AND ")+")")
	}
	if len(alternatives) > 0 {
		where = append(where, "("+strings.Join(alternatives, " OR ")+")")
	}
	if f.Cursor != "" {
		where = append(where, "(i.priority,i.id)>(?,?)")
		args = append(args, c.Priority, c.ID)
	}
	args = append(args, f.Limit+1)
	rows, err := tx.QueryContext(ctx, "SELECT "+itemColumns+",substr(i.description,1,?) FROM items i WHERE "+strings.Join(where, " AND ")+" ORDER BY i.priority,i.id LIMIT ?", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		i, err := scanItem(rows)
		if err != nil {
			return out, err
		}
		// Lists carry only the excerpt; the full description is read per item.
		i.Description = ""
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
