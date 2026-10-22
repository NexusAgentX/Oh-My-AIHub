package market

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"golang.org/x/net/http/httpguts"
)

const MaxPrice money.Amount = 100000 * money.Amount(money.Scale)

var tierPattern = regexp.MustCompile(`^(openai|anthropic|gemini):[A-Za-z0-9_-]{1,48}$`)

func webURL(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	u, err := url.Parse(value)
	return err == nil && len(value) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}
func ValidateSupplier(v Supplier) error {
	if strings.TrimSpace(v.Name) == "" || utf8.RuneCountInString(v.Name) > 64 || !slices.Contains([]string{"official_api", "official_subscription", "relay"}, v.Kind) ||
		!webURL(v.WebsiteURL, false) || !webURL(v.DocsURL, true) || !webURL(v.IconSource, true) || utf8.RuneCountInString(v.Summary) > 160 || utf8.RuneCountInString(v.Description) > 8000 || len(v.IconData) > 350000 {
		return ErrInvalid
	}
	return nil
}

// Icon payloads still pass the existing supplier icon normalizer at the HTTP
// boundary. The repository does not introduce a second image fetching service.
func ValidateGroup(v Group) error {
	if v.SupplierID == "" || strings.TrimSpace(v.Name) == "" || utf8.RuneCountInString(v.Name) > 64 || utf8.RuneCountInString(v.Description) > 8000 || !webURL(v.StatusURL, true) {
		return ErrInvalid
	}
	return ValidateDefaults(v.Defaults)
}

func ValidateDefaults(v Defaults) error {
	inRange := func(p *int32, low, high int32) bool { return p == nil || (*p >= low && *p <= high) }
	control := func(s string) bool { return strings.ContainsFunc(s, unicode.IsControl) }
	if v.UserAgent != nil && (*v.UserAgent == "" || utf8.RuneCountInString(*v.UserAgent) > 512 || control(*v.UserAgent)) {
		return ErrInvalid
	}
	if !inRange(v.ConcurrencyLimit, 1, 1<<30) || !inRange(v.RPMLimit, 1, 1<<30) || !inRange(v.TTFTTimeoutMS, 1000, 600000) || !inRange(v.RequestTimeoutMS, 1000, 3600000) || !inRange(v.IdleTimeoutMS, 1000, 3600000) || !inRange(v.CooldownFailures, 1, 100) || !inRange(v.CooldownSeconds, 10, 86400) {
		return ErrInvalid
	}
	if v.HeaderRules == nil {
		return nil
	}
	if len(v.HeaderRules.Set) > 32 || len(v.HeaderRules.Remove) > 32 {
		return ErrInvalid
	}
	forbidden := []string{"host", "content-length", "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "authorization", "x-api-key", "x-goog-api-key", "cookie", "accept-encoding"}
	valid := func(name string) bool {
		return len(name) <= 64 && httpguts.ValidHeaderFieldName(name) && !slices.Contains(forbidden, strings.ToLower(name))
	}
	for _, r := range v.HeaderRules.Set {
		if !valid(r.Name) || utf8.RuneCountInString(r.Value) > 1024 || control(r.Value) {
			return ErrInvalid
		}
	}
	for _, name := range v.HeaderRules.Remove {
		if !valid(name) {
			return ErrInvalid
		}
	}
	return nil
}

func ValidateGroupModel(v GroupModel) error {
	if v.GroupID == "" || !gatewaycontract.ValidModelID(v.ModelID) || !gatewaycontract.ValidModelID(v.UpstreamModel) || strings.TrimSpace(v.DisplayName) == "" || utf8.RuneCountInString(v.DisplayName) > 128 || !slices.Contains([]string{"text", "image"}, v.ModelType) || utf8.RuneCountInString(v.Notes) > 2000 || (v.ContextWindow != nil && *v.ContextWindow <= 0) || (v.MaxOutputTokens != nil && *v.MaxOutputTokens <= 0) {
		return ErrInvalid
	}
	return nil
}

func validPrices(p TokenPrices) bool {
	for _, v := range []money.Amount{p.Input, p.Output, p.CacheWrite, p.CacheRead} {
		if v < 0 || v > MaxPrice {
			return false
		}
	}
	if len(p.Details) > 16 {
		return false
	}
	for k, v := range p.Details {
		if ledger.TokenBucket(k) < 0 || v < 0 || v > MaxPrice {
			return false
		}
	}
	return true
}
func ValidateQuote(q Quote) error {
	if q.GroupModelID == "" || !ledger.ValidCurrency(q.Currency) || !slices.Contains([]string{"per_million_tokens", "per_request", "per_successful_image"}, q.BillingUnit) || !validPrices(q.Prices) || len(q.Tiers) > 16 || len(q.UnitPrices) > 128 {
		return ErrInvalid
	}
	for _, t := range q.Tiers {
		c := t.Conditions
		if !validPrices(t.Prices) || utf8.RuneCountInString(t.Name) > 64 || !t.Ledger().HasPredicate() || (c.ServiceTier != "" && (!tierPattern.MatchString(c.ServiceTier) || c.ServiceTier == "openai:priority")) ||
			(c.MinPromptTokens != nil && *c.MinPromptTokens < 0) || (c.MaxPromptTokens != nil && *c.MaxPromptTokens < 0) || (c.MinPromptTokens != nil && c.MaxPromptTokens != nil && *c.MinPromptTokens >= *c.MaxPromptTokens) || (c.StartMinute == nil) != (c.EndMinute == nil) ||
			(c.StartMinute != nil && (*c.StartMinute < 0 || *c.StartMinute > 1439 || *c.EndMinute < 1 || *c.EndMinute > 1440 || *c.StartMinute == *c.EndMinute)) {
			return ErrInvalid
		}
		if len(c.Weekdays) > 7 {
			return ErrInvalid
		}
		seenDays := map[int]bool{}
		for _, day := range c.Weekdays {
			if day < 1 || day > 7 || seenDays[day] {
				return ErrInvalid
			}
			seenDays[day] = true
		}
		if (c.StartMinute != nil || len(c.Weekdays) > 0) && c.Timezone == "" {
			return ErrInvalid
		}
		if c.Timezone != "" {
			if _, err := time.LoadLocation(c.Timezone); err != nil {
				return ErrInvalid
			}
		}
	}
	baseUnit := false
	for _, p := range q.UnitPrices {
		if !slices.Contains([]string{"request", "successful_image", "tool_call", "tool_session", "tool_second", "tool_query"}, p.Unit) || p.Price < 0 || p.Price > MaxPrice || len(p.Tool) > 128 || len(p.ImageSize) > 64 || len(p.ImageQuality) > 32 || len(p.ImageBackground) > 32 || (p.ServiceTier != "" && (!tierPattern.MatchString(p.ServiceTier) || p.ServiceTier == "openai:priority")) {
			return ErrInvalid
		}
		if strings.HasPrefix(p.Unit, "tool_") && p.Tool == "" {
			return ErrInvalid
		}
		if !strings.HasPrefix(p.Unit, "tool_") && p.Tool != "" {
			return ErrInvalid
		}
		if (q.BillingUnit == "per_request" && p.Unit == "request") || (q.BillingUnit == "per_successful_image" && p.Unit == "successful_image") {
			baseUnit = true
		}
	}
	if q.BillingUnit != "per_million_tokens" && !baseUnit {
		return ErrInvalid
	}
	return nil
}

func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ModelCapabilityDigest covers only what a probe can depend on: the model a
// group offers and the upstream model behind it. Prices, display labels,
// addresses, timeouts, concurrency, a group's UA/headers and its ordering do
// not change a Key's protocol evidence.
func ModelCapabilityDigest(v GroupModel) string {
	return digest(struct {
		Model, Upstream, Type string
		Context, Output       *int64
	}{v.ModelID, v.UpstreamModel, v.ModelType, v.ContextWindow, v.MaxOutputTokens})
}

// Decode rejects unknown fields in revision payloads rather than treating JSON
// as an unvalidated escape hatch. F2 additionally checks operation/ACL/version.
func Decode(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
