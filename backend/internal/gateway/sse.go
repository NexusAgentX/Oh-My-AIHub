package gateway

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/secretguard"
)

type SSEAnalysis struct {
	Frame               []byte
	Semantic            bool
	Terminal            bool
	StreamEnd           bool
	ErrorCode           string
	ErrorMessage        string
	CredentialFragments []SSECredentialFragment
	Observation         UsageObservation
	UsageState          UsageState
	EventNameMismatch   bool
}

type SSECredentialFragment struct {
	StreamKey string
	Text      string
}

func rebuildSSEFrame(eventName string, data []byte) []byte {
	var buffer bytes.Buffer
	if eventName != "" {
		buffer.WriteString("event: ")
		buffer.WriteString(eventName)
		buffer.WriteByte('\n')
	}
	buffer.WriteString("data: ")
	buffer.Write(data)
	buffer.WriteString("\n\n")
	return buffer.Bytes()
}

func splitSSEData(frame []byte) (data []byte, eventName string, ok bool, err error) {
	lines := bytes.Split(frame, []byte("\n"))
	dataParts := make([][]byte, 0)
	for _, line := range lines {
		trimmed := bytes.TrimSuffix(line, []byte("\r"))
		switch {
		case len(trimmed) == 0, bytes.HasPrefix(trimmed, []byte(":")):
			continue
		case bytes.HasPrefix(trimmed, []byte("data:")):
			part := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
			dataParts = append(dataParts, part)
		case bytes.HasPrefix(trimmed, []byte("event:")):
			// The SSE specification lets the last event field win, and an empty
			// event field resets the event type.
			name := strings.TrimSpace(string(bytes.TrimPrefix(trimmed, []byte("event:"))))
			if strings.ContainsAny(name, "\r\n") {
				continue
			}
			eventName = name
		case bytes.HasPrefix(trimmed, []byte("id:")), bytes.HasPrefix(trimmed, []byte("retry:")):
			// Transport metadata and comments are deliberately not forwarded. They
			// are not part of the protocol billing contract and otherwise create a
			// second unstructured credential-echo channel.
			continue
		default:
			// Unknown SSE fields are ignored per the specification instead of
			// failing the whole stream.
			continue
		}
	}
	if len(dataParts) == 0 {
		return nil, eventName, false, nil
	}
	return bytes.Join(dataParts, []byte("\n")), eventName, true, nil
}

func streamingCredentialFragments(protocol channel.Protocol, value map[string]any) ([]SSECredentialFragment, error) {
	fragments := make([]SSECredentialFragment, 0, min(MaxSSECredentialStreams, 32))
	if err := appendJSONCredentialFragments(&fragments, string(protocol), value); err != nil {
		return nil, err
	}
	return fragments, nil
}

func appendJSONCredentialFragments(fragments *[]SSECredentialFragment, base string, raw any) error {
	appendFragment := func(key, text string) error {
		if text == "" {
			return nil
		}
		if len(*fragments) >= MaxSSECredentialStreams {
			return ErrResponseTooBig
		}
		*fragments = append(*fragments, SSECredentialFragment{StreamKey: key, Text: text})
		return nil
	}
	switch value := raw.(type) {
	case string:
		return appendFragment(base, value)
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for position, key := range keys {
			// Keys are untrusted strings too. Ordinal-by-parent keeps a split key
			// continuous across events without joining values from different paths.
			if err := appendFragment(base+"/@key/"+strconv.Itoa(position), key); err != nil {
				return err
			}
			if err := appendJSONCredentialFragments(fragments, base+"/"+credentialPathSegment(key), value[key]); err != nil {
				return err
			}
		}
	case []any:
		for index, item := range value {
			segment := strconv.Itoa(index)
			if object, ok := item.(map[string]any); ok {
				if itemIndex, valid := intField(object, "index"); valid {
					segment = "index=" + strconv.FormatInt(itemIndex, 10)
				} else {
					for _, identityKey := range []string{"id", "item_id", "event_id"} {
						if identityValue := stringField(object, identityKey); identityValue != "" {
							segment = identityKey + "=" + credentialPathSegment(identityValue)
							break
						}
					}
				}
			}
			if err := appendJSONCredentialFragments(fragments, base+"/["+segment+"]", item); err != nil {
				return err
			}
		}
	}
	return nil
}

func credentialPathSegment(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

type bufferedStreamFrame struct {
	data     []byte
	semantic bool
}

type streamingCredentialGuard struct {
	credentials []string
	tails       map[string]string
	maxTail     int
}

func newStreamingCredentialGuard(credentials ...string) *streamingCredentialGuard {
	filtered := make([]string, 0, len(credentials))
	maximum := 0
	for _, credential := range credentials {
		if credential == "" {
			continue
		}
		filtered = append(filtered, credential)
		maximum = max(maximum, len(credential)-1)
	}
	return &streamingCredentialGuard{credentials: filtered, tails: make(map[string]string), maxTail: maximum}
}

func (g *streamingCredentialGuard) containsFrame(frame []byte) bool {
	return secretguard.ContainsExactOrJSONEscaped(string(frame), g.credentials...)
}

func (g *streamingCredentialGuard) containsFragments(fragments []SSECredentialFragment) (bool, error) {
	for _, fragment := range fragments {
		if _, exists := g.tails[fragment.StreamKey]; !exists && len(g.tails) >= MaxSSECredentialStreams {
			return false, ErrResponseTooBig
		}
		combined := g.tails[fragment.StreamKey] + fragment.Text
		if secretguard.ContainsExact(combined, g.credentials...) {
			return true, nil
		}
		if g.maxTail <= 0 {
			continue
		}
		if len(combined) > g.maxTail {
			combined = combined[len(combined)-g.maxTail:]
		}
		g.tails[fragment.StreamKey] = combined
	}
	return false, nil
}

func sseFrameContainsDecodedCredential(frame []byte, credentials ...string) bool {
	data, _, ok, err := splitSSEData(frame)
	if err != nil || !ok || strings.TrimSpace(string(data)) == "[DONE]" {
		return false
	}
	return secretguard.ContainsExactInJSON(data, credentials...)
}

func readSSEFrameWithTimeout(reader *bufio.Reader, body io.Closer, timeout time.Duration) ([]byte, error) {
	type result struct {
		frame []byte
		err   error
	}
	resultChannel := make(chan result, 1)
	go func() {
		frame, err := readSSEFrame(reader)
		resultChannel <- result{frame: frame, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case value := <-resultChannel:
		return value.frame, value.err
	case <-timer.C:
		_ = body.Close()
		return nil, context.DeadlineExceeded
	}
}

func readSSEFrame(reader *bufio.Reader) ([]byte, error) {
	buffer := bytes.Buffer{}
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if buffer.Len() > MaxSSEEventBytes-len(fragment) {
				return nil, ErrResponseTooBig
			}
			buffer.Write(fragment)
			if err == nil && len(bytes.TrimSpace(fragment)) == 0 {
				return buffer.Bytes(), nil
			}
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if buffer.Len() > 0 {
				return buffer.Bytes(), nil
			}
			return nil, io.EOF
		default:
			return nil, err
		}
	}
}

func writeStreamFrame(w http.ResponseWriter, frame []byte) error {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(StreamingIdleTimeout))
	if written, err := w.Write(frame); err != nil {
		return err
	} else if written != len(frame) {
		return io.ErrShortWrite
	}
	return controller.Flush()
}
