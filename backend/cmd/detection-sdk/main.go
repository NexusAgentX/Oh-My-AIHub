// detection-sdk is a fixed SDK probe, invoked only by the detector's controlled
// process runner. It never receives an upstream key or inherits host login.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"google.golang.org/genai"
)

type probeResult struct {
	OK       bool   `json:"ok"`
	Provider string `json:"provider"`
	Version  string `json:"version"`
}

func versions() map[string]string {
	result := map[string]string{}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Replace != nil {
				continue
			}
			if dep.Path == "github.com/anthropics/anthropic-sdk-go" || dep.Path == "google.golang.org/genai" {
				result[dep.Path] = dep.Version
			}
		}
	}
	return result
}

type bridgeTransport struct {
	token string
	next  *http.Transport
}

func (b bridgeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("X-AIHub-SDK-Bridge", b.token)
	return b.next.RoundTrip(copy)
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		_ = json.NewEncoder(os.Stdout).Encode(versions())
		return
	}
	provider := os.Getenv("AIHUB_SDK_PROVIDER")
	result := probeResult{Provider: provider}
	module := map[string]string{"anthropic": "github.com/anthropics/anthropic-sdk-go", "gemini": "google.golang.org/genai"}[provider]
	result.Version = versions()[module]
	defer func() { _ = json.NewEncoder(os.Stdout).Encode(result) }()
	base, err := url.Parse(os.Getenv("AIHUB_SDK_BASE_URL"))
	if len(os.Args) != 2 || os.Args[1] != "--probe" || err != nil || base.Scheme != "http" || base.Hostname() != "127.0.0.1" || base.User != nil || base.Path != "" || base.RawQuery != "" || result.Version == "" {
		return
	}
	token := os.Getenv("AIHUB_SDK_BRIDGE_TOKEN")
	model, prompt, expected := os.Getenv("AIHUB_SDK_MODEL"), os.Getenv("AIHUB_SDK_PROMPT"), os.Getenv("AIHUB_SDK_EXPECTED")
	if len(token) != 64 || model == "" || len(model) > 256 || prompt == "" || expected == "" {
		return
	}
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 1, MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: bridgeTransport{token: token, next: transport}, Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	switch provider {
	case "anthropic":
		// Construct the message service directly with explicit options. The root
		// client's environment/profile credential discovery is never called.
		service := anthropic.NewMessageService(option.WithoutEnvironmentDefaults(), option.WithAPIKey("sdk-placeholder"), option.WithBaseURL(base.String()), option.WithHTTPClient(client), option.WithMaxRetries(0), option.WithHeader("anthropic-version", "2023-06-01"))
		message, err := service.New(ctx, anthropic.MessageNewParams{Model: anthropic.Model(model), MaxTokens: 256, Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))}})
		if err != nil {
			return
		}
		var text strings.Builder
		for _, block := range message.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		result.OK = message.Role == "assistant" && strings.TrimSpace(text.String()) == expected
	case "gemini":
		attempts := int32(1)
		automatic := false
		// This SDK reads default environment variables even with explicit config.
		// It therefore runs only in this fresh-environment child, never in server.
		gc, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: "sdk-placeholder", Backend: genai.BackendGeminiAPI, HTTPClient: client, HTTPOptions: genai.HTTPOptions{BaseURL: base.String(), APIVersion: "v1beta", RetryOptions: &genai.HTTPRetryOptions{Attempts: &attempts}}})
		if err != nil {
			return
		}
		response, err := gc.Models.GenerateContent(ctx, model, genai.Text(prompt), &genai.GenerateContentConfig{MaxOutputTokens: 256, AutomaticContinuation: &automatic})
		if err != nil {
			return
		}
		result.OK = strings.TrimSpace(response.Text()) == expected
	}
}
