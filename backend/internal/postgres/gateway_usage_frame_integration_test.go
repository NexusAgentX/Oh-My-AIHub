package postgres_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The frames of a Chat stream as an upstream sends it once include_usage is
// on: every chunk carries "usage":null, and the end brings a frame with no
// choices and the usage.
const (
	usageContentFrame = "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}],\"usage\":null}\n\n"
	usageFinishFrame  = "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":null}\n\n"
	usageOnlyFrame    = "data: {\"id\":\"c1\",\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n\n"
	usageCommentFrame = ": processed\n\n"
	usageDoneFrame    = "data: [DONE]\n\n"
)

func toCRLF(text string) string { return strings.ReplaceAll(text, "\n", "\r\n") }

// The gateway turns include_usage on to be able to bill a stream; a client
// that did not ask for it must not see the frame that causes. One that did
// gets the upstream's bytes exactly.
func TestGatewayKeepsTheUsageOnlyFrameItAskedForFromClientsThatDidNot(t *testing.T) {
	for _, tc := range []struct {
		name         string
		upstream     string
		stream       string // the client's request body
		sentUpstream string // the body the upstream must receive
		stripped     bool
	}{
		{
			"no stream_options", usageContentFrame + usageFinishFrame + usageOnlyFrame + usageCommentFrame + usageDoneFrame,
			`{"model":"gpt-test","stream":true}`, `{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`, true,
		},
		{
			"include_usage explicitly false", usageContentFrame + usageFinishFrame + usageOnlyFrame + usageDoneFrame,
			`{"model":"gpt-test","stream":true,"stream_options":{"include_usage":false}}`, `{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`, true,
		},
		{
			"stream_options without include_usage", usageContentFrame + usageOnlyFrame + usageDoneFrame,
			`{"model":"gpt-test","stream":true,"stream_options":{"continuous_usage_stats":false}}`, `{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true,"continuous_usage_stats":false}}`, true,
		},
		{
			"CRLF event boundaries", toCRLF(usageContentFrame + usageFinishFrame + usageOnlyFrame + usageDoneFrame),
			`{"model":"gpt-test","stream":true}`, `{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`, true,
		},
		{
			"the client asked for usage itself", usageContentFrame + usageFinishFrame + usageOnlyFrame + usageDoneFrame,
			`{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`, `{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`, false,
		},
		{
			"usage in the last frame that has choices", usageContentFrame + "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n\n" + usageDoneFrame,
			`{"model":"gpt-test","stream":true}`, `{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`, false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newGroupEnv(t)
			consumer := e.member("consumer", "100")
			sharer := e.member("sharer", "0")
			relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
				w.Header().Set("Content-Type", "text/event-stream")
				// Several reads, cut in the middle of frames, as a real network would.
				flusher := w.(http.Flusher)
				data := tc.upstream
				for len(data) > 0 {
					n := min(37, len(data))
					_, _ = io.WriteString(w, data[:n])
					flusher.Flush()
					data = data[n:]
				}
			})
			e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
			_, secret := e.defaultKey(consumer)

			recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(tc.stream), nil)
			if recorder.Code != http.StatusOK {
				t.Fatalf("call = %d %s", recorder.Code, recorder.Body.String())
			}
			if seen := relay.inference()[0].Body; string(seen) != tc.sentUpstream {
				t.Fatalf("upstream received %s, want %s", seen, tc.sentUpstream)
			}
			want := tc.upstream
			if tc.stripped {
				// Exactly the usage-only frame goes; every other byte stays.
				frame := usageOnlyFrame
				if strings.Contains(tc.upstream, "\r\n") {
					frame = toCRLF(usageOnlyFrame)
				}
				want = strings.Replace(tc.upstream, frame, "", 1)
				if want == tc.upstream {
					t.Fatal("the test upstream has no usage-only frame")
				}
			}
			if !bytes.Equal(recorder.Body.Bytes(), []byte(want)) {
				t.Fatalf("client received %q\nwant %q", recorder.Body.String(), want)
			}
			// Billing is the same in every case: the gateway read the usage frame
			// whether or not the client was shown it.
			callID := recorder.Header().Get("X-AIHub-Request-Id")
			row := e.callRow(callID)
			if row.Outcome != "succeeded" || row.Input != 1000 || row.Output != 500 || row.CostNano != 40_000_000 || !row.HasLedgerTx {
				t.Fatalf("call = %+v", row)
			}
			if row.ResponseBytes != int64(len(want)) {
				t.Fatalf("response bytes = %d, want the %d the client received", row.ResponseBytes, len(want))
			}
			e.assertZeroSum()
		})
	}
}
