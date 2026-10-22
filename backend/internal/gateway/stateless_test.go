package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// The gateway keeps no server-side state, so a request that asks for it is
// either rewritten (a store flag the upstream would honour) or refused. These
// tests pin both sides of that boundary at the scan level; the wire behaviour
// is covered from the API side too.

var storeBodies = []struct {
	name, in, want string
}{
	{"only member", `{"store":true}`, `{}`},
	{"last member", `{"model":"m","input":"x","store":true}`, `{"model":"m","input":"x"}`},
	{"first member", `{"store":true,"model":"m"}`, `{"model":"m"}`},
	{"middle member", `{"a":1,"store":true,"b":2}`, `{"a":1,"b":2}`},
	{"spaces around", `{ "store" : true }`, `{ }`},
	{"capitalised key", `{"Store": true, "a": 1}`, `{"a": 1}`},
	{"string true", `{"store":"true","a":1}`, `{"a":1}`},
	{"string TRUE", `{"store":"TRUE","a":1}`, `{"a":1}`},
	{"number one", `{"store":1,"a":1}`, `{"a":1}`},
	{"duplicate with a true", `{"store":false,"store":true,"a":1}`, `{"a":1}`},
	{"duplicate both true", `{"store":true,"store":true}`, `{}`},
	{"adjacent duplicates", `{"a":1,"store":true,"Store":true,"b":2}`, `{"a":1,"b":2}`},
	{"only true stores", `{"a":false,"nested":{"store":true}}`, `{"a":false,"nested":{"store":true}}`},
	{"escaped key", `{"st\u006fre":true,"a":1}`, `{"a":1}`},
	{"false stays", `{"model":"m","store":false}`, `{"model":"m","store":false}`},
	{"null stays", `{"store":null}`, `{"store":null}`},
	{"absent stays", `{"model":"m","input":"x"}`, `{"model":"m","input":"x"}`},
}

func TestStatelessBodyRemovesStoreThatAsksForStorage(t *testing.T) {
	for _, tc := range storeBodies {
		body := []byte(tc.in)
		_, scan, err := ScanRequest(body)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := StatelessBody(body, scan)
		if string(got) != tc.want {
			t.Fatalf("%s: got %s, want %s", tc.name, got, tc.want)
		}
		if !json.Valid(got) {
			t.Fatalf("%s: result is not JSON: %s", tc.name, got)
		}
	}
}

func TestStatelessBodyKeepsTheBodyWhenThereIsNothingToRemove(t *testing.T) {
	for _, text := range []string{`{"model":"m","store":false}`, `{"model":"m","input":"x"}`, `{"Store":null}`} {
		body := []byte(text)
		_, scan, err := ScanRequest(body)
		if err != nil {
			t.Fatal(err)
		}
		got := StatelessBody(body, scan)
		if len(got) != len(body) || &got[0] != &body[0] {
			t.Fatalf("%s: copied %q", text, got)
		}
	}
}

func TestUpstreamBodySplicesStoreInTheSameCopy(t *testing.T) {
	body := []byte(`{"model":"m","stream":true,"store":true}`)
	_, scan, err := ScanRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	got, injected := UpstreamBody(body, scan, "upstream", false, true)
	if injected || string(got) != `{"model":"upstream","stream":true}` {
		t.Fatalf("got %q (injected %v)", got, injected)
	}
	kept, _ := UpstreamBody(body, scan, "upstream", false, false)
	if string(kept) != `{"model":"upstream","stream":true,"store":true}` {
		t.Fatalf("an entrance that keeps the flag lost it: %q", kept)
	}
}

func TestObjectScanFindsStatefulFields(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"model":"m","previous_response_id":"resp_1"}`, "previous_response_id"},
		{`{"conversation":"conv_1"}`, "conversation"},
		{`{"conversation":{"id":"conv_1"}}`, "conversation"},
		{`{"Previous_Response_ID":"resp_1"}`, "previous_response_id"},
		{`{"previous_response_id":null,"conversation":"conv_1"}`, "conversation"},
		{`{"previous_response_id":"","conversation":null}`, ""},
		{`{"model":"m","input":[{"previous_response_id":"nested"}]}`, ""},
	}
	for _, tc := range cases {
		body := []byte(tc.in)
		_, scan, err := ScanRequest(body)
		if err != nil {
			t.Fatal(err)
		}
		if got := scan.statefulField(body); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStatelessPathIsTheTwoJSONEntrances(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/responses": true, "/v1/chat/completions": true,
		"/v1/responses/resp_1": false, "/v1/messages": false, "/v1beta/models/x": false,
		"/backend-api/codex/responses": false, "/v1/images/generations": false,
	} {
		if got := statelessPath(path); got != want {
			t.Fatalf("%s: got %v, want %v", path, got, want)
		}
	}
}

func FuzzStatelessBody(f *testing.F) {
	for _, tc := range storeBodies {
		f.Add([]byte(tc.in))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		_, scan, err := ScanRequest(body)
		if err != nil {
			return
		}
		got := StatelessBody(body, scan)
		if json.Valid(body) && !json.Valid(got) {
			t.Fatalf("%q: not JSON: %q", body, got)
		}
		// The result asks for no server-side storage and says otherwise the
		// same thing.
		_, after, err := ScanRequest(got)
		if err != nil {
			t.Fatalf("%q: unreadable result %q: %v", body, got, err)
		}
		for _, member := range after.entries {
			if strings.EqualFold(member.key, "store") && storesTruthy(got[member.start:member.end]) {
				t.Fatalf("%q: still asks for storage: %q", body, got)
			}
		}
		if !json.Valid(body) {
			return
		}
		var before, result map[string]json.RawMessage
		if json.Unmarshal(body, &before) != nil || json.Unmarshal(got, &result) != nil {
			t.Fatalf("%q: got %q", body, got)
		}
		if string(jsonText(t, withoutStore(before))) != string(jsonText(t, withoutStore(result))) {
			t.Fatalf("%q: members changed: %q", body, got)
		}
	})
}

func withoutStore(m map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m))
	for key, value := range m {
		if !strings.EqualFold(key, "store") {
			out[key] = value
		}
	}
	return out
}

func jsonText(t *testing.T, m map[string]json.RawMessage) []byte {
	t.Helper()
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
