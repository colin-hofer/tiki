// Package httpapi serves the web UI and JSON API, including authentication,
// authorization, bounded requests, and live updates.
package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"tiki/frontend"
	"tiki/internal/tiki"
)

type access uint8

const (
	public access = iota
	read
	write
	admin
)

type endpoint func(*http.Request, tiki.User) (any, error)

type server struct {
	store    *tiki.Store
	attempts loginLimiter
}

// Handler serves the web UI and versioned JSON API. Authorization belongs at
// this boundary; Store is a trusted in-process API.
func Handler(store *tiki.Store) http.Handler {
	s := &server{store: store, attempts: loginLimiter{windows: make(map[string]loginWindow)}}
	mux := http.NewServeMux()
	mux.Handle("/", frontend.Handler())
	registerDownloads(mux, downloads())
	methods := make(map[string][]string)
	for _, path := range []string{"/api/v1/cli", "/api/v1/cli/install.sh", "/api/v1/cli/downloads/", "/api/v1/skills/tiki/SKILL.md", "/api/v1/skills/tiki/SKILL.md.sha256"} {
		methods[path] = []string{http.MethodGet, http.MethodHead}
	}
	// Streams manage their own deadlines and authentication lifetime.
	mux.Handle("GET /api/v1/events", &eventStream{store: store, slots: make(chan struct{}, 256), heartbeat: 15 * time.Second})
	methods["/api/v1/events"] = []string{http.MethodGet}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := store.Ping(ctx); err != nil {
			writeError(w, &tiki.Error{Code: "unavailable", Message: "database is unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	for _, route := range []struct {
		pattern string
		access  access
		handle  endpoint
	}{
		{"POST /api/v1/auth/login", public, s.limit(s.login)},
		{"GET /api/v1/auth/me", read, s.me},
		{"PATCH /api/v1/auth/me", read, s.changeName},
		{"POST /api/v1/auth/logout", read, s.logout},
		{"POST /api/v1/auth/password", read, s.limit(s.changePassword)},
		{"POST /api/v1/users", admin, s.createUser},
		{"GET /api/v1/users", read, s.users},
		{"PATCH /api/v1/users/{id}", admin, s.changeUserRole},
		{"DELETE /api/v1/users/{id}", admin, s.removeUser},
		{"POST /api/v1/invites", admin, s.createInvite},
		{"GET /api/v1/invites", admin, s.invites},
		{"DELETE /api/v1/invites/{id}", admin, s.revokeInvite},
		{"POST /api/v1/auth/invite", public, s.limit(s.invite)},
		{"POST /api/v1/auth/join", public, s.limit(s.join)},
		{"POST /api/v1/items", write, s.createItem},
		{"GET /api/v1/items", read, s.items},
		{"GET /api/v1/board", read, s.board},
		{"GET /api/v1/items/{id}", read, s.item},
		{"PATCH /api/v1/items/{id}", write, s.updateItem},
		{"DELETE /api/v1/items/{id}", write, s.deleteItem},
		{"POST /api/v1/items/{id}/move", write, s.moveItem},
		{"POST /api/v1/items/{id}/comments", write, s.comment},
		{"GET /api/v1/items/{id}/activity", read, s.activity},
		{"GET /api/v1/tags", read, s.tags},
		{"DELETE /api/v1/tags", write, s.deleteTag},
	} {
		method, path, _ := strings.Cut(route.pattern, " ")
		methods[path] = append(methods[path], method)
		if method == http.MethodGet {
			methods[path] = append(methods[path], http.MethodHead)
		}
		mux.Handle(route.pattern, s.handle(route.access, route.handle))
	}
	for path, allowed := range methods {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			writeError(w, &tiki.Error{Code: "method_not_allowed", Message: "method not allowed"})
		})
	}
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &tiki.Error{Code: "not_found", Message: "API route not found"})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func (s *server) handle(access access, fn endpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		var user tiki.User
		if access != public {
			var err error
			user, err = s.store.Authenticate(ctx, bearerToken(r))
			if err != nil {
				writeError(w, err)
				return
			}
		}
		if access == admin && user.Role != tiki.RoleAdmin || access == write && user.Role != tiki.RoleAdmin && user.Role != tiki.RoleMember {
			writeError(w, &tiki.Error{Code: "forbidden", Message: "insufficient permissions"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		out, err := fn(r, user)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}
