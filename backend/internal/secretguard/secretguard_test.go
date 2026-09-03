package secretguard

import "testing"

func TestProtectUpstreamErrorOnlyBlocksExactCredential(t *testing.T) {
	code, message, blocked := ProtectUpstreamError("vendor_error", "model vendor/example failed at https://relay.example/v1", "secret-value")
	if blocked || code != "vendor_error" || message != "model vendor/example failed at https://relay.example/v1" {
		t.Fatalf("ordinary error changed: %q %q blocked=%v", code, message, blocked)
	}
	code, message, blocked = ProtectUpstreamError("vendor_secret-value", "ignored", "secret-value")
	if !blocked || code != CredentialErrorCode || message != CredentialErrorMessage {
		t.Fatalf("credential echo not blocked: %q %q blocked=%v", code, message, blocked)
	}
	code, message, blocked = ProtectUpstreamError("vendor_error", "Bearer secret-value", "secret-value")
	if !blocked || code != CredentialErrorCode || message != CredentialErrorMessage {
		t.Fatalf("credential envelope not blocked: %q %q blocked=%v", code, message, blocked)
	}
}

func TestContainsExactOrJSONEscaped(t *testing.T) {
	credential := "secret\"with\\escapes"
	encoded := `{"error":"secret\"with\\escapes"}`
	if !ContainsExactOrJSONEscaped(encoded, credential) {
		t.Fatal("JSON-escaped credential was not detected")
	}
}

func TestContainsExactInJSONRecognizesEquivalentEscapes(t *testing.T) {
	credential := `https://relay.example/key`
	if !ContainsExactInJSON([]byte(`{"debug":"https:\/\/relay.example\/k\u0065y"}`), credential) {
		t.Fatal("equivalent JSON escapes bypassed exact credential guard")
	}
	if ContainsExactInJSON([]byte(`{"debug":"https://relay.example/other"}`), credential) {
		t.Fatal("ordinary JSON string was treated as the credential")
	}
	if ContainsExactInJSON([]byte(`not-json https:\/\/relay.example\/k\u0065y`), credential) {
		t.Fatal("invalid JSON should not be decoded as a JSON credential echo")
	}
}
