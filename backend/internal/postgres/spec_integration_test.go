package postgres_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// Every JSON response the integration tests receive from /api is validated
// against backend/api/openapi.yaml, so the real queries (not just fakes) are
// held to the contract.

type openAPI struct {
	document map[string]any
	compiler *jsonschema.Compiler
}

var (
	specOnce sync.Once
	spec     *openAPI
	specErr  error
)

func loadSpec() (*openAPI, error) {
	specOnce.Do(func() {
		raw, err := os.ReadFile("../../api/openapi.yaml")
		if err != nil {
			specErr = err
			return
		}
		var document map[string]any
		if specErr = yaml.Unmarshal(raw, &document); specErr != nil {
			return
		}
		encoded, _ := json.Marshal(document)
		resource, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
		if err != nil {
			specErr = err
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		compiler.AssertFormat()
		if specErr = compiler.AddResource("openapi.json", resource); specErr != nil {
			return
		}
		spec = &openAPI{document: document, compiler: compiler}
	})
	return spec, specErr
}

func (s *openAPI) find(method, path string) map[string]any {
	var best map[string]any
	bestLiterals := -1
	segments := strings.Split(path, "/")
	for template, item := range s.document["paths"].(map[string]any) {
		operation, ok := item.(map[string]any)[strings.ToLower(method)].(map[string]any)
		if !ok {
			continue
		}
		parts := strings.Split(template, "/")
		if len(parts) != len(segments) {
			continue
		}
		literals, matched := 0, true
		for index, part := range parts {
			if strings.HasPrefix(part, "{") {
				continue
			}
			if part != segments[index] {
				matched = false
				break
			}
			literals++
		}
		if matched && literals > bestLiterals {
			best, bestLiterals = operation, literals
		}
	}
	return best
}

// assertContract fails the test when a response differs from the contract.
func assertContract(t *testing.T, method, target string, recorder *httptest.ResponseRecorder) {
	t.Helper()
	document, err := loadSpec()
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	path := strings.SplitN(target, "?", 2)[0]
	operation := document.find(method, path)
	if operation == nil {
		t.Fatalf("%s %s is not in the contract", method, path)
	}
	declared, ok := operation["responses"].(map[string]any)[strconv.Itoa(recorder.Code)].(map[string]any)
	if !ok {
		t.Fatalf("%s %s answered undeclared status %d: %s", method, path, recorder.Code, recorder.Body.String())
	}
	if recorder.Code == 204 || strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/") {
		return
	}
	var schemaName string
	if reference, isRef := declared["$ref"].(string); isRef && strings.HasPrefix(reference, "#/components/responses/") {
		schemaName = "ErrorResponse"
	} else {
		content, _ := declared["content"].(map[string]any)["application/json"].(map[string]any)
		if content == nil {
			t.Fatalf("%s %s: status %d has no JSON content in the contract", method, path, recorder.Code)
		}
		schemaName = strings.TrimPrefix(content["schema"].(map[string]any)["$ref"].(string), "#/components/schemas/")
	}
	schema, err := document.compiler.Compile("openapi.json#/components/schemas/" + schemaName)
	if err != nil {
		t.Fatalf("compile %s: %v", schemaName, err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(recorder.Body.Bytes()))
	if err != nil {
		t.Fatalf("%s %s: not JSON: %v", method, path, err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatalf("%s %s violates schema %s: %v\n%s", method, target, schemaName, err, recorder.Body.String())
	}
}
