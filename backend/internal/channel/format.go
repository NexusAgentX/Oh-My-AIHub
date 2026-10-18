// Package channel holds the upstream relay boundary. Feature A keeps only the
// versioned credential keyring (ADR-0036) and the pinned outbound policy;
// Feature B builds channels, discovery and format tests on top of them.
package channel

import (
	"errors"
	"unicode"
)

// Format identifies a native forwarding API family.
type Format string

const (
	FormatOpenAIChat      Format = "openai_chat"
	FormatOpenAIResponses Format = "openai_responses"
	FormatAnthropic       Format = "anthropic"
	FormatGemini          Format = "gemini"
	FormatOpenAIImages    Format = "openai_images"
)

var ErrInvalidInput = errors.New("invalid channel input")
var ErrInvalidBaseURL = errors.New("invalid channel base URL")

// EncryptedCredential is one AEAD-sealed secret bound to its owner record,
// version and key ID.
type EncryptedCredential struct {
	Version    int64
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

// StoredCredential is a sealed credential together with the ID of the record
// it is bound to (a channel or a platform API key), as the startup keyring
// check reads it back from the database.
type StoredCredential struct {
	RecordID string
	Sealed   EncryptedCredential
}

func validCredential(value string) bool {
	if value == "" || len([]byte(value)) > 8192 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
