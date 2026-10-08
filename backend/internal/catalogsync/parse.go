// Package catalogsync imports the public Bifrost datasheet into the local catalog.
package catalogsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const SourceURL = "https://getbifrost.ai/datasheet"
const MaxBytes = 32 << 20

type Record map[string]json.RawMessage

func (r Record) Text(key string) string { var v string; _ = json.Unmarshal(r[key], &v); return v }
func (r Record) Bool(key string) bool   { var v bool; _ = json.Unmarshal(r[key], &v); return v }

type Entry struct {
	Key      string
	Raw      json.RawMessage
	Model    catalog.Model
	Problems []string
	Warnings []string
}

var legalID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)

// ModelID preserves the complete last source-key component, including versions.
func ModelID(key string) string { return key[strings.LastIndex(key, "/")+1:] }

// Decode requires a complete nonempty object; a truncated fetch must never mark records missing.
func Decode(data []byte, rate string) ([]Entry, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("datasheet exceeds size limit")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("invalid datasheet object")
	}
	seen := map[string]bool{}
	entries := []Entry{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := tok.(string)
		if key == "" || seen[key] {
			return nil, fmt.Errorf("duplicate or empty source key")
		}
		seen[key] = true
		var raw json.RawMessage
		if err = dec.Decode(&raw); err != nil {
			return nil, err
		}
		var r Record
		if json.Unmarshal(raw, &r) != nil || r == nil {
			return nil, fmt.Errorf("invalid record %s", key)
		}
		if r.Text("mode") != "chat" && r.Text("mode") != "responses" {
			continue
		}
		entries = append(entries, Parse(key, raw, rate))
	}
	if _, err = dec.Token(); err != nil {
		return nil, err
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing datasheet content")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("datasheet has no chat/Responses models")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return entries, nil
}

// Price converts USD/token into nano-points/million with exact decimal arithmetic,
// rounding upwards once at the storage boundary (never binary float arithmetic).
var numberPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]{0,20})(?:\.[0-9]{1,24})?(?:[eE][+-]?[0-9]{1,2})?$`)

func Price(raw json.RawMessage, rate string) (money.Amount, error) {
	if len(raw) > 64 || len(rate) > 32 || !numberPattern.Match(raw) {
		return 0, fmt.Errorf("invalid bounded decimal")
	}
	r, ok := new(big.Rat).SetString(rate)
	if !ok || r.Sign() <= 0 {
		return 0, fmt.Errorf("exchange rate not configured")
	}
	n, ok := new(big.Rat).SetString(string(raw))
	if !ok || n.Sign() < 0 {
		return 0, fmt.Errorf("invalid token price")
	}
	n.Mul(n, r).Mul(n, big.NewRat(1_000_000_000_000_000, 1))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(n.Num(), n.Denom(), rem)
	if rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() || q.Int64() > int64(catalog.MaxPriceNanoPerMillion) {
		return 0, fmt.Errorf("token price exceeds catalog limit")
	}
	return money.Amount(q.Int64()), nil
}

var priceFields = map[string]string{"input_cost_per_token": "input", "output_cost_per_token": "output", "cache_creation_input_token_cost": "cache_write", "cache_read_input_token_cost": "cache_read", "input_cost_per_token_cache_hit": "cache_read", "cache_creation_input_token_cost_above_1hr": "cache_write_1h", "input_cost_per_text_token": "input_text", "input_cost_per_image_token": "input_image", "input_cost_per_audio_token": "input_audio", "input_cost_per_video_token": "input_video", "output_cost_per_text_token": "output_text", "output_cost_per_image_token": "output_image", "output_cost_per_audio_token": "output_audio", "output_cost_per_video_token": "output_video", "cache_read_input_audio_token_cost": "cache_read_audio", "cache_read_input_image_token_cost": "cache_read_image"}
var above = regexp.MustCompile(`_above_([0-9]+)k_tokens`)

type condition struct {
	threshold int64
	tier      string
}

func Parse(key string, raw json.RawMessage, rate string) Entry {
	e := Entry{Key: key, Raw: raw, Problems: []string{}, Warnings: []string{}}
	var r Record
	_ = json.Unmarshal(raw, &r)
	waitingRate := rate == ""
	if waitingRate {
		rate = "1"
	}
	e.Model = catalog.Model{ID: ModelID(key), DisplayName: key, Provider: r.Text("provider"), InputModalities: []string{"text"}, OutputModalities: []string{"text"}, SupportsTools: r.Bool("supports_function_calling"), SupportsStructuredOutput: r.Bool("supports_response_schema") || r.Bool("supports_native_structured_output"), SupportsVision: r.Bool("supports_vision") || r.Bool("supports_image_input")}
	if len(e.Model.Provider) > 64 {
		e.Problems = append(e.Problems, "provider exceeds 64 characters")
		e.Model.Provider = ""
	}
	for field, mod := range map[string]string{"supports_vision": "image", "supports_image_input": "image", "supports_audio_input": "audio", "supports_video_input": "video", "supports_pdf_input": "file"} {
		if r.Bool(field) {
			e.Model.InputModalities = append(e.Model.InputModalities, mod)
		}
	}
	if r.Bool("supports_audio_output") {
		e.Model.OutputModalities = append(e.Model.OutputModalities, "audio")
	}
	for field, target := range map[string]*[]string{"supported_modalities": &e.Model.InputModalities, "supported_output_modalities": &e.Model.OutputModalities} {
		var v []string
		if json.Unmarshal(r[field], &v) == nil && len(v) > 0 {
			*target = v
		}
	}
	groups := map[condition]map[string]money.Amount{}
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, field := range keys {
		if field == "off_peak_pricing" && r["off_peak_cost_multiplier"] == nil && r["peak_hours"] == nil {
			continue
		}
		if !(strings.Contains(field, "cost") || field == "tiered_pricing" || field == "off_peak_pricing" || field == "peak_hours" || strings.Contains(field, "uplift_multiplier")) {
			continue
		}
		if strings.Contains(field, "batches") {
			e.Warnings = append(e.Warnings, field+": Batch 未覆盖")
			continue
		}
		stem := field
		c := condition{}
		if match := above.FindStringSubmatch(stem); match != nil {
			n, err := strconv.ParseInt(match[1], 10, 64)
			if err != nil || n <= 0 || n > 1_000_000_000 {
				e.Problems = append(e.Problems, field+": invalid threshold")
				continue
			}
			c.threshold = n*1000 + 1
			stem = above.ReplaceAllString(stem, "")
		}
		for _, tier := range []string{"priority", "flex"} {
			if strings.HasSuffix(stem, "_"+tier) {
				c.tier = tier
				stem = strings.TrimSuffix(stem, "_"+tier)
			}
		}
		bucket, known := priceFields[stem]
		if !known {
			if strings.Contains(field, "token") || field == "tiered_pricing" || strings.Contains(field, "multiplier") || field == "off_peak_pricing" || field == "peak_hours" {
				e.Problems = append(e.Problems, field+": 需要人工处理 token 定价条件")
			} else {
				e.Warnings = append(e.Warnings, field+": 附加或非 token 费用未覆盖")
			}
			continue
		}
		if c.tier != "" && r.Text("provider") != "openai" && r.Text("provider") != "anthropic" && r.Text("provider") != "gemini" {
			e.Problems = append(e.Problems, field+": 提供方服务档语义未确认")
			continue
		}
		p, err := Price(r[field], rate)
		if err != nil {
			e.Problems = append(e.Problems, field+": "+err.Error())
			continue
		}
		if groups[c] == nil {
			groups[c] = map[string]money.Amount{}
		}
		if prior, ok := groups[c][bucket]; ok && prior != p {
			e.Problems = append(e.Problems, field+": conflicting prices")
		}
		groups[c][bucket] = p
	}
	base := groups[condition{}]
	for _, b := range []string{"input", "output"} {
		if _, ok := base[b]; !ok {
			e.Problems = append(e.Problems, b+": 缺少有效基础价")
		}
	}
	// Bifrost's cache rate fallback is the corresponding tier's input rate.
	if _, ok := base["cache_read"]; !ok {
		e.Warnings = append(e.Warnings, "缓存读缺价：继承对应输入价")
	}
	if _, ok := base["cache_write"]; !ok {
		e.Warnings = append(e.Warnings, "缓存写缺价：继承对应输入价")
	}
	apply := func(values map[string]money.Amount) ledger.Prices {
		p := ledger.Prices{InputPerMillion: values["input"], OutputPerMillion: values["output"], CacheWritePerMillion: values["input"], CacheReadPerMillion: values["input"], TokenPrices: map[string]money.Amount{}}
		for k, v := range values {
			switch k {
			case "cache_read":
				p.CacheReadPerMillion = v
			case "cache_write":
				p.CacheWritePerMillion = v
			case "input", "output":
			default:
				p.TokenPrices[k] = v
			}
		}
		if _, ok := p.TokenPrices["cache_write_1h"]; ok {
			p.TokenPrices["cache_write_5m"] = p.CacheWritePerMillion
		}
		return p
	}
	merge := func(dst, src map[string]money.Amount) {
		for k, v := range src {
			dst[k] = v
		}
	}
	conditions := []condition{}
	for c := range groups {
		if c != (condition{}) {
			conditions = append(conditions, c)
		}
	}
	// Include service/threshold intersections so first-match selection doesn't discard either dimension.
	for c := range groups {
		if c.tier != "" {
			for t := range groups {
				if t.threshold > 0 {
					x := condition{threshold: t.threshold, tier: c.tier}
					if _, ok := groups[x]; !ok {
						e.Problems = append(e.Problems, "服务档与输入阈值缺少明确交叉价格")
						groups[x] = map[string]money.Amount{}
						conditions = append(conditions, x)
					}
				}
			}
		}
	}
	// If two condition dimensions both override a bucket, a partial intersection
	// cannot resolve the precedence for that bucket. Require an explicit cross rate.
	for c, values := range groups {
		if c.tier == "" && c.threshold > 0 {
			for service, flat := range groups {
				if service.tier != "" && service.threshold == 0 {
					cross := groups[condition{threshold: c.threshold, tier: service.tier}]
					for bucket, generic := range values {
						if specific, ok := flat[bucket]; ok && specific != generic {
							if _, ok := cross[bucket]; !ok {
								e.Problems = append(e.Problems, "交叉价格缺少 "+bucket+" 明确值")
							}
						}
					}
				}
			}
		}
	}
	sort.Slice(conditions, func(i, j int) bool {
		if conditions[i].tier != conditions[j].tier {
			return conditions[i].tier > conditions[j].tier
		}
		return conditions[i].threshold > conditions[j].threshold
	})
	for _, c := range conditions {
		v := map[string]money.Amount{}
		merge(v, base)
		// Apply progressively increasing generic thresholds, then service and service thresholds.
		thresholds := []int64{0}
		for t := range groups {
			if t.tier == "" && t.threshold > 0 && t.threshold <= c.threshold {
				thresholds = append(thresholds, t.threshold)
			}
		}
		sort.Slice(thresholds, func(i, j int) bool { return thresholds[i] < thresholds[j] })
		for _, n := range thresholds {
			merge(v, groups[condition{threshold: n}])
		}
		if c.tier != "" {
			merge(v, groups[condition{tier: c.tier}])
			ns := []int64{}
			for t := range groups {
				if t.tier == c.tier && t.threshold > 0 && t.threshold <= c.threshold {
					ns = append(ns, t.threshold)
				}
			}
			sort.Slice(ns, func(i, j int) bool { return ns[i] < ns[j] })
			for _, n := range ns {
				merge(v, groups[condition{threshold: n, tier: c.tier}])
			}
		}
		p := apply(v)
		name := "Bifrost " + c.tier
		if c.threshold > 0 {
			name += fmt.Sprintf(" 输入 > %d", c.threshold-1)
		}
		t := ledger.PriceTier{Name: name, Timezone: "UTC", InputPrice: p.InputPerMillion, OutputPrice: p.OutputPerMillion, CacheWritePrice: p.CacheWritePerMillion, CacheReadPrice: p.CacheReadPerMillion, TokenPrices: p.TokenPrices}
		if c.threshold > 0 {
			n := c.threshold
			t.MinPromptTokens = &n
		}
		if c.tier != "" {
			t.ServiceTier = r.Text("provider") + ":" + c.tier
			if r.Text("provider") == "gemini" {
				t.ServiceTier = "gemini:" + strings.ToUpper(c.tier)
			}
		}
		e.Model.PriceTiers = append(e.Model.PriceTiers, t)
	}
	if r["off_peak_pricing"] != nil && r["off_peak_cost_multiplier"] == nil && r["peak_hours"] == nil {
		applyOffPeak(&e, r, rate)
	}
	p := apply(base)
	e.Model.InputPrice = p.InputPerMillion
	e.Model.OutputPrice = p.OutputPerMillion
	e.Model.CacheWritePrice = p.CacheWritePerMillion
	e.Model.CacheReadPrice = p.CacheReadPerMillion
	e.Model.TokenPrices = p.TokenPrices
	e.Model = catalog.Normalize(e.Model)
	if catalog.Validate(e.Model) != nil {
		e.Problems = append(e.Problems, "超出本地目录字段或最多 16 个价格档限制")
	}
	if waitingRate {
		e.Problems = []string{"尚未配置美元到积分换算率"}
	}
	sort.Strings(e.Problems)
	e.Problems = slices.Compact(e.Problems)
	sort.Strings(e.Warnings)
	e.Warnings = slices.Compact(e.Warnings)
	return e
}

// Explicit UTC off-peak schedules are supported only when no other condition
// needs an ambiguous Cartesian product. Conflicting multiplier definitions stay review-only.
func applyOffPeak(e *Entry, r Record, rate string) {
	if len(e.Model.PriceTiers) > 0 {
		e.Problems = append(e.Problems, "峰谷与其他条件交叉需人工处理")
		return
	}
	var off Record
	if json.Unmarshal(r["off_peak_pricing"], &off) != nil {
		e.Problems = append(e.Problems, "无效峰谷价格")
		return
	}
	var windows []struct {
		Hours    json.RawMessage `json:"hours_utc"`
		Weekdays []int           `json:"weekdays"`
	}
	if json.Unmarshal(off["windows"], &windows) != nil || len(windows) == 0 {
		e.Problems = append(e.Problems, "缺少明确UTC峰谷时间窗")
		return
	}
	merged := Record{}
	for k, v := range r {
		if k != "off_peak_pricing" {
			merged[k] = v
		}
	}
	for k, v := range off {
		if k == "windows" {
			continue
		}
		if _, ok := priceFields[k]; !ok {
			e.Problems = append(e.Problems, "无法解释峰谷字段 "+k)
			return
		}
		merged[k] = v
	}
	if off["cache_read_input_token_cost"] != nil {
		delete(merged, "input_cost_per_token_cache_hit")
	}
	if off["input_cost_per_token_cache_hit"] != nil {
		delete(merged, "cache_read_input_token_cost")
	}
	raw, _ := json.Marshal(merged)
	p := Parse(e.Key, raw, rate)
	if len(p.Problems) > 0 {
		e.Problems = append(e.Problems, p.Problems...)
		return
	}
	for _, w := range windows {
		var hours []string
		if json.Unmarshal(w.Hours, &hours) != nil {
			var one string
			if json.Unmarshal(w.Hours, &one) != nil {
				e.Problems = append(e.Problems, "无效峰谷小时")
				return
			}
			hours = []string{one}
		}
		for _, h := range hours {
			parts := strings.Split(h, "-")
			if len(parts) != 2 {
				e.Problems = append(e.Problems, "无效峰谷时间段")
				return
			}
			parse := func(x string) (int16, bool) {
				t, err := time.Parse("15:04", x)
				if err != nil {
					return 0, false
				}
				return int16(t.Hour()*60 + t.Minute()), true
			}
			start, ok := parse(parts[0])
			end, ok2 := parse(parts[1])
			if !ok || !ok2 {
				e.Problems = append(e.Problems, "无效峰谷时间段")
				return
			}
			if end == 0 {
				end = 1440
			}
			if start == end {
				e.Problems = append(e.Problems, "空峰谷时间段")
				return
			}
			e.Model.PriceTiers = append(e.Model.PriceTiers, ledger.PriceTier{Name: "Bifrost 谷价", Timezone: "UTC", Weekdays: w.Weekdays, StartMinute: &start, EndMinute: &end, InputPrice: p.Model.InputPrice, OutputPrice: p.Model.OutputPrice, CacheWritePrice: p.Model.CacheWritePrice, CacheReadPrice: p.Model.CacheReadPrice, TokenPrices: p.Model.TokenPrices})
		}
	}
}
