package detection

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

const codexProbeModel = "aihub-probe-model"
const codexProbeCredential = "isolated-upstream-placeholder"

// This fixture is private to one detector invocation. It is never published,
// billed to a platform account or included in market usage samples. Its zero
// quote merely exercises the real admission/pricing code; it is not a supplier
// price. The real test Key stays in the bound parent Session.
type codexBridge struct {
	mu                sync.Mutex
	parent            *Session
	store             *codexProbeStore
	engine            *gateway.Engine
	mux               *http.ServeMux
	token             string
	base              *url.URL
	entry             int
	witnesses         []string
	separated         bool
	requests          int
	closed            bool
	wg                sync.WaitGroup
	bootstrap         []string
	observed          map[string]int
	usage             Usage
	slots             chan struct{}
	handlers          chan struct{}
	limited           bool
	wire              codexWireFacts
	callIDs           map[string]bool
	failure           *codexUpstreamFailure
	contextOutputs    map[[32]byte]map[string]bool
	contextContinued  map[string]bool
	contextExecutions []codexContextExecution
	compactionWindows [][][32]byte
	reasoningCiphers  map[[32]byte]bool
	activity          codexProbeActivity
	profile           string
	imageOutputs      map[[32]byte]bool
	noteMarker        string
	hintMarker        string
	hintSeedStarted   bool
}

type codexContextExecution struct {
	Endpoint                          string
	Start, End                        int64
	HintShape, HintKnown, HintMatched bool
}

type codexUpstreamFailure struct {
	State, Reason string
}

// Only bounded semantic facts leave this private bridge. Call IDs and native
// request/response bodies remain transient memory and never enter reports.
type codexWireFacts struct {
	HintSeeded                                                         bool
	ImageGenerations, ImageEdits, ImageContinued                       int
	ImagePermission                                                    bool
	CompactionV1, CompactionV2, CompactionReturned, CompactionReplayed int
	ReasoningProduced, ReasoningReplayed                               int
	SearchStructured                                                   bool
	BackgroundOverlaps, CancelledCompactions                           int
	SingleSettlement                                                   bool
	RequestedTier, ActualTier                                          string
	LiteRequests, StandardRequests, ToolDeclarations                   int
	ToolCalls, ToolResults, SSE, ReliableUsage                         int
	ZstdRequests, PlainRequests                                        int
	TextDeltas, ToolDeltas                                             int
	AllTurns, ParallelDisabled, DeveloperInstructions                  int
	UniqueToolCalls, MaxCallsPerResponse                               int
}

func newCodexBridge(s *Session, upstreamModel string, bootstrap []string) (*codexBridge, error) {
	if s.exchange != nil || s.entry < 0 || s.entry >= len(s.resolved.Addresses) {
		return nil, ErrInvalid
	}
	seed := make([]byte, 96)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	defer clear(seed)
	token := "sk-aih-probe-" + hex.EncodeToString(seed[64:])
	namespace, err := gatewaycontract.NewContextNamespace(seed[:32])
	if err != nil {
		return nil, err
	}
	keyring, err := channel.ParseKeyring("probe="+base64.StdEncoding.EncodeToString(seed[32:64]), "probe")
	if err != nil {
		return nil, err
	}
	sealed, err := keyring.Encrypt("probe-channel", 1, codexProbeCredential)
	if err != nil {
		return nil, err
	}
	quote := market.Quote{ID: "probe-zero-quote", GroupModelID: "probe-offering", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}
	formats := []channel.Format{channel.FormatOpenAIResponses}
	if s.Target.ModelType == "image" {
		quote.BillingUnit = "per_successful_image"
		quote.UnitPrices = []market.UnitPrice{{Unit: "successful_image", Price: money.FromNano(0)}}
		formats = []channel.Format{channel.FormatOpenAIImages}
	}
	book, err := quotepricing.Freeze(quotepricing.Book{GroupID: "probe-group", GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{codexProbeModel: {ModelID: codexProbeModel, Revision: 1, Quote: quote}}})
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(s.resolved.Addresses[s.entry])
	if err != nil {
		return nil, err
	}
	proof := map[string]bool{}
	// Approved synthetic capability inputs avoid waiting for the report that
	// this very job is producing. They are not upstream assertions or reports.
	manifest, err := gatewaycontract.Load()
	if err != nil {
		return nil, err
	}
	for _, suite := range manifest.Suites {
		for _, req := range suite.Requirements {
			for _, label := range req.Assertions {
				proof["upstream_protocol/"+req.ID+"/"+label] = true
			}
		}
	}
	if upstreamModel == "" {
		upstreamModel = codexProbeModel // model-less auxiliary witness only
	}
	advanced := channel.Advanced{HeaderRules: channel.HeaderRules{Remove: slices.Clone(s.resolved.Headers.Remove)}}
	if s.resolved.UserAgent != "" {
		ua := s.resolved.UserAgent
		advanced.UserAgent = &ua
	}
	for _, set := range s.resolved.Headers.Set {
		advanced.HeaderRules.Set = append(advanced.HeaderRules.Set, channel.HeaderSet{Name: set.Name, Value: set.Value})
	}
	c := gateway.Candidate{GroupID: "probe-group", GroupModelID: "probe-offering", ChannelID: "probe-channel", CredentialScope: "probe-scope", CredentialRevision: 1, OwnerID: "probe-provider", BaseURL: base.String(), UpstreamModel: upstreamModel, Credential: sealed, Quotes: book, MultiplierNano: ledger.UnitRate, Evidence: proof, Advanced: advanced}
	store := &codexProbeStore{hash: apikey.Hash(token), key: gateway.KeyAuth{OwnerID: "probe-consumer", KeyID: s.jobID, Enabled: true, AccountActive: true, AllowedModels: []string{codexProbeModel}}, row: gateway.CandidateRecord{ModelID: codexProbeModel, Formats: formats, Candidate: c}, finished: map[string]bool{}}
	b := &codexBridge{parent: s, store: store, token: token, base: base, entry: s.entry, separated: true, bootstrap: slices.Clone(bootstrap), observed: map[string]int{}, callIDs: map[string]bool{}, contextOutputs: map[[32]byte]map[string]bool{}, contextContinued: map[string]bool{}, reasoningCiphers: map[[32]byte]bool{}, imageOutputs: map[[32]byte]bool{}, activity: codexProbeActivity{changed: make(chan struct{})}, slots: make(chan struct{}, 2), handlers: make(chan struct{}, 4)}
	b.engine = gateway.NewEngine(gateway.Dependencies{Store: store, Catalog: store, Settings: settings.NewService(codexProbeSettings{}), Routing: codexProbeRouting{}, Keyring: keyring, Outbound: codexProbeOutbound{Outbound: s.outbound, bridge: b}, ContextNamespace: namespace, Resources: codexProbeResources{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CallTimeLimit: s.limits.RequestTimeout})
	b.mux = http.NewServeMux()
	for _, endpoint := range manifest.Endpoints {
		if h := b.engine.CodexHandler(endpoint); h != nil {
			b.mux.HandleFunc(endpoint.Method+" "+endpoint.Path, h)
		} else if endpoint.ID == "responses.create" {
			b.mux.HandleFunc(endpoint.Method+" "+endpoint.Path, func(w http.ResponseWriter, r *http.Request) {
				b.engine.Serve(w, r, channel.FormatOpenAIResponses, "", false)
			})
		}
	}
	return b, nil
}

func (b *codexBridge) close() {
	b.mu.Lock()
	b.closed = true
	b.activity.notify()
	b.mu.Unlock()
	b.wg.Wait()
	clear(b.store.hash)
	b.token = ""
}

func (b *codexBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	select {
	case b.handlers <- struct{}{}:
		defer func() { <-b.handlers }()
	default:
		b.mu.Lock()
		b.limited = true
		b.mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	b.wg.Add(1)
	b.mu.Unlock()
	defer b.wg.Done()
	if r.URL.Host != "" && !net.ParseIP(r.URL.Hostname()).IsLoopback() {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if r.URL.Path == "/probe/async-state" {
		b.serveAsyncState(w, r)
		return
	}
	// The official-client harness can observe cancellation even though the
	// production engine deliberately drains disconnected calls for settlement.
	observation := make(chan struct{})
	stop := context.AfterFunc(r.Context(), func() { close(observation) })
	defer stop()
	r = r.WithContext(context.WithValue(r.Context(), codexClientCancellationKey{}, (<-chan struct{})(observation)))
	b.mux.ServeHTTP(w, r)
}

type codexProbeWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
	limit  int64
}

func (w *codexProbeWriter) Header() http.Header { return w.header }
func (w *codexProbeWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *codexProbeWriter) Write(body []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if int64(len(body)) > w.limit-int64(w.body.Len()) {
		return 0, ErrBusy
	}
	return w.body.Write(body)
}
func (w *codexProbeWriter) Flush() { w.WriteHeader(http.StatusOK) }

func (b *codexBridge) exchange(ctx context.Context, input Request) (Response, error) {
	ep, ok := b.parent.endpoints[input.EndpointID]
	if !ok || (ep.Feature != "F8" && ep.ID != "responses.create") || !matchPath(ep.Path, input.Path) || int64(len(input.Body)) > b.parent.limits.RequestBytes || input.Authentication != AuthenticationValid {
		return Response{}, ErrInvalid
	}
	u := &url.URL{Scheme: "http", Host: "127.0.0.1", Path: input.Path, RawQuery: input.Query.Encode()}
	r, err := http.NewRequestWithContext(ctx, ep.Method, u.String(), bytes.NewReader(input.Body))
	if err != nil {
		return Response{}, err
	}
	r.Header = input.Headers.Clone()
	if r.Header == nil {
		r.Header = http.Header{}
	}
	r.Header.Set("Authorization", "Bearer "+b.token)
	b.mu.Lock()
	before := len(b.witnesses)
	b.mu.Unlock()
	w := &codexProbeWriter{header: http.Header{}, limit: b.parent.limits.ResponseBytes}
	b.ServeHTTP(w, r)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	b.mu.Lock()
	refs := slices.Clone(b.witnesses[before:])
	b.mu.Unlock()
	if len(refs) == 0 && strings.HasPrefix(ep.ID, "identity.") {
		refs = slices.Clone(b.bootstrap)
	}
	if len(refs) == 0 {
		// A local fixture or detector failure cannot become supplier evidence.
		return Response{}, ErrInvalid
	}
	return Response{Status: w.status, Headers: w.header, Body: bytes.Clone(w.body.Bytes()), AttemptID: refs[len(refs)-1], PriorAttempts: refs[:len(refs)-1], Sent: true}, nil
}

type codexProbeOutbound struct {
	channel.Outbound
	bridge *codexBridge
}

func (p codexProbeOutbound) WithExtraBlockedHosts(hosts []string) (channel.Outbound, error) {
	if len(hosts) != 0 {
		return nil, ErrInvalid
	}
	return p, nil
}
func (p codexProbeOutbound) GatewayClient(context.Context, string) (*http.Client, error) {
	return &http.Client{Transport: p, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func (p codexProbeOutbound) RoundTrip(req *http.Request) (*http.Response, error) {
	b := p.bridge
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		b.mu.Lock()
		b.limited = true
		b.mu.Unlock()
		return nil, ErrBusy
	}
	b.mu.Lock()
	if b.closed || b.requests >= 256 {
		if b.requests >= 256 {
			b.limited = true
		}
		b.mu.Unlock()
		return nil, ErrBusy
	}
	b.requests++
	b.wg.Add(1)
	b.separated = b.separated && req.Header.Get("Authorization") == "Bearer "+codexProbeCredential && !strings.Contains(req.Header.Get("Authorization"), b.token)
	b.mu.Unlock()
	defer b.wg.Done()
	if req.URL.Scheme != b.base.Scheme || req.URL.Host != b.base.Host || req.URL.User != nil {
		return nil, ErrInvalid
	}
	var ep gatewaycontract.Endpoint
	for _, e := range b.parent.endpoints {
		if (e.Feature == "F8" || e.ID == "responses.create") && e.Method == req.Method && req.URL.EscapedPath() == strings.TrimSuffix(b.base.EscapedPath(), "/")+e.Path {
			ep = e
			break
		}
	}
	if ep.ID == "" {
		return nil, ErrInvalid
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, b.parent.limits.RequestBytes+1))
	if err != nil || int64(len(body)) > b.parent.limits.RequestBytes {
		return nil, ErrBusy
	}
	// Each concurrent CLI request gets private mutable execution state. Only
	// metadata counters and real witnesses are joined under the bridge lock.
	session := *b.parent
	session.exchange, session.entry = nil, b.entry
	session.usage = Usage{}
	session.allowed = map[string]bool{ep.ID: true}
	session.retained, session.retainedBytes = nil, 0
	defer func() { session.secret = nil; session.clear() }()
	seedWitnesses := []string{}
	if b.profile == "notes_lifecycle" && ep.ID == "codex.notes.thread_hint" {
		seedWitnesses = b.seedCLIHint(req.Context(), &session, body)
	}
	category := "query"
	if ep.Replay == "inference" {
		category = "generate"
	}
	compact := false
	cancelCase := false
	if ep.ID == "codex.responses" {
		plain, e := nativeencoding.Decode(req.Context(), req.Header.Get("Content-Encoding"), body)
		if e == nil {
			cancelCase = b.profile == "async_compact" && bytes.Contains(plain, []byte("AIHUB-ASYNC-CANCEL-"))
			root, _ := decodeNative(plain)
			for _, raw := range list(root["input"]) {
				compact = compact || str(object(raw)["type"]) == "compaction_trigger"
			}
		}
		b.startActivity(compact)
	}
	started := time.Now().UnixNano()
	response, err := session.Do(req.Context(), Request{EndpointID: ep.ID, Path: ep.Path, Query: req.URL.Query(), Headers: req.Header, Body: body, Category: category, prepared: true})
	response.PriorAttempts = append(seedWitnesses, response.PriorAttempts...)
	finished := time.Now().UnixNano()
	if ep.ID == "codex.responses" {
		cancelled := false
		if compact && cancelCase {
			// Gate only delivery of an already-observed genuine result. This
			// tests client shutdown and gateway draining without inducing an
			// upstream fault or asserting that the upstream itself cancelled.
			if client, ok := req.Context().Value(codexClientCancellationKey{}).(<-chan struct{}); ok {
				select {
				case <-client:
					cancelled = true
				case <-req.Context().Done():
				}
			}
		}
		b.finishActivity(compact, cancelled)
	}
	var failure *codexUpstreamFailure
	if err == nil && (response.Status < 200 || response.Status >= 300) {
		failure = &codexUpstreamFailure{State: "inconclusive", Reason: "external_error"}
		if response.Status == 401 || response.Status == 403 {
			failure.Reason = "key_unavailable"
		} else if response.Status != 429 && response.Status < 500 {
			if root, e := decodeNative(response.Body); e == nil && nativeError("openai-responses", root) {
				failure.State, failure.Reason = "fail", "assertion_failed"
				if unsupportedNative(root, response.Status) {
					failure.State, failure.Reason = "unsupported", "unsupported"
				}
			}
		}
	}
	var usage Usage
	var reply nativeReply
	validStream := false
	wire := codexWireFacts{}
	if ep.ID == "codex.responses" {
		if req.Header.Get("Content-Encoding") == "zstd" {
			wire.ZstdRequests++
		} else if req.Header.Get("Content-Encoding") == "" {
			wire.PlainRequests++
		}
		plain, decodeErr := nativeencoding.Decode(req.Context(), req.Header.Get("Content-Encoding"), body)
		if decodeErr == nil {
			if root, err := decodeNative(plain); err == nil {
				if b.profile == "notes_lifecycle" {
					b.mu.Lock()
					if b.noteMarker == "" {
						b.noteMarker = codexNoteMarker.FindString(string(plain))
					}
					b.mu.Unlock()
				}
				lite := false
				for _, raw := range list(root["input"]) {
					if str(object(raw)["type"]) == "compaction_trigger" {
						wire.CompactionV2++
					}
				}
				b.mu.Lock()
				for _, window := range b.compactionWindows {
					if codexCompactionReplayed(list(root["input"]), window) {
						wire.CompactionReplayed++
					}
				}
				for _, raw := range list(root["input"]) {
					item := object(raw)
					if str(item["type"]) == "reasoning" && b.reasoningCiphers[sha256.Sum256([]byte(str(item["encrypted_content"])))] {
						wire.ReasoningReplayed++
					}
				}
				b.mu.Unlock()
				if tier := str(root["service_tier"]); canonicalNativeTier("openai-responses", tier) != "" {
					wire.RequestedTier = tier
				}
				if str(object(root["reasoning"])["context"]) == "all_turns" {
					wire.AllTurns++
				}
				if root["parallel_tool_calls"] == false {
					wire.ParallelDisabled++
				}
				contextCalls := map[string]string{}
				for _, raw := range list(root["input"]) {
					item := object(raw)
					if str(item["role"]) == "developer" {
						text, _ := json.Marshal(item["content"])
						if bytes.Contains(text, []byte(codexprobe.Marker)) {
							wire.DeveloperInstructions++
						}
					}
					if str(item["type"]) == "function_call" {
						endpoint := "codex." + str(item["namespace"]) + "." + str(item["name"])
						if str(item["namespace"]) == "web" && str(item["name"]) == "run" {
							endpoint = "codex.search"
						}
						contextCalls[str(item["call_id"])] = endpoint
					}
				}
				for _, raw := range list(root["input"]) {
					item := object(raw)
					if str(item["type"]) == "additional_tools" {
						lite = true
						wire.ToolDeclarations += syntheticToolDeclarations(list(item["tools"]))
					}
					if str(item["type"]) == "function_call_output" {
						endpoint := contextCalls[str(item["call_id"])]
						hash := codexContextOutputHash(item["output"])
						b.mu.Lock()
						known := b.callIDs[str(item["call_id"])]
						if endpoint == "codex.image_gen.imagegen" {
							for _, raw := range list(item["output"]) {
								part := object(raw)
								if str(part["type"]) == "input_image" && b.imageOutputs[sha256.Sum256([]byte(str(part["image_url"])))] {
									wire.ImageContinued++
								}
							}
						}
						if b.contextOutputs[hash][endpoint] {
							b.contextContinued[endpoint] = true
						}
						b.mu.Unlock()
						output, _ := json.Marshal(item["output"])
						if known && bytes.Contains(output, []byte(codexprobe.Marker)) {
							wire.ToolResults++
						}
					}
				}
				if lite {
					wire.LiteRequests++
				} else {
					wire.StandardRequests++
					wire.ToolDeclarations += syntheticToolDeclarations(list(root["tools"]))
				}
			} else {
				b.mu.Lock()
				b.limited = true
				b.mu.Unlock()
			}
		} else {
			b.mu.Lock()
			b.limited = true
			b.mu.Unlock()
		}
	}
	if err == nil && response.Status >= 200 && response.Status < 300 && category == "generate" {
		if strings.Contains(response.Headers.Get("Content-Type"), "text/event-stream") {
			parsed := parseNativeStream("openai-responses", response.Body, b.parent.limits.EventBytes)
			reply = parsed.Reply
			validStream = parsed.Valid && parsed.Terminal
			if parsed.Valid && parsed.Terminal {
				wire.SSE++
				if parsed.TextLifecycle {
					wire.TextDeltas++
				}
				if parsed.ToolLifecycle {
					wire.ToolDeltas++
				}
			}
		} else if root, decodeErr := decodeNative(response.Body); decodeErr == nil {
			reply = parseNative("openai-responses", root)
		}
		if reply.Usage.Reliable && (responseTerminal(reply.Status) || str(reply.Object["object"]) == "response.compaction") {
			usage.InputTokens, usage.OutputTokens = reply.Usage.Input, reply.Usage.Output
			wire.ReliableUsage++
		}
		for _, candidate := range reply.Candidates {
			wire.MaxCallsPerResponse += len(candidate.Calls)
			for _, call := range candidate.Calls {
				if call.Name == "synthetic_echo" && call.ID != "" && matchesNativeSchema(call.Arguments) {
					wire.ToolCalls++
				}
			}
		}
	}
	unsafeImage := b.profile == "image_lifecycle" && ep.ID == "codex.responses" && err == nil && response.Status >= 200 && response.Status < 300 && (!validStream || !safeCLIImageOutput(reply.Object))
	if unsafeImage {
		failure = &codexUpstreamFailure{State: "fail", Reason: "assertion_failed"}
	}
	b.mu.Lock()
	b.parent.usage.Add(session.usage)
	b.usage.Add(usage)
	b.observed[ep.ID]++
	if ep.ID == "codex.notes.thread_hint" && err == nil && response.Status >= 200 && response.Status < 300 {
		root, e := decodeNative(response.Body)
		text, shape := root["text"].(string)
		if len(b.contextExecutions) >= 256 {
			b.limited = true
		} else {
			b.contextExecutions = append(b.contextExecutions, codexContextExecution{Endpoint: ep.ID, Start: started, End: finished, HintShape: e == nil && shape, HintKnown: b.hintMarker != "" || b.noteMarker != "", HintMatched: b.noteMarker != "" && strings.Contains(text, b.noteMarker) || b.hintMarker != "" && strings.Contains(text, b.hintMarker)})
		}
	}
	if err == nil && response.Status >= 200 && response.Status < 300 && (ep.ID == "codex.images" || ep.ID == "codex.images.edit") {
		b.observeCLIImage(ep.ID, response, &wire)
	}
	for _, raw := range list(reply.Object["output"]) {
		item := object(raw)
		if cipher := str(item["encrypted_content"]); str(item["type"]) == "reasoning" && cipher != "" {
			if len(b.reasoningCiphers) >= 8 {
				b.limited = true
			} else {
				b.reasoningCiphers[sha256.Sum256([]byte(cipher))] = true
				wire.ReasoningProduced++
			}
		}
	}
	if window, valid := codexCompactionWindow(list(reply.Object["output"])); valid && err == nil && response.Status >= 200 && response.Status < 300 {
		wire.CompactionReturned++
		if ep.ID == "codex.compact" {
			wire.CompactionV1++
		}
		if len(b.compactionWindows) < 8 {
			b.compactionWindows = append(b.compactionWindows, window)
		} else {
			b.limited = true
		}
	}
	if err == nil && response.Status >= 200 && response.Status < 300 && (strings.HasPrefix(ep.ID, "codex.notes.") && ep.ID != "codex.notes.thread_hint" || strings.HasPrefix(ep.ID, "codex.history.") || ep.ID == "codex.search") {
		if value, e := decodeNativeValue(response.Body); e == nil {
			if ep.ID == "codex.search" {
				root := object(value)
				wire.SearchStructured = codexSearchStructure(root)
			}
			output, valid := codexContextOutput(value)
			if ep.ID == "codex.search" {
				// The pinned CLI uses SearchResponse.output as an input_text
				// content array, even when encrypted_output is also supplied.
				text, present := object(value)["output"].(string)
				output, valid = []any{nativeObject{"type": "input_text", "text": text}}, present
			}
			if valid {
				hash := codexContextOutputHash(output)
				if b.contextOutputs[hash] == nil && len(b.contextOutputs) >= 128 {
					b.limited = true
				} else {
					if b.contextOutputs[hash] == nil {
						b.contextOutputs[hash] = map[string]bool{}
					}
					b.contextOutputs[hash][ep.ID] = true
				}
				if len(b.contextExecutions) < 256 {
					b.contextExecutions = append(b.contextExecutions, codexContextExecution{Endpoint: ep.ID, Start: started, End: finished})
				} else {
					b.limited = true
				}
			}
		}
	}
	if failure != nil {
		b.failure = failure
	}
	for _, candidate := range reply.Candidates {
		for _, call := range candidate.Calls {
			if call.ID != "" && call.Name == "synthetic_echo" && matchesNativeSchema(call.Arguments) {
				if b.callIDs[call.ID] || len(b.callIDs) < 64 {
					b.callIDs[call.ID] = true
				} else {
					b.limited = true
				}
			}
		}
	}
	b.wire.LiteRequests += wire.LiteRequests
	b.wire.StandardRequests += wire.StandardRequests
	b.wire.ToolDeclarations += wire.ToolDeclarations
	b.wire.ToolCalls += wire.ToolCalls
	b.wire.ToolResults += wire.ToolResults
	b.wire.SSE += wire.SSE
	b.wire.ReliableUsage += wire.ReliableUsage
	b.wire.ZstdRequests += wire.ZstdRequests
	b.wire.PlainRequests += wire.PlainRequests
	b.wire.TextDeltas += wire.TextDeltas
	b.wire.ToolDeltas += wire.ToolDeltas
	b.wire.AllTurns += wire.AllTurns
	b.wire.ParallelDisabled += wire.ParallelDisabled
	b.wire.DeveloperInstructions += wire.DeveloperInstructions
	b.wire.UniqueToolCalls = len(b.callIDs)
	b.wire.MaxCallsPerResponse = max(b.wire.MaxCallsPerResponse, wire.MaxCallsPerResponse)
	b.wire.CompactionV1 += wire.CompactionV1
	b.wire.CompactionV2 += wire.CompactionV2
	b.wire.CompactionReturned += wire.CompactionReturned
	b.wire.CompactionReplayed += wire.CompactionReplayed
	b.wire.ReasoningProduced += wire.ReasoningProduced
	b.wire.ReasoningReplayed += wire.ReasoningReplayed
	b.wire.SearchStructured = b.wire.SearchStructured || wire.SearchStructured
	b.wire.ImageGenerations += wire.ImageGenerations
	b.wire.ImageEdits += wire.ImageEdits
	b.wire.ImageContinued += wire.ImageContinued
	if wire.RequestedTier != "" {
		b.wire.RequestedTier = wire.RequestedTier
	}
	if actual := canonicalNativeTier("openai-responses", reply.Usage.Tier); actual != "" {
		b.wire.ActualTier = actual
	}
	for _, id := range append(response.PriorAttempts, response.AttemptID) {
		if id != "" && !slices.Contains(b.witnesses, id) {
			b.witnesses = append(b.witnesses, id)
		}
	}
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if unsafeImage {
		return nil, ErrInvalid
	}
	return &http.Response{StatusCode: response.Status, Header: response.Headers, Body: io.NopCloser(bytes.NewReader(response.Body)), ContentLength: int64(len(response.Body)), Request: req}, nil
}

func codexContextOutputHash(value any) [32]byte {
	// JSON text tool results are semantically compared, since Rust and Go may
	// serialize object member order differently. Ciphertext/content stays exact.
	if text, ok := value.(string); ok {
		if decoded, err := decodeNativeValue([]byte(text)); err == nil {
			value = decoded
		}
	}
	return sha256.Sum256(mustNativeJSON(value))
}

func syntheticToolDeclarations(tools []any) int {
	count := 0
	for _, raw := range tools {
		tool := object(raw)
		if str(tool["type"]) == "namespace" {
			count += syntheticToolDeclarations(list(tool["tools"]))
		} else if str(tool["type"]) == "function" && str(tool["name"]) == "synthetic_echo" {
			count++
		}
	}
	return count
}

type codexProbeStore struct {
	mu              sync.Mutex
	hash            []byte
	key             gateway.KeyAuth
	row             gateway.CandidateRecord
	extraRows       []gateway.CandidateRecord
	count           int
	finished        map[string]bool
	duplicateFinish bool
}

func (s *codexProbeStore) LookupKey(_ context.Context, hash []byte) (gateway.KeyAuth, error) {
	if subtle.ConstantTimeCompare(hash, s.hash) != 1 {
		return gateway.KeyAuth{}, gateway.ErrKeyNotFound
	}
	return s.key, nil
}
func (*codexProbeStore) TouchKey(context.Context, string, time.Time) error { return nil }
func (*codexProbeStore) Points(context.Context, string) (ledger.Points, error) {
	return ledger.Points{Balance: money.FromNano(1_000_000_000)}, nil
}
func (*codexProbeStore) KeySpend(context.Context, string, time.Time) (gateway.Spend, error) {
	return gateway.Spend{}, nil
}
func (s *codexProbeStore) Candidates(_ context.Context, model string, format channel.Format) ([]gateway.Candidate, error) {
	for _, row := range append([]gateway.CandidateRecord{s.row}, s.extraRows...) {
		if model == row.ModelID && slices.Contains(row.Formats, format) {
			return []gateway.Candidate{row.Candidate}, nil
		}
	}
	return nil, nil
}
func (s *codexProbeStore) AuxiliaryCandidates(context.Context, channel.Format) ([]gateway.CandidateRecord, error) {
	return append([]gateway.CandidateRecord{s.row}, s.extraRows...), nil
}
func (*codexProbeStore) AttemptHistory(context.Context, time.Time) ([]gateway.AttemptHistoryRow, error) {
	return nil, nil
}
func (*codexProbeStore) ResponseChannel(context.Context, string, string) (string, error) {
	return "", gateway.ErrKeyNotFound
}
func (s *codexProbeStore) InsertCall(context.Context, gateway.CallInsert) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count >= 128 {
		return "", ErrBusy
	}
	s.count++
	return ids.NewUUID()
}
func (s *codexProbeStore) FinishCall(_ context.Context, finish gateway.CallFinish) (gateway.FinishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished[finish.ID] {
		s.duplicateFinish = true
		return gateway.FinishResult{}, nil
	}
	s.finished[finish.ID] = true
	return gateway.FinishResult{Finished: true}, nil
}
func (*codexProbeStore) SweepStaleCalls(context.Context, time.Time) (int64, error)        { return 0, nil }
func (*codexProbeStore) RecordChannelEvent(context.Context, string, string, string) error { return nil }
func (s *codexProbeStore) OnlineModels(context.Context) ([]gateway.OnlineModel, error) {
	rows := []gateway.OnlineModel{{ID: s.row.ModelID, Formats: s.row.Formats}}
	for _, row := range s.extraRows {
		rows = append(rows, gateway.OnlineModel{ID: row.ModelID, Formats: row.Formats})
	}
	return rows, nil
}
func (*codexProbeStore) CustomModelOnline(context.Context, string) (bool, error) { return false, nil }
func (s *codexProbeStore) Get(_ context.Context, model string) (catalog.Model, error) {
	for _, row := range append([]gateway.CandidateRecord{s.row}, s.extraRows...) {
		if model == row.ModelID {
			return catalog.Model{ID: model, Enabled: true}, nil
		}
	}
	return catalog.Model{}, catalog.ErrNotFound
}
func (s *codexProbeStore) List(ctx context.Context, _ bool) ([]catalog.Model, error) {
	models := []catalog.Model{{ID: s.row.ModelID, Enabled: true}}
	for _, row := range s.extraRows {
		models = append(models, catalog.Model{ID: row.ModelID, Enabled: true})
	}
	return models, nil
}

type codexProbeSettings struct{}

func (codexProbeSettings) GetSettings(context.Context) (settings.Settings, error) {
	return settings.Settings{DefaultMaxAttempts: 1, DefaultTTFTTimeoutMS: 30000, DefaultRequestTimeoutMS: 180000, DefaultIdleTimeoutMS: 30000, DefaultCooldownFailures: 3, DefaultCooldownSeconds: 300, USDCNYRateNano: ledger.UnitRate, AccountMaxInflightCalls: 2}, nil
}
func (codexProbeSettings) UpdateSettings(context.Context, string, settings.Settings) (settings.Settings, error) {
	return settings.Settings{}, ErrInvalid
}

type codexProbeRouting struct{}

func (codexProbeRouting) Resolve(_ context.Context, _, _, model string) (routing.Pref, error) {
	return routing.Default(model), nil
}
func (codexProbeRouting) Get(context.Context, string, string, string) (routing.Pref, error) {
	return routing.Pref{}, routing.ErrNotFound
}
func (codexProbeRouting) Set(context.Context, string, string, routing.Pref) (routing.Pref, error) {
	return routing.Pref{}, ErrInvalid
}
func (codexProbeRouting) Delete(context.Context, string, string, string) error { return ErrInvalid }
func (codexProbeRouting) ListForKey(context.Context, string, string) ([]routing.Pref, error) {
	return nil, nil
}

type codexProbeResources struct{}

func (codexProbeResources) Lookup(context.Context, gatewaycontract.Reference) ([]resourcestate.Record, error) {
	return nil, nil // no unrelated upstream resource is owned by this fixture
}
func (codexProbeResources) Observe(_ context.Context, rows []resourcestate.Record) error {
	for _, row := range rows {
		if !resourcestate.ValidRecord(row) {
			return ErrInvalid
		}
	}
	return nil
}
func (codexProbeResources) Delete(context.Context, gatewaycontract.Principal, gatewaycontract.Reference, gatewaycontract.Binding) error {
	return errors.New("probe does not own stored resources")
}
