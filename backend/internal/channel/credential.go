package channel

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

type Keyring struct {
	activeKeyID string
	keys        map[string][]byte
}

func ParseKeyring(encoded, activeKeyID string) (*Keyring, error) {
	activeKeyID = strings.TrimSpace(activeKeyID)
	if strings.TrimSpace(encoded) == "" || activeKeyID == "" {
		return nil, errors.New("channel credential keyring and active key ID are required")
	}
	keys := make(map[string][]byte)
	for _, item := range strings.Split(encoded, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("invalid channel credential keyring entry")
		}
		keyID := strings.TrimSpace(parts[0])
		if len(keyID) > 64 {
			return nil, fmt.Errorf("channel credential key ID is too long")
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("channel credential key %q must be 32 bytes encoded as base64", keyID)
		}
		if _, exists := keys[keyID]; exists {
			return nil, fmt.Errorf("duplicate channel credential key ID %q", keyID)
		}
		keys[keyID] = key
	}
	if _, exists := keys[activeKeyID]; !exists {
		return nil, fmt.Errorf("active channel credential key %q is absent", activeKeyID)
	}
	return &Keyring{activeKeyID: activeKeyID, keys: keys}, nil
}

func (k *Keyring) ActiveKeyID() string { return k.activeKeyID }

func (k *Keyring) Encrypt(channelID string, version int64, plaintext string) (EncryptedCredential, error) {
	if channelID == "" || version <= 0 || strings.TrimSpace(plaintext) == "" {
		return EncryptedCredential{}, ErrInvalidInput
	}
	key := k.keys[k.activeKeyID]
	block, err := aes.NewCipher(key)
	if err != nil {
		return EncryptedCredential{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return EncryptedCredential{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return EncryptedCredential{}, err
	}
	ciphertext := aead.Seal(nil, nonce, []byte(plaintext), credentialAAD(channelID, version, k.activeKeyID))
	return EncryptedCredential{Version: version, KeyID: k.activeKeyID, Nonce: nonce, Ciphertext: ciphertext}, nil
}

func (k *Keyring) Decrypt(channelID string, encrypted EncryptedCredential) (string, error) {
	key, exists := k.keys[encrypted.KeyID]
	if !exists {
		return "", fmt.Errorf("channel credential key %q is unavailable", encrypted.KeyID)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(encrypted.Nonce) != aead.NonceSize() {
		return "", errors.New("channel credential nonce is invalid")
	}
	plaintext, err := aead.Open(nil, encrypted.Nonce, encrypted.Ciphertext, credentialAAD(channelID, encrypted.Version, encrypted.KeyID))
	if err != nil {
		return "", fmt.Errorf("decrypt channel credential: %w", err)
	}
	return string(plaintext), nil
}

func credentialAAD(channelID string, version int64, keyID string) []byte {
	var encoded bytes.Buffer
	_ = binary.Write(&encoded, binary.BigEndian, uint32(len(channelID)))
	encoded.WriteString(channelID)
	_ = binary.Write(&encoded, binary.BigEndian, version)
	_ = binary.Write(&encoded, binary.BigEndian, uint32(len(keyID)))
	encoded.WriteString(keyID)
	return encoded.Bytes()
}
