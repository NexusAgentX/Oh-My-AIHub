package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

const maxBackgroundJobs = 256

var ErrBackgroundNotFound = errors.New("background response not found")
var responseIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// BackgroundJob contains only routing, authentication ciphertext and billing
// metadata. Neither request/response content nor plaintext credentials is stored.
type BackgroundJob struct {
	CallID     string
	AccountID  string
	KeyID      string
	ChannelID  string
	ResponseID string
	Metadata   []byte
	Deadline   time.Time
	Settled    bool
}

type BackgroundStore interface {
	SaveBackground(context.Context, BackgroundJob) error
	LookupBackground(context.Context, string, string, string) (BackgroundJob, error)
	PendingBackground(context.Context) ([]BackgroundJob, error)
	ClaimBackground(context.Context, time.Time) ([]BackgroundJob, error)
	RetryBackground(context.Context, string, time.Time) error
}

type backgroundMetadata struct {
	RequestedServiceTier string
	Mode                 routing.Mode
	Source               routing.Source
	Started              time.Time
	Key                  KeyAuth
	Candidate            Candidate
	Model                catalog.Model
	Settings             settings.Settings
	Requested            string
	ModelID              string
	Stream               bool
	Attempts             []Attempt
	Diagnostics          Diagnostics
	Hints                billingHints
	Location             authLocation
	Headers              http.Header
}

type backgroundLease struct{ account, channel func() }

func (e *Engine) reserveBackground(id string) bool {
	e.jobsMu.Lock()
	defer e.jobsMu.Unlock()
	if len(e.jobs) >= maxBackgroundJobs {
		return false
	}
	e.jobs[id] = backgroundLease{}
	return true
}
func (e *Engine) releaseBackground(id string) {
	e.jobsMu.Lock()
	lease, ok := e.jobs[id]
	delete(e.jobs, id)
	e.jobsMu.Unlock()
	if ok {
		if lease.account != nil {
			lease.account()
		}
		if lease.channel != nil {
			lease.channel()
		}
	}
}

// RestoreBackground restores pending account/channel reservations before serving.
// The store separately verifies every retained credential at startup.
func (e *Engine) RestoreBackground(ctx context.Context) error {
	if e.Jobs == nil {
		return nil
	}
	jobs, err := e.Jobs.PendingBackground(ctx)
	if err != nil {
		return err
	}
	if len(jobs) > maxBackgroundJobs {
		return errors.New("background task capacity exceeded")
	}
	for _, job := range jobs {
		var metadata backgroundMetadata
		if err := json.Unmarshal(job.Metadata, &metadata); err != nil {
			return err
		}
		if _, err := e.Keyring.Decrypt(metadata.Candidate.ChannelID, metadata.Candidate.Credential); err != nil {
			return fmt.Errorf("background credential unavailable for %s: %w", job.CallID, err)
		}
		release, _ := e.inflight.Acquire(job.AccountID, 0)
		e.jobs[job.CallID] = backgroundLease{account: release, channel: e.Runtime.RestoreInflight(job.ChannelID)}
	}
	return nil
}

func terminalResponse(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "incomplete":
		return true
	}
	return false
}

func (e *Engine) keepBackground(call *callState, model catalog.Model, candidate Candidate, defaults settings.Settings, id string) (saveErr error) {
	if call.pending || call.polling {
		return nil
	}
	if e.Jobs == nil || !responseIDPattern.MatchString(id) {
		return errors.New("invalid background response metadata")
	}
	metadata := backgroundMetadata{RequestedServiceTier: call.requestedServiceTier, Mode: call.mode, Source: call.source, Started: call.started, Key: call.key, Candidate: candidate, Model: model, Settings: defaults, Requested: call.requested, ModelID: call.modelID, Stream: call.stream, Attempts: call.attempts, Diagnostics: call.diagnostics, Hints: call.hints, Location: call.authLocation, Headers: call.upstreamHeaders}
	defer func() {
		if saveErr == nil {
			return
		}
		// A failed durable registration must not silently leave provider work running.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		response, _, err := e.backgroundExchange(ctx, metadata, id, http.MethodPost, "cancel", "", nil)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxFailureBody))
			response.Body.Close()
		}
	}()

	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("background metadata limit exceeded")
	}
	e.jobsMu.Lock()
	e.jobs[call.id] = backgroundLease{account: call.releaseAccount, channel: call.releaseChannel}
	e.jobsMu.Unlock()
	call.pending = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = e.Jobs.SaveBackground(ctx, BackgroundJob{CallID: call.id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, ChannelID: candidate.ChannelID, ResponseID: id, Metadata: raw, Deadline: call.started.Add(e.CallTimeLimit)}); err != nil {
		call.pending = false
		// The request's defers release its reservations on this failure.
		e.jobsMu.Lock()
		delete(e.jobs, call.id)
		e.jobsMu.Unlock()
	}
	return err
}

// RunBackground uses four workers, at most sixteen polls per five-second
// round, ten seconds per exchange and a thirty-second database lease. Pending
// jobs keep their account/channel slots, including across process restarts.
func (e *Engine) RunBackground(ctx context.Context) {
	if e.Jobs == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		e.pollBackground(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (e *Engine) pollBackground(ctx context.Context) {
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	jobs, err := e.Jobs.ClaimBackground(claimCtx, e.Now())
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			e.Logger.Error("gateway: claiming background tasks failed", "error", err)
		}
		return
	}
	var wg sync.WaitGroup
	queue := make(chan BackgroundJob, len(jobs))
	for _, job := range jobs {
		queue <- job
	}
	close(queue)
	for range 4 {
		wg.Go(func() {
			for job := range queue {
				if ctx.Err() != nil {
					return
				}
				e.pollBackgroundJob(ctx, job)
			}
		})
	}
	wg.Wait()
}
func (e *Engine) pollBackgroundJob(ctx context.Context, job BackgroundJob) {
	var metadata backgroundMetadata
	if json.Unmarshal(job.Metadata, &metadata) != nil {
		e.Logger.Error("gateway: invalid task metadata", "request_id", job.CallID)
		return
	}
	call := &callState{requestedServiceTier: metadata.RequestedServiceTier, mode: metadata.Mode, source: metadata.Source, id: job.CallID, started: metadata.Started, key: metadata.Key, format: channel.FormatOpenAIResponses, stream: metadata.Stream, requested: metadata.Requested, modelID: metadata.ModelID, attempts: metadata.Attempts, diagnostics: metadata.Diagnostics, hints: metadata.Hints, polling: true, pending: true}
	if e.Resources != nil && metadata.Candidate.GroupID != "" {
		// The durable background registration is a trusted producer chain and
		// can finish a failed initial ownership write without replaying creation.
		row := resourcestate.Record{Resource: gatewaycontract.Resource{Kind: "response", ID: job.ResponseID, AccountID: metadata.Key.OwnerID, KeyID: metadata.Key.KeyID, Binding: candidateBinding(metadata.Candidate), CredentialScope: metadata.Candidate.CredentialScope, CreationID: job.CallID}, ModelID: metadata.ModelID}
		if err := e.Resources.Observe(ctx, []resourcestate.Record{row}); err != nil {
			if errors.Is(err, resourcestate.ErrQuarantined) {
				finish := CallFinish{ID: call.id, AccountID: call.key.OwnerID, Outcome: OutcomeUpstreamFailed, ModelID: call.modelID, ChannelID: job.ChannelID, Attempts: call.attempts, DurationMS: int(e.Now().Sub(call.started).Milliseconds()), Diagnostics: &call.diagnostics}
				e.settle(call, metadata.Candidate, finish, http.StatusBadGateway, finish.Usage)
			}
			return
		}
		call.resourceObserver = &resourceObserver{format: channel.FormatOpenAIResponses, store: true, responseID: job.ResponseID, expectedResponseID: job.ResponseID}
	}
	if !e.Now().Before(job.Deadline) {
		// Cancellation may return final usage. Feed it through normal settlement
		// before falling back to an interrupted call without reliable usage.
		cancelCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		if response, secret, err := e.backgroundExchange(cancelCtx, metadata, job.ResponseID, http.MethodPost, "cancel", "", nil); err == nil {
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				watchCtx, watchCancel := context.WithCancel(context.Background())
				result := attemptResult{response: response, started: metadata.Started, upstream: cancelCtx, cancel: func(error) {}, watch: watchClient(watchCtx, func(error) {}, e.DrainWindow), secret: secret}
				e.streamBack(discardResponseWriter{header: http.Header{}}, call, metadata.Model, metadata.Candidate, result, metadata.Settings)
				watchCancel()
				if call.completed {
					return
				}
			} else {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxFailureBody))
				response.Body.Close()
			}
		}
		finish := CallFinish{ID: call.id, AccountID: call.key.OwnerID, Outcome: OutcomeInterrupted, ModelID: call.modelID, ChannelID: job.ChannelID, Attempts: call.attempts, DurationMS: int(e.Now().Sub(call.started).Milliseconds()), Diagnostics: &call.diagnostics}
		e.settle(call, metadata.Candidate, finish, http.StatusGatewayTimeout, finish.Usage)
		return
	}
	pollCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	response, secret, err := e.backgroundExchange(pollCtx, metadata, job.ResponseID, http.MethodGet, "", "", nil)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(response.Body, maxFailureBody))
		return
	}
	// streamBack feeds the same usage observer and atomic settlement as a live
	// request. The discard writer deliberately retains no generated content.
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	result := attemptResult{response: response, started: metadata.Started, upstream: pollCtx, cancel: func(error) {}, watch: watchClient(watchCtx, func(error) {}, e.DrainWindow), secret: secret}
	e.streamBack(discardResponseWriter{header: http.Header{}}, call, metadata.Model, metadata.Candidate, result, metadata.Settings)
	if !call.completed {
		retryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = e.Jobs.RetryBackground(retryCtx, call.id, e.Now().Add(5*time.Second))
	}
}

type discardResponseWriter struct{ header http.Header }

func (d discardResponseWriter) Header() http.Header       { return d.header }
func (discardResponseWriter) WriteHeader(int)             {}
func (discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }

func (e *Engine) backgroundExchange(ctx context.Context, m backgroundMetadata, id, method, operation, query string, body io.Reader) (*http.Response, string, error) {
	if !responseIDPattern.MatchString(id) {
		return nil, "", ErrBackgroundNotFound
	}
	secret, err := e.Keyring.OpenCredential(m.Candidate.ChannelID, m.Candidate.Credential)
	if err != nil {
		return nil, "", err
	}
	_, policy, err := e.Settings(ctx)
	if err != nil {
		return nil, "", err
	}
	// A saved resource stays on its original channel, credential and immutable
	// entrance list. Selected addresses are alternate entrances to that engine.
	entries := m.Candidate.Entrances
	if len(entries) == 0 {
		entries = []Entrance{{BaseURL: m.Candidate.BaseURL}}
	}
	maxAttempts := int(m.Settings.DefaultMaxAttempts)
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if len(entries) > maxAttempts {
		entries = entries[:maxAttempts]
	}
	path := "/v1/responses/" + id
	if operation != "" {
		path += "/" + operation
	}
	var lastErr error
	for index, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, secret, err
		}
		client, err := policy.GatewayClient(ctx, entry.BaseURL)
		if err != nil {
			lastErr = err
			continue
		}
		target, err := UpstreamURL(entry.BaseURL, path, query, channel.FormatOpenAIResponses, m.Candidate.UpstreamModel, m.Location, secret)
		if err != nil {
			return nil, secret, err
		}
		request, err := http.NewRequestWithContext(ctx, method, target, body)
		if err != nil {
			return nil, secret, err
		}
		request.Header = UpstreamHeaders(m.Headers, m.Location, secret, m.Candidate.Advanced)
		response, err := client.Do(request)
		// A caller-supplied body is single-use: never replay it after partial send.
		if body != nil {
			return response, secret, err
		}
		if err != nil {
			lastErr = err
			continue
		}
		if response.StatusCode < 500 || index == len(entries)-1 {
			return response, secret, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxFailureBody))
		response.Body.Close()
		lastErr = errors.New("background entrance unavailable")
	}
	if lastErr == nil {
		lastErr = errors.New("background entrance unavailable")
	}
	return nil, secret, lastErr
}

// ServeResponse authenticates every follow-up, binds it to the creating Key,
// and never routes a response identifier to another channel or credential.
func (e *Engine) ServeResponse(w http.ResponseWriter, r *http.Request, cancel bool) {
	key, _, ok := e.authenticate(w, r)
	if !ok {
		return
	}
	if !key.AccountActive || !keyUsable(key, e.Now()) {
		WriteError(w, DialectOf(r), http.StatusUnauthorized, "invalid_api_key", "API Key 已停用或过期", "")
		return
	}
	id := r.PathValue("responseID")
	if e.Jobs == nil || !responseIDPattern.MatchString(id) {
		WriteError(w, DialectOf(r), 404, "response_not_found", "响应不存在", "")
		return
	}
	job, err := e.Jobs.LookupBackground(r.Context(), key.OwnerID, key.KeyID, id)
	if errors.Is(err, ErrBackgroundNotFound) {
		WriteError(w, DialectOf(r), 404, "response_not_found", "响应不存在", "")
		return
	}
	if err != nil {
		WriteError(w, DialectOf(r), 500, "internal_error", "读取响应失败", "")
		return
	}
	e.serveResponseJob(w, r, key, id, cancel, job, nil)
}

func (e *Engine) serveResponseJob(w http.ResponseWriter, r *http.Request, key KeyAuth, id string, cancel bool, job BackgroundJob, owned *resourcestate.Record) {
	var metadata backgroundMetadata
	if json.Unmarshal(job.Metadata, &metadata) != nil {
		WriteError(w, DialectOf(r), 500, "internal_error", "读取响应失败", "")
		return
	}
	if owned != nil {
		if job.CallID != owned.CreationID || job.ResponseID != id || job.ChannelID != metadata.Candidate.ChannelID || metadata.Key.OwnerID != key.OwnerID || metadata.Key.KeyID != key.KeyID || metadata.ModelID != owned.ModelID || !resourceMatch(metadata.Candidate, owned) || !metadata.Candidate.ValidOffering(metadata.ModelID) {
			WriteError(w, DialectOf(r), 404, "resource_unavailable", "资源不存在、归属冲突或已不可用", "")
			return
		}
		_ = cancel
	}
	if len(key.AllowedModels) > 0 && !containsString(key.AllowedModels, metadata.ModelID) {
		WriteError(w, DialectOf(r), 403, "model_not_allowed", "该 API Key 不允许使用此模型", "")
		return
	}
	select {
	case e.followupSlots <- struct{}{}:
		defer func() { <-e.followupSlots }()
	default:
		WriteError(w, DialectOf(r), 429, "too_many_response_reads", "响应读取过多，请稍后重试", job.CallID)
		return
	}
	operation, method := "", http.MethodGet
	if cancel {
		operation, method = "cancel", http.MethodPost
	}
	// Apply the ordinary gateway credential/hop-header rules to live follow-ups.
	// Preserve opaque protocol headers, body and query parameters; UpstreamURL
	// removes the platform's key parameter before adding any upstream credential.
	headers := metadata.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	for name, values := range r.Header {
		headers[name] = values
	}
	metadata.Headers = headers
	var body io.Reader
	if cancel && r.Body != nil && r.Body != http.NoBody {
		if r.ContentLength > maxRequestBytes {
			WriteError(w, DialectOf(r), 413, "request_too_large", "请求体过大", job.CallID)
			return
		}
		body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	}
	ctx, stop := context.WithTimeout(r.Context(), e.CallTimeLimit)
	defer stop()
	timeouts := frozenBackgroundTimeouts(metadata)
	stream := r.URL.Query().Get("stream") == "true"
	var firstByte, total, idle *time.Timer
	if stream && timeouts.ttft > 0 {
		firstByte = time.AfterFunc(timeouts.ttft, stop)
	}
	if !stream && timeouts.request > 0 {
		total = time.AfterFunc(timeouts.request, stop)
	}
	defer func() {
		for _, timer := range []*time.Timer{firstByte, total, idle} {
			if timer != nil {
				timer.Stop()
			}
		}
	}()
	response, secret, err := e.backgroundExchange(ctx, metadata, id, method, operation, r.URL.RawQuery, body)
	if err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			WriteError(w, DialectOf(r), 413, "request_too_large", "请求体过大", job.CallID)
			return
		}
		WriteError(w, DialectOf(r), 502, "upstream_failed", "读取上游响应失败", job.CallID)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Error bodies use the non-streaming request bound after headers, like
		// ordinary inference. They cannot consume an unbounded stream read.
		if firstByte != nil {
			firstByte.Stop()
		}
		if stream && timeouts.request > 0 {
			total = time.AfterFunc(timeouts.request, stop)
		}

		payload, _ := io.ReadAll(io.LimitReader(response.Body, maxFailureBody))
		newUpstreamFailure(response, payload, secret).write(w, job.CallID)
		return
	}
	// Headers are not a first response byte. Track every actual body read,
	// including bounded JSON buffering, with the same timeout stages as inference.
	response.Body = backgroundReadBody{ReadCloser: response.Body, ctx: ctx, read: func() {
		if firstByte != nil {
			firstByte.Stop()
			firstByte = nil
		}
		if stream && timeouts.idle > 0 {
			if idle == nil {
				idle = time.AfterFunc(timeouts.idle, stop)
			} else {
				idle.Reset(timeouts.idle)
			}
		}
	}}
	if owned != nil {
		err := e.forwardBackgroundResource(w, r, response, key, metadata, job, owned, cancel)
		if err != nil {
			WriteError(w, DialectOf(r), 502, "upstream_failed", "读取上游响应失败", job.CallID)
		}
		return
	}
	for name, values := range ClientResponseHeaders(response.Header, response.Uncompressed) {
		w.Header()[name] = values
	}
	w.Header().Set("X-AIHub-Request-Id", job.CallID)
	w.WriteHeader(response.StatusCode)
	buffer := getChunk()
	defer putChunk(buffer)
	for {
		n, readErr := response.Body.Read(*buffer)
		if n > 0 {
			_ = http.NewResponseController(w).SetWriteDeadline(e.Now().Add(clientWriteLimit))
			if _, err := w.Write((*buffer)[:n]); err != nil {
				return
			}
			_ = http.NewResponseController(w).Flush()
		}
		if readErr != nil {
			return
		}
	}
}

// stageBackground reads only the initial non-streaming acknowledgment, so its
// routing metadata can be committed before its identifier reaches the client.
func stageBackground(result *attemptResult) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(result.first), result.response.Body), maxJSONBuffer+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxJSONBuffer {
		return nil, errors.New("background acknowledgment exceeds parser budget")
	}
	result.first = body
	result.firstErr = io.EOF
	return body, nil
}
