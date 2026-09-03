package c2c

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
		return nil, errors.New("C2C private data keyring and active key ID are required")
	}
	keys := make(map[string][]byte)
	for _, item := range strings.Split(encoded, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, errors.New("invalid C2C private data keyring entry")
		}
		keyID := strings.TrimSpace(parts[0])
		if len(keyID) > 64 {
			return nil, errors.New("C2C private data key ID is too long")
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("C2C private data key %q must be 32 bytes encoded as base64", keyID)
		}
		if _, exists := keys[keyID]; exists {
			return nil, fmt.Errorf("duplicate C2C private data key ID %q", keyID)
		}
		keys[keyID] = key
	}
	if _, exists := keys[activeKeyID]; !exists {
		return nil, fmt.Errorf("active C2C private data key %q is absent", activeKeyID)
	}
	return &Keyring{activeKeyID: activeKeyID, keys: keys}, nil
}

func (k *Keyring) ActiveKeyID() string { return k.activeKeyID }

func (k *Keyring) Encrypt(recordID, purpose string, plaintext []byte) (EncryptedValue, error) {
	if k == nil || strings.TrimSpace(recordID) == "" || strings.TrimSpace(purpose) == "" || len(plaintext) == 0 {
		return EncryptedValue{}, ErrInvalidInput
	}
	aead, err := newAEAD(k.keys[k.activeKeyID])
	if err != nil {
		return EncryptedValue{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return EncryptedValue{}, err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, encryptionAAD(recordID, purpose, k.activeKeyID))
	return EncryptedValue{KeyID: k.activeKeyID, Nonce: nonce, Ciphertext: ciphertext}, nil
}

func (k *Keyring) Decrypt(recordID, purpose string, encrypted EncryptedValue) ([]byte, error) {
	if k == nil {
		return nil, errors.New("C2C private data keyring is unavailable")
	}
	key, exists := k.keys[encrypted.KeyID]
	if !exists {
		return nil, fmt.Errorf("C2C private data key %q is unavailable", encrypted.KeyID)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(encrypted.Nonce) != aead.NonceSize() {
		return nil, errors.New("C2C private data nonce is invalid")
	}
	plaintext, err := aead.Open(nil, encrypted.Nonce, encrypted.Ciphertext, encryptionAAD(recordID, purpose, encrypted.KeyID))
	if err != nil {
		return nil, fmt.Errorf("decrypt C2C private data: %w", err)
	}
	return plaintext, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encryptionAAD(recordID, purpose, keyID string) []byte {
	var encoded bytes.Buffer
	for _, value := range []string{"oh-my-aihub:c2c:v1", purpose, recordID, keyID} {
		_ = binary.Write(&encoded, binary.BigEndian, uint32(len(value)))
		encoded.WriteString(value)
	}
	return encoded.Bytes()
}
