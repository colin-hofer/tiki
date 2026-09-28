package tiki

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Bootstrap creates the first administrator; it fails if any account already exists.
func (s *Store) Bootstrap(ctx context.Context, name, email, password string) (User, error) {
	return s.createAccount(ctx, User{Name: name, Email: email, Role: RoleAdmin}, password, true)
}

// CreateUser provisions an account or restores a removed identity with new credentials.
func (s *Store) CreateUser(ctx context.Context, name, email string, role Role, password string) (User, error) {
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
	if !user.Role.Valid() {
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
	err = tx.QueryRowContext(ctx, "INSERT INTO users(name,email,role,password_hash) VALUES(?,?,?,?) RETURNING id", user.Name, user.Email, user.Role, hash).Scan(&user.ID)
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

// ChangeName updates an active user's display name.
func (s *Store) ChangeName(ctx context.Context, id ID, name string) (User, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return User{}, invalid("name must be valid UTF-8 without control characters (max 200 bytes)")
	}
	var user User
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "UPDATE users SET name=? WHERE id=? AND removed_at=0 RETURNING id,name,email,role,removed_at", name, id).
			Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.RemovedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return missing()
		}
		return err
	})
	return user, err
}

// ChangeUserRole changes an active user's role while retaining at least one administrator.
func (s *Store) ChangeUserRole(ctx context.Context, id ID, role Role) (User, error) {
	if !role.Valid() {
		return User{}, invalid("role must be admin, member, or viewer")
	}
	return s.changeUser(ctx, id, role, false)
}

// RemoveUser revokes access while retaining identity for ticket history and
// existing assignments. A new invite can restore the same email and identity.
func (s *Store) RemoveUser(ctx context.Context, id ID) (User, error) {
	return s.changeUser(ctx, id, "", true)
}

func (s *Store) changeUser(ctx context.Context, id ID, role Role, remove bool) (User, error) {
	var user User
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT id,name,email,role,removed_at FROM users WHERE id=?", id).Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.RemovedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return missing()
		}
		if err != nil {
			return err
		}
		if user.RemovedAt != 0 {
			if remove {
				return nil
			}
			return &Error{Code: "conflict", Message: "user has been removed; send an invite to restore access"}
		}
		if !remove && user.Role == role {
			return nil
		}
		if user.Role == RoleAdmin {
			var admins int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE role='admin' AND removed_at=0").Scan(&admins); err != nil {
				return err
			}
			if admins <= 1 {
				return &Error{Code: "conflict", Message: "cannot remove or demote the last administrator"}
			}
			// Outstanding links must not retain privileges granted by this admin.
			if _, err := tx.ExecContext(ctx, "DELETE FROM invites WHERE created_by=?", id); err != nil {
				return err
			}
		}
		if remove {
			user.RemovedAt = time.Now().Unix()
			if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "UPDATE users SET removed_at=?,password_hash='' WHERE id=?", user.RemovedAt, id)
		} else {
			user.Role = role
			_, err = tx.ExecContext(ctx, "UPDATE users SET role=? WHERE id=?", role, id)
		}
		return err
	})
	return user, err
}

// Users lists active and removed identities in ascending ID order.
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
