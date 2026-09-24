package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"tiki/internal/tiki"
)

// A valid 200-item page with 100 fully escaped tags per item can exceed 8 MiB.
const maxResponseBytes = 16 << 20

func (a *app) request(ctx context.Context, method, path string, body, out any) error {
	server, err := serverURL(a.server)
	if err != nil {
		return err
	}
	if a.timeout <= 0 {
		return &tiki.Error{Code: "validation", Message: "timeout must be positive"}
	}
	var token string
	if path != "/api/v1/auth/login" {
		saved, err := loadSession()
		if errors.Is(err, os.ErrNotExist) {
			return &tiki.Error{Code: "unauthorized", Message: "not signed in; run tiki auth login --email YOUR_EMAIL"}
		}
		if err != nil {
			return err
		}
		if saved.Server != server {
			return &tiki.Error{Code: "validation", Message: "saved login belongs to a different server; run tiki auth login for this server"}
		}
		if saved.Session.Token == "" || saved.Session.ExpiresAt <= time.Now().Unix() {
			return &tiki.Error{Code: "unauthorized", Message: "session expired; run tiki auth login again"}
		}
		token = saved.Session.Token
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return &tiki.Error{Code: "validation", Message: err.Error()}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, server+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return &tiki.Error{Code: "transport", Message: err.Error()}
	}
	defer response.Body.Close()
	limited := &io.LimitedReader{R: response.Body, N: maxResponseBytes + 1}
	d := json.NewDecoder(limited)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error tiki.Error `json:"error"`
		}
		if err = d.Decode(&failure); err != nil || failure.Error.Code == "" {
			return &tiki.Error{Code: "transport", Message: "server returned " + response.Status}
		}
		return &failure.Error
	}
	if err = d.Decode(out); err != nil {
		return &tiki.Error{Code: "transport", Message: "invalid server response: " + err.Error()}
	}
	if err = d.Decode(new(any)); err != io.EOF || limited.N == 0 {
		return &tiki.Error{Code: "transport", Message: "server response must contain one JSON value within 16 MiB"}
	}
	return nil
}
