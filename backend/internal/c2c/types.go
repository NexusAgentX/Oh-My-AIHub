// Package c2c holds the C2C sell-order market. Feature A keeps only the
// private-data keyring (C2C_PRIVATE_DATA_KEYRING); Feature C builds the
// order and trade domain on top of it.
package c2c

import "errors"

var ErrInvalidInput = errors.New("invalid C2C input")

// EncryptedValue is one AEAD-sealed private value bound to its record,
// purpose and key ID.
type EncryptedValue struct {
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}
