package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiki/internal/tiki"
)

func TestHTTPInputAndReadiness(t *testing.T) {
	s, admin := fixture(t)
	session, err := s.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/missing", 404}, {"PUT", "/api/v1/items/1", 405}, {"OPTIONS", "/api/v1/items", 405},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if tc.status == 405 && w.Header().Get("Allow") == "" {
			t.Fatal("method response has no Allow header")
		}
	}
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
	}{
		{"null", `null`, "application/json", 400},
		{"array", `[]`, "application/json", 400},
		{"unknown field", `{"title":"x","unknown":true}`, "application/json", 400},
		{"extra value", `{"title":"x"} {}`, "application/json", 400},
		{"wrong type", `{"title":"x"}`, "text/plain", 415},
		{"too large", `{"title":"x","description":"` + strings.Repeat("x", 2<<20) + `"}`, "application/json", 413},
		{"large suffix", `{"title":"x"}` + strings.Repeat(" ", 2<<20), "application/json", 413},
		{"charset", `{"title":"x"}`, "application/json; charset=utf-8", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/items", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+session.Token)
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || !json.Valid(w.Body.Bytes()) {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing response protections")
			}
		})
	}
	probe := func(path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("%s: %d, want %d", path, w.Code, want)
		}
	}
	probe("/readyz", http.StatusOK)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	probe("/healthz", http.StatusOK)
	probe("/readyz", http.StatusServiceUnavailable)
}

func TestDeadlineErrorIsNotInternalFailure(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, context.DeadlineExceeded)
	if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), `"code":"timeout"`) {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
}

func TestPaginationLimitsAreConsistent(t *testing.T) {
	s, admin := fixture(t)
	item, err := s.Create(t.Context(), admin.ID, tiki.CreateItem{Title: "Page"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(s)
	for _, path := range []string{"/items", "/users", "/tags", "/invites", "/items/" + item.ID.String() + "/activity"} {
		for _, query := range []string{"", "?limit=1", "?limit=", "?limit=0", "?limit=-1", "?limit=201", "?limit=abc"} {
			r := httptest.NewRequest("GET", "/api/v1"+path+query, nil)
			r.Header.Set("Authorization", "Bearer "+session.Token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			want := 400
			if query == "" || query == "?limit=1" {
				want = 200
			}
			if w.Code != want {
				t.Errorf("%s%s: got %d, want %d: %s", path, query, w.Code, want, w.Body.String())
			}
		}
	}
}
