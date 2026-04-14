package pool_test

import (
	"testing"

	"proxypools/internal/model"
	"proxypools/internal/pool"
)

func TestScoreMapsHealthyNodeToL1(t *testing.T) {
	status := model.NodeRuntimeStatus{LatencyMS: 200, RecentSuccessRate: 1.0, ConsecutiveFailures: 0}
	scored := pool.Score(status)
	if scored.Tier != "L1" {
		t.Fatalf("expected L1, got %s", scored.Tier)
	}
}
