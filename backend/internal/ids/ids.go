// Package ids generates random version-4 UUIDs for records whose ID must be
// known before insertion (for example because it is bound into an AEAD).
package ids

import (
	"crypto/rand"
	"fmt"
)

func NewUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}
