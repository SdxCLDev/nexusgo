-- Ver docs/08-modelo-datos.md §8.2.
CREATE TABLE integrations (
    integration_id    TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    external_system   TEXT NOT NULL,
    direction         TEXT NOT NULL CHECK (direction IN ('OUTBOUND', 'INBOUND', 'BIDIRECTIONAL')),
    mode              TEXT NOT NULL CHECK (mode IN ('SYNC', 'ASYNC')),
    delivery_mode     TEXT CHECK (delivery_mode IS NULL OR delivery_mode IN ('push_db', 'pull_api')),
    version           TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);
