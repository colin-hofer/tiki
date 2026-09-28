package tiki

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestCommentsPersistRetryAndStreamWithoutChangingTicket(t *testing.T) {
	s, user := fixture(t)
	item := addItem(t, s, user.ID, "Discuss")
	before, _ := s.Revision(t.Context())
	input := CreateComment{Body: "  Hello\nworld  ", ClientID: "message-1"}
	first, err := s.AddComment(t.Context(), user.ID, item.ID, input)
	if err != nil || string(first.Data) != `{"body":"Hello\nworld"}` || first.ActorID != user.ID || first.ID == 0 {
		t.Fatalf("comment: %+v, %v", first, err)
	}
	var group sync.WaitGroup
	for range 5 {
		group.Go(func() {
			retry, err := s.AddComment(t.Context(), user.ID, item.ID, input)
			if err != nil || !reflect.DeepEqual(retry, first) {
				t.Errorf("retry: %+v, %v", retry, err)
			}
		})
	}
	group.Wait()
	_, err = s.AddComment(t.Context(), user.ID, item.ID, CreateComment{Body: "Different", ClientID: input.ClientID})
	requireCode(t, err, "conflict")
	second, err := s.AddComment(t.Context(), user.ID, item.ID, CreateComment{Body: "Second", ClientID: "message-2"})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.Revision(t.Context())
	updates, err := s.Updates(t.Context(), before, after)
	if err != nil || updates.Reset || len(updates.Items) != 0 || len(updates.Activity) != 2 || !reflect.DeepEqual(updates.Activity, []Activity{first, second}) {
		t.Fatalf("lost or duplicated live comments: %+v, %v", updates, err)
	}
	current, err := s.Get(t.Context(), item.ID)
	if err != nil || current.Version != item.Version || current.UpdatedAt != item.UpdatedAt {
		t.Fatalf("comment changed ticket: %+v, %v", current, err)
	}
	activity, err := s.Activity(t.Context(), item.ID, 0, nil, 50)
	if err != nil || len(activity.Activity) != 3 {
		t.Fatalf("duplicate activity: %+v, %v", activity, err)
	}
	if err := s.Delete(t.Context(), user.ID, item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.read.QueryRow("SELECT count(*) FROM activity WHERE item_id=?", item.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphaned comments: %d, %v", count, err)
	}
	_, err = s.AddComment(t.Context(), user.ID, item.ID, input)
	requireCode(t, err, "not_found")
}

func TestCommentsPaginationValidationAndRollback(t *testing.T) {
	s, user := fixture(t)
	item := addItem(t, s, user.ID, "History")
	other := addItem(t, s, user.ID, "Other ticket")
	before, _ := s.Revision(t.Context())
	changed := s.Changes()
	for _, input := range []CreateComment{
		{Body: " \n ", ClientID: "empty"}, {Body: strings.Repeat("x", MaxCommentBytes+1), ClientID: "large"},
		{Body: "Hello"}, {Body: "Hello", ClientID: "bad key"}, {Body: "Hello", ClientID: strings.Repeat("x", 129)},
	} {
		_, err := s.AddComment(t.Context(), user.ID, item.ID, input)
		requireCode(t, err, "validation")
	}
	if _, err := s.AddComment(t.Context(), 999, item.ID, CreateComment{Body: "Invalid actor", ClientID: "rollback"}); err == nil {
		t.Fatal("accepted invalid actor")
	}
	after, _ := s.Revision(t.Context())
	if before != after {
		t.Fatal("failed comment changed revision")
	}
	select {
	case <-changed:
		t.Fatal("failed comment notified clients")
	default:
	}
	initial, err := s.Activity(t.Context(), item.ID, 0, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	all := initial.Activity
	for i := range 6 {
		comment, err := s.AddComment(t.Context(), user.ID, item.ID, CreateComment{Body: fmt.Sprint(i), ClientID: fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, comment)
		if i == 2 {
			title := "Changed between messages"
			item, err = s.Update(t.Context(), user.ID, item.ID, UpdateItem{Version: item.Version, Title: &title})
			if err != nil {
				t.Fatal(err)
			}
			edited, err := s.Activity(t.Context(), item.ID, 0, nil, 1)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, edited.Activity[0])
		}
		if _, err := s.AddComment(t.Context(), user.ID, other.ID, CreateComment{Body: "Other", ClientID: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Activity(t.Context(), item.ID, 0, nil, 2)
	if err != nil || len(page.Activity) != 2 || !reflect.DeepEqual(page.Activity, all[len(all)-2:]) || page.NextBefore != all[len(all)-2].ID {
		t.Fatalf("latest: %+v, %v", page, err)
	}
	page, err = s.Activity(t.Context(), item.ID, page.NextBefore, nil, 2)
	if err != nil || !reflect.DeepEqual(page.Activity, all[len(all)-4:len(all)-2]) {
		t.Fatalf("older: %+v, %v", page, err)
	}
	cursor := ID(0)
	var caught []Activity
	for {
		page, err = s.Activity(t.Context(), item.ID, 0, &cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		caught = append(caught, page.Activity...)
		if page.NextAfter == 0 {
			break
		}
		cursor = page.NextAfter
	}
	if len(caught) != len(all) {
		t.Fatalf("catchup lost messages: %+v", caught)
	}
	for i := range all {
		if !reflect.DeepEqual(caught[i], all[i]) {
			t.Fatal("unordered catchup")
		}
	}
	_, err = s.Activity(t.Context(), item.ID, 1, &cursor, 50)
	requireCode(t, err, "validation")
	_, err = s.Activity(t.Context(), 999, 0, nil, 50)
	requireCode(t, err, "not_found")
}

func TestCommentsBoundLivePayloadAndRecoverFromHistory(t *testing.T) {
	s, user := fixture(t)
	item := addItem(t, s, user.ID, "Burst")
	before, _ := s.Revision(t.Context())
	// JSON-escaped bytes exceed the SSE budget before the row limit does.
	for i := range 64 {
		if _, err := s.AddComment(t.Context(), user.ID, item.ID, CreateComment{Body: strings.Repeat("<", MaxCommentBytes), ClientID: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := s.Revision(t.Context())
	updates, err := s.Updates(t.Context(), before, after)
	if err != nil || !updates.Reset || len(updates.Activity) != 0 {
		t.Fatalf("unbounded payload: %+v, %v", updates, err)
	}
	var cursor ID
	count := 0
	for {
		page, err := s.Activity(t.Context(), item.ID, 0, &cursor, 200)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(page)
		if len(encoded) > 3<<20 {
			t.Fatalf("unbounded history: %d", len(encoded))
		}
		count += len(page.Activity)
		if page.NextAfter == 0 {
			break
		}
		cursor = page.NextAfter
	}
	if count != 65 {
		t.Fatalf("history cannot recover: %d", count)
	}
}
