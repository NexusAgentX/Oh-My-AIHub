package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// OpenAPI 契约测试：backend/api/openapi.yaml 是前后端契约的唯一来源（ADR-0021）。
// 这里断言路由表与规范逐项一致（路径、门禁、负责 Feature 与实现状态），
// 并在处理器测试中用规范里的 JSON Schema 校验每个真实响应。

const openAPIPath = "../../api/openapi.yaml"

type openAPISpec struct {
	document   map[string]any
	compiler   *jsonschema.Compiler
	schemaKeys []string
}

func loadOpenAPI(t *testing.T) *openAPISpec {
	t.Helper()
	raw, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("openapi.yaml 不是合法 YAML: %v", err)
	}
	if document["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v，契约须为 OpenAPI 3.1.0", document["openapi"])
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	if err := compiler.AddResource("openapi.json", resource); err != nil {
		t.Fatal(err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	keys := make([]string, 0, len(schemas))
	for name := range schemas {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return &openAPISpec{document: document, compiler: compiler, schemaKeys: keys}
}

func (spec *openAPISpec) schema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	schema, err := spec.compiler.Compile("openapi.json#/components/schemas/" + name)
	if err != nil {
		t.Fatalf("schema %s 无法编译: %v", name, err)
	}
	return schema
}

// assertSchema 将值经 JSON 往返后按规范 schema 校验，保证校验对象与线上字节一致。
func (spec *openAPISpec) assertSchema(t *testing.T, name string, value any) {
	t.Helper()
	encoded, ok := value.([]byte)
	if !ok {
		var err error
		if encoded, err = json.Marshal(value); err != nil {
			t.Fatal(err)
		}
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("%s: 响应不是合法 JSON: %v\n%s", name, err, encoded)
	}
	if err := spec.schema(t, name).Validate(instance); err != nil {
		t.Fatalf("响应不符合 schema %s: %v\n%s", name, err, encoded)
	}
}

type specOperation struct {
	method, path string
	body         map[string]any
}

func (spec *openAPISpec) operations() map[string]specOperation {
	result := map[string]specOperation{}
	for path, item := range spec.document["paths"].(map[string]any) {
		for method, body := range item.(map[string]any) {
			result[strings.ToUpper(method)+" "+path] = specOperation{method: strings.ToUpper(method), path: path, body: body.(map[string]any)}
		}
	}
	return result
}

// find 按“方法 + 具体路径”匹配规范中的路径模板。字面段优先于参数段。
func (spec *openAPISpec) find(method, path string) (specOperation, bool) {
	var best specOperation
	bestLiterals := -1
	segments := strings.Split(path, "/")
	for _, operation := range spec.operations() {
		if operation.method != method {
			continue
		}
		template := strings.Split(operation.path, "/")
		if len(template) != len(segments) {
			continue
		}
		literals, matched := 0, true
		for index, part := range template {
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
	return best, bestLiterals >= 0
}

// assertResponse 校验真实响应：状态码必须在规范中登记，JSON 响应体必须符合对应 schema。
func (spec *openAPISpec) assertResponse(t *testing.T, method, path string, recorder *httptest.ResponseRecorder) {
	t.Helper()
	operation, ok := spec.find(method, strings.SplitN(path, "?", 2)[0])
	if !ok {
		t.Fatalf("%s %s 未在规范中登记", method, path)
	}
	responses := operation.body["responses"].(map[string]any)
	declared, ok := responses[strconv.Itoa(recorder.Code)].(map[string]any)
	if !ok {
		t.Fatalf("%s %s 返回未登记的状态码 %d: %s", method, path, recorder.Code, recorder.Body.String())
	}
	if recorder.Code == http.StatusNoContent {
		return
	}
	if reference, isRef := declared["$ref"].(string); isRef {
		if !strings.HasPrefix(reference, "#/components/responses/") {
			t.Fatalf("unexpected response ref %s", reference)
		}
		spec.assertSchema(t, "ErrorResponse", recorder.Body.Bytes())
		return
	}
	schemaRef := declared["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"].(string)
	spec.assertSchema(t, strings.TrimPrefix(schemaRef, "#/components/schemas/"), recorder.Body.Bytes())
}

// routeKey 把 ServeMux 模式转换为规范使用的“METHOD /path/{param}”形式。
func routeKey(pattern string) string {
	method, path, _ := strings.Cut(pattern, " ")
	return method + " " + strings.ReplaceAll(path, "...}", "}")
}

func TestOpenAPIPathsMatchRouteTable(t *testing.T) {
	spec := loadOpenAPI(t)
	_, routes := buildHandler(Dependencies{})
	operations := spec.operations()

	seen := map[string]bool{}
	for _, entry := range routes {
		key := routeKey(entry.pattern)
		if seen[key] {
			t.Errorf("路由表重复：%s", key)
		}
		seen[key] = true
		operation, ok := operations[key]
		if !ok {
			t.Errorf("路由 %q 未在 openapi.yaml 中登记", entry.pattern)
			continue
		}
		if got := operation.body["x-access"]; got != string(entry.access) {
			t.Errorf("%s: x-access = %v，路由门禁为 %s", key, got, entry.access)
		}
		if got := operation.body["x-feature"]; got != entry.feature {
			t.Errorf("%s: x-feature = %v，路由登记为 Feature %s", key, got, entry.feature)
		}
		responses, _ := operation.body["responses"].(map[string]any)
		if !entry.implemented && entry.access != accessGatewayKey && responses["501"] == nil {
			t.Errorf("%s: 未实现的路由须登记 501", key)
		}
	}
	for key := range operations {
		if !seen[key] {
			t.Errorf("openapi.yaml 登记了不存在的路由：%s", key)
		}
	}
}

func TestOpenAPIOperationsAreWellFormed(t *testing.T) {
	spec := loadOpenAPI(t)
	operationIDs := map[string]string{}
	for key, operation := range spec.operations() {
		id, _ := operation.body["operationId"].(string)
		if id == "" {
			t.Errorf("%s 缺少 operationId", key)
		} else if previous, dup := operationIDs[id]; dup {
			t.Errorf("operationId %q 重复：%s 与 %s", id, previous, key)
		}
		operationIDs[id] = key

		access, _ := operation.body["x-access"].(string)
		security, _ := operation.body["security"].([]any)
		switch access {
		case "public":
			if len(security) != 0 {
				t.Errorf("%s: public 路由不应声明 security", key)
			}
		case "session", "ready", "admin":
			if len(security) != 1 || security[0].(map[string]any)["sessionCookie"] == nil {
				t.Errorf("%s: %s 路由须声明 sessionCookie", key, access)
			}
		case "gateway_key":
			if len(security) == 0 || !strings.HasPrefix(operation.path, "/v1") {
				t.Errorf("%s: 外部 API 入口须位于 /v1、/v1beta 下并声明 Key 认证", key)
			}
			if _, hasBody := operation.body["requestBody"]; hasBody != (operation.method == "POST") {
				t.Errorf("%s: 外部 API 入口只有 POST 登记原生请求体", key)
			}
		default:
			t.Errorf("%s: 未知 x-access %q", key, access)
		}
		if access != "gateway_key" && !strings.HasPrefix(operation.path, "/api/") {
			t.Errorf("%s: 非外部入口必须位于 /api/ 下", key)
		}
		switch operation.body["x-feature"] {
		case "A", "B", "C", "G":
		default:
			t.Errorf("%s: x-feature 须为 A/B/C/G", key)
		}
		responses := operation.body["responses"].(map[string]any)
		if access != "gateway_key" && operation.method != "GET" && responses["403"] == nil {
			t.Errorf("%s: 写操作须登记同源校验的 403", key)
		}
		if access == "admin" || access == "ready" || access == "session" {
			if responses["401"] == nil || responses["403"] == nil {
				t.Errorf("%s: 须登记 401/403", key)
			}
		}
	}
}

func TestOpenAPIPathParametersMatchPatterns(t *testing.T) {
	spec := loadOpenAPI(t)
	components := spec.document["components"].(map[string]any)["parameters"].(map[string]any)
	for key, operation := range spec.operations() {
		declared := map[string]bool{}
		parameters, _ := operation.body["parameters"].([]any)
		for _, raw := range parameters {
			parameter := raw.(map[string]any)
			if reference, ok := parameter["$ref"].(string); ok {
				name := strings.TrimPrefix(reference, "#/components/parameters/")
				resolved, exists := components[name]
				if !exists {
					t.Errorf("%s: 引用了不存在的参数 %s", key, name)
					continue
				}
				parameter = resolved.(map[string]any)
			}
			if parameter["in"] == "path" {
				declared["{"+parameter["name"].(string)+"}"] = true
			}
		}
		for _, segment := range strings.Split(operation.path, "/") {
			if strings.HasPrefix(segment, "{") && !declared[segment] {
				t.Errorf("%s: 路径参数 %s 未声明", key, segment)
			}
		}
	}
}

func TestOpenAPISchemasCompile(t *testing.T) {
	spec := loadOpenAPI(t)
	for _, name := range spec.schemaKeys {
		spec.schema(t, name)
	}
}

// TestPlannedRoutesKeepTheirGateAndAnswerNotImplemented 对每条尚未实现的路由：
// 未登录时被门禁拒绝，管理员登录后返回契约登记的 501。
func TestPlannedRoutesKeepTheirGateAndAnswerNotImplemented(t *testing.T) {
	spec := loadOpenAPI(t)
	store := newFakeStore()
	handler := newFakeHandler(store)
	admin := bootstrap(t, spec, handler)
	_, routes := buildHandler(Dependencies{})
	for _, entry := range routes {
		if entry.implemented {
			continue
		}
		method, path, _ := strings.Cut(entry.pattern, " ")
		concrete := strings.NewReplacer(
			"{modelID}", "gpt-5", "{model}", "gemini-2.5-flash:generateContent",
			"{keyID}", "00000000-0000-4000-8000-0000000000aa", "{channelID}", "00000000-0000-4000-8000-0000000000bb",
			"{callID}", "00000000-0000-4000-8000-0000000000cc", "{orderID}", "00000000-0000-4000-8000-0000000000dd",
			"{tradeID}", "00000000-0000-4000-8000-0000000000ee", "{transactionID}", "00000000-0000-4000-8000-0000000000ff",
		).Replace(path)
		if entry.access != accessGatewayKey {
			anonymous := admin.call(t, method, concrete, nil, withoutCookie)
			if anonymous.Code != http.StatusUnauthorized {
				t.Errorf("%s %s 未登录 = %d", method, concrete, anonymous.Code)
			}
		}
		recorder := admin.call(t, method, concrete, nil)
		if recorder.Code != http.StatusNotImplemented || !strings.Contains(recorder.Body.String(), `"error":"not_implemented"`) {
			t.Errorf("%s %s = %d %s", method, concrete, recorder.Code, recorder.Body.String())
			continue
		}
		if entry.access != accessGatewayKey {
			spec.assertResponse(t, method, concrete, recorder)
		}
	}
}
