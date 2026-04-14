package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"proxypools/internal/config"
	"proxypools/internal/model"
	"proxypools/internal/web"
)

type fakeManualSwitchService struct {
	nodeID  int64
	portKey string
	err     error
}

func (f *fakeManualSwitchService) SwitchNode(ctx context.Context, nodeID int64) error {
	f.nodeID = nodeID
	return f.err
}

func (f *fakeManualSwitchService) SwitchNodeByPort(ctx context.Context, portKey string, nodeID int64) error {
	f.portKey = portKey
	f.nodeID = nodeID
	return f.err
}

type fakeSubscriptionService struct {
	payload map[string]any
	err     error
}

func (f *fakeSubscriptionService) GetPrimarySubscription(ctx context.Context) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.payload, nil
}

func (f *fakeSubscriptionService) RefreshSubscription(ctx context.Context) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.payload, nil
}

type fakeNodeAdminService struct {
	nodeID   int64
	portKey  string
	disabled bool
	err      error
}

func (f *fakeNodeAdminService) SetNodeManualDisabled(ctx context.Context, nodeID int64, disabled bool) error {
	f.nodeID = nodeID
	f.disabled = disabled
	return f.err
}

func (f *fakeNodeAdminService) SetNodeManualDisabledByPort(ctx context.Context, portKey string, nodeID int64, disabled bool) error {
	f.portKey = portKey
	f.nodeID = nodeID
	f.disabled = disabled
	return f.err
}

type fakeRuntimeAdminService struct {
	called        bool
	err           error
	portKey       string
	runtimeMode   string
	poolAlgorithm string
}

func (f *fakeRuntimeAdminService) UnlockSelection(ctx context.Context) error {
	f.called = true
	return f.err
}

func (f *fakeRuntimeAdminService) UnlockSelectionByPort(ctx context.Context, portKey string) error {
	f.called = true
	f.portKey = portKey
	return f.err
}

func (f *fakeRuntimeAdminService) UpdateRuntimeSettings(ctx context.Context, runtimeMode string, poolAlgorithm string) error {
	f.called = true
	f.runtimeMode = runtimeMode
	f.poolAlgorithm = poolAlgorithm
	return f.err
}

func (f *fakeRuntimeAdminService) UpdateRuntimeSettingsByPort(ctx context.Context, portKey string, runtimeMode string, poolAlgorithm string) error {
	f.called = true
	f.portKey = portKey
	f.runtimeMode = runtimeMode
	f.poolAlgorithm = poolAlgorithm
	return f.err
}

type fakeEventLogService struct {
	items []map[string]any
	err   error
}

func (f *fakeEventLogService) ListEventLogs(ctx context.Context, limit int) ([]map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

type fakeDispatcherStatusService struct {
	status map[string]any
	err    error
}

func (f *fakeDispatcherStatusService) GetDispatcherStatus(ctx context.Context) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.status, nil
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
		summary["http_listen"] = "127.0.0.1:8777"
		summary["socks_listen"] = "127.0.0.1:8780"
	}
	return summary, nil
}

func (fakeRuntimeSummaryProvider) ListPortRuntimeStates(ctx context.Context) ([]model.PortRuntimeState, error) {
	return []model.PortRuntimeState{
		{PortKey: config.DefaultPortKey, RuntimeState: model.RuntimeState{RuntimeMode: "single_active", PoolAlgorithm: "sequential"}},
		{PortKey: "canary", RuntimeState: model.RuntimeState{RuntimeMode: "pool", PoolAlgorithm: "balance"}},
	}, nil
}

func routerDeps() web.Dependencies {
	return web.Dependencies{
		AdminUsername:     "admin",
		AdminPasswordHash: web.HashPassword("admin"),
		Ports: []config.PortConfig{
			{Key: config.DefaultPortKey, Name: "默认入口", HTTPListenAddr: "0.0.0.0", HTTPListenPort: 7777, SOCKSListenAddr: "0.0.0.0", SOCKSListenPort: 7780, RuntimeMode: "single_active", PoolAlgorithm: "sequential"},
			{Key: "canary", Name: "灰度入口", HTTPListenAddr: "127.0.0.1", HTTPListenPort: 8777, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 8780, RuntimeMode: "pool", PoolAlgorithm: "balance"},
		},
		RuntimeStateProvider: fakeRuntimeSummaryProvider{},
	}
}

func TestHealthEndpointIsPublic(t *testing.T) {
	r := web.NewRouter(web.Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestDashboardRequiresAuth(t *testing.T) {
	r := web.NewRouter(web.Dependencies{AdminUsername: "admin", AdminPasswordHash: web.HashPassword("admin")})
	for _, path := range []string{"/", "/api/runtime"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for %s, got %d", path, w.Code)
		}
	}
}

func TestSubscriptionEndpoints(t *testing.T) {
	service := &fakeSubscriptionService{payload: map[string]any{"name": "default", "url": "https://example.com/sub", "last_fetch_status": "success", "last_fetch_at": "2026-04-12T08:00:00Z"}}
	deps := routerDeps()
	deps.SubscriptionService = service
	r := web.NewRouter(deps)

	getReq := httptest.NewRequest(http.MethodGet, "/api/subscription", nil)
	getReq.SetBasicAuth("admin", "admin")
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected GET /api/subscription 200, got %d", getW.Code)
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/subscription/refresh", nil)
	postReq.SetBasicAuth("admin", "admin")
	postW := httptest.NewRecorder()
	r.ServeHTTP(postW, postReq)
	if postW.Code != http.StatusOK {
		t.Fatalf("expected POST /api/subscription/refresh 200, got %d", postW.Code)
	}
}

func TestEventsEndpoint(t *testing.T) {
	service := &fakeEventLogService{items: []map[string]any{{"event_type": "manual_switch", "message": "node switched manually"}}}
	deps := routerDeps()
	deps.EventLogService = service
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected events 200, got %d", w.Code)
	}
}

func TestDispatcherEndpoint(t *testing.T) {
	service := &fakeDispatcherStatusService{status: map[string]any{"enabled": true, "algorithm": "balance", "selected_port_key": "canary"}}
	deps := routerDeps()
	deps.DispatcherStatusService = service
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodGet, "/api/dispatcher", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected dispatcher 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"selected_port_key":"canary"`) {
		t.Fatalf("expected dispatcher body to contain selected port, got %s", w.Body.String())
	}
}

func TestRuntimeEndpointReturnsLaneFields(t *testing.T) {
	deps := routerDeps()
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected runtime 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"lane_count":2`) {
		t.Fatalf("expected lane_count in runtime response, got %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"lane_details"`) {
		t.Fatalf("expected lane_details in runtime response, got %s", w.Body.String())
	}
}

func TestRuntimeSettingsEndpoint(t *testing.T) {
	service := &fakeRuntimeAdminService{}
	deps := routerDeps()
	deps.RuntimeAdminService = service
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/settings", strings.NewReader(`{"runtime_mode":"pool","pool_algorithm":"random"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected settings 204, got %d", w.Code)
	}
	if service.runtimeMode != "pool" || service.poolAlgorithm != "random" || service.portKey != config.DefaultPortKey {
		t.Fatalf("unexpected runtime settings update: %s/%s/%s", service.portKey, service.runtimeMode, service.poolAlgorithm)
	}
}

func TestRuntimeSettingsByPortEndpoint(t *testing.T) {
	service := &fakeRuntimeAdminService{}
	deps := routerDeps()
	deps.RuntimeAdminService = service
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/api/ports/canary/runtime/settings", strings.NewReader(`{"runtime_mode":"pool","pool_algorithm":"balance"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected scoped settings 204, got %d", w.Code)
	}
	if service.portKey != "canary" || service.poolAlgorithm != "balance" {
		t.Fatalf("unexpected scoped runtime settings update: %s/%s", service.portKey, service.poolAlgorithm)
	}
}

func TestUnlockSelectionEndpoint(t *testing.T) {
	service := &fakeRuntimeAdminService{}
	deps := routerDeps()
	deps.RuntimeAdminService = service
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/unlock", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected unlock 204, got %d", w.Code)
	}
	if !service.called || service.portKey != config.DefaultPortKey {
		t.Fatal("expected default unlock service to be called")
	}
}

func TestUnlockSelectionByPortEndpoint(t *testing.T) {
	service := &fakeRuntimeAdminService{}
	deps := routerDeps()
	deps.RuntimeAdminService = service
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/api/ports/canary/runtime/unlock", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected scoped unlock 204, got %d", w.Code)
	}
	if !service.called || service.portKey != "canary" {
		t.Fatalf("expected canary unlock, got %s", service.portKey)
	}
}

func TestNodeEnableDisableEndpoints(t *testing.T) {
	service := &fakeNodeAdminService{}
	deps := routerDeps()
	deps.NodeAdminService = service
	r := web.NewRouter(deps)

	disableReq := httptest.NewRequest(http.MethodPost, "/api/nodes/9/disable", nil)
	disableReq.SetBasicAuth("admin", "admin")
	disableW := httptest.NewRecorder()
	r.ServeHTTP(disableW, disableReq)
	if disableW.Code != http.StatusNoContent {
		t.Fatalf("expected disable 204, got %d", disableW.Code)
	}
	if service.nodeID != 9 || !service.disabled || service.portKey != config.DefaultPortKey {
		t.Fatalf("expected disable node 9 on default, got node=%d disabled=%v port=%s", service.nodeID, service.disabled, service.portKey)
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/api/ports/canary/nodes/9/enable", nil)
	enableReq.SetBasicAuth("admin", "admin")
	enableW := httptest.NewRecorder()
	r.ServeHTTP(enableW, enableReq)
	if enableW.Code != http.StatusNoContent {
		t.Fatalf("expected enable 204, got %d", enableW.Code)
	}
	if service.nodeID != 9 || service.disabled || service.portKey != "canary" {
		t.Fatalf("expected enable node 9 on canary, got node=%d disabled=%v port=%s", service.nodeID, service.disabled, service.portKey)
	}
}

func TestPortsEndpoint(t *testing.T) {
	deps := routerDeps()
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodGet, "/api/ports", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected ports 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"key":"canary"`) {
		t.Fatalf("expected canary in ports payload, got %s", w.Body.String())
	}
}

func TestSwitchEndpointCallsManualSwitchService(t *testing.T) {
	service := &fakeManualSwitchService{}
	deps := routerDeps()
	deps.ManualSwitchService = service
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/7/switch", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if service.nodeID != 7 || service.portKey != config.DefaultPortKey {
		t.Fatalf("expected node id 7 on default, got %d/%s", service.nodeID, service.portKey)
	}
}

func TestSwitchByPortEndpointCallsManualSwitchService(t *testing.T) {
	service := &fakeManualSwitchService{}
	deps := routerDeps()
	deps.ManualSwitchService = service
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/api/ports/canary/nodes/7/switch", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if service.nodeID != 7 || service.portKey != "canary" {
		t.Fatalf("expected node id 7 on canary, got %d/%s", service.nodeID, service.portKey)
	}
}

func TestSwitchEndpointRejectsInvalidNodeID(t *testing.T) {
	deps := routerDeps()
	deps.ManualSwitchService = &fakeManualSwitchService{}
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/bad/switch", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSwitchEndpointSurfacesServiceError(t *testing.T) {
	service := &fakeManualSwitchService{err: context.DeadlineExceeded}
	deps := routerDeps()
	deps.ManualSwitchService = service
	r := web.NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/7/switch", nil)
	req.SetBasicAuth("admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
