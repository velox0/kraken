package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Fix struct {
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	ScriptPath            string `json:"script_path"`
	SupportedErrorPattern string `json:"supported_error_pattern"`
	TimeoutSec            int    `json:"timeout_sec"`
}

type CreateFixParams struct {
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	ScriptPath            string `json:"script_path"`
	SupportedErrorPattern string `json:"supported_error_pattern"`
	TimeoutSec            int    `json:"timeout_sec"`
}

type UpdateFixParams struct {
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	ScriptPath            string `json:"script_path"`
	SupportedErrorPattern string `json:"supported_error_pattern"`
	TimeoutSec            int    `json:"timeout_sec"`
}

type FixRun struct {
	ID          int64      `json:"id"`
	ProjectID   int64      `json:"project_id"`
	FixID       *int64     `json:"fix_id,omitempty"`
	FixName     string     `json:"fix_name"`
	ScriptPath  string     `json:"script_path"`
	Trigger     string     `json:"trigger"`
	RequestedBy string     `json:"requested_by"`
	Status      string     `json:"status"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Output      string     `json:"output"`
	DurationMs  *int       `json:"duration_ms,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Store) FindMatchingFix(ctx context.Context, projectID int64, checkType, errMessage string) (*Fix, error) {
	query := `
		SELECT f.id, f.name, f.type, f.script_path, f.supported_error_pattern, f.timeout_sec
		FROM fixes f
		JOIN project_fixes pf ON pf.fix_id = f.id
		WHERE pf.project_id = $1
		  AND (f.type = $2 OR f.type = 'any')
		ORDER BY f.id ASC
	`
	rows, err := s.pool.Query(ctx, query, projectID, checkType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := 0
	for rows.Next() {
		var f Fix
		if err := rows.Scan(&f.ID, &f.Name, &f.Type, &f.ScriptPath, &f.SupportedErrorPattern, &f.TimeoutSec); err != nil {
			return nil, err
		}
		candidates++
		matched, matchErr := regexp.MatchString(f.SupportedErrorPattern, errMessage)
		if matchErr != nil {
			log.Printf("[autofix] fix %d (%q) pattern %q regex error: %v", f.ID, f.Name, f.SupportedErrorPattern, matchErr)
			continue
		}
		if matched {
			return &f, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if candidates == 0 {
		log.Printf("[autofix] project %d: no candidate fixes found for checkType=%q (check project_fixes table)", projectID, checkType)
	}
	return nil, nil
}

func (s *Store) ListProjectFixes(ctx context.Context, projectID int64) ([]Fix, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.id, f.name, f.type, f.script_path, f.supported_error_pattern, f.timeout_sec
		FROM fixes f
		JOIN project_fixes pf ON pf.fix_id = f.id
		WHERE pf.project_id=$1
		ORDER BY f.id ASC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]Fix, 0)
	for rows.Next() {
		var f Fix
		if err := rows.Scan(&f.ID, &f.Name, &f.Type, &f.ScriptPath, &f.SupportedErrorPattern, &f.TimeoutSec); err != nil {
			return nil, err
		}
		res = append(res, f)
	}
	return res, rows.Err()
}

func (s *Store) GetProjectFix(ctx context.Context, projectID, fixID int64) (*Fix, error) {
	var f Fix
	err := s.pool.QueryRow(ctx, `
		SELECT f.id, f.name, f.type, f.script_path, f.supported_error_pattern, f.timeout_sec
		FROM fixes f
		JOIN project_fixes pf ON pf.fix_id = f.id
		WHERE pf.project_id=$1 AND pf.fix_id=$2
	`, projectID, fixID).Scan(&f.ID, &f.Name, &f.Type, &f.ScriptPath, &f.SupportedErrorPattern, &f.TimeoutSec)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

func (s *Store) CreateFix(ctx context.Context, p CreateFixParams) (Fix, error) {
	if p.Type == "" {
		p.Type = "any"
	}
	if p.TimeoutSec <= 0 {
		p.TimeoutSec = 30
	}
	var f Fix
	err := s.pool.QueryRow(ctx, `
		INSERT INTO fixes(name, type, script_path, supported_error_pattern, timeout_sec)
		VALUES($1, $2, $3, $4, $5)
		RETURNING id, name, type, script_path, supported_error_pattern, timeout_sec
	`, strings.TrimSpace(p.Name), strings.TrimSpace(p.Type), strings.TrimSpace(p.ScriptPath), strings.TrimSpace(p.SupportedErrorPattern), p.TimeoutSec).
		Scan(&f.ID, &f.Name, &f.Type, &f.ScriptPath, &f.SupportedErrorPattern, &f.TimeoutSec)
	if err != nil {
		return Fix{}, err
	}
	return f, nil
}

func (s *Store) AttachFixToProject(ctx context.Context, projectID, fixID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_fixes(project_id, fix_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, projectID, fixID)
	return err
}

func (s *Store) UpdateFix(ctx context.Context, fixID int64, p UpdateFixParams) (Fix, error) {
	if strings.TrimSpace(p.Name) == "" {
		return Fix{}, errors.New("fix name is required")
	}
	if strings.TrimSpace(p.ScriptPath) == "" {
		return Fix{}, errors.New("script_path is required")
	}
	if strings.TrimSpace(p.SupportedErrorPattern) == "" {
		return Fix{}, errors.New("supported_error_pattern is required")
	}
	if p.Type == "" {
		p.Type = "any"
	}
	if p.TimeoutSec <= 0 {
		p.TimeoutSec = 30
	}
	var f Fix
	err := s.pool.QueryRow(ctx, `
		UPDATE fixes
		SET name=$2, type=$3, script_path=$4, supported_error_pattern=$5, timeout_sec=$6
		WHERE id=$1
		RETURNING id, name, type, script_path, supported_error_pattern, timeout_sec
	`, fixID,
		strings.TrimSpace(p.Name),
		strings.TrimSpace(p.Type),
		strings.TrimSpace(p.ScriptPath),
		strings.TrimSpace(p.SupportedErrorPattern),
		p.TimeoutSec,
	).Scan(&f.ID, &f.Name, &f.Type, &f.ScriptPath, &f.SupportedErrorPattern, &f.TimeoutSec)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Fix{}, fmt.Errorf("fix %d not found", fixID)
		}
		return Fix{}, err
	}
	return f, nil
}

func (s *Store) DeleteFix(ctx context.Context, fixID int64) error {
	cmd, err := s.pool.Exec(ctx, `DELETE FROM fixes WHERE id=$1`, fixID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("fix %d not found", fixID)
	}
	return nil
}

func (s *Store) DetachFixFromProject(ctx context.Context, projectID, fixID int64) error {
	cmd, err := s.pool.Exec(ctx, `DELETE FROM project_fixes WHERE project_id=$1 AND fix_id=$2`, projectID, fixID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("fix %d not attached to project %d", fixID, projectID)
	}
	return nil
}

func (s *Store) ListFixRunsByProject(ctx context.Context, projectID int64, limit int) ([]FixRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, fix_id, fix_name, script_path, trigger, requested_by,
		       status, exit_code, output, duration_ms, started_at, finished_at, created_at
		FROM fix_runs
		WHERE project_id=$1 AND created_at >= NOW() - INTERVAL '3 days'
		ORDER BY created_at DESC
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := make([]FixRun, 0)
	for rows.Next() {
		var r FixRun
		var fixID sql.NullInt64
		var exitCode sql.NullInt32
		var durationMs sql.NullInt32
		var finishedAt sql.NullTime
		if err := rows.Scan(
			&r.ID, &r.ProjectID, &fixID, &r.FixName, &r.ScriptPath, &r.Trigger, &r.RequestedBy,
			&r.Status, &exitCode, &r.Output, &durationMs, &r.StartedAt, &finishedAt, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		if fixID.Valid {
			v := fixID.Int64
			r.FixID = &v
		}
		if exitCode.Valid {
			v := int(exitCode.Int32)
			r.ExitCode = &v
		}
		if durationMs.Valid {
			v := int(durationMs.Int32)
			r.DurationMs = &v
		}
		if finishedAt.Valid {
			r.FinishedAt = &finishedAt.Time
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func (s *Store) GetFixRun(ctx context.Context, projectID, runID int64) (*FixRun, error) {
	var r FixRun
	var fixID sql.NullInt64
	var exitCode sql.NullInt32
	var durationMs sql.NullInt32
	var finishedAt sql.NullTime
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, fix_id, fix_name, script_path, trigger, requested_by,
		       status, exit_code, output, duration_ms, started_at, finished_at, created_at
		FROM fix_runs
		WHERE id=$1 AND project_id=$2
	`, runID, projectID).Scan(
		&r.ID, &r.ProjectID, &fixID, &r.FixName, &r.ScriptPath, &r.Trigger, &r.RequestedBy,
		&r.Status, &exitCode, &r.Output, &durationMs, &r.StartedAt, &finishedAt, &r.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if fixID.Valid {
		v := fixID.Int64
		r.FixID = &v
	}
	if exitCode.Valid {
		v := int(exitCode.Int32)
		r.ExitCode = &v
	}
	if durationMs.Valid {
		v := int(durationMs.Int32)
		r.DurationMs = &v
	}
	if finishedAt.Valid {
		r.FinishedAt = &finishedAt.Time
	}
	return &r, nil
}

func (s *Store) InsertFixRun(ctx context.Context, projectID int64, fixID *int64, fixName, scriptPath, trigger, requestedBy string) (int64, error) {
	var id int64
	var fixIDArg sql.NullInt64
	if fixID != nil {
		fixIDArg = sql.NullInt64{Int64: *fixID, Valid: true}
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO fix_runs (project_id, fix_id, fix_name, script_path, trigger, requested_by, status, started_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'running', NOW())
		RETURNING id
	`, projectID, nullInt64Arg(fixIDArg), fixName, scriptPath, trigger, requestedBy).Scan(&id)
	return id, err
}

func (s *Store) UpdateFixRunResult(ctx context.Context, runID int64, success bool, exitCode int, output string, durationMs int) error {
	status := "success"
	if !success {
		status = "failure"
	}
	if len(output) > 8000 {
		output = output[:8000]
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE fix_runs
		SET status=$2, exit_code=$3, output=$4, duration_ms=$5, finished_at=NOW()
		WHERE id=$1
	`, runID, status, exitCode, output, durationMs)
	return err
}

func (s *Store) CleanupOldFixRuns(ctx context.Context) (int64, error) {
	cmd, err := s.pool.Exec(ctx, `DELETE FROM fix_runs WHERE created_at < NOW() - INTERVAL '3 days'`)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}
