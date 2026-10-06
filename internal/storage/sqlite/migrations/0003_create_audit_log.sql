-- Ver docs/08-modelo-datos.md §8.4.
CREATE TABLE audit_log (
    audit_id          TEXT PRIMARY KEY,
    correlation_id    TEXT NOT NULL,
    job_id            TEXT,
    integration_id    TEXT NOT NULL,
    client_id         TEXT NOT NULL,
    external_system   TEXT NOT NULL,
    direction         TEXT NOT NULL,
    mode              TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (status IN ('INICIADO', 'EXITOSO', 'FALLIDO', 'PARCIAL')),
    request_summary   TEXT,
    response_summary  TEXT,
    error_detail      TEXT,
    started_at        TEXT NOT NULL,
    finished_at       TEXT,
    duration_ms       INTEGER
);

CREATE INDEX idx_audit_log_correlation_id ON audit_log (correlation_id);
CREATE INDEX idx_audit_log_integration_started ON audit_log (integration_id, started_at);
