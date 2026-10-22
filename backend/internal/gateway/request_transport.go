package gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
)

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
