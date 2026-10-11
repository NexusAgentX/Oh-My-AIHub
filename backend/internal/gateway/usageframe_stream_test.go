package gateway

import (
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// streamThrough plays an upstream answer through streamBack and returns what
// the client received and what was settled.
func streamThrough(t *testing.T, format channel.Format, injected bool, contentType string, chunks ...string) (*drainRun, string) {
	t.Helper()
	run := startDrainRun(t, format, 5*time.Second, chunks[0], func(r *drainRun) {
		r.call.usageInjected = injected
		if contentType != "" {
			r.result.response.Header.Set("Content-Type", contentType)
		}
	})
	for _, chunk := range chunks[1:] {
		run.send(chunk)
	}
	run.end()
	run.wait(2 * time.Second)
	return run, run.client.received()
}

func TestInjectedUsageFrameIsKeptFromTheClientButStillBilled(t *testing.T) {
	upstream := contentEvent + finishEvent + usageEvent + doneEvent
	stripped := contentEvent + finishEvent + doneEvent
	run, got := streamThrough(t, channel.FormatOpenAIChat, true, "", contentEvent, finishEvent+usageEvent, doneEvent)
	if got != stripped {
		t.Fatalf("client received %q\nwant %q (upstream sent %q)", got, stripped, upstream)
	}
	finish := run.store.finish
	if finish.Outcome != OutcomeSucceeded || finish.Usage.InputTokens != 1000 || finish.Usage.OutputTokens != 500 || finish.Bill == nil {
		t.Fatalf("finish = %+v", finish)
	}
	if finish.ResponseBytes != int64(len(stripped)) {
		t.Fatalf("response bytes = %d, want only what the client was sent: %d", finish.ResponseBytes, len(stripped))
	}
}

func TestUsageFrameCutAcrossReadsIsStillKept(t *testing.T) {
	_, got := streamThrough(t, channel.FormatOpenAIChat, true, "",
		contentEvent+"data: {\"choices\":[],\"usa", "ge\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n", "\ndata: [DO", "NE]\n\n")
	if want := contentEvent + doneEvent; got != want {
		t.Fatalf("client received %q, want %q", got, want)
	}
}

func TestAnEventThatNeverEndsIsPassedOnAtTheEndOfTheStream(t *testing.T) {
	cut := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n"
	run, got := streamThrough(t, channel.FormatOpenAIChat, true, "", contentEvent, cut)
	if want := contentEvent + cut; got != want {
		t.Fatalf("client received %q, want %q", got, want)
	}
	if run.store.finish.ResponseBytes != int64(len(contentEvent+cut)) {
		t.Fatalf("response bytes = %d", run.store.finish.ResponseBytes)
	}
}

func TestUsageFrameIsLeftAloneWhenTheClientAskedForUsage(t *testing.T) {
	upstream := contentEvent + usageEvent + doneEvent
	run, got := streamThrough(t, channel.FormatOpenAIChat, false, "", contentEvent, usageEvent, doneEvent)
	if got != upstream {
		t.Fatalf("client received %q, want %q", got, upstream)
	}
	if run.store.finish.Usage.OutputTokens != 500 {
		t.Fatalf("finish = %+v", run.store.finish)
	}
}

func TestUsageFrameIsLeftAloneOutsideAChatEventStream(t *testing.T) {
	for _, tc := range []struct {
		name        string
		format      channel.Format
		contentType string
	}{
		{"not an event stream", channel.FormatOpenAIChat, "application/json"},
		{"anthropic", channel.FormatAnthropic, ""},
		{"responses", channel.FormatOpenAIResponses, ""},
		{"gemini", channel.FormatGemini, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := contentEvent + usageEvent + doneEvent
			_, got := streamThrough(t, tc.format, true, tc.contentType, contentEvent, usageEvent, doneEvent)
			if got != upstream {
				t.Fatalf("client received %q, want %q", got, upstream)
			}
		})
	}
}

func TestUsageInTheLastFrameWithChoicesIsLeftAlone(t *testing.T) {
	last := "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n\n"
	upstream := contentEvent + last + doneEvent
	run, got := streamThrough(t, channel.FormatOpenAIChat, true, "", contentEvent, last, doneEvent)
	if got != upstream {
		t.Fatalf("client received %q, want %q", got, upstream)
	}
	if run.store.finish.Usage.OutputTokens != 500 {
		t.Fatalf("finish = %+v", run.store.finish)
	}
}
