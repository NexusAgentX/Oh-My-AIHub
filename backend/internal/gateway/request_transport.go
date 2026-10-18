package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
)

var errContextNamespaceUnavailable = errors.New("context namespace unavailable")

var nativeZstdRequirement = CapabilityRequirement{Layer: "upstream_protocol", ID: "codex-special.RL14/native_zstd_request"}

type codexNamespaceApplied struct{}

type requestCodecError struct{ cause error }

func (e *requestCodecError) Error() string { return "request transport encoding failed" }
func (e *requestCodecError) Unwrap() error { return e.cause }

// needsCodexNamespace reuses the existing root scan. Ordinary inference does
// not parse metadata or allocate a second request/header block.
func needsCodexNamespace(r *http.Request, body []byte, scan objectScan, format channel.Format) bool {
	if strings.HasPrefix(r.URL.Path, "/backend-api/codex/") {
		return true
	}
	if format != channel.FormatOpenAIResponses {
		return false
	}
	for _, name := range []string{"Session-Id", "Thread-Id", "X-Client-Request-Id", "X-Codex-Parent-Thread-Id", "X-Codex-Turn-Metadata"} {
		if len(r.Header.Values(name)) > 0 {
			return true
		}
	}
	for _, member := range scan.entries {
		if member.key != "context" && member.key != "client_metadata" {
			continue
		}
		found := false
		_, _ = walkObject(body[member.start:member.end], 0, func(key []byte, _, _ int) {
			name := string(key)
			if member.key == "context" {
				found = found || name == "session_id"
			} else {
				switch name {
				case "session_id", "thread_id", "parent_thread_id", "forked_from_thread_id", "x-codex-parent-thread-id", "x-codex-turn-metadata":
					found = true
				}
			}
		})
		if found {
			return true
		}
	}
	return false
}

func (e *Engine) isolateCodexRequest(r *http.Request, body []byte, scan objectScan, format channel.Format, key KeyAuth) (*http.Request, []byte, bool, error) {
	if !needsCodexNamespace(r, body, scan, format) {
		return r, body, false, nil
	}
	if e.ContextNamespace == nil {
		return nil, nil, false, errContextNamespaceUnavailable
	}
	output, headers, err := RewriteCodexContext(body, r.Header, principal(key), e.ContextNamespace)
	if err != nil {
		return nil, nil, false, err
	}
	next := r.Clone(context.WithValue(r.Context(), codexNamespaceApplied{}, true))
	next.Header = headers
	return next, output, true, nil
}

func readNativeRequest(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	encoding, err := nativeencoding.Encoding(r.Header.Values("Content-Encoding"))
	if err != nil {
		return nil, "", err
	}
	body, err := readRequestBody(w, r)
	if err != nil {
		return nil, "", err
	}
	decoded, err := nativeencoding.Decode(r.Context(), encoding, body)
	return decoded, encoding, err
}
func transportError(err error) (int, string, string) {
	switch {
	case errors.Is(err, errContextNamespaceUnavailable):
		return 503, "context_namespace_unavailable", "会话隔离服务尚未就绪"
	case errors.Is(err, nativeencoding.ErrBusy):
		return 429, "request_codec_busy", "压缩请求处理繁忙，请稍后重试"
	case errors.Is(err, nativeencoding.ErrInvalid):
		return 400, "invalid_compressed_request", "压缩请求格式无效或不完整"
	case errors.Is(err, nativeencoding.ErrEncoding):
		return 415, "unsupported_content_encoding", "不支持此请求编码"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return 408, "request_codec_timeout", "请求编解码已取消或超时"
	default:
		return 413, "request_too_large", "请求体过大或读取失败"
	}
}
func requestWireHeaders(h http.Header, encoding string) {
	h.Del("Content-Length")
	if encoding == "" {
		h.Del("Content-Encoding")
	} else {
		h.Set("Content-Encoding", encoding)
	}
	// Model/identifier splices and transport encoding invalidate caller digests.
	for _, key := range []string{"Content-MD5", "Digest", "Content-Digest", "Repr-Digest"} {
		h.Del(key)
	}
}

// Approved channel header rules cannot replace an authenticated caller's
// protected context with a shared or foreign session. Absence stays absent.
func contextWireHeaders(out, mapped http.Header) {
	for _, name := range []string{"Session-Id", "Thread-Id", "X-Client-Request-Id", "X-Codex-Parent-Thread-Id", "X-Codex-Turn-Metadata"} {
		out.Del(name)
		if values := mapped.Values(name); len(values) > 0 {
			out[name] = append([]string(nil), values...)
		}
	}
}
