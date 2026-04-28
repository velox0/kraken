package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type FixEnvVar struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
	Value     string `json:"value"`
	IsSecret  bool   `json:"is_secret"`
}

type FixEnvCrypto interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

func (s *Store) SetFixEnvCrypto(c FixEnvCrypto) {
	s.fixEnvCrypto = c
}

func (s *Store) encryptEnvValue(plaintext string) (string, error) {
	if s.fixEnvCrypto == nil {
		return plaintext, nil
	}
	return s.fixEnvCrypto.Encrypt(plaintext)
}

func (s *Store) decryptEnvValue(ciphertext string) (string, error) {
	if s.fixEnvCrypto == nil {
		return ciphertext, nil
	}
	return s.fixEnvCrypto.Decrypt(ciphertext)
}

// ListFixEnvVars returns all env vars for a project. Secret values are masked.
func (s *Store) ListFixEnvVars(ctx context.Context, projectID int64) ([]FixEnvVar, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, name, value, is_secret
		FROM fix_env_vars
		WHERE project_id=$1
		ORDER BY name ASC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]FixEnvVar, 0)
	for rows.Next() {
		var v FixEnvVar
		if err := rows.Scan(&v.ID, &v.ProjectID, &v.Name, &v.Value, &v.IsSecret); err != nil {
			return nil, err
		}
		if v.IsSecret {
			v.Value = "••••••••"
		} else {
			// Decrypt non-secret values for display.
			decrypted, err := s.decryptEnvValue(v.Value)
			if err == nil {
				v.Value = decrypted
			}
		}
		res = append(res, v)
	}
	return res, rows.Err()
}

// GetFixEnvVarsForExecution returns all env vars for a project with decrypted
// plaintext values. Only used internally by the autofix engine.
func (s *Store) GetFixEnvVarsForExecution(ctx context.Context, projectID int64) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, value
		FROM fix_env_vars
		WHERE project_id=$1
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var name, encrypted string
		if err := rows.Scan(&name, &encrypted); err != nil {
			return nil, err
		}
		decrypted, err := s.decryptEnvValue(encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt env var %q: %w", name, err)
		}
		result[name] = decrypted
	}
	return result, rows.Err()
}

// UpsertFixEnvVar creates or replaces an environment variable for a project.
func (s *Store) UpsertFixEnvVar(ctx context.Context, projectID int64, name, value string, isSecret bool) (FixEnvVar, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return FixEnvVar{}, errors.New("env var name is required")
	}

	encrypted, err := s.encryptEnvValue(value)
	if err != nil {
		return FixEnvVar{}, fmt.Errorf("encrypt env var: %w", err)
	}

	var v FixEnvVar
	err = s.pool.QueryRow(ctx, `
		INSERT INTO fix_env_vars (project_id, name, value, is_secret, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (project_id, name)
		DO UPDATE SET value=$3, is_secret=$4, updated_at=NOW()
		RETURNING id, project_id, name, value, is_secret
	`, projectID, name, encrypted, isSecret).Scan(&v.ID, &v.ProjectID, &v.Name, &v.Value, &v.IsSecret)
	if err != nil {
		return FixEnvVar{}, err
	}

	// Mask the returned value.
	if v.IsSecret {
		v.Value = "••••••••"
	} else {
		v.Value = value // Return the original plaintext for non-secrets.
	}
	return v, nil
}

// DeleteFixEnvVar deletes an environment variable by ID.
func (s *Store) DeleteFixEnvVar(ctx context.Context, projectID, envVarID int64) error {
	cmd, err := s.pool.Exec(ctx, `DELETE FROM fix_env_vars WHERE id=$1 AND project_id=$2`, envVarID, projectID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("env var %d not found", envVarID)
	}
	return nil
}
