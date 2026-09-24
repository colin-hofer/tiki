package tiki

import (
	"context"
	"database/sql"
)

// Board reads all visible columns from one snapshot. Per-column limits keep a
// large backlog or completed history from displacing active work. The existing
// status/priority index bounds each lookup; no full-workspace sort is needed.
func (s *Store) Board(ctx context.Context, f Filter) (map[Status]Page, error) {
	if f.Cursor != "" || f.Limit != 0 {
		return nil, invalid("board pagination uses the item list endpoint")
	}
	if f.Status != "" && !validStatus(f.Status) {
		return nil, invalid("unknown status")
	}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	columns := make(map[Status]Page, 7)
	for _, status := range []Status{StatusBacklog, StatusTodo, StatusInProgress, StatusCodeReview, StatusBlocked, StatusComplete, StatusVoid} {
		if f.Status != "" && f.Status != status {
			continue
		}
		column := f
		column.Status, column.Limit = status, 100
		if f.Status == "" && (status == StatusBacklog || status == StatusComplete || status == StatusVoid) {
			column.Limit = 20
		}
		page, err := listItems(ctx, tx, column)
		if err != nil {
			return nil, err
		}
		columns[status] = page
	}
	return columns, nil
}
