package httpapi

import (
	"net/http"
	"strconv"

	"tiki/internal/tiki"
)

func (s *server) createItem(r *http.Request, u tiki.User) (any, error) {
	var in tiki.CreateItem
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.store.Create(r.Context(), u.ID, in)
}

func (s *server) items(r *http.Request, _ tiki.User) (any, error) {
	f, err := itemFilter(r)
	if err != nil {
		return nil, err
	}
	return s.store.List(r.Context(), f)
}

func (s *server) board(r *http.Request, _ tiki.User) (any, error) {
	f, err := itemFilter(r)
	if err != nil {
		return nil, err
	}
	columns, err := s.store.Board(r.Context(), f)
	if err != nil {
		return nil, err
	}
	// Read directories after the item snapshot, so newly assigned users/tags
	// are present. Oversized directories retain their usual page cursors.
	users, err := s.store.Users(r.Context(), 0, tiki.MaxPageSize)
	if err != nil {
		return nil, err
	}
	tags, err := s.store.Tags(r.Context(), "", tiki.MaxPageSize, false)
	if err != nil {
		return nil, err
	}
	return struct {
		Columns map[tiki.Status]tiki.Page `json:"columns"`
		Users   tiki.UserPage             `json:"users"`
		Tags    tiki.TagPage              `json:"tags"`
	}{columns, users, tags}, nil
}

func (s *server) item(r *http.Request, _ tiki.User) (any, error) {
	id, err := tiki.ParseID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return s.store.Get(r.Context(), id)
}

func (s *server) updateItem(r *http.Request, u tiki.User) (any, error) {
	id, err := tiki.ParseID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	var in tiki.UpdateItem
	if err = decode(r, &in); err != nil {
		return nil, err
	}
	return s.store.Update(r.Context(), u.ID, id, in)
}

func (s *server) deleteItem(r *http.Request, u tiki.User) (any, error) {
	id, err := tiki.ParseID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	var in struct {
		Version int64 `json:"version"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	err = s.store.Delete(r.Context(), u.ID, id, in.Version)
	return map[string]bool{"deleted": err == nil}, err
}

func (s *server) moveItem(r *http.Request, u tiki.User) (any, error) {
	id, err := tiki.ParseID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	var in tiki.MoveItem
	if err = decode(r, &in); err != nil {
		return nil, err
	}
	return s.store.Move(r.Context(), u.ID, id, in)
}

func (s *server) comment(r *http.Request, user tiki.User) (any, error) {
	id, err := tiki.ParseID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	var in tiki.CreateComment
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.store.AddComment(r.Context(), user.ID, id, in)
}

func (s *server) activity(r *http.Request, _ tiki.User) (any, error) {
	id, err := tiki.ParseID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	query := r.URL.Query()
	limit, err := queryLimit(r, tiki.DefaultPageSize)
	if err != nil {
		return nil, err
	}
	var before tiki.ID
	if query.Has("before") {
		before, err = tiki.ParseID(query.Get("before"))
		if err != nil {
			return nil, err
		}
	}
	var after *tiki.ID
	if query.Has("after") {
		value := tiki.ID(0)
		if query.Get("after") != "0" {
			value, err = tiki.ParseID(query.Get("after"))
			if err != nil {
				return nil, err
			}
		}
		after = &value
	}
	return s.store.Activity(r.Context(), id, before, after, limit)
}

func (s *server) tags(r *http.Request, _ tiki.User) (any, error) {
	limit, err := queryLimit(r, tiki.DefaultPageSize)
	if err != nil {
		return nil, err
	}
	usage := false
	if value := r.URL.Query().Get("usage"); value != "" {
		usage, err = strconv.ParseBool(value)
		if err != nil {
			return nil, invalid("invalid usage flag")
		}
	}
	return s.store.Tags(r.Context(), r.URL.Query().Get("after"), limit, usage)
}

func (s *server) deleteTag(r *http.Request, user tiki.User) (any, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	count, err := s.store.DeleteTag(r.Context(), user.ID, in.Name)
	return map[string]any{"deleted": err == nil, "removed_from": count}, err
}
