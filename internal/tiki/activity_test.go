package tiki

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestTextEditsCoalesceAndCatchUp(t *testing.T) {
	s, user := fixture(t)
	item := addItem(t, s, user.ID, "Typing")
	var first, cursor ID
	var revision Revision
	for n := range 10 {
		description := fmt.Sprintf("Draft %d", n)
		var err error
		item, err = s.Update(t.Context(), user.ID, item.ID, UpdateItem{Version: item.Version, Description: &description})
		if err != nil {
			t.Fatal(err)
		}
		page, err := s.Activity(t.Context(), item.ID, 0, nil, 50)
		if err != nil || len(page.Activity) != 2 {
			t.Fatalf("typing should store one edit: %+v, %v", page, err)
		}
		latest := page.Activity[1]
		var data itemEdit
		if err := json.Unmarshal(latest.Data, &data); err != nil {
			t.Fatal(err)
		}
		if latest.ID <= cursor || data.Version != item.Version || data.Changes.Description == nil || *data.Changes.Description != description {
			t.Fatalf("edit did not advance to latest value: %+v", latest)
		}
		if n == 0 {
			first = latest.ID
		} else if data.GroupID != first {
			t.Fatalf("group changed across missed edits: got %v, want %v", data.GroupID, first)
		}
		next, err := s.Revision(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			updates, err := s.Updates(t.Context(), revision, next)
			if err != nil || updates.Reset || len(updates.Activity) != 1 || updates.Activity[0].ID != latest.ID || len(updates.Items) != 1 || updates.Items[0].Version != item.Version {
				t.Fatalf("live replacement missing: %+v, %v", updates, err)
			}
		}
		cursor, revision = latest.ID, next
	}
	// A disconnected client still catches up from an ID whose row was removed.
	page, err := s.Activity(t.Context(), item.ID, 0, &first, 1)
	if err != nil || len(page.Activity) != 1 || page.Activity[0].ID != cursor || page.NextAfter != 0 {
		t.Fatalf("missed replacements did not catch up: %+v, %v", page, err)
	}
	page, err = s.Activity(t.Context(), item.ID, first, nil, 1)
	if err != nil || len(page.Activity) != 1 || page.Activity[0].Kind != "item.created" {
		t.Fatalf("deleted cursor broke older history: %+v, %v", page, err)
	}
	// Inserting the replacement and deleting the old entry must be atomic with
	// the ticket edit, including when the cleanup fails.
	if _, err := s.write.Exec(`CREATE TRIGGER fail_coalesce BEFORE DELETE ON activity BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	description := "Must roll back"
	_, err = s.Update(t.Context(), user.ID, item.ID, UpdateItem{Version: item.Version, Description: &description})
	if err == nil {
		t.Fatal("expected cleanup failure")
	}
	unchanged, err := s.Get(t.Context(), item.ID)
	if err != nil || unchanged.Version != item.Version || unchanged.Description != item.Description {
		t.Fatalf("failed coalescing changed item: %+v, %v", unchanged, err)
	}
	after, err := s.Revision(t.Context())
	if err != nil || after != revision {
		t.Fatalf("failed coalescing changed revision: %+v, %v", after, err)
	}
}

func TestTextEditGroupBoundaries(t *testing.T) {
	s, user := fixture(t)
	other, err := s.CreateUser(t.Context(), "Other", "other@activity.test", "member", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	text, link, status := "Text", "https://example.test", StatusTodo
	for _, tc := range []struct {
		name   string
		first  UpdateItem
		second UpdateItem
		joins  bool
	}{
		{"description", UpdateItem{Description: &text}, UpdateItem{Description: &text}, true},
		{"title", UpdateItem{Title: &text}, UpdateItem{Title: &text}, true},
		{"link", UpdateItem{URL: &link}, UpdateItem{URL: &link}, true},
		{"same fields", UpdateItem{Title: &text, Description: &text}, UpdateItem{Title: &text, Description: &text}, true},
		{"different field", UpdateItem{Description: &text}, UpdateItem{Title: &text}, false},
		{"extra field", UpdateItem{Description: &text}, UpdateItem{Description: &text, Title: &text}, false},
		{"status", UpdateItem{Status: &status}, UpdateItem{Status: &status}, false},
		{"mixed", UpdateItem{Status: &status, Description: &text}, UpdateItem{Status: &status, Description: &text}, false},
		{"tags", UpdateItem{AddTags: []string{"tag"}}, UpdateItem{AddTags: []string{"tag"}}, false},
		{"assignees", UpdateItem{AddAssignees: []ID{user.ID}}, UpdateItem{AddAssignees: []ID{user.ID}}, false},
		{"empty", UpdateItem{}, UpdateItem{}, false},
		{"actor", UpdateItem{Description: &text}, UpdateItem{Description: &text}, false},
		{"comment", UpdateItem{Description: &text}, UpdateItem{Description: &text}, false},
		{"expired", UpdateItem{Description: &text}, UpdateItem{Description: &text}, false},
		{"other ticket", UpdateItem{Description: &text}, UpdateItem{Description: &text}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := addItem(t, s, user.ID, tc.name)
			tc.first.Version = item.Version
			item, err = s.Update(t.Context(), user.ID, item.ID, tc.first)
			if err != nil {
				t.Fatal(err)
			}
			actor, count := user.ID, 3
			switch tc.name {
			case "actor":
				actor = other.ID
			case "comment":
				if _, err := s.AddComment(t.Context(), user.ID, item.ID, CreateComment{Body: "Between edits", ClientID: "between"}); err != nil {
					t.Fatal(err)
				}
				count++
			case "expired":
				if _, err := s.write.Exec("UPDATE activity SET created_at=? WHERE item_id=?", time.Now().Add(-6*time.Minute).UTC().Format(time.RFC3339Nano), item.ID); err != nil {
					t.Fatal(err)
				}
			case "other ticket":
				addItem(t, s, other.ID, "Unrelated activity")
			}
			tc.second.Version = item.Version
			if _, err := s.Update(t.Context(), actor, item.ID, tc.second); err != nil {
				t.Fatal(err)
			}
			if tc.joins {
				count--
			}
			page, err := s.Activity(t.Context(), item.ID, 0, nil, 50)
			if err != nil || len(page.Activity) != count {
				t.Fatalf("want %d entries: %+v, %v", count, page, err)
			}
		})
	}
}
