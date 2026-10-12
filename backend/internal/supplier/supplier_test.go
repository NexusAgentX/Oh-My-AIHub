package supplier

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestExactCanonicalRecognition(t *testing.T) {
	policy, _ := channel.NewOutboundPolicy(nil, nil)
	service := Service{Outbound: policy}
	value, err := service.Normalize(Supplier{Name: "Example", Kind: "relay", WebsiteURL: "https://example.com", Visible: true, Endpoints: []Endpoint{{BaseURL: "https://API.example.com:443/team/v1/"}}})
	if err != nil || value.Endpoints[0].BaseURL != "https://api.example.com/team" {
		t.Fatalf("normalize: %+v %v", value, err)
	}
	if Match([]Supplier{value}, "https://api.example.com/team") == nil {
		t.Fatal("missing exact match")
	}
	for _, address := range []string{"https://api.example.com", "https://api.example.com/team2", "https://other.example.com/team"} {
		if Match([]Supplier{value}, address) != nil {
			t.Fatal("inexact address matched")
		}
	}
	value.Visible = false
	if Match([]Supplier{value}, "https://api.example.com/team") != nil {
		t.Fatal("hidden supplier matched")
	}
	value.Endpoints = append(value.Endpoints, Endpoint{BaseURL: "https://api.example.com/team"})
	if _, err := service.Normalize(value); err == nil {
		t.Fatal("duplicate canonical address accepted")
	}
}
func TestIconValidationAndLocalRasterCopy(t *testing.T) {
	var buffer bytes.Buffer
	_ = png.Encode(&buffer, image.NewNRGBA(image.Rect(0, 0, 240, 160)))
	encoded, err := NormalizeIcon("data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.SplitN(encoded, ",", 2)[1])
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width != 128 || config.Height > 128 {
		t.Fatal("icon not resized")
	}
	for _, raw := range []string{"https://example.com/icon.png", "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>")), "data:image/png;base64,bad"} {
		if _, err := NormalizeIcon(raw); err == nil {
			t.Fatal("invalid icon accepted")
		}
	}
	policy, _ := channel.NewOutboundPolicy(nil, nil)
	service := Service{Outbound: policy}
	if _, _, err := service.FetchIcon(context.Background(), "http://example.com"); err == nil {
		t.Fatal("insecure source accepted")
	}
}

func TestOpenAIOfficialEndpoint(t *testing.T) {
	policy, err := channel.NewOutboundPolicy(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Outbound: policy}
	value, err := service.Normalize(Supplier{Name: "OpenAI", Kind: "official", WebsiteURL: "https://openai.com", Visible: true, Endpoints: []Endpoint{{BaseURL: "https://api.openai.com/v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Endpoints) != 1 || value.Endpoints[0].BaseURL != "https://api.openai.com" {
		t.Fatalf("unexpected endpoints: %+v", value.Endpoints)
	}
	if Match([]Supplier{value}, "https://api.openai.com") == nil {
		t.Fatal("official endpoint not recognized")
	}
}
