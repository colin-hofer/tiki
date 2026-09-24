package tiki

import (
	"context"
	"database/sql"
	"testing"
)

func TestBoardPrioritizesActiveWorkAndKeepsCursors(t *testing.T) {
	store, admin := fixture(t)
	for _, status := range []Status{StatusBacklog, StatusTodo, StatusInProgress, StatusCodeReview, StatusBlocked, StatusComplete, StatusVoid} {
		for i := 0; i < 105; i++ {
			if _, err := store.Create(t.Context(), admin.ID, CreateItem{Title: "Ticket", Status: status, Description: "Never in the board response", Tags: []string{"api"}, Assignees: []ID{admin.ID}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	board, err := store.Board(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(board) != 7 {
		t.Fatalf("missing columns: %d", len(board))
	}
	for status, page := range board {
		want := 100
		if status == StatusBacklog || status == StatusComplete || status == StatusVoid {
			want = 20
		}
		if len(page.Items) != want || page.NextCursor == "" {
			t.Fatalf("%s: %d items, cursor=%q", status, len(page.Items), page.NextCursor)
		}
		for _, item := range page.Items {
			if item.Description != "" || item.Status != status {
				t.Fatalf("invalid card: %+v", item)
			}
		}
		next, err := store.List(t.Context(), Filter{Status: status, Limit: 100, Cursor: page.NextCursor})
		if err != nil || len(next.Items) != 105-want || next.Items[0].ID <= page.Items[len(page.Items)-1].ID {
			t.Fatalf("%s continuation: %d items, %v", status, len(next.Items), err)
		}
	}
	focused, err := store.Board(t.Context(), Filter{Status: StatusComplete, Tags: []string{"api"}, Assignee: admin.ID})
	if err != nil || len(focused) != 1 || len(focused[StatusComplete].Items) != 100 {
		t.Fatalf("focused history: %d columns, %v", len(focused), err)
	}
	empty, err := store.Board(t.Context(), Filter{Tags: []string{"missing"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range empty {
		if len(page.Items) != 0 {
			t.Fatal("ignored filter")
		}
	}
	for _, filter := range []Filter{{Status: "unknown"}, {Cursor: "ignored"}, {Limit: 100}, {Assignee: -1}} {
		_, err := store.Board(t.Context(), filter)
		requireCode(t, err, "validation")
	}
}

func TestBoardSnapshotSurvivesConcurrentMoves(t *testing.T) {
	store, admin := fixture(t)
	item := addItem(t, store, admin.ID, "Moving")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		for {
			status := StatusTodo
			if item.Status == StatusTodo {
				status = StatusComplete
			}
			next, err := store.Update(ctx, admin.ID, item.ID, UpdateItem{Version: item.Version, Status: &status})
			if err != nil {
				done <- err
				return
			}
			item = next
		}
	}()
	defer func() { cancel(); <-done }()
	for range 30 {
		board, err := store.Board(t.Context(), Filter{})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, page := range board {
			count += len(page.Items)
		}
		if count != 1 {
			t.Fatalf("inconsistent snapshot: item appeared %d times", count)
		}
	}
}

// Board must release its read connection before the HTTP layer loads metadata,
// including when the pool is limited to one connection.
func TestBoardReleasesReadConnection(t *testing.T) {
	store, _ := fixture(t)
	store.read.SetMaxOpenConns(1)
	if _, err := store.Board(t.Context(), Filter{}); err != nil {
		t.Fatal(err)
	}
	tx, err := store.read.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
}
