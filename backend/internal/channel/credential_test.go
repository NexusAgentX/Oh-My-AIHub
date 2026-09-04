package channel

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func encodedKey(value byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

func TestKeyringFailsClosedAndEncryptsWithBoundAAD(t *testing.T) {
	for _, test := range []struct {
		encoded string
		active  string
	}{
		{"", "v1"},
		{"v1=" + encodedKey(1), ""},
		{"v1=" + encodedKey(1), "v2"},
		{"v1=not-base64", "v1"},
		{"v1=" + base64.StdEncoding.EncodeToString([]byte("short")), "v1"},
		{"v1=" + encodedKey(1) + ",v1=" + encodedKey(2), "v1"},
	} {
		if _, err := ParseKeyring(test.encoded, test.active); err == nil {
			t.Fatalf("ParseKeyring(%q, %q) unexpectedly succeeded", test.encoded, test.active)
		}
	}

	keyring, err := ParseKeyring("old="+encodedKey(1)+",active="+encodedKey(2), "active")
	if err != nil {
		t.Fatal(err)
	}
	first, err := keyring.Encrypt("00000000-0000-4000-8000-000000000001", 4, "upstream-secret")
	if err != nil {
		t.Fatal(err)
	}
	second, err := keyring.Encrypt("00000000-0000-4000-8000-000000000001", 4, "upstream-secret")
	if err != nil {
		t.Fatal(err)
	}
	if first.KeyID != "active" || bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatalf("encryption did not use the active key and random nonce: %#v %#v", first, second)
	}
	plaintext, err := keyring.Decrypt("00000000-0000-4000-8000-000000000001", first)
	if err != nil || plaintext != "upstream-secret" {
		t.Fatalf("Decrypt = %q, %v", plaintext, err)
	}
	if _, err := keyring.Decrypt("00000000-0000-4000-8000-000000000002", first); err == nil {
		t.Fatal("credential decrypted under a different channel ID")
	}
	wrongVersion := first
	wrongVersion.Version++
	if _, err := keyring.Decrypt("00000000-0000-4000-8000-000000000001", wrongVersion); err == nil {
		t.Fatal("credential decrypted under a different semantic version")
	}
	tampered := first
	tampered.Ciphertext = append([]byte(nil), first.Ciphertext...)
	tampered.Ciphertext[0] ^= 1
	if _, err := keyring.Decrypt("00000000-0000-4000-8000-000000000001", tampered); err == nil {
		t.Fatal("tampered credential decrypted")
	}
}

func TestCredentialValidationRejectsHeaderControlCharacters(t *testing.T) {
	for _, value := range []string{"", "line\nbreak", "carriage\rreturn", "nul\x00byte", strings.Repeat("x", 8193)} {
		if validCredential(value) {
			t.Fatalf("credential %q unexpectedly valid", value)
		}
	}
	for _, value := range []string{"k", "short-key", "  exact-secret-with-spaces  "} {
		if !validCredential(value) {
			t.Fatalf("credential %q unexpectedly rejected", value)
		}
	}
}
