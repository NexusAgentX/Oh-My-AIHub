package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestResourceSSERegistersBeforeAnyIdentifierByteAndPreservesWire(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		raw := []byte(": heartbeat" + newline + newline + `data: {"type":"response.created","response":{"object":"response","id":"resp-owned"}}` + newline + newline)
		for _, cut := range []int{1, 17, len(raw)} {
			var sent bytes.Buffer
			registered := false
			gate := resourceEventGate{observe: func(body []byte) error {
				if bytes.Contains(body, []byte("resp-owned")) {
					registered = true
				}
				return nil
			}, send: func(data []byte) {
				if bytes.Contains(data, []byte("resp-owned")) && !registered {
					t.Fatal("ID emitted before ownership")
				}
				sent.Write(data)
			}}
			for i := 0; i < len(raw); i += cut {
				if err := gate.write(raw[i:min(i+cut, len(raw))]); err != nil {
					t.Fatal(err)
				}
			}
			if err := gate.finish(); err != nil || !registered || !bytes.Equal(sent.Bytes(), raw) {
				t.Fatalf("wire changed cut=%d newline=%q: %v", cut, newline, err)
			}
		}
	}
	var sent bytes.Buffer
	fail := errors.New("database failed")
	gate := resourceEventGate{observe: func([]byte) error { return fail }, send: func(data []byte) { sent.Write(data) }}
	if err := gate.write([]byte("data: {\"id\":\"private\"}\n\n")); !errors.Is(err, fail) || sent.Len() != 0 {
		t.Fatal("failed ownership leaked a partial ID", err, sent.String())
	}
}
func TestNativeResourceProducerIgnoresOpaqueIDsAndTracksActualRemoteAudio(t *testing.T) {
	o := resourceObserver{format: channel.FormatOpenAIResponses, store: false}
	rows, err := o.observe([]byte(`{"object":"response","id":"response-not-stored","output":[{"type":"reasoning","id":"opaque-r","encrypted_content":"cipher"},{"type":"compaction","id":"opaque-c","encrypted_content":"cipher"},{"type":"code_interpreter_call","container_id":"cntr-produced"}]}`))
	if err != nil || len(rows) != 1 || rows[0].Kind != "container" {
		t.Fatal(rows, err)
	}
	o = resourceObserver{format: channel.FormatOpenAIChat}
	rows, err = o.observe([]byte(`{"object":"chat.completion","id":"not-stored","choices":[{"message":{"role":"assistant","audio":{"id":"audio-produced","data":"private-content"}}}]}`))
	if err != nil || len(rows) != 1 || rows[0].Kind != "audio" || rows[0].ID != "audio-produced" {
		t.Fatal(rows, err)
	}
	o = resourceObserver{format: channel.FormatOpenAIResponses, store: true, expectedResponseID: "resp-owned"}
	if _, err = o.observe([]byte(`{"object":"response","id":"resp-other"}`)); err == nil {
		t.Fatal("owned read accepted another response identity")
	}
}
func TestImageMultipartOnlyModelBytesChange(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.SetBoundary("aihub-synthetic-boundary")
	_ = writer.WriteField("model", "platform/model:2026")
	_ = writer.WriteField("prompt", "中文 prompt with model text and opaque bytes")
	_ = writer.WriteField("quality", "high")
	file, err := writer.CreateFormFile("image[]", "synthetic.png")
	if err != nil {
		t.Fatal(err)
	}
	pixels := []byte{0, 1, 2, 3, 255, 0, 10, 13}
	_, _ = file.Write(pixels)
	_ = writer.Close()
	raw := buf.Bytes()
	facts, _, plan, err := scanImageMultipart(raw, writer.FormDataContentType())
	if err != nil || facts.Model != "platform/model:2026" || len(plan.models) != 1 {
		t.Fatal(facts, err)
	}
	changed := plan.replace(raw, "native/model:2026")
	want := bytes.Replace(raw, []byte("platform/model:2026"), []byte("native/model:2026"), 1)
	if !bytes.Equal(changed, want) || !bytes.Contains(changed, pixels) {
		t.Fatal("multipart file/header/boundary changed")
	}
	if _, _, _, err = scanImageMultipart(append([]byte(nil), raw[:len(raw)-15]...), writer.FormDataContentType()); err == nil {
		t.Fatal("truncated multipart accepted")
	}
}
func TestNativeImageStreamingBillsCompletedProductOnce(t *testing.T) {
	for _, format := range []string{"image_generation", "image_edit"} {
		raw := fmt.Sprintf("data: {\"type\":%q,\"b64_json\":\"partial\"}\n\ndata: {\"type\":%q,\"b64_json\":\"final\",\"size\":\"1024x1024\"}\n\n", format+".partial_image", format+".completed")
		got := observe(channel.FormatOpenAIImages, true, "text/event-stream", raw, 1)
		if len(got.Units) != 2 || got.Units[1].QuantityNano != 1_000_000_000 || got.Found {
			t.Fatal("partial image or requested count became usage", got)
		}
		if strings.Contains(fmt.Sprint(got.Units), "partial") || strings.Contains(fmt.Sprint(got.Units), "final") {
			t.Fatal("image content retained")
		}
	}
}
