package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Cipher cifra y descifra el payload de credenciales para almacenamiento en
// reposo — ver docs/06-autenticacion-seguridad.md §6.4.
type Cipher interface {
	Encrypt(plaintext []byte) (string, error)
	Decrypt(ciphertext string) ([]byte, error)
}

// aesGCMCipher cifra con AES-256-GCM. La clave se deriva de un secreto
// arbitrario (variable de entorno) con SHA-256, de modo que la longitud del
// secreto no importe. El nonce aleatorio de cada mensaje se antepone al
// ciphertext; todo se codifica en base64 para guardarlo como TEXT.
type aesGCMCipher struct {
	gcm cipher.AEAD
}

// NewAESGCMCipher construye un cifrador AES-256-GCM a partir de un secreto.
func NewAESGCMCipher(secret string) (Cipher, error) {
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("credentials: no se pudo crear el cifrador AES: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("credentials: no se pudo crear GCM: %w", err)
	}
	return &aesGCMCipher{gcm: gcm}, nil
}

func (c *aesGCMCipher) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("credentials: no se pudo generar nonce: %w", err)
	}
	sealed := c.gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *aesGCMCipher) Decrypt(ciphertext string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("credentials: ciphertext base64 inválido: %w", err)
	}
	nonceSize := c.gcm.NonceSize()
	if len(raw) < nonceSize {
		return nil, fmt.Errorf("credentials: ciphertext demasiado corto")
	}
	nonce, sealed := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := c.gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("credentials: no se pudo descifrar (clave incorrecta o dato corrupto): %w", err)
	}
	return plaintext, nil
}

// plaintextCipher NO cifra: guarda el payload en claro (marcado con un prefijo
// para que sea evidente en la base de datos). Es un placeholder solo para
// desarrollo local cuando no se configuró NEXUS_CRED_KEY; el cifrado real
// (AES-GCM) es obligatorio fuera de 'dev' — ver docs/10-plan-de-trabajo-poc.md
// Fase 6/9 y docs/06-autenticacion-seguridad.md §6.4.
type plaintextCipher struct{}

const plaintextPrefix = "plaintext:"

func (plaintextCipher) Encrypt(plaintext []byte) (string, error) {
	return plaintextPrefix + base64.StdEncoding.EncodeToString(plaintext), nil
}

func (plaintextCipher) Decrypt(ciphertext string) ([]byte, error) {
	raw := strings.TrimPrefix(ciphertext, plaintextPrefix)
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("credentials: payload en claro inválido: %w", err)
	}
	return b, nil
}

// NewCipher elige el cifrador según el secreto y el ambiente. Con secreto no
// vacío usa AES-GCM. Sin secreto, solo en 'dev' cae al placeholder sin cifrar
// (registrando una advertencia); en cualquier otro ambiente es un error, para
// no arrancar producción con credenciales en claro.
func NewCipher(secret, env string, logger *slog.Logger) (Cipher, error) {
	if secret != "" {
		return NewAESGCMCipher(secret)
	}
	if env == "dev" {
		if logger != nil {
			logger.Warn("NEXUS_CRED_KEY no está definido: las credenciales externas se guardarán SIN CIFRAR (solo aceptable en 'dev')")
		}
		return plaintextCipher{}, nil
	}
	return nil, fmt.Errorf("credentials: NEXUS_CRED_KEY es obligatorio fuera del ambiente 'dev'")
}
