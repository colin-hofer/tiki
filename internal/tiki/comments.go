package tiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxCommentBytes bounds the UTF-8 body of one comment.
const MaxCommentBytes = 16 << 10

// CreateComment carries plain text and a caller-chosen idempotency key.
type CreateComment struct {
	Body     string `json:"body"`
	ClientID string `json:"client_id"`
}

// AddComment appends a timeline entry without changing the ticket version.
// Reusing a client ID with the same actor, ticket, and normalized body is idempotent.
func (s *Store) AddComment(ctx context.Context, actor, item ID, in CreateComment) (Activity, error) {
	var out Activity
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || len(in.Body) > MaxCommentBytes || !utf8.ValidString(in.Body) {
		return out, invalid("comment must contain text and be at most 16 KiB")
	}
	if len(in.ClientID) < 1 || len(in.ClientID) > 128 {
		return out, invalid("client_id must contain 1 to 128 ASCII letters, digits, hyphens or underscores")
	}
	for _, c := range in.ClientID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return out, invalid("invalid client_id")
		}
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM items WHERE id=?)", item).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return missing()
		}
		var err error
		out, err = scanActivity(tx.QueryRowContext(ctx, "SELECT "+activityColumns+" FROM activity WHERE item_id=? AND actor_id=? AND client_id=?", item, actor, in.ClientID))
		if err == nil {
			var saved CreateComment
			if err := json.Unmarshal(out.Data, &saved); err != nil {
				return err
			}
			if saved.Body != in.Body {
				return &Error{Code: "conflict", Message: "client_id was already used for a different comment"}
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		data, err := json.Marshal(map[string]string{"body": in.Body})
		if err != nil {
			return err
		}
		out = Activity{ItemID: &item, ActorID: actor, Kind: "comment.created", Data: data, ClientID: in.ClientID, CreatedAt: now()}
		return tx.QueryRowContext(ctx, `INSERT INTO activity(item_id,actor_id,kind,data,created_at,client_id)
			VALUES(?,?,?,?,?,?) RETURNING id`, item, actor, out.Kind, string(out.Data), out.CreatedAt, out.ClientID).Scan(&out.ID)
	})
	return out, err
}
