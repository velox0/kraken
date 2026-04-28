package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type UptimePoint struct {
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	UpSeconds      int       `json:"up_seconds"`
	DownSeconds    int       `json:"down_seconds"`
	UnknownSeconds int       `json:"unknown_seconds"`
	UptimeRatio    float64   `json:"uptime_ratio"`
}

type UptimeState struct {
	CurrentStatus string    `json:"current_status"`
	CursorAt      time.Time `json:"cursor_at"`
}

type uptimeAgg struct {
	up   int
	down int
}

func (s *Store) RecordProjectUptimeStatus(ctx context.Context, projectID int64, status string, at time.Time) error {
	if status != "up" && status != "down" {
		return fmt.Errorf("invalid uptime status: %s", status)
	}
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	freshnessWindow, err := projectUptimeFreshnessWindow(ctx, tx, projectID)
	if err != nil {
		return err
	}

	var currentStatus string
	var cursorAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT current_status, cursor_at
		FROM project_uptime_state
		WHERE project_id=$1
		FOR UPDATE
	`, projectID).Scan(&currentStatus, &cursorAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `
				INSERT INTO project_uptime_state(project_id, current_status, cursor_at, updated_at)
				VALUES($1, $2, $3, NOW())
			`, projectID, status, at)
			if err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		return err
	}

	if !at.After(cursorAt) {
		_, err = tx.Exec(ctx, `
			UPDATE project_uptime_state
			SET current_status=$2, updated_at=NOW()
			WHERE project_id=$1
		`, projectID, status)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	carryEnd := at
	if at.Sub(cursorAt) > freshnessWindow {
		carryEnd = cursorAt.Add(freshnessWindow)
	}
	if err := accumulateUptimeDurationTx(ctx, tx, projectID, currentStatus, cursorAt, carryEnd); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE project_uptime_state
		SET current_status=$2, cursor_at=$3, updated_at=NOW()
		WHERE project_id=$1
	`, projectID, status, at)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetUptimeSeries(ctx context.Context, projectID int64, from, to time.Time, bucketSize time.Duration) ([]UptimePoint, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("invalid uptime range")
	}
	if bucketSize <= 0 {
		return nil, fmt.Errorf("invalid bucket size")
	}

	from = from.UTC()
	to = to.UTC()
	interval := fmt.Sprintf("%d seconds", int(bucketSize.Seconds()))
	freshnessWindow, err := projectUptimeFreshnessWindow(ctx, s.pool, projectID)
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT
			date_bin($3::interval, bucket_start, $2::timestamptz) AS slot_start,
			COALESCE(SUM(up_seconds), 0)::INT AS up_seconds,
			COALESCE(SUM(down_seconds), 0)::INT AS down_seconds
		FROM project_uptime_minutes
		WHERE project_id=$1
		  AND bucket_start >= $2
		  AND bucket_start < $4
		GROUP BY slot_start
		ORDER BY slot_start ASC
	`, projectID, from, interval, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bySlot := make(map[time.Time]uptimeAgg)
	for rows.Next() {
		var slotStart time.Time
		var upSeconds int
		var downSeconds int
		if err := rows.Scan(&slotStart, &upSeconds, &downSeconds); err != nil {
			return nil, err
		}
		bySlot[slotStart.UTC()] = uptimeAgg{up: upSeconds, down: downSeconds}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Add tail from last cursor to now for near-real-time uptime.
	state, err := s.getUptimeState(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state != nil {
		tailStart := state.CursorAt.UTC()
		if tailStart.Before(from) {
			tailStart = from
		}
		tailEnd := time.Now().UTC()
		if tailEnd.After(to) {
			tailEnd = to
		}
		maxTailEnd := state.CursorAt.UTC().Add(freshnessWindow)
		if tailEnd.After(maxTailEnd) {
			tailEnd = maxTailEnd
		}
		if tailEnd.After(tailStart) {
			addTailToSlots(bySlot, from, bucketSize, state.CurrentStatus, tailStart, tailEnd)
		}
	}

	points := make([]UptimePoint, 0)
	for slot := from; slot.Before(to); slot = slot.Add(bucketSize) {
		bucketEnd := slot.Add(bucketSize)
		if bucketEnd.After(to) {
			bucketEnd = to
		}
		durationSec := int(bucketEnd.Sub(slot).Seconds())
		if durationSec <= 0 {
			continue
		}

		val := bySlot[slot]
		up := val.up
		down := val.down
		if up < 0 {
			up = 0
		}
		if down < 0 {
			down = 0
		}
		if up > durationSec {
			up = durationSec
		}
		if down > durationSec {
			down = durationSec
		}

		known := up + down
		if known > durationSec {
			overflow := known - durationSec
			if up >= down {
				up -= overflow
				if up < 0 {
					up = 0
				}
			} else {
				down -= overflow
				if down < 0 {
					down = 0
				}
			}
			known = up + down
		}
		if known > 0 && known < durationSec {
			missing := durationSec - known
			if down > up {
				down += missing
			} else {
				up += missing
			}
		}

		ratio := 0.0
		known = up + down
		if known > 0 {
			ratio = float64(up) / float64(known)
		}

		points = append(points, UptimePoint{
			Start:          slot,
			End:            bucketEnd,
			UpSeconds:      up,
			DownSeconds:    down,
			UnknownSeconds: 0,
			UptimeRatio:    ratio,
		})
	}
	return points, nil
}

func (s *Store) getUptimeState(ctx context.Context, projectID int64) (*UptimeState, error) {
	var state UptimeState
	err := s.pool.QueryRow(ctx, `
		SELECT current_status, cursor_at
		FROM project_uptime_state
		WHERE project_id=$1
	`, projectID).Scan(&state.CurrentStatus, &state.CursorAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

func accumulateUptimeDurationTx(ctx context.Context, tx pgx.Tx, projectID int64, status string, start, end time.Time) error {
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		return nil
	}

	for cursor := start; cursor.Before(end); {
		minuteStart := cursor.Truncate(time.Minute)
		minuteEnd := minuteStart.Add(time.Minute)
		if minuteEnd.After(end) {
			minuteEnd = end
		}

		seconds := int(minuteEnd.Sub(cursor).Seconds())
		if seconds <= 0 {
			break
		}

		up := 0
		down := 0
		if status == "up" {
			up = seconds
		} else {
			down = seconds
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO project_uptime_minutes(project_id, bucket_start, up_seconds, down_seconds)
			VALUES($1, $2, $3, $4)
			ON CONFLICT(project_id, bucket_start)
			DO UPDATE SET
				up_seconds = LEAST(60, project_uptime_minutes.up_seconds + EXCLUDED.up_seconds),
				down_seconds = LEAST(60, project_uptime_minutes.down_seconds + EXCLUDED.down_seconds)
		`, projectID, minuteStart, up, down)
		if err != nil {
			return err
		}
		cursor = minuteEnd
	}
	return nil
}

func addTailToSlots(bySlot map[time.Time]uptimeAgg, origin time.Time, bucketSize time.Duration, status string, start, end time.Time) {
	for cursor := start; cursor.Before(end); {
		slotStart := alignToSlot(origin, bucketSize, cursor)
		slotEnd := slotStart.Add(bucketSize)
		if slotEnd.After(end) {
			slotEnd = end
		}
		seconds := int(slotEnd.Sub(cursor).Seconds())
		if seconds <= 0 {
			break
		}

		entry := bySlot[slotStart]
		if status == "up" {
			entry.up += seconds
		} else {
			entry.down += seconds
		}
		bySlot[slotStart] = entry
		cursor = slotEnd
	}
}

func alignToSlot(origin time.Time, bucketSize time.Duration, ts time.Time) time.Time {
	if !ts.After(origin) {
		return origin
	}
	delta := ts.Sub(origin)
	steps := int64(delta / bucketSize)
	return origin.Add(time.Duration(steps) * bucketSize)
}

type uptimeRowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func projectUptimeFreshnessWindow(ctx context.Context, q uptimeRowQuerier, projectID int64) (time.Duration, error) {
	var intervalSec int
	err := q.QueryRow(ctx, `SELECT check_interval_sec FROM projects WHERE id=$1`, projectID).Scan(&intervalSec)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("project %d not found", projectID)
		}
		return 0, err
	}
	if intervalSec <= 0 {
		intervalSec = 30
	}
	freshnessSec := intervalSec * 4
	if freshnessSec < 90 {
		freshnessSec = 90
	}
	if freshnessSec > 3600 {
		freshnessSec = 3600
	}
	return time.Duration(freshnessSec) * time.Second, nil
}
