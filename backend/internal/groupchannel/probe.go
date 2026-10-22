package groupchannel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channelprobe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type ProbeConfig struct {
	Runtime    *Runtime
	Repository Repository
	Keyring    *channel.Keyring
	Outbound   func(context.Context) (channel.Outbound, error)
	Edited     func(string)
	Logger     *slog.Logger
}
type Prober struct {
	ProbeConfig
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	wake    chan struct{}
	queue   chan string
	mu      sync.Mutex
	pending map[string]bool
}

func NewProber(ctx context.Context, c ProbeConfig) *Prober {
	ctx, cancel := context.WithCancel(ctx)
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	p := &Prober{ProbeConfig: c, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), queue: make(chan string, 64), pending: map[string]bool{}}
	c.Runtime.Notify = p.Notify
	p.wg.Add(3)
	go p.produce()
	for range 2 {
		go p.work()
	}
	return p
}
func (p *Prober) Close() { p.cancel(); p.wg.Wait() }
func (p *Prober) Notify() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func pendingModels(state *State, r Record) []market.GroupModel {
	g, err := group(state.Directory, r.GroupID)
	if err != nil || !r.OwnerActive || r.Status == channel.StatusSuspended || len(selected(state.Directory, r)) == 0 {
		return nil
	}
	choices := map[string]bool{}
	for _, c := range state.Data.Choices {
		if c.ChannelID == r.ID {
			choices[c.GroupModelID] = c.Excluded
		}
	}
	probes := map[string]string{}
	for _, v := range state.Data.Probes {
		if v.ChannelID == r.ID {
			probes[v.GroupModelID] = v.Binding
		}
	}
	out := []market.GroupModel{}
	for _, m := range state.Directory.Models {
		if m.GroupID == g.ID && m.Enabled && m.DeletedAt == nil && !choices[m.ID] && probes[m.ID] != ProbeBinding(r, g, m) {
			out = append(out, m)
		}
	}
	return out
}
func (p *Prober) produce() {
	defer p.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	p.Notify()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		case <-p.wake:
		}
		state, err := p.Runtime.Current()
		if err != nil {
			continue
		}
		for _, r := range state.Data.Channels {
			if len(pendingModels(state, r)) == 0 {
				continue
			}
			p.mu.Lock()
			if p.pending[r.ID] {
				p.mu.Unlock()
				continue
			}
			p.pending[r.ID] = true
			select {
			case p.queue <- r.ID:
				p.mu.Unlock()
			default:
				delete(p.pending, r.ID)
				p.mu.Unlock()
			}
			if len(p.queue) == cap(p.queue) {
				break
			}
		}
	}
}
func (p *Prober) work() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case id := <-p.queue:
			p.run(id)
			p.mu.Lock()
			delete(p.pending, id)
			p.mu.Unlock()
		}
	}
}
func (p *Prober) run(id string) {
	state, err := p.Runtime.Current()
	if err != nil {
		return
	}
	var r Record
	for _, v := range state.Data.Channels {
		if v.ID == id {
			r = v
			break
		}
	}
	if r.ID == "" {
		return
	}
	models := pendingModels(state, r)
	if len(models) == 0 {
		return
	}
	var g market.Group
	for _, v := range state.Directory.Groups {
		if v.ID == r.GroupID {
			g = v
			break
		}
	}
	secret, err := p.Keyring.OpenCredential(r.ID, r.Credential)
	if err != nil {
		return
	}
	policy, err := p.Outbound(p.ctx)
	if err != nil {
		return
	}
	effective, _ := ResolveDefaults(r.Overrides, g.Defaults)
	entries := selected(state.Directory, r)
	changed := false
	for _, m := range models {
		if p.ctx.Err() != nil {
			break
		}
		result := Probe{ChannelID: r.ID, GroupModelID: m.ID, Binding: ProbeBinding(r, g, m), Tests: map[channel.Format]channel.FormatTest{}, TestedAt: time.Now()}
		formats := channel.Formats
		if m.ModelType == "image" {
			formats = []channel.Format{channel.FormatOpenAIImages, channel.FormatOpenAIResponses, channel.FormatGemini}
		}
		for _, format := range formats {
			if p.ctx.Err() != nil {
				break
			}
			result.Tests[format] = basicProbeAt(p.ctx, policy, entries, effective, m, format, secret)
		}
		if p.ctx.Err() != nil {
			break
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(p.ctx), 10*time.Second)
		saved, e := p.Repository.SaveProbe(ctx, r, g, m, result)
		cancel()
		if e != nil {
			p.Logger.Warn("saving group channel probe failed", "channel_id", id, "error", e)
			break
		}
		changed = changed || saved
	}
	if changed {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(p.ctx), 20*time.Second)
		err = p.Runtime.Refresh(ctx)
		cancel()
		if err != nil {
			p.Logger.Warn("refreshing group channel probes failed", "channel_id", id, "error", err)
		} else if p.Edited != nil {
			p.Edited(id)
		}
	}
}
func probeBody(f channel.Format, model string) ([]byte, string) {
	var body any
	protocol := ""
	switch f {
	case channel.FormatOpenAIChat:
		protocol = "openai-chat-completions"
		body = map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "Reply OK."}}, "max_completion_tokens": 64, "stream": false}
	case channel.FormatOpenAIResponses:
		protocol = "openai-responses"
		body = map[string]any{"model": model, "input": "Reply OK.", "max_output_tokens": 64, "store": false, "stream": false}
	case channel.FormatAnthropic:
		protocol = "native-messages"
		body = map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "Reply OK."}}, "max_tokens": 64, "stream": false}
	case channel.FormatGemini:
		protocol = "native-generate-content"
		body = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Reply OK."}}}}, "generationConfig": map[string]any{"maxOutputTokens": 64}}
	case channel.FormatOpenAIImages:
		protocol = "openai-images"
		body = map[string]any{"model": model, "prompt": "A small blue square on a white background.", "n": 1}
	}
	b, _ := json.Marshal(body)
	return b, protocol
}
func basicProbe(ctx context.Context, policy channel.Outbound, entries []gateway.Entrance, d market.Defaults, m market.GroupModel, f channel.Format, secret string) (out channel.FormatTest) {
	return basicProbeAt(ctx, policy, entries, d, m, f, secret)
}

func basicProbeAt(ctx context.Context, policy channel.Outbound, entries []gateway.Entrance, d market.Defaults, m market.GroupModel, f channel.Format, secret string) (out channel.FormatTest) {
	started := time.Now()
	out.TestedAt = started
	defer func() { ms := int(time.Since(started).Milliseconds()); out.DurationMS = &ms }()
	limit := 30 * time.Second
	if m.ModelType == "image" {
		limit = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	payload, protocol := probeBody(f, m.UpstreamModel)
	if m.ModelType == "image" && f != channel.FormatOpenAIImages {
		payload = channelprobe.BasicImageRequest(protocol, m.UpstreamModel)
	}
	reason := "upstream_unreachable"
	for _, entry := range entries {
		if ctx.Err() != nil {
			reason = "probe_cancelled_or_timed_out"
			break
		}
		client, err := policy.FreshClient(ctx, entry.BaseURL)
		if err != nil {
			reason = "outbound_validation_failed"
			continue
		}
		target, err := channel.BuildEndpoint(entry.BaseURL, f, m.UpstreamModel, false)
		if err != nil {
			reason = "invalid_upstream_endpoint"
			break
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
		if err != nil {
			reason = "invalid_probe_request"
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Accept-Encoding", "identity")
		if d.HeaderRules != nil {
			for _, name := range d.HeaderRules.Remove {
				req.Header.Del(name)
			}
			for _, rule := range d.HeaderRules.Set {
				req.Header.Set(rule.Name, rule.Value)
			}
		}
		if d.UserAgent != nil {
			req.Header.Set("User-Agent", *d.UserAgent)
		}
		channel.ApplyAuthentication(req, f, secret)
		resp, err := client.Do(req)
		if err != nil {
			reason = "upstream_transport_error"
			continue
		}
		status := resp.StatusCode
		out.StatusCode = &status
		if status >= 500 {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			reason = "upstream_server_error"
			continue
		}
		if status < 200 || status >= 300 {
			resp.Body.Close()
			reason = "upstream_rejected_probe"
			break
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
		resp.Body.Close()
		if readErr != nil || len(body) > 32<<20 {
			reason = "invalid_or_oversize_reply"
			break
		}
		out.OK = channelprobe.ValidateBasicReply(protocol, body)
		if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			out.OK = channelprobe.ValidateBasicStreamReply(protocol, body)
		}
		if m.ModelType == "image" {
			out.OK = channelprobe.ValidateBasicImageReply(protocol, body)
		}
		if !out.OK {
			reason = "invalid_native_reply"
		}
		clear(body)
		if out.OK {
			return out
		}
		break
	}
	out.Error = &reason
	return out
}
