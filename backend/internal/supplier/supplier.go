// Package supplier provides bounded icon validation and retrieval for market contributions.
package supplier

import (
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"net/url"
)

var ErrInvalid = errors.New("invalid supplier icon or website")

// Service fetches icons through the configured fixed-egress policy.
type Service struct{ Outbound channel.Outbound }

func WebURL(raw string, optional bool) bool {
	if raw == "" {
		return optional
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && len(raw) <= 2048
}
