package supplier

import (
	"bytes"
	"context"
	"encoding/base64"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"image"
	"image/png"
	"strings"
	"testing"
)

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
