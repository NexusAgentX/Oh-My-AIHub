// Package channel holds the upstream relay boundary. Feature A keeps only the
// versioned credential keyring (ADR-0009) and the pinned outbound policy;
// Feature B builds channels, discovery and format tests on top of them.
package channel

import (
	"errors"
	"unicode"
)

// Format is one of the four native API formats the gateway forwards without
// conversion.
type Format string

const (
	FormatOpenAIChat      Format = "openai_chat"
	FormatOpenAIResponses Format = "openai_responses"
	FormatAnthropic       Format = "anthropic"
	FormatGemini          Format = "gemini"
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
