package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// HashAPIKey calcula el hash de una API Key para almacenamiento.
//
// Nota: docs/06-autenticacion-seguridad.md §6.1.5 menciona bcrypt/argon2 como
// referencia, pensados para secretos de baja entropía (contraseñas elegidas
// por personas). Las API Keys de Nexus son generadas aleatoriamente con alta
// entropía (ver GenerateAPIKey), por lo que SHA-256 es suficiente y evita
// agregar una dependencia externa — mismo criterio que el resto del proyecto.
func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// VerifyAPIKey compara en tiempo constante una API Key contra su hash almacenado.
func VerifyAPIKey(raw, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashAPIKey(raw)), []byte(hash)) == 1
}

// GenerateAPIKey crea una API Key aleatoria de alta entropía (32 bytes).
func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
