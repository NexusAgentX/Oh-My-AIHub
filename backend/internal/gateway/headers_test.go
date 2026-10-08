package gateway

import (
	"net/http"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestUpstreamHeadersDropCredentialsAndApplyRules(t *testing.T) {
	ua := "relay-ua/1"
	client := http.Header{
		"Authorization": {"Bearer sk-aih-secret"}, "Cookie": {"a=b"}, "X-Aihub-Tag": {"batch"}, "Accept-Encoding": {"br"},
		"Connection": {"keep-alive, X-Hop"}, "X-Hop": {"1"}, "X-Forwarded-For": {"1.2.3.4"}, "X-Keep": {"yes"},
		"Anthropic-Version": {"2023-06-01"}, "User-Agent": {"client-ua"}, "X-Drop": {"me"},
	}
	advanced := channel.Advanced{UserAgent: &ua, HeaderRules: channel.HeaderRules{
		Set: []channel.HeaderSet{{Name: "x-extra", Value: "1"}, {Name: "X-Keep", Value: "overwritten"}}, Remove: []string{"x-drop"},
	}}
	got := UpstreamHeaders(client, authBearer, "upstream-key", advanced)
	if got.Get("Authorization") != "Bearer upstream-key" || got.Get("User-Agent") != "relay-ua/1" || got.Get("X-Extra") != "1" || got.Get("X-Keep") != "overwritten" {
		t.Fatalf("headers = %#v", got)
	}
	for _, name := range []string{"Cookie", "X-Aihub-Tag", "Accept-Encoding", "X-Hop", "Connection", "X-Forwarded-For", "X-Drop"} {
		if got.Get(name) != "" {
			t.Fatalf("%s was forwarded: %#v", name, got)
		}
	}
	if got.Get("Anthropic-Version") != "2023-06-01" || got.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %#v", got)
	}
	for location, name := range map[authLocation]string{authXAPIKey: "X-Api-Key", authGoogleHeader: "X-Goog-Api-Key"} {
		headers := UpstreamHeaders(client, location, "k", channel.Advanced{})
		if headers.Get(name) != "k" || headers.Get("Authorization") != "" {
			t.Fatalf("location %d headers = %#v", location, headers)
		}
	}
	query := UpstreamHeaders(client, authQuery, "k", channel.Advanced{})
	if query.Get("Authorization") != "" || query.Get("X-Api-Key") != "" || query.Get("X-Goog-Api-Key") != "" {
		t.Fatalf("query auth must send no credential header: %#v", query)
	}
}

func TestUpstreamURLReplacesGeminiModelAndKeyParameter(t *testing.T) {
	got, err := UpstreamURL("https://relay.example/api", "/v1beta/models/gemini-alias:streamGenerateContent", "alt=sse&key=sk-aih-x&a=%2F", channel.FormatGemini, "gemini-2.5 pro", authQuery, "up/key")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://relay.example/api/v1beta/models/gemini-2.5%20pro:streamGenerateContent?alt=sse&a=%2F&key=up%2Fkey"
	if got != want {
		t.Fatalf("url = %s\nwant  %s", got, want)
	}
	got, _ = UpstreamURL("https://relay.example", "/v1/chat/completions", "", channel.FormatOpenAIChat, "x", authBearer, "k")
	if got != "https://relay.example/v1/chat/completions" {
		t.Fatalf("url = %s", got)
	}
}

func TestClientResponseHeaders(t *testing.T) {
	upstream := http.Header{
		"Content-Type": {"text/event-stream"}, "Set-Cookie": {"s=1"}, "Transfer-Encoding": {"chunked"}, "Content-Length": {"10"},
		"Content-Encoding": {"gzip"}, "X-Request-Id": {"up-1"}, "Connection": {"X-Conn"}, "X-Conn": {"1"},
	}
	got := ClientResponseHeaders(upstream, true)
	if got.Get("Content-Type") == "" || got.Get("X-Request-Id") == "" {
		t.Fatalf("lost headers: %#v", got)
	}
	for _, name := range []string{"Set-Cookie", "Transfer-Encoding", "Content-Length", "Content-Encoding", "X-Conn", "Connection"} {
		if got.Get(name) != "" {
			t.Fatalf("%s kept: %#v", name, got)
		}
	}
	if kept := ClientResponseHeaders(upstream, false); kept.Get("Content-Length") != "10" || kept.Get("Content-Encoding") != "gzip" {
		t.Fatalf("non-decompressed headers = %#v", kept)
	}
}
