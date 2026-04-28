package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/velox0/kraken/internal/monitor"
)

type Store struct {
	pool         *pgxpool.Pool
	fixEnvCrypto FixEnvCrypto
}

func New(ctx context.Context, postgresURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, postgresURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func nullInt64Arg(v sql.NullInt64) interface{} {
	if v.Valid {
		return v.Int64
	}
	return nil
}

func nullIntArg(v *int) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

func nullableInt(v int) interface{} {
	if v <= 0 {
		return nil
	}
	return v
}

func nullableString(s string) interface{} {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return truncate(s, 2048)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func clampLimit(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > 500 {
		return 500
	}
	return limit
}

// normalizeAssertions ensures a nil slice is stored as an empty JSON array.
func normalizeAssertions(a []monitor.Assertion) []monitor.Assertion {
	if a == nil {
		return []monitor.Assertion{}
	}
	return a
}

// unmarshalAssertions decodes JSONB bytes into a slice of Assertion.
func unmarshalAssertions(raw []byte) []monitor.Assertion {
	if len(raw) == 0 {
		return []monitor.Assertion{}
	}
	var a []monitor.Assertion
	if err := json.Unmarshal(raw, &a); err != nil {
		return []monitor.Assertion{}
	}
	if a == nil {
		return []monitor.Assertion{}
	}
	return a
}
