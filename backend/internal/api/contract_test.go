package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/dashboard"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/feerate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ops"
)

// OpenAPI 契约测试：backend/api/openapi.yaml 是前后端契约的唯一来源。
// 这里断言路由表与规范逐项一致，并用规范中的 JSON Schema 校验各响应构造器的真实输出。

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
		encoded, err = json.Marshal(value)
		if err != nil {
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

// routeKey 把 ServeMux 模式转换为规范使用的“METHOD /path/{param}”形式。
func routeKey(pattern string) string {
	method, path := http.MethodPost, pattern // 外部协议入口不限方法，规范只登记 POST
	if before, after, found := strings.Cut(pattern, " "); found {
		method, path = before, after
	}
	path = strings.ReplaceAll(path, "...}", "}")
	return method + " " + path
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
			t.Errorf("路由 %q 未在 openapi.yaml 中登记（期望 %s）", entry.pattern, key)
			continue
		}
		if got := operation.body["x-access"]; got != string(entry.access) {
			t.Errorf("%s: x-access = %v，路由门禁为 %s", key, got, entry.access)
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
			if len(security) != 1 {
				t.Errorf("%s: 外部协议入口须声明网关 Key 认证", key)
			}
			if _, hasBody := operation.body["requestBody"]; !hasBody || !strings.HasPrefix(operation.path, "/v1") {
				t.Errorf("%s: 外部协议入口只登记路径与认证", key)
			}
		default:
			t.Errorf("%s: 未知 x-access %q", key, access)
		}
		if access != "gateway_key" && !strings.HasPrefix(operation.path, "/api/") {
			t.Errorf("%s: 非协议入口必须位于 /api/ 下", key)
		}
		responses := operation.body["responses"].(map[string]any)
		if access != "gateway_key" && operation.method != "GET" && responses["403"] == nil {
			t.Errorf("%s: 写操作须登记同源校验的 403", key)
		}
		if access == "admin" || access == "ready" {
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
	// 每个操作引用的 schema 都必须存在。
	for key, operation := range spec.operations() {
		for _, name := range referencedSchemas(operation.body) {
			if _, err := spec.compiler.Compile("openapi.json#/components/schemas/" + name); err != nil {
				t.Errorf("%s 引用的 schema %s 无法编译: %v", key, name, err)
			}
		}
	}
}

func referencedSchemas(node any) []string {
	var names []string
	switch value := node.(type) {
	case map[string]any:
		if reference, ok := value["$ref"].(string); ok && strings.HasPrefix(reference, "#/components/schemas/") {
			names = append(names, strings.TrimPrefix(reference, "#/components/schemas/"))
		}
		for _, child := range value {
			names = append(names, referencedSchemas(child)...)
		}
	case []any:
		for _, child := range value {
			names = append(names, referencedSchemas(child)...)
		}
	}
	return names
}

// --- 响应校验：用真实的响应构造器与处理器输出对照 schema ---

// unit 是 1 积分对应的内部最小单位数。
const unit = money.Amount(money.Scale)

var contractTime = time.Date(2026, 10, 7, 8, 30, 15, 123456789, time.UTC)

func contractPointer[T any](value T) *T { return &value }

func contractTiers() []ledger.PriceTier {
	return []ledger.PriceTier{
		{Name: "高峰", Timezone: "Asia/Shanghai", Weekdays: []int{1, 2, 3}, StartMinute: contractPointer(int16(540)), EndMinute: contractPointer(int16(1080)),
			InputPrice: 3 * unit, OutputPrice: 6 * unit, CacheWritePrice: 1, CacheReadPrice: 2},
		{Name: "长上下文", Timezone: "UTC", MinPromptTokens: contractPointer(int64(1000)), MaxPromptTokens: contractPointer(int64(2000)),
			InputPrice: 1, OutputPrice: 2, CacheWritePrice: 3, CacheReadPrice: 4},
	}
}

func contractCatalogModel() catalog.Model {
	return catalog.Model{
		ID: "openai/gpt-test", Name: "GPT Test", Provider: "OpenAI", ContextWindow: 128000, ParameterInfo: "未公开",
		InputModalities: []string{"text"}, OutputModalities: []string{"text"}, SupportsTools: true,
		InputPrice: 2*unit + 500_000_000, OutputPrice: 10 * unit, CacheWritePrice: 1, CacheReadPrice: 2,
		PriceTiers: contractTiers(), Status: catalog.StatusActive, Version: 3,
		CreatedAt: contractTime, UpdatedAt: contractTime, PriceUpdatedAt: contractTime,
	}
}

func contractValidation() channel.ValidationAttempt {
	return channel.ValidationAttempt{
		ID: "val-1", OfferID: "offer-1", ValidationVersion: 2, AttemptSeq: 1, ActorAccountID: "acct-1",
		Status: channel.ValidationFailed, ErrorCategory: channel.ErrorUpstream, HTTPStatus: 502,
		RawError: "bad gateway", RawErrorTruncated: true, Duration: 1500 * time.Millisecond,
		StartedAt: contractTime, CompletedAt: &contractTime,
	}
}

func contractOffer() channel.Offer {
	validation := contractValidation()
	return channel.Offer{
		ID: "offer-1", ChannelID: "chan-1", ModelID: "openai/gpt-test", ModelName: "GPT Test", ModelProvider: "OpenAI",
		Protocol: channel.ProtocolOpenAIChat, UpstreamModelID: "gpt-test", Multiplier: unit / 2,
		Status: channel.OfferActive, ValidationVersion: 2, Version: 4,
		InputPrice: 2 * unit, OutputPrice: 8 * unit, CacheWritePrice: unit, CacheReadPrice: unit / 10,
		PriceTiers: contractTiers(), LatestValidation: &validation, Eligible: true,
		CallSuccessRate: contractPointer("0.99"), TTFTMilliseconds: contractPointer(int64(420)),
		TokensPerSecond: contractPointer("55.5"), CallCount: contractPointer(int64(12)),
		ProviderIncome: contractPointer(money.Amount(5 * unit)), CreatedAt: contractTime, UpdatedAt: contractTime,
	}
}

func contractChannel() channel.Channel {
	return channel.Channel{
		ID: "chan-1", OwnerAccountID: "acct-1", OwnerDisplayName: "分享者", DisplayName: "我的渠道",
		NormalizedBaseURL: "https://api.example.com/v1", CredentialConfigured: true, CredentialVersion: 2,
		CredentialUpdatedAt: &contractTime, Status: channel.StatusPublished, Version: 5,
		Offers: []channel.Offer{contractOffer()}, CreatedAt: contractTime, UpdatedAt: contractTime,
	}
}

func contractUsage() *ledger.UsageV1 {
	return &ledger.UsageV1{InputTokens: 10, OutputTokens: 20, CacheWriteTokens: 1, CacheReadTokens: 2}
}

func contractCall() gateway.Call {
	ttft, duration := 300*time.Millisecond, 2*time.Second
	return gateway.Call{
		ID: "call-1", ConsumerAccountID: "acct-1", APIKeyID: "key-1", KeyPrefix: "oma_abcd", KeyGeneration: 1,
		PoolID: "pool-1", PoolVersion: 2, CanonicalModelID: "openai/gpt-test", Protocol: channel.ProtocolOpenAIChat,
		Status: gateway.CallSucceeded, DecisionCode: "ok", CandidateCount: 2, UpstreamAttemptCount: 1,
		HoldID: "hold-1", Preauthorized: unit, FeeRateVersion: 1, FeeRateNano: 20_000_000,
		FinalOfferID: "offer-1", FinalChannelName: "我的渠道", CompletionReason: "completed", Usage: contractUsage(),
		ProviderCharge: unit / 2, PlatformFee: unit / 100, SettledPriceTierSeq: 1, FinalHTTPStatus: 200,
		Attempts: []gateway.Attempt{{
			ID: "att-1", CallID: "call-1", Sequence: 1, OfferID: "offer-1", ChannelDisplayName: "我的渠道",
			ProviderAccountID: "acct-2", Status: gateway.AttemptSucceeded, HTTPStatus: 200, SemanticCommitted: true,
			TTFT: &ttft, Duration: &duration, Usage: contractUsage(), TokensPerSecondNano: contractPointer(int64(55_500_000_000)),
			StartedAt: contractTime, CompletedAt: &contractTime,
		}},
		CreatedAt: contractTime, CompletedAt: &contractTime,
	}
}

func contractC2COrder() c2c.Order {
	return c2c.Order{
		ID: "order-1", OwnerAccountID: "acct-1", OwnerDisplayName: "卖家", Side: c2c.SideSell, UnitPriceFen: 100,
		Total: 10 * unit, Available: 5 * unit, Allocated: unit, Settled: 3 * unit, Closed: unit,
		Minimum: unit, Maximum: 5 * unit, Status: c2c.OrderOpen, Takeable: true,
		PaymentTypes:   []c2c.PaymentMethodType{c2c.PaymentWeChat},
		PaymentMethods: []c2c.PaymentMethod{{ID: "pm-1", Type: c2c.PaymentWeChat, Position: 1, Contact: "wx", Instructions: "备注订单号", QRAvailable: true}},
		CreatedAt:      contractTime, UpdatedAt: contractTime,
	}
}

func contractC2CTrade() c2c.Trade {
	return c2c.Trade{
		ID: "trade-1", OrderID: "order-1", OrderSide: c2c.SideSell, BuyerAccountID: "acct-2", BuyerDisplayName: "买家",
		SellerAccountID: "acct-1", SellerDisplayName: "卖家", BuyerCreditFrozen: true, Quantity: unit,
		UnitPriceFen: 100, FiatAmountFen: 100, Status: c2c.TradeDisputed,
		SelectedPaymentMethod: &c2c.PaymentMethod{ID: "pm-1", Type: c2c.PaymentAlipay, Contact: "ali", QRAvailable: false},
		PaymentReference:      "REF123", PaymentReferenceGone: &contractTime, PaymentDeadline: contractTime, ReviewDueAt: &contractTime,
		LedgerTransactionID: "tx-1",
		Statements:          []c2c.Statement{{ID: "st-1", ActorAccountID: "acct-2", ActorDisplayName: "买家", Text: "已付款", CharacterCount: 3, CreatedAt: contractTime, DeletedAt: &contractTime}},
		Events: []c2c.Event{
			{ID: 1, ActorAccountID: "acct-2", Action: "trade.paid", CreatedAt: contractTime},
			{ID: 2, ActorAccountID: "admin", Action: "dispute.buyer_restricted", Reason: "恶意争议", CreatedAt: contractTime},
		},
		CreatedAt: contractTime, UpdatedAt: contractTime, PaidAt: &contractTime, ResolvedAt: &contractTime,
	}
}

func TestOpenAPIAccountAndErrorResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	for _, account := range []identity.Account{
		{ID: "acct-1", Username: "alice", DisplayName: "Alice", Status: identity.StatusActive, Version: 2,
			CreditLimit: 100 * unit, PostedBalance: -5 * unit, CreatedAt: contractTime, UpdatedAt: contractTime, PasswordChangedAt: &contractTime},
		{ID: "acct-2", Username: "bob", DisplayName: "Bob", IsAdmin: true, Status: identity.StatusDisabled, MustChangePassword: true,
			CreditFrozen: true, CreatedAt: contractTime, UpdatedAt: contractTime},
	} {
		spec.assertSchema(t, "Account", accountResponse(account))
		spec.assertSchema(t, "AccountList", map[string]any{"accounts": []map[string]any{accountResponse(account)}})
	}

	recorder := httptest.NewRecorder()
	writeError(recorder, http.StatusForbidden, "forbidden", "没有执行该操作的权限")
	spec.assertSchema(t, "ErrorResponse", recorder.Body.Bytes())

	recorder = httptest.NewRecorder()
	writeDomainError(recorder, errors.New("boom"))
	spec.assertSchema(t, "ErrorResponse", recorder.Body.Bytes())
}

func TestOpenAPIHealthAndInstanceResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	handler := NewHandler(Dependencies{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	spec.assertSchema(t, "Health", recorder.Body.Bytes())

	failing := NewHandler(Dependencies{DatabaseReady: func(context.Context) error { return errors.New("down") }})
	recorder = httptest.NewRecorder()
	failing.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("health = %d", recorder.Code)
	}
	spec.assertSchema(t, "ErrorResponse", recorder.Body.Bytes())

	application := newInstanceApp(t, &instanceStubStore{})
	recorder = httptest.NewRecorder()
	application.instanceState(recorder, httptest.NewRequest(http.MethodGet, "/api/instance", nil))
	spec.assertSchema(t, "InstanceState", recorder.Body.Bytes())

	recorder = httptest.NewRecorder()
	application.instanceInitialize(recorder, httptest.NewRequest(http.MethodPost, "/api/instance/initialize", strings.NewReader(
		`{"username":"founder","display_name":"创始人","password":"Instance-Init-2026!"}`)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("initialize = %d %s", recorder.Code, recorder.Body.String())
	}
	spec.assertSchema(t, "InstanceInitializeResponse", recorder.Body.Bytes())

	application = newInstanceApp(t, &instanceStubStore{})
	recorder = httptest.NewRecorder()
	application.instanceInitialize(recorder, httptest.NewRequest(http.MethodPost, "/api/instance/initialize", strings.NewReader(`not json`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("initialize with invalid body = %d %s", recorder.Code, recorder.Body.String())
	}
	spec.assertSchema(t, "ErrorResponse", recorder.Body.Bytes())
}

func TestOpenAPICatalogResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	model := contractCatalogModel()
	spec.assertSchema(t, "Model", modelResponse(model))
	minimal := model
	minimal.PriceTiers, minimal.SupportsTools = nil, false
	recorder := httptest.NewRecorder()
	writeModelList(recorder, []catalog.Model{model, minimal})
	spec.assertSchema(t, "ModelList", recorder.Body.Bytes())
	spec.assertSchema(t, "ModelEnvelope", map[string]any{"model": modelResponse(model)})
}

func TestOpenAPILedgerResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	for _, wallet := range []ledger.Wallet{
		{PostedBalance: 5 * unit, CreditLimit: 10 * unit, EffectiveCredit: 10 * unit, SpendableCapacity: 15 * unit, UpdatedAt: contractTime},
		{PostedBalance: -20 * unit, CreditLimit: 10 * unit, CreditFrozen: true, OverLimit: true, UpdatedAt: contractTime},
	} {
		spec.assertSchema(t, "Wallet", walletResponse(wallet))
	}
	entry := ledger.Entry{
		ID: 42, TransactionID: "tx-1", Ordinal: 1, BusinessRole: "provider_income", Amount: unit,
		PostedBalanceBefore: 0, PostedBalanceAfter: unit, CreatedAt: contractTime, TransactionKind: ledger.TransactionTransfer,
		Reason: "调用结算", ReferenceType: "call", ReferenceID: "call-1", ActorAccountID: "acct-1", HoldID: "hold-1",
		Counterparties: []ledger.Counterparty{{AccountKind: ledger.AccountUser, IdentityAccountID: "acct-2", BusinessRole: "consumer", Amount: -unit}},
		AccountKind:    ledger.AccountUser, IdentityAccountID: "acct-1",
	}
	spec.assertSchema(t, "LedgerEntry", entryResponse(entry))
	spec.assertSchema(t, "LedgerEntryPage", map[string]any{"entries": []map[string]any{entryResponse(entry)}, "next_before": "42"})
	spec.assertSchema(t, "LedgerTransaction", transactionResponse(ledger.Transaction{
		ID: "tx-1", IdempotencyKey: "key", Kind: ledger.TransactionAdjustment, Reason: "调整", ReferenceType: "ticket",
		ReferenceID: "T-1", ActorAccountID: "admin", Entries: []ledger.Entry{entry}, CreatedAt: contractTime,
	}))
	spec.assertSchema(t, "LedgerMetrics", ledgerMetricsResponse(ledger.Metrics{
		TotalPostedBalance: "0", PositivePostedBalance: "10", NegativePostedBalance: "-10", TotalCreditLimit: "100", UsedCredit: "10",
		AssetReserved: "1", SpendAuthorized: "1.5", IncentivePostedBalance: "0", LossPostedBalance: "0",
		PostedProjectionDifference: "0", AssetReservationDifference: "0", SpendAuthorizationDifference: "0", AccountCount: 3,
	}))
}

func TestOpenAPIChannelResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	item := contractChannel()
	spec.assertSchema(t, "OwnerChannel", ownerChannelResponse(item))
	spec.assertSchema(t, "OwnerOffer", ownerOfferResponse(item.Offers[0]))
	bare := item
	bare.Offers = []channel.Offer{{ID: "o2", ModelID: "m", Protocol: channel.ProtocolGemini, Status: channel.OfferDisabled, Multiplier: unit}}
	bare.CredentialUpdatedAt = nil
	spec.assertSchema(t, "OwnerChannel", ownerChannelResponse(bare))
	spec.assertSchema(t, "AdminChannel", adminChannelResponse(item))
	spec.assertSchema(t, "AdminChannel", adminChannelResponse(bare))
	spec.assertSchema(t, "MarketChannel", marketChannelResponse(item))
	validation := contractValidation()
	spec.assertSchema(t, "ValidationAttempt", validationResponse(validation, true))
	spec.assertSchema(t, "ValidationSummary", validationResponse(validation, false))
	spec.assertSchema(t, "ValidationAttemptList", map[string]any{"validation_attempts": []map[string]any{validationResponse(validation, true)}})
	spec.assertSchema(t, "MarketOffer", marketOfferResponse(channel.MarketOffer{
		OfferID: "offer-1", ChannelID: "chan-1", ChannelDisplayName: "我的渠道", OwnerAccountID: "acct-1", OwnerDisplayName: "分享者",
		ModelID: "openai/gpt-test", ModelName: "GPT Test", ModelProvider: "OpenAI", Protocol: channel.ProtocolAnthropic,
		Multiplier: unit, InputPrice: unit, ValidationStatus: channel.ValidationPassed, PriceTiers: contractTiers(), LastTestedAt: &contractTime,
	}))
}

func TestOpenAPIGatewayResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	key := gateway.APIKey{
		ID: "key-1", DisplayName: "默认", Prefix: "oma_abcd", Generation: 1, Status: gateway.KeyActive, Version: 2,
		Pools: []gateway.ModelPool{{
			ID: "pool-1", CanonicalModelID: "openai/gpt-test", ModelName: "GPT Test", Protocol: channel.ProtocolOpenAIChat, Version: 1,
			Members: []gateway.PoolMember{{
				Priority: 1, OfferID: "offer-1", ChannelID: "chan-1", ChannelDisplayName: "我的渠道", OwnerDisplayName: "分享者",
				AddedValidationVersion: 1, CurrentValidationVersion: 1, Eligible: true, InputPrice: unit,
				Multiplier: unit, PriceTiers: contractTiers(), CallSuccessRate: contractPointer("1"),
			}},
			CreatedAt: contractTime, UpdatedAt: contractTime,
		}},
		LastUsedAt: &contractTime, CreatedAt: contractTime, UpdatedAt: contractTime,
	}
	spec.assertSchema(t, "ApiKey", apiKeyResponse(key))
	spec.assertSchema(t, "ApiKeyWithSecret", map[string]any{"key": apiKeyResponse(key), "secret": "oma_secret"})
	key.Pools, key.LastUsedAt = nil, nil
	spec.assertSchema(t, "ApiKey", apiKeyResponse(key))

	call := contractCall()
	spec.assertSchema(t, "GatewayCall", gatewayCallResponse(call))
	rejected := gateway.Call{ID: "c2", Protocol: channel.ProtocolGemini, Status: gateway.CallRejected, DecisionCode: "no_candidate", CreatedAt: contractTime}
	spec.assertSchema(t, "GatewayCall", gatewayCallResponse(rejected))
	spec.assertSchema(t, "Dashboard", dashboardResponse(gateway.Dashboard{
		ConsumerSpent: unit, ProviderIncome: unit, ActiveKeyCount: 1, RecentCalls: []gateway.Call{call, rejected},
	}))
}

func TestOpenAPIPendingItemResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	items := []dashboard.PendingItem{
		{ID: "c2c-release-t1", Kind: dashboard.KindC2CRelease, Label: "待放行", Tone: dashboard.ToneWarning, Title: "买家 已付款 ¥10.00", Detail: "确认收款后放行 10 积分", To: "/c2c/trades/t1"},
		{ID: "route-single-k1-p1", Kind: dashboard.KindRouteSingle, Label: "单渠道", Tone: dashboard.ToneInfo, Title: "GPT-5", Detail: "主力", To: "/market?model=m&protocol=openai_responses"},
	}
	spec.assertSchema(t, "PendingItemList", pendingItemListResponse(items))
	spec.assertSchema(t, "PendingItemList", pendingItemListResponse(nil))
}

func TestOpenAPIC2CResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	order := contractC2COrder()
	spec.assertSchema(t, "C2COrder", c2cOrderResponse(order))
	spec.assertSchema(t, "C2COrder", c2cOrderResponse(c2c.Order{ID: "o", Side: c2c.SideBuy, Status: c2c.OrderCancelled, CreatedAt: contractTime, UpdatedAt: contractTime, CancelledAt: &contractTime,
		PaymentTypes: []c2c.PaymentMethodType{}}))
	trade := contractC2CTrade()
	spec.assertSchema(t, "C2CTrade", c2cTradeResponse(trade))
	spec.assertSchema(t, "C2CAdminTrade", c2cAdminTradeResponse(trade))
	plain := c2c.Trade{ID: "t", OrderSide: c2c.SideBuy, Status: c2c.TradeAwaitingPayment, PaymentDeadline: contractTime, CreatedAt: contractTime, UpdatedAt: contractTime}
	spec.assertSchema(t, "C2CTrade", c2cTradeResponse(plain))
	spec.assertSchema(t, "C2CMarket", c2cMarketResponse(c2c.Market{
		GuidancePriceFen: 100, LatestPriceFen: contractPointer(int64(101)), SellOrders: []c2c.Order{order}, BuyOrders: []c2c.Order{order},
	}))
	adminJSON, _ := json.Marshal(c2cAdminTradeResponse(trade))
	if !bytes.Contains(adminJSON, []byte("dispute.buyer_restricted")) {
		t.Fatal("管理员视图应包含处罚事件")
	}
	participantJSON, _ := json.Marshal(c2cTradeResponse(trade))
	if bytes.Contains(participantJSON, []byte("dispute.buyer_restricted")) {
		t.Fatal("参与方视图不应包含处罚事件")
	}
}

func TestOpenAPIFeeRateResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	version := feerate.Version{Version: 2, Rate: 20_000_000, CreatedByID: "admin", CreatedByUsername: "root", Reason: "调整", CreatedAt: contractTime}
	spec.assertSchema(t, "FeeRateVersion", feeRateResponse(version))
	system := feerate.Version{Version: 1, Rate: 0, CreatedAt: contractTime}
	spec.assertSchema(t, "FeeRateHistory", map[string]any{"current": feeRateResponse(version), "history": []map[string]any{feeRateResponse(version), feeRateResponse(system)}})
}

func TestOpenAPIOpsResponses(t *testing.T) {
	spec := loadOpenAPI(t)
	store := &fakeOpsStore{
		metrics: ops.Metrics{
			Window: ops.Window{From: contractTime, To: contractTime.Add(time.Hour)},
			Ledger: ops.NewLedgerMetricsView(ledger.Metrics{TotalPostedBalance: "0", PositivePostedBalance: "1", NegativePostedBalance: "-1",
				TotalCreditLimit: "10", UsedCredit: "1", AssetReserved: "0", SpendAuthorized: "0", IncentivePostedBalance: "0", LossPostedBalance: "0",
				PostedProjectionDifference: "0", AssetReservationDifference: "0", SpendAuthorizationDifference: "0"}),
			EffectiveCredit: "10",
			NegativeBalances: []ops.NegativeBalanceRisk{{AccountID: "a", Username: "u", PostedBalance: "-1", NegativeSince: "2026-10-01T00:00:00Z",
				LastFinancialActivity: "2026-10-02T00:00:00Z", InactiveDays: 3, OverLimit: true, CreditLimit: "10"}},
			API:         ops.APIMetrics{SuccessRate: contractPointer("0.5"), AverageTTFTMillis: contractPointer(int64(100))},
			Consumption: ops.ConsumptionMetrics{ConsumerSpend: "1", ProviderIncome: "1", OwnUsageIncome: "0", OtherConsumerIncome: "1", PlatformFee: "0"},
			C2C: ops.C2CMetrics{Orders: []ops.C2COrderStatusCount{{Side: "sell", Status: "open", Count: 1}},
				Trades: []ops.C2CTradeStatusCount{{Status: "paid", Count: 1}}, Quote: ops.C2CMarketQuote{BestBidPriceFen: contractPointer(int64(99))}},
			Concentration: ops.ConcentrationMetrics{TotalPositive: "1", HHI: contractPointer("1")},
		},
		providers: ops.ProviderIncomeSnapshot{TotalIncome: "1", OtherConsumerIncome: "1", OwnUsageIncome: "0", ActiveProviders: 1,
			Providers: []ops.ProviderIncomeRow{{AccountID: "a", DisplayName: "分享者", TotalIncome: "1", OtherConsumerIncome: "1", OwnUsageIncome: "0"}}},
		anomalies: ops.Anomalies{Hard: []ops.Anomaly{{Kind: "zero_sum", Count: 1, Detail: "d", Drilldown: "/admin/ledger"}}, Attention: []ops.Anomaly{}, CheckedAt: contractTime},
		records: []ops.InspectionRecord{{ID: "i1", InspectionVersion: ops.InspectionVersion, TriggeredBy: "manual", ZeroSumDifference: "0",
			PostedProjectionDifference: "0", AssetProjectionDifference: "0", AuthorizationProjectionDiff: "0", CheckedAt: contractTime}},
		summary: ops.TrialSummary{GeneratedAt: contractTime, FirstCallAt: &contractTime, LastInspectionOK: contractPointer(true)},
	}
	application := &app{ops: store}
	window := "?from=2026-10-07T00:00:00Z&to=2026-10-08T00:00:00Z"
	call := func(handler http.HandlerFunc, method, target string) []byte {
		recorder := httptest.NewRecorder()
		handler(recorder, httptest.NewRequest(method, target, nil))
		if recorder.Code >= 300 {
			t.Fatalf("%s %s = %d %s", method, target, recorder.Code, recorder.Body.String())
		}
		return recorder.Body.Bytes()
	}
	spec.assertSchema(t, "OpsMetricsEnvelope", call(application.opsMetrics, http.MethodGet, "/api/admin/ops/metrics"+window))
	spec.assertSchema(t, "OpsProviderIncomeEnvelope", call(application.opsProviderIncome, http.MethodGet, "/api/admin/ops/providers"+window))
	spec.assertSchema(t, "OpsAnomaliesEnvelope", call(application.opsAnomalies, http.MethodGet, "/api/admin/ops/anomalies"))
	spec.assertSchema(t, "OpsInspectionList", call(application.opsListInspections, http.MethodGet, "/api/admin/ops/inspections"))
	spec.assertSchema(t, "OpsTrialSummaryEnvelope", call(application.opsTrialSummary, http.MethodGet, "/api/admin/ops/trial-summary"))
	spec.assertSchema(t, "OpsInspectionEnvelope", map[string]any{"inspection": store.records[0]})
	recorder := httptest.NewRecorder()
	application.opsRunInspection(recorder, httptest.NewRequest(http.MethodPost, "/api/admin/ops/inspections", nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("run inspection = %d", recorder.Code)
	}
}
