package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	recorder := httptest.NewRecorder()

	NewHandler(Dependencies{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("expected JSON response, got %q", contentType)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("expected healthy response, got %q", body)
	}
}

type failingLogoutStore struct {
	identity.Store
	err error
}

type failingAuthenticationStore struct {
	identity.Store
	err error
}

func (s failingAuthenticationStore) FindAccountBySession(context.Context, []byte, time.Time) (identity.Account, error) {
	return identity.Account{}, s.err
}

func (s failingLogoutStore) DeleteSession(context.Context, []byte) error {
	return s.err
}

func TestLogoutPreservesCookieWhenSessionRevocationFails(t *testing.T) {
	service, err := identity.NewService(failingLogoutStore{err: errors.New("database unavailable")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	application := &app{identity: service, cookieName: defaultSessionCookie}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	request.AddCookie(&http.Cookie{
		Name:  defaultSessionCookie,
		Value: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
	})
	recorder := httptest.NewRecorder()

	application.logout(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("logout response = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("logout failure unexpectedly changed cookies: %#v", cookies)
	}
}

func TestSessionLookupFailurePreservesCookieAndReturnsServerError(t *testing.T) {
	service, err := identity.NewService(failingAuthenticationStore{err: errors.New("database unavailable")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	application := &app{identity: service, cookieName: defaultSessionCookie}
	nextCalled := false
	handler := application.requireSession(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/account", nil)
	request.AddCookie(&http.Cookie{
		Name:  defaultSessionCookie,
		Value: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
	})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("session response = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if nextCalled {
		t.Fatal("session middleware called the protected handler after lookup failure")
	}
	if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("session lookup failure unexpectedly changed cookies: %#v", cookies)
	}
}

func TestUnsafeRequestsRequireSameOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://hub.example/api/auth/login", strings.NewReader(`{"username":"user","password":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	NewHandler(Dependencies{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "cross_origin_request") {
		t.Fatalf("response = %d %s, want cross-origin rejection", recorder.Code, recorder.Body.String())
	}
}

func TestDecodeJSONRejectsTrailingContent(t *testing.T) {
	for _, body := range []string{
		`{"value":1} trailing`,
		`{"value":1}{"value":2}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		recorder := httptest.NewRecorder()
		var target struct {
			Value int `json:"value"`
		}
		if err := decodeJSON(recorder, request, &target); err == nil {
			t.Fatalf("decodeJSON(%q) unexpectedly succeeded", body)
		}
	}
}

func TestSameOriginUsesTrustedForwardedScheme(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://hub.example/api/test", nil)
	request.Header.Set("Origin", "https://hub.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()
	application := &app{trustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}}

	application.requireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("forwarded HTTPS same-origin response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestSameOriginIgnoresUntrustedForwardedScheme(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://hub.example/api/test", nil)
	request.RemoteAddr = "198.51.100.20:4321"
	request.Header.Set("Origin", "https://hub.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()
	application := &app{trustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}}

	application.requireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("untrusted forwarded scheme response = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestLoginClientIPOnlyUsesTrustedProxyHeader(t *testing.T) {
	application := &app{trustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}}
	request := httptest.NewRequest(http.MethodGet, "http://hub.example/api/test", nil)
	request.Header.Set("X-Real-IP", "203.0.113.9")

	if got := application.loginClientIP(request); got != "203.0.113.9" {
		t.Fatalf("trusted proxy client IP = %q", got)
	}
	request.RemoteAddr = "198.51.100.20:4321"
	if got := application.loginClientIP(request); got != "198.51.100.20" {
		t.Fatalf("untrusted proxy client IP = %q, want remote address", got)
	}
}

func TestPasswordWorkAdmissionCapsConcurrentArgon2(t *testing.T) {
	loginSlots := make(chan struct{}, 2)
	accountSlots := make(chan struct{}, 2)
	if !acquirePasswordSlot(accountSlots) || !acquirePasswordSlot(accountSlots) {
		t.Fatal("available password work slots were rejected")
	}
	if acquirePasswordSlot(accountSlots) {
		t.Fatal("password work exceeded the configured concurrency limit")
	}
	if !acquirePasswordSlot(loginSlots) {
		t.Fatal("account password work exhausted reserved login capacity")
	}
	<-accountSlots
	if !acquirePasswordSlot(accountSlots) {
		t.Fatal("released password work slot was not reusable")
	}
}

func TestLoginLimiterExpiresFailures(t *testing.T) {
	now := time.Date(2026, time.September, 2, 0, 0, 0, 0, time.UTC)
	limiter := newLoginLimiter(2, time.Minute, 10)
	limiter.now = func() time.Time { return now }

	if !limiter.allowed("key") {
		t.Fatal("fresh key was unexpectedly limited")
	}
	limiter.failure("key")
	limiter.failure("key")
	if limiter.allowed("key") {
		t.Fatal("key remained allowed after reaching the failure limit")
	}
	now = now.Add(time.Minute + time.Second)
	if !limiter.allowed("key") {
		t.Fatal("key remained limited after the window expired")
	}
}

func TestAttemptLimiterCountsEveryAttemptAndExpires(t *testing.T) {
	now := time.Date(2026, time.September, 2, 0, 0, 0, 0, time.UTC)
	limiter := newLoginLimiter(2, time.Minute, 10)
	limiter.now = func() time.Time { return now }

	if !limiter.take("account") || !limiter.take("account") {
		t.Fatal("attempt limiter rejected available capacity")
	}
	if limiter.take("account") {
		t.Fatal("attempt limiter accepted work above its limit")
	}
	now = now.Add(time.Minute + time.Second)
	if !limiter.take("account") {
		t.Fatal("attempt limiter did not restore capacity after the window")
	}
}

func TestCombinedAttemptLimitersDoNotPolluteSubjectAfterIPLimit(t *testing.T) {
	application := &app{
		loginAttempts:     newLoginLimiter(20, time.Minute, 10),
		loginIPAttempts:   newLoginLimiter(1, time.Minute, 10),
		passwordChanges:   newLoginLimiter(8, time.Minute, 10),
		passwordChangeIPs: newLoginLimiter(1, time.Minute, 10),
	}
	if !application.allowLoginAttempt("192.0.2.1", "192.0.2.1\x00first") {
		t.Fatal("first login attempt was unexpectedly rejected")
	}
	if application.allowLoginAttempt("192.0.2.1", "192.0.2.1\x00second") {
		t.Fatal("login attempt exceeded the IP limit")
	}
	if _, polluted := application.loginAttempts.attempts["192.0.2.1\x00second"]; polluted {
		t.Fatal("IP-limited login polluted a second username key")
	}
	if !application.allowPasswordChangeAttempt("192.0.2.2", "account-one") {
		t.Fatal("first password change attempt was unexpectedly rejected")
	}
	if application.allowPasswordChangeAttempt("192.0.2.2", "account-two") {
		t.Fatal("password change attempt exceeded the IP limit")
	}
	if _, polluted := application.passwordChanges.attempts["account-two"]; polluted {
		t.Fatal("IP-limited password change polluted a second account key")
	}
}

func TestParseModelPriceUsesExactNanoPointPrecision(t *testing.T) {
	amount, err := parseModelPrice("0.0375")
	if err != nil || amount.String() != "0.0375" {
		t.Fatalf("parseModelPrice = %s, %v", amount, err)
	}
	for _, value := range []string{"0.0000000001", "100000.000000001", "100000.01"} {
		if _, err := parseModelPrice(value); err == nil {
			t.Fatalf("parseModelPrice(%q) unexpectedly succeeded", value)
		}
	}
}

func TestAccountResponseFreezesAllSpendableCapacity(t *testing.T) {
	response := accountResponse(identity.Account{
		CreditLimit:     money.Amount(10_000_000_000),
		CreditFrozen:    true,
		PostedBalance:   money.Amount(5_000_000_000),
		AssetReserved:   money.Amount(1_000_000_000),
		SpendAuthorized: money.Amount(1_000_000_000),
	})

	if response["effective_credit_limit"] != "0" || response["spendable_capacity"] != "0" {
		t.Fatalf("frozen account response = %+v, want zero effective credit and spendable capacity", response)
	}
}
