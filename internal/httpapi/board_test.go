package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"tiki/internal/tiki"
)

func TestBoardIncludesBoundedDirectoriesAndFilters(t *testing.T) {
	store, admin := fixture(t)
	session, err := store.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(t.Context(), admin.ID, tiki.CreateItem{Title: "Visible", Status: tiki.StatusTodo, Tags: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	handler := Handler(store)
	for _, tc := range []struct {
		query, token string
		status       int
	}{
		{"", "", 401}, {"?status=unknown", session.Token, 400}, {"?assignee=bad", session.Token, 400},
		{"?limit=100", session.Token, 400}, {"?cursor=bad", session.Token, 400}, {"?tag=api&status=todo", session.Token, 200},
	} {
		request := httptest.NewRequest("GET", "/api/v1/board"+tc.query, nil)
		request.Header.Set("Authorization", "Bearer "+tc.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.query, response.Code, response.Body.String())
		}
		if tc.status != 200 {
			continue
		}
		var board struct {
			Columns map[tiki.Status]tiki.Page `json:"columns"`
			Users   tiki.UserPage             `json:"users"`
			Tags    tiki.TagPage              `json:"tags"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &board); err != nil {
			t.Fatal(err)
		}
		if len(board.Columns) != 1 || len(board.Columns[tiki.StatusTodo].Items) != 1 || len(board.Users.Users) != 1 || len(board.Tags.Tags) != 1 {
			t.Fatalf("incomplete board: %+v", board)
		}
	}
}
