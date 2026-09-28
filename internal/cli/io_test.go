package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiki/internal/tiki"
)

func TestUsageFailuresHaveStableExitCode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, args := range [][]string{
		{"unknown"}, {"item", "unknown"}, {"item", "get"}, {"item", "list", "extra"},
		{"item", "create", "--description", "x", "--body-file", "-"},
		{"item", "list", "--unknown"}, {"item", "list", "--limit", "abc"},
	} {
		var out, stderr bytes.Buffer
		if code := run(append([]string{"--json"}, args...), strings.NewReader(""), &out, &stderr); code != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), `"code":"validation"`) {
			t.Fatalf("%v: code=%d, stdout=%q, stderr=%q", args, code, out.String(), stderr.String())
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOutputReportsBrokenPipes(t *testing.T) {
	for _, out := range []any{tiki.Item{ID: 1, Title: "Title"}, tiki.Page{Items: []tiki.Item{{ID: 1, Title: "Title"}}}} {
		a := app{out: brokenWriter{}, err: io.Discard}
		if err := a.print(out); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("%T ignored output failure: %v", out, err)
		}
	}
}

func TestBodyFileUsesProvidedInputAndBoundsSize(t *testing.T) {
	got, err := bodyFile("-", strings.NewReader("from injected stdin"), tiki.MaxDescriptionBytes)
	if err != nil || got != "from injected stdin" {
		t.Fatalf("body: %q, %v", got, err)
	}
	if _, err := bodyFile("-", strings.NewReader(strings.Repeat("x", 256*1024+1)), tiki.MaxDescriptionBytes); err == nil {
		t.Fatal("unbounded description")
	}
}

func TestClientRejectsRedirectsAndMalformedResponses(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// A failed request must never send a session to a redirect target.
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("client followed a redirect")
	}))
	defer destination.Close()
	for _, body := range []string{`{} {}`, `{}garbage`, `{}`, strings.Repeat(" ", maxResponseBytes) + `{}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if body == `{}` {
				http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
				return
			}
			io.WriteString(w, body)
		}))
		if err := saveSession(clientSession{Server: server.URL, Session: tiki.Session{User: tiki.User{ID: 1}, Token: "test-session", ExpiresAt: time.Now().Add(time.Hour).Unix()}}); err != nil {
			t.Fatal(err)
		}
		a := app{server: server.URL, timeout: time.Second}
		var out any
		err := a.request(context.Background(), "GET", "/api/v1/auth/me", nil, &out)
		server.Close()
		var api *tiki.Error
		if !errors.As(err, &api) || api.Code != "transport" {
			t.Fatalf("accepted response: %v", err)
		}
	}
}

func TestClientAcceptsMaximumMembershipPage(t *testing.T) {
	tags := make([]string, 100)
	for i := range tags {
		tags[i] = fmt.Sprintf("%03d%s", i, strings.Repeat("<", 61))
	}
	page := tiki.Page{Items: make([]tiki.Item, tiki.MaxPageSize)}
	for i := range page.Items {
		page.Items[i] = tiki.Item{ID: tiki.ID(i + 1), CreatedBy: 1, Title: "Item", Tags: tags}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(page) }))
	defer server.Close()
	// Login is the client's only unauthenticated route; this tests decoding, not authorization.
	a := app{server: server.URL, timeout: 5 * time.Second}
	var got tiki.Page
	if err := a.request(t.Context(), "POST", "/api/v1/auth/login", nil, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != tiki.MaxPageSize || len(got.Items[0].Tags) != 100 {
		t.Fatal("truncated valid item page")
	}
}

func TestClientPreservesNumbersAndValidatesErrorBodies(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"precise integer", `{"version":9007199254740993}`, 200, ""},
		{"valid error", `{"error":{"code":"conflict","message":"changed"}}`, 409, "conflict"},
		{"extra error value", `{"error":{"code":"conflict","message":"changed"}} {}`, 409, "transport"},
		{"oversized error", `{"error":{"code":"conflict","message":"changed"}}` + strings.Repeat(" ", maxResponseBytes), 409, "transport"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			var output bytes.Buffer
			a := app{server: server.URL, timeout: time.Second, json: true, out: &output}
			err := printResponse[any](&a, t.Context(), "POST", "/api/v1/auth/login", nil)
			if tc.code == "" {
				if err != nil || strings.TrimSpace(output.String()) != tc.body {
					t.Fatalf("lost precision: %s, %v", &output, err)
				}
			} else {
				var api *tiki.Error
				if !errors.As(err, &api) || api.Code != tc.code || output.Len() != 0 {
					t.Fatalf("unexpected error/output: %v, %s", err, &output)
				}
			}
		})
	}
}
