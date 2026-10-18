package gateway

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

const (
	metricBins       = 192
	maxMetricSeries  = 4096
	maxMetricBuckets = 8192
)

// Metric scope is supplied by admitted catalog candidates, never arbitrary
// request text. Empty tier is the unrequested/default calling context; explicit
// tiers use measured actual tiers so a downgrade cannot supply priority speed.
type modelMetricKey struct {
	protocol       string
	channel, model string
	format         channel.Format
	tier           string
	stream         bool
}
type modelMetricSample struct {
	end           string
	firstOutputMS *int
	outputTPS     *float64
}
type metricHistogram [metricBins]uint32
type metricCounts struct{ attempts, successes, excluded, clientErrors, anomalyAttempts, anomalyClient uint32 }
type metricBucket struct {
	metricCounts
	latency, speed metricHistogram
}
type modelMetricWindow struct {
	buckets                                     map[int64]*metricBucket
	attempts, successes, excluded, clientErrors int64
	anomalyAttempts, anomalyClient              int64
	latency, speed                              [metricBins]uint64
}
type modelMetricStats struct {
	attempts, successes, excluded, clientErrors int64
	anomalyAttempts, anomalyClient              int64
	latencySamples, speedSamples                uint64
	firstOutputP50MS, outputTPSP50              *float64
}
type modelMetricsBook struct {
	mu      sync.Mutex
	series  map[modelMetricKey]*modelMetricWindow
	buckets int
	dropped uint64
}

func newModelMetricsBook() *modelMetricsBook {
	return &modelMetricsBook{series: map[modelMetricKey]*modelMetricWindow{}}
}

func metricBin(value float64, speed bool) int {
	if value < 0 || speed && value == 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return -1
	}
	if value == 0 {
		return 0
	}
	bin := math.Floor(math.Log2(value)*8) + 1
	if speed {
		bin = math.Floor((math.Log2(value) + 16) * 4)
	}
	return max(0, min(metricBins-1, int(bin)))
}
func metricBinValue(bin int, speed bool) float64 {
	if bin == 0 && !speed {
		return 0
	}
	lower := math.Exp2(float64(bin-1) / 8)
	upper := math.Exp2(float64(bin) / 8)
	if speed {
		lower = math.Exp2(float64(bin)/4 - 16)
		upper = math.Exp2(float64(bin+1)/4 - 16)
	}
	return (lower + upper) / 2
}
func metricMedian(hist *[metricBins]uint64, speed bool) (*float64, uint64) {
	var total uint64
	for _, n := range hist {
		total += n
	}
	if total == 0 {
		return nil, 0
	}
	lower, upper := (total-1)/2, total/2
	var seen uint64
	var a, b float64
	for i, n := range hist {
		if seen <= lower && seen+n > lower {
			a = metricBinValue(i, speed)
		}
		if seen <= upper && seen+n > upper {
			b = metricBinValue(i, speed)
			break
		}
		seen += n
	}
	value := (a + b) / 2
	return &value, total
}

// Each finished attempt takes one bounded map update, with at most one sparse
// bucket allocation. Series/bucket saturation drops observation instead of
// allocating or queuing more work; expired space is recovered by refresh.
func (b *modelMetricsBook) record(key modelMetricKey, sample modelMetricSample, at time.Time) bool {
	key = normalizeModelMetricKey(key)
	epoch := statsEpoch(at)
	if key.channel == "" || key.model == "" || epoch <= 0 || !validMetricProtocol(key.protocol, key.format) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	window := b.series[key]
	if window == nil && len(b.series) >= maxMetricSeries {
		b.dropped++
		return false
	}
	var bucket *metricBucket
	if window != nil {
		bucket = window.buckets[epoch]
	}
	if bucket == nil && b.buckets >= maxMetricBuckets {
		b.dropped++
		return false
	}
	if window == nil {
		window = &modelMetricWindow{buckets: map[int64]*metricBucket{}}
		b.series[key] = window
	}
	if bucket == nil {
		bucket = &metricBucket{}
		window.buckets[epoch] = bucket
		b.buckets++
	}
	// A five-minute counter has a fixed width. Refuse overflow even under
	// synthetic or pathological rates instead of silently wrapping statistics.
	if bucket.attempts == math.MaxUint32 || bucket.excluded == math.MaxUint32 || bucket.anomalyAttempts == math.MaxUint32 {
		b.dropped++
		return false
	}
	if sample.end == EndClientError || sample.end == EndClientDisconnected {
		bucket.excluded++
		window.excluded++
		if sample.end == EndClientError {
			bucket.clientErrors++
			window.clientErrors++
			bucket.anomalyAttempts++
			window.anomalyAttempts++
			bucket.anomalyClient++
			window.anomalyClient++
		}
		return true
	}
	bucket.attempts++
	window.attempts++
	bucket.anomalyAttempts++
	window.anomalyAttempts++
	if sample.end != EndCompleted {
		return true
	}
	bucket.successes++
	window.successes++
	if sample.firstOutputMS != nil {
		if bin := metricBin(float64(*sample.firstOutputMS), false); bin >= 0 {
			bucket.latency[bin]++
			window.latency[bin]++
		}
	}
	if sample.outputTPS != nil {
		if bin := metricBin(*sample.outputTPS, true); bin >= 0 {
			bucket.speed[bin]++
			window.speed[bin]++
		}
	}
	return true
}

// Refresh is bounded by the global sparse bucket/series limits. It performs no
// SQL and is called by the background stats publisher, never for each frame.
func (b *modelMetricsBook) view(now time.Time) (map[modelMetricKey]modelMetricStats, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	floor := statsEpoch(now) - statsBuckets + 1
	out := make(map[modelMetricKey]modelMetricStats, len(b.series))
	for key, window := range b.series {
		for epoch, bucket := range window.buckets {
			if epoch >= floor {
				continue
			}
			window.attempts -= int64(bucket.attempts)
			window.successes -= int64(bucket.successes)
			window.excluded -= int64(bucket.excluded)
			window.clientErrors -= int64(bucket.clientErrors)
			window.anomalyAttempts -= int64(bucket.anomalyAttempts)
			window.anomalyClient -= int64(bucket.anomalyClient)
			for i := range metricBins {
				window.latency[i] -= uint64(bucket.latency[i])
				window.speed[i] -= uint64(bucket.speed[i])
			}
			delete(window.buckets, epoch)
			b.buckets--
		}
		if len(window.buckets) == 0 {
			delete(b.series, key)
			continue
		}
		latency, ln := metricMedian(&window.latency, false)
		speed, sn := metricMedian(&window.speed, true)
		out[key] = modelMetricStats{attempts: window.attempts, successes: window.successes, excluded: window.excluded, clientErrors: window.clientErrors, anomalyAttempts: window.anomalyAttempts, anomalyClient: window.anomalyClient, latencySamples: ln, speedSamples: sn, firstOutputP50MS: latency, outputTPSP50: speed}
	}
	return out, b.dropped
}

func (b *modelMetricsBook) resetAnomalies(channelID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, window := range b.series {
		if key.channel != channelID {
			continue
		}
		window.anomalyAttempts, window.anomalyClient = 0, 0
		for _, bucket := range window.buckets {
			bucket.anomalyAttempts, bucket.anomalyClient = 0, 0
		}
	}
}

// ModelAttemptHistoryRow is a bounded aggregate of v2 observations. Legacy
// first-byte and whole-channel metrics cannot warm the new ranking scope.
type ModelAttemptHistoryRow struct {
	Protocol                                   string
	HistoryTruncated                           bool
	ChannelID, ModelID, Tier                   string
	Format                                     channel.Format
	Stream                                     bool
	Bucket                                     int64
	LatencyBin, SpeedBin                       int
	Counted, Successes, Excluded, ClientErrors int64
	BeforeEditAttempts, BeforeEditClientErrors int64
}

type ModelMetricHistory interface {
	ModelAttemptHistory(context.Context, time.Time, int32) ([]ModelAttemptHistoryRow, error)
}

func (b *modelMetricsBook) load(rows []ModelAttemptHistoryRow, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	floor, ceiling := statsEpoch(now)-statsBuckets+1, statsEpoch(now)
	for _, row := range rows {
		if row.Bucket < floor || row.Bucket > ceiling || row.ModelID == "" || row.ChannelID == "" || row.Tier != requestedMetricTier(row.Format, row.Tier) || !validMetricProtocol(row.Protocol, row.Format) {
			continue
		}
		if row.Counted < 0 || row.Counted > math.MaxUint32 || row.Successes < 0 || row.Successes > row.Counted || row.Excluded < 0 || row.Excluded > math.MaxUint32 || row.ClientErrors < 0 || row.ClientErrors > row.Excluded || row.Counted+row.Excluded == 0 || row.BeforeEditAttempts < 0 || row.BeforeEditAttempts > row.Counted+row.ClientErrors || row.BeforeEditClientErrors < 0 || row.BeforeEditClientErrors > row.ClientErrors {
			b.dropped++
			continue
		}
		key := modelMetricKey{channel: row.ChannelID, model: row.ModelID, format: row.Format, tier: row.Tier, stream: row.Stream, protocol: row.Protocol}
		window := b.series[key]
		if window == nil && len(b.series) >= maxMetricSeries {
			b.dropped++
			continue
		}
		var bucket *metricBucket
		if window != nil {
			bucket = window.buckets[row.Bucket]
		}
		if bucket == nil && b.buckets >= maxMetricBuckets {
			b.dropped++
			continue
		}
		if window == nil {
			window = &modelMetricWindow{buckets: map[int64]*metricBucket{}}
			b.series[key] = window
		}
		if bucket == nil {
			bucket = &metricBucket{}
			window.buckets[row.Bucket] = bucket
			b.buckets++
		}
		anomaly := row.Counted + row.ClientErrors - row.BeforeEditAttempts
		client := row.ClientErrors - row.BeforeEditClientErrors
		if client > anomaly || row.Counted > math.MaxUint32-int64(bucket.attempts) || row.Excluded > math.MaxUint32-int64(bucket.excluded) || anomaly > math.MaxUint32-int64(bucket.anomalyAttempts) {
			b.dropped++
			continue
		}
		bucket.attempts += uint32(row.Counted)
		window.attempts += row.Counted
		bucket.successes += uint32(row.Successes)
		window.successes += row.Successes
		bucket.excluded += uint32(row.Excluded)
		window.excluded += row.Excluded
		bucket.clientErrors += uint32(row.ClientErrors)
		window.clientErrors += row.ClientErrors
		bucket.anomalyAttempts += uint32(anomaly)
		window.anomalyAttempts += anomaly
		bucket.anomalyClient += uint32(client)
		window.anomalyClient += client
		if row.LatencyBin >= 0 && row.LatencyBin < metricBins {
			bucket.latency[row.LatencyBin] += uint32(row.Successes)
			window.latency[row.LatencyBin] += uint64(row.Successes)
		}
		if row.SpeedBin >= 0 && row.SpeedBin < metricBins {
			bucket.speed[row.SpeedBin] += uint32(row.Successes)
			window.speed[row.SpeedBin] += uint64(row.Successes)
		}
	}
}
