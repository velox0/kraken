package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type SMTPProfile struct {
	ID                int64
	Host              string
	Port              int
	Username          string
	PasswordEncrypted string
	FromEmail         string
}

type SMTPProfileSummary struct {
	ID        int64  `json:"id"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	FromEmail string `json:"from_email"`
}

func (s *Store) ListSMTPProfiles(ctx context.Context) ([]SMTPProfileSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, host, port, username, from_email
		FROM smtp_profiles
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]SMTPProfileSummary, 0)
	for rows.Next() {
		var item SMTPProfileSummary
		if err := rows.Scan(&item.ID, &item.Host, &item.Port, &item.Username, &item.FromEmail); err != nil {
			return nil, err
		}
		res = append(res, item)
	}
	return res, rows.Err()
}

func (s *Store) GetSMTPProfile(ctx context.Context, id int64) (*SMTPProfile, error) {
	var p SMTPProfile
	err := s.pool.QueryRow(ctx, `
		SELECT id, host, port, username, password_encrypted, from_email
		FROM smtp_profiles
		WHERE id=$1
	`, id).Scan(&p.ID, &p.Host, &p.Port, &p.Username, &p.PasswordEncrypted, &p.FromEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *Store) CreateSMTPProfile(ctx context.Context, host string, port int, username, encryptedPassword, fromEmail string) (SMTPProfile, error) {
	var p SMTPProfile
	err := s.pool.QueryRow(ctx, `
		INSERT INTO smtp_profiles(host, port, username, password_encrypted, from_email)
		VALUES($1, $2, $3, $4, $5)
		RETURNING id, host, port, username, password_encrypted, from_email
	`, host, port, username, encryptedPassword, fromEmail).
		Scan(&p.ID, &p.Host, &p.Port, &p.Username, &p.PasswordEncrypted, &p.FromEmail)
	if err != nil {
		return SMTPProfile{}, err
	}
	return p, nil
}
