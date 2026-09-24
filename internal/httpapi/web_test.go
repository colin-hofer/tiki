//go:build !dev

package httpapi

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedWebAndAPI(t *testing.T) {
	store, _ := fixture(t)
	h := Handler(store)
	get := func(method, path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	html := get("GET", "/?item=1", 200)
	if !strings.Contains(html.Body.String(), `<div id="app">`) || html.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("missing app shell or HTML cache policy")
	}
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(html.Body.String(), -1)
	if len(assets) < 2 {
		t.Fatal("frontend build did not include JavaScript and CSS")
	}
	for _, asset := range assets {
		w := get("GET", asset[1], 200)
		if w.Body.Len() == 0 || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("bad static response: %s", asset[1])
		}
		if head := get("HEAD", asset[1], 200); head.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
	}
	for _, path := range []string{"/assets/", "/assets/missing.js", "/.env", "/src/App.svelte", "/missing"} {
		get("GET", path, 404)
	}
	get("POST", "/", 405)
	if w := get("GET", "/api/v1/items", 401); !strings.Contains(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("static handler intercepted the API")
	}
	get("GET", "/readyz", 200)
}
