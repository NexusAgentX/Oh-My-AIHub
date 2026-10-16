package gateway

import (
	"bytes"
	"encoding/json"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"math"
	"strconv"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

const maxBillingItems = 128

func validGeo(value string) bool {
	if len(value) == 0 || len(value) > 32 {
		return false
	}
	for _, c := range value {
		if c != '-' && c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func (o *Observer) unit(key string, count int64) {
	if count < 0 || count > math.MaxInt64/ledger.FixedPointScale {
		o.note("工具用量无效，未计价")
		return
	}
	if o.detail.Units == nil {
		o.detail.Units = map[string]int64{}
	}
	o.detail.Units[key] = count * ledger.FixedPointScale
}

func (o *Observer) observeServerTools(usage jsonObject) {
	server := o.objectOf(usage.get("server_tool_use"))
	if n, ok := count(server.get("web_search_requests")); ok {
		o.unit("web_search", n)
	}
	// A request count cannot establish container runtime or allocated memory.
	if n, ok := count(server.get("code_execution_requests")); ok && n > 0 {
		o.note("代码执行已观察，缺少可靠时长，时长费用未计价")
	}
}

func anthropicIterations(items []anthropicFields, parent *anthropicFields) []ledger.UsageIteration {
	result := make([]ledger.UsageIteration, 0, len(items))
	for _, item := range items {
		value := func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return clamp(*p)
		}
		d := &ledger.UsageDetail{ServiceTier: "", InferenceGeo: item.InferenceGeo}
		if item.ServiceTier != "" {
			d.ServiceTier = "anthropic:" + item.ServiceTier
		}
		if item.Speed == "fast" {
			d.ServiceTier = "anthropic:fast"
		}
		if item.Type != "advisor_message" {
			if d.InferenceGeo == "" {
				d.InferenceGeo = parent.InferenceGeo
			}
			if d.ServiceTier == "" && parent.ServiceTier != "" {
				d.ServiceTier = "anthropic:" + parent.ServiceTier
			}
			if parent.Speed == "fast" {
				d.ServiceTier = "anthropic:fast"
			}
		}
		write := value(item.CacheCreation)
		if ttl := item.CacheCreationDetails; ttl != nil && ttl.Five != nil && ttl.Hour != nil && *ttl.Five >= 0 && *ttl.Hour >= 0 && *ttl.Five <= write && *ttl.Hour <= write-*ttl.Five {
			d.Tokens = map[string]int64{"cache_write_5m": *ttl.Five, "cache_write_1h": *ttl.Hour}
		}
		result = append(result, ledger.UsageIteration{Type: item.Type, Model: item.Model, Usage: ledger.Usage{InputTokens: value(item.InputTokens), OutputTokens: value(item.OutputTokens), CacheWriteTokens: write, CacheReadTokens: value(item.CacheReadInputs), Detail: d}})
	}
	return result
}

func (o *Observer) observeResponseItems(raw []byte) {
	// Plain message output cannot contain a billable tool. Escaped member values
	// take the full path so JSON spelling never changes accounting semantics.
	if !bytes.Contains(raw, []byte("_call")) && !bytes.Contains(raw, []byte{92}) {
		return
	}
	eachElement(raw, o.observeResponseItem)
}

func (o *Observer) observeResponseItem(raw []byte) {
	if !json.Valid(raw) {
		return
	}
	o.members = o.members[:0]
	item := o.objectOf(raw)
	kind := textValue(item.get("type"))
	if kind != "web_search_call" && kind != "file_search_call" && kind != "code_interpreter_call" && kind != "image_generation_call" {
		return
	}
	id := textValue(item.get("id"))
	if id == "" || len(id) > 256 {
		o.note("工具结果缺少可靠标识，未完整统计")
		return
	}
	if kind == "image_generation_call" {
		if validObservationID(id) {
			o.observeImageGeneration(item, id, "")
		} else {
			o.note("图像调用标识无效，未完整统计")
		}
		return
	}
	if o.billedItems == nil {
		o.billedItems = map[string]struct{}{}
	}
	if _, exists := o.billedItems[id]; exists {
		return
	}
	if len(o.billedItems) >= maxBillingItems {
		o.note("工具结果超过解析上限，未完整统计")
		return
	}
	o.billedItems[id] = struct{}{}
	switch kind {
	case "web_search_call", "file_search_call":
		key := strings.TrimSuffix(kind, "_call")
		current := o.detail.Units[key] / ledger.FixedPointScale
		o.unit(key, current+1)
	case "code_interpreter_call":
		// Observe each container once, never once per output chunk.
		container := textValue(item.get("container_id"))
		if container == "" {
			o.note("容器规格缺失，未按规格统计")
			return
		}
		marker := "container:" + container
		if _, exists := o.billedItems[marker]; exists {
			return
		}
		if len(o.billedItems) >= maxBillingItems {
			o.note("容器数超过解析上限，未完整计价")
			return
		}
		o.billedItems[marker] = struct{}{}
		memory := textValue(item.get("memory_limit"))
		if memory == "" {
			memory = o.containerMemory
		}

		key := ""
		switch memory {
		case "1g", "1gb":
			key = "container_1gb"
		case "4g", "4gb":
			key = "container_4gb"
		case "16g", "16gb":
			key = "container_16gb"
		case "64g", "64gb":
			key = "container_64gb"
		}
		if key == "" || len(container) > 256 {
			o.note("容器规格缺失，未按规格统计")
			return
		}
		o.unit(key, o.detail.Units[key]/ledger.FixedPointScale+1)

	}
}

func (o *Observer) observeGeminiTools(root, usage jsonObject) {
	n, ok := count(usage.get("toolUsePromptTokenCount"))
	if ok && n >= 0 {
		o.detail.ToolPromptTokens = n
	}
	queries, maps := int64(0), int64(0)
	candidates := root.get("candidates")
	if hasGroundingMetadata(candidates) {
		eachElement(candidates, func(raw []byte) {
			candidate := o.objectOf(raw)
			grounding := o.objectOf(candidate.get("groundingMetadata"))
			eachElement(grounding.get("webSearchQueries"), func([]byte) { queries++ })
			eachElement(grounding.get("groundingChunks"), func(raw []byte) {
				chunk := o.objectOf(raw)
				if len(chunk.get("maps")) > 0 {
					maps++
				}
			})
		})
	}
	if queries > 0 {
		o.unit("google_search_query", queries)
		o.unit("google_search_prompt", 1)
	}
	if maps > 0 {
		// Grounding chunks establish a grounded prompt, not a query count.
		o.note("Maps 检索片段不能确认查询次数")
		o.unit("google_maps_prompt", 1)
	}
	grounded := o.detail.Units["google_search_prompt"] > 0 || o.detail.Units["google_maps_prompt"] > 0
	if grounded && o.geminiToolAdded > 0 {
		o.usage.InputTokens -= o.geminiToolAdded
		o.geminiToolAdded = 0
	}
	if n > 0 && !grounded && o.geminiToolInputBillable {
		// usage summaries are cumulative; absorbGemini replaced the input first.
		if total, valid := sumTokens(o.usage.InputTokens, n); valid {
			o.usage.InputTokens = total
			o.geminiToolAdded = n
			// Tool modality may be unknown: keep it in the generic input remainder.
		} else {
			o.note("工具输入总量无效，未计价")
		}
	} else if n > 0 && !grounded {
		o.note("工具输入来源未确认，未另行计价")
	}
}

func (o *Observer) updateIterationTotals(old, next ledger.Usage) {
	for i := len(o.detail.Iterations) - 1; i >= 0; i-- {
		it := &o.detail.Iterations[i]
		if it.Type != "" && it.Type != "message" {
			continue
		}
		for _, item := range []struct {
			before, after int64
			dest          *int64
		}{{old.InputTokens, next.InputTokens, &it.Usage.InputTokens}, {old.OutputTokens, next.OutputTokens, &it.Usage.OutputTokens}, {old.CacheReadTokens, next.CacheReadTokens, &it.Usage.CacheReadTokens}, {old.CacheWriteTokens, next.CacheWriteTokens, &it.Usage.CacheWriteTokens}} {
			delta := item.after - item.before
			if delta < 0 && *item.dest < -delta || delta > 0 && *item.dest > math.MaxInt64-delta {
				o.note("迭代增量无法可靠分配，保留原轮次用量")
				continue
			}
			*item.dest += delta
		}
		if d := it.Usage.Detail; d != nil {
			if d.Tokens["cache_write_5m"]+d.Tokens["cache_write_1h"] > it.Usage.CacheWriteTokens {
				delete(d.Tokens, "cache_write_5m")
				delete(d.Tokens, "cache_write_1h")
			}
		}
		return
	}
}

func hasGroundingMetadata(raw []byte) bool {
	return bytes.Contains(raw, []byte(`"groundingMetadata"`)) || bytes.Contains(raw, []byte(`\u`))
}

// Only protocol identifiers, enums and dimensions are retained; generated
// content, revised prompts and arbitrary error messages never enter snapshots.
func validObservationID(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, c := range value {
		if c != '_' && c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func observationEnum(item jsonObject, key string, dest *string, values ...string) {
	value := textValue(item.get(key))
	for _, allowed := range values {
		if value == allowed {
			*dest = value
			return
		}
	}
}

func (o *Observer) observeImageGeneration(item jsonObject, id string, eventStatus string) {
	index := -1
	for i := range o.detail.ImageGenerations {
		if o.detail.ImageGenerations[i].ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		if len(o.billedItems) >= maxBillingItems {
			o.note("工具结果超过解析上限，未完整统计")
			return
		}
		if o.billedItems == nil {
			o.billedItems = map[string]struct{}{}
		}
		if _, exists := o.billedItems[id]; exists {
			return
		}
		o.billedItems[id] = struct{}{}
		o.detail.ImageGenerations = append(o.detail.ImageGenerations, ledger.ImageGeneration{ID: id})
		index = len(o.detail.ImageGenerations) - 1
		o.unit("image_generation", o.detail.Units["image_generation"]/ledger.FixedPointScale+1)
		o.note("图像工具仅作观察，暂不收取图像子模型费用")
	}
	image := &o.detail.ImageGenerations[index]
	// Terminal states never regress when a cumulative or repeated frame arrives.
	if image.Status != "completed" && image.Status != "failed" {
		next := textValue(item.get("status"))
		if next == "" {
			next = eventStatus
		}
		if next != "in_progress" || image.Status != "generating" {
			switch next {
			case "in_progress", "generating", "completed", "failed":
				image.Status = next
			}
		}
	}
	observationEnum(item, "quality", &image.Quality, "low", "medium", "high", "xhigh", "max", "auto")
	observationEnum(item, "output_format", &image.OutputFormat, "png", "webp", "jpeg")
	observationEnum(item, "background", &image.Background, "transparent", "opaque", "auto")
	observationEnum(item, "action", &image.Action, "generate", "edit", "auto")
	size := textValue(item.get("size"))
	if len(size) <= 11 {
		w, h, ok := strings.Cut(size, "x")
		width, e1 := strconv.Atoi(w)
		height, e2 := strconv.Atoi(h)
		if ok && e1 == nil && e2 == nil && width > 0 && height > 0 && width <= 65536 && height <= 65536 && strconv.Itoa(width) == w && strconv.Itoa(height) == h {
			image.Size = size
		}
	}
	model := textValue(item.get("model"))
	if catalog.ValidModelID(model) {
		image.Model, image.ModelSource = model, "response"
	} else if image.Model == "" && catalog.ValidModelID(o.imageModel) {
		image.Model, image.ModelSource = o.imageModel, "request"
	}
	if raw := item.get("usage"); len(raw) > 0 {
		child := NewObserver(channel.FormatOpenAIResponses, false, "application/json")
		child.absorb(append(append([]byte(`{"usage":`), raw...), '}'))
		if child.found {
			usage := child.Finish().Usage
			// Repeated summaries may omit previously reported token subdivisions.
			if image.Usage == nil {
				image.Usage = &usage
			} else {
				if usage.InputTokens > image.Usage.InputTokens {
					image.Usage.InputTokens = usage.InputTokens
				}
				if usage.OutputTokens > image.Usage.OutputTokens {
					image.Usage.OutputTokens = usage.OutputTokens
				}
				if usage.CacheReadTokens > image.Usage.CacheReadTokens {
					image.Usage.CacheReadTokens = usage.CacheReadTokens
				}
				if usage.CacheWriteTokens > image.Usage.CacheWriteTokens {
					image.Usage.CacheWriteTokens = usage.CacheWriteTokens
				}
				if usage.Detail != nil && len(usage.Detail.Tokens) > 0 {
					image.Usage.Detail = usage.Detail
				}
			}
		}
	}
}
