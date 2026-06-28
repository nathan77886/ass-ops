package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"gorm.io/gorm"
	"os"
	"strings"
)

func argoToken(connection *GormArgoConnection) string {
	token, err := NewArgoSyncer().argoToken(context.Background(), nil, connection)
	if err != nil {
		return ""
	}
	return token
}

func (s *ArgoSyncer) argoCredentialCiphertext(ctx context.Context, db *gorm.DB, connection *GormArgoConnection) (string, error) {
	var credential GormConnectionCredential
	err := db.WithContext(ctx).
		Where(&GormConnectionCredential{Kind: "argo_token"}).
		Where("id = ? AND project_id = ?", connection.CredentialID.String, connection.ProjectID).
		First(&credential).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", ErrNotFound
		}
		return "", err
	}
	if strings.TrimSpace(credential.SecretCiphertext) == "" {
		return "", fmt.Errorf("Argo credential has no token configured")
	}
	return credential.SecretCiphertext, nil
}

func decryptArgoCredentialSecret(ciphertext, material string) (string, error) {
	parts := strings.Split(strings.TrimSpace(ciphertext), ":")
	if len(parts) == 3 && parts[0] == "v1" {
		parts = parts[1:]
	} else if len(parts) != 2 {
		return "", fmt.Errorf("invalid credential ciphertext")
	}
	nonce, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decoding credential nonce: %w", err)
	}
	sealed, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding credential ciphertext: %w", err)
	}
	sum := sha256.Sum256([]byte("assops:webhook-secret-encryption:" + material))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting credential: %w", err)
	}
	return string(plain), nil
}

func argoCredentialSecretKeyMaterial() string {
	if value := strings.TrimSpace(os.Getenv("ASSOPS_WEBHOOK_SECRET_KEY")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("ASSOPS_JWT_SECRET")); value != "" {
		return value
	}
	return "dev-assops-webhook-change-me"
}
