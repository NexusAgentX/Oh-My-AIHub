package detection

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const nativeFixtureCommit = "fd4b33dd945b0197e03a27dd2f7d681b8a508faf"
const nativeFixtureBase = "https://raw.githubusercontent.com/NexusAgentX/Oh-My-AIHub/" + nativeFixtureCommit + "/backend/internal/detection/fixtures/"

//go:embed fixtures/*.png fixtures/*.wav fixtures/*.mp4 fixtures/*.pdf fixtures/SOURCE.json
var nativeFixtureFiles embed.FS

type nativeFixtureSet struct {
	Files   map[string][]byte
	Version string
}

var loadNativeFixtures = sync.OnceValues(func() (*nativeFixtureSet, error) {
	body, err := nativeFixtureFiles.ReadFile("fixtures/SOURCE.json")
	if err != nil {
		return nil, err
	}
	var source struct {
		Files map[string]struct {
			SHA256 string `json:"sha256"`
			Bytes  int    `json:"bytes"`
		} `json:"files"`
	}
	if err = json.Unmarshal(body, &source); err != nil {
		return nil, err
	}
	names := []string{"image-a.png", "image-b.png", "mask-a.png", "audio-a.wav", "video-a.mp4", "document-a.pdf"}
	files := map[string][]byte{}
	h := sha256.New()
	h.Write([]byte(nativeFixtureCommit))
	h.Write(body)
	if len(source.Files) != len(names) {
		return nil, errors.New("synthetic fixture manifest")
	}
	for _, name := range names {
		raw, e := nativeFixtureFiles.ReadFile("fixtures/" + name)
		if e != nil {
			return nil, e
		}
		sum := sha256.Sum256(raw)
		expected := source.Files[name]
		if len(raw) != expected.Bytes || hex.EncodeToString(sum[:]) != expected.SHA256 {
			return nil, errors.New("synthetic fixture checksum")
		}
		files[name] = raw
		h.Write([]byte(name))
		h.Write(sum[:])
	}
	return &nativeFixtureSet{Files: files, Version: hex.EncodeToString(h.Sum(nil))}, nil
})

func nativeFixtures() (map[string][]byte, string, error) {
	f, err := loadNativeFixtures()
	if err != nil {
		return nil, "", err
	}
	return f.Files, f.Version, nil
}

func nativeFixture(name string) []byte {
	files, _, err := nativeFixtures()
	if err != nil {
		panic(err)
	}
	body, ok := files[name]
	if !ok {
		panic(ErrInvalid)
	}
	return body
}
func nativeDataURL(name, mime string) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(nativeFixture(name))
}
func mediaBody(protocol, model, prompt string, parts ...nativeObject) nativeObject {
	body := baseNative(protocol, model, prompt)
	content := []any{}
	switch protocol {
	case "openai-chat-completions", "native-messages":
		content = append(content, nativeObject{"type": "text", "text": prompt})
		for _, p := range parts {
			content = append(content, p)
		}
		object(list(body["messages"])[0])["content"] = content
	case "openai-responses":
		content = append(content, nativeObject{"type": "input_text", "text": prompt})
		for _, p := range parts {
			content = append(content, p)
		}
		object(list(body["input"])[0])["content"] = content
	case "native-generate-content":
		content = append(content, nativeObject{"text": prompt})
		for _, p := range parts {
			content = append(content, p)
		}
		object(list(body["contents"])[0])["parts"] = content
	}
	return body
}
func imagePart(protocol, name string, remote bool) nativeObject {
	uri := nativeDataURL(name, "image/png")
	if remote {
		uri = nativeFixtureBase + name
	}
	switch protocol {
	case "openai-chat-completions":
		return nativeObject{"type": "image_url", "image_url": nativeObject{"url": uri}}
	case "openai-responses":
		return nativeObject{"type": "input_image", "image_url": uri, "detail": "auto"}
	case "native-messages":
		source := nativeObject{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(nativeFixture(name))}
		if remote {
			source = nativeObject{"type": "url", "url": uri}
		}
		return nativeObject{"type": "image", "source": source}
	case "native-generate-content":
		if remote {
			return nativeObject{"fileData": nativeObject{"mimeType": "image/png", "fileUri": uri}}
		}
		return nativeObject{"inlineData": nativeObject{"mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(nativeFixture(name))}}
	}
	return nil
}
func recognizedColors(out nativeOutcome, wanted []string) bool {
	if !out.Reply.Structure || !nativeEndedNormally(out.Reply) {
		return false
	}
	found := []string{}
	for _, word := range strings.FieldsFunc(strings.ToLower(out.Reply.Text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if slices.Contains([]string{"red", "blue", "green", "yellow"}, word) {
			found = append(found, word)
		}
	}
	return slices.Equal(found, wanted)
}
func (n *nativeScene) vision(ctx context.Context) {
	prompt := "Name the left and right panel colors in the image, in English, left to right. Reply only with the two color names separated by a space."
	for _, remote := range []bool{false, true} {
		body := mediaBody(n.protocol, n.s.Target.Model, prompt, imagePart(n.protocol, "image-a.png", remote))
		out := n.infer(ctx, body, false, true)
		label := "inline_content_recognition"
		if remote {
			label = "url_or_uri_content_recognition"
		}
		n.check(label, out, recognizedColors(out, []string{"red", "blue"}))
	}
	body := mediaBody(n.protocol, n.s.Target.Model, "Name the left and right panel colors of each image, in image order, in English. Reply only with four color names separated by spaces.", imagePart(n.protocol, "image-a.png", false), imagePart(n.protocol, "image-b.png", false))
	out := n.infer(ctx, body, false, true)
	n.check("multiple_images_when_native", out, recognizedColors(out, []string{"red", "blue", "green", "yellow"}))
}
func documentPart(protocol string, remote bool) nativeObject {
	data := base64.StdEncoding.EncodeToString(nativeFixture("document-a.pdf"))
	uri := nativeFixtureBase + "document-a.pdf"
	switch protocol {
	case "openai-chat-completions":
		return nativeObject{"type": "file", "file": nativeObject{"filename": "synthetic.pdf", "file_data": "data:application/pdf;base64," + data}}
	case "openai-responses":
		if remote {
			return nativeObject{"type": "input_file", "file_url": uri}
		}
		return nativeObject{"type": "input_file", "filename": "synthetic.pdf", "file_data": "data:application/pdf;base64," + data}
	case "native-messages":
		source := nativeObject{"type": "base64", "media_type": "application/pdf", "data": data}
		if remote {
			source = nativeObject{"type": "url", "url": uri}
		}
		return nativeObject{"type": "document", "source": source, "title": "Synthetic document", "citations": nativeObject{"enabled": true}}
	case "native-generate-content":
		if remote {
			return nativeObject{"fileData": nativeObject{"mimeType": "application/pdf", "fileUri": uri}}
		}
		return nativeObject{"inlineData": nativeObject{"mimeType": "application/pdf", "data": data}}
	}
	return nil
}
func documentRecognized(out nativeOutcome) bool {
	return out.Reply.Structure && nativeEndedNormally(out.Reply) && strings.Contains(out.Reply.Text, "SAPPHIRE-731") && strings.Contains(out.Reply.Text, "ORCHID-264")
}
func validDocumentCitations(protocol string, reply nativeReply) bool {
	if protocol == "native-generate-content" {
		found := false
		for _, raw := range list(reply.Object["candidates"]) {
			candidate := object(raw)
			for _, value := range list(object(candidate["citationMetadata"])["citationSources"]) {
				source := object(value)
				start, sok := fieldInt(source, "startIndex")
				end, eok := fieldInt(source, "endIndex")
				if str(source["uri"]) != nativeFixtureBase+"document-a.pdf" || !sok || !eok || end <= start || end > int64(len(reply.Text)) {
					return false
				}
				span := reply.Text[start:end]
				if !utf8.ValidString(span) || !strings.Contains(span, "SAPPHIRE-731") && !strings.Contains(span, "ORCHID-264") {
					return false
				}
				found = true
			}
		}
		return found
	}
	found := false
	valid := true
	for _, candidate := range reply.Candidates {
		for _, raw := range candidate.Parts {
			part := object(raw)
			parts := []any{part}
			if str(part["type"]) == "message" {
				parts = list(part["content"])
			}
			for _, value := range parts {
				p := object(value)
				citations := list(p["citations"])
				if protocol == "openai-responses" {
					citations = list(p["annotations"])
				}
				for _, rawCitation := range citations {
					c := object(rawCitation)
					found = true
					if protocol == "native-messages" {
						doc, dok := fieldInt(c, "document_index")
						start, sok := fieldInt(c, "start_page_number")
						end, eok := fieldInt(c, "end_page_number")
						quote := str(c["cited_text"])
						valid = valid && dok && doc == 0 && sok && eok && start >= 1 && end > start && end <= 3 && str(c["type"]) == "page_location"
						known := false
						for page, sentence := range []string{"The notebook code is SAPPHIRE-731.", "The delivery code is ORCHID-264."} {
							if quote != "" && strings.Contains(sentence, quote) && strings.Contains(quote, []string{"SAPPHIRE-731", "ORCHID-264"}[page]) && strings.Contains(str(p["text"]), []string{"SAPPHIRE-731", "ORCHID-264"}[page]) && start <= int64(page+1) && end > int64(page+1) {
								known = true
							}
						}
						valid = valid && known
					} else if protocol == "openai-responses" {
						index, ok := fieldInt(c, "index")
						valid = valid && str(c["type"]) == "file_citation" && ok && index == 0 && str(c["file_id"]) != "" && str(c["filename"]) == "synthetic.pdf"
					} else {
						valid = false
					}
				}
			}
		}
	}
	return found && valid
}
func (n *nativeScene) documents(ctx context.Context) {
	remoteOptions := []bool{false, true}
	if n.protocol == "openai-chat-completions" {
		remoteOptions = remoteOptions[:1]
	}
	for _, remote := range remoteOptions {
		body := mediaBody(n.protocol, n.s.Target.Model, "Read both pages. Give the notebook code and the delivery code exactly as written, and cite the document where native citations are available.", documentPart(n.protocol, remote))
		out := n.infer(ctx, body, false, true)
		label := "native_inline_content_recognition"
		if remote {
			label = "native_url_when_supported"
		}
		n.check(label, out, documentRecognized(out))
		n.check("no_external_file_id_authorization", out, true)
		if n.protocol != "openai-chat-completions" {
			if !out.Reply.Annotations {
				if n.protocol == "native-messages" {
					n.uncertain("citations_when_native", out)
				} else {
					n.unobserved("citations_when_native", out)
				}
			} else {
				n.check("citations_when_native", out, documentRecognized(out) && validDocumentCitations(n.protocol, out.Reply))
			}
		}
	}
}
