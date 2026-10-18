package detection

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"mime"
	"strconv"
	"strings"
)

func audioPart(protocol, data, contentType string) nativeObject {
	if protocol == "native-generate-content" {
		return nativeObject{"inlineData": nativeObject{"mimeType": contentType, "data": data}}
	}
	// Responses M04 is explicitly a supplier-extension profile. This wire shape
	// is not represented as an official Responses audio contract or converted
	// into Chat. Official Responses rejects it; native Chat uses this shape.
	return nativeObject{"type": "input_audio", "input_audio": nativeObject{"data": data, "format": "wav"}}
}
func validWave(raw []byte) bool {
	if len(raw) < 44 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(raw[4:8]))+8 != uint64(len(raw)) {
		return false
	}
	formatValid, dataValid := false, false
	blockAlign := 0
	for i := 12; i+8 <= len(raw); {
		kind := string(raw[i : i+4])
		length := int64(binary.LittleEndian.Uint32(raw[i+4 : i+8]))
		start := i + 8
		if length > int64(len(raw)-start) {
			return false
		}
		chunk := raw[start : start+int(length)]
		switch kind {
		case "fmt ":
			if len(chunk) < 16 {
				return false
			}
			format := binary.LittleEndian.Uint16(chunk[:2])
			channels := binary.LittleEndian.Uint16(chunk[2:4])
			rate := binary.LittleEndian.Uint32(chunk[4:8])
			bits := binary.LittleEndian.Uint16(chunk[14:16])
			blockAlign = int(binary.LittleEndian.Uint16(chunk[12:14]))
			formatValid = (format == 1 || format == 3 && bits == 32) && channels >= 1 && channels <= 2 && rate >= 8000 && rate <= 96000 && (bits == 8 || bits == 16 || bits == 24 || bits == 32) && blockAlign == int(channels)*int(bits)/8
		case "data":
			if blockAlign == 0 || len(chunk) < 320 || len(chunk)%blockAlign != 0 {
				return false
			}
			dataValid = sampleVariation(chunk, blockAlign)
		}
		i = start + int(length) + int(length%2)
	}
	return formatValid && dataValid
}
func validNativeAudio(data, contentType string) bool {
	if len(data) == 0 || base64.StdEncoding.DecodedLen(len(data)) > 16<<20 {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	if mediaType == "audio/wav" || mediaType == "audio/x-wav" {
		return validWave(raw)
	}
	if mediaType == "audio/L16" || mediaType == "audio/l16" || mediaType == "audio/pcm" {
		rate, err := strconv.Atoi(params["rate"])
		if err != nil || rate < 8000 || rate > 96000 || len(raw) < 320 || len(raw)%2 != 0 {
			return false
		}
		return sampleVariation(raw, 2)
	}
	return false
}
func sampleVariation(raw []byte, frame int) bool {
	if frame < 1 || len(raw) < 320 || len(raw)%frame != 0 {
		return false
	}
	changes := 0
	for i := frame; i < len(raw); i += frame {
		if !bytes.Equal(raw[i-frame:i], raw[i:i+frame]) {
			changes++
			if changes >= 8 {
				return true
			}
		}
	}
	return false
}
func audioOutput(protocol string, reply nativeReply) (string, string) {
	if len(reply.Candidates) != 1 {
		return "", ""
	}
	if protocol == "openai-chat-completions" && len(reply.Candidates[0].Parts) == 1 {
		return str(object(object(reply.Candidates[0].Parts[0])["audio"])["data"]), "audio/wav"
	}
	if protocol == "native-generate-content" {
		for _, raw := range reply.Candidates[0].Parts {
			data := object(object(raw)["inlineData"])
			if strings.HasPrefix(str(data["mimeType"]), "audio/") {
				return str(data["data"]), str(data["mimeType"])
			}
		}
	}
	return "", ""
}
func (n *nativeScene) audio(ctx context.Context) {
	data := base64.StdEncoding.EncodeToString(nativeFixture("audio-a.wav"))
	body := mediaBody(n.protocol, n.s.Target.Model, "Count the distinct tone bursts in this recording. Reply only with the decimal integer count.", audioPart(n.protocol, data, "audio/wav"))
	if n.protocol == "openai-chat-completions" {
		body["modalities"] = []any{"text"}
	}
	input := n.infer(ctx, body, false, true)
	label := "native_inline_input_when_applicable"
	if n.protocol == "openai-responses" {
		label = "vendor_extension.input_audio"
		if input.Response.Status == 400 {
			root, err := decodeNative(input.Response.Body)
			e := object(root["error"])
			message := strings.ToLower(str(e["message"]))
			if err == nil && nativeError(n.protocol, root) && str(e["code"]) == "invalid_value" && strings.Contains(message, "input_audio") && strings.Contains(message, "supported") {
				input.State = "unsupported"
				input.Reason = "unsupported"
			}
		}
	}
	recognized := contentMatches(input, "2") && nativeEndedNormally(input.Reply)
	n.check(label, input, recognized)
	n.check("actual_content_and_usage", input, recognized && input.Reply.Usage.Reliable)
	if n.protocol == "openai-responses" {
		return
	}
	body = baseNative(n.protocol, n.s.Target.Model, "Say only the synthetic phrase: violet seven.")
	if n.protocol == "openai-chat-completions" {
		body["modalities"] = []any{"text", "audio"}
		body["audio"] = nativeObject{"voice": "alloy", "format": "wav"}
	} else {
		body = baseNative(n.protocol, n.s.Target.Model, "violet seven.")
		body["generationConfig"] = nativeObject{"responseModalities": []any{"AUDIO"}, "speechConfig": nativeObject{"voiceConfig": nativeObject{"voice": "Kore"}}}
	}
	out := n.infer(ctx, body, false, true)
	audio, mime := audioOutput(n.protocol, out.Reply)
	valid := out.Reply.Structure && nativeEndedNormally(out.Reply) && validNativeAudio(audio, mime)
	n.check("native_output_when_applicable", out, valid)
	n.check("actual_content_and_usage", out, valid && out.Reply.Usage.Reliable)
}
func (n *nativeScene) video(ctx context.Context) {
	for _, remote := range []bool{false, true} {
		part := nativeObject{"inlineData": nativeObject{"mimeType": "video/mp4", "data": base64.StdEncoding.EncodeToString(nativeFixture("video-a.mp4"))}}
		label := "inline_video_content_recognition"
		if remote {
			part = nativeObject{"fileData": nativeObject{"mimeType": "video/mp4", "fileUri": nativeFixtureBase + "video-a.mp4"}}
			label = "url_video_content_recognition"
		}
		body := mediaBody(n.protocol, n.s.Target.Model, "Name the dominant color at the beginning and at the end of this video, in English, in time order. Reply only with two color names separated by a space.", part)
		out := n.infer(ctx, body, false, true)
		n.check(label, out, recognizedColors(out, []string{"red", "blue"}))
		n.check("no_files_upload", out, true)
	}
}
