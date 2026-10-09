-- Ver docs/08-modelo-datos.md §8.5 y docs/06-autenticacion-seguridad.md §6.4.
-- encrypted_payload guarda el payload (usuario/clave, client_secret, etc.)
-- cifrado en reposo. La PK es (external_system, environment) para mantener
-- credenciales separadas por ambiente.
CREATE TABLE external_credentials (
    external_system   TEXT NOT NULL,
    environment       TEXT NOT NULL CHECK (environment IN ('DEV', 'TEST', 'PROD')),
    credential_type   TEXT NOT NULL CHECK (credential_type IN ('BASIC', 'OAUTH2', 'API_KEY', 'CERTIFICATE')),
    encrypted_payload TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (external_system, environment)
);
