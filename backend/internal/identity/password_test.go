package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
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
	if got := phcParams(t, hash); got != productionPHCParams {
		t.Fatalf("HashPassword parameters = %s, want %s", got, productionPHCParams)
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

// 生产参数写成字面量而不是引用常量，这样任何一次意外的参数改动都会让测试失败。
const productionPHCParams = "$m=65536,t=3,p=2$"

func phcParams(t *testing.T, encoded string) string {
	t.Helper()
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		t.Fatalf("encoded hash has %d parts, want 6: %q", len(parts), encoded)
	}
	return "$" + parts[3] + "$"
}

func TestDefaultServiceHashesWithProductionParameters(t *testing.T) {
	service, err := NewService(nil, time.Hour)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if got := phcParams(t, service.dummyHash); got != productionPHCParams {
		t.Fatalf("default service dummy hash parameters = %s, want %s", got, productionPHCParams)
	}
	hash, err := service.hashPassword("A-long-enough-password")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if got := phcParams(t, hash); got != productionPHCParams {
		t.Fatalf("default service hash parameters = %s, want %s", got, productionPHCParams)
	}
}

func TestServiceHonoursExplicitPasswordParameters(t *testing.T) {
	cheap := PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1}
	service, err := NewService(nil, time.Hour, WithPasswordParams(cheap))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if got := phcParams(t, service.dummyHash); got != "$m=8192,t=1,p=1$" {
		t.Fatalf("dummy hash parameters = %s, want $m=8192,t=1,p=1$", got)
	}
	hash, err := service.hashPassword("A-long-enough-password")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if got := phcParams(t, hash); got != "$m=8192,t=1,p=1$" {
		t.Fatalf("hash parameters = %s, want $m=8192,t=1,p=1$", got)
	}
	if !VerifyPassword(hash, "A-long-enough-password") || VerifyPassword(hash, "not-the-password") {
		t.Fatal("hash with explicit parameters did not verify correctly")
	}
}

func TestNewServiceRejectsPasswordParametersOutsideVerifierLimits(t *testing.T) {
	for name, params := range map[string]PasswordParams{
		"memory below floor":       {Memory: 8*1024 - 1, Iterations: 3, Parallelism: 2},
		"memory above ceiling":     {Memory: 256*1024 + 1, Iterations: 3, Parallelism: 2},
		"zero iterations":          {Memory: 64 * 1024, Iterations: 0, Parallelism: 2},
		"iterations above ceiling": {Memory: 64 * 1024, Iterations: 11, Parallelism: 2},
		"zero parallelism":         {Memory: 64 * 1024, Iterations: 3, Parallelism: 0},
		"parallelism above":        {Memory: 64 * 1024, Iterations: 3, Parallelism: 17},
	} {
		if _, err := NewService(nil, time.Hour, WithPasswordParams(params)); err == nil {
			t.Fatalf("%s: NewService accepted %+v", name, params)
		}
	}
}

func TestVerifyPasswordLimitsAreUnchanged(t *testing.T) {
	const tail = "$c2FsdHNhbHRzYWx0c2FsdA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, params := range []string{
		"m=8191,t=3,p=2",
		"m=262145,t=3,p=2",
		"m=65536,t=0,p=2",
		"m=65536,t=11,p=2",
		"m=65536,t=3,p=0",
		"m=65536,t=3,p=17",
	} {
		if VerifyPassword("$argon2id$v=19$"+params+tail, "password") {
			t.Fatalf("verifier accepted out-of-range parameters %s", params)
		}
	}
}
