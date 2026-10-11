package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func bodyOfSize(n int) []byte {
	body := make([]byte, n)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	return body
}

func requestWithBody(body io.Reader, announced int64) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	request.ContentLength = announced
	return request
}

func TestReadRequestBodyReadsWhatIsAnnounced(t *testing.T) {
	for _, size := range []int{0, 1, 90, bodyFirstStage - 1, bodyFirstStage, bodyFirstStage + 1, 3*bodyFirstStage + 17, 1 << 20} {
		want := bodyOfSize(size)
		got, err := readRequestBody(httptest.NewRecorder(), requestWithBody(bytes.NewReader(want), int64(size)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%d bytes: got %d bytes, err %v", size, len(got), err)
		}
		if size > 0 && cap(got) != size {
			t.Fatalf("%d bytes were announced and the buffer has room for %d", size, cap(got))
		}
	}
}

func TestReadRequestBodyReadsInPiecesAsTheyArrive(t *testing.T) {
	want := bodyOfSize(5*bodyFirstStage + 3)
	for name, reader := range map[string]io.Reader{
		"one byte at a time": iotest.OneByteReader(bytes.NewReader(want)),
		"half reads":         iotest.HalfReader(bytes.NewReader(want)),
		"data with eof":      iotest.DataErrReader(bytes.NewReader(want)),
	} {
		got, err := readRequestBody(httptest.NewRecorder(), requestWithBody(reader, int64(len(want))))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: got %d bytes, err %v", name, len(got), err)
		}
	}
}

func TestReadRequestBodyWithoutAnnouncedLengthReadsToTheEnd(t *testing.T) {
	want := bodyOfSize(200_000)
	for _, announced := range []int64{-1, 0} {
		got, err := readRequestBody(httptest.NewRecorder(), requestWithBody(bytes.NewReader(want), announced))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("announced %d: got %d bytes, err %v", announced, len(got), err)
		}
	}
}

// A client that announces more than it sends is a failed read, as it was when
// the whole body was read until its end: the server's body reader reports the
// early end as an error.
func TestReadRequestBodyFailsWhenTheBodyEndsEarly(t *testing.T) {
	for _, size := range []int{10, bodyFirstStage - 1, bodyFirstStage + 10, 3 * bodyFirstStage} {
		_, err := readRequestBody(httptest.NewRecorder(), requestWithBody(bytes.NewReader(bodyOfSize(size/2)), int64(size)))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("announced %d, sent %d: err = %v", size, size/2, err)
		}
	}
	failure := errors.New("connection reset")
	_, err := readRequestBody(httptest.NewRecorder(), requestWithBody(io.MultiReader(bytes.NewReader(bodyOfSize(100)), iotest.ErrReader(failure)), 1000))
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
}

// Announcing a size commits no memory beyond the first stage until that much
// has been sent.
func TestReadRequestBodyAllocatesTheAnnouncedSizeOnlyForABodyThatHasStarted(t *testing.T) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := readRequestBody(httptest.NewRecorder(), requestWithBody(iotest.ErrReader(io.ErrUnexpectedEOF), maxRequestBytes))
	runtime.ReadMemStats(&after)
	if err == nil || after.TotalAlloc-before.TotalAlloc > 1<<20 {
		t.Fatalf("err = %v; announcing %d bytes allocated %d", err, maxRequestBytes, after.TotalAlloc-before.TotalAlloc)
	}
}

func TestReadRequestBodyKeepsTheLimit(t *testing.T) {
	// More announced than the limit: read as before, and stopped at the limit.
	recorder := httptest.NewRecorder()
	_, err := readRequestBody(recorder, requestWithBody(io.LimitReader(zeros{}, maxRequestBytes+10), maxRequestBytes+10))
	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want a MaxBytesError", err)
	}
	// Nothing announced and more sent than the limit.
	_, err = readRequestBody(httptest.NewRecorder(), requestWithBody(io.LimitReader(zeros{}, maxRequestBytes+10), -1))
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want a MaxBytesError", err)
	}
	// Exactly the limit is fine.
	got, err := readRequestBody(httptest.NewRecorder(), requestWithBody(io.LimitReader(zeros{}, maxRequestBytes), maxRequestBytes))
	if err != nil || len(got) != maxRequestBytes {
		t.Fatalf("a body of the limit's size: %d bytes, err %v", len(got), err)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestReadRequestBodyThroughARealServer(t *testing.T) {
	var got []byte
	var gotErr error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotErr = readRequestBody(w, r)
	}))
	defer server.Close()
	want := bodyOfSize(3*bodyFirstStage + 5)
	for name, body := range map[string]io.Reader{
		"announced": bytes.NewReader(want),
		"chunked":   io.MultiReader(bytes.NewReader(want)),
	} {
		response, err := http.Post(server.URL, "application/json", body)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if gotErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: %d bytes, err %v", name, len(got), gotErr)
		}
	}
}

func TestChunkPoolHandsOutFullSizedBuffers(t *testing.T) {
	buffer := getChunk()
	if len(*buffer) != chunkSize {
		t.Fatalf("buffer of %d bytes", len(*buffer))
	}
	putChunk(buffer)
	putChunk(nil) // a stream that never took one gives nothing back
}

// The buffer the first chunk was read into goes on serving the rest of the
// answer. Frames of every size, up to several buffers long, must come out
// whole and in order, and the injected usage frame must still be cut.
func TestStreamBackReusesTheBufferOfTheFirstChunk(t *testing.T) {
	big := "data: {\"choices\":[{\"delta\":{\"content\":\"" + strings.Repeat("x", 3*chunkSize+123) + "\"}}],\"usage\":null}\n\n"
	frames := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}],\"usage\":null}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}],\"usage\":null}\n\n",
		big,
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}],\"usage\":null}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":9}}\n\n",
		"data: [DONE]\n\n",
	}
	buffer := getChunk()
	run := startDrainRun(t, channel.FormatOpenAIChat, 5*time.Second, "", func(r *drainRun) {
		r.call.usageInjected = true
		n := copy(*buffer, frames[0])
		r.result.chunk, r.result.first = buffer, (*buffer)[:n]
	})
	for _, frame := range frames[1:] {
		for len(frame) > 0 { // what the transport hands over in one read is at most a buffer
			n := min(len(frame), chunkSize)
			run.send(frame[:n])
			frame = frame[n:]
		}
	}
	run.end()
	run.wait(2 * time.Second)
	want := strings.Join(append(append([]string(nil), frames[:4]...), frames[5]), "")
	if got := run.client.received(); got != want {
		t.Fatalf("client received %d bytes, want %d", len(got), len(want))
	}
	if finish := run.store.finish; finish.Usage.InputTokens != 7 || finish.Usage.OutputTokens != 9 {
		t.Fatalf("finish = %+v", finish)
	}
}
