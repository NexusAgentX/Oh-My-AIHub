package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestGatewayDTOsHaveExplicitSensitiveFieldBoundaries(t *testing.T) {
	now := time.Now()
	key := gateway.APIKey{
		ID: "key-id", OwnerAccountID: "owner-account-must-not-leak", DisplayName: "开发 Key",
		Prefix: "oai_test", Generation: 2, Status: gateway.KeyActive, Version: 4,
		Pools: []gateway.ModelPool{{
			ID: "pool-id", CanonicalModelID: "model-id", ModelName: "Model", Protocol: channel.ProtocolOpenAIResponse,
			Version: 3, CreatedAt: now, UpdatedAt: now,
			Members: []gateway.PoolMember{{
				Priority: 1, OfferID: "offer-id", ChannelID: "channel-id", ChannelDisplayName: "Aurora",
				OwnerDisplayName: "共享者", Eligible: true, InputPrice: money.FromNano(money.Scale),
			}},
		}},
		CreatedAt: now, UpdatedAt: now,
	}
	encodedKey, err := json.Marshal(apiKeyResponse(key))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"owner-account-must-not-leak", "secret", "hash", "base_url", "upstream_model_id", "credential",
	} {
		if strings.Contains(string(encodedKey), forbidden) {
			t.Fatalf("API Key DTO leaked %q: %s", forbidden, encodedKey)
		}
	}

	call := gateway.Call{
		ID: "call-id", ConsumerAccountID: "consumer-id", APIKeyID: "key-id", KeyPrefix: "oai_test",
		PoolID: "pool-id", CanonicalModelID: "model-id", Protocol: channel.ProtocolOpenAIResponse,
		Status: gateway.CallFailed, Preauthorized: money.FromNano(2 * money.Scale),
		Attempts: []gateway.Attempt{{
			ID: "attempt-id", OfferID: "offer-id", Status: gateway.AttemptFailed,
			RawError: "upstream message retained", Usage: &ledger.UsageV1{InputTokens: 12}, StartedAt: now,
		}},
		CreatedAt: now,
	}
	encodedCall, err := json.Marshal(gatewayCallResponse(call))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedCall), "upstream message retained") {
		t.Fatalf("call DTO omitted the allowed parsed upstream error: %s", encodedCall)
	}
	for _, nullable := range []string{`"ttft_milliseconds":null`, `"duration_milliseconds":null`, `"tokens_per_second":null`} {
		if !strings.Contains(string(encodedCall), nullable) {
			t.Fatalf("call DTO did not preserve missing metric %s: %s", nullable, encodedCall)
		}
	}
	for _, forbidden := range []string{
		"request_body", "response_body", "request_headers", "response_headers", "authorization",
		"api_key_hash", "credential", "base_url", "upstream_model_id",
	} {
		if strings.Contains(string(encodedCall), forbidden) {
			t.Fatalf("gateway call DTO leaked forbidden field %q: %s", forbidden, encodedCall)
		}
	}

	zeroDuration := time.Duration(0)
	zeroNano := int64(0)
	call.Attempts[0].TTFT = &zeroDuration
	call.Attempts[0].Duration = &zeroDuration
	call.Attempts[0].TokensPerSecondNano = &zeroNano
	encodedZero, err := json.Marshal(gatewayCallResponse(call))
	if err != nil {
		t.Fatal(err)
	}
	for _, zero := range []string{`"ttft_milliseconds":0`, `"duration_milliseconds":0`, `"tokens_per_second":"0.000000000"`} {
		if !strings.Contains(string(encodedZero), zero) {
			t.Fatalf("call DTO did not preserve legitimate zero metric %s: %s", zero, encodedZero)
		}
	}
}
