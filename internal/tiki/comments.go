package tiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const MaxCommentBytes = 16 << 10

type CreateComment struct {
	Body     string `json:"body"`
	ClientID string `json:"client_id"`
}

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
		result, err := tx.ExecContext(ctx, "INSERT INTO activity(item_id,actor_id,kind,data,created_at,client_id) VALUES(?,?,?,?,?,?)", item, actor, out.Kind, string(out.Data), out.CreatedAt, out.ClientID)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		out.ID = ID(id)
		return nil
	})
	return out, err
}
