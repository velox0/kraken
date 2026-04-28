package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Project struct {
	ID                       int64     `json:"id"`
	Name                     string    `json:"name"`
	Domain                   string    `json:"domain"`
	CheckIntervalSec         int       `json:"check_interval_sec"`
	FailureThreshold         int       `json:"failure_threshold"`
	AutofixEnabled           bool      `json:"autofix_enabled"`
	MaxAutofixRetries        int       `json:"max_autofix_retries"`
	SMTPProfileID            *int64    `json:"smtp_profile_id,omitempty"`
	AlertEmails              []string  `json:"alert_emails"`
	EmailSubjectOpened       string    `json:"email_subject_opened"`
	EmailBodyOpened          string    `json:"email_body_opened"`
	EmailSubjectResolved     string    `json:"email_subject_resolved"`
	EmailBodyResolved        string    `json:"email_body_resolved"`
	EmailSubjectRepeated     string    `json:"email_subject_repeated"`
	EmailBodyRepeated        string    `json:"email_body_repeated"`
	EmailSubjectAutofixLimit string    `json:"email_subject_autofix_limit"`
	EmailBodyAutofixLimit    string    `json:"email_body_autofix_limit"`
	NextCheckAt              time.Time `json:"next_check_at"`
	CreatedAt                time.Time `json:"created_at"`
}

type CreateProjectParams struct {
	Name                     string   `json:"name"`
	Domain                   string   `json:"domain"`
	CheckIntervalSec         int      `json:"check_interval_sec"`
	FailureThreshold         int      `json:"failure_threshold"`
	AutofixEnabled           bool     `json:"autofix_enabled"`
	MaxAutofixRetries        int      `json:"max_autofix_retries"`
	SMTPProfileID            *int64   `json:"smtp_profile_id"`
	AlertEmails              []string `json:"alert_emails"`
	EmailSubjectOpened       string   `json:"email_subject_opened"`
	EmailBodyOpened          string   `json:"email_body_opened"`
	EmailSubjectResolved     string   `json:"email_subject_resolved"`
	EmailBodyResolved        string   `json:"email_body_resolved"`
	EmailSubjectRepeated     string   `json:"email_subject_repeated"`
	EmailBodyRepeated        string   `json:"email_body_repeated"`
	EmailSubjectAutofixLimit string   `json:"email_subject_autofix_limit"`
	EmailBodyAutofixLimit    string   `json:"email_body_autofix_limit"`
}

type UpdateProjectParams struct {
	Name                     string   `json:"name"`
	Domain                   string   `json:"domain"`
	CheckIntervalSec         int      `json:"check_interval_sec"`
	FailureThreshold         int      `json:"failure_threshold"`
	AutofixEnabled           bool     `json:"autofix_enabled"`
	MaxAutofixRetries        int      `json:"max_autofix_retries"`
	SMTPProfileID            *int64   `json:"smtp_profile_id"`
	AlertEmails              []string `json:"alert_emails"`
	EmailSubjectOpened       string   `json:"email_subject_opened"`
	EmailBodyOpened          string   `json:"email_body_opened"`
	EmailSubjectResolved     string   `json:"email_subject_resolved"`
	EmailBodyResolved        string   `json:"email_body_resolved"`
	EmailSubjectRepeated     string   `json:"email_subject_repeated"`
	EmailBodyRepeated        string   `json:"email_body_repeated"`
	EmailSubjectAutofixLimit string   `json:"email_subject_autofix_limit"`
	EmailBodyAutofixLimit    string   `json:"email_body_autofix_limit"`
}

type ProjectHealth struct {
	ProjectID           int64
	ConsecutiveFailures int
	LastStatus          string
	UpdatedAt           time.Time
}

type DueProject struct {
	ID               int64
	CheckIntervalSec int
}

const (
	defaultEmailSubjectOpened       = "[DOWN] {domain} is unreachable"
	defaultEmailBodyOpened          = "Project: {project_name}\nDomain: {domain}\nEvent: opened\nIncident ID: {incident_id}\nCheck: #{check_id} {check_type} {check_target}\nError: {error}\nTimestamp: {timestamp}\nAutofix: {autofix_status}"
	defaultEmailSubjectResolved     = "[RESOLVED] {domain} recovered"
	defaultEmailBodyResolved        = "Project: {project_name}\nDomain: {domain}\nEvent: resolved\nIncident ID: {incident_id}\nCheck: #{check_id} {check_type} {check_target}\nTimestamp: {timestamp}\nAutofix: {autofix_status}"
	defaultEmailSubjectRepeated     = "[DOWN][REPEATED] {domain} still failing"
	defaultEmailBodyRepeated        = "Project: {project_name}\nDomain: {domain}\nEvent: repeated\nIncident ID: {incident_id}\nCheck: #{check_id} {check_type} {check_target}\nError: {error}\nTimestamp: {timestamp}\nAutofix: {autofix_status}"
	defaultEmailSubjectAutofixLimit = "[AUTOFIX LIMIT] {domain} retries exhausted"
	defaultEmailBodyAutofixLimit    = "Project: {project_name}\nDomain: {domain}\nIncident ID: {incident_id}\nAutofix attempts: {autofix_attempts}\nMax retries: {max_retries}\nTimestamp: {timestamp}\n\nAutomatic fixes have been exhausted. Manual intervention required."
)

func normalizeProjectEmailTemplates(p *Project) {
	if strings.TrimSpace(p.EmailSubjectOpened) == "" {
		p.EmailSubjectOpened = defaultEmailSubjectOpened
	}
	if strings.TrimSpace(p.EmailBodyOpened) == "" {
		p.EmailBodyOpened = defaultEmailBodyOpened
	}
	if strings.TrimSpace(p.EmailSubjectResolved) == "" {
		p.EmailSubjectResolved = defaultEmailSubjectResolved
	}
	if strings.TrimSpace(p.EmailBodyResolved) == "" {
		p.EmailBodyResolved = defaultEmailBodyResolved
	}
	if strings.TrimSpace(p.EmailSubjectRepeated) == "" {
		p.EmailSubjectRepeated = defaultEmailSubjectRepeated
	}
	if strings.TrimSpace(p.EmailBodyRepeated) == "" {
		p.EmailBodyRepeated = defaultEmailBodyRepeated
	}
	if strings.TrimSpace(p.EmailSubjectAutofixLimit) == "" {
		p.EmailSubjectAutofixLimit = defaultEmailSubjectAutofixLimit
	}
	if strings.TrimSpace(p.EmailBodyAutofixLimit) == "" {
		p.EmailBodyAutofixLimit = defaultEmailBodyAutofixLimit
	}
}

func normalizeCreateProjectEmailParams(p *CreateProjectParams) {
	if strings.TrimSpace(p.EmailSubjectOpened) == "" {
		p.EmailSubjectOpened = defaultEmailSubjectOpened
	}
	if strings.TrimSpace(p.EmailBodyOpened) == "" {
		p.EmailBodyOpened = defaultEmailBodyOpened
	}
	if strings.TrimSpace(p.EmailSubjectResolved) == "" {
		p.EmailSubjectResolved = defaultEmailSubjectResolved
	}
	if strings.TrimSpace(p.EmailBodyResolved) == "" {
		p.EmailBodyResolved = defaultEmailBodyResolved
	}
	if strings.TrimSpace(p.EmailSubjectRepeated) == "" {
		p.EmailSubjectRepeated = defaultEmailSubjectRepeated
	}
	if strings.TrimSpace(p.EmailBodyRepeated) == "" {
		p.EmailBodyRepeated = defaultEmailBodyRepeated
	}
	if strings.TrimSpace(p.EmailSubjectAutofixLimit) == "" {
		p.EmailSubjectAutofixLimit = defaultEmailSubjectAutofixLimit
	}
	if strings.TrimSpace(p.EmailBodyAutofixLimit) == "" {
		p.EmailBodyAutofixLimit = defaultEmailBodyAutofixLimit
	}
}

func normalizeProjectEmailParams(p *UpdateProjectParams) {
	if strings.TrimSpace(p.EmailSubjectOpened) == "" {
		p.EmailSubjectOpened = defaultEmailSubjectOpened
	}
	if strings.TrimSpace(p.EmailBodyOpened) == "" {
		p.EmailBodyOpened = defaultEmailBodyOpened
	}
	if strings.TrimSpace(p.EmailSubjectResolved) == "" {
		p.EmailSubjectResolved = defaultEmailSubjectResolved
	}
	if strings.TrimSpace(p.EmailBodyResolved) == "" {
		p.EmailBodyResolved = defaultEmailBodyResolved
	}
	if strings.TrimSpace(p.EmailSubjectRepeated) == "" {
		p.EmailSubjectRepeated = defaultEmailSubjectRepeated
	}
	if strings.TrimSpace(p.EmailBodyRepeated) == "" {
		p.EmailBodyRepeated = defaultEmailBodyRepeated
	}
	if strings.TrimSpace(p.EmailSubjectAutofixLimit) == "" {
		p.EmailSubjectAutofixLimit = defaultEmailSubjectAutofixLimit
	}
	if strings.TrimSpace(p.EmailBodyAutofixLimit) == "" {
		p.EmailBodyAutofixLimit = defaultEmailBodyAutofixLimit
	}
}

func (s *Store) CreateProject(ctx context.Context, p CreateProjectParams) (Project, error) {
	if p.FailureThreshold <= 0 {
		p.FailureThreshold = 3
	}
	if p.MaxAutofixRetries <= 0 {
		p.MaxAutofixRetries = 3
	}
	if p.AlertEmails == nil {
		p.AlertEmails = []string{}
	}
	normalizeCreateProjectEmailParams(&p)
	var project Project
	var smtp sql.NullInt64
	if p.SMTPProfileID != nil {
		smtp = sql.NullInt64{Int64: *p.SMTPProfileID, Valid: true}
	}
	query := `
		INSERT INTO projects (
			name, domain, check_interval_sec, failure_threshold, autofix_enabled, max_autofix_retries, smtp_profile_id, alert_emails,
			email_subject_opened, email_body_opened, email_subject_resolved, email_body_resolved,
			email_subject_repeated, email_body_repeated, email_subject_autofix_limit, email_body_autofix_limit
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING
			id, name, domain, check_interval_sec, failure_threshold, autofix_enabled, max_autofix_retries, smtp_profile_id, alert_emails,
			email_subject_opened, email_body_opened, email_subject_resolved, email_body_resolved,
			email_subject_repeated, email_body_repeated, email_subject_autofix_limit, email_body_autofix_limit,
			next_check_at, created_at
	`
	var smtpID sql.NullInt64
	err := s.pool.QueryRow(ctx, query,
		strings.TrimSpace(p.Name),
		strings.TrimSpace(p.Domain),
		p.CheckIntervalSec,
		p.FailureThreshold,
		p.AutofixEnabled,
		p.MaxAutofixRetries,
		nullInt64Arg(smtp),
		p.AlertEmails,
		p.EmailSubjectOpened,
		p.EmailBodyOpened,
		p.EmailSubjectResolved,
		p.EmailBodyResolved,
		p.EmailSubjectRepeated,
		p.EmailBodyRepeated,
		p.EmailSubjectAutofixLimit,
		p.EmailBodyAutofixLimit,
	).Scan(
		&project.ID,
		&project.Name,
		&project.Domain,
		&project.CheckIntervalSec,
		&project.FailureThreshold,
		&project.AutofixEnabled,
		&project.MaxAutofixRetries,
		&smtpID,
		&project.AlertEmails,
		&project.EmailSubjectOpened,
		&project.EmailBodyOpened,
		&project.EmailSubjectResolved,
		&project.EmailBodyResolved,
		&project.EmailSubjectRepeated,
		&project.EmailBodyRepeated,
		&project.EmailSubjectAutofixLimit,
		&project.EmailBodyAutofixLimit,
		&project.NextCheckAt,
		&project.CreatedAt,
	)
	if err != nil {
		return Project{}, err
	}
	if smtpID.Valid {
		project.SMTPProfileID = &smtpID.Int64
	}
	normalizeProjectEmailTemplates(&project)
	_, err = s.pool.Exec(ctx, `INSERT INTO project_health(project_id) VALUES($1) ON CONFLICT DO NOTHING`, project.ID)
	if err != nil {
		return Project{}, err
	}
	return project, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	query := `
		SELECT
			id, name, domain, check_interval_sec, failure_threshold, autofix_enabled, max_autofix_retries, smtp_profile_id, alert_emails,
			email_subject_opened, email_body_opened, email_subject_resolved, email_body_resolved,
			email_subject_repeated, email_body_repeated, email_subject_autofix_limit, email_body_autofix_limit,
			next_check_at, created_at
		FROM projects
		ORDER BY id ASC
	`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := make([]Project, 0)
	for rows.Next() {
		var p Project
		var smtpID sql.NullInt64
		if err := rows.Scan(
			&p.ID, &p.Name, &p.Domain, &p.CheckIntervalSec, &p.FailureThreshold, &p.AutofixEnabled, &p.MaxAutofixRetries, &smtpID, &p.AlertEmails,
			&p.EmailSubjectOpened, &p.EmailBodyOpened, &p.EmailSubjectResolved, &p.EmailBodyResolved,
			&p.EmailSubjectRepeated, &p.EmailBodyRepeated, &p.EmailSubjectAutofixLimit, &p.EmailBodyAutofixLimit,
			&p.NextCheckAt, &p.CreatedAt,
		); err != nil {
			return nil, err
		}
		if smtpID.Valid {
			p.SMTPProfileID = &smtpID.Int64
		}
		normalizeProjectEmailTemplates(&p)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *Store) GetProjectByID(ctx context.Context, projectID int64) (*Project, error) {
	var project Project
	var smtpID sql.NullInt64
	err := s.pool.QueryRow(ctx, `
		SELECT
			id, name, domain, check_interval_sec, failure_threshold, autofix_enabled, max_autofix_retries, smtp_profile_id, alert_emails,
			email_subject_opened, email_body_opened, email_subject_resolved, email_body_resolved,
			email_subject_repeated, email_body_repeated, email_subject_autofix_limit, email_body_autofix_limit,
			next_check_at, created_at
		FROM projects
		WHERE id=$1
	`, projectID).Scan(
		&project.ID,
		&project.Name,
		&project.Domain,
		&project.CheckIntervalSec,
		&project.FailureThreshold,
		&project.AutofixEnabled,
		&project.MaxAutofixRetries,
		&smtpID,
		&project.AlertEmails,
		&project.EmailSubjectOpened,
		&project.EmailBodyOpened,
		&project.EmailSubjectResolved,
		&project.EmailBodyResolved,
		&project.EmailSubjectRepeated,
		&project.EmailBodyRepeated,
		&project.EmailSubjectAutofixLimit,
		&project.EmailBodyAutofixLimit,
		&project.NextCheckAt,
		&project.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if smtpID.Valid {
		project.SMTPProfileID = &smtpID.Int64
	}
	normalizeProjectEmailTemplates(&project)
	return &project, nil
}

func (s *Store) UpdateProject(ctx context.Context, projectID int64, p UpdateProjectParams) (Project, error) {
	if p.CheckIntervalSec <= 0 {
		return Project{}, errors.New("check interval must be greater than 0")
	}
	if p.FailureThreshold <= 0 {
		return Project{}, errors.New("failure threshold must be greater than 0")
	}
	if p.MaxAutofixRetries < 0 {
		p.MaxAutofixRetries = 3
	}
	if p.AlertEmails == nil {
		p.AlertEmails = []string{}
	}
	normalizeProjectEmailParams(&p)

	var project Project
	var smtpArg sql.NullInt64
	if p.SMTPProfileID != nil {
		smtpArg = sql.NullInt64{Int64: *p.SMTPProfileID, Valid: true}
	}
	var smtpID sql.NullInt64
	err := s.pool.QueryRow(ctx, `
		UPDATE projects
		SET
			name=$2,
			domain=$3,
			check_interval_sec=$4,
			failure_threshold=$5,
			autofix_enabled=$6,
			max_autofix_retries=$7,
			smtp_profile_id=$8,
			alert_emails=$9,
			email_subject_opened=$10,
			email_body_opened=$11,
			email_subject_resolved=$12,
			email_body_resolved=$13,
			email_subject_repeated=$14,
			email_body_repeated=$15,
			email_subject_autofix_limit=$16,
			email_body_autofix_limit=$17
		WHERE id=$1
		RETURNING
			id, name, domain, check_interval_sec, failure_threshold, autofix_enabled, max_autofix_retries, smtp_profile_id, alert_emails,
			email_subject_opened, email_body_opened, email_subject_resolved, email_body_resolved,
			email_subject_repeated, email_body_repeated, email_subject_autofix_limit, email_body_autofix_limit,
			next_check_at, created_at
	`,
		projectID,
		strings.TrimSpace(p.Name),
		strings.TrimSpace(p.Domain),
		p.CheckIntervalSec,
		p.FailureThreshold,
		p.AutofixEnabled,
		p.MaxAutofixRetries,
		nullInt64Arg(smtpArg),
		p.AlertEmails,
		p.EmailSubjectOpened,
		p.EmailBodyOpened,
		p.EmailSubjectResolved,
		p.EmailBodyResolved,
		p.EmailSubjectRepeated,
		p.EmailBodyRepeated,
		p.EmailSubjectAutofixLimit,
		p.EmailBodyAutofixLimit,
	).Scan(
		&project.ID,
		&project.Name,
		&project.Domain,
		&project.CheckIntervalSec,
		&project.FailureThreshold,
		&project.AutofixEnabled,
		&project.MaxAutofixRetries,
		&smtpID,
		&project.AlertEmails,
		&project.EmailSubjectOpened,
		&project.EmailBodyOpened,
		&project.EmailSubjectResolved,
		&project.EmailBodyResolved,
		&project.EmailSubjectRepeated,
		&project.EmailBodyRepeated,
		&project.EmailSubjectAutofixLimit,
		&project.EmailBodyAutofixLimit,
		&project.NextCheckAt,
		&project.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Project{}, fmt.Errorf("project %d not found", projectID)
		}
		return Project{}, err
	}
	if smtpID.Valid {
		project.SMTPProfileID = &smtpID.Int64
	}
	normalizeProjectEmailTemplates(&project)
	return project, nil
}

func (s *Store) SetProjectAutofix(ctx context.Context, projectID int64, enabled bool) error {
	cmd, err := s.pool.Exec(ctx, `UPDATE projects SET autofix_enabled=$2 WHERE id=$1`, projectID, enabled)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("project %d not found", projectID)
	}
	return nil
}

func (s *Store) DeleteProject(ctx context.Context, projectID int64) error {
	cmd, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("project %d not found", projectID)
	}
	return nil
}

func (s *Store) GetProjectHealth(ctx context.Context, projectID int64) (ProjectHealth, error) {
	_ = s.ensureProjectHealth(ctx, projectID)
	var h ProjectHealth
	err := s.pool.QueryRow(ctx, `
		SELECT project_id, consecutive_failures, last_status, updated_at
		FROM project_health
		WHERE project_id=$1
	`, projectID).Scan(&h.ProjectID, &h.ConsecutiveFailures, &h.LastStatus, &h.UpdatedAt)
	if err != nil {
		return ProjectHealth{}, err
	}
	return h, nil
}

func (s *Store) ensureProjectHealth(ctx context.Context, projectID int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO project_health(project_id) VALUES ($1) ON CONFLICT DO NOTHING`, projectID)
	return err
}

func (s *Store) SetProjectHealth(ctx context.Context, projectID int64, consecutiveFailures int, lastStatus string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_health(project_id, consecutive_failures, last_status, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT(project_id)
		DO UPDATE SET consecutive_failures = EXCLUDED.consecutive_failures,
		              last_status = EXCLUDED.last_status,
		              updated_at = NOW()
	`, projectID, consecutiveFailures, lastStatus)
	return err
}

func (s *Store) AcquireDueProjects(ctx context.Context, limit int) ([]DueProject, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, check_interval_sec
		FROM projects
		WHERE next_check_at <= NOW()
		ORDER BY next_check_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	due := make([]DueProject, 0, limit)
	for rows.Next() {
		var p DueProject
		if err := rows.Scan(&p.ID, &p.CheckIntervalSec); err != nil {
			return nil, err
		}
		due = append(due, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, p := range due {
		_, err := tx.Exec(ctx, `
			UPDATE projects
			SET next_check_at = NOW() + make_interval(secs => $2)
			WHERE id=$1
		`, p.ID, p.CheckIntervalSec)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return due, nil
}
