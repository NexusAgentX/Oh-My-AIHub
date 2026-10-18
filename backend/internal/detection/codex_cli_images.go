package detection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"slices"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

const codexProbeTextModel = "aihub-probe-text"

func (b *codexBridge) configureCLIImage(catalog nativeObject) error {
	if b.parent.Target.ModelType != "image" {
		return ErrInvalid
	}
	models := []string{}
	for _, raw := range list(catalog["models"]) {
		model := object(raw)
		id := str(model["slug"])
		if gatewaycontract.ValidModelID(id) && id != b.parent.Target.Model && (model["input_modalities"] == nil || slices.ContainsFunc(list(model["input_modalities"]), func(v any) bool { return str(v) == "text" })) {
			models = append(models, id)
		}
	}
	if len(models) == 0 {
		return ErrNotFound
	}
	slices.Sort(models)
	// A catalog candidate only supplies a companion request target. Its actual
	// inference must succeed; this never projects declared text capability or a
	// made-up supplier price onto the image model.
	quote := market.Quote{ID: "probe-text-zero-quote", GroupModelID: "probe-text-offering", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}
	book, err := quotepricing.Freeze(quotepricing.Book{GroupID: "probe-group", GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{codexProbeTextModel: {ModelID: codexProbeTextModel, UpstreamModel: models[0], Revision: 1, Quote: quote}}})
	if err != nil {
		return err
	}
	candidate := b.store.row.Candidate
	candidate.UpstreamModel, candidate.GroupModelID, candidate.Quotes = models[0], quote.GroupModelID, book
	b.store.extraRows = []gateway.CandidateRecord{{ModelID: codexProbeTextModel, Formats: []channel.Format{channel.FormatOpenAIResponses}, Candidate: candidate}}
	b.store.key.AllowedModels = []string{codexProbeModel, codexProbeTextModel}
	b.store.key.Aliases = map[string]string{"gpt-image-2": codexProbeModel}
	return nil
}

// Called under the private bridge mutex. Only pixel facts/hashes survive.
func (b *codexBridge) observeCLIImage(endpoint string, response Response, wire *codexWireFacts) {
	root, err := decodeNative(response.Body)
	rows := list(root["data"])
	if err != nil || root["error"] != nil || len(rows) != 1 {
		return
	}
	encoded := str(object(rows[0])["b64_json"])
	if _, valid := inspectImage(encoded); !valid {
		return
	}
	if len(b.imageOutputs) >= 8 {
		b.limited = true
		return
	}
	b.imageOutputs[sha256.Sum256([]byte("data:image/png;base64,"+encoded))] = true
	if endpoint == "codex.images" {
		wire.ImageGenerations++
	} else {
		wire.ImageEdits++
	}
	b.usage.Images++
	if usage := imageUsage(root); usage.Reliable {
		b.usage.InputTokens += usage.Input
		b.usage.OutputTokens += usage.Output
	}
}

func safeCLIImageOutput(root nativeObject) bool {
	if len(list(root["output"])) == 0 {
		return false
	}
	for _, raw := range list(root["output"]) {
		item := object(raw)
		switch str(item["type"]) {
		case "message", "reasoning":
		case "function_call":
			args, err := decodeNative([]byte(str(item["arguments"])))
			if str(item["namespace"]) != "image_gen" || str(item["name"]) != "imagegen" || err != nil || str(args["prompt"]) == "" || len(str(args["prompt"])) > 8192 {
				return false
			}
			for name, value := range args {
				switch name {
				case "prompt":
				case "num_last_images_to_include":
					if n, ok := integer(value); value != nil && (!ok || n != 1) {
						return false
					}
				case "referenced_image_paths":
					if paths, ok := value.([]any); value != nil && (!ok || len(paths) != 0) {
						return false
					}
				case "transparent_background":
					if _, ok := value.(bool); !ok {
						return false
					}
				default:
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func (b *codexBridge) verifyCLIImagePermission(ctx context.Context) bool {
	allowed := b.store.key.AllowedModels
	b.store.key.AllowedModels = []string{codexProbeTextModel}
	defer func() { b.store.key.AllowedModels = allowed }()
	before := b.requests
	r, _ := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1/backend-api/codex/images/generations", bytes.NewBufferString(`{"model":"gpt-image-2","prompt":"Synthetic independent-model ACL check."}`))
	r.Header.Set("Authorization", "Bearer "+b.token)
	r.Header.Set("Content-Type", "application/json")
	w := &codexProbeWriter{header: http.Header{}, limit: b.parent.limits.ResponseBytes}
	// Execution is already drained; run the production handler directly rather
	// than reopening the closed worker listener. This local denial has no cost.
	b.mux.ServeHTTP(w, r)
	return w.status == http.StatusForbidden && b.requests == before
}
