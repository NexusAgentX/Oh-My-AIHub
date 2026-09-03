package main

import (
	"context"
	"testing"
	"time"
)

func TestRecoveryCycleGivesEachSubsystemAnIndependentTimeout(t *testing.T) {
	secondStartedFresh := false
	result := runRecoveryCycle(
		context.Background(), 10*time.Millisecond,
		func(ctx context.Context) (int64, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
		func(ctx context.Context) (int64, error) {
			deadline, hasDeadline := ctx.Deadline()
			secondStartedFresh = ctx.Err() == nil && hasDeadline && time.Until(deadline) > 0
			return 2, nil
		},
	)
	if result.channelErr == nil || result.gatewayErr != nil || result.gateways != 2 || !secondStartedFresh {
		t.Fatalf("independent recovery contexts = %+v, second fresh=%t", result, secondStartedFresh)
	}
}
