// Package nativeencoding bounds request transport compression. It performs no
// protocol conversion and never persists plaintext or compressed payloads.
package nativeencoding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const MaxBytes = 32 << 20

var (
	ErrEncoding = errors.New("unsupported request content encoding")
	ErrInvalid  = errors.New("invalid compressed request")
	ErrTooLarge = errors.New("request transport size limit exceeded")
	ErrBusy     = errors.New("request codec capacity exhausted")
	slots       = make(chan struct{}, 2)
)

func Encoding(values []string) (string, error) {
	if len(values) > 1 {
		return "", ErrEncoding
	}
	value := ""
	if len(values) == 1 {
		value = strings.ToLower(strings.TrimSpace(values[0]))
	}
	switch value {
	case "", "identity":
		return "", nil
	case "zstd":
		return "zstd", nil
	default:
		return "", ErrEncoding
	}
}
func acquire() bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

type contextReader struct {
	context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.Context.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

type boundedWriter struct {
	ctx    context.Context
	buffer bytes.Buffer
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > MaxBytes-w.buffer.Len() {
		return 0, ErrTooLarge
	}
	return w.buffer.Write(p)
}
func Decode(ctx context.Context, encoding string, wire []byte) ([]byte, error) {
	if len(wire) > MaxBytes {
		return nil, ErrTooLarge
	}
	if encoding == "" {
		return wire, nil
	}
	if encoding != "zstd" {
		return nil, ErrEncoding
	}
	if len(wire) == 0 {
		return nil, ErrInvalid
	}
	if !acquire() {
		return nil, ErrBusy
	}
	defer func() { <-slots }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	decoder, err := zstd.NewReader(contextReader{ctx, bytes.NewReader(wire)}, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(64<<20), zstd.WithDecoderMaxWindow(MaxBytes))
	if err != nil {
		return nil, ErrInvalid
	}
	defer decoder.Close()
	body, err := io.ReadAll(io.LimitReader(contextReader{ctx, decoder}, MaxBytes+1))
	if len(body) > MaxBytes {
		return nil, ErrTooLarge
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, zstd.ErrWindowSizeExceeded) || errors.Is(err, zstd.ErrDecoderSizeExceeded) {
			return nil, ErrTooLarge
		}
		return nil, ErrInvalid
	}
	return body, nil
}
func Encode(ctx context.Context, encoding string, body []byte) ([]byte, error) {
	if len(body) > MaxBytes {
		return nil, ErrTooLarge
	}
	if encoding == "" {
		return body, nil
	}
	if encoding != "zstd" {
		return nil, ErrEncoding
	}
	if !acquire() {
		return nil, ErrBusy
	}
	defer func() { <-slots }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output := &boundedWriter{ctx: ctx}
	encoder, err := zstd.NewWriter(output, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(1<<20), zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, ErrInvalid
	}
	// Write in chunks so cancellation is observed between bounded zstd blocks.
	_, copyErr := io.CopyBuffer(encoder, contextReader{ctx, bytes.NewReader(body)}, make([]byte, 64<<10))
	closeErr := encoder.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return output.buffer.Bytes(), nil
}
