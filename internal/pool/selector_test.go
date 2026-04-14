package pool_test

import (
	"testing"

	"proxypools/internal/model"
	"proxypools/internal/pool"
)

func TestSelectRandomNextSkipsCurrentAndDisabledNodes(t *testing.T) {
	statuses := []model.NodeRuntimeStatus{
		{NodeID: 1, State: "active", Tier: "L1", Score: 90},
		{NodeID: 2, State: "active", Tier: "L1", Score: 88, ManualDisabled: true},
		{NodeID: 3, State: "cooldown", Tier: "L2", Score: 80},
		{NodeID: 4, State: "active", Tier: "L2", Score: 70},
	}

	next, ok := pool.SelectRandomNext(1, statuses)
	if !ok {
		t.Fatal("expected random next node")
	}
	if next.NodeID != 4 {
		t.Fatalf("expected node 4, got %d", next.NodeID)
	}
}

func TestSelectBalanceNextPrefersHighestScoreInSameTier(t *testing.T) {
	statuses := []model.NodeRuntimeStatus{
		{NodeID: 1, State: "active", Tier: "L1", Score: 90},
		{NodeID: 2, State: "active", Tier: "L1", Score: 88},
		{NodeID: 3, State: "active", Tier: "L1", Score: 95},
		{NodeID: 4, State: "active", Tier: "L2", Score: 99},
	}

	next, ok := pool.SelectBalanceNext(1, statuses)
	if !ok {
		t.Fatal("expected a balance next node")
	}
	if next.NodeID != 3 {
		t.Fatalf("expected node 3, got %d", next.NodeID)
	}
}

func TestSelectSequentialNextRotatesHealthyNodes(t *testing.T) {
	statuses := []model.NodeRuntimeStatus{
		{NodeID: 1, State: "active", Tier: "L1", Score: 90},
		{NodeID: 2, State: "active", Tier: "L1", Score: 88},
		{NodeID: 3, State: "active", Tier: "L2", Score: 80},
	}

	next, ok := pool.SelectSequentialNext(1, statuses)
	if !ok {
		t.Fatal("expected a sequential next node")
	}
	if next.NodeID != 2 {
		t.Fatalf("expected node 2, got %d", next.NodeID)
	}
}

func TestSelectSequentialNextSkipsDisabledAndWraps(t *testing.T) {
	statuses := []model.NodeRuntimeStatus{
		{NodeID: 1, State: "active", Tier: "L1", Score: 90},
		{NodeID: 2, State: "active", Tier: "L1", Score: 88, ManualDisabled: true},
		{NodeID: 3, State: "cooldown", Tier: "L2", Score: 80},
		{NodeID: 4, State: "active", Tier: "L2", Score: 70},
	}

	next, ok := pool.SelectSequentialNext(4, statuses)
	if !ok {
		t.Fatal("expected wrapped sequential next node")
	}
	if next.NodeID != 1 {
		t.Fatalf("expected node 1, got %d", next.NodeID)
	}
}

func TestSelectNextPrefersSameTier(t *testing.T) {
	statuses := []model.NodeRuntimeStatus{
		{NodeID: 1, State: "cooldown", Tier: "L1", Score: 90},
		{NodeID: 2, State: "active", Tier: "L1", Score: 88},
		{NodeID: 3, State: "active", Tier: "L2", Score: 80},
	}

	next, ok := pool.SelectNext(1, statuses)
	if !ok {
		t.Fatal("expected a replacement node")
	}
	if next.NodeID != 2 {
		t.Fatalf("expected node 2, got %d", next.NodeID)
	}
}
