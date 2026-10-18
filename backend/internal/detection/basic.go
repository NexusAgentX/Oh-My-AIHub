package detection

import (
	"encoding/json"
	"net/url"
	"strings"
)

// BasicImageRequest uses the same bounded native image request as full
// detection. It neither converts protocols nor uses an independent Files API.
func BasicImageRequest(protocol, model string) []byte {
	b, _ := json.Marshal(builtinBody(protocol, model, "image_tool"))
	return b
}
func ValidateBasicImageReply(protocol string, body []byte) bool {
	if protocol == "openai-images" {
		return ValidateBasicReply(protocol, body)
	}
	o, err := decodeNative(body)
	if err != nil {
		return false
	}
	r := parseNative(protocol, o)
	if !r.Structure || !nativeEndedNormally(r) {
		return false
	}
	if protocol == "openai-responses" {
		for _, v := range list(o["output"]) {
			item := object(v)
			if str(item["type"]) == "image_generation_call" && str(item["status"]) == "completed" {
				if _, ok := inspectImage(str(item["result"])); ok {
					return true
				}
			}
		}
		return false
	}
	for _, candidate := range list(o["candidates"]) {
		for _, part := range list(object(object(candidate)["content"])["parts"]) {
			data := object(object(part)["inlineData"])
			if strings.HasPrefix(str(data["mimeType"]), "image/") {
				if _, ok := inspectImage(str(data["data"])); ok {
					return true
				}
			}
		}
	}
	return false
}

// ValidateBasicReply shares the native parsers with full detection. This only
// proves a credential's basic callability; it grants no advanced capability.
func ValidateBasicReply(protocol string, body []byte) bool {
	o, err := decodeNative(body)
	if err != nil || o["error"] != nil {
		return false
	}
	if protocol == "openai-images" {
		if len(list(o["data"])) == 0 {
			return false
		}
		for _, v := range list(o["data"]) {
			p := object(v)
			if encoded := str(p["b64_json"]); encoded != "" {
				if _, ok := inspectImage(encoded); !ok {
					return false
				}
				continue
			}
			u, err := url.Parse(str(p["url"]))
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
				return false
			}
		}
		return true
	}
	r := parseNative(protocol, o)
	// A small probe may spend its output budget entirely on hidden reasoning.
	// A valid native completion with reliable generated-token usage still
	// proves Key callability; it does not prove any reasoning capability.
	return r.Structure && (r.Text != "" || r.Images > 0 || r.Usage.Reliable && r.Usage.Output > 0)
}
