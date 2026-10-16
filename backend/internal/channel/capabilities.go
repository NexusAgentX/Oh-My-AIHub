package channel

import "slices"

var ChannelCapabilities = cells(ChannelFeatures, CodexChannelFeatures)
var ModelCapabilities = cells(ModelFeatures, nil)

func ValidCapabilities(values, allowed []string) bool {
	if len(values) > len(allowed) {
		return false
	}
	for i, v := range values {
		if !slices.Contains(allowed, v) || slices.Contains(values[:i], v) {
			return false
		}
	}
	return true
}

// CapabilityList normalizes empty output without modifying snapshot-owned slices.
func CapabilityList(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
