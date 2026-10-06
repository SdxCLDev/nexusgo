-- Ver docs/08-modelo-datos.md §8.3.1.
CREATE TABLE job_items (
    job_item_id   TEXT PRIMARY KEY,
    job_id        TEXT NOT NULL REFERENCES jobs (job_id),
    external_id   TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('SUCCESS', 'FAILED')),
    data          TEXT,
    error_detail  TEXT,
    created_at    TEXT NOT NULL
);

CREATE INDEX idx_job_items_job_status ON job_items (job_id, status);
