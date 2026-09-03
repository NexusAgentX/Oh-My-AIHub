package postgres_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/api"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

func TestStoreIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	basePool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(basePool.Close)

	schema := "test_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
	})

	schemaURL := withSearchPath(t, databaseURL, schema)
	if err := database.Migrate(ctx, schemaURL); err != nil {
		t.Fatalf("migrate isolated test schema: %v", err)
	}
	pool, err := database.Open(ctx, schemaURL)
	if err != nil {
		t.Fatalf("open isolated test schema: %v", err)
	}
	t.Cleanup(pool.Close)

	store := storepg.New(pool)
	identityService, err := identity.NewService(store, 24*time.Hour)
	if err != nil {
		t.Fatalf("create identity service: %v", err)
	}
	catalogService := catalog.NewService(store)

	admin := createExactlyOneBootstrapAdmin(t, ctx, store)

	login, err := identityService.Login(ctx, admin.Username, "Bootstrap-password-2026")
	if err != nil {
		t.Fatalf("log in bootstrap administrator: %v", err)
	}
	if !login.Account.MustChangePassword || !login.Account.IsAdmin {
		t.Fatalf("bootstrap account = %+v, want administrator requiring password change", login.Account)
	}
	if _, err := identityService.Authenticate(ctx, login.SessionToken); err != nil {
		t.Fatalf("authenticate bootstrap session: %v", err)
	}

	changed, err := identityService.ChangePassword(ctx, admin.ID, "Bootstrap-password-2026", "A-new-administrator-password-2026")
	if err != nil {
		t.Fatalf("change bootstrap password: %v", err)
	}
	admin = changed.Account
	if admin.MustChangePassword || admin.PasswordVersion != 2 {
		t.Fatalf("changed account = %+v, want ready account at password version 2", admin)
	}
	if _, err := identityService.Authenticate(ctx, login.SessionToken); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("old session error = %v, want invalid credentials", err)
	}
	if _, err := identityService.Authenticate(ctx, changed.SessionToken); err != nil {
		t.Fatalf("authenticate replacement session: %v", err)
	}
	var storedTokenHash []byte
	if err := pool.QueryRow(ctx, `SELECT token_hash FROM sessions WHERE account_id = $1`, admin.ID).Scan(&storedTokenHash); err != nil {
		t.Fatalf("read persisted session token digest: %v", err)
	}
	wantTokenHash := sha256.Sum256([]byte(changed.SessionToken))
	if !bytes.Equal(storedTokenHash, wantTokenHash[:]) || bytes.Equal(storedTokenHash, []byte(changed.SessionToken)) {
		t.Fatal("database session value was not the expected one-way token digest")
	}
	if _, err := identityService.Login(ctx, admin.Username, "Bootstrap-password-2026"); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("old password login error = %v, want invalid credentials", err)
	}

	handler := api.NewHandler(api.Dependencies{
		Identity:     identityService,
		Catalog:      catalogService,
		CookieSecure: true,
	})
	adminCookie := loginCookie(t, handler, admin.Username, "A-new-administrator-password-2026")
	if !adminCookie.HttpOnly || !adminCookie.Secure || adminCookie.SameSite != http.SameSiteLaxMode || adminCookie.Path != "/" || adminCookie.Domain != "" {
		t.Fatalf("session cookie = %+v, want secure host-only HttpOnly SameSite=Lax cookie", adminCookie)
	}

	crossOrigin := jsonRequest(t, http.MethodPost, "https://hub.example/api/admin/accounts", map[string]any{
		"username": "blocked.user", "display_name": "跨站请求", "credit_limit": "0",
	})
	crossOrigin.Header.Set("Origin", "https://evil.example")
	crossOrigin.AddCookie(adminCookie)
	crossOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossOriginResponse, crossOrigin)
	if crossOriginResponse.Code != http.StatusForbidden || !strings.Contains(crossOriginResponse.Body.String(), "cross_origin_request") {
		t.Fatalf("cross-origin response = %d %s", crossOriginResponse.Code, crossOriginResponse.Body.String())
	}

	createOverHTTP := jsonRequest(t, http.MethodPost, "https://hub.example/api/admin/accounts", map[string]any{
		"username": "member.web", "display_name": "网页成员", "credit_limit": "3.75", "is_admin": false, "status": "active",
	})
	createOverHTTP.Header.Set("Origin", "https://hub.example")
	createOverHTTP.AddCookie(adminCookie)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createOverHTTP)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("HTTP account creation = %d %s", createResponse.Code, createResponse.Body.String())
	}
	var createdPayload struct {
		Account struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			IsAdmin  bool   `json:"is_admin"`
			Status   string `json:"status"`
			Version  int64  `json:"version"`
		} `json:"account"`
		InitialPassword string `json:"initial_password"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createdPayload); err != nil {
		t.Fatalf("decode account creation response: %v", err)
	}
	if createdPayload.Account.ID == "" || createdPayload.Account.Username != "member.web" || createdPayload.Account.IsAdmin || createdPayload.Account.Status != "active" || createdPayload.InitialPassword == "" {
		t.Fatalf("account creation payload = %+v", createdPayload)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/admin/accounts", nil)
	listRequest.AddCookie(adminCookie)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), "initial_password") || strings.Contains(listResponse.Body.String(), createdPayload.InitialPassword) {
		t.Fatalf("account list leaked one-time credential: %d %s", listResponse.Code, listResponse.Body.String())
	}
	exactAccountRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/admin/accounts?q="+url.QueryEscape(createdPayload.Account.ID), nil)
	exactAccountRequest.AddCookie(adminCookie)
	exactAccountResponse := httptest.NewRecorder()
	handler.ServeHTTP(exactAccountResponse, exactAccountRequest)
	var exactAccountPayload struct {
		Accounts []struct {
			ID string `json:"id"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(exactAccountResponse.Body.Bytes(), &exactAccountPayload); err != nil {
		t.Fatalf("decode exact account search: %v", err)
	}
	if exactAccountResponse.Code != http.StatusOK || len(exactAccountPayload.Accounts) != 1 || exactAccountPayload.Accounts[0].ID != createdPayload.Account.ID {
		t.Fatalf("exact account search = %d %+v", exactAccountResponse.Code, exactAccountPayload.Accounts)
	}

	invalidAccountRequest := jsonRequest(t, http.MethodPatch, "https://hub.example/api/admin/accounts/not-a-uuid", map[string]string{"status": "disabled"})
	invalidAccountRequest.Header.Set("Origin", "https://hub.example")
	invalidAccountRequest.AddCookie(adminCookie)
	invalidAccountResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidAccountResponse, invalidAccountRequest)
	if invalidAccountResponse.Code != http.StatusNotFound {
		t.Fatalf("invalid account UUID response = %d %s", invalidAccountResponse.Code, invalidAccountResponse.Body.String())
	}

	httpModel := map[string]any{
		"id":                         "openai/http-route-model",
		"name":                       "HTTP Route Model",
		"provider":                   "OpenAI",
		"context_window":             128000,
		"parameter_info":             "",
		"input_modalities":           []string{"text"},
		"output_modalities":          []string{"text"},
		"supports_tools":             true,
		"supports_structured_output": true,
		"supports_vision":            false,
		"input_price":                "0.0375",
		"output_price":               "1.25",
		"cache_write_price":          "0",
		"cache_read_price":           "0.00375",
		"status":                     "active",
	}
	createModelRequest := jsonRequest(t, http.MethodPost, "https://hub.example/api/admin/models", httpModel)
	createModelRequest.Header.Set("Origin", "https://hub.example")
	createModelRequest.AddCookie(adminCookie)
	createModelResponse := httptest.NewRecorder()
	handler.ServeHTTP(createModelResponse, createModelRequest)
	if createModelResponse.Code != http.StatusCreated || !strings.Contains(createModelResponse.Body.String(), `"input_price":"0.0375"`) {
		t.Fatalf("HTTP model creation = %d %s", createModelResponse.Code, createModelResponse.Body.String())
	}

	getModelRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/admin/models/openai/http-route-model", nil)
	getModelRequest.AddCookie(adminCookie)
	getModelResponse := httptest.NewRecorder()
	handler.ServeHTTP(getModelResponse, getModelRequest)
	if getModelResponse.Code != http.StatusOK || !strings.Contains(getModelResponse.Body.String(), "HTTP Route Model") {
		t.Fatalf("slash-containing model detail = %d %s", getModelResponse.Code, getModelResponse.Body.String())
	}

	httpModel["status"] = "disabled"
	httpModel["input_price"] = "0.075"
	httpModel["expected_version"] = 1
	delete(httpModel, "id")
	updateModelRequest := jsonRequest(t, http.MethodPut, "https://hub.example/api/admin/models/openai/http-route-model", httpModel)
	updateModelRequest.Header.Set("Origin", "https://hub.example")
	updateModelRequest.AddCookie(adminCookie)
	updateModelResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateModelResponse, updateModelRequest)
	if updateModelResponse.Code != http.StatusOK || !strings.Contains(updateModelResponse.Body.String(), `"input_price":"0.075"`) {
		t.Fatalf("HTTP model update = %d %s", updateModelResponse.Code, updateModelResponse.Body.String())
	}

	publicModelRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/models/openai/http-route-model", nil)
	publicModelRequest.AddCookie(adminCookie)
	publicModelResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicModelResponse, publicModelRequest)
	if publicModelResponse.Code != http.StatusNotFound {
		t.Fatalf("disabled public model response = %d %s", publicModelResponse.Code, publicModelResponse.Body.String())
	}

	memberWebCookie := loginCookie(t, handler, "member.web", createdPayload.InitialPassword)
	forcedRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/account", nil)
	forcedRequest.AddCookie(memberWebCookie)
	forcedResponse := httptest.NewRecorder()
	handler.ServeHTTP(forcedResponse, forcedRequest)
	if forcedResponse.Code != http.StatusForbidden || !strings.Contains(forcedResponse.Body.String(), "password_change_required") {
		t.Fatalf("forced-password-change gate = %d %s", forcedResponse.Code, forcedResponse.Body.String())
	}

	changeRequest := jsonRequest(t, http.MethodPut, "https://hub.example/api/account/password", map[string]any{
		"current_password": createdPayload.InitialPassword,
		"new_password":     "Member-web-password-2026",
	})
	changeRequest.Header.Set("Origin", "https://hub.example")
	changeRequest.AddCookie(memberWebCookie)
	changeResponse := httptest.NewRecorder()
	handler.ServeHTTP(changeResponse, changeRequest)
	if changeResponse.Code != http.StatusOK {
		t.Fatalf("HTTP password change = %d %s", changeResponse.Code, changeResponse.Body.String())
	}
	memberReadyCookie := responseCookie(t, changeResponse)
	adminRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/admin/accounts", nil)
	adminRequest.AddCookie(memberReadyCookie)
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusForbidden || !strings.Contains(adminResponse.Body.String(), "administrator_required") {
		t.Fatalf("ordinary-user admin boundary = %d %s", adminResponse.Code, adminResponse.Body.String())
	}

	ordinaryWrites := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/admin/accounts"},
		{http.MethodPatch, "/api/admin/accounts/" + createdPayload.Account.ID},
		{http.MethodPost, "/api/admin/models"},
		{http.MethodPut, "/api/admin/models/openai/http-route-model"},
	}
	for _, write := range ordinaryWrites {
		request := jsonRequest(t, write.method, "https://hub.example"+write.path, map[string]any{})
		request.Header.Set("Origin", "https://hub.example")
		request.AddCookie(memberReadyCookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "administrator_required") {
			t.Fatalf("ordinary-user write %s %s = %d %s, want administrator rejection", write.method, write.path, response.Code, response.Body.String())
		}
	}

	grantRequest := jsonRequest(t, http.MethodPatch, "https://hub.example/api/admin/accounts/"+createdPayload.Account.ID, map[string]any{
		"expected_version": createdPayload.Account.Version,
		"is_admin":         true,
	})
	grantRequest.Header.Set("Origin", "https://hub.example")
	grantRequest.AddCookie(adminCookie)
	grantResponse := httptest.NewRecorder()
	handler.ServeHTTP(grantResponse, grantRequest)
	if grantResponse.Code != http.StatusOK || !strings.Contains(grantResponse.Body.String(), `"is_admin":true`) {
		t.Fatalf("grant administrator = %d %s", grantResponse.Code, grantResponse.Body.String())
	}
	var grantedPayload struct {
		Account struct {
			Version int64 `json:"version"`
		} `json:"account"`
	}
	if err := json.Unmarshal(grantResponse.Body.Bytes(), &grantedPayload); err != nil {
		t.Fatalf("decode granted administrator response: %v", err)
	}
	grantedAdminRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/admin/accounts", nil)
	grantedAdminRequest.AddCookie(memberReadyCookie)
	grantedAdminResponse := httptest.NewRecorder()
	handler.ServeHTTP(grantedAdminResponse, grantedAdminRequest)
	if grantedAdminResponse.Code != http.StatusOK {
		t.Fatalf("newly granted administrator access = %d %s", grantedAdminResponse.Code, grantedAdminResponse.Body.String())
	}
	demoteRequest := jsonRequest(t, http.MethodPatch, "https://hub.example/api/admin/accounts/"+createdPayload.Account.ID, map[string]any{
		"expected_version": grantedPayload.Account.Version,
		"is_admin":         false,
	})
	demoteRequest.Header.Set("Origin", "https://hub.example")
	demoteRequest.AddCookie(adminCookie)
	demoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(demoteResponse, demoteRequest)
	if demoteResponse.Code != http.StatusOK || !strings.Contains(demoteResponse.Body.String(), `"is_admin":false`) {
		t.Fatalf("revoke administrator = %d %s", demoteResponse.Code, demoteResponse.Body.String())
	}

	credit := mustAmount(t, "10.25")
	created, err := identityService.CreateInvitedAccount(ctx, admin, " Member.One ", "成员一", credit, false, identity.StatusActive)
	if err != nil {
		t.Fatalf("create invited account: %v", err)
	}
	if created.Account.Username != "member.one" || created.Account.CreditLimit != credit || created.InitialPassword == "" || !created.Account.MustChangePassword {
		t.Fatalf("created account = %+v, want normalized invited account with one-time password", created)
	}
	memberLogin, err := identityService.Login(ctx, "MEMBER.ONE", created.InitialPassword)
	if err != nil {
		t.Fatalf("log in invited account: %v", err)
	}
	if !memberLogin.Account.MustChangePassword {
		t.Fatal("invited account did not retain the forced-password-change flag")
	}
	if _, err := identityService.CreateInvitedAccount(ctx, admin, "member.one", "重复成员", 0, false, identity.StatusActive); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("duplicate username error = %v, want conflict", err)
	}
	disabledInvite, err := identityService.CreateInvitedAccount(ctx, admin, "disabled.invite", "停用邀请", 0, false, identity.StatusDisabled)
	if err != nil || disabledInvite.Account.Status != identity.StatusDisabled {
		t.Fatalf("create disabled invited account = %+v, err = %v", disabledInvite.Account, err)
	}
	if _, err := identityService.Login(ctx, disabledInvite.Account.Username, disabledInvite.InitialPassword); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("disabled invite login error = %v, want invalid credentials", err)
	}

	newCredit := mustAmount(t, "42.5")
	updated, err := identityService.UpdateAccount(ctx, admin, created.Account.ID, identity.AccountUpdate{
		ExpectedVersion: created.Account.Version,
		CreditLimit:     &newCredit,
	})
	if err != nil {
		t.Fatalf("update account credit: %v", err)
	}
	if updated.CreditLimit != newCredit {
		t.Fatalf("updated credit = %s, want %s", updated.CreditLimit, newCredit)
	}
	trueValue := true
	if _, err := identityService.UpdateAccount(ctx, admin, created.Account.ID, identity.AccountUpdate{
		ExpectedVersion: created.Account.Version,
		IsAdmin:         &trueValue,
	}); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("stale account update error = %v, want conflict", err)
	}
	currentAccount, err := store.FindAccountByID(ctx, created.Account.ID)
	if err != nil || currentAccount.IsAdmin || currentAccount.CreditLimit != newCredit {
		t.Fatalf("account after stale update = %+v, err = %v", currentAccount.Account, err)
	}
	disabled := identity.StatusDisabled
	if _, err := identityService.UpdateAccount(ctx, admin, created.Account.ID, identity.AccountUpdate{
		ExpectedVersion: updated.Version,
		Status:          &disabled,
	}); err != nil {
		t.Fatalf("disable invited account: %v", err)
	}
	if _, err := identityService.Authenticate(ctx, memberLogin.SessionToken); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("disabled account session error = %v, want invalid credentials", err)
	}
	if _, err := identityService.Login(ctx, created.Account.Username, created.InitialPassword); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("disabled account login error = %v, want invalid credentials", err)
	}
	if _, err := identityService.UpdateAccount(ctx, admin, admin.ID, identity.AccountUpdate{
		ExpectedVersion: admin.Version,
		Status:          &disabled,
	}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self-disable administrator error = %v, want forbidden", err)
	}

	raceAccount, err := identityService.CreateInvitedAccount(ctx, admin, "password.race", "并发改密", 0, false, identity.StatusActive)
	if err != nil {
		t.Fatalf("create password race account: %v", err)
	}
	raceLogin, err := identityService.Login(ctx, raceAccount.Account.Username, raceAccount.InitialPassword)
	if err != nil {
		t.Fatalf("login password race account: %v", err)
	}
	type passwordChangeResult struct {
		password string
		result   identity.LoginResult
		err      error
	}
	passwordResults := make(chan passwordChangeResult, 2)
	for _, password := range []string{"Password-race-winner-one", "Password-race-winner-two"} {
		go func(password string) {
			result, err := identityService.ChangePassword(ctx, raceAccount.Account.ID, raceAccount.InitialPassword, password)
			passwordResults <- passwordChangeResult{password: password, result: result, err: err}
		}(password)
	}
	var passwordWinner passwordChangeResult
	var passwordSuccesses, passwordConflicts int
	for range 2 {
		result := <-passwordResults
		if result.err == nil {
			passwordWinner = result
			passwordSuccesses++
		} else if errors.Is(result.err, identity.ErrConflict) {
			passwordConflicts++
		} else {
			t.Fatalf("concurrent password change error = %v", result.err)
		}
	}
	if passwordSuccesses != 1 || passwordConflicts != 1 {
		t.Fatalf("concurrent password changes successes/conflicts = %d/%d, want 1/1", passwordSuccesses, passwordConflicts)
	}
	if _, err := identityService.Authenticate(ctx, raceLogin.SessionToken); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("pre-change race session error = %v, want invalid credentials", err)
	}
	if _, err := identityService.Login(ctx, raceAccount.Account.Username, raceAccount.InitialPassword); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("pre-change race password error = %v, want invalid credentials", err)
	}
	if _, err := identityService.Login(ctx, raceAccount.Account.Username, passwordWinner.password); err != nil {
		t.Fatalf("winning concurrent password did not log in: %v", err)
	}

	model := catalog.Model{
		ID:                       "openai/gpt-5.2",
		Name:                     "GPT-5.2",
		Provider:                 "OpenAI",
		ContextWindow:            400_000,
		ParameterInfo:            "待官方公开",
		InputModalities:          []string{" Text ", "image", "text"},
		OutputModalities:         []string{"text"},
		SupportsTools:            true,
		SupportsStructuredOutput: true,
		SupportsVision:           true,
		InputPrice:               mustAmount(t, "1.25"),
		OutputPrice:              mustAmount(t, "10.75"),
		CacheWritePrice:          mustAmount(t, "0.5"),
		CacheReadPrice:           mustAmount(t, "0.13"),
		Status:                   catalog.StatusActive,
	}
	createdModel, err := catalogService.Create(ctx, admin, model)
	if err != nil {
		t.Fatalf("create catalog model: %v", err)
	}
	if createdModel.InputPrice.String() != "1.25" || createdModel.OutputPrice.String() != "10.75" || len(createdModel.InputModalities) != 2 {
		t.Fatalf("created model = %+v, want exact prices and normalized modalities", createdModel)
	}
	var internalID string
	if err := pool.QueryRow(ctx, `SELECT internal_id::text FROM models WHERE id = $1`, model.ID).Scan(&internalID); err != nil || internalID == "" {
		t.Fatalf("stable internal model ID = %q, err = %v", internalID, err)
	}
	publicModels, err := catalogService.ListPublic(ctx, "gpt")
	if err != nil || len(publicModels) != 1 {
		t.Fatalf("public model list = %+v, err = %v", publicModels, err)
	}
	publicModelListRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/models?q=gpt", nil)
	publicModelListRequest.AddCookie(memberReadyCookie)
	publicModelListResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicModelListResponse, publicModelListRequest)
	if publicModelListResponse.Code != http.StatusOK || !strings.Contains(publicModelListResponse.Body.String(), "openai/gpt-5.2") {
		t.Fatalf("ordinary account public model list = %d %s", publicModelListResponse.Code, publicModelListResponse.Body.String())
	}
	publicModelDetailRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/models/openai/gpt-5.2", nil)
	publicModelDetailRequest.AddCookie(memberReadyCookie)
	publicModelDetailResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicModelDetailResponse, publicModelDetailRequest)
	if publicModelDetailResponse.Code != http.StatusOK || !strings.Contains(publicModelDetailResponse.Body.String(), "GPT-5.2") {
		t.Fatalf("ordinary account public model detail = %d %s", publicModelDetailResponse.Code, publicModelDetailResponse.Body.String())
	}
	metadataModel := createdModel
	metadataModel.Name = "GPT-5.2 Updated"
	metadataUpdated, err := catalogService.Update(ctx, admin, metadataModel.ID, createdModel.Version, metadataModel)
	if err != nil {
		t.Fatalf("update non-price model metadata: %v", err)
	}
	if !metadataUpdated.PriceUpdatedAt.Equal(createdModel.PriceUpdatedAt) {
		t.Fatalf("non-price update changed price timestamp from %s to %s", createdModel.PriceUpdatedAt, metadataUpdated.PriceUpdatedAt)
	}
	winningModel := metadataUpdated
	winningModel.ParameterInfo = "最新管理员编辑"
	winningModel, err = catalogService.Update(ctx, admin, winningModel.ID, metadataUpdated.Version, winningModel)
	if err != nil {
		t.Fatalf("update model with current version: %v", err)
	}
	staleModel := metadataUpdated
	staleModel.ParameterInfo = "过期管理员编辑"
	if _, err := catalogService.Update(ctx, admin, staleModel.ID, metadataUpdated.Version, staleModel); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("stale model update error = %v, want conflict", err)
	}
	createdModel = winningModel
	createdModel.Status = catalog.StatusDisabled
	createdModel.InputPrice = mustAmount(t, "1.35")
	updatedModel, err := catalogService.Update(ctx, admin, createdModel.ID, createdModel.Version, createdModel)
	if err != nil {
		t.Fatalf("update catalog model: %v", err)
	}
	if updatedModel.InputPrice.String() != "1.35" || !updatedModel.PriceUpdatedAt.After(createdModel.PriceUpdatedAt) {
		t.Fatalf("updated model = %+v, want exact changed price and timestamp", updatedModel)
	}
	if _, err := catalogService.GetPublic(ctx, model.ID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("disabled public model error = %v, want not found", err)
	}
	adminModels, err := catalogService.ListAdmin(ctx, admin, "")
	if err != nil || len(adminModels) != 2 {
		t.Fatalf("administrator model list = %+v, err = %v", adminModels, err)
	}
	if _, err := catalogService.GetAdmin(ctx, admin, model.ID); err != nil {
		t.Fatalf("administrator could not read disabled model history: %v", err)
	}

	backupAdmin, err := identityService.CreateInvitedAccount(ctx, admin, "backup.admin", "备用管理员", 0, true, identity.StatusActive)
	if err != nil {
		t.Fatalf("create backup administrator: %v", err)
	}
	falseValue := false
	adminResults := make(chan error, 2)
	go func() {
		_, err := identityService.UpdateAccount(ctx, admin, backupAdmin.Account.ID, identity.AccountUpdate{
			ExpectedVersion: backupAdmin.Account.Version,
			IsAdmin:         &falseValue,
		})
		adminResults <- err
	}()
	go func() {
		_, err := identityService.UpdateAccount(ctx, backupAdmin.Account, admin.ID, identity.AccountUpdate{
			ExpectedVersion: admin.Version,
			IsAdmin:         &falseValue,
		})
		adminResults <- err
	}()
	var adminSuccesses, adminConflicts int
	for range 2 {
		err := <-adminResults
		if err == nil {
			adminSuccesses++
		} else if errors.Is(err, identity.ErrConflict) {
			adminConflicts++
		} else {
			t.Fatalf("concurrent administrator demotion error = %v", err)
		}
	}
	if adminSuccesses != 1 || adminConflicts != 1 {
		t.Fatalf("concurrent administrator demotions successes/conflicts = %d/%d, want 1/1", adminSuccesses, adminConflicts)
	}
	var activeAdministrators int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE is_admin AND status = 'active'`).Scan(&activeAdministrators); err != nil || activeAdministrators != 1 {
		t.Fatalf("active administrator count = %d, err = %v, want 1", activeAdministrators, err)
	}

	var auditEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&auditEvents); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if auditEvents < 12 {
		t.Fatalf("audit event count = %d, want at least 12", auditEvents)
	}
	var auditActor, auditTarget, auditReason, auditDetails string
	if err := pool.QueryRow(ctx, `
		SELECT actor_account_id::text, target_id, reason, details::text
		FROM audit_events
		WHERE action = 'account.created' AND target_id = $1
		ORDER BY id DESC LIMIT 1`, createdPayload.Account.ID).Scan(&auditActor, &auditTarget, &auditReason, &auditDetails); err != nil {
		t.Fatalf("read account creation audit: %v", err)
	}
	if auditActor != admin.ID || auditTarget != createdPayload.Account.ID || auditReason == "" || !strings.Contains(auditDetails, `"username": "member.web"`) {
		t.Fatalf("account creation audit = actor %q target %q reason %q details %q", auditActor, auditTarget, auditReason, auditDetails)
	}
	if err := pool.QueryRow(ctx, `
		SELECT actor_account_id::text, target_id, reason, details::text
		FROM audit_events
		WHERE action = 'account.password_changed' AND target_id = $1
		ORDER BY id DESC LIMIT 1`, createdPayload.Account.ID).Scan(&auditActor, &auditTarget, &auditReason, &auditDetails); err != nil {
		t.Fatalf("read password change audit: %v", err)
	}
	if auditActor != createdPayload.Account.ID || auditReason == "" || !strings.Contains(auditDetails, `"password_version": 2`) {
		t.Fatalf("password change audit = actor %q target %q reason %q details %q", auditActor, auditTarget, auditReason, auditDetails)
	}
	if err := pool.QueryRow(ctx, `
		SELECT actor_account_id::text, target_id, reason, details::text
		FROM audit_events
		WHERE action = 'model.updated' AND target_id = $1
		ORDER BY id DESC LIMIT 1`, updatedModel.ID).Scan(&auditActor, &auditTarget, &auditReason, &auditDetails); err != nil {
		t.Fatalf("read model update audit: %v", err)
	}
	if auditActor != admin.ID || auditTarget != updatedModel.ID || auditReason == "" || !strings.Contains(auditDetails, `"version": 4`) {
		t.Fatalf("model update audit = actor %q target %q reason %q details %q", auditActor, auditTarget, auditReason, auditDetails)
	}
	if err := pool.QueryRow(ctx, `
		SELECT actor_account_id::text, target_id, reason, details::text
		FROM audit_events
		WHERE action = 'account.updated' AND target_id = $1
		ORDER BY id DESC LIMIT 1`, createdPayload.Account.ID).Scan(&auditActor, &auditTarget, &auditReason, &auditDetails); err != nil {
		t.Fatalf("read account update audit: %v", err)
	}
	if auditActor != admin.ID || auditTarget != createdPayload.Account.ID || auditReason == "" || !strings.Contains(auditDetails, `"is_admin": false`) {
		t.Fatalf("account update audit = actor %q target %q reason %q details %q", auditActor, auditTarget, auditReason, auditDetails)
	}
	auditRows, err := pool.Query(ctx, `SELECT details::text FROM audit_events`)
	if err != nil {
		t.Fatalf("read audit details for credential check: %v", err)
	}
	defer auditRows.Close()
	knownSecrets := []string{
		"Bootstrap-password-2026",
		"A-new-administrator-password-2026",
		"Member-web-password-2026",
		"Password-race-winner-one",
		"Password-race-winner-two",
		createdPayload.InitialPassword,
		created.InitialPassword,
		disabledInvite.InitialPassword,
		raceAccount.InitialPassword,
		"$argon2id$",
	}
	for auditRows.Next() {
		var details string
		if err := auditRows.Scan(&details); err != nil {
			t.Fatalf("scan audit details: %v", err)
		}
		for _, secret := range knownSecrets {
			if secret != "" && strings.Contains(details, secret) {
				t.Fatalf("audit details leaked a known credential marker: %q", details)
			}
		}
	}
	if err := auditRows.Err(); err != nil {
		t.Fatalf("iterate audit details: %v", err)
	}
}

func createExactlyOneBootstrapAdmin(t *testing.T, ctx context.Context, store *storepg.Store) identity.Account {
	t.Helper()
	passwordHash, err := identity.HashPassword("Bootstrap-password-2026")
	if err != nil {
		t.Fatalf("hash bootstrap password: %v", err)
	}

	type result struct {
		account identity.Account
		err     error
	}
	results := make(chan result, 2)
	var group sync.WaitGroup
	for _, username := range []string{"admin.one", "admin.two"} {
		group.Add(1)
		go func(username string) {
			defer group.Done()
			account, err := store.CreateBootstrapAdmin(ctx, identity.NewAccount{
				Username:           username,
				DisplayName:        "初始管理员",
				PasswordHash:       passwordHash,
				IsAdmin:            true,
				Status:             identity.StatusActive,
				MustChangePassword: true,
			})
			results <- result{account: account, err: err}
		}(username)
	}
	group.Wait()
	close(results)

	var winner identity.Account
	var successes, conflicts int
	for result := range results {
		switch {
		case result.err == nil:
			winner = result.account
			successes++
		case errors.Is(result.err, identity.ErrConflict):
			conflicts++
		default:
			t.Fatalf("bootstrap race returned unexpected error: %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("bootstrap race successes/conflicts = %d/%d, want 1/1", successes, conflicts)
	}
	return winner
}

func withSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func randomHex(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generate test schema suffix: %v", err)
	}
	return hex.EncodeToString(value)
}

func mustAmount(t *testing.T, value string) money.Amount {
	t.Helper()
	amount, err := money.Parse(value)
	if err != nil {
		t.Fatalf("parse amount %q: %v", value, err)
	}
	return amount
}

func jsonRequest(t *testing.T, method, target string, payload any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode request payload: %v", err)
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func loginCookie(t *testing.T, handler http.Handler, username, password string) *http.Cookie {
	t.Helper()
	request := jsonRequest(t, http.MethodPost, "https://hub.example/api/auth/login", map[string]string{
		"username": username,
		"password": password,
	})
	request.Header.Set("Origin", "https://hub.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login %q = %d %s", username, response.Code, response.Body.String())
	}
	return responseCookie(t, response)
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("response cookies = %+v, want exactly one", cookies)
	}
	return cookies[0]
}
