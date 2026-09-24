package httpapi

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tiki/internal/tiki"
)

func openEvents(t *testing.T, server *httptest.Server, token string) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("invalid stream response: %v", response)
	}
	return bufio.NewReader(response.Body)
}

func readEvent(t *testing.T, reader *bufio.Reader, kind string) string {
	t.Helper()
	for {
		var frame strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("waiting for %s: %v", kind, err)
			}
			if line == "\n" {
				break
			}
			frame.WriteString(line)
		}
		value := frame.String()
		if strings.HasPrefix(value, ":") {
			continue
		}
		if !strings.HasPrefix(value, "event: "+kind+"\n") {
			t.Fatalf("wanted %s, got %q", kind, value)
		}
		return value
	}
}

func TestEventsCommitFanoutReconnectAndRevocation(t *testing.T) {
	store, admin := fixture(t)
	session, err := store.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler(store))
	t.Cleanup(server.Close)
	first := openEvents(t, server, session.Token)
	second := openEvents(t, server, session.Token)
	readEvent(t, first, "ready")
	readEvent(t, second, "ready")
	item, err := store.Create(t.Context(), admin.ID, tiki.CreateItem{Title: "Committed", Description: "Authoritative description"})
	if err != nil {
		t.Fatal(err)
	}
	for _, reader := range []*bufio.Reader{first, second} {
		if frame := readEvent(t, reader, "change"); !strings.Contains(frame, item.Title) || !strings.Contains(frame, item.Description) || !strings.Contains(frame, `"version":1`) {
			t.Fatal(frame)
		}
	}
	// Reconnection starts at current state even if the client missed every event.
	third := openEvents(t, server, session.Token)
	if frame := readEvent(t, third, "ready"); !strings.Contains(frame, `"reset":true`) {
		t.Fatal(frame)
	}
	if _, err := store.CreateUser(t.Context(), "Viewer", "viewer@events.test", "viewer", testPassword); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []*bufio.Reader{first, second, third} {
		if frame := readEvent(t, reader, "change"); !strings.Contains(frame, `"users":true`) {
			t.Fatal(frame)
		}
	}
	if err := store.Logout(t.Context(), session.Token); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []*bufio.Reader{first, second, third} {
		readEvent(t, reader, "expired")
		if _, err := reader.ReadByte(); err != io.EOF {
			t.Fatalf("revoked stream is still open: %v", err)
		}
	}
}

func TestEventsCrossStoreCatchupAndIdleDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	store, err := tiki.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	admin, err := store.Bootstrap(t.Context(), "Admin", "admin@events.test", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	other, err := tiki.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	server := httptest.NewUnstartedServer(&eventStream{store: store, slots: make(chan struct{}, 1), heartbeat: 60 * time.Millisecond})
	server.Config.WriteTimeout = 20 * time.Millisecond
	server.Start()
	t.Cleanup(server.Close)
	reader := openEvents(t, server, session.Token)
	readEvent(t, reader, "ready")
	// A heartbeat after WriteTimeout proves an idle connection stays usable.
	if line, err := reader.ReadString('\n'); err != nil || line != ": keepalive\n" {
		t.Fatalf("heartbeat: %q %v", line, err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Create(t.Context(), admin.ID, tiki.CreateItem{Title: "Other process"}); err != nil {
		t.Fatal(err)
	}
	readEvent(t, reader, "change")
	if err := other.Logout(t.Context(), session.Token); err != nil {
		t.Fatal(err)
	}
	readEvent(t, reader, "expired")
}

func TestEventsRequireAuthAndBoundConnections(t *testing.T) {
	store, admin := fixture(t)
	session, err := store.Login(t.Context(), admin.Email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	stream := &eventStream{store: store, slots: make(chan struct{}, 1), heartbeat: time.Hour}
	server := httptest.NewServer(stream)
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		method, token string
		status        int
	}{{"GET", "", 401}, {"HEAD", session.Token, 405}, {"POST", session.Token, 405}} {
		req := httptest.NewRequest(tc.method, "/api/v1/events", nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		response := httptest.NewRecorder()
		stream.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Fatalf("%s: %d", tc.method, response.Code)
		}
	}
	reader := openEvents(t, server, session.Token)
	readEvent(t, reader, "ready")
	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+session.Token)
	response := httptest.NewRecorder()
	stream.ServeHTTP(response, req)
	if response.Code != 503 {
		t.Fatalf("unbounded streams: %d", response.Code)
	}
}
