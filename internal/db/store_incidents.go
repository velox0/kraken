package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Incident struct {
	ID              int64      `json:"id"`
	ProjectID       int64      `json:"project_id"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	ErrorMessage    string     `json:"error_message"`
	LastAlertSentAt *time.Time `json:"last_alert_sent_at,omitempty"`
	AutofixAttempts int        `json:"autofix_attempts"`
}

func (s *Store) GetOpenIncident(ctx context.Context, projectID int64) (*Incident, error) {
	query := `
		SELECT id, project_id, status, started_at, resolved_at, error_message, last_alert_sent_at, autofix_attempts
		FROM incidents
		WHERE project_id=$1 AND status='open'
		LIMIT 1
	`
	var inc Incident
	var resolved sql.NullTime
	var lastAlert sql.NullTime
	err := s.pool.QueryRow(ctx, query, projectID).Scan(
		&inc.ID,
		&inc.ProjectID,
		&inc.Status,
		&inc.StartedAt,
		&resolved,
		&inc.ErrorMessage,
		&lastAlert,
		&inc.AutofixAttempts,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if resolved.Valid {
		v := resolved.Time
		inc.ResolvedAt = &v
	}
	if lastAlert.Valid {
		v := lastAlert.Time
		inc.LastAlertSentAt = &v
	}
	return &inc, nil
}

func (s *Store) CreateIncident(ctx context.Context, projectID int64, errorMessage string) (Incident, error) {
	query := `
		INSERT INTO incidents(project_id, status, error_message)
		VALUES ($1, 'open', $2)
		ON CONFLICT (project_id) WHERE status='open' DO NOTHING
		RETURNING id, project_id, status, started_at, resolved_at, error_message, last_alert_sent_at, autofix_attempts
	`
	var inc Incident
	var resolved sql.NullTime
	var lastAlert sql.NullTime
	err := s.pool.QueryRow(ctx, query, projectID, truncate(errorMessage, 1024)).Scan(
		&inc.ID,
		&inc.ProjectID,
		&inc.Status,
		&inc.StartedAt,
		&resolved,
		&inc.ErrorMessage,
		&lastAlert,
		&inc.AutofixAttempts,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			existing, getErr := s.GetOpenIncident(ctx, projectID)
			if getErr != nil {
				return Incident{}, getErr
			}
			if existing == nil {
				return Incident{}, fmt.Errorf("failed to create incident for project %d", projectID)
			}
			return *existing, nil
		}
		return Incident{}, err
	}
	if resolved.Valid {
		v := resolved.Time
		inc.ResolvedAt = &v
	}
	if lastAlert.Valid {
		v := lastAlert.Time
		inc.LastAlertSentAt = &v
	}
	return inc, nil
}

func (s *Store) ResolveIncident(ctx context.Context, incidentID int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE incidents
		SET status='resolved', resolved_at=NOW()
		WHERE id=$1 AND status='open'
	`, incidentID)
	return err
}

func (s *Store) UpdateIncidentAlertTime(ctx context.Context, incidentID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE incidents SET last_alert_sent_at=NOW() WHERE id=$1`, incidentID)
	return err
}

func (s *Store) IncrementIncidentAutofixAttempts(ctx context.Context, incidentID int64) (int, error) {
	var attempts int
	err := s.pool.QueryRow(ctx, `
		UPDATE incidents SET autofix_attempts = autofix_attempts + 1
		WHERE id=$1
		RETURNING autofix_attempts
	`, incidentID).Scan(&attempts)
	if err != nil {
		return 0, err
	}
	return attempts, nil
}
