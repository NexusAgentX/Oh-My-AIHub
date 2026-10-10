package api

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

// 契约覆盖门禁（Issue #262）：openapi.yaml 中的每个 operation 都必须至少有一个
// 成功（2xx）响应在本包测试里经过 assertResponse 的 schema 校验，否则整包测试失败。
// 没被测试覆盖到的接口与规范之间的偏差不会被其他契约测试发现，门禁补上这一缺口。
//
// 只运行部分测试（-run、-skip、-list）时不触发门禁。

// coverageExceptions 是门禁的显式例外清单：operation（“METHOD /path”，与规范一致）到不在本包验证的原因。
// 新增例外必须写明原因；例外被覆盖或不再存在于规范时门禁同样失败，保证清单只会变短。
var coverageExceptions = map[string]string{
	"POST /v1/chat/completions":   gatewayPassthrough,
	"POST /v1/responses":          gatewayPassthrough,
	"POST /v1/messages":           gatewayPassthrough,
	"POST /v1beta/models/{model}": gatewayPassthrough,
	"GET /v1/models":              gatewayPassthrough,
	"GET /v1beta/models":          gatewayPassthrough,
}

// gatewayPassthrough 说明外部模型 API 入口为何不在本包验证：规范只登记 default 的原生透传响应，
// 没有可校验的状态码与 schema；其行为由 internal/postgres 的网关集成测试用真实上游替身覆盖。
const gatewayPassthrough = "外部模型 API 原生透传，规范只登记 default 响应而无 schema；由 internal/postgres 网关集成测试验证"

// validatedSuccess 登记本次测试运行中经过校验的成功响应，键为“METHOD /path/{param}”。
var validatedSuccess = &successRegistry{validated: map[string]bool{}}

type successRegistry struct {
	mu        sync.Mutex
	validated map[string]bool
}

func (r *successRegistry) record(operation string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.validated[operation] = true
}

func (r *successRegistry) has(operation string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.validated[operation]
}

// uncoveredProblems 返回门禁不通过的原因；为空表示通过。
func uncoveredProblems(operations []string, validated func(string) bool, exceptions map[string]string) []string {
	known := make(map[string]bool, len(operations))
	var problems []string
	for _, operation := range operations {
		known[operation] = true
		_, excepted := exceptions[operation]
		switch ok := validated(operation); {
		case !ok && !excepted:
			problems = append(problems, "未覆盖：没有经过 schema 校验的 2xx 响应："+operation)
		case ok && excepted:
			problems = append(problems, "例外已失效：该接口已被测试覆盖，请从 coverageExceptions 删除："+operation)
		}
	}
	for operation, reason := range exceptions {
		if !known[operation] {
			problems = append(problems, "例外已失效：规范中不存在该接口，请从 coverageExceptions 删除："+operation)
		}
		if strings.TrimSpace(reason) == "" {
			problems = append(problems, "例外缺少原因："+operation)
		}
	}
	sort.Strings(problems)
	return problems
}

// isFullRun 判断本次是否为整包测试运行；任何用例筛选都会让覆盖结果失去意义。
func isFullRun() bool {
	for _, name := range []string{"test.run", "test.skip", "test.list", "test.fuzz"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return false
		}
	}
	return true
}

func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 && isFullRun() {
		document, err := readOpenAPIDocument()
		if err != nil {
			fmt.Fprintln(os.Stderr, "契约覆盖门禁：", err)
			os.Exit(1)
		}
		operations := make([]string, 0, 128)
		for key := range (&openAPISpec{document: document}).operations() {
			operations = append(operations, key)
		}
		if problems := uncoveredProblems(operations, validatedSuccess.has, coverageExceptions); len(problems) > 0 {
			fmt.Fprintf(os.Stderr, "\n契约覆盖门禁失败（%d 项）：openapi.yaml 的每个接口都需要至少一个经 assertResponse 校验的 2xx 响应。\n", len(problems))
			for _, problem := range problems {
				fmt.Fprintln(os.Stderr, "  - "+problem)
			}
			fmt.Fprintln(os.Stderr, "补充测试，或在 coverage_test.go 的 coverageExceptions 中登记带原因的例外。")
			code = 1
		}
	}
	os.Exit(code)
}

func TestUncoveredProblems(t *testing.T) {
	operations := []string{"GET /a", "GET /b", "POST /c"}
	validated := func(operation string) bool { return operation == "GET /a" || operation == "POST /c" }
	if problems := uncoveredProblems(operations, validated, map[string]string{"GET /b": "原因"}); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	problems := uncoveredProblems(operations, validated, map[string]string{"POST /c": "已覆盖", "GET /gone": "已删除", "GET /b": " "})
	if len(problems) != 3 {
		t.Fatalf("problems = %v", problems)
	}
	if got := uncoveredProblems(operations, validated, nil); len(got) != 1 || !strings.Contains(got[0], "GET /b") {
		t.Fatalf("uncovered = %v", got)
	}
}
