package gateway

import (
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
	channel, model string
	format         channel.Format
	tier           string
}
type modelMetricSample struct {
	end           string
	firstOutputMS *int
	outputTPS     *float64
}
type metricHistogram [metricBins]uint32
type metricCounts struct{ attempts, successes, excluded, clientErrors uint32 }
type metricBucket struct {
	metricCounts
	latency, speed metricHistogram
}
type modelMetricWindow struct {
	buckets                                     map[int64]*metricBucket
	attempts, successes, excluded, clientErrors int64
	latency, speed                              [metricBins]uint64
}
type modelMetricStats struct {
	attempts, successes, excluded, clientErrors int64
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
	epoch := statsEpoch(at)
	if key.channel == "" || key.model == "" || epoch <= 0 {
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
	if bucket.attempts == math.MaxUint32 || bucket.excluded == math.MaxUint32 {
		b.dropped++
		return false
	}
	if sample.end == EndClientError || sample.end == EndClientDisconnected {
		bucket.excluded++
		window.excluded++
		if sample.end == EndClientError {
			bucket.clientErrors++
			window.clientErrors++
		}
		return true
	}
	bucket.attempts++
	window.attempts++
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
		out[key] = modelMetricStats{attempts: window.attempts, successes: window.successes, excluded: window.excluded, clientErrors: window.clientErrors, latencySamples: ln, speedSamples: sn, firstOutputP50MS: latency, outputTPSP50: speed}
	}
	return out, b.dropped
}
