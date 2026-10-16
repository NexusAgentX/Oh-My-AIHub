package gateway

import (
	"fmt"
	"github.com/klauspost/compress/zstd"
	"io"
	"net/http"
	"strings"
	"sync"
)

// chunkPool lends the read buffers of upstream answers. Every answer needs one
// from its first read to its last, and most answers are small, so a fresh
// 32 KiB per answer was a large part of what a small request allocated.
var chunkPool = sync.Pool{New: func() any {
	buffer := make([]byte, chunkSize)
	return &buffer
}}

func getChunk() *[]byte { return chunkPool.Get().(*[]byte) }

// putChunk gives a buffer back. Nothing may use it afterwards: the observer,
// the usage-frame filter and the client's writer all copy what they keep (a
// ResponseWriter does not hold on to the slice it was given).
func putChunk(buffer *[]byte) {
	if buffer != nil {
		chunkPool.Put(buffer)
	}
}

// bodyFirstStage is how much of a request body is allocated before any of it
// has arrived. Most bodies fit; a client that announces more and then sends
// nothing costs this much, not the announced size.
const bodyFirstStage = 64 << 10

// readRequestBody reads the whole request body, at most maxRequestBytes of it.
//
// A body that announces its length (Content-Length) is read into a buffer of
// exactly that size, not into one that io.ReadAll grows by copying from 512
// bytes up; the first bodyFirstStage bytes are read before the rest is
// allocated, so the announcement alone commits no more memory. A body without a
// length, or one announcing more than the limit, is read by io.ReadAll as it
// always was (and fails at the limit the same way).
func readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	reader := http.MaxBytesReader(w, r.Body, maxRequestBytes)
	encoding := strings.TrimSpace(strings.ToLower(r.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		if encoding != "zstd" {
			return nil, fmt.Errorf("unsupported content encoding")
		}
		decoder, err := zstd.NewReader(reader, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20), zstd.WithDecoderMaxWindow(32<<20))
		if err != nil {
			return nil, err
		}
		defer decoder.Close()
		body, err := io.ReadAll(io.LimitReader(decoder, maxRequestBytes+1))
		if err != nil {
			return nil, err
		}
		if len(body) > maxRequestBytes {
			return nil, fmt.Errorf("decoded request exceeds limit")
		}
		r.Header.Del("Content-Encoding")
		r.Header.Del("Content-Length")
		r.ContentLength = int64(len(body))
		return body, nil
	}
	size := r.ContentLength
	if size <= 0 || size > maxRequestBytes {
		return io.ReadAll(reader)
	}
	body := make([]byte, min(size, bodyFirstStage))
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	if int64(len(body)) == size {
		return body, nil
	}
	whole := make([]byte, size)
	copy(whole, body)
	if _, err := io.ReadFull(reader, whole[len(body):]); err != nil {
		return nil, err
	}
	return whole, nil
}
