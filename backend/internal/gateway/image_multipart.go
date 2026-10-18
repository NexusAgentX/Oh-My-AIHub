package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
)

type imageMultipart struct {
	models      []entry
	mask        bool
	inputImages int
}

// scanImageMultipart validates a bounded in-memory multipart body without
// writing temporary files. Only model-field bytes are spliced on forwarding;
// boundaries, headers, filenames and uploaded pixels remain byte-identical.
func scanImageMultipart(body []byte, contentType string) (RequestFacts, objectScan, *imageMultipart, error) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return RequestFacts{}, objectScan{}, nil, errors.New("invalid image multipart")
	}
	boundary := params["boundary"]
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	parts := 0
	for {
		part, e := reader.NextRawPart()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return RequestFacts{}, objectScan{}, nil, e
		}
		parts++
		if parts > 128 {
			return RequestFacts{}, objectScan{}, nil, errors.New("too many image parts")
		}
		if _, e = io.Copy(io.Discard, part); e != nil {
			return RequestFacts{}, objectScan{}, nil, e
		}
	}
	marker := []byte("--" + boundary)
	plan := &imageMultipart{}
	fields := map[string]json.RawMessage{}
	// Boundaries are recognized only at MIME delimiter lines. A valid binary
	// part cannot contain an unescaped matching delimiter line by definition.
	pos := 0
	for pos < len(body) {
		at := bytes.Index(body[pos:], marker)
		if at < 0 {
			break
		}
		at += pos
		if at > 0 && (at < 2 || !bytes.Equal(body[at-2:at], []byte("\r\n"))) {
			pos = at + len(marker)
			continue
		}
		start := at + len(marker)
		if bytes.HasPrefix(body[start:], []byte("--")) {
			break
		}
		if !bytes.HasPrefix(body[start:], []byte("\r\n")) {
			pos = start
			continue
		}
		start += 2
		headerEnd := bytes.Index(body[start:], []byte("\r\n\r\n"))
		if headerEnd < 0 {
			return RequestFacts{}, objectScan{}, nil, errors.New("invalid multipart headers")
		}
		headerEnd += start
		headers, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(body[start : headerEnd+4]))).ReadMIMEHeader()
		if err != nil {
			return RequestFacts{}, objectScan{}, nil, err
		}
		valueStart := headerEnd + 4
		next := valueStart
		for {
			v := bytes.Index(body[next:], append([]byte("\r\n"), marker...))
			if v < 0 {
				return RequestFacts{}, objectScan{}, nil, errors.New("unterminated image multipart")
			}
			next += v
			tail := body[next+2+len(marker):]
			if bytes.HasPrefix(tail, []byte("\r\n")) || bytes.HasPrefix(tail, []byte("--")) {
				break
			}
			next += 2 + len(marker)
		}
		_, disp, err := mime.ParseMediaType(headers.Get("Content-Disposition"))
		if err != nil {
			return RequestFacts{}, objectScan{}, nil, err
		}
		name := disp["name"]
		if next > valueStart {
			switch name {
			case "mask":
				plan.mask = true
			case "image", "image[]", "images", "images[]":
				plan.inputImages++
			}
		}
		if disp["filename"] == "" {
			switch name {
			case "model", "size", "quality", "background", "stream":
				if next-valueStart > 4096 {
					return RequestFacts{}, objectScan{}, nil, errors.New("image option exceeds limit")
				}
				value := string(body[valueStart:next])
				encoded, _ := json.Marshal(value)
				if name == "stream" {
					if value == "true" || value == "false" {
						encoded = []byte(value)
					} else {
						return RequestFacts{}, objectScan{}, nil, errors.New("invalid stream flag")
					}
				}
				if previous, ok := fields[name]; ok && !bytes.Equal(previous, encoded) {
					return RequestFacts{}, objectScan{}, nil, errors.New("conflicting image fields")
				}
				fields[name] = encoded
				if name == "model" {
					plan.models = append(plan.models, entry{start: valueStart, end: next})
				}
			}
		}
		pos = next + 2
	}
	meta, _ := json.Marshal(fields)
	facts, scan, err := ScanRequest(meta)
	// A multipart body is never scanned again as JSON by the forwarding path.
	_ = scan
	return facts, objectScan{}, plan, err
}
func (p *imageMultipart) replace(body []byte, model string) []byte {
	if p == nil {
		return body
	}
	changes := make([]splice, 0, len(p.models))
	for _, m := range p.models {
		changes = append(changes, splice{start: m.start, end: m.end, value: []byte(model)})
	}
	return applySplices(body, changes)
}
