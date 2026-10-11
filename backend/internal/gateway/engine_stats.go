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
	previous := e.statsSnap.Load()
	var announce []Anomaly
	for model, channels := range snapshot.anomalies {
		for id, anomaly := range channels {
			if previous != nil {
				if _, already := previous.anomalies[model][id]; already {
					continue
				}
			}
			key := pairKey{model, id}
			if last, announced := e.anomalyNotified[key]; announced && now.Sub(last) < anomalyRenotifyAfter {
				continue
			}
			e.anomalyNotified[key] = now
			if !quiet {
				announce = append(announce, anomaly)
			}
		}
	}
	for key, at := range e.anomalyNotified {
		if _, flagged := snapshot.anomalies[key.model][key.channel]; !flagged && now.Sub(at) >= anomalyRenotifyAfter {
			delete(e.anomalyNotified, key)
		}
	}
	e.statsSnap.Store(snapshot)
	e.statsBuild.Unlock()

	for _, anomaly := range announce {
		e.Logger.Warn("gateway: a channel answers a model with 4xx almost every time", "channel_id", anomaly.Channel, "model", anomaly.Model,
			"client_errors", anomaly.Client, "attempts", anomaly.Total)
		e.Runtime.Announce(anomaly.Channel, channel.EventAnomalousClientErrors, anomaly.Reason())
	}
	return snapshot
}

// recordAttempt counts a finished attempt of the call in the channel statistics.
func (e *Engine) recordAttempt(call *callState, candidate Candidate, record Attempt) {
	e.StatsBook.Record(candidate.ChannelID, call.modelID, call.stream, record, e.Now())
}
