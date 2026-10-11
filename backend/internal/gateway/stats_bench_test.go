package gateway

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkRecord is what the end of every attempt pays.
func BenchmarkRecord(b *testing.B) {
	book := NewStatsBook()
	now := bucketStart(someEpoch)
	ttft := 420
	attempt := Attempt{EndReason: EndCompleted, TTFTMS: &ttft}
	b.ReportAllocs()
	for b.Loop() {
		book.Record("channel", "model", true, attempt, now)
	}
}

func BenchmarkRecordParallel(b *testing.B) {
	book := NewStatsBook()
	now := bucketStart(someEpoch)
	ttft := 420
	attempt := Attempt{EndReason: EndCompleted, TTFTMS: &ttft}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			book.Record("channel", "model", true, attempt, now)
		}
	})
}

// BenchmarkView is the periodic republish for a full house: 100 channels with a
// full day of streaming traffic and 2000 channel-and-model windows.
func BenchmarkView(b *testing.B) {
	book := NewStatsBook()
	end := someEpoch + 4*statsBuckets
	for channel := range 100 {
		id := fmt.Sprintf("channel-%d", channel)
		for epoch := end - statsBuckets + 1; epoch <= end; epoch++ {
			for sample := range 6 {
				ttft := 150 + (channel*7+sample*131+int(epoch)%97)%900
				book.Record(id, fmt.Sprintf("model-%d", (channel+sample)%20), true, attemptOf(EndCompleted, ttft), bucketStart(epoch))
			}
		}
	}
	now := bucketStart(end).Add(time.Minute)
	b.ReportAllocs()
	for b.Loop() {
		detectAnomalies(book.view(now).models)
	}
}
