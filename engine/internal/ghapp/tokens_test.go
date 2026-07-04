package ghapp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

// TestAppJWT: un PEM RSA válido produce un JWT de 3 partes; un PEM roto falla claro.
func TestAppJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	jwt, err := AppJWT(Credentials{AppID: 42, PEM: string(pemBytes)})
	if err != nil {
		t.Fatalf("AppJWT: %v", err)
	}
	if parts := strings.Split(jwt, "."); len(parts) != 3 {
		t.Fatalf("JWT debe tener 3 partes, tiene %d", len(parts))
	}
	if _, err := AppJWT(Credentials{AppID: 1, PEM: "no-es-pem"}); err == nil {
		t.Fatal("PEM inválido debe fallar")
	}
}
