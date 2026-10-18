package gateway

import (
	"context"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// statsSnapshot is what ranking and the model pages read: the 24-hour health of
// every channel and the channels that answer a model with 4xx (see detectAnomalies).
// It is built from the StatsBook every few seconds and never modified, so a
// request takes it with one atomic read.
type statsSnapshot struct {
	channels  map[string]ChannelStats
	anomalies map[string]map[string]Anomaly // by model, then channel
	models    map[metricScope]modelRouteStats
	dropped   uint64
}

type metricScope struct {
	protocol string
	model    string
	format   channel.Format
	tier     string
	stream   bool
}
type modelRouteStats struct {
	channels  map[string]ChannelStats
	anomalies map[string]Anomaly
}

func (s *statsSnapshot) forModel(model string, format channel.Format, tier string, stream bool) (map[string]ChannelStats, map[string]Anomaly) {
	return s.forProtocol(model, format, nativeProtocol(format), tier, stream)
}
func (s *statsSnapshot) forProtocol(model string, format channel.Format, protocol, tier string, stream bool) (map[string]ChannelStats, map[string]Anomaly) {
	view := s.models[metricScope{protocol, model, format, requestedMetricTier(format, tier), stream}]
	return view.channels, view.anomalies
}

func (e *Engine) ModelStatsForProtocol(model string, format channel.Format, protocol, tier string, stream bool) map[string]ChannelStats {
	stats, _ := e.statsView().forProtocol(model, format, protocol, tier, stream)
	return stats
}

// ModelStats is an immutable, measured scope. A missing scope stays unknown;
// whole-channel history is never spread over the models it happens to offer.
func (e *Engine) ModelStats(model string, format channel.Format, tier string, stream bool) map[string]ChannelStats {
	stats, _ := e.statsView().forModel(model, format, tier, stream)
	return stats
}

func (e *Engine) modelStatsView(now time.Time) (map[metricScope]modelRouteStats, uint64) {
	rows, dropped := e.modelMetrics.view(now)
	out := map[metricScope]modelRouteStats{}
	pairs := map[metricScope][]pairSums{}
	for key, row := range rows {
		scope := metricScope{key.protocol, key.model, key.format, key.tier, key.stream}
		view := out[scope]
		if view.channels == nil {
			view.channels = map[string]ChannelStats{}
		}
		view.channels[key.channel] = ChannelStats{Attempts: row.attempts, Successes: row.successes, FirstOutputP50MS: row.firstOutputP50MS, FirstOutputSamples: int64(row.latencySamples), OutputTPSP50: row.outputTPSP50, OutputSpeedSamples: int64(row.speedSamples), ExcludedSamples: row.excluded}
		out[scope] = view
		pairs[scope] = append(pairs[scope], pairSums{channel: key.channel, total: row.anomalyAttempts, client: row.anomalyClient})
	}
	for scope, rows := range pairs {
		view := out[scope]
		view.anomalies = detectAnomalies(map[string][]pairSums{scope.model: rows})[scope.model]
		out[scope] = view
	}
	return out, dropped
}

// Stats returns the current 24h health of the channels that had attempts in the
// window. The map is shared and must not be modified.
func (e *Engine) Stats() map[string]ChannelStats { return e.statsView().channels }

// statsView returns the published snapshot, building the first one if none
// exists yet (a test, or a request before the first refresh).
func (e *Engine) statsView() *statsSnapshot {
	if snapshot := e.statsSnap.Load(); snapshot != nil {
		return snapshot
	}
	return e.buildStats(false, false)
}

// RefreshStats publishes the statistics as they stand now. RunRefresh does it
// every statsRefreshInterval; it reads memory only.
func (e *Engine) RefreshStats() { e.buildStats(true, false) }

// LoadStats seeds the statistics window with the attempts the database holds
// for the last 24 hours. It runs once at startup, before the gateway takes
// requests, and its read is the only database work the channel statistics ever
// cause. The anomalies it finds are taken as already announced: they began
// before this process, which announced them if it was running.
func (e *Engine) LoadStats(ctx context.Context) error {
	now := e.Now()
	since := time.Unix((statsEpoch(now)-statsBuckets+1)*statsBucketSeconds, 0)
	rows, err := e.Store.AttemptHistory(ctx, since)
	if err != nil {
		return err
	}
	e.StatsBook.Load(rows, now)
	if source, ok := e.Store.(ModelMetricHistory); ok {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		rows, err := source.ModelAttemptHistory(bounded, since, maxMetricBuckets+1)
		cancel()
		if err != nil {
			return err
		}
		if len(rows) > maxMetricBuckets || len(rows) > 0 && rows[0].HistoryTruncated {
			e.Logger.Warn("gateway: model metric warmup truncated; uncovered scopes stay unknown", "row_limit", maxMetricBuckets)
			// Arbitrarily truncated rows can omit failures of a returned scope.
			// Cold-start all scopes instead of publishing a biased success rate.
			rows = nil
		}
		e.modelMetrics.load(rows, now)
	}
	e.buildStats(true, true)
	return nil
}

// buildStats reads the book and publishes a snapshot. Without force it only
// builds when none exists. A channel that has newly become anomalous on a model
// is announced as a channel event, unless it was announced within
// anomalyRenotifyAfter (or quiet is set).
func (e *Engine) buildStats(force, quiet bool) *statsSnapshot {
	e.statsBuild.Lock()
	if current := e.statsSnap.Load(); current != nil && !force {
		e.statsBuild.Unlock()
		return current
	}
	now := e.Now()
	view := e.StatsBook.view(now)
	snapshot := &statsSnapshot{channels: view.channels, anomalies: detectAnomalies(view.models)}
	snapshot.models, snapshot.dropped = e.modelStatsView(now)
	previous := e.statsSnap.Load()
	type scopedAnomaly struct {
		scope metricScope
		value Anomaly
	}
	var announce []scopedAnomaly
	for scope, view := range snapshot.models {
		for id, anomaly := range view.anomalies {
			if previous != nil {
				if _, already := previous.models[scope].anomalies[id]; already {
					continue
				}
			}
			key := modelMetricKey{channel: id, model: scope.model, format: scope.format, tier: scope.tier, stream: scope.stream, protocol: scope.protocol}
			if last, announced := e.anomalyNotified[key]; announced && now.Sub(last) < anomalyRenotifyAfter {
				continue
			}
			e.anomalyNotified[key] = now
			if !quiet {
				announce = append(announce, scopedAnomaly{scope, anomaly})
			}
		}
	}
	for key, at := range e.anomalyNotified {
		scope := metricScope{key.protocol, key.model, key.format, key.tier, key.stream}
		if _, flagged := snapshot.models[scope].anomalies[key.channel]; !flagged && now.Sub(at) >= anomalyRenotifyAfter {
			delete(e.anomalyNotified, key)
		}
	}
	if snapshot.dropped > 0 && (previous == nil || snapshot.dropped > previous.dropped) {
		e.Logger.Warn("gateway: model metric observations dropped at bounded capacity", "dropped", snapshot.dropped)
	}
	e.statsSnap.Store(snapshot)
	e.statsBuild.Unlock()

	for _, item := range announce {
		anomaly := item.value
		e.Logger.Warn("gateway: a channel answers a model with 4xx almost every time", "channel_id", anomaly.Channel, "model", anomaly.Model,
			"client_errors", anomaly.Client, "attempts", anomaly.Total, "format", item.scope.format, "tier", item.scope.tier, "stream", item.scope.stream)
		e.Runtime.Announce(anomaly.Channel, channel.EventAnomalousClientErrors, anomaly.Reason())
	}
	return snapshot
}

// recordAttempt counts a finished attempt of the call in the channel statistics.
func (e *Engine) recordAttempt(call *callState, candidate Candidate, record Attempt) {
	e.StatsBook.Record(candidate.ChannelID, call.modelID, call.stream, record, e.Now())
	if record.SelectionFinal != nil && !*record.SelectionFinal || call.modelID == "" || call.polling {
		return
	}
	// modelID was resolved against the catalog before a candidate was admitted.
	// Only the fixed tier vocabulary reaches a metric key.
	key := modelMetricKey{channel: candidate.ChannelID, model: call.modelID, format: call.format, tier: requestedMetricTier(call.format, call.requestedServiceTier), stream: call.stream, protocol: callMetricProtocol(call)}
	e.modelMetrics.record(key, modelMetricSample{end: record.EndReason, firstOutputMS: record.SelectionOutputMS, outputTPS: record.OutputTPS}, e.Now())
}

// ChannelEdited tells the engine that a channel's configuration was changed (its
// settings, its models, its formats) or that it was taken down or restored.
// Whatever 4xx answers the channel gave before are no longer evidence about it:
// they came from the old configuration, and a channel ranked last is rarely
// called, so without this it would stay last until they age out of the
// 24-hour window. The 4xx anomaly figures of the channel are cleared, the
// rule is applied again and the result published before it returns, so the next
// ranking already takes the channel for what the new configuration earns. The
// channel's success rate and first-token time stay, and so do its cooldown and
// limits. If the new configuration turns out just as broken, it is flagged
// and announced again once the rule has enough fresh attempts.
//
// A call that began before the edit and ends after it is counted as of its end.
// After a restart the same call is left out, because the warm-up judges by when
// calls began (AttemptHistoryRow.BeforeEditAttempts); the difference is the
// calls in flight at the moment of the edit.
func (e *Engine) ChannelEdited(channelID string) {
	e.StatsBook.ResetAnomalyStats(channelID)
	e.modelMetrics.resetAnomalies(channelID)
	e.statsBuild.Lock()
	for key := range e.anomalyNotified {
		if key.channel == channelID {
			delete(e.anomalyNotified, key)
		}
	}
	e.statsBuild.Unlock()
	e.RefreshStats()
}
