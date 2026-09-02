package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestChannelDTOsHaveExplicitSensitiveFieldBoundaries(t *testing.T) {
	completed := time.Now()
	item := channel.Channel{
		ID: "channel-id", OwnerAccountID: "owner-id", OwnerDisplayName: "共享者",
		DisplayName: "Aurora", NormalizedBaseURL: "https://private-upstream.example/prefix",
		CredentialConfigured: true, CredentialVersion: 4, Status: channel.StatusPublished,
		Offers: []channel.Offer{{
			ID: "offer-id", ModelID: "openai/model", ModelName: "Model", ModelProvider: "OpenAI",
			Protocol: channel.ProtocolOpenAIResponse, UpstreamModelID: "private-upstream-model",
			Multiplier: money.FromNano(money.Scale), Status: channel.OfferActive, Eligible: true,
			LatestValidation: &channel.ValidationAttempt{
				ID: "attempt-id", Status: channel.ValidationFailed, ErrorCategory: channel.ErrorUpstream,
				RawError: "unredacted owner-only upstream error", CompletedAt: &completed,
			},
		}},
	}
	ownerEncoded, _ := json.Marshal(ownerChannelResponse(item))
	if !strings.Contains(string(ownerEncoded), item.NormalizedBaseURL) || !strings.Contains(string(ownerEncoded), "private-upstream-model") {
		t.Fatalf("owner DTO omitted authorized management fields: %s", ownerEncoded)
	}
	if strings.Contains(string(ownerEncoded), "unredacted owner-only upstream error") {
		t.Fatalf("generic owner DTO exposed raw validation error: %s", ownerEncoded)
	}
	if !strings.Contains(string(ownerEncoded), `"eligible":true`) || !strings.Contains(string(ownerEncoded), `"ineligible_reason":""`) {
		t.Fatalf("owner DTO omitted authoritative routing eligibility: %s", ownerEncoded)
	}
	dedicatedEncoded, _ := json.Marshal(validationResponse(*item.Offers[0].LatestValidation, true))
	if !strings.Contains(string(dedicatedEncoded), "unredacted owner-only upstream error") {
		t.Fatalf("dedicated authorized validation DTO omitted raw error: %s", dedicatedEncoded)
	}
	for name, response := range map[string]map[string]any{
		"market": marketChannelResponse(item),
		"admin":  adminChannelResponse(item),
	} {
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"private-upstream.example", "private-upstream-model", "unredacted owner-only upstream error", "key_id", "nonce", "ciphertext", "base_url", "upstream_model_id", "raw_error", "provider_income"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("%s DTO leaked %q: %s", name, forbidden, encoded)
			}
		}
	}
}

func TestParseMultiplierUsesExactNineDigitFixedPoint(t *testing.T) {
	for _, value := range []string{"0", "0.000000001", "1.25", "1000"} {
		if _, err := parseMultiplier(value); err != nil {
			t.Fatalf("parseMultiplier(%q): %v", value, err)
		}
	}
	for _, value := range []string{"-1", "0.0000000001", "1000.000000001", "1e2", ""} {
		if _, err := parseMultiplier(value); err == nil {
			t.Fatalf("parseMultiplier(%q) unexpectedly succeeded", value)
		}
	}
}
