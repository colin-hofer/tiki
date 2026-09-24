package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"tiki/internal/tiki"
)

// eventStream sends bounded batches of current items after successful commits.
// Each connection starts with ready: subscribe first, then fetch the view. A
// reconnect always fetches current state, so neither replay nor a cursor from
// the browser is needed to recover missed changes or server restarts.
type eventStream struct {
	store     *tiki.Store
	slots     chan struct{}
	heartbeat time.Duration
}

func (s *eventStream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, &tiki.Error{Code: "method_not_allowed", Message: "method not allowed"})
		return
	}
	token := bearerToken(r)
	read := func() (tiki.Revision, error) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := s.store.Authenticate(ctx, token); err != nil {
			return tiki.Revision{}, err
		}
		return s.store.Revision(ctx)
	}
	changed := s.store.Changes()
	revision, err := read()
	if err != nil {
		writeError(w, err)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		writeError(w, &tiki.Error{Code: "unavailable", Message: "too many event streams; retry shortly"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	send := func(kind string, updates tiki.Updates) error {
		if err := control.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		var err error
		if kind == "" {
			_, err = fmt.Fprint(w, ": keepalive\n\n")
		} else {
			data, marshalErr := json.Marshal(updates)
			if marshalErr != nil {
				return marshalErr
			}
			_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		}
		if err != nil {
			return err
		}
		if err := control.Flush(); err != nil {
			return err
		}
		// Keep idle streams alive beyond the server's ordinary WriteTimeout.
		return control.SetWriteDeadline(time.Time{})
	}
	if err := send("ready", tiki.Updates{Reset: true, Users: true}); err != nil {
		return
	}
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		beat := false
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-ticker.C:
			// Also catches writes from another Store/process and expired sessions.
			beat = true
		}
		changed = s.store.Changes()
		next, err := read()
		if err != nil {
			var apiErr *tiki.Error
			if errors.As(err, &apiErr) && apiErr.Code == "unauthorized" {
				_ = send("expired", tiki.Updates{})
			}
			return
		}
		if next != revision {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			updates, err := s.store.Updates(ctx, revision, next)
			cancel()
			if err != nil {
				return
			}
			if err := send("change", updates); err != nil {
				return
			}
			revision = next
		} else if beat {
			if err := send("", tiki.Updates{}); err != nil {
				return
			}
		}
	}
}
