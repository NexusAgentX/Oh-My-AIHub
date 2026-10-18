package market

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// CredentialScope compares an actual upstream secret inside one engine/group
// without retaining the secret or making encryption-key rotation an identity
// change. Never expose this equality tag in consumer responses.
func CredentialScope(root []byte, groupID string, secret []byte) (string, error) {
	if len(root) != 32 || groupID == "" || len(secret) == 0 {
		return "", ErrInvalid
	}
	h := hmac.New(sha256.New, root)
	h.Write([]byte("aihub/credential-scope/v1"))
	var length [8]byte
	for _, value := range [][]byte{[]byte(groupID), secret} {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		h.Write(length[:])
		h.Write(value)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
