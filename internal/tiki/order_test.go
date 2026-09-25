package tiki

import (
	"reflect"
	"testing"
)

func TestMoveStatusIsAtomic(t *testing.T) {
	s, admin := fixture(t)
	anchor := addItem(t, s, admin.ID, "Anchor")
	item := addItem(t, s, admin.ID, "Moving")
	status := StatusTodo
	moved, err := s.Move(t.Context(), admin.ID, item.ID, MoveItem{Version: item.Version, Before: anchor.ID, Status: &status})
	if err != nil || moved.Status != status || moved.Priority >= anchor.Priority || moved.Version != item.Version+1 {
		t.Fatalf("move: %+v, %v", moved, err)
	}
	activity, err := s.Activity(t.Context(), item.ID, 0, 50)
	if err != nil || len(activity.Activity) != 2 || activity.Activity[1].Kind != "item.moved" {
		t.Fatalf("one move should create one activity record: %+v, %v", activity, err)
	}
	invalidStatus := Status("unknown")
	for _, tc := range []struct {
		name string
		in   MoveItem
		code string
	}{
		{"stale", MoveItem{Version: 1, After: anchor.ID, Status: &status}, "conflict"},
		{"missing anchor", MoveItem{Version: moved.Version, After: 99999, Status: &status}, "not_found"},
		{"invalid status", MoveItem{Version: moved.Version, After: anchor.ID, Status: &invalidStatus}, "validation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Move(t.Context(), admin.ID, item.ID, tc.in)
			requireCode(t, err, tc.code)
			current, err := s.Get(t.Context(), item.ID)
			if err != nil || !reflect.DeepEqual(current, moved) {
				t.Fatalf("failed move changed item: %+v, %v", current, err)
			}
		})
	}
	// Failure after both fields were updated must roll back the entire operation.
	_, err = s.write.ExecContext(t.Context(), `CREATE TRIGGER fail_move BEFORE INSERT ON activity WHEN NEW.kind='item.moved' BEGIN SELECT RAISE(ABORT, 'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	status = StatusComplete
	_, err = s.Move(t.Context(), admin.ID, item.ID, MoveItem{Version: moved.Version, After: anchor.ID, Status: &status})
	if err == nil {
		t.Fatal("expected activity failure")
	}
	current, err := s.Get(t.Context(), item.ID)
	if err != nil || !reflect.DeepEqual(current, moved) {
		t.Fatalf("transaction did not roll back: %+v, %v", current, err)
	}
}
