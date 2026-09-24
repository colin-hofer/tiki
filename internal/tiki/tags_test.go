package tiki

import "testing"

func TestDeleteTagRemovesAllMembershipsWithHistoryAndVersions(t *testing.T) {
	s, admin := fixture(t)
	ctx := t.Context()
	first, err := s.Create(ctx, admin.ID, CreateItem{Title: "Keep this ticket", Tags: []string{"repo/tiki", "keep", "unused"}, Assignees: []ID{admin.ID}})
	if err != nil {
		t.Fatal(err)
	}
	first, err = s.Update(ctx, admin.ID, first.ID, UpdateItem{Version: first.Version, RemoveTags: []string{"unused"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx, admin.ID, CreateItem{Title: "Keep this too", Status: StatusComplete, Tags: []string{"repo/tiki"}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Tags(ctx, "", 200, true)
	if err != nil || page.Usage["repo/tiki"] != 2 || page.Usage["keep"] != 1 || page.Usage["unused"] != 0 {
		t.Fatalf("usage: %+v: %v", page, err)
	}
	before, err := s.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteTag(ctx, 999, "repo/tiki"); err == nil {
		t.Fatal("invalid actor must roll back the deletion")
	}
	kept, err := s.Get(ctx, first.ID)
	if err != nil || kept.Version != first.Version || len(kept.Tags) != 2 {
		t.Fatalf("failed deletion modified ticket: %+v: %v", kept, err)
	}
	count, err := s.DeleteTag(ctx, admin.ID, " REPO/TIKI ")
	if err != nil || count != 2 {
		t.Fatalf("delete: %d: %v", count, err)
	}
	for _, original := range []Item{first, second} {
		current, err := s.Get(ctx, original.ID)
		if err != nil || current.Version != original.Version+1 || current.Title != original.Title || current.Status != original.Status || current.Priority != original.Priority || len(current.Assignees) != len(original.Assignees) {
			t.Fatalf("ticket not preserved/versioned: %+v: %v", current, err)
		}
		for _, tag := range current.Tags {
			if tag != "keep" {
				t.Fatalf("unexpected tag: %s", tag)
			}
		}
		activity, err := s.Activity(ctx, original.ID, 0, 50)
		if err != nil || activity.Activity[len(activity.Activity)-1].Kind != "tag.deleted" {
			t.Fatalf("deletion missing from history: %+v: %v", activity, err)
		}
		_, err = s.Update(ctx, admin.ID, original.ID, UpdateItem{Version: original.Version, AddTags: []string{"repo/tiki"}})
		requireCode(t, err, "conflict")
	}
	if count, err := s.DeleteTag(ctx, admin.ID, "unused"); err != nil || count != 0 {
		t.Fatalf("unused tag: %d: %v", count, err)
	}
	page, err = s.Tags(ctx, "", 200, true)
	if err != nil || len(page.Tags) != 1 || page.Tags[0] != "keep" {
		t.Fatalf("deleted tags in directory: %+v: %v", page, err)
	}
	after, err := s.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	updates, err := s.Updates(ctx, before, after)
	if err != nil || !updates.Reset {
		t.Fatalf("missing live invalidation: %+v: %v", updates, err)
	}
	_, err = s.DeleteTag(ctx, admin.ID, "unused")
	requireCode(t, err, "not_found")
}
