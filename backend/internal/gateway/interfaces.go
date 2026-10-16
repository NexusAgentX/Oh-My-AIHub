package gateway

import "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"

// observationFormat shares native usage decoding without conflating independently
// declared upstream endpoint capabilities.
func observationFormat(f channel.Format) channel.Format {
	switch f {
	case channel.FormatOpenAICompact, channel.FormatOpenAICount,
		channel.FormatOpenAIImageGeneration, channel.FormatOpenAIImageEdit, channel.FormatOpenAIImageVariation:
		return channel.FormatOpenAIResponses
	}
	return f
}
func imageFormat(f channel.Format) bool {
	return f == channel.FormatOpenAIImageGeneration || f == channel.FormatOpenAIImageEdit || f == channel.FormatOpenAIImageVariation
}
