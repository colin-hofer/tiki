package tiki

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const passwordPrefix = "$argon2id$v=19$m=65536,t=3,p=2$"

func hashPassword(password string) string {
	salt := make([]byte, 16)
	// crypto/rand.Read fills the buffer or terminates on an unrecoverable RNG failure.
	rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(strings.TrimPrefix(encoded, passwordPrefix), "$")
	if !strings.HasPrefix(encoded, passwordPrefix) || len(parts) != 2 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil || len(salt) != 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(expected) != 32 {
		return false
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(key, expected) == 1
}

func validPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= 8 && len(password) <= 1024
}

func (s *Store) passwordWork(ctx context.Context, fn func() error) error {
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	default:
		return &Error{Code: "rate_limited", Message: "authentication is busy; retry shortly"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	return ctx.Err()
}

// ChangePassword verifies the current password and atomically revokes all sessions.
func (s *Store) ChangePassword(ctx context.Context, userID ID, current, replacement string) error {
	if !validPassword(replacement) {
		return invalid("new password must contain at least 8 characters and at most 1024 bytes")
	}
	if len(current) > 1024 {
		return invalid("current password is incorrect")
	}
	var encoded, newHash string
	if err := s.read.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=?", userID).Scan(&encoded); err != nil {
		return err
	}
	if err := s.passwordWork(ctx, func() error {
		if !verifyPassword(encoded, current) {
			return invalid("current password is incorrect")
		}
		newHash = hashPassword(replacement)
		return nil
	}); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=? AND password_hash=?", newHash, userID, encoded)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			return &Error{Code: "conflict", Message: "password changed concurrently; sign in again"}
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", userID)
		return err
	})
}
