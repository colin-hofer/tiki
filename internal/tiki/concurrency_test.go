package tiki

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReadersAndWriterDoNotBlockEachOther(t *testing.T) {
	s, admin := fixture(t)
	item := addItem(t, s, admin.ID, "Committed")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	write, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer write.Rollback()
	if _, err = write.ExecContext(ctx, "UPDATE items SET title='Uncommitted' WHERE id=?", item.ID); err != nil {
		t.Fatal(err)
	}
	page, err := s.List(ctx, Filter{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "Committed" {
		t.Fatalf("reader blocked or saw uncommitted data: %+v, %v", page, err)
	}
	if err = write.Rollback(); err != nil {
		t.Fatal(err)
	}

	read, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	var title string
	if err = read.QueryRowContext(ctx, "SELECT title FROM items WHERE id=?", item.ID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	updated := "Updated"
	if _, err = s.Update(ctx, admin.ID, item.ID, UpdateItem{Version: item.Version, Title: &updated}); err != nil {
		t.Fatalf("reader blocked writer: %v", err)
	}
	if err = read.QueryRowContext(ctx, "SELECT title FROM items WHERE id=?", item.ID).Scan(&title); err != nil || title != "Committed" {
		t.Fatalf("read transaction lost its snapshot: %q, %v", title, err)
	}
	got, err := s.Get(ctx, item.ID)
	if err != nil || got.Title != updated {
		t.Fatalf("fresh reader missed commit: %+v, %v", got, err)
	}
}

func TestReadPoolConnectionsAreReadOnly(t *testing.T) {
	s, _ := fixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	// Hold every connection to force creation and validate settings on each one.
	for range s.read.Stats().MaxOpenConnections {
		conn, err := s.read.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var enabled int
		for _, pragma := range []string{"PRAGMA foreign_keys", "PRAGMA query_only"} {
			if err := conn.QueryRowContext(ctx, pragma).Scan(&enabled); err != nil || enabled != 1 {
				t.Fatalf("%s: %d, %v", pragma, enabled, err)
			}
		}
		if _, err := conn.ExecContext(ctx, "DELETE FROM items"); err == nil {
			t.Fatal("reader accepted a write")
		}
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := s.List(canceled, Filter{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued read ignored cancellation: %v", err)
	}
}

func TestConcurrentStoresHaveOneEditWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	admin, err := first.Bootstrap(t.Context(), "Admin", "admin@example.test", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	item := addItem(t, first, admin.ID, "Shared")
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*Store{first, second} {
		wg.Go(func() {
			title := "Edited"
			_, err := store.Update(t.Context(), admin.ID, item.ID, UpdateItem{Version: item.Version, Title: &title})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			requireCode(t, err, "conflict")
		}
	}
	if winners != 1 {
		t.Fatalf("got %d successful edits", winners)
	}
	page, err := first.Activity(t.Context(), item.ID, 0, 50)
	if err != nil || len(page.Activity) != 2 {
		t.Fatalf("non-atomic history: %+v, %v", page, err)
	}
}

func TestDirectoryPaginationHasNoPhantomPage(t *testing.T) {
	s, admin := fixture(t)
	if _, err := s.Create(t.Context(), admin.ID, CreateItem{Title: "Tagged", Tags: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	users, err := s.Users(t.Context(), 0, 1)
	if err != nil || len(users.Users) != 1 || users.NextAfter != 0 {
		t.Fatalf("users: %+v, %v", users, err)
	}
	tags, err := s.Tags(t.Context(), "", 1, false)
	if err != nil || len(tags.Tags) != 1 || tags.NextAfter != "a" {
		t.Fatalf("tags: %+v, %v", tags, err)
	}
	tags, err = s.Tags(t.Context(), tags.NextAfter, 1, false)
	if err != nil || len(tags.Tags) != 1 || tags.Tags[0] != "b" || tags.NextAfter != "" {
		t.Fatalf("final tags: %+v, %v", tags, err)
	}
}
