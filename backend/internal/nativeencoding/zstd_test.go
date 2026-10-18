package nativeencoding

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestZstdTransportRoundTripAndTruncation(t *testing.T) {
	plain := []byte("{\"input\":\"合成 opaque 内容 \u2603\",\"model\":\"fixture/model\"}")
	wire, err := Encode(context.Background(), "zstd", plain)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(context.Background(), "zstd", wire)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatal("transport changed native bytes", err)
	}
	for _, broken := range [][]byte{nil, []byte("not zstd"), wire[:len(wire)-1]} {
		if _, err = Decode(context.Background(), "zstd", broken); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid or incomplete frame accepted", err)
		}
	}
}

func TestZstdBoundsExpansionAndConcurrentWork(t *testing.T) {
	writer, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	wire := writer.EncodeAll(make([]byte, MaxBytes+1), nil)
	writer.Close()
	if _, err = Decode(context.Background(), "zstd", wire); !errors.Is(err, ErrTooLarge) {
		t.Fatal("expansion limit ignored", err)
	}
	if _, err = Decode(context.Background(), "", make([]byte, MaxBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatal("identity size limit ignored", err)
	}
	slots <- struct{}{}
	slots <- struct{}{}
	defer func() { <-slots; <-slots }()
	if _, err = Decode(context.Background(), "zstd", wire); !errors.Is(err, ErrBusy) {
		t.Fatal("decode did not reject saturation", err)
	}
	if _, err = Encode(context.Background(), "zstd", []byte("{}")); !errors.Is(err, ErrBusy) {
		t.Fatal("encode did not reject saturation", err)
	}
}

func TestRequestEncodingAndCancellation(t *testing.T) {
	for _, h := range [][]string{{"gzip"}, {"zstd, identity"}, {"zstd", "zstd"}} {
		if _, err := Encoding(h); !errors.Is(err, ErrEncoding) {
			t.Fatal("ambiguous/unsupported encoding accepted")
		}
	}
	for _, h := range [][]string{nil, {"identity"}, {""}, {" ZSTD "}} {
		if _, err := Encoding(h); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Encode(ctx, "zstd", []byte("{}")); !errors.Is(err, context.Canceled) {
		t.Fatal("encoding ignored cancel", err)
	}
	wire, err := Encode(context.Background(), "zstd", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(ctx, "zstd", wire); !errors.Is(err, context.Canceled) {
		t.Fatal("decoding ignored cancel", err)
	}
}

func TestIdentityTransportRetainsOriginalBufferWithoutAllocation(t *testing.T) {
	body := []byte("{\"opaque\":\"retain exact bytes\"}")
	allocs := testing.AllocsPerRun(100, func() {
		for _, transform := range []func(context.Context, string, []byte) ([]byte, error){Encode, Decode} {
			out, err := transform(context.Background(), "", body)
			if err != nil || &out[0] != &body[0] {
				panic("identity copied native body")
			}
		}
	})
	if allocs != 0 {
		t.Fatalf("identity allocations = %g", allocs)
	}
}
