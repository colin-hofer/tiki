package httpapi

import (
	"net/http"

	"tiki/internal/tiki"
)

func (s *server) createUser(r *http.Request, _ tiki.User) (any, error) {
	var in struct {
		Name     string    `json:"name"`
		Email    string    `json:"email"`
		Role     tiki.Role `json:"role"`
		Password string    `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Role == "" {
		in.Role = tiki.RoleMember
	}
	return s.store.CreateUser(r.Context(), in.Name, in.Email, in.Role, in.Password)
}

func (s *server) users(r *http.Request, _ tiki.User) (any, error) {
	after, limit, err := pagination(r)
	if err != nil {
		return nil, err
	}
	return s.store.Users(r.Context(), after, limit)
}

func (s *server) changeUserRole(r *http.Request, _ tiki.User) (any, error) {
	id, err := tiki.ParseID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	var in struct {
		Role tiki.Role `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.store.ChangeUserRole(r.Context(), id, in.Role)
}

func (s *server) removeUser(r *http.Request, _ tiki.User) (any, error) {
	id, err := tiki.ParseID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return s.store.RemoveUser(r.Context(), id)
}
