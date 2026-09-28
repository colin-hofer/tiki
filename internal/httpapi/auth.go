package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"

	"tiki/internal/tiki"
)

func (s *server) login(r *http.Request, _ tiki.User) (any, error) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.store.Login(r.Context(), in.Email, in.Password)
}

func (s *server) me(_ *http.Request, user tiki.User) (any, error) {
	return user, nil
}

func (s *server) changeName(r *http.Request, user tiki.User) (any, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.store.ChangeName(r.Context(), user.ID, in.Name)
}

func (s *server) logout(r *http.Request, _ tiki.User) (any, error) {
	err := s.store.Logout(r.Context(), bearerToken(r))
	return map[string]bool{"logged_out": err == nil}, err
}

func (s *server) changePassword(r *http.Request, user tiki.User) (any, error) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	err := s.store.ChangePassword(r.Context(), user.ID, in.Current, in.New)
	return map[string]bool{"password_changed": err == nil}, err
}

func (s *server) invite(r *http.Request, _ tiki.User) (any, error) {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	invite, err := s.store.Invite(r.Context(), in.Token)
	return map[string]any{"role": invite.Role, "expires_at": invite.ExpiresAt}, err
}

func (s *server) join(r *http.Request, _ tiki.User) (any, error) {
	var in struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.store.ClaimInvite(r.Context(), in.Token, in.Name, in.Email, in.Password)
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

func (s *server) limit(next endpoint) endpoint {
	return func(r *http.Request, user tiki.User) (any, error) {
		if !s.attempts.allow(r.RemoteAddr) {
			return nil, &tiki.Error{Code: "rate_limited", Message: "too many authentication attempts; retry in one minute"}
		}
		return next(r, user)
	}
}
