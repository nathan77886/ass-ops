package app

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func testEncryptedPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	block, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key), []byte("passphrase"), x509.PEMCipherAES256)
	if err != nil {
		t.Fatalf("encrypt pem block: %v", err)
	}
	return pem.EncodeToMemory(block)
}

func TestTruncateOutput(t *testing.T) {
	got := truncateOutput("abcdef", 3)
	if got != "abc\n[truncated]" {
		t.Fatalf("truncateOutput = %q", got)
	}
}

func TestSanitizeSSHOutput(t *testing.T) {
	got := sanitizeSSHOutput("AWS_SECRET_ACCESS_KEY=abc123\nAuthorization: Bearer token-value\nPASSWORD=hunter2\nSSHPASS=secret-pass\ncurl -u user:pass --password pass2\njwt=eyJhbGciOiJIUzI1NiJ9.abc.def")
	for _, leaked := range []string{"abc123", "token-value", "hunter2", "secret-pass", "user:pass", "pass2", "eyJhbGciOiJIUzI1NiJ9.abc.def"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sanitizeSSHOutput leaked %q in %q", leaked, got)
		}
	}
}
