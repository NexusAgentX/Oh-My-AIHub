package gateway

import (
	"encoding/json"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

// ObserveNativeImage is shared by the native image endpoints.
// It keeps only counts and reliable usage. Requested n is never a product
// count and no image, URL or revised prompt is retained or fetched.
func ObserveNativeImage(body []byte) (Observation, quotepricing.Facts) {
	if len(body) > maxRequestBytes || !json.Valid(body) {
		return Observation{UsageStatus: "parse_limit"}, quotepricing.Facts{IndependentImage: true}
	}
	o := NewObserver(channel.FormatOpenAIResponses, false, "application/json")
	o.absorb(body)
	observed := o.Finish()
	facts := quotepricing.Facts{Usage: observed.Usage, MainReliable: observed.Found, IndependentImage: true}
	if observed.ProtocolError {
		return observed, facts
	}
	o.members = nil
	root := o.objectOf(body)
	if value := root.get("error"); len(value) > 0 && string(value) != "null" {
		observed.ProtocolError = true
		facts.MainReliable = false
		return observed, facts
	}
	products := int64(0)
	kind := textValue(root.get("type"))
	if (kind == "image_generation.completed" || kind == "image_edit.completed") && len(root.get("b64_json")) > 2 {
		products = 1
	}
	eachElement(root.get("data"), func(raw []byte) {
		item := o.objectOf(raw)
		if len(item.get("error")) > 0 && string(item.get("error")) != "null" {
			return
		}
		nonempty := func(value []byte) bool { return len(value) > 2 && value[0] == '"' }
		if nonempty(item.get("url")) || nonempty(item.get("b64_json")) {
			products++
		}
	})
	if products > 0 {
		tier := ""
		if observed.Usage.Detail != nil {
			tier = observed.Usage.Detail.ServiceTier
		}
		unit := quotepricing.Unit{Unit: "successful_image", QuantityNano: products * ledger.UnitRate, ImageSize: textValue(root.get("size")), ImageQuality: textValue(root.get("quality")), ImageBackground: textValue(root.get("background")), ServiceTier: tier}
		// Restrict retained selectors, not the transparent response itself.
		if len(unit.ImageSize) > 64 {
			unit.ImageSize = ""
		}
		if len(unit.ImageQuality) > 32 {
			unit.ImageQuality = ""
		}
		if len(unit.ImageBackground) > 32 {
			unit.ImageBackground = ""
		}
		facts.Units = []quotepricing.Unit{{Unit: "request", QuantityNano: ledger.UnitRate, ImageSize: unit.ImageSize, ImageQuality: unit.ImageQuality, ImageBackground: unit.ImageBackground, ServiceTier: unit.ServiceTier}, unit}
	}
	return observed, facts
}
