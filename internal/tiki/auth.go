package tiki

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const passwordPrefix = "$argon2id$v=19$m=65536,t=3,p=2$"
const sessionLifetime = 7 * 24 * time.Hour

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

func (s *Store) Bootstrap(ctx context.Context, name, email, password string) (User, error) {
	return s.createAccount(ctx, User{Name: name, Email: email, Role: "admin"}, password, true)
}

func (s *Store) CreateUser(ctx context.Context, name, email, role, password string) (User, error) {
	return s.createAccount(ctx, User{Name: name, Email: email, Role: role}, password, false)
}

func validName(name string) bool {
	return name != "" && len(name) <= 200 && utf8.ValidString(name) && !strings.ContainsFunc(name, unicode.IsControl)
}

func normalizeAccount(user User, password string) (User, error) {
	user.Name = strings.TrimSpace(user.Name)
	user.Email = strings.ToLower(strings.TrimSpace(user.Email))
	address, err := mail.ParseAddress(user.Email)
	if err != nil || address.Address != user.Email || len(user.Email) > 254 {
		return User{}, invalid("a valid email address is required")
	}
	if !validName(user.Name) {
		return User{}, invalid("name must be valid UTF-8 without control characters (max 200 bytes)")
	}
	if user.Role != "admin" && user.Role != "member" && user.Role != "viewer" {
		return User{}, invalid("role must be admin, member, or viewer")
	}
	if !validPassword(password) {
		return User{}, invalid("password must contain at least 8 characters and at most 1024 bytes")
	}
	return user, nil
}

func insertAccount(ctx context.Context, tx *sql.Tx, user User, hash string) (User, error) {
	// A new admin-issued invitation can restore a removed identity without
	// losing attribution or reusing its old password or sessions.
	err := tx.QueryRowContext(ctx, "UPDATE users SET name=?,role=?,password_hash=?,removed_at=0 WHERE email=? AND removed_at<>0 RETURNING id", user.Name, user.Role, hash, user.Email).Scan(&user.ID)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE email=?)", user.Email).Scan(&exists); err != nil {
		return User{}, err
	}
	if exists {
		return User{}, &Error{Code: "conflict", Message: "a user with that email already exists"}
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO users(name,email,role,password_hash) VALUES(?,?,?,?)", user.Name, user.Email, user.Role, hash)
	if err != nil {
		return User{}, err
	}
	id, err := result.LastInsertId()
	user.ID = ID(id)
	return user, err
}

func (s *Store) createAccount(ctx context.Context, user User, password string, bootstrap bool) (User, error) {
	user, err := normalizeAccount(user, password)
	if err != nil {
		return User{}, err
	}
	var hash string
	if err := s.passwordWork(ctx, func() error { hash = hashPassword(password); return nil }); err != nil {
		return User{}, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if bootstrap {
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return &Error{Code: "conflict", Message: "database is already initialized; sign in with auth login"}
			}
		}
		user, err = insertAccount(ctx, tx, user, hash)
		return err
	})
	return user, err
}

func hashSession(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

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

func (s *Store) Logout(ctx context.Context, token string) error {
	_, err := s.write.ExecContext(ctx, "DELETE FROM sessions WHERE hash=?", hashSession(token))
	if err == nil {
		s.notify()
	}
	return err
}

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

func (s *Store) Users(ctx context.Context, after ID, limit int) (UserPage, error) {
	out := UserPage{Users: []User{}}
	if after < 0 {
		return out, invalid("after must be a positive ID")
	}
	if limit < 1 || limit > MaxPageSize {
		return out, invalid("limit must be between 1 and 200")
	}
	rows, err := s.read.QueryContext(ctx, "SELECT id,name,email,role,removed_at FROM users WHERE id>? ORDER BY id LIMIT ?", after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.Role, &u.RemovedAt); err != nil {
			return out, err
		}
		if len(out.Users) == limit {
			out.NextAfter = out.Users[len(out.Users)-1].ID
			break
		}
		out.Users = append(out.Users, u)
	}
	return out, rows.Err()
}
