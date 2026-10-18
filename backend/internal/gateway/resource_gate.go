package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
)

func nativeProtocol(f channel.Format) string {
	switch f {
	case channel.FormatOpenAIChat:
		return "openai-chat-completions"
	case channel.FormatOpenAIResponses:
		return "openai-responses"
	case channel.FormatAnthropic:
		return "native-messages"
	case channel.FormatGemini:
		return "native-generate-content"
	case channel.FormatOpenAIImages:
		return "openai-images"
	}
	return ""
}
func principal(k KeyAuth) gatewaycontract.Principal {
	return gatewaycontract.Principal{AccountID: k.OwnerID, KeyID: k.KeyID, Active: k.AccountActive}
}
func candidateBinding(c Candidate) gatewaycontract.Binding {
	return gatewaycontract.Binding{GroupID: c.GroupID, ChannelID: c.ChannelID, CredentialVersion: strconv.FormatInt(c.CredentialRevision, 10)}
}
func resourceMatch(c Candidate, r *resourcestate.Record) bool {
	return r == nil || candidateBinding(c) == r.Binding && c.CredentialScope == r.CredentialScope
}
func supportsAll(c Candidate, required []CapabilityRequirement) bool {
	for _, r := range required {
		if !c.Evidence[r.Layer+"/"+r.ID] {
			return false
		}
	}
	return true
}
func (e *Engine) prepareResources(ctx context.Context, call *callState, body []byte, path string) error {
	if e.Resources == nil {
		return nil
	}
	if call.format == channel.FormatOpenAIImages {
		endpoint := "images.generations"
		if strings.HasSuffix(path, "/edits") {
			endpoint = "images.edits"
		}
		if strings.HasSuffix(path, "/variations") {
			endpoint = "images.variations"
		}
		call.resourceRequired = []CapabilityRequirement{{Layer: "upstream_protocol", ID: endpoint + "/native_json_or_multipart"}}
	}
	if call.imageMultipart != nil {
		return nil
	}
	refs, err := nativeRequestReferences(nativeProtocol(call.format), body)
	if err != nil {
		return err
	}
	call.resourceReferences = refs
	call.resourceBinding, err = resourcestate.Resolve(ctx, e.Resources, principal(call.key), refs)
	if err != nil {
		return err
	}
	var scratch Observer
	object := scratch.objectOf(body)
	store := call.format == channel.FormatOpenAIResponses && string(object.get("store")) != "false" || call.format == channel.FormatOpenAIChat && string(object.get("store")) == "true"
	if strings.HasSuffix(path, "/compact") {
		store = false
		call.resourceRequired = endpointRequirement("responses.compact")
	}
	addCap := func(id string) {
		call.resourceRequired = append(call.resourceRequired, CapabilityRequirement{Layer: "upstream_protocol", ID: id})
	}
	if string(object.get("store")) == "true" {
		if call.format == channel.FormatOpenAIChat {
			addCap("openai-chat-completions.stored/create_store_true")
		}
		if call.format == channel.FormatOpenAIResponses {
			addCap("openai-responses.state/owned_response_retrieve_input_items_delete")
		}
	}
	for _, ref := range refs {
		switch ref.Kind {
		case "response":
			addCap("openai-responses.state/previous_response_id_three_turns")
		case "conversation":
			addCap("openai-responses.state/conversation_flow")
		case "item":
			addCap("openai-responses.state/item_reference")
		case "cache":
			addCap("native-generate-content.cache_tier/cached_content_generate_and_stream_usage")
		}
	}
	if call.hints.Background {
		addCap("openai-responses.background/background_create")
	}
	track := store || len(object.get("modalities")) > 0 || len(object.get("audio")) > 0
	eachElement(object.get("tools"), func(raw []byte) {
		var part Observer
		tool := part.objectOf(raw)
		kind := textValue(tool.get("type"))
		if kind == "code_interpreter" || strings.HasPrefix(kind, "code_execution_") {
			track = true
		}
	})
	if track {
		call.resourceObserver = &resourceObserver{format: call.format, store: store}
	}
	return nil
}
func (e *Engine) registerResources(call *callState, c Candidate, raw []byte) error {
	if call.resourceObserver == nil || e.Resources == nil {
		return nil
	}
	found, err := call.resourceObserver.observe(raw)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return nil
	}
	if c.GroupID == "" || c.CredentialScope == "" || c.CredentialRevision < 1 {
		return gatewaycontract.ErrConflict
	}
	rows := make([]resourcestate.Record, 0, len(found))
	for _, v := range found {
		alreadyReferenced := false
		if v.Kind == "container" || v.Kind == "audio" {
			for _, ref := range call.resourceReferences {
				if ref.Kind == v.Kind && ref.ID == v.ID {
					alreadyReferenced = true
					break
				}
			}
		}
		if alreadyReferenced {
			continue
		}
		rows = append(rows, resourcestate.Record{Resource: gatewaycontract.Resource{Kind: v.Kind, ID: v.ID, ParentID: v.ParentID, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, Binding: candidateBinding(c), CredentialScope: c.CredentialScope, CreationID: call.id}, ModelID: call.modelID})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return e.Resources.Observe(ctx, rows)
}

// resourceEventGate holds complete SSE events until their producer IDs have
// been durably registered. The original bytes (including CR/LF and comments)
// are emitted unchanged; no partial identifier can escape before ownership.
// This runs only on requests capable of producing persistent resources.
type resourceEventGate struct {
	event      []byte
	lineStart  int
	pendingCR  bool
	closedLine bool
	failed     error
	observe    func([]byte) error
	send       func([]byte)
}

func (g *resourceEventGate) flush() error {
	var data []byte
	lines := bytes.FieldsFunc(g.event, func(r rune) bool { return r == '\r' || r == '\n' })
	for _, l := range lines {
		if v, ok := bytes.CutPrefix(l, []byte("data:")); ok {
			v = bytes.TrimPrefix(v, []byte(" "))
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, v...)
		}
	}
	if len(data) > 0 && data[0] == '{' {
		if err := g.observe(data); err != nil {
			g.failed = err
			return err
		}
	}
	g.send(g.event)
	g.event = g.event[:0]
	g.lineStart = 0
	g.closedLine = false
	return nil
}
func (g *resourceEventGate) write(chunk []byte) error {
	if g.failed != nil {
		return g.failed
	}
	for _, b := range chunk {
		// CRLF is one separator. A CR-only blank line is committed before the
		// following byte; a trailing CR is finished at EOF.
		if g.pendingCR {
			g.pendingCR = false
			if b == '\n' {
				g.event = append(g.event, b)
				g.lineStart = len(g.event)
				if g.closedLine {
					if err := g.flush(); err != nil {
						return err
					}
				}
				continue
			}
			if g.closedLine {
				if err := g.flush(); err != nil {
					return err
				}
			}
		}
		if len(g.event) >= maxLineBuffer {
			return errors.New("resource SSE event exceeds limit")
		}
		g.event = append(g.event, b)
		if b == '\r' || b == '\n' {
			blank := len(g.event)-1 == g.lineStart
			g.lineStart = len(g.event)
			g.closedLine = blank
			if b == '\r' {
				g.pendingCR = true
			} else if blank {
				if err := g.flush(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (g *resourceEventGate) finish() error {
	if g.failed != nil {
		return g.failed
	}
	if len(g.event) > 0 {
		return g.flush()
	}
	return nil
}
func isResourceSSE(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}

func stageResourceJSON(result *attemptResult) ([]byte, error) {
	payload := append([]byte(nil), result.first...)
	if len(payload) > maxRequestBytes {
		return nil, errors.New("resource response exceeds limit")
	}
	if result.firstErr != nil && !errors.Is(result.firstErr, io.EOF) {
		return nil, result.firstErr
	}
	if result.firstErr == nil {
		rest, err := io.ReadAll(io.LimitReader(result.response.Body, int64(maxRequestBytes-len(payload)+1)))
		if err != nil {
			return nil, err
		}
		payload = append(payload, rest...)
	}
	if len(payload) > maxRequestBytes {
		return nil, errors.New("resource response exceeds limit")
	}
	result.first = payload
	result.firstErr = io.EOF
	return payload, nil
}

func referenceRequirements(refs []gatewaycontract.Reference) []CapabilityRequirement {
	out := []CapabilityRequirement{}
	for _, ref := range refs {
		id := ""
		switch ref.Kind {
		case "response":
			id = "openai-responses.state/previous_response_id_three_turns"
		case "conversation":
			id = "openai-responses.state/conversation_flow"
		case "item":
			id = "openai-responses.state/item_reference"
		case "cache":
			id = "native-generate-content.cache_tier/cached_content_generate_and_stream_usage"
		}
		if id != "" {
			out = append(out, CapabilityRequirement{Layer: "upstream_protocol", ID: id})
		}
	}
	return out
}
