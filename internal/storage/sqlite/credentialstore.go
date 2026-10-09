package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"nexusgo/internal/credentials"
)

// CredentialStore implementa credentials.Store sobre SQLite — ver
// docs/08-modelo-datos.md §8.5. El payload se cifra en reposo con el Cipher
// inyectado (ver docs/06-autenticacion-seguridad.md §6.4); Get lo devuelve ya
// descifrado. El ambiente se normaliza a mayúsculas (DEV/TEST/PROD) para
// coincidir con el CHECK de la migración.
type CredentialStore struct {
	db     *sql.DB
	cipher credentials.Cipher
}

func NewCredentialStore(db *sql.DB, cipher credentials.Cipher) *CredentialStore {
	return &CredentialStore{db: db, cipher: cipher}
}

func (s *CredentialStore) Get(ctx context.Context, externalSystem, environment string) (credentials.Credential, bool, error) {
	env := strings.ToUpper(environment)
	var credType, encrypted string
	err := s.db.QueryRowContext(ctx, `
		SELECT credential_type, encrypted_payload FROM external_credentials
		WHERE external_system = ? AND environment = ?
	`, externalSystem, env).Scan(&credType, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return credentials.Credential{}, false, nil
	}
	if err != nil {
		return credentials.Credential{}, false, fmt.Errorf("sqlite: error buscando credenciales de %q (%s): %w", externalSystem, env, err)
	}

	plaintext, err := s.cipher.Decrypt(encrypted)
	if err != nil {
		return credentials.Credential{}, false, fmt.Errorf("sqlite: no se pudo descifrar las credenciales de %q (%s): %w", externalSystem, env, err)
	}

	var payload map[string]string
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return credentials.Credential{}, false, fmt.Errorf("sqlite: payload de credenciales corrupto para %q: %w", externalSystem, err)
	}

	return credentials.Credential{
		ExternalSystem: externalSystem,
		Type:           credType,
		Environment:    env,
		Payload:        payload,
	}, true, nil
}

func (s *CredentialStore) Upsert(ctx context.Context, c credentials.Credential) error {
	plaintext, err := json.Marshal(c.Payload)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo serializar el payload de credenciales: %w", err)
	}
	encrypted, err := s.cipher.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo cifrar las credenciales de %q: %w", c.ExternalSystem, err)
	}

	env := strings.ToUpper(c.Environment)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO external_credentials (external_system, environment, credential_type, encrypted_payload, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(external_system, environment) DO UPDATE SET
			credential_type   = excluded.credential_type,
			encrypted_payload = excluded.encrypted_payload,
			updated_at        = excluded.updated_at
	`, c.ExternalSystem, env, c.Type, encrypted, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo guardar las credenciales de %q (%s): %w", c.ExternalSystem, env, err)
	}
	return nil
}
