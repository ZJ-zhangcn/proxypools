package sqlite_test

import (
	"context"
	"testing"

	"proxypools/internal/model"
	sqliteRepo "proxypools/internal/storage/sqlite"
)

func TestMigrateAndSaveSubscription(t *testing.T) {
	repo, err := sqliteRepo.New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("new repo failed: %v", err)
	}

	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}

	sub := model.Subscription{Name: "default", URL: "https://example.com/sub"}
	if err := repo.UpsertSubscription(context.Background(), &sub); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	got, err := repo.GetPrimarySubscription(context.Background())
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.URL != sub.URL {
		t.Fatalf("expected url %s, got %s", sub.URL, got.URL)
	}
	if got.LastAddedNodes != 0 || got.LastRemovedNodes != 0 {
		t.Fatalf("expected zero added/removed defaults, got %d/%d", got.LastAddedNodes, got.LastRemovedNodes)
	}

	if err := repo.UpdateSubscriptionFetchResult(context.Background(), got.ID, "2026-04-12T08:00:00Z", "success", "", 3, 1); err != nil {
		t.Fatalf("update fetch result failed: %v", err)
	}
	updated, err := repo.GetPrimarySubscription(context.Background())
	if err != nil {
		t.Fatalf("get updated subscription failed: %v", err)
	}
	if updated.LastFetchStatus != "success" || updated.LastAddedNodes != 3 || updated.LastRemovedNodes != 1 {
		t.Fatalf("unexpected updated fetch result: %#v", updated)
	}

	event := model.EventLog{EventType: "manual_switch", Level: "info", Message: "node switched manually", RelatedNodeID: 7}
	if err := repo.CreateEventLog(context.Background(), event); err != nil {
		t.Fatalf("create event log failed: %v", err)
	}
	items, err := repo.ListEventLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("list event logs failed: %v", err)
	}
	if len(items) != 1 || items[0].EventType != "manual_switch" {
		t.Fatalf("unexpected event logs: %#v", items)
	}
}

func TestRequestLaneStateRoundTrip(t *testing.T) {
	repo, err := sqliteRepo.New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("new repo failed: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	state := model.RequestLaneState{
		PortKey:          "default",
		LaneKey:          "lane-http-1",
		Protocol:         "http",
		AssignedNodeID:   7,
		State:            "ready",
		LastSwitchReason: "lane_allocator_assigned",
		LastSwitchAt:     "2026-04-13T08:00:00Z",
	}
	if err := repo.UpsertRequestLaneState(context.Background(), state); err != nil {
		t.Fatalf("upsert lane state failed: %v", err)
	}
	got, err := repo.GetRequestLaneState(context.Background(), "default", "lane-http-1")
	if err != nil {
		t.Fatalf("get lane state failed: %v", err)
	}
	if got.AssignedNodeID != 7 || got.Protocol != "http" || got.State != "ready" {
		t.Fatalf("unexpected lane state: %#v", got)
	}
	items, err := repo.ListRequestLaneStatesByPort(context.Background(), "default")
	if err != nil {
		t.Fatalf("list lane states failed: %v", err)
	}
	if len(items) != 1 || items[0].LaneKey != "lane-http-1" {
		t.Fatalf("unexpected lane states: %#v", items)
	}
	if err := repo.DeleteRequestLaneStatesByPort(context.Background(), "default"); err != nil {
		t.Fatalf("delete lane states failed: %v", err)
	}
	items, err = repo.ListRequestLaneStatesByPort(context.Background(), "default")
	if err != nil {
		t.Fatalf("list lane states after delete failed: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected lane states to be deleted, got %#v", items)
	}
}
