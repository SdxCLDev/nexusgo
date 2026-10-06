// Package auth implementa la autenticación de entrada de Nexus: emisión y
// verificación de JWT (HS256) y el registro de clientes (API Keys + scopes)
// — ver docs/06-autenticacion-seguridad.md §6.1.
//
// Simplificación aceptada para la PoC: se usa HS256 (clave simétrica) en vez
// de RS256, ya que Nexus corre como un único proceso. Se implementa a mano
// sobre la librería estándar (sin dependencias externas) siguiendo el mismo
// criterio que internal/core/id.go.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Claims son los datos firmados del token — ver docs/06-autenticacion-seguridad.md §6.1.3.
type Claims struct {
	Subject   string   `json:"sub"`
	Scopes    []string `json:"scopes"`
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp"`
	Issuer    string   `json:"iss"`
}

// HasScope indica si el token autoriza el scope indicado.
func (c Claims) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

var ErrInvalidToken = errors.New("token inválido")
var ErrExpiredToken = errors.New("token expirado")

const jwtHeader = `{"alg":"HS256","typ":"JWT"}`

// IssueJWT firma un token HS256 para los claims indicados.
func IssueJWT(secret []byte, claims Claims) (string, error) {
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(jwtHeader))

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64
	signature := sign(secret, signingInput)

	return signingInput + "." + signature, nil
}

// ParseAndVerifyJWT valida la firma y la expiración de un token y devuelve sus claims.
//
// Nota de seguridad: el algoritmo NO se lee del header del token (eso
// habilitaría ataques de confusión de algoritmo, ej. forzar "alg: none");
// siempre se verifica con HMAC-SHA256 usando la clave del servidor.
func ParseAndVerifyJWT(secret []byte, token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	signingInput := parts[0] + "." + parts[1]
	expectedSig := sign(secret, signingInput)
	if !hmac.Equal([]byte(expectedSig), []byte(parts[2])) {
		return nil, ErrInvalidToken
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}

	var claims Claims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, ErrInvalidToken
	}

	if time.Now().Unix() >= claims.ExpiresAt {
		return nil, ErrExpiredToken
	}

	return &claims, nil
}

func sign(secret []byte, signingInput string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
