package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type APIKey struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	KeyHash   string    `json:"-"`
	Scopes    []string  `json:"scopes"`
	UserID    *int64    `json:"user_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateAPIKey(ctx context.Context, name, keyHash string, scopes []string, userID *int64) (APIKey, error) {
	if scopes == nil {
		scopes = []string{}
	}
	var k APIKey
	var uID sql.NullInt64
	if userID != nil {
		uID = sql.NullInt64{Int64: *userID, Valid: true}
	}
	var retUID sql.NullInt64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_keys(name, key_hash, scopes, user_id)
		VALUES($1, $2, $3, $4)
		RETURNING id, name, key_hash, scopes, user_id, created_at
	`, name, keyHash, scopes, nullInt64Arg(uID)).Scan(&k.ID, &k.Name, &k.KeyHash, &k.Scopes, &retUID, &k.CreatedAt)
	if err != nil {
		return APIKey{}, err
	}
	if retUID.Valid {
		k.UserID = &retUID.Int64
	}
	return k, nil
}

func (s *Store) GetAPIKeyByHash(ctx context.Context, keyHash string) (*APIKey, error) {
	var k APIKey
	var retUID sql.NullInt64
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, key_hash, scopes, user_id, created_at
		FROM api_keys
		WHERE key_hash=$1
	`, keyHash).Scan(&k.ID, &k.Name, &k.KeyHash, &k.Scopes, &retUID, &k.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if retUID.Valid {
		k.UserID = &retUID.Int64
	}
	return &k, nil
}

func (s *Store) DeleteAPIKey(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM api_keys WHERE id=$1`, id)
	return err
}

func (s *Store) DeleteAPIKeysByUser(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM api_keys WHERE user_id=$1`, userID)
	return err
}
