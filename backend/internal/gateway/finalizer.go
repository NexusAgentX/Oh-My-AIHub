package gateway

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

func FinalizerHash(status CallStatus, reason, offerID string, httpStatus int, usage *ledger.UsageV1) [32]byte {
	payload := struct {
		Status     CallStatus      `json:"status"`
		Reason     string          `json:"reason"`
		OfferID    string          `json:"offer_id"`
		HTTPStatus int             `json:"http_status"`
		Usage      *ledger.UsageV1 `json:"usage"`
	}{status, reason, offerID, httpStatus, usage}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return sha256.Sum256(encoded)
}
