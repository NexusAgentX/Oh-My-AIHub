package gateway

import (
	"bufio"
	"bytes"
	"errors"
	"mime"
	"net/textproto"
	"strconv"
	"strings"
)

// multipartFacts locates only small scalar fields in an already bounded body.
// Image bytes are neither decoded nor copied. Replacing the one model span
// preserves boundaries, headers and all image bytes exactly.
func multipartFacts(body []byte, contentType string) (RequestFacts, *entry, error) {
	var facts RequestFacts
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil || media != "multipart/form-data" || len(params["boundary"]) == 0 || len(params["boundary"]) > 70 {
		return facts, nil, errMalformedJSON
	}
	boundary := []byte("--" + params["boundary"])
	if !bytes.HasPrefix(body, boundary) {
		return facts, nil, errMalformedJSON
	}
	var model *entry
	offset := len(boundary)
	for parts := 0; parts < 128; parts++ {
		if bytes.HasPrefix(body[offset:], []byte("--")) {
			if !facts.HasModel {
				return facts, nil, errors.New("model required")
			}
			return facts, model, nil
		}
		if !bytes.HasPrefix(body[offset:], []byte("\r\n")) {
			return facts, nil, errMalformedJSON
		}
		offset += 2
		endHeader := bytes.Index(body[offset:], []byte("\r\n\r\n"))
		if endHeader < 0 || endHeader > 16384 {
			return facts, nil, errMalformedJSON
		}
		headers, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(body[offset : offset+endHeader+4]))).ReadMIMEHeader()
		if err != nil {
			return facts, nil, err
		}
		disposition, p, err := mime.ParseMediaType(headers.Get("Content-Disposition"))
		if err != nil || disposition != "form-data" {
			return facts, nil, errMalformedJSON
		}
		start := offset + endHeader + 4
		end := bytes.Index(body[start:], append([]byte("\r\n"), boundary...))
		if end < 0 {
			return facts, nil, errMalformedJSON
		}
		end += start
		switch p["name"] {
		case "model":
			if model != nil || p["filename"] != "" || end-start > 256 {
				return facts, nil, errMalformedJSON
			}
			facts.Model = string(body[start:end])
			facts.HasModel = facts.Model != ""
			model = &entry{key: "model", start: start, end: end}
		case "stream":
			if end-start > 5 {
				return facts, nil, errMalformedJSON
			}
			facts.Stream, err = strconv.ParseBool(string(body[start:end]))
			if err != nil {
				return facts, nil, err
			}
		}
		offset = end + 2 + len(boundary)
	}
	return facts, nil, errors.New("too many multipart parts")
}
func multipartType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(contentType), "multipart/")
}
