package api

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"strconv"
	"time"
)

// channelEventJSON is the OpenAPI ChannelEvent schema.
type channelEventJSON struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

func newChannelEventsJSON(events []channel.Event) []channelEventJSON {
	items := make([]channelEventJSON, 0, len(events))
	for _, event := range events {
		items = append(items, channelEventJSON{ID: itoa(event.ID), Kind: event.Kind, Reason: event.Reason, CreatedAt: event.CreatedAt})
	}
	return items
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }
