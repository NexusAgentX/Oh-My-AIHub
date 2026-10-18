package detection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

type catalogObservation struct {
	FirstModel string
	Last       nativeOutcome
	Valid      bool
	Attempts   []string
}

func catalogEndpoint(protocol string) (string, string, string) {
	switch protocol {
	case "native-messages":
		return "models.anthropic.list", "models.anthropic.get", "/v1/models"
	case "native-generate-content":
		return "models.gemini.list", "models.gemini.get", "/v1beta/models"
	default:
		return "models.openai.list", "models.openai.get", "/v1/models"
	}
}
func (n *nativeScene) readCatalog(ctx context.Context) catalogObservation {
	id, _, path := catalogEndpoint(n.protocol)
	o := catalogObservation{Valid: true, Attempts: []string{}}
	cursor := ""
	seenCursors := map[string]bool{}
	seenModels := map[string]bool{}
	for page := range 3 {
		query := url.Values{}
		if n.protocol == "native-messages" {
			query.Set("limit", "2")
			if cursor != "" {
				query.Set("after_id", cursor)
			}
		} else if n.protocol == "native-generate-content" {
			query.Set("pageSize", "2")
			if cursor != "" {
				query.Set("pageToken", cursor)
			}
		}
		out := n.call(ctx, Request{EndpointID: id, Path: path, Query: query, Category: "query"}, true)
		o.Last = out
		o.Attempts = append(o.Attempts, out.Response.AttemptID)
		if out.State != "pass" {
			o.Valid = false
			break
		}
		root := out.Reply.Object
		key := "data"
		if n.protocol == "native-generate-content" {
			key = "models"
		}
		models, present := root[key].([]any)
		if !present || len(models) > 10000 {
			o.Valid = false
			break
		}
		for _, v := range models {
			item := object(v)
			model := str(item["id"])
			if n.protocol == "native-generate-content" {
				model = strings.TrimPrefix(str(item["name"]), "models/")
			}
			if !gatewaycontract.ValidModelID(model) || seenModels[model] {
				o.Valid = false
				continue
			}
			seenModels[model] = true
			if o.FirstModel == "" {
				o.FirstModel = model
			}
		}
		next := ""
		if n.protocol == "native-messages" {
			if more, ok := root["has_more"].(bool); !ok {
				o.Valid = false
			} else if more {
				next = str(root["last_id"])
				if !gatewaycontract.ValidModelID(next) {
					o.Valid = false
					break
				}
			}
		} else if n.protocol == "native-generate-content" {
			next = str(root["nextPageToken"])
		}
		if next == "" {
			break
		}
		if len(next) > 4096 || seenCursors[next] || next == cursor {
			o.Valid = false
			break
		}
		seenCursors[next] = true
		cursor = next
		// Three pages establish the bounded pagination behavior. More models
		// outside this sampled catalog are not claimed to have been inspected.
		if page == 2 {
			break
		}
	}
	o.Last.Reply.Structure = o.Valid
	return o
}
func (n *nativeScene) catalog(ctx context.Context) {
	o := n.readCatalog(ctx)
	n.check("fixed_protocol_auth_and_version", o.Last, o.Valid)
	n.check("bounded_directory_pagination", o.Last, o.Valid)
	if o.FirstModel != "" && o.Valid {
		_, id, path := catalogEndpoint(n.protocol)
		out := n.call(ctx, Request{EndpointID: id, Path: path + "/" + url.PathEscape(o.FirstModel), Category: "query"}, true)
		model := str(out.Reply.Object["id"])
		if n.protocol == "native-generate-content" {
			model = strings.TrimPrefix(str(out.Reply.Object["name"]), "models/")
		}
		n.linkedCheck("metadata_not_capacity_proof", o.Last, out, model == o.FirstModel)
	} else {
		n.uncertain("metadata_not_capacity_proof", o.Last)
	}
}
func errorProof(protocol string, out nativeOutcome, statuses []int) (nativeOutcome, bool) {
	o, err := decodeNative(out.Response.Body)
	valid := err == nil && nativeError(protocol, o) && slices.Contains(statuses, out.Response.Status)
	proof := out
	if valid {
		proof.State = "pass"
		proof.Reason = "ok"
	} else if out.Response.Status != 0 && out.Response.Status != 429 && out.Response.Status < 500 {
		proof.State = "fail"
		proof.Reason = "assertion_failed"
	}
	return proof, valid
}
func (n *nativeScene) groupErrors(ctx context.Context) {
	catalog := n.readCatalog(ctx)
	model := catalog.FirstModel
	uuid, err := ids.NewUUID()
	if err != nil {
		return
	}
	unknown := "aihub-unknown-" + uuid
	if model == "" {
		model = unknown
	}
	id, path, query := nativeEndpoint(n.protocol, model, false)
	body := baseNative(n.protocol, model, nativeBasicPrompt)
	encoded, _ := json.Marshal(body)
	for _, auth := range []Authentication{AuthenticationInvalid, AuthenticationAbsent} {
		out := n.call(ctx, Request{EndpointID: id, Path: path, Query: query, Body: encoded, Headers: http.Header{"Content-Type": {"application/json"}}, Category: "generate", Authentication: auth}, false)
		proof, valid := errorProof(n.protocol, out, []int{401, 403})
		n.check("invalid_auth", proof, valid)
		n.check("native_error_structure", proof, valid)
	}
	unknownBody := baseNative(n.protocol, unknown, nativeBasicPrompt)
	encoded, _ = json.Marshal(unknownBody)
	id, path, query = nativeEndpoint(n.protocol, unknown, false)
	out := n.call(ctx, Request{EndpointID: id, Path: path, Query: query, Body: encoded, Headers: http.Header{"Content-Type": {"application/json"}}, Category: "generate"}, false)
	proof, valid := errorProof(n.protocol, out, []int{400, 404})
	root, _ := decodeNative(out.Response.Body)
	e := object(root["error"])
	message := strings.ToLower(str(e["message"]))
	valid = valid && (str(e["code"]) == "model_not_found" || strings.Contains(message, "model") && (strings.Contains(message, "not found") || strings.Contains(message, "does not exist") || strings.Contains(message, "not exist") || strings.Contains(message, "unknown") || strings.Contains(message, "not supported")))
	n.check("unknown_model", proof, valid)
	n.check("native_error_structure", proof, nativeError(n.protocol, root))
	id, path, query = nativeEndpoint(n.protocol, model, false)
	out = n.call(ctx, Request{EndpointID: id, Path: path, Query: query, Body: []byte(`{invalid-json`), Headers: http.Header{"Content-Type": {"application/json"}}, Category: "generate"}, false)
	proof, valid = errorProof(n.protocol, out, []int{400, 422})
	n.check("invalid_request", proof, valid)
	n.check("native_error_structure", proof, valid)
	// Natural quota/service errors remain observations; the detector never
	// induces overload, depletes credit or waits for a reset window.
	if catalog.Last.Response.Status == 429 || catalog.Last.Response.Status >= 500 {
		root, err := decodeNative(catalog.Last.Response.Body)
		proof := catalog.Last
		if err == nil && nativeError(n.protocol, root) {
			proof.State = "pass"
			proof.Reason = "ok"
		}
		n.check("natural_service_errors_observed_only", proof, err == nil && nativeError(n.protocol, root))
	} else if catalog.Last.Response.Status > 0 {
		n.checks["natural_service_errors_observed_only"] = Assertion{State: "unobserved", Reason: "not_observed", AttemptIDs: []string{catalog.Last.Response.AttemptID}}
	} else {
		n.unobserved("natural_service_errors_observed_only", catalog.Last)
	}
}
