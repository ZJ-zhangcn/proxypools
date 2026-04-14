package pool_test

import (
	"testing"

	"proxypools/internal/pool"
)

func TestSelectSequentialDispatcherPortRotatesHealthyPorts(t *testing.T) {
	candidates := []pool.DispatcherCandidate{
		{PortKey: "default", HealthyNodeCount: 2},
		{PortKey: "canary", HealthyNodeCount: 1},
		{PortKey: "standby", HealthyNodeCount: 0},
	}

	next, ok := pool.SelectSequentialDispatcherPort("default", candidates)
	if !ok {
		t.Fatal("expected sequential dispatcher candidate")
	}
	if next.PortKey != "canary" {
		t.Fatalf("expected canary, got %s", next.PortKey)
	}
}

func TestSelectRandomDispatcherPortSkipsCurrentWhenPossible(t *testing.T) {
	candidates := []pool.DispatcherCandidate{
		{PortKey: "default", HealthyNodeCount: 2},
		{PortKey: "canary", HealthyNodeCount: 1},
	}

	next, ok := pool.SelectRandomDispatcherPort("default", candidates)
	if !ok {
		t.Fatal("expected random dispatcher candidate")
	}
	if next.PortKey != "canary" {
		t.Fatalf("expected canary, got %s", next.PortKey)
	}
}

func TestSelectBalanceDispatcherPortPrefersHealthThenScore(t *testing.T) {
	candidates := []pool.DispatcherCandidate{
		{PortKey: "default", HealthyNodeCount: 1, CurrentActiveScore: 90},
		{PortKey: "canary", HealthyNodeCount: 3, CurrentActiveScore: 50},
		{PortKey: "blue", HealthyNodeCount: 3, CurrentActiveScore: 95},
	}

	next, ok := pool.SelectBalanceDispatcherPort("default", candidates)
	if !ok {
		t.Fatal("expected balance dispatcher candidate")
	}
	if next.PortKey != "blue" {
		t.Fatalf("expected blue, got %s", next.PortKey)
	}
}

func TestSelectSequentialDispatcherLaneRotatesHealthyLanes(t *testing.T) {
	candidates := []pool.DispatcherCandidate{
		{PortKey: "default", LaneKey: "lane-http-1", Protocol: "http", HealthyNodeCount: 1},
		{PortKey: "default", LaneKey: "lane-http-2", Protocol: "http", HealthyNodeCount: 1},
		{PortKey: "default", LaneKey: "lane-http-3", Protocol: "http", HealthyNodeCount: 0},
	}
	next, ok := pool.SelectSequentialDispatcherLane("lane-http-1", candidates)
	if !ok {
		t.Fatal("expected sequential lane candidate")
	}
	if next.LaneKey != "lane-http-2" {
		t.Fatalf("expected lane-http-2, got %s", next.LaneKey)
	}
}

func TestSelectRandomDispatcherLaneSkipsCurrentWhenPossible(t *testing.T) {
	candidates := []pool.DispatcherCandidate{
		{PortKey: "default", LaneKey: "lane-http-1", Protocol: "http", HealthyNodeCount: 1},
		{PortKey: "default", LaneKey: "lane-http-2", Protocol: "http", HealthyNodeCount: 1},
	}
	next, ok := pool.SelectRandomDispatcherLane("lane-http-1", candidates)
	if !ok {
		t.Fatal("expected random lane candidate")
	}
	if next.LaneKey != "lane-http-2" {
		t.Fatalf("expected lane-http-2, got %s", next.LaneKey)
	}
}

func TestSelectRandomDispatcherLanePrefersHigherWeight(t *testing.T) {
	candidates := []pool.DispatcherCandidate{
		{PortKey: "default", LaneKey: "lane-http-1", Protocol: "http", HealthyNodeCount: 1, Weight: 1},
		{PortKey: "default", LaneKey: "lane-http-2", Protocol: "http", HealthyNodeCount: 1, Weight: 5},
	}
	next, ok := pool.SelectRandomDispatcherLane("lane-http-1", candidates)
	if !ok {
		t.Fatal("expected weighted random lane candidate")
	}
	if next.LaneKey != "lane-http-2" {
		t.Fatalf("expected lane-http-2, got %s", next.LaneKey)
	}
}

func TestSelectStickyDispatcherLaneUsesStableKey(t *testing.T) {
	candidates := []pool.DispatcherCandidate{
		{PortKey: "default", LaneKey: "lane-http-1", Protocol: "http", HealthyNodeCount: 1},
		{PortKey: "default", LaneKey: "lane-http-2", Protocol: "http", HealthyNodeCount: 1},
		{PortKey: "default", LaneKey: "lane-http-3", Protocol: "http", HealthyNodeCount: 1},
	}
	first, ok := pool.SelectStickyDispatcherLane("user-123", candidates)
	if !ok {
		t.Fatal("expected sticky lane candidate")
	}
	second, ok := pool.SelectStickyDispatcherLane("user-123", candidates)
	if !ok {
		t.Fatal("expected sticky lane candidate on second select")
	}
	if first.LaneKey != second.LaneKey {
		t.Fatalf("expected stable sticky lane selection, got %s and %s", first.LaneKey, second.LaneKey)
	}
}
