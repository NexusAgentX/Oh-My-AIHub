package gateway

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestScanRequestReadsOnlyTopLevelKeys(t *testing.T) {
	body := []byte(` {"messages":[{"model":"inner","content":"{\"model\":\"x\"}"}],"mod\u0065l" : "gpt-5" ,"stream":true,"previous_response_id":"resp_1","extra":{"model":"nested"}} `)
	facts, _, err := ScanRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	want := RequestFacts{Model: "gpt-5", HasModel: true, Stream: true, PreviousResponseID: "resp_1"}
	if facts != want {
		t.Fatalf("facts = %#v, want %#v", facts, want)
	}
}

func TestScanRequestHandlesMissingAndNonStringModel(t *testing.T) {
	for _, body := range []string{`{}`, `{"model":null}`, `{"model":5}`, `{"model":["a"]}`} {
		facts, _, err := ScanRequest([]byte(body))
		if err != nil || facts.HasModel {
			t.Fatalf("%s: facts = %#v, err = %v", body, facts, err)
		}
	}
	for _, body := range []string{``, `[]`, `{"model":"a"`, `{"model" "a"}`, `{"a":1,}`, `not json`, `{"a":"unterminated}`} {
		if _, _, err := ScanRequest([]byte(body)); err == nil {
			t.Fatalf("%q was accepted", body)
		}
	}
}

func TestReplaceModelKeepsEveryOtherByte(t *testing.T) {
	body := []byte("{\n  \"messages\": [{\"role\":\"user\",\"content\":\"hi \\\"model\\\": 1\"}],\n  \"model\"  :  \"alias\",\n  \"temperature\": 1.50,\n  \"unicode\": \"\\u00e9\"\n}\n")
	_, scan, err := ScanRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	got := ReplaceModel(body, scan, "upstream/real-name")
	want := bytes.Replace(body, []byte(`"alias"`), []byte(`"upstream/real-name"`), 1)
	if !bytes.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestReplaceModelEscapesAndReplacesDuplicates(t *testing.T) {
	body := []byte(`{"model":"a","x":1,"model":"b"}`)
	_, scan, _ := ScanRequest(body)
	got := ReplaceModel(body, scan, `we"ird<>&`)
	if string(got) != `{"model":"we\"ird<>&","x":1,"model":"we\"ird<>&"}` {
		t.Fatalf("got %s", got)
	}
}

func TestEnsureIncludeUsage(t *testing.T) {
	cases := []struct {
		in, out string
		changed bool
	}{
		{`{"model":"m","stream":true}`, `{"model":"m","stream":true,"stream_options":{"include_usage":true}}`, true},
		{`{"model":"m","stream":true }`, `{"model":"m","stream":true,"stream_options":{"include_usage":true} }`, true},
		{`{"stream_options":{"include_usage":true},"a":1}`, `{"stream_options":{"include_usage":true},"a":1}`, false},
		{`{"stream_options":{"include_usage": true },"a":1}`, `{"stream_options":{"include_usage": true },"a":1}`, false},
		{`{"stream_options":{"include_usage":false},"a":1}`, `{"stream_options":{"include_usage":true},"a":1}`, true},
		{`{"stream_options":{"include_usage":true,"include_usage":false},"a":1}`, `{"stream_options":{"include_usage":true,"include_usage":true},"a":1}`, true},
		{`{"stream_options":{"include_usage":false,"include_usage":true},"a":1}`, `{"stream_options":{"include_usage":false,"include_usage":true},"a":1}`, false},
		{`{"stream_options":{},"a":1}`, `{"stream_options":{"include_usage":true},"a":1}`, true},
		{`{"stream_options":{"other":1},"a":1}`, `{"stream_options":{"include_usage":true,"other":1},"a":1}`, true},
		{`{"stream_options":null}`, `{"stream_options":{"include_usage":true}}`, true},
		{`{}`, `{"stream_options":{"include_usage":true}}`, true},
	}
	for _, tc := range cases {
		_, scan, err := ScanRequest([]byte(tc.in))
		if err != nil {
			t.Fatal(err)
		}
		got, changed := EnsureIncludeUsage([]byte(tc.in), scan)
		if string(got) != tc.out || changed != tc.changed {
			t.Fatalf("in  %s\ngot %s (changed %v)\nwant %s (changed %v)", tc.in, got, changed, tc.out, tc.changed)
		}
		if !json.Valid(got) {
			t.Fatalf("result is not valid JSON: %s", got)
		}
	}
}

func FuzzScanAndSplice(f *testing.F) {
	for _, seed := range []string{
		`{"model":"a","stream":true}`, `{"a":{"model":"x"},"model":"b"}`, `{"model":"a\"b","k":[1,2,{"model":3}]}`,
		` { "mod\u0065l" : "q" , "stream_options" : { "include_usage" : false } } `, `{}`, `{"model":"é","x":"\u00e9"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		facts, scan, err := ScanRequest(body)
		var generic map[string]json.RawMessage
		valid := json.Unmarshal(body, &generic) == nil
		if !valid {
			return // the scanner may be more lenient than encoding/json, but must not panic
		}
		if err != nil {
			t.Fatalf("valid JSON object rejected: %q: %v", body, err)
		}
		if raw, ok := generic["model"]; ok {
			var model string
			if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &model) == nil && (!facts.HasModel || facts.Model != model) {
				t.Fatalf("model = %q/%v, want %q in %q", facts.Model, facts.HasModel, model, body)
			}
		}
		replaced := ReplaceModel(body, scan, "REPLACED")
		var after map[string]json.RawMessage
		if err := json.Unmarshal(replaced, &after); err != nil {
			t.Fatalf("replacement broke JSON: %q -> %q", body, replaced)
		}
		for key, value := range generic {
			if key == "model" {
				continue
			}
			if !reflect.DeepEqual(after[key], value) {
				t.Fatalf("key %q changed: %q -> %q", key, body, replaced)
			}
		}
		if _, ok := generic["model"]; ok && string(after["model"]) != `"REPLACED"` {
			t.Fatalf("model not replaced: %q -> %q", body, replaced)
		}
		injected, changed := EnsureIncludeUsage(body, scan)
		if changed == bytes.Equal(injected, body) {
			t.Fatalf("changed = %v but the body %q became %q", changed, body, injected)
		}
		var withUsage map[string]json.RawMessage
		if err := json.Unmarshal(injected, &withUsage); err != nil {
			t.Fatalf("injection broke JSON: %q -> %q", body, injected)
		}
		var options map[string]json.RawMessage
		if err := json.Unmarshal(withUsage["stream_options"], &options); err != nil || string(options["include_usage"]) != "true" {
			t.Fatalf("include_usage missing: %q -> %q", body, injected)
		}
	})
}

// ---- the pricing members ----

// decodedPricing is the reference interpretation of the request service tier.
func decodedPricing(body []byte) string {
	var request struct {
		ServiceTier string `json:"service_tier"`
	}
	_ = json.Unmarshal(body, &request)
	return request.ServiceTier
}

func checkPricing(t testing.TB, body []byte) {
	t.Helper()
	facts, _, _ := ScanRequest(body)
	if want := decodedPricing(body); facts.ServiceTier != want {
		t.Fatalf("%q: service_tier=%q want=%q", body, facts.ServiceTier, want)
	}
}

var pricingBodies = []string{
	`{"model":"m","enable_thinking":false}`,
	`{"model":"m","enable_thinking":true,"service_tier":"priority"}`,
	`{"model":"m","enable_thinking":null,"service_tier":null}`,
	`{"model":"m"}`, `{}`, `{"enable_thinking":"false"}`, `{"enable_thinking":0}`, `{"enable_thinking":{"a":1}}`, `{"enable_thinking":[true]}`,
	`{"enable_thinking":true,"enable_thinking":"x"}`, `{"enable_thinking":true,"enable_thinking":null}`, `{"enable_thinking":null,"enable_thinking":{}}`,
	`{"enable_thinking":false,"enable_thinking":true}`, `{"enable_thinking":true,"enable_thinking":false}`,
	`{"Enable_Thinking":false}`, `{"ENABLE_THINKING":true}`, `{"enable\u005fthinking":false}`, `{"enable_thin\u212Aing":false}`,
	`{"service_tier":"a","service_tier":5}`, `{"service_tier":"a","service_tier":null}`, `{"service_tier":"a","SERVICE_TIER":"b"}`,
	`{"service_tier":"\u0061\ud83d\ude00\ud800"}`, "{\"service_tier\":\"caf\u00e9 \xff\"}", `{"service_tier":"line\nbreak \"quoted\""}`,
	`{"service_tier":true}`, `{"service_tier":["a"]}`, `{"service_tier":{"a":"b"}}`, `{"s\u017fervice_tier":"x"}`,
	`{"nested":{"enable_thinking":false,"service_tier":"x"},"model":"m"}`,
	`{"messages":[{"enable_thinking":false}],"service_tier":"flex"}`,
	` { "enable_thinking" : false , "service_tier" : "x" } `, "{\"enable_thinking\":\n\tfalse}",
	// not a single valid JSON object: nothing is read, whatever the scan makes of it
	`{"enable_thinking":false} trailing`, `{"enable_thinking":false}{"service_tier":"x"}`, `{"enable_thinking":tru}`, `{"enable_thinking":false,"a":01}`,
	`{"enable_thinking":false,"a":"\x"}`, `{"enable_thinking":false`, `{"service_tier":"x",}`, `[{"enable_thinking":false}]`, `null`, `"x"`, ``,
	`{"service_tier":"x","a":1.}`, `{"service_tier":"x","a":[1,]}`, `{"service_tier":"x","a":NaN}`,
	"{\"service_tier\":\"x\",\"a\":\"raw\nnewline\"}",
}

func TestScanRequestReadsThePricingMembersLikeEncodingJSON(t *testing.T) {
	for _, body := range pricingBodies {
		checkPricing(t, []byte(body))
	}
	facts, _, err := ScanRequest([]byte(`{"model":"m","enable_thinking":false,"service_tier":"flex"}`))
	if err != nil || facts.ServiceTier != "flex" {
		t.Fatalf("facts = %+v, err = %v", facts, err)
	}
}

// A body this size must not cost a decode of its own: the members are read
// from the scan, and without any of them the body is not validated either.
func TestScanRequestReadsPricingWithoutDecodingTheBody(t *testing.T) {
	filler := strings.Repeat(`{"role":"user","content":"`+strings.Repeat("x", 1000)+`"},`, 2000)
	body := []byte(`{"model":"m","messages":[` + filler + `{}],"enable_thinking":false,"service_tier":"flex"}`)
	if !json.Valid(body) {
		t.Fatal("test body is not valid JSON")
	}
	checkPricing(t, body)
	allocated := testing.AllocsPerRun(5, func() { _, _, _ = ScanRequest(body) })
	if allocated > 40 {
		t.Fatalf("scanning a %d byte body made %.0f allocations", len(body), allocated)
	}
}

func FuzzScanRequestPricing(f *testing.F) {
	for _, body := range pricingBodies {
		f.Add([]byte(body))
	}
	f.Fuzz(func(t *testing.T, body []byte) { checkPricing(t, body) })
}

// ---- one scan for the model and include_usage ----

// legacyUpstreamBody is how the gateway used to build the body of an OpenAI
// Chat stream: replace the model, scan the result again, then ensure usage.
func legacyUpstreamBody(body []byte, scan objectScan, model string, ensureUsage bool) ([]byte, bool) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(model)
	encoded := bytes.TrimRight(buffer.Bytes(), "\n")
	var splices []splice
	for _, member := range scan.all("model") {
		splices = append(splices, splice{start: member.start, end: member.end, value: encoded})
	}
	upstream := applySplices(body, splices)
	if !ensureUsage {
		return upstream, false
	}
	_, rescanned, err := ScanRequest(upstream)
	if err != nil {
		return upstream, false
	}
	return EnsureIncludeUsage(upstream, rescanned)
}

var upstreamBodyCases = []string{
	`{"model":"m","stream":true}`, `{"model":"m","stream":true,"stream_options":{"include_usage":true}}`,
	`{"model":"m","stream":true,"stream_options":{"include_usage":false}}`, `{"model":"m","stream_options":{"other":1},"stream":true}`,
	`{"stream_options":{"include_usage":false,"include_usage":false},"model":"m"}`, `{"stream_options":null,"model":"m"}`, `{"stream_options":5,"model":"m"}`,
	`{"model":"m"}`, `{"model":"m","model":"n","stream":true}`, `{"stream":true,"model":"m"}`, `{"model":"m","stream_options":{}}`,
	`{"mod\u0065l":"m","stream":true}`, `{"model":"up\"stream","x":1}`, `{"model":"é","x":1}`, `{"model":5}`, `{}`, ` {"stream" : true , "model" : "m" } `,
	`{"model":"upstream","stream":true}`, `{"model":"upstream","stream":true,"stream_options":{"include_usage":true}}`,
	`{"messages":[{"model":"x"}],"stream_options":{"include_usage":false},"model":"upstream"}`,
}

func TestUpstreamBodyIsReplaceModelThenEnsureIncludeUsage(t *testing.T) {
	for _, text := range upstreamBodyCases {
		body := []byte(text)
		_, scan, err := ScanRequest(body)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		for _, model := range []string{"upstream", "we\"ird<>&\n", "upstream/é", "", "m"} {
			for _, ensure := range []bool{false, true} {
				got, gotInjected := UpstreamBody(body, scan, model, ensure, false)
				want, wantInjected := legacyUpstreamBody(body, scan, model, ensure)
				if !bytes.Equal(got, want) || gotInjected != wantInjected {
					t.Fatalf("%s, model %q, ensure %v:\ngot  %q (%v)\nwant %q (%v)", text, model, ensure, got, gotInjected, want, wantInjected)
				}
			}
		}
	}
}

func TestUpstreamBodyIsTheBodyItselfWhenNothingChanges(t *testing.T) {
	body := []byte(`{"model":"upstream","stream":true,"stream_options":{"include_usage":true}}`)
	_, scan, _ := ScanRequest(body)
	got, injected := UpstreamBody(body, scan, "upstream", true, false)
	if injected || len(got) != len(body) || &got[0] != &body[0] {
		t.Fatalf("a body that needs no change was copied: %q (injected %v)", got, injected)
	}
	changed, injected := UpstreamBody(body, scan, "other", true, false)
	if injected || string(changed) != `{"model":"other","stream":true,"stream_options":{"include_usage":true}}` {
		t.Fatalf("got %q (injected %v)", changed, injected)
	}
}

func FuzzUpstreamBody(f *testing.F) {
	for _, text := range upstreamBodyCases {
		f.Add([]byte(text), "upstream", true)
	}
	f.Fuzz(func(t *testing.T, body []byte, model string, ensure bool) {
		_, scan, err := ScanRequest(body)
		if err != nil {
			return
		}
		got, gotInjected := UpstreamBody(body, scan, model, ensure, false)
		want, wantInjected := legacyUpstreamBody(body, scan, model, ensure)
		if !bytes.Equal(got, want) || gotInjected != wantInjected {
			t.Fatalf("%q, model %q, ensure %v:\ngot  %q (%v)\nwant %q (%v)", body, model, ensure, got, gotInjected, want, wantInjected)
		}
	})
}

func TestEncodeStringMatchesEncodingJSON(t *testing.T) {
	for _, value := range []string{"", "gpt-4o", "a b", `q"uote`, `back\slash`, "tab\t", "nl\n", "é", "\u2028\u2029", "<>&", "del\x7f", "\xff", "ctl\x01", "日本語"} {
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(value)
		if want := bytes.TrimRight(buffer.Bytes(), "\n"); !bytes.Equal(encodeString(value), want) {
			t.Fatalf("%q: got %s, want %s", value, encodeString(value), want)
		}
	}
}

// ---- skipString ----

// legacySkipString is the byte-by-byte loop skipString replaced.
func legacySkipString(text []byte, i int) (int, error) {
	i++
	for i < len(text) {
		switch text[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1, nil
		default:
			i++
		}
	}
	return 0, errMalformedJSON
}

func checkSkipString(t testing.TB, text []byte) {
	t.Helper()
	for start := 0; start < len(text) && start < 8; start++ {
		got, gotErr := skipString(text, start)
		want, wantErr := legacySkipString(text, start)
		if got != want || (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("skipString(%q, %d) = %d, %v; the byte loop gave %d, %v", text, start, got, gotErr, want, wantErr)
		}
	}
}

func TestSkipStringMatchesTheByteLoop(t *testing.T) {
	for _, text := range []string{`""`, `"a"`, `"a\"b"`, `"a\\"`, `"a\\\"b"`, `"a\`, `"a\"`, `"`, `"\\`, `"\\\\"x"`, `"unterminated`, `"éé\n"tail`, `"x" "y"`} {
		checkSkipString(t, []byte(text))
	}
	// Every string of quotes, backslashes and letters up to a length.
	alphabet := []byte{'"', '\\', 'a', 'u'}
	for length := 1; length <= 9; length++ {
		text := make([]byte, length)
		for code := 0; code < 1<<(2*length); code++ {
			for i := range text {
				text[i] = alphabet[code>>(2*i)&3]
			}
			checkSkipString(t, text)
		}
	}
}

func FuzzSkipString(f *testing.F) {
	for _, seed := range []string{`"a\"b"`, `"a\\"`, `"\\\"`, `"abc`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, text []byte) { checkSkipString(t, text) })
}
