package gateway

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

const apiKeyPrefix = "oma_live_"

func generateAPIKey() (secret string, prefix string, hash [32]byte, err error) {
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return "", "", [32]byte{}, err
	}
	secret = apiKeyPrefix + base64.RawURLEncoding.EncodeToString(random)
	prefix = secret[:min(20, len(secret))]
	hash = sha256.Sum256([]byte(secret))
	return secret, prefix, hash, nil
}

func hashAPIKey(secret string) ([32]byte, error) {
	if !strings.HasPrefix(secret, apiKeyPrefix) || len(secret) < 48 || len(secret) > 128 || strings.TrimSpace(secret) != secret {
		return [32]byte{}, ErrInvalidAPIKey
	}
	return sha256.Sum256([]byte(secret)), nil
}
