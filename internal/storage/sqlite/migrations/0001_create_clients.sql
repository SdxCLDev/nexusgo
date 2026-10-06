-- Ver docs/08-modelo-datos.md §8.1.
CREATE TABLE clients (
    client_id     TEXT PRIMARY KEY,
    api_key_hash  TEXT NOT NULL,
    scopes        TEXT NOT NULL, -- JSON array serializado, ver docs/08-modelo-datos.md §8.8
    status        TEXT NOT NULL CHECK (status IN ('ACTIVE', 'REVOKED')),
    created_at    TEXT NOT NULL, -- ISO 8601 UTC
    last_used_at  TEXT
);
