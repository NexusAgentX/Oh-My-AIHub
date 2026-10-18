package detection

import "testing"

func TestBasicCallabilityAllowsConsumedHiddenOutputButRejectsEmptyOrErrorReplies(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"id":"fixture","object":"chat.completion","created":1,"model":"native","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":64,"total_tokens":68}}`, true},
		{`{"id":"fixture","object":"chat.completion","created":1,"model":"native","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}`, false},
		{`{}`, false}, {`{"error":{"message":"denied"}}`, false},
	}
	for _, c := range cases {
		if got := ValidateBasicReply("openai-chat-completions", []byte(c.body)); got != c.want {
			t.Fatal("native callability result", got, c.want)
		}
	}
}
