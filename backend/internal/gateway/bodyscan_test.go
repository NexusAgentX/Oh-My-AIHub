package gateway

import (
	"bytes"
	"encoding/json"
	"reflect"
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
	cases := []struct{ in, out string }{
		{`{"model":"m","stream":true}`, `{"model":"m","stream":true,"stream_options":{"include_usage":true}}`},
		{`{"model":"m","stream":true }`, `{"model":"m","stream":true,"stream_options":{"include_usage":true} }`},
		{`{"stream_options":{"include_usage":true},"a":1}`, `{"stream_options":{"include_usage":true},"a":1}`},
		{`{"stream_options":{"include_usage":false},"a":1}`, `{"stream_options":{"include_usage":true},"a":1}`},
		{`{"stream_options":{},"a":1}`, `{"stream_options":{"include_usage":true},"a":1}`},
		{`{"stream_options":{"other":1},"a":1}`, `{"stream_options":{"include_usage":true,"other":1},"a":1}`},
		{`{"stream_options":null}`, `{"stream_options":{"include_usage":true}}`},
		{`{}`, `{"stream_options":{"include_usage":true}}`},
	}
	for _, tc := range cases {
		_, scan, err := ScanRequest([]byte(tc.in))
		if err != nil {
			t.Fatal(err)
		}
		got := EnsureIncludeUsage([]byte(tc.in), scan)
		if string(got) != tc.out {
			t.Fatalf("in  %s\ngot %s\nwant %s", tc.in, got, tc.out)
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
		injected := EnsureIncludeUsage(body, scan)
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
