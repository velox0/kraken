package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/velox0/kraken/internal/monitor"
)

type LogEntry struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type CheckRun struct {
	ID             int64     `json:"id"`
	CheckID        int64     `json:"check_id"`
	ProjectID      int64     `json:"project_id"`
	Status         string    `json:"status"`
	ResponseTimeMs *int      `json:"response_time_ms,omitempty"`
	ErrorMessage   *string   `json:"error_message,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type RouteHealth struct {
	CheckID            int64               `json:"check_id"`
	Type               string              `json:"type"`
	Target             string              `json:"target"`
	TimeoutMs          int                 `json:"timeout_ms"`
	Assertions         []monitor.Assertion `json:"assertions"`
	LastStatus         string              `json:"last_status"`
	LastCheckedAt      *time.Time          `json:"last_checked_at,omitempty"`
	LastResponseTimeMs *int                `json:"last_response_time_ms,omitempty"`
	LastErrorMessage   *string             `json:"last_error_message,omitempty"`
	Runs1h             int                 `json:"runs_1h"`
	Healthy1h          int                 `json:"healthy_1h"`
	Failed1h           int                 `json:"failed_1h"`
	SuccessRate1h      float64             `json:"success_rate_1h"`
}

func (s *Store) InsertCheckRun(ctx context.Context, checkID int64, projectID int64, status string, responseTimeMs int, errorMessage string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO check_runs(check_id, project_id, status, response_time_ms, error_message)
		VALUES ($1, $2, $3, $4, $5)
	`, checkID, projectID, status, nullableInt(responseTimeMs), nullableString(errorMessage))
	return err
}

func (s *Store) InsertLog(ctx context.Context, projectID int64, level, message string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO logs(project_id, level, message, timestamp)
		VALUES ($1, $2, $3, NOW())
	`, projectID, level, truncate(message, 2048))
	return err
}

func (s *Store) ListLogsByProject(ctx context.Context, projectID int64, limit int) ([]LogEntry, error) {
	limit = clampLimit(limit, 100)
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, level, message, timestamp
		FROM logs
		WHERE project_id=$1
		ORDER BY timestamp DESC
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]LogEntry, 0, limit)
	for rows.Next() {
		var item LogEntry
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Level, &item.Message, &item.Timestamp); err != nil {
			return nil, err
		}
		res = append(res, item)
	}
	return res, rows.Err()
}

func (s *Store) ListIncidentsByProject(ctx context.Context, projectID int64, limit int) ([]Incident, error) {
	limit = clampLimit(limit, 50)
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, status, started_at, resolved_at, error_message, last_alert_sent_at
		FROM incidents
		WHERE project_id=$1
		ORDER BY started_at DESC
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]Incident, 0, limit)
	for rows.Next() {
		var inc Incident
		var resolved sql.NullTime
		var alert sql.NullTime
		if err := rows.Scan(&inc.ID, &inc.ProjectID, &inc.Status, &inc.StartedAt, &resolved, &inc.ErrorMessage, &alert); err != nil {
			return nil, err
		}
		if resolved.Valid {
			t := resolved.Time
			inc.ResolvedAt = &t
		}
		if alert.Valid {
			t := alert.Time
			inc.LastAlertSentAt = &t
		}
		res = append(res, inc)
	}
	return res, rows.Err()
}

func (s *Store) ListCheckRunsByProject(ctx context.Context, projectID int64, limit int) ([]CheckRun, error) {
	limit = clampLimit(limit, 100)
	rows, err := s.pool.Query(ctx, `
		SELECT id, check_id, project_id, status, response_time_ms, error_message, created_at
		FROM check_runs
		WHERE project_id=$1
		ORDER BY created_at DESC
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]CheckRun, 0, limit)
	for rows.Next() {
		var item CheckRun
		var response sql.NullInt32
		var errMessage sql.NullString
		if err := rows.Scan(&item.ID, &item.CheckID, &item.ProjectID, &item.Status, &response, &errMessage, &item.CreatedAt); err != nil {
			return nil, err
		}
		if response.Valid {
			v := int(response.Int32)
			item.ResponseTimeMs = &v
		}
		if errMessage.Valid {
			v := errMessage.String
			item.ErrorMessage = &v
		}
		res = append(res, item)
	}
	return res, rows.Err()
}

func (s *Store) ListCheckRunsByCheck(ctx context.Context, projectID, checkID int64, limit int) ([]CheckRun, error) {
	limit = clampLimit(limit, 120)
	rows, err := s.pool.Query(ctx, `
		SELECT id, check_id, project_id, status, response_time_ms, error_message, created_at
		FROM check_runs
		WHERE project_id=$1 AND check_id=$2
		ORDER BY created_at DESC
		LIMIT $3
	`, projectID, checkID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]CheckRun, 0, limit)
	for rows.Next() {
		var item CheckRun
		var response sql.NullInt32
		var errMessage sql.NullString
		if err := rows.Scan(&item.ID, &item.CheckID, &item.ProjectID, &item.Status, &response, &errMessage, &item.CreatedAt); err != nil {
			return nil, err
		}
		if response.Valid {
			v := int(response.Int32)
			item.ResponseTimeMs = &v
		}
		if errMessage.Valid {
			v := errMessage.String
			item.ErrorMessage = &v
		}
		res = append(res, item)
	}
	return res, rows.Err()
}

func (s *Store) ListRouteHealthByProject(ctx context.Context, projectID int64) ([]RouteHealth, error) {
	rows, err := s.pool.Query(ctx, `
		WITH latest_runs AS (
			SELECT DISTINCT ON (check_id)
				check_id,
				status,
				response_time_ms,
				error_message,
				created_at
			FROM check_runs
			WHERE project_id=$1
			ORDER BY check_id, created_at DESC
		),
		hourly_stats AS (
			SELECT
				check_id,
				COUNT(*) FILTER (WHERE created_at >= NOW() - INTERVAL '1 hour')::INT AS runs_1h,
				COUNT(*) FILTER (WHERE created_at >= NOW() - INTERVAL '1 hour' AND status='healthy')::INT AS healthy_1h,
				COUNT(*) FILTER (WHERE created_at >= NOW() - INTERVAL '1 hour' AND status='failed')::INT AS failed_1h
			FROM check_runs
			WHERE project_id=$1
			GROUP BY check_id
		)
		SELECT
			c.id,
			c.type,
			c.target,
			c.timeout_ms,
			c.assertions,
			COALESCE(l.status, 'unknown') AS last_status,
			l.created_at,
			l.response_time_ms,
			l.error_message,
			COALESCE(h.runs_1h, 0),
			COALESCE(h.healthy_1h, 0),
			COALESCE(h.failed_1h, 0)
		FROM checks c
		LEFT JOIN latest_runs l ON l.check_id = c.id
		LEFT JOIN hourly_stats h ON h.check_id = c.id
		WHERE c.project_id=$1
		ORDER BY c.id ASC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]RouteHealth, 0)
	for rows.Next() {
		var item RouteHealth
		var assertionsRaw []byte
		var lastChecked sql.NullTime
		var responseTime sql.NullInt32
		var errMessage sql.NullString
		if err := rows.Scan(
			&item.CheckID,
			&item.Type,
			&item.Target,
			&item.TimeoutMs,
			&assertionsRaw,
			&item.LastStatus,
			&lastChecked,
			&responseTime,
			&errMessage,
			&item.Runs1h,
			&item.Healthy1h,
			&item.Failed1h,
		); err != nil {
			return nil, err
		}
		item.Assertions = unmarshalAssertions(assertionsRaw)
		if lastChecked.Valid {
			v := lastChecked.Time
			item.LastCheckedAt = &v
		}
		if responseTime.Valid {
			v := int(responseTime.Int32)
			item.LastResponseTimeMs = &v
		}
		if errMessage.Valid {
			v := errMessage.String
			item.LastErrorMessage = &v
		}
		if item.Runs1h > 0 {
			item.SuccessRate1h = float64(item.Healthy1h) / float64(item.Runs1h)
		}
		res = append(res, item)
	}
	return res, rows.Err()
}
