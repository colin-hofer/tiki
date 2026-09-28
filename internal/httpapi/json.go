package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"tiki/internal/tiki"
)

func itemFilter(r *http.Request) (tiki.Filter, error) {
	q := r.URL.Query()
	f := tiki.Filter{Tags: q["tag"], Status: tiki.Status(q.Get("status")), Queries: q["query"], Cursor: q.Get("cursor")}
	var err error
	f.Limit, err = queryLimit(r, 0)
	if err != nil {
		return tiki.Filter{}, err
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
	if v := r.URL.Query().Get("after"); v != "" {
		after, err = tiki.ParseID(v)
		if err != nil {
			return 0, 0, err
		}
	}
	limit, err := queryLimit(r, tiki.DefaultPageSize)
	return after, limit, err
}

// A missing limit uses the endpoint's default; an explicit limit must be valid.
func queryLimit(r *http.Request, fallback int) (int, error) {
	q := r.URL.Query()
	if !q.Has("limit") {
		return fallback, nil
	}
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit < 1 || limit > tiki.MaxPageSize {
		return 0, invalid("limit must be between 1 and 200")
	}
	return limit, nil
}

func decode[T any](r *http.Request, out *T) error {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return &tiki.Error{Code: "unsupported_media_type", Message: "Content-Type must be application/json"}
		}
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &tiki.Error{Code: "too_large", Message: "request body exceeds 2 MiB"}
		}
		return invalid("invalid JSON: " + err.Error())
	}
	if out == nil {
		return invalid("body must be a JSON object")
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

func invalid(message string) *tiki.Error { return &tiki.Error{Code: "validation", Message: message} }
