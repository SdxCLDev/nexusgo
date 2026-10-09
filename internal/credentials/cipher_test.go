package credentials

import (
	"strings"
	"testing"
)

func TestAESGCM_RoundTrip(t *testing.T) {
	c, err := NewAESGCMCipher("clave-de-prueba")
	if err != nil {
		t.Fatalf("NewAESGCMCipher: %v", err)
	}
	plaintext := []byte(`{"user":"admin","password":"secreta"}`)

	enc, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(enc, "admin") || strings.Contains(enc, "secreta") {
		t.Errorf("el ciphertext no debería contener el texto en claro: %q", enc)
	}

	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(dec) != string(plaintext) {
		t.Errorf("roundtrip = %q, se esperaba %q", dec, plaintext)
	}
}

func TestAESGCM_WrongKeyFails(t *testing.T) {
	c1, _ := NewAESGCMCipher("clave-A")
	c2, _ := NewAESGCMCipher("clave-B")
	enc, _ := c1.Encrypt([]byte("dato"))
	if _, err := c2.Decrypt(enc); err == nil {
		t.Error("descifrar con otra clave debería fallar")
	}
}

func TestNewCipher_RequiresKeyOutsideDev(t *testing.T) {
	if _, err := NewCipher("", "prod", nil); err == nil {
		t.Error("se esperaba error: NEXUS_CRED_KEY es obligatorio fuera de 'dev'")
	}
	if _, err := NewCipher("", "dev", nil); err != nil {
		t.Errorf("en 'dev' sin clave debería caer al placeholder, no fallar: %v", err)
	}
	if _, err := NewCipher("una-clave", "prod", nil); err != nil {
		t.Errorf("con clave debería construir AES-GCM: %v", err)
	}
}
