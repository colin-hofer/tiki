package tiki

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Invite struct {
	ID        ID     `json:"id"`
	Role      string `json:"role"`
	CreatedBy ID     `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	// Token is returned only when creating a link; only its hash is stored.
	Token string `json:"token,omitempty"`
}

type InvitePage struct {
	Invites   []Invite `json:"invites"`
	NextAfter ID       `json:"next_after,omitempty"`
}

func invalidInvite() error {
	return &Error{Code: "not_found", Message: "this invite is invalid, expired, or already used; ask for a new link"}
}

func (s *Store) CreateInvite(ctx context.Context, actor ID, role string, lifetime time.Duration) (Invite, error) {
	if role == "" {
		role = "member"
	}
	if role != "admin" && role != "member" && role != "viewer" {
		return Invite{}, invalid("role must be admin, member, or viewer")
	}
	if lifetime < time.Minute || lifetime > 30*24*time.Hour {
		return Invite{}, invalid("invite lifetime must be between one minute and 30 days")
	}
	now := time.Now()
	invite := Invite{Role: role, CreatedBy: actor, CreatedAt: now.Unix(), ExpiresAt: now.Add(lifetime).Unix(), Token: randomToken()}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM invites WHERE expires_at<=?", now.Unix()); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO invites(hash,role,created_by,created_at,expires_at) VALUES(?,?,?,?,?)", hashSession(invite.Token), role, actor, invite.CreatedAt, invite.ExpiresAt)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		invite.ID = ID(id)
		return err
	})
	return invite, err
}

func (s *Store) Invite(ctx context.Context, token string) (Invite, error) {
	var invite Invite
	if len(token) != 43 {
		return invite, invalidInvite()
	}
	err := s.read.QueryRowContext(ctx, "SELECT id,role,created_by,created_at,expires_at FROM invites WHERE hash=? AND expires_at>?", hashSession(token), time.Now().Unix()).Scan(&invite.ID, &invite.Role, &invite.CreatedBy, &invite.CreatedAt, &invite.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = invalidInvite()
	}
	return invite, err
}

// ClaimInvite consumes the invite, creates the user and signs them in in one
// transaction. Validation failures leave the link usable; concurrent claims
// (including through another server process) can produce only one account.
func (s *Store) ClaimInvite(ctx context.Context, token, name, email, password string) (Session, error) {
	invite, err := s.Invite(ctx, token)
	if err != nil {
		return Session{}, err
	}
	user, err := normalizeAccount(User{Name: name, Email: email, Role: invite.Role}, password)
	if err != nil {
		return Session{}, err
	}
	var hash string
	if err = s.passwordWork(ctx, func() error { hash = hashPassword(password); return nil }); err != nil {
		return Session{}, err
	}
	var session Session
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "DELETE FROM invites WHERE hash=? AND expires_at>? RETURNING role", hashSession(token), time.Now().Unix()).Scan(&user.Role)
		if errors.Is(err, sql.ErrNoRows) {
			return invalidInvite()
		}
		if err != nil {
			return err
		}
		user, err = insertAccount(ctx, tx, user, hash)
		if err != nil {
			return err
		}
		session = newSession(user)
		return insertSession(ctx, tx, session)
	})
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Store) RevokeInvite(ctx context.Context, id ID) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM invites WHERE id=?", id)
		return err
	})
}

func (s *Store) Invites(ctx context.Context, after ID, limit int) (InvitePage, error) {
	out := InvitePage{Invites: []Invite{}}
	if after < 0 || limit < 1 || limit > MaxPageSize {
		return out, invalid("after must be nonnegative and limit must be between 1 and 200")
	}
	rows, err := s.read.QueryContext(ctx, "SELECT id,role,created_by,created_at,expires_at FROM invites WHERE id>? AND expires_at>? ORDER BY id LIMIT ?", after, time.Now().Unix(), limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var invite Invite
		if err := rows.Scan(&invite.ID, &invite.Role, &invite.CreatedBy, &invite.CreatedAt, &invite.ExpiresAt); err != nil {
			return out, err
		}
		out.Invites = append(out.Invites, invite)
	}
	if len(out.Invites) > limit {
		out.NextAfter = out.Invites[limit-1].ID
		out.Invites = out.Invites[:limit]
	}
	return out, rows.Err()
}
