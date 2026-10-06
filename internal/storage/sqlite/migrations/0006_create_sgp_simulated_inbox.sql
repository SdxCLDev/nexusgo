-- Tabla de simulación: hace las veces de la base de datos de SGP para
-- probar el modo de entrega push_db sin acceso real a SGP — ver
-- docs/05-patron-asincrono.md §5.5(a) y docs/10-plan-de-trabajo-poc.md Fase 5.
-- No tiene equivalente en el modelo de datos "real" (docs/08-modelo-datos.md):
-- es exclusiva de esta PoC y se elimina cuando exista acceso real a SGP.
CREATE TABLE sgp_simulated_inbox (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id        TEXT NOT NULL,
    external_id   TEXT NOT NULL,
    data          TEXT NOT NULL,
    inserted_at   TEXT NOT NULL
);

CREATE INDEX idx_sgp_simulated_inbox_job ON sgp_simulated_inbox (job_id);
