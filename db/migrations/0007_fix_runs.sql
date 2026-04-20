CREATE TABLE IF NOT EXISTS fix_runs (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    fix_id BIGINT REFERENCES fixes(id) ON DELETE SET NULL,
    fix_name TEXT NOT NULL,
    script_path TEXT NOT NULL,
    trigger TEXT NOT NULL CHECK (trigger IN ('manual', 'autofix')),
    requested_by TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('running', 'success', 'failure')),
    exit_code INTEGER,
    output TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS fix_runs_project_idx ON fix_runs(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS fix_runs_cleanup_idx ON fix_runs(created_at);
