package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type User struct {
	ID             int64      `json:"id"`
	Email          string     `json:"email"`
	PasswordHash   string     `json:"-"`
	DisplayName    string     `json:"display_name"`
	Scopes         []string   `json:"scopes"`
	RoleLevel      int        `json:"role_level"`
	IsFrozen       bool       `json:"is_frozen"`
	FailedAttempts int        `json:"failed_attempts"`
	FrozenAt       *time.Time `json:"frozen_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type UpdateUserParams struct {
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Scopes      []string `json:"scopes"`
	RoleLevel   int      `json:"role_level"`
}

const userCols = `id, email, password_hash, display_name, scopes, role_level, is_frozen, failed_attempts, frozen_at, created_at, updated_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName, &u.Scopes, &u.RoleLevel, &u.IsFrozen, &u.FailedAttempts, &u.FrozenAt, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) CreateUser(ctx context.Context, email, passwordHash, displayName string, scopes []string, roleLevel int) (User, error) {
	if scopes == nil {
		scopes = []string{}
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users(email, password_hash, display_name, scopes, role_level)
		VALUES($1, $2, $3, $4, $5)
		RETURNING `+userCols, email, passwordHash, displayName, scopes, roleLevel)
	u, err := scanUser(row)
	if err != nil {
		return User{}, err
	}
	return *u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email=$1`, email)
	return scanUser(row)
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id)
	return scanUser(row)
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY role_level, email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName, &u.Scopes, &u.RoleLevel, &u.IsFrozen, &u.FailedAttempts, &u.FrozenAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	if users == nil {
		users = []User{}
	}
	return users, nil
}

func (s *Store) UpdateUser(ctx context.Context, id int64, p UpdateUserParams) (*User, error) {
	if p.Scopes == nil {
		p.Scopes = []string{}
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE users SET email=$2, display_name=$3, scopes=$4, role_level=$5, updated_at=NOW()
		WHERE id=$1
		RETURNING `+userCols, id, p.Email, p.DisplayName, p.Scopes, p.RoleLevel)
	return scanUser(row)
}

func (s *Store) UpdateUserPassword(ctx context.Context, id int64, passwordHash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash=$2, updated_at=NOW() WHERE id=$1`, id, passwordHash)
	return err
}

func (s *Store) IncrementFailedAttempts(ctx context.Context, id int64) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		UPDATE users SET failed_attempts = failed_attempts + 1, updated_at=NOW()
		WHERE id=$1
		RETURNING failed_attempts
	`, id).Scan(&count)
	return count, err
}

func (s *Store) FreezeUser(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET is_frozen=TRUE, frozen_at=NOW(), updated_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) UnfreezeUser(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET is_frozen=FALSE, frozen_at=NULL, failed_attempts=0, updated_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) ResetFailedAttempts(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET failed_attempts=0, updated_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	return err
}
