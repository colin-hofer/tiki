package tiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const itemColumns = `i.id,i.type,i.status,i.priority,i.title,i.created_by,i.created_at,i.updated_at,i.version,i.url,
 (SELECT json_group_array(CAST(user_id AS TEXT)) FROM (SELECT user_id FROM item_assignees WHERE item_id=i.id ORDER BY user_id)),
 (SELECT json_group_array(name) FROM (SELECT t.name FROM tags t JOIN item_tags it ON it.tag_id=t.id WHERE it.item_id=i.id ORDER BY t.name))`

func scanItem(row scanner) (Item, error) {
	var i Item
	var assignees, tags string
	err := row.Scan(&i.ID, &i.Type, &i.Status, &i.Priority, &i.Title, &i.CreatedBy, &i.CreatedAt, &i.UpdatedAt, &i.Version, &i.URL, &assignees, &tags, &i.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return i, missing()
	}
	if err != nil {
		return i, err
	}
	if err = json.Unmarshal([]byte(assignees), &i.Assignees); err != nil {
		return i, err
	}
	i.Preview = descriptionPreview(i.Description)
	err = json.Unmarshal([]byte(tags), &i.Tags)
	return i, err
}

func getItem(ctx context.Context, q rowQuerier, id ID) (Item, error) {
	return scanItem(q.QueryRowContext(ctx, "SELECT "+itemColumns+",i.description FROM items i WHERE i.id=?", id))
}

// Get returns a ticket with its full description and current memberships.
func (s *Store) Get(ctx context.Context, id ID) (Item, error) {
	return getItem(ctx, s.read, id)
}

// Delete permanently removes an item and its activity, using the same version
// check as edits so a stale client cannot delete someone else's newer work.
func (s *Store) Delete(ctx context.Context, actor, id ID, version int64) error {
	if version < 1 {
		return invalid("version must be positive")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkItemVersion(ctx, tx, id, version); err != nil {
			return err
		}
		// Insert before removing history to keep the activity revision increasing.
		// A global event makes live clients refresh their board and pagination.
		if err := event(ctx, tx, actor, nil, "item.deleted", map[string]ID{"id": id}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM activity WHERE item_id=?", id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM items WHERE id=?", id)
		return err
	})
}

// Create inserts a ticket, memberships, and its initial activity atomically.
func (s *Store) Create(ctx context.Context, actor ID, in CreateItem) (Item, error) {
	var out Item
	if in.Type == "" {
		in.Type = ItemTypeTask
	}
	if in.Status == "" {
		in.Status = StatusBacklog
	}
	base := Item{Title: strings.TrimSpace(in.Title), Description: in.Description, URL: strings.TrimSpace(in.URL), Type: in.Type, Status: in.Status}
	if in.Priority != nil {
		base.Priority = *in.Priority
	}
	if err := validateItem(base); err != nil {
		return out, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		priority := base.Priority
		if in.Priority == nil {
			var err error
			priority, err = appendPriority(ctx, tx, actor)
			if err != nil {
				return err
			}
		}
		timestamp := now()
		var id ID
		err := tx.QueryRowContext(ctx, `INSERT INTO items
			(type,status,priority,title,description,url,created_by,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?) RETURNING id`,
			base.Type, base.Status, priority, base.Title, base.Description, base.URL, actor, timestamp, timestamp).Scan(&id)
		if err != nil {
			return err
		}
		if err = memberships(ctx, tx, id, in.Assignees, nil, in.Tags, nil); err != nil {
			return err
		}
		out, err = getItem(ctx, tx, id)
		if err != nil {
			return err
		}
		return event(ctx, tx, actor, &out.ID, "item.created", out)
	})
	return out, err
}

// Update applies a patch and records activity if the expected version matches.
func (s *Store) Update(ctx context.Context, actor, id ID, in UpdateItem) (Item, error) {
	var out Item
	if in.Version <= 0 {
		return out, invalid("positive expected version required")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		i, err := getItem(ctx, tx, id)
		if err != nil {
			return err
		}
		if i.Version != in.Version {
			return conflict(i.Version)
		}
		if in.Title != nil {
			i.Title = strings.TrimSpace(*in.Title)
		}
		if in.Description != nil {
			i.Description = *in.Description
		}
		if in.URL != nil {
			i.URL = strings.TrimSpace(*in.URL)
		}
		if in.Type != nil {
			i.Type = *in.Type
		}
		if in.Status != nil {
			i.Status = *in.Status
		}
		if in.Priority != nil {
			i.Priority = *in.Priority
		}
		if err = validateItem(i); err != nil {
			return err
		}
		if err = memberships(ctx, tx, id, in.AddAssignees, in.RemoveAssignees, in.AddTags, in.RemoveTags); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE items SET title=?,description=?,url=?,type=?,status=?,priority=?,updated_at=?,version=version+1 WHERE id=?", i.Title, i.Description, i.URL, i.Type, i.Status, i.Priority, now(), id)
		if err != nil {
			return err
		}
		out, err = getItem(ctx, tx, id)
		if err != nil {
			return err
		}
		return editEvent(ctx, tx, actor, id, in, out.Version)
	})
	return out, err
}

// The writer transaction keeps the version check and subsequent mutation atomic.
func checkItemVersion(ctx context.Context, tx *sql.Tx, id ID, expected int64) error {
	var version int64
	err := tx.QueryRowContext(ctx, "SELECT version FROM items WHERE id=?", id).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return missing()
	}
	if err != nil {
		return err
	}
	if version != expected {
		return conflict(version)
	}
	return nil
}

func validateItem(i Item) error {
	if strings.TrimSpace(i.Title) == "" || utf8.RuneCountInString(i.Title) > 300 || !utf8.ValidString(i.Title) || strings.ContainsFunc(i.Title, unicode.IsControl) {
		return invalid("title must contain 1–300 characters without control characters")
	}
	if len(i.Description) > MaxDescriptionBytes || !utf8.ValidString(i.Description) {
		return invalid("description must be UTF-8 and at most 256 KiB")
	}
	if i.URL != "" {
		u, err := url.Parse(i.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(i.URL) > 2048 || strings.ContainsFunc(i.URL, unicode.IsControl) {
			return invalid("url must be an absolute http(s) link of at most 2048 characters")
		}
	}
	if !i.Status.Valid() {
		return invalid("unknown status")
	}
	if !i.Type.Valid() {
		return invalid("type must be bug, feature, or task")
	}
	if math.IsNaN(i.Priority) || math.IsInf(i.Priority, 0) {
		return invalid("priority must be finite")
	}
	return nil
}
