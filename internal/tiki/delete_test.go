package tiki

import "testing"

func TestDeleteRemovesItemAndPublishesReset(t *testing.T) {
	s, admin := fixture(t)
	ctx := t.Context()
	item, err := s.Create(ctx, admin.ID, CreateItem{Title: "Delete me", Assignees: []ID{admin.ID}, Tags: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	changed := s.Changes()
	requireCode(t, s.Delete(ctx, admin.ID, item.ID, 0), "validation")
	requireCode(t, s.Delete(ctx, admin.ID, item.ID, item.Version+1), "conflict")
	// An invalid actor must roll back the deletion and notification together.
	if err := s.Delete(ctx, 999, item.ID, item.Version); err == nil {
		t.Fatal("expected invalid actor to fail")
	}
	if _, err := s.Get(ctx, item.ID); err != nil {
		t.Fatal("failed deletion removed item", err)
	}
	select {
	case <-changed:
		t.Fatal("failed deletion notified subscribers")
	default:
	}
	if err := s.Delete(ctx, admin.ID, item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("deletion did not notify subscribers")
	}
	_, err = s.Get(ctx, item.ID)
	requireCode(t, err, "not_found")
	for _, table := range []string{"activity", "item_tags", "item_assignees"} {
		var count int
		if err := s.read.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE item_id=?", item.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s left %d rows: %v", table, count, err)
		}
	}
	after, err := s.Revision(ctx)
	if err != nil || after.Activity <= before.Activity {
		t.Fatalf("revision did not advance: %v -> %v: %v", before, after, err)
	}
	updates, err := s.Updates(ctx, before, after)
	if err != nil || !updates.Reset {
		t.Fatalf("expected live reset: %+v: %v", updates, err)
	}
	next := addItem(t, s, admin.ID, "Next ticket")
	if next.ID <= item.ID {
		t.Fatal("deleted ticket ID reused")
	}
}
