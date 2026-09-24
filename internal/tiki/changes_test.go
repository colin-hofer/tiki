package tiki

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestChangesOnlyWakeAfterCommit(t *testing.T) {
	store, admin := fixture(t)
	revision, err := store.Revision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	changed := store.Changes()
	failure := errors.New("rollback")
	err = store.transaction(t.Context(), func(tx *sql.Tx) error {
		if err := event(t.Context(), tx, admin.ID, nil, "test.rollback", nil); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Fatal("rolled back transaction notified subscribers")
	default:
	}
	if next, err := store.Revision(t.Context()); err != nil || next != revision {
		t.Fatalf("rollback changed revision: %v %v", next, err)
	}
	addItem(t, store, admin.ID, "Committed")
	select {
	case <-changed:
	default:
		t.Fatal("commit did not notify subscriber")
	}
	if next, err := store.Revision(t.Context()); err != nil || next == revision {
		t.Fatalf("commit did not change revision: %v %v", next, err)
	}
	select {
	case <-store.Changes():
		t.Fatal("new subscription is already closed")
	default:
	}
}

func TestUpdatesCoalesceAndBoundPayloads(t *testing.T) {
	store, admin := fixture(t)
	before, err := store.Revision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	item := addItem(t, store, admin.ID, "First")
	title := "Latest"
	item, err = store.Update(t.Context(), admin.ID, item.ID, UpdateItem{Version: item.Version, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.Revision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.Updates(t.Context(), before, after)
	if err != nil || batch.Reset || len(batch.Items) != 1 || batch.Items[0].Version != item.Version || batch.Items[0].Title != title {
		t.Fatalf("expected one current item: %+v %v", batch, err)
	}
	// Exceed the payload budget with valid large descriptions; send one reset,
	// rather than allocating or queueing an unbounded history for a slow tab.
	before = after
	for i := 0; i < 9; i++ {
		if _, err := store.Create(t.Context(), admin.ID, CreateItem{Title: "Large", Description: strings.Repeat("x", MaxDescriptionBytes)}); err != nil {
			t.Fatal(err)
		}
	}
	after, err = store.Revision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch, err = store.Updates(t.Context(), before, after)
	if err != nil || !batch.Reset || len(batch.Items) != 0 {
		t.Fatalf("expected bounded reset: %d items, reset=%v, error=%v", len(batch.Items), batch.Reset, err)
	}
	before = after
	if err := store.transaction(t.Context(), func(tx *sql.Tx) error { return event(t.Context(), tx, admin.ID, nil, "ordering.reset", nil) }); err != nil {
		t.Fatal(err)
	}
	after, err = store.Revision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch, err = store.Updates(t.Context(), before, after)
	if err != nil || !batch.Reset {
		t.Fatalf("ordering reset: %+v %v", batch, err)
	}
}
