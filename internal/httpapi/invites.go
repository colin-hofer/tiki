package httpapi

import (
	"net/http"
	"time"

	"tiki/internal/tiki"
)

func (s *server) createInvite(r *http.Request, user tiki.User) (any, error) {
	in := struct {
		Role      tiki.Role `json:"role"`
		ExpiresIn int64     `json:"expires_in"`
	}{ExpiresIn: 7 * 24 * 60 * 60}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.ExpiresIn < 60 || in.ExpiresIn > 30*24*60*60 {
		return nil, invalid("expires_in must be between 60 and 2592000 seconds")
	}
	return s.store.CreateInvite(r.Context(), user.ID, in.Role, time.Duration(in.ExpiresIn)*time.Second)
}

func (s *server) invites(r *http.Request, _ tiki.User) (any, error) {
	after, limit, err := pagination(r)
	if err != nil {
		return nil, err
	}
	return s.store.Invites(r.Context(), after, limit)
}

func (s *server) revokeInvite(r *http.Request, _ tiki.User) (any, error) {
	id, err := tiki.ParseID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	err = s.store.RevokeInvite(r.Context(), id)
	return map[string]bool{"revoked": err == nil}, err
}
