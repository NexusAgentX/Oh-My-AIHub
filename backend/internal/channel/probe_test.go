package channel

import (
	"fmt"
	"testing"
)

func TestMatchModelIsExactThenCaseInsensitiveThenDateStripped(t *testing.T) {
	catalog := []string{"gpt-5", "claude-sonnet-4-5", "gemini-2.5-flash"}
	cases := map[string]string{
		"gpt-5": "gpt-5", "GPT-5": "gpt-5", "claude-sonnet-4-5-20250929": "claude-sonnet-4-5", "Claude-Sonnet-4-5@20250929": "claude-sonnet-4-5",
		"gemini-2.5-flash-2025-06-17": "gemini-2.5-flash", "gpt-5-mini": "", "unknown": "",
	}
	for upstream, want := range cases {
		got := MatchModel(upstream, catalog)
		switch {
		case want == "" && got != nil:
			t.Fatalf("%s matched %s", upstream, *got)
		case want != "" && (got == nil || *got != want):
			t.Fatalf("%s = %v, want %s", upstream, got, want)
		}
	}
}

func TestSuggestFormatsFollowTheModelFamily(t *testing.T) {
	cases := map[string][]Format{
		"claude-sonnet-4-5": {FormatOpenAIChat, FormatAnthropic}, "anthropic/claude-3-opus": {FormatOpenAIChat, FormatAnthropic},
		"gpt-5": {FormatOpenAIChat, FormatOpenAIResponses}, "o3-mini": {FormatOpenAIChat, FormatOpenAIResponses},
		"gemini-2.5-pro": {FormatOpenAIChat, FormatGemini}, "deepseek-chat": {FormatOpenAIChat}, "ollama-x": {FormatOpenAIChat},
	}
	for model, want := range cases {
		if got := SuggestFormats(model); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s = %v, want %v", model, got, want)
		}
	}
}

func TestValidateAdvancedRejectsForbiddenHeadersAndBadRanges(t *testing.T) {
	ua := "agent/1"
	bad := []Advanced{
		{HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "Authorization", Value: "x"}}}},
		{HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "host", Value: "x"}}}},
		{HeaderRules: HeaderRules{Remove: []string{"Content-Length"}}},
		{HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "X-Ok", Value: "line\nbreak"}}}},
		{HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "bad name", Value: "x"}}}},
	}
	zero, tooHigh, short, long := int32(0), int32(200), int32(500), int32(5)
	bad = append(bad, Advanced{ConcurrencyLimit: &zero}, Advanced{CooldownFailures: &tooHigh}, Advanced{TTFTTimeoutMS: &short},
		Advanced{TTFTTimeoutMS: &[]int32{60000}[0], TotalTimeoutMS: &[]int32{30000}[0]}, Advanced{CooldownSeconds: &long})
	for index, advanced := range bad {
		if err := ValidateAdvanced(advanced); err == nil {
			t.Fatalf("case %d was accepted: %+v", index, advanced)
		}
	}
	good := Advanced{UserAgent: &ua, HeaderRules: HeaderRules{Set: []HeaderSet{{Name: "X-Tenant", Value: "acme"}}, Remove: []string{"X-Debug"}}}
	if err := ValidateAdvanced(good); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeModelsDefaultsUpstreamAndOrdersFormats(t *testing.T) {
	known := func(id string) bool { return id == "gpt-5" || id == "claude" }
	got, err := NormalizeModels([]Model{{ModelID: " gpt-5 ", MultiplierNano: 1_500_000_000, Formats: []Format{FormatOpenAIResponses, FormatOpenAIChat}}}, known)
	if err != nil || got[0].UpstreamModel != "gpt-5" || fmt.Sprint(got[0].Formats) != "[openai_chat openai_responses]" {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, bad := range [][]Model{
		nil,
		{{ModelID: "missing", Formats: []Format{FormatOpenAIChat}}},
		{{ModelID: "gpt-5", Formats: nil}},
		{{ModelID: "gpt-5", Formats: []Format{"smoke"}}},
		{{ModelID: "gpt-5", Formats: []Format{FormatOpenAIChat, "smoke"}}},
		{{ModelID: "gpt-5", Formats: []Format{FormatOpenAIChat}, MultiplierNano: MaxMultiplierNano + 1}},
		{{ModelID: "gpt-5", Formats: []Format{FormatOpenAIChat}}, {ModelID: "gpt-5", Formats: []Format{FormatOpenAIChat}}},
	} {
		if _, err := NormalizeModels(bad, known); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
