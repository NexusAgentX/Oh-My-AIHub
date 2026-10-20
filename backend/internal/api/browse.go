package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// defaultKeyJSON is the OpenAPI DefaultKey schema.
type defaultKeyJSON struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
}

// homeTodayJSON is the today object of the OpenAPI HomeResponse schema.
type homeTodayJSON struct {
	Spend          string `json:"spend"`
	Calls          int64  `json:"calls"`
	SucceededCalls int64  `json:"succeeded_calls"`
}

// homeChannelsJSON is the channels object of the OpenAPI HomeResponse schema.
type homeChannelsJSON struct {
	TodayRevenue string `json:"today_revenue"`
	Online       int64  `json:"online"`
	Total        int64  `json:"total"`
}

// homeResponseJSON is the OpenAPI HomeResponse schema.
type homeResponseJSON struct {
	Points           pointsSummaryJSON `json:"points"`
	Today            homeTodayJSON     `json:"today"`
	Channels         homeChannelsJSON  `json:"channels"`
	DefaultKey       *defaultKeyJSON   `json:"default_key"`
	PendingC2CTrades int64             `json:"pending_c2c_trades"`
	RecentCalls      []callSummaryJSON `json:"recent_calls"`
}

func ratio(value float64) string { return strconv.FormatFloat(value, 'f', 6, 64) }

// newGatewayCallSummaryJSON builds the CallSummary schema from the gateway's home page rows.
func newGatewayCallSummaryJSON(call gateway.CallSummary) callSummaryJSON {
	var channelRef *channelRefJSON
	if call.ChannelID != nil {
		channelRef = &channelRefJSON{ID: *call.ChannelID}
		if call.ChannelName != nil {
			channelRef.Name = *call.ChannelName
		}
	}
	charged := money.Amount(0)
	if call.Booked {
		charged = money.FromNano((call.Cost + call.Fee).Nano())
	}
	return callSummaryJSON{
		ID:             call.ID,
		CreatedAt:      call.CreatedAt,
		CompletedAt:    call.CompletedAt,
		ModelID:        call.ModelID,
		RequestedModel: call.RequestedModel,
		Format:         string(call.Format),
		Stream:         call.Stream,
		Tag:            call.Tag,
		APIKey:         newKeyRefJSON(call.KeyID, call.KeyName),
		Outcome:        call.Outcome,
		Channel:        channelRef,
		AttemptCount:   call.AttemptCount,
		Usage: usageJSON{
			InputTokens: call.Usage.InputTokens, OutputTokens: call.Usage.OutputTokens,
			CacheWriteTokens: call.Usage.CacheWriteTokens, CacheReadTokens: call.Usage.CacheReadTokens,
		},
		Cost:       call.Cost.String(),
		Fee:        call.Fee.String(),
		Charged:    charged.String(),
		TTFTMS:     widenMillis(call.TTFTMS),
		DurationMS: widenMillis(call.DurationMS),
	}
}

// widenMillis converts the gateway's int32 milliseconds to the int the call schemas share.
func widenMillis(value *int32) *int {
	if value == nil {
		return nil
	}
	wide := int(*value)
	return &wide
}

func (a *app) getHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := accountFromContext(ctx).ID
	var marketState *groupchannel.State
	if a.groupChannels != nil {
		if a.groupChannels.Runtime == nil {
			writeGroupChannelError(w, groupchannel.ErrUnavailable)
			return
		}
		var err error
		marketState, err = a.groupChannels.Runtime.Current()
		if err != nil {
			writeGroupChannelError(w, err)
			return
		}
	}
	if err := a.keys.EnsureDefault(ctx, accountID); err != nil {
		writeDomainError(w, err)
		return
	}
	points, err := a.ledger.Points(ctx, accountID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	stats, err := a.browse.Home(ctx, accountID, localtime.DayStart(time.Now()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if marketState != nil {
		stats.ChannelsOnline, stats.ChannelsTotal = 0, 0
		for _, v := range marketState.Views {
			if v.Owner.ID == accountID {
				stats.ChannelsTotal++
				if v.Available {
					stats.ChannelsOnline++
				}
			}
		}
	}
	recent, err := a.browse.RecentCalls(ctx, accountID, 5)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	keys, err := a.keys.List(ctx, accountID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	var defaultKey *defaultKeyJSON
	for _, key := range keys {
		if key.IsDefault {
			defaultKey = &defaultKeyJSON{ID: key.ID, Name: key.Name, Prefix: key.Prefix}
		}
	}
	calls := make([]callSummaryJSON, 0, len(recent))
	for _, call := range recent {
		calls = append(calls, newGatewayCallSummaryJSON(call))
	}
	writeJSON(w, http.StatusOK, homeResponseJSON{
		Points:           pointsSummaryJSON{Balance: points.Balance.String(), CreditLimit: points.CreditLimit.String(), Available: points.Available().String()},
		Today:            homeTodayJSON{Spend: stats.TodaySpend.String(), Calls: stats.TodayCalls, SucceededCalls: stats.TodaySucceeded},
		Channels:         homeChannelsJSON{TodayRevenue: stats.RevenueToday.String(), Online: stats.ChannelsOnline, Total: stats.ChannelsTotal},
		DefaultKey:       defaultKey,
		PendingC2CTrades: stats.PendingTrades,
		RecentCalls:      calls,
	})
}

// registerBrowseRoutes registers the account home page. Market browsing has its own routes.
func (a *app) registerBrowseRoutes(r *router) {
	r.implement("B", "GET /api/home", accessReady, a.getHome)
}
