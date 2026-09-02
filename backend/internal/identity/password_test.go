package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("A-long-enough-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(hash, "A-long-enough-password") {
		t.Fatal("hashed password did not verify")
	}
	if VerifyPassword(hash, "not-the-password") {
		t.Fatal("wrong password verified")
	}
}

func TestGenerateInitialPasswordUsesFreshRandomBytes(t *testing.T) {
	first, err := GenerateInitialPassword()
	if err != nil {
		t.Fatalf("GenerateInitialPassword: %v", err)
	}
	second, err := GenerateInitialPassword()
	if err != nil {
		t.Fatalf("GenerateInitialPassword second call: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(decoded) != 18 {
		t.Fatalf("initial password decoded length = %d, err = %v, want 18 bytes", len(decoded), err)
	}
	if first == second {
		t.Fatal("two generated initial passwords unexpectedly matched")
	}
}

func TestNewSessionStoresOnlyTokenDigest(t *testing.T) {
	service := &Service{now: time.Now, sessionLifetime: 24 * time.Hour}
	token, session, err := service.newSession("account-id", 7)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("session token decoded length = %d, err = %v, want 32 bytes", len(raw), err)
	}
	want := sha256.Sum256([]byte(token))
	if !bytes.Equal(session.TokenHash, want[:]) {
		t.Fatal("stored session token hash does not match SHA-256 digest")
	}
	if bytes.Equal(session.TokenHash, []byte(token)) {
		t.Fatal("session stored the raw token instead of a digest")
	}
	if session.PasswordVersion != 7 || session.AccountID != "account-id" {
		t.Fatalf("session binding = %+v", session)
	}
}

func TestVerifyPasswordRejectsUnsafePHCParameters(t *testing.T) {
	for _, encoded := range []string{
		"$argon2id$v=19$m=0,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=65536,t=0,p=2$c2FsdHNhbHRzYWx0c2FsdA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=65536,t=3,p=0$c2FsdHNhbHRzYWx0c2FsdA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=65536,t=3,p=2$c2hvcnQ$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		if VerifyPassword(encoded, "password") {
			t.Fatalf("unsafe PHC string unexpectedly verified: %q", encoded)
		}
	}
}
