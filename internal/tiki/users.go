package tiki

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) ChangeUserRole(ctx context.Context, id ID, role string) (User, error) {
	if role != "admin" && role != "member" && role != "viewer" {
		return User{}, invalid("role must be admin, member, or viewer")
	}
	return s.changeUser(ctx, id, role, false)
}

// RemoveUser revokes access while retaining identity for ticket history and
// existing assignments. A new invite can restore the same email and identity.
func (s *Store) RemoveUser(ctx context.Context, id ID) (User, error) {
	return s.changeUser(ctx, id, "", true)
}

func (s *Store) changeUser(ctx context.Context, id ID, role string, remove bool) (User, error) {
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
		if user.Role == "admin" {
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
