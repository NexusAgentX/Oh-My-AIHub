package gatewaycontract

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode"
)

// ContextNamespace is the maintainer-approved, channel-independent namespace.
// F7/F8 persist this random root encrypted with the existing keyring. It must
// not be regenerated on restart or derived from the active encryption-key ID.
// Only identifiers are transformed; no message/history/note content is accepted.
type ContextNamespace struct{ key [32]byte }

func NewContextNamespace(key []byte) (*ContextNamespace, error) {
	if len(key) != 32 {
		return nil, ErrConflict
	}
	n := &ContextNamespace{}
	copy(n.key[:], key)
	return n, nil
}

func (n *ContextNamespace) Identifier(actor Principal, original string) (string, error) {
	if !actor.Active || actor.AccountID == "" || actor.KeyID == "" {
		return "", ErrInactive
	}
	if n == nil || len(original) == 0 || len(original) > 256 || strings.ContainsFunc(original, unicode.IsControl) {
		return "", ErrConflict
	}
	mac := hmac.New(sha256.New, n.key[:])
	_, _ = mac.Write([]byte("aihub/codex-context/v1"))
	for _, s := range []string{actor.AccountID, actor.KeyID, original} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(s)))
		_, _ = mac.Write(length[:])
		_, _ = mac.Write([]byte(s))
	}
	raw := mac.Sum(nil)[:16]
	// UUIDv8 wire shape is accepted wherever the fixed CLI sends UUIDs. HMAC,
	// rather than a visible owner prefix, prevents guessing another namespace.
	raw[6] = raw[6]&0x0f | 0x80
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:]), nil
}
