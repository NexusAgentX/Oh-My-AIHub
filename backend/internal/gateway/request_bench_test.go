package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Request-side benchmarks: what the gateway does with a request body before
// the upstream sees it. Each has a "before" that is the way it used to be done
// (kept here, not in the gateway) next to the way it is done now:
//
//	go -C backend test ./internal/gateway -run '^$' -bench 'RequestFacts|UpstreamBody|ReadRequestBody' -benchmem

var benchRequestSizes = []struct {
	name string
	size int
}{{"1KB", 1 << 10}, {"64KB", 64 << 10}, {"1MB", 1 << 20}, {"8MB", 8 << 20}}

// benchRequestBody is a chat request of about the given size: messages of
// prompt text, then the usual parameters, among them the two pricing members.
func benchRequestBody(size int) []byte {
	var body bytes.Buffer
	body.WriteString(`{"model":"gpt-bench","stream":true,"messages":[`)
	message := `{"role":"user","content":"` + strings.Repeat("lorem ipsum dolor sit amet ", 40) + `"}`
	for first := true; body.Len() < size; first = false {
		if !first {
			body.WriteByte(',')
		}
		body.WriteString(message)
	}
	body.WriteString(`],"temperature":0.7,"max_tokens":1024,"enable_thinking":false,"service_tier":"auto"}`)
	return body.Bytes()
}

func BenchmarkRequestFacts(b *testing.B) {
	for _, tc := range benchRequestSizes {
		body := benchRequestBody(tc.size)
		b.Run(tc.name+"/decode-then-scan", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				var pricing struct {
					EnableThinking *bool  `json:"enable_thinking"`
					ServiceTier    string `json:"service_tier"`
				}
				_ = json.Unmarshal(body, &pricing)
				if _, _, err := ScanRequest(body); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/scan", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				facts, _, err := ScanRequest(body)
				if err != nil || facts.EnableThinking == nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkUpstreamBody(b *testing.B) {
	for _, tc := range benchRequestSizes {
		body := benchRequestBody(tc.size)
		_, scan, err := ScanRequest(body)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(tc.name+"/chat-stream/replace-rescan-ensure", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				legacyUpstreamBody(body, scan, "upstream-model", true)
			}
		})
		b.Run(tc.name+"/chat-stream/one-pass", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				UpstreamBody(body, scan, "upstream-model", true)
			}
		})
		b.Run(tc.name+"/same-model/replace", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				legacyUpstreamBody(body, scan, "gpt-bench", false)
			}
		})
		b.Run(tc.name+"/same-model/one-pass", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				UpstreamBody(body, scan, "gpt-bench", false)
			}
		})
	}
}

func BenchmarkReadRequestBody(b *testing.B) {
	for _, tc := range benchRequestSizes {
		body := benchRequestBody(tc.size)
		request := func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			return r
		}
		b.Run(tc.name+"/read-all", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				r := request()
				if _, err := io.ReadAll(http.MaxBytesReader(httptest.NewRecorder(), r.Body, maxRequestBytes)); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/content-length", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := readRequestBody(httptest.NewRecorder(), request()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Logf("sizes: %v", func() []string {
		var sizes []string
		for _, tc := range benchRequestSizes {
			sizes = append(sizes, fmt.Sprintf("%s=%d", tc.name, len(benchRequestBody(tc.size))))
		}
		return sizes
	}())
}
