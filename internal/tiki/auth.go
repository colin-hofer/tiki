package tiki

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const sessionLifetime = 7 * 24 * time.Hour

func hashSession(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// Login verifies credentials and creates a session using the current account state.
func (s *Store) Login(ctx context.Context, email, password string) (Session, error) {
	var out Session
	badCredentials := &Error{Code: "unauthorized", Message: "invalid email or password"}
	if len(email) > 254 || len(password) > 1024 {
		return out, badCredentials
	}
	var encoded string
	err := s.read.QueryRowContext(ctx, "SELECT id,name,email,role,password_hash FROM users WHERE email=? AND removed_at=0", strings.ToLower(strings.TrimSpace(email))).Scan(&out.User.ID, &out.User.Name, &out.User.Email, &out.User.Role, &encoded)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if !found {
		// Spend the same hash work for unknown emails, without initializing Argon2 at CLI startup.
		encoded = passwordPrefix + base64.RawStdEncoding.EncodeToString(make([]byte, 16)) + "$" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	}
	err = s.passwordWork(ctx, func() error {
		matches := verifyPassword(encoded, password)
		if !matches || !found {
			return badCredentials
		}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	out = newSession(out.User)
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		// A concurrent password change must not resurrect a session for the old password.
		var current string
		if err := tx.QueryRowContext(ctx, "SELECT password_hash,name,role FROM users WHERE id=? AND removed_at=0", out.User.ID).Scan(&current, &out.User.Name, &out.User.Role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return badCredentials
			}
			return err
		}
		if current != encoded {
			return badCredentials
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix()); err != nil {
			return err
		}
		return insertSession(ctx, tx, out)
	})
	return out, err
}

func randomToken() string {
	token := make([]byte, 32)
	rand.Read(token)
	return base64.RawURLEncoding.EncodeToString(token)
}

func newSession(user User) Session {
	return Session{User: user, Token: randomToken(), ExpiresAt: time.Now().Add(sessionLifetime).Unix()}
}

func insertSession(ctx context.Context, tx *sql.Tx, session Session) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO sessions(user_id,hash,expires_at) VALUES(?,?,?)", session.User.ID, hashSession(session.Token), session.ExpiresAt)
	return err
}

// Authenticate resolves an unexpired token to the user's current identity and role.
func (s *Store) Authenticate(ctx context.Context, token string) (User, error) {
	var user User
	if len(token) != 43 {
		return user, &Error{Code: "unauthorized", Message: "session expired or invalid; sign in with auth login"}
	}
	err := s.read.QueryRowContext(ctx, "SELECT u.id,u.name,u.email,u.role FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.hash=? AND s.expires_at>? AND u.removed_at=0", hashSession(token), time.Now().Unix()).Scan(&user.ID, &user.Name, &user.Email, &user.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return user, &Error{Code: "unauthorized", Message: "session expired or invalid; sign in with auth login"}
	}
	return user, err
}

// Logout revokes a session. Revoking an absent session is harmless.
func (s *Store) Logout(ctx context.Context, token string) error {
	_, err := s.write.ExecContext(ctx, "DELETE FROM sessions WHERE hash=?", hashSession(token))
	if err == nil {
		s.notify()
	}
	return err
}
