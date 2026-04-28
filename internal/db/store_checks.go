package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/velox0/kraken/internal/monitor"
)

type Check struct {
	ID         int64               `json:"id"`
	ProjectID  int64               `json:"project_id"`
	Type       string              `json:"type"`
	Target     string              `json:"target"`
	TimeoutMs  int                 `json:"timeout_ms"`
	Assertions []monitor.Assertion `json:"assertions"`
	CreatedAt  time.Time           `json:"created_at"`
}

type CreateCheckParams struct {
	ProjectID  int64               `json:"project_id"`
	Type       string              `json:"type"`
	Target     string              `json:"target"`
	TimeoutMs  int                 `json:"timeout_ms"`
	Assertions []monitor.Assertion `json:"assertions"`
}

type ReplaceCheckParams struct {
	ID         *int64              `json:"id"`
	Type       string              `json:"type"`
	Target     string              `json:"target"`
	TimeoutMs  int                 `json:"timeout_ms"`
	Assertions []monitor.Assertion `json:"assertions"`
}

type CheckContext struct {
	Check
	ProjectName              string
	ProjectDomain            string
	FailureThreshold         int
	AutofixEnabled           bool
	MaxAutofixRetries        int
	ProjectSMTPID            *int64
	AlertEmails              []string
	EmailSubjectOpened       string
	EmailBodyOpened          string
	EmailSubjectResolved     string
	EmailBodyResolved        string
	EmailSubjectRepeated     string
	EmailBodyRepeated        string
	EmailSubjectAutofixLimit string
	EmailBodyAutofixLimit    string
	CheckIntervalSec         int
	ProjectNextCheck         time.Time
	ProjectCreatedAt         time.Time
}

func (s *Store) CreateCheck(ctx context.Context, p CreateCheckParams) (Check, error) {
	if p.TimeoutMs <= 0 {
		p.TimeoutMs = 5000
	}
	if p.Type != "http" && p.Type != "tcp" && p.Type != "ping" {
		return Check{}, fmt.Errorf("unsupported check type: %s", p.Type)
	}

	assertionsJSON, err := json.Marshal(normalizeAssertions(p.Assertions))
	if err != nil {
		return Check{}, fmt.Errorf("marshal assertions: %w", err)
	}
	query := `
		INSERT INTO checks (project_id, type, target, timeout_ms, assertions)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, project_id, type, target, timeout_ms, assertions, created_at
	`
	var c Check
	var assertionsRaw []byte
	err = s.pool.QueryRow(ctx, query,
		p.ProjectID,
		p.Type,
		strings.TrimSpace(p.Target),
		p.TimeoutMs,
		assertionsJSON,
	).Scan(&c.ID, &c.ProjectID, &c.Type, &c.Target, &c.TimeoutMs, &assertionsRaw, &c.CreatedAt)
	if err != nil {
		return Check{}, err
	}
	c.Assertions = unmarshalAssertions(assertionsRaw)
	return c, nil
}

func (s *Store) ListChecksByProject(ctx context.Context, projectID int64) ([]Check, error) {
	query := `
		SELECT id, project_id, type, target, timeout_ms, assertions, created_at
		FROM checks
		WHERE project_id=$1
		ORDER BY id ASC
	`
	rows, err := s.pool.Query(ctx, query, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	checks := make([]Check, 0)
	for rows.Next() {
		var c Check
		var assertionsRaw []byte
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Type, &c.Target, &c.TimeoutMs, &assertionsRaw, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Assertions = unmarshalAssertions(assertionsRaw)
		checks = append(checks, c)
	}
	return checks, rows.Err()
}

func (s *Store) ReplaceProjectChecks(ctx context.Context, projectID int64, checks []ReplaceCheckParams) ([]Check, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var projectExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, projectID).Scan(&projectExists); err != nil {
		return nil, err
	}
	if !projectExists {
		return nil, fmt.Errorf("project %d not found", projectID)
	}

	existingRows, err := tx.Query(ctx, `SELECT id FROM checks WHERE project_id=$1`, projectID)
	if err != nil {
		return nil, err
	}
	existingIDs := make(map[int64]struct{})
	for existingRows.Next() {
		var id int64
		if err := existingRows.Scan(&id); err != nil {
			existingRows.Close()
			return nil, err
		}
		existingIDs[id] = struct{}{}
	}
	if err := existingRows.Err(); err != nil {
		existingRows.Close()
		return nil, err
	}
	existingRows.Close()

	seenInputIDs := make(map[int64]struct{})
	keepIDs := make([]int64, 0, len(checks))
	for _, in := range checks {
		checkType := strings.ToLower(strings.TrimSpace(in.Type))
		if checkType != "http" && checkType != "tcp" && checkType != "ping" {
			return nil, fmt.Errorf("unsupported check type: %s", in.Type)
		}
		target := strings.TrimSpace(in.Target)
		if target == "" {
			return nil, errors.New("check target is required")
		}

		timeout := in.TimeoutMs
		if timeout <= 0 {
			timeout = 5000
		}

		if in.ID != nil && *in.ID > 0 {
			checkID := *in.ID
			if _, dup := seenInputIDs[checkID]; dup {
				return nil, fmt.Errorf("duplicate check id in request: %d", checkID)
			}
			seenInputIDs[checkID] = struct{}{}
			if _, ok := existingIDs[checkID]; !ok {
				return nil, fmt.Errorf("check %d does not belong to project %d", checkID, projectID)
			}
			aJSON, mErr := json.Marshal(normalizeAssertions(in.Assertions))
			if mErr != nil {
				return nil, fmt.Errorf("marshal assertions: %w", mErr)
			}
			cmd, err := tx.Exec(ctx, `
				UPDATE checks
				SET type=$3, target=$4, timeout_ms=$5, assertions=$6
				WHERE id=$1 AND project_id=$2
			`, checkID, projectID, checkType, target, timeout, aJSON)
			if err != nil {
				return nil, err
			}
			if cmd.RowsAffected() == 0 {
				return nil, fmt.Errorf("check %d does not belong to project %d", checkID, projectID)
			}
			keepIDs = append(keepIDs, checkID)
			continue
		}

		aJSON2, mErr2 := json.Marshal(normalizeAssertions(in.Assertions))
		if mErr2 != nil {
			return nil, fmt.Errorf("marshal assertions: %w", mErr2)
		}
		var newID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO checks(project_id, type, target, timeout_ms, assertions)
			VALUES($1, $2, $3, $4, $5)
			RETURNING id
		`, projectID, checkType, target, timeout, aJSON2).Scan(&newID); err != nil {
			return nil, err
		}
		keepIDs = append(keepIDs, newID)
	}

	if len(keepIDs) == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM checks WHERE project_id=$1`, projectID); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `DELETE FROM checks WHERE project_id=$1 AND NOT (id = ANY($2))`, projectID, keepIDs); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ListChecksByProject(ctx, projectID)
}

func (s *Store) ListChecksForProjects(ctx context.Context, projectIDs []int64) ([]Check, error) {
	if len(projectIDs) == 0 {
		return nil, nil
	}
	query := `
		SELECT id, project_id, type, target, timeout_ms, assertions, created_at
		FROM checks
		WHERE project_id = ANY($1)
		ORDER BY id ASC
	`
	rows, err := s.pool.Query(ctx, query, projectIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	checks := make([]Check, 0)
	for rows.Next() {
		var c Check
		var assertionsRaw []byte
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Type, &c.Target, &c.TimeoutMs, &assertionsRaw, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Assertions = unmarshalAssertions(assertionsRaw)
		checks = append(checks, c)
	}
	return checks, rows.Err()
}

func (s *Store) GetCheckContext(ctx context.Context, checkID int64) (CheckContext, error) {
	query := `
		SELECT c.id, c.project_id, c.type, c.target, c.timeout_ms, c.assertions, c.created_at,
		       p.name, p.domain, p.failure_threshold, p.autofix_enabled, p.max_autofix_retries, p.smtp_profile_id, p.alert_emails,
		       p.email_subject_opened, p.email_body_opened, p.email_subject_resolved, p.email_body_resolved,
		       p.email_subject_repeated, p.email_body_repeated, p.email_subject_autofix_limit, p.email_body_autofix_limit,
		       p.check_interval_sec, p.next_check_at, p.created_at
		FROM checks c
		JOIN projects p ON p.id = c.project_id
		WHERE c.id = $1
	`
	var r CheckContext
	var assertionsRaw []byte
	var smtp sql.NullInt64
	err := s.pool.QueryRow(ctx, query, checkID).Scan(
		&r.ID,
		&r.ProjectID,
		&r.Type,
		&r.Target,
		&r.TimeoutMs,
		&assertionsRaw,
		&r.CreatedAt,
		&r.ProjectName,
		&r.ProjectDomain,
		&r.FailureThreshold,
		&r.AutofixEnabled,
		&r.MaxAutofixRetries,
		&smtp,
		&r.AlertEmails,
		&r.EmailSubjectOpened,
		&r.EmailBodyOpened,
		&r.EmailSubjectResolved,
		&r.EmailBodyResolved,
		&r.EmailSubjectRepeated,
		&r.EmailBodyRepeated,
		&r.EmailSubjectAutofixLimit,
		&r.EmailBodyAutofixLimit,
		&r.CheckIntervalSec,
		&r.ProjectNextCheck,
		&r.ProjectCreatedAt,
	)
	if err != nil {
		return CheckContext{}, err
	}
	r.Assertions = unmarshalAssertions(assertionsRaw)
	if smtp.Valid {
		r.ProjectSMTPID = &smtp.Int64
	}
	normalizeCheckContextTemplates(&r)
	return r, nil
}

func normalizeCheckContextTemplates(c *CheckContext) {
	if strings.TrimSpace(c.EmailSubjectOpened) == "" {
		c.EmailSubjectOpened = defaultEmailSubjectOpened
	}
	if strings.TrimSpace(c.EmailBodyOpened) == "" {
		c.EmailBodyOpened = defaultEmailBodyOpened
	}
	if strings.TrimSpace(c.EmailSubjectResolved) == "" {
		c.EmailSubjectResolved = defaultEmailSubjectResolved
	}
	if strings.TrimSpace(c.EmailBodyResolved) == "" {
		c.EmailBodyResolved = defaultEmailBodyResolved
	}
	if strings.TrimSpace(c.EmailSubjectRepeated) == "" {
		c.EmailSubjectRepeated = defaultEmailSubjectRepeated
	}
	if strings.TrimSpace(c.EmailBodyRepeated) == "" {
		c.EmailBodyRepeated = defaultEmailBodyRepeated
	}
	if strings.TrimSpace(c.EmailSubjectAutofixLimit) == "" {
		c.EmailSubjectAutofixLimit = defaultEmailSubjectAutofixLimit
	}
	if strings.TrimSpace(c.EmailBodyAutofixLimit) == "" {
		c.EmailBodyAutofixLimit = defaultEmailBodyAutofixLimit
	}
}
