-- Ver docs/08-modelo-datos.md §8.3.
CREATE TABLE jobs (
    job_id              TEXT PRIMARY KEY,
    integration_id      TEXT NOT NULL,
    correlation_id      TEXT NOT NULL,
    client_id           TEXT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'COMPLETED', 'FAILED', 'PARTIAL')),
    delivery_mode       TEXT NOT NULL CHECK (delivery_mode IN ('push_db', 'pull_api')),
    progress_total      INTEGER,
    progress_processed  INTEGER NOT NULL DEFAULT 0,
    progress_failed     INTEGER NOT NULL DEFAULT 0,
    result_summary      TEXT,
    acked_at            TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    finished_at         TEXT
);

CREATE INDEX idx_jobs_correlation_id ON jobs (correlation_id);
CREATE INDEX idx_jobs_status_updated ON jobs (status, updated_at);
