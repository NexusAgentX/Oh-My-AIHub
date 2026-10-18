package detection

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

type Limits struct {
	Workers        int
	Queue          int
	PerOwner       int
	RequestBytes   int64
	ResponseBytes  int64
	EventBytes     int
	RequestTimeout time.Duration
	RetainedBytes  int64
}

func DefaultLimits() Limits {
	return Limits{Workers: 2, Queue: 64, PerOwner: 2, RequestBytes: 32 << 20, ResponseBytes: 32 << 20, EventBytes: 1 << 20, RequestTimeout: 180 * time.Second, RetainedBytes: 64 << 20}
}
func (l Limits) Validate() error {
	if l.Workers < 1 || l.Workers > 16 || l.Queue < 1 || l.Queue > 1024 || l.PerOwner < 1 || l.PerOwner > 16 || l.RequestBytes < 1 || l.RequestBytes > 64<<20 || l.ResponseBytes < 1 || l.ResponseBytes > 64<<20 || l.EventBytes < 1 || int64(l.EventBytes) > l.ResponseBytes || l.RequestTimeout < time.Second || l.RequestTimeout > time.Hour || l.RetainedBytes < 1 || l.RetainedBytes > 256<<20 {
		return ErrInvalid
	}
	return nil
}

type Session struct {
	entry         int
	Target        Target
	Parameters    Parameters
	resolved      Resolved
	secret        []byte
	outbound      channel.Outbound
	prepareEgress func(context.Context) (channel.Outbound, error)
	endpoints     map[string]gatewaycontract.Endpoint
	allowed       map[string]bool
	store         Store
	jobID         string
	limits        Limits
	usage         Usage
	source        string
	retained      map[string][]byte
	retainedBytes int64
}
type Request struct {
	EndpointID string
	Path       string
	Query      url.Values
	Headers    http.Header
	Body       []byte
	Category   string
}
type Response struct {
	AttemptID string
	Status    int
	Headers   http.Header
	Body      []byte
}

func (s *Session) Remember(id string, body []byte) error {
	if !safeCode(id) {
		return ErrInvalid
	}
	old := int64(len(s.retained[id]))
	size := int64(len(body))
	if s.retainedBytes-old+size > s.limits.RetainedBytes {
		return ErrBusy
	}
	if _, ok := s.retained[id]; !ok && len(s.retained) >= 256 {
		return ErrBusy
	}
	if s.retained == nil {
		s.retained = map[string][]byte{}
	}
	s.retained[id] = bytes.Clone(body)
	s.retainedBytes += size - old
	return nil
}
func (s *Session) Recall(id string) []byte { return bytes.Clone(s.retained[id]) }
func (s *Session) clear() {
	clear(s.secret)
	for _, body := range s.retained {
		clear(body)
	}
	s.retained = nil
	s.secret = nil
}
func matchPath(template, path string) bool {
	if strings.ContainsAny(path, "?#\\\r\n") || !strings.HasPrefix(path, "/") {
		return false
	}
	want, got := strings.Split(template, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i, piece := range want {
		if piece == got[i] {
			continue
		}
		if !strings.Contains(piece, "{") {
			return false
		}
		prefix, rest, ok := strings.Cut(piece, "{")
		if !ok {
			return false
		}
		field, suffix, ok := strings.Cut(rest, "}")
		if !ok || !strings.HasPrefix(got[i], prefix) || !strings.HasSuffix(got[i], suffix) {
			return false
		}
		encoded := strings.TrimSuffix(strings.TrimPrefix(got[i], prefix), suffix)
		decoded, err := url.PathUnescape(encoded)
		if field == "model" && err == nil && gatewaycontract.ValidModelID(decoded) {
			continue
		}
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/?#\\\x00") {
			return false
		}
	}
	return true
}
func (s *Session) Do(ctx context.Context, input Request) (Response, error) {
	e, ok := s.endpoints[input.EndpointID]
	if !ok || !s.allowed[e.ID] || !matchPath(e.Path, input.Path) || int64(len(input.Body)) > s.limits.RequestBytes || !containsCategory(input.Category) || len(s.resolved.Addresses) == 0 {
		return Response{}, ErrInvalid
	}
	id, err := ids.NewUUID()
	if err != nil {
		return Response{}, err
	}
	start := time.Now()
	attempt := Attempt{ID: id, EndpointID: e.ID, Method: e.Method, Path: e.Path, Category: input.Category, At: start, Reason: "network_error"}
	s.usage.HTTP++
	switch input.Category {
	case "generate":
		s.usage.Generate++
	case "count":
		s.usage.Count++
	case "query":
		s.usage.Query++
	case "cleanup":
		s.usage.Cleanup++
	}
	// One selected entrance per call; scene code may explicitly retry only with
	// known replay safety. A blind automatic retry can duplicate a tool/write.
	base := s.resolved.Addresses[s.entry]
	callCtx, cancel := context.WithTimeout(ctx, s.limits.RequestTimeout)
	defer cancel()
	policy := s.outbound
	if s.prepareEgress != nil {
		policy, err = s.prepareEgress(callCtx)
	}
	var client *http.Client
	if err == nil {
		client, err = policy.FreshClient(callCtx, base)
	}
	var output Response
	if err == nil {
		defer client.CloseIdleConnections()
		var sent atomic.Bool
		callCtx = httptrace.WithClientTrace(callCtx, &httptrace.ClientTrace{WroteHeaders: func() { sent.Store(true) }})
		var target *url.URL
		target, err = url.Parse(base)
		if err == nil {
			target.RawPath = ""
			target.Path = strings.TrimSuffix(target.Path, "/") + input.Path
			query := url.Values{}
			for k, v := range input.Query {
				query[k] = append([]string(nil), v...)
			}
			for _, name := range []string{"key", "api_key", "access_token", "token"} {
				query.Del(name)
			}
			target.RawQuery = query.Encode()
			// Path is already safely escaped by the scenario; preserve those bytes.
			if decoded, decodeErr := url.PathUnescape(target.Path); decodeErr == nil {
				target.RawPath = target.Path
				target.Path = decoded
			} else {
				err = ErrInvalid
			}
		}
		if err == nil {
			var req *http.Request
			req, err = http.NewRequestWithContext(callCtx, e.Method, target.String(), bytes.NewReader(input.Body))
			if err == nil {
				req.Header = input.Headers.Clone()
				if req.Header == nil {
					req.Header = make(http.Header)
				}
				if s.resolved.UserAgent != "" {
					req.Header.Set("User-Agent", s.resolved.UserAgent)
				}
				for _, name := range s.resolved.Headers.Remove {
					req.Header.Del(name)
				}
				for _, v := range s.resolved.Headers.Set {
					req.Header.Set(v.Name, v.Value)
				}
				// Credentials/transport fields cannot be supplied through a scene/header
				// override. Never use a query-string key or log the resulting request.
				for name := range req.Header {
					for _, protected := range []string{"Authorization", "x-api-key", "x-goog-api-key", "Cookie", "Proxy-Authorization", "Content-Length", "Host", "Connection", "Transfer-Encoding"} {
						if strings.EqualFold(name, protected) {
							delete(req.Header, name)
							break
						}
					}
				}
				switch e.Protocol {
				case "native-messages":
					req.Header.Set("x-api-key", string(s.secret))
					if req.Header.Get("anthropic-version") == "" {
						req.Header.Set("anthropic-version", "2023-06-01")
					}
				case "native-generate-content":
					req.Header.Set("x-goog-api-key", string(s.secret))
				default:
					req.Header.Set("Authorization", "Bearer "+string(s.secret))
				}
				var resp *http.Response
				resp, err = client.Do(req)
				attempt.Sent = sent.Load()
				if resp != nil {
					attempt.Status = resp.StatusCode
					attempt.ResponseReceived = true
					output.Status = resp.StatusCode
					output.Headers = resp.Header.Clone()
					output.Body, err = io.ReadAll(io.LimitReader(resp.Body, s.limits.ResponseBytes+1))
					_ = resp.Body.Close()
					if int64(len(output.Body)) > s.limits.ResponseBytes {
						clear(output.Body)
						output.Body = nil
						err = ErrBusy
						attempt.Reason = "body_limit"
					} else if err == nil && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") && oversizedEvent(output.Body, s.limits.EventBytes) {
						clear(output.Body)
						output.Body = nil
						err = ErrBusy
						attempt.Reason = "body_limit"
					} else if err == nil {
						attempt.Reason = "ok"
					}
				}
			}
		}
	}
	if err != nil && attempt.Reason != "body_limit" {
		if errors.Is(err, context.DeadlineExceeded) {
			attempt.Reason = "timeout"
		} else if ctx.Err() != nil {
			attempt.Reason = "cancelled"
		}
	}
	attempt.DurationMS = time.Since(start).Milliseconds()
	output.AttemptID = id
	// Metadata writes have their own bounded deadline so cancellation still
	// records the actual failed/partial attempt. No body/header/secret is saved.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if saveErr := s.store.RecordAttempt(saveCtx, s.jobID, attempt); saveErr != nil {
		return Response{}, saveErr
	}
	return output, err
}
func containsCategory(category string) bool {
	return category == "generate" || category == "count" || category == "query" || category == "cleanup"
}
func (s *Session) Meter(input, output, images, tools int64) error {
	if input < 0 || output < 0 || images < 0 || tools < 0 {
		return ErrInvalid
	}
	s.usage.InputTokens += input
	s.usage.OutputTokens += output
	s.usage.Images += images
	s.usage.Tools += tools
	return nil
}

func oversizedEvent(body []byte, limit int) bool {
	size := 0
	for len(body) > 0 {
		i := bytes.IndexByte(body, '\n')
		var line []byte
		if i < 0 {
			line = body
			body = nil
		} else {
			line = body[:i]
			body = body[i+1:]
		}
		if len(bytes.TrimSuffix(line, []byte("\r"))) == 0 {
			size = 0
			continue
		}
		size += len(line) + 1
		if size > limit {
			return true
		}
	}
	return false
}

func (s *Session) EntryCount() int { return len(s.resolved.Addresses) }
func (s *Session) SelectEntry(index int) error {
	if index < 0 || index >= len(s.resolved.Addresses) {
		return ErrInvalid
	}
	s.entry = index
	return nil
}
