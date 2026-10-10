package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

const (
	streamHeartbeat    = 15 * time.Second
	streamWriteTimeout = 30 * time.Second
	streamBuffer       = 64
)

// streamFilter is the subset of the list filters a live stream supports.
type streamFilter struct {
	model, keyID, channelID, accountID, outcome string
}

func parseStreamFilter(r *http.Request) (streamFilter, bool) {
	query := r.URL.Query()
	filter := streamFilter{
		model: query.Get("model"), keyID: query.Get("api_key_id"), channelID: query.Get("channel_id"),
		accountID: query.Get("account_id"), outcome: query.Get("outcome"),
	}
	for _, id := range []string{filter.keyID, filter.channelID, filter.accountID} {
		if id != "" && !uuidPattern.MatchString(id) {
			return filter, false
		}
	}
	return filter, filter.outcome == "" || observe.ValidOutcome(filter.outcome)
}

// touches reports whether the call was served or attempted by the channel.
func touches(record observe.CallRecord, channelID string) bool {
	if record.Channel != nil && record.Channel.ID == channelID {
		return true
	}
	return slices.ContainsFunc(record.Attempts, func(attempt observe.Attempt) bool {
		return attempt.Channel != nil && attempt.Channel.ID == channelID
	})
}

func (f streamFilter) matches(record observe.CallRecord) bool {
	if f.model != "" && (record.ModelID == nil || *record.ModelID != f.model) && record.RequestedModel != f.model {
		return false
	}
	if f.keyID != "" && (record.Key == nil || record.Key.ID != f.keyID) {
		return false
	}
	if f.accountID != "" && record.Account.ID != f.accountID {
		return false
	}
	if f.outcome != "" && record.Outcome != f.outcome {
		return false
	}
	return f.channelID == "" || touches(record, f.channelID)
}

// serveStream pushes live call messages as Server-Sent Events until the
// client leaves. scope limits which calls this stream may ever see; render
// builds the event payload, the same call schema as the matching list endpoint.
func serveStream[T any](a *app, w http.ResponseWriter, r *http.Request, filter streamFilter, scope func(observe.CallRecord) bool, render func(observe.CallRecord) T) {
	if a.feed == nil {
		writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "实时流暂不可用")
		return
	}
	controller := http.NewResponseController(w)
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("X-Accel-Buffering", "no")
	messages, cancel := a.feed.Subscribe(streamBuffer)
	defer cancel()

	write := func(text string) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
		if _, err := w.Write([]byte(text)); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	w.WriteHeader(http.StatusOK)
	if !write("retry: 3000\n: connected\n\n") {
		return
	}
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !write(": heartbeat\n\n") {
				return
			}
		case message, ok := <-messages:
			if !ok {
				return
			}
			if !scope(message.Call) || !filter.matches(message.Call) {
				continue
			}
			payload, err := json.Marshal(render(message.Call))
			if err != nil {
				continue
			}
			if !write("event: " + message.Kind + "\ndata: " + string(payload) + "\n\n") {
				return
			}
		}
	}
}

func (a *app) streamMyCalls(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseStreamFilter(r)
	if !ok {
		writeInvalidQuery(w)
		return
	}
	filter.accountID = ""
	me := accountFromContext(r.Context()).ID
	serveStream(a, w, r, filter, func(record observe.CallRecord) bool { return record.Account.ID == me },
		func(record observe.CallRecord) callSummaryJSON { return newCallSummaryJSON(record.CallRow) })
}

func (a *app) streamAdminCalls(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseStreamFilter(r)
	if !ok {
		writeInvalidQuery(w)
		return
	}
	serveStream(a, w, r, filter, func(observe.CallRecord) bool { return true },
		func(record observe.CallRecord) adminCallJSON { return newAdminCallJSON(record.CallRow) })
}

func (a *app) streamChannelCalls(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	if !uuidPattern.MatchString(channelID) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	if _, err := a.observe.ChannelAccess(r.Context(), viewerOf(r), channelID); err != nil {
		writeObserveError(w, err)
		return
	}
	filter, ok := parseStreamFilter(r)
	if !ok {
		writeInvalidQuery(w)
		return
	}
	// The channel owner sees model and outcome filters only.
	filter = streamFilter{model: filter.model, outcome: filter.outcome}
	serveStream(a, w, r, filter, func(record observe.CallRecord) bool { return touches(record, channelID) },
		func(record observe.CallRecord) channelCallJSON {
			row := record.CallRow
			for _, attempt := range record.Attempts {
				if attempt.Channel != nil && attempt.Channel.ID == channelID {
					row.ScopeAttempts = append(row.ScopeAttempts, attempt)
				}
			}
			return newChannelCallJSON(row, channelID)
		})
}
