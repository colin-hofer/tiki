package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"tiki/frontend"
	"tiki/internal/tiki"
)

// Handler serves the web UI and versioned JSON API. Authorization belongs at this boundary;
// Store is a trusted in-process API and must not be exposed directly to clients.
func Handler(store *tiki.Store) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", frontend.Handler())
	methods := make(map[string][]string)
	attempts := &loginLimiter{windows: make(map[string]loginWindow)}
	// Streams have their own write deadlines and do not use the JSON timeout.
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
	handle := func(pattern, role string, fn func(*http.Request, tiki.User) (any, error)) {
		method, path, _ := strings.Cut(pattern, " ")
		methods[path] = append(methods[path], method)
		if method == http.MethodGet {
			methods[path] = append(methods[path], http.MethodHead)
		}
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			var user tiki.User
			if role != "public" {
				token := bearerToken(r)
				if token == "" {
					writeError(w, &tiki.Error{Code: "unauthorized", Message: "sign in with auth login"})
					return
				}
				var err error
				user, err = store.Authenticate(ctx, token)
				if err != nil {
					writeError(w, err)
					return
				}
			}
			if (role == "admin" && user.Role != "admin") || (role == "write" && user.Role != "admin" && user.Role != "member") {
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
	handle("POST /api/v1/auth/login", "public", func(r *http.Request, _ tiki.User) (any, error) {
		if !attempts.allow(r.RemoteAddr) {
			return nil, &tiki.Error{Code: "rate_limited", Message: "too many sign-in attempts; retry in one minute"}
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return store.Login(r.Context(), in.Email, in.Password)
	})
	handle("GET /api/v1/auth/me", "read", func(_ *http.Request, user tiki.User) (any, error) { return user, nil })
	handle("POST /api/v1/auth/logout", "read", func(r *http.Request, _ tiki.User) (any, error) {
		err := store.Logout(r.Context(), bearerToken(r))
		return map[string]bool{"logged_out": err == nil}, err
	})
	handle("POST /api/v1/auth/password", "read", func(r *http.Request, user tiki.User) (any, error) {
		if !attempts.allow(r.RemoteAddr) {
			return nil, &tiki.Error{Code: "rate_limited", Message: "too many authentication attempts; retry in one minute"}
		}
		var in struct {
			Current string `json:"current_password"`
			New     string `json:"new_password"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		err := store.ChangePassword(r.Context(), user.ID, in.Current, in.New)
		return map[string]bool{"password_changed": err == nil}, err
	})
	handle("POST /api/v1/users", "admin", func(r *http.Request, _ tiki.User) (any, error) {
		var in struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Role     string `json:"role"`
			Password string `json:"password"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		if in.Role == "" {
			in.Role = "member"
		}
		return store.CreateUser(r.Context(), in.Name, in.Email, in.Role, in.Password)
	})
	handle("GET /api/v1/users", "read", func(r *http.Request, _ tiki.User) (any, error) {
		after, limit, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return store.Users(r.Context(), after, limit)
	})
	handle("PATCH /api/v1/users/{id}", "admin", func(r *http.Request, _ tiki.User) (any, error) {
		id, err := tiki.ParseID(r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		var in struct {
			Role string `json:"role"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return store.ChangeUserRole(r.Context(), id, in.Role)
	})
	handle("DELETE /api/v1/users/{id}", "admin", func(r *http.Request, _ tiki.User) (any, error) {
		id, err := tiki.ParseID(r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		return store.RemoveUser(r.Context(), id)
	})
	handle("POST /api/v1/invites", "admin", func(r *http.Request, user tiki.User) (any, error) {
		in := struct {
			Role      string `json:"role"`
			ExpiresIn int64  `json:"expires_in"`
		}{ExpiresIn: 7 * 24 * 60 * 60}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		if in.ExpiresIn < 60 || in.ExpiresIn > 30*24*60*60 {
			return nil, invalid("expires_in must be between 60 and 2592000 seconds")
		}
		return store.CreateInvite(r.Context(), user.ID, in.Role, time.Duration(in.ExpiresIn)*time.Second)
	})
	handle("GET /api/v1/invites", "admin", func(r *http.Request, _ tiki.User) (any, error) {
		after, limit, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return store.Invites(r.Context(), after, limit)
	})
	handle("DELETE /api/v1/invites/{id}", "admin", func(r *http.Request, _ tiki.User) (any, error) {
		id, err := tiki.ParseID(r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		err = store.RevokeInvite(r.Context(), id)
		return map[string]bool{"revoked": err == nil}, err
	})
	// Keep invite secrets in request bodies, out of URLs and access logs.
	handle("POST /api/v1/auth/invite", "public", func(r *http.Request, _ tiki.User) (any, error) {
		if !attempts.allow(r.RemoteAddr) {
			return nil, &tiki.Error{Code: "rate_limited", Message: "too many authentication attempts; retry in one minute"}
		}
		var in struct {
			Token string `json:"token"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		invite, err := store.Invite(r.Context(), in.Token)
		return map[string]any{"role": invite.Role, "expires_at": invite.ExpiresAt}, err
	})
	handle("POST /api/v1/auth/join", "public", func(r *http.Request, _ tiki.User) (any, error) {
		if !attempts.allow(r.RemoteAddr) {
			return nil, &tiki.Error{Code: "rate_limited", Message: "too many authentication attempts; retry in one minute"}
		}
		var in struct {
			Token    string `json:"token"`
			Name     string `json:"name"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return store.ClaimInvite(r.Context(), in.Token, in.Name, in.Email, in.Password)
	})
	handle("POST /api/v1/items", "write", func(r *http.Request, u tiki.User) (any, error) {
		var in tiki.CreateItem
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return store.Create(r.Context(), u.ID, in)
	})
	handle("GET /api/v1/items", "read", func(r *http.Request, _ tiki.User) (any, error) {
		f, err := itemFilter(r)
		if err != nil {
			return nil, err
		}
		return store.List(r.Context(), f)
	})
	handle("GET /api/v1/board", "read", func(r *http.Request, _ tiki.User) (any, error) {
		f, err := itemFilter(r)
		if err != nil {
			return nil, err
		}
		columns, err := store.Board(r.Context(), f)
		if err != nil {
			return nil, err
		}
		// Read directories after the item snapshot, so newly assigned users/tags
		// are present. Oversized directories retain their usual page cursors.
		users, err := store.Users(r.Context(), 0, tiki.MaxPageSize)
		if err != nil {
			return nil, err
		}
		tags, err := store.Tags(r.Context(), "", tiki.MaxPageSize)
		if err != nil {
			return nil, err
		}
		return struct {
			Columns map[tiki.Status]tiki.Page `json:"columns"`
			Users   tiki.UserPage             `json:"users"`
			Tags    tiki.TagPage              `json:"tags"`
		}{columns, users, tags}, nil
	})
	handle("GET /api/v1/items/{id}", "read", func(r *http.Request, _ tiki.User) (any, error) {
		id, err := tiki.ParseID(r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		return store.Get(r.Context(), id)
	})
	handle("PATCH /api/v1/items/{id}", "write", func(r *http.Request, u tiki.User) (any, error) {
		id, err := tiki.ParseID(r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		var in tiki.UpdateItem
		if err = decode(r, &in); err != nil {
			return nil, err
		}
		return store.Update(r.Context(), u.ID, id, in)
	})
	handle("POST /api/v1/items/{id}/move", "write", func(r *http.Request, u tiki.User) (any, error) {
		id, err := tiki.ParseID(r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		var in tiki.MoveItem
		if err = decode(r, &in); err != nil {
			return nil, err
		}
		return store.Move(r.Context(), u.ID, id, in)
	})
	handle("GET /api/v1/items/{id}/activity", "read", func(r *http.Request, _ tiki.User) (any, error) {
		id, err := tiki.ParseID(r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		after, limit, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return store.Activity(r.Context(), id, after, limit)
	})
	handle("GET /api/v1/tags", "read", func(r *http.Request, _ tiki.User) (any, error) {
		limit := tiki.DefaultPageSize
		var err error
		if value := r.URL.Query().Get("limit"); value != "" {
			limit, err = strconv.Atoi(value)
			if err != nil {
				return nil, invalid("invalid limit")
			}
		}
		return store.Tags(r.Context(), r.URL.Query().Get("after"), limit)
	})
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

func itemFilter(r *http.Request) (tiki.Filter, error) {
	q := r.URL.Query()
	f := tiki.Filter{Tags: q["tag"], Status: tiki.Status(q.Get("status")), Cursor: q.Get("cursor")}
	var err error
	if q.Has("limit") {
		f.Limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || f.Limit <= 0 {
			return tiki.Filter{}, invalid("limit must be between 1 and 200")
		}
	}
	if q.Get("assignee") == "none" {
		f.Unassigned = true
	} else if q.Has("assignee") {
		f.Assignee, err = tiki.ParseID(q.Get("assignee"))
		if err != nil {
			return tiki.Filter{}, err
		}
	}
	return f, nil
}

func pagination(r *http.Request) (tiki.ID, int, error) {
	var after tiki.ID
	var err error
	limit := tiki.DefaultPageSize
	if v := r.URL.Query().Get("after"); v != "" {
		after, err = tiki.ParseID(v)
		if err != nil {
			return 0, 0, err
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil {
			return 0, 0, invalid("invalid limit")
		}
	}
	return after, limit, nil
}

func decode(r *http.Request, out any) error {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return &tiki.Error{Code: "unsupported_media_type", Message: "Content-Type must be application/json"}
		}
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &tiki.Error{Code: "too_large", Message: "request body exceeds 2 MiB"}
		}
		return invalid("invalid JSON: " + err.Error())
	}
	if err := d.Decode(new(any)); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &tiki.Error{Code: "too_large", Message: "request body exceeds 2 MiB"}
		}
		return invalid("body must contain exactly one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, out any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(out); err != nil {
		slog.Debug("write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	var api *tiki.Error
	if !errors.As(err, &api) {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			api = &tiki.Error{Code: "timeout", Message: "request canceled or timed out"}
		} else {
			slog.Error("request failed", "error", err)
			api = &tiki.Error{Code: "internal", Message: "internal server error"}
		}
	}
	status := http.StatusInternalServerError
	switch api.Code {
	case "validation":
		status = http.StatusBadRequest
	case "method_not_allowed":
		status = http.StatusMethodNotAllowed
	case "too_large":
		status = http.StatusRequestEntityTooLarge
	case "unsupported_media_type":
		status = http.StatusUnsupportedMediaType
	case "timeout":
		status = http.StatusGatewayTimeout
	case "unavailable":
		status = http.StatusServiceUnavailable
	case "unauthorized":
		status = http.StatusUnauthorized
	case "rate_limited":
		status = http.StatusTooManyRequests
		w.Header().Set("Retry-After", "60")
	case "forbidden":
		status = http.StatusForbidden
	case "not_found":
		status = http.StatusNotFound
	case "conflict", "cursor_expired":
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]any{"error": api})
}

func bearerToken(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return token
}

type loginWindow struct {
	until time.Time
	count int
}
type loginLimiter struct {
	mu      sync.Mutex
	windows map[string]loginWindow
}

func (l *loginLimiter) allow(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	window, exists := l.windows[host]
	if !exists && len(l.windows) >= 4096 {
		for key, value := range l.windows {
			if !now.Before(value.until) {
				delete(l.windows, key)
			}
		}
		if len(l.windows) >= 4096 {
			return false
		}
	}
	if !now.Before(window.until) {
		window = loginWindow{until: now.Add(time.Minute)}
	}
	if window.count >= 10 {
		return false
	}
	window.count++
	l.windows[host] = window
	return true
}

func invalid(message string) *tiki.Error { return &tiki.Error{Code: "validation", Message: message} }
