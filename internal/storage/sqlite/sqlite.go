// Package sqlite implementa la persistencia de Nexus sobre SQLite para la
// fase de prueba de concepto — ver docs/08-modelo-datos.md §8.8. El driver
// es modernc.org/sqlite (reimplementación de SQLite en Go puro): no requiere
// CGO ni un compilador de C, lo que mantiene a Nexus como un binario único
// y facilita compilar en cualquier máquina de desarrollo o CI.
//
// Las migraciones (migrations/*.sql) se embeben en el binario con go:embed,
// consistente con el objetivo de distribución como ejecutable único. Se
// aplican con un runner mínimo hecho a mano (sin golang-migrate ni otra
// dependencia): cada archivo se ejecuta una sola vez, en orden, dentro de
// una transacción, y se registra en schema_migrations. Para los migrations
// puramente DDL de esta PoC es suficiente y evita sumar una dependencia más.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open abre (o crea) la base de datos SQLite en dbPath y aplica las
// migraciones pendientes antes de devolver la conexión.
func Open(ctx context.Context, dbPath string) (*sql.DB, error) {
	// busy_timeout evita errores "database is locked" ante escrituras
	// concurrentes; journal_mode=WAL permite lectores concurrentes con un
	// único escritor — ver la limitación de concurrencia de SQLite anotada
	// en docs/08-modelo-datos.md §8.8.
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: no se pudo abrir %q: %w", dbPath, err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: no se pudo conectar a %q: %w", dbPath, err)
	}

	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("sqlite: no se pudo crear schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("sqlite: no se pudieron leer las migraciones embebidas: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // los nombres 0001_, 0002_, ... definen el orden de aplicación.

	for _, name := range names {
		if err := applyMigrationIfPending(ctx, db, name); err != nil {
			return err
		}
	}
	return nil
}

func applyMigrationIfPending(ctx context.Context, db *sql.DB, name string) error {
	var applied int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, name).Scan(&applied); err != nil {
		return fmt.Errorf("sqlite: no se pudo verificar la migración %q: %w", name, err)
	}
	if applied > 0 {
		return nil
	}

	contents, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo leer la migración %q: %w", name, err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo iniciar transacción para %q: %w", name, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op si ya hubo Commit

	for _, stmt := range splitStatements(string(contents)) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("sqlite: error aplicando la migración %q: %w", name, err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		name, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("sqlite: no se pudo registrar la migración %q: %w", name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: no se pudo confirmar la migración %q: %w", name, err)
	}
	return nil
}

// splitStatements separa un archivo de migración en sentencias individuales.
// Alcanza con partir por ";" porque las migraciones de esta PoC son DDL
// simple (CREATE TABLE/INDEX) sin cadenas de texto que contengan ";".
func splitStatements(sqlText string) []string {
	raw := strings.Split(sqlText, ";")
	stmts := make([]string, 0, len(raw))
	for _, s := range raw {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			stmts = append(stmts, trimmed)
		}
	}
	return stmts
}
