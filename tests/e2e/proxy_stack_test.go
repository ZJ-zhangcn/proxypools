package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"proxypools/internal/config"
	"proxypools/internal/model"
	"proxypools/internal/web"
)

type fakeSubscriptionService struct{}

func (fakeSubscriptionService) GetPrimarySubscription(ctx context.Context) (map[string]any, error) {
	return map[string]any{
		"id":                 int64(1),
		"name":               "default",
		"url":                "https://example.com/sub",
		"enabled":            true,
		"last_fetch_at":      "2026-04-12T05:00:00Z",
		"last_fetch_status":  "success",
		"last_fetch_error":   "",
		"last_added_nodes":   2,
		"last_removed_nodes": 1,
	}, nil
}

func (fakeSubscriptionService) RefreshSubscription(ctx context.Context) (map[string]any, error) {
	return fakeSubscriptionService{}.GetPrimarySubscription(ctx)
}

type fakeRuntimeAdminService struct{}

func (fakeRuntimeAdminService) UnlockSelection(ctx context.Context) error {
	return nil
}

func (fakeRuntimeAdminService) UnlockSelectionByPort(ctx context.Context, portKey string) error {
	return nil
}

func (fakeRuntimeAdminService) UpdateRuntimeSettings(ctx context.Context, runtimeMode string, poolAlgorithm string) error {
	return nil
}

func (fakeRuntimeAdminService) UpdateRuntimeSettingsByPort(ctx context.Context, portKey string, runtimeMode string, poolAlgorithm string) error {
	return nil
}

type fakeNodeAdminService struct{}

func (fakeNodeAdminService) SetNodeManualDisabled(ctx context.Context, nodeID int64, disabled bool) error {
	return nil
}

func (fakeNodeAdminService) SetNodeManualDisabledByPort(ctx context.Context, portKey string, nodeID int64, disabled bool) error {
	return nil
}

type fakeEventLogService struct{}

func (fakeEventLogService) ListEventLogs(ctx context.Context, limit int) ([]map[string]any, error) {
	return []map[string]any{{
		"id":              int64(1),
		"event_type":      "manual_switch",
		"level":           "info",
		"message":         "node switched manually",
		"related_node_id": int64(1),
		"metadata_json":   "{}",
		"created_at":      "2026-04-12T05:20:00Z",
	}}, nil
}

type fakeRuntimeSummaryProvider struct{}

func (fakeRuntimeSummaryProvider) RuntimeSummary(ctx context.Context) (map[string]any, error) {
	return map[string]any{
		"current_active_node_id": int64(1),
		"selection_mode":         "auto",
		"runtime_mode":           "pool",
		"pool_algorithm":         "random",
		"restart_required":       false,
		"healthy_nodes":          1,
		"total_nodes":            1,
		"last_health_check_at":   "2026-04-12T05:10:00Z",
		"last_switch_reason":     "pool_random_rotate",
		"last_switch_at":         "2026-04-12T05:00:00Z",
		"lane_count":             2,
		"ready_lane_count":       1,
		"lane_details": []map[string]any{{
			"port_key":           "default",
			"lane_key":           "lane-http-1",
			"protocol":           "http",
			"assigned_node_id":   int64(1),
			"state":              "ready",
			"last_switch_reason": "lane_allocator_assigned",
		}},
		"node_details": []map[string]any{{
			"id":                   int64(1),
			"name":                 "node-1",
			"protocol_type":        "shadowsocks",
			"server":               "127.0.0.1",
			"port":                 8388,
			"state":                "active",
			"tier":                 "L1",
			"score":                95.0,
			"latency_ms":           120,
			"recent_success_rate":  1.0,
			"consecutive_failures": 0,
			"cooldown_until":       "",
			"manual_disabled":      false,
			"is_active":            true,
		}},
	}, nil
}

func (fakeRuntimeSummaryProvider) RuntimeSummaryByPort(ctx context.Context, portKey string) (map[string]any, error) {
	summary, _ := fakeRuntimeSummaryProvider{}.RuntimeSummary(ctx)
	summary["port_key"] = portKey
	if portKey == "canary" {
		summary["runtime_mode"] = "pool"
		summary["pool_algorithm"] = "balance"
	}
	return summary, nil
}

func (fakeRuntimeSummaryProvider) ListPortRuntimeStates(ctx context.Context) ([]model.PortRuntimeState, error) {
	return []model.PortRuntimeState{
		{PortKey: config.DefaultPortKey, RuntimeState: model.RuntimeState{RuntimeMode: "single_active", PoolAlgorithm: "sequential"}},
		{PortKey: "canary", RuntimeState: model.RuntimeState{RuntimeMode: "pool", PoolAlgorithm: "balance"}},
	}, nil
}

type fakeDispatcherStatusService struct{}

func (fakeDispatcherStatusService) GetDispatcherStatus(ctx context.Context) (map[string]any, error) {
	return map[string]any{
		"enabled":                true,
		"http_listen":            "0.0.0.0:8888",
		"socks_listen":           "0.0.0.0:8889",
		"algorithm":              "balance",
		"selected_port_key":      "canary",
		"selected_healthy_nodes": 3,
		"selected_score":         95.0,
	}, nil
}

func testDeps() web.Dependencies {
	return web.Dependencies{
		AdminUsername:          "admin",
		AdminPasswordHash:      web.HashPassword("admin"),
		ConfigPath:             "data/sing-box.json",
		AdminListen:            "0.0.0.0:8080",
		HTTPListen:             "0.0.0.0:7777",
		SOCKSListen:            "0.0.0.0:7780",
		HealthListen:           "127.0.0.1:19090",
		SubscriptionConfigured: true,
		Ports: []config.PortConfig{
			{Key: config.DefaultPortKey, Name: "默认入口", HTTPListenAddr: "0.0.0.0", HTTPListenPort: 7777, SOCKSListenAddr: "0.0.0.0", SOCKSListenPort: 7780, RuntimeMode: "single_active", PoolAlgorithm: "sequential"},
			{Key: "canary", Name: "灰度入口", HTTPListenAddr: "127.0.0.1", HTTPListenPort: 8777, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 8780, RuntimeMode: "pool", PoolAlgorithm: "balance"},
		},
		RuntimeStateProvider:    fakeRuntimeSummaryProvider{},
		SubscriptionService:     fakeSubscriptionService{},
		NodeAdminService:        fakeNodeAdminService{},
		RuntimeAdminService:     fakeRuntimeAdminService{},
		EventLogService:         fakeEventLogService{},
		DispatcherStatusService: fakeDispatcherStatusService{},
	}
}

func TestAdminHealthEndpoint(t *testing.T) {
	r := web.NewRouter(web.Dependencies{AdminUsername: "admin", AdminPasswordHash: web.HashPassword("admin")})
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSubscriptionEndpointReturnsStructuredStatus(t *testing.T) {
	r := web.NewRouter(testDeps())
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/subscription", nil)
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.SetBasicAuth("admin", "admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["url"] != "https://example.com/sub" {
		t.Fatalf("expected url to be set, got %#v", body["url"])
	}
	if body["last_fetch_status"] != "success" {
		t.Fatalf("expected success fetch status, got %#v", body["last_fetch_status"])
	}
	if body["last_added_nodes"] != float64(2) {
		t.Fatalf("expected last_added_nodes=2, got %#v", body["last_added_nodes"])
	}
	if body["last_removed_nodes"] != float64(1) {
		t.Fatalf("expected last_removed_nodes=1, got %#v", body["last_removed_nodes"])
	}
}

func TestEventsEndpointReturnsStructuredStatus(t *testing.T) {
	r := web.NewRouter(testDeps())
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.SetBasicAuth("admin", "admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected one event log item, got %#v", body["items"])
	}
}

func TestRuntimeSettingsEndpointReturnsNoContent(t *testing.T) {
	r := web.NewRouter(testDeps())
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/runtime/settings", strings.NewReader(`{"runtime_mode":"pool","pool_algorithm":"random"}`))
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("admin", "admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestDispatcherEndpointReturnsStructuredStatus(t *testing.T) {
	r := web.NewRouter(testDeps())
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/dispatcher", nil)
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.SetBasicAuth("admin", "admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["algorithm"] != "balance" {
		t.Fatalf("expected dispatcher algorithm=balance, got %#v", body["algorithm"])
	}
	if body["selected_port_key"] != "canary" {
		t.Fatalf("expected selected_port_key=canary, got %#v", body["selected_port_key"])
	}
}

func TestRuntimeEndpointReturnsStructuredStatus(t *testing.T) {
	r := web.NewRouter(testDeps())
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/runtime", nil)
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.SetBasicAuth("admin", "admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["config_path"] != "data/sing-box.json" {
		t.Fatalf("expected config_path to be set, got %#v", body["config_path"])
	}
	if body["subscription_configured"] != true {
		t.Fatalf("expected subscription_configured=true, got %#v", body["subscription_configured"])
	}
	if _, ok := body["running"]; !ok {
		t.Fatal("expected running field in runtime response")
	}
	if _, ok := body["last_switch_reason"]; !ok {
		t.Fatal("expected last_switch_reason field in runtime response")
	}
	if _, ok := body["selection_mode"]; !ok {
		t.Fatal("expected selection_mode field in runtime response")
	}
	if body["runtime_mode"] != "pool" {
		t.Fatalf("expected runtime_mode=pool, got %#v", body["runtime_mode"])
	}
	if body["pool_algorithm"] != "random" {
		t.Fatalf("expected pool_algorithm=random, got %#v", body["pool_algorithm"])
	}
	if _, ok := body["last_switch_at"]; !ok {
		t.Fatal("expected last_switch_at field in runtime response")
	}
	if body["admin_listen"] != "0.0.0.0:8080" {
		t.Fatalf("expected admin_listen to be set, got %#v", body["admin_listen"])
	}
	if body["http_listen"] != "0.0.0.0:7777" {
		t.Fatalf("expected http_listen to be set, got %#v", body["http_listen"])
	}
	if body["socks_listen"] != "0.0.0.0:7780" {
		t.Fatalf("expected socks_listen to be set, got %#v", body["socks_listen"])
	}
	if body["health_listen"] != "127.0.0.1:19090" {
		t.Fatalf("expected health_listen to be set, got %#v", body["health_listen"])
	}
	if body["last_subscription_fetch_at"] != "2026-04-12T05:00:00Z" {
		t.Fatalf("expected last_subscription_fetch_at to be set, got %#v", body["last_subscription_fetch_at"])
	}
	if body["last_subscription_status"] != "success" {
		t.Fatalf("expected last_subscription_status to be set, got %#v", body["last_subscription_status"])
	}
	if body["last_health_check_at"] != "2026-04-12T05:10:00Z" {
		t.Fatalf("expected last_health_check_at to be set, got %#v", body["last_health_check_at"])
	}
	if body["lane_count"] != float64(2) {
		t.Fatalf("expected lane_count=2, got %#v", body["lane_count"])
	}
	if body["ready_lane_count"] != float64(1) {
		t.Fatalf("expected ready_lane_count=1, got %#v", body["ready_lane_count"])
	}
	laneDetails, ok := body["lane_details"]
	if !ok {
		t.Fatal("expected lane_details field in runtime response")
	}
	if _, ok := laneDetails.([]any); !ok {
		t.Fatalf("expected lane_details array, got %#v", laneDetails)
	}
	nodeDetails, ok := body["node_details"]
	if !ok {
		t.Fatal("expected node_details field in runtime response")
	}
	items, ok := nodeDetails.([]any)
	if !ok {
		t.Fatalf("expected node_details array, got %#v", nodeDetails)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one node detail")
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first node detail object, got %#v", items[0])
	}
	if _, ok := first["id"]; !ok {
		t.Fatal("expected node detail id")
	}
	if _, ok := first["name"]; !ok {
		t.Fatal("expected node detail name")
	}
	if _, ok := first["state"]; !ok {
		t.Fatal("expected node detail state")
	}
	if _, ok := first["tier"]; !ok {
		t.Fatal("expected node detail tier")
	}
}

func TestPortRuntimeEndpointReturnsScopedStatus(t *testing.T) {
	r := web.NewRouter(testDeps())
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/ports/canary/runtime", nil)
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.SetBasicAuth("admin", "admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["port_key"] != "canary" {
		t.Fatalf("expected port_key=canary, got %#v", body["port_key"])
	}
	if body["pool_algorithm"] != "balance" {
		t.Fatalf("expected pool_algorithm=balance, got %#v", body["pool_algorithm"])
	}
}

func TestPortsEndpointReturnsConfiguredPorts(t *testing.T) {
	r := web.NewRouter(testDeps())
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/ports", nil)
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.SetBasicAuth("admin", "admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected 2 ports, got %#v", body["items"])
	}
}
