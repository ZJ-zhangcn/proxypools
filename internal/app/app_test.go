package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"proxypools/internal/config"
	"proxypools/internal/model"
	"proxypools/internal/pool"
	"proxypools/internal/runtime"
	sqliteRepo "proxypools/internal/storage/sqlite"

	_ "modernc.org/sqlite"
)

type fakeSwitcher struct {
	calls   []string
	failTag map[string]error
}

func (f *fakeSwitcher) SwitchSelector(group, name string) error {
	f.calls = append(f.calls, group+":"+name)
	if f.failTag != nil {
		if err, ok := f.failTag[name]; ok {
			return err
		}
	}
	return nil
}

type fakeSubscriptionRefresher struct {
	nodes []model.Node
	err   error
}

func (f *fakeSubscriptionRefresher) Refresh(ctx context.Context, subscriptionURL string) ([]model.Node, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.nodes, nil
}

func TestNewReturnsApp(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	cfg := config.Default()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil app")
	}
}

func TestGetPrimarySubscriptionReturnsNotConfiguredStateWhenMissing(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "app.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "sing-box.json")
	cfg.SubscriptionURL = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}

	payload, err := a.GetPrimarySubscription(context.Background())
	if err != nil {
		t.Fatalf("expected empty subscription payload, got %v", err)
	}
	if payload["enabled"] != false {
		t.Fatalf("expected enabled=false, got %#v", payload["enabled"])
	}
	if payload["last_fetch_status"] != "not_configured" {
		t.Fatalf("expected not_configured status, got %#v", payload["last_fetch_status"])
	}
	if payload["url"] != "" {
		t.Fatalf("expected empty subscription url, got %#v", payload["url"])
	}
}

func TestRefreshSubscriptionReturnsNotConfiguredErrorWhenMissing(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "app.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "sing-box.json")
	cfg.SubscriptionURL = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}

	_, err = a.RefreshSubscription(context.Background())
	if err == nil || err.Error() != "subscription not configured" {
		t.Fatalf("expected subscription not configured error, got %v", err)
	}
}

func TestNewRestoresPersistedSubscriptionAndActiveNode(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "app.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "sing-box.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{
		{Key: config.DefaultPortKey},
		{Key: "canary", HTTPListenAddr: "127.0.0.1", HTTPListenPort: 8777, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 8780, RuntimeMode: "pool", PoolAlgorithm: "balance"},
	}

	repo, storedNodes := seedRepository(t, cfg.DBPath)
	state, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get runtime state failed: %v", err)
	}
	state.CurrentActiveNodeID = storedNodes[1].ID
	state.SelectionMode = "manual_locked"
	state.LastSwitchReason = "manual_switch"
	state.LastSwitchAt = "2026-04-12T07:23:17Z"
	if err := repo.UpdateRuntimeState(context.Background(), *state); err != nil {
		t.Fatalf("update runtime state failed: %v", err)
	}

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to restore persisted state, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()

	if !a.SubscriptionConfigured {
		t.Fatal("expected persisted nodes to mark subscription as configured")
	}
	content, err := os.ReadFile(cfg.SingboxConfigPath)
	if err != nil {
		t.Fatalf("read config failed: %v", err)
	}
	if !strings.Contains(string(content), fmt.Sprintf("\"default\":\"node-%d\"", storedNodes[1].ID)) {
		t.Fatalf("expected config default to restore node-%d, got %s", storedNodes[1].ID, string(content))
	}
	if !strings.Contains(string(content), `"tag":"http-in-canary"`) {
		t.Fatalf("expected canary inbound in config, got %s", string(content))
	}
	canaryState, err := repo.GetPortRuntimeState(context.Background(), "canary")
	if err != nil {
		t.Fatalf("get canary runtime state failed: %v", err)
	}
	if canaryState.RuntimeMode != "pool" || canaryState.PoolAlgorithm != "balance" {
		t.Fatalf("expected canary runtime settings to sync from config, got %#v", canaryState)
	}
}

func TestSwitchNodeUpdatesStateAndSelectors(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "switch.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "switch.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{
		{Key: config.DefaultPortKey},
		{Key: "canary", HTTPListenAddr: "127.0.0.1", HTTPListenPort: 8777, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 8780, RuntimeMode: "pool", PoolAlgorithm: "balance"},
	}

	_, storedNodes := seedRepository(t, cfg.DBPath)
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()

	switcher := &fakeSwitcher{}
	a.switcher = switcher

	if err := a.SwitchNodeByPort(context.Background(), "canary", storedNodes[1].ID); err != nil {
		t.Fatalf("expected SwitchNodeByPort to succeed, got %v", err)
	}
	if got, want := strings.Join(switcher.calls, ","), fmt.Sprintf("active-http-canary:node-%d,active-socks-canary:node-%d", storedNodes[1].ID, storedNodes[1].ID); got != want {
		t.Fatalf("expected selector calls %s, got %s", want, got)
	}
	state, err := a.repo.GetPortRuntimeState(context.Background(), "canary")
	if err != nil {
		t.Fatalf("get canary runtime state failed: %v", err)
	}
	if state.CurrentActiveNodeID != storedNodes[1].ID {
		t.Fatalf("expected active node %d, got %d", storedNodes[1].ID, state.CurrentActiveNodeID)
	}
	if state.SelectionMode != "manual_locked" {
		t.Fatalf("expected manual_locked, got %s", state.SelectionMode)
	}
	if state.LastSwitchReason != "manual_switch" {
		t.Fatalf("expected manual_switch reason, got %s", state.LastSwitchReason)
	}
}

func TestSwitchNodeRejectsMissingRuntimeStatus(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "missing-status.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "missing-status.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""

	_, storedNodes := seedRepository(t, cfg.DBPath)
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM node_runtime_status WHERE node_id = ?`, storedNodes[1].ID); err != nil {
		t.Fatalf("delete runtime status failed: %v", err)
	}

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()

	err = a.SwitchNode(context.Background(), storedNodes[1].ID)
	if err == nil || err.Error() != "node status not found" {
		t.Fatalf("expected node status not found, got %v", err)
	}
}

func TestUpdateRuntimeSettingsPersistsAndUpdatesConfig(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "settings.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "settings.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{
		{Key: config.DefaultPortKey},
		{Key: "canary", HTTPListenAddr: "127.0.0.1", HTTPListenPort: 8777, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 8780, RuntimeMode: "pool", PoolAlgorithm: "balance"},
	}

	repo, _ := seedRepository(t, cfg.DBPath)
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()

	if err := a.UpdateRuntimeSettingsByPort(context.Background(), "canary", "pool", "random"); err != nil {
		t.Fatalf("expected update runtime settings to succeed, got %v", err)
	}
	portCfg, err := portConfigByKey(a.Config, "canary")
	if err != nil {
		t.Fatalf("expected canary config to exist, got %v", err)
	}
	if portCfg.RuntimeMode != "pool" || portCfg.PoolAlgorithm != "random" {
		t.Fatalf("expected canary config to update, got %#v", portCfg)
	}
	state, err := repo.GetPortRuntimeState(context.Background(), "canary")
	if err != nil {
		t.Fatalf("get canary runtime state failed: %v", err)
	}
	if state.RuntimeMode != "pool" || state.PoolAlgorithm != "random" {
		t.Fatalf("expected runtime state to persist settings, got %s/%s", state.RuntimeMode, state.PoolAlgorithm)
	}
}

func TestUnlockSelectionSwitchesBackToAuto(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "unlock.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "unlock.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{
		{Key: config.DefaultPortKey},
		{Key: "canary", HTTPListenAddr: "127.0.0.1", HTTPListenPort: 8777, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 8780, RuntimeMode: "pool", PoolAlgorithm: "balance"},
	}

	repo, storedNodes := seedRepository(t, cfg.DBPath)

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()

	state, err := repo.GetPortRuntimeState(context.Background(), "canary")
	if err != nil {
		t.Fatalf("get canary runtime state failed: %v", err)
	}
	state.CurrentActiveNodeID = storedNodes[1].ID
	state.SelectionMode = "manual_locked"
	state.LastSwitchReason = "manual_switch"
	if err := repo.UpdatePortRuntimeState(context.Background(), *state); err != nil {
		t.Fatalf("update runtime state failed: %v", err)
	}

	if err := a.UnlockSelectionByPort(context.Background(), "canary"); err != nil {
		t.Fatalf("expected unlock to succeed, got %v", err)
	}
	updated, err := repo.GetPortRuntimeState(context.Background(), "canary")
	if err != nil {
		t.Fatalf("get runtime state failed: %v", err)
	}
	if updated.SelectionMode != "auto" {
		t.Fatalf("expected auto mode after unlock, got %s", updated.SelectionMode)
	}
	if updated.LastSwitchReason != "manual_unlock" {
		t.Fatalf("expected manual_unlock reason, got %s", updated.LastSwitchReason)
	}
}

func TestSetNodeManualDisabledUpdatesStatus(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "disable.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "disable.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{
		{Key: config.DefaultPortKey},
		{Key: "canary", HTTPListenAddr: "127.0.0.1", HTTPListenPort: 8777, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 8780, RuntimeMode: "pool", PoolAlgorithm: "balance"},
	}

	repo, storedNodes := seedRepository(t, cfg.DBPath)
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()

	if err := a.SetNodeManualDisabledByPort(context.Background(), "canary", storedNodes[1].ID, true); err != nil {
		t.Fatalf("expected disable to succeed, got %v", err)
	}
	statuses, err := repo.ListNodeRuntimeStatusesByPort(context.Background(), "canary")
	if err != nil {
		t.Fatalf("list statuses failed: %v", err)
	}
	for _, status := range statuses {
		if status.NodeID == storedNodes[1].ID && !status.ManualDisabled {
			t.Fatal("expected node to be manually disabled")
		}
	}

	if err := a.SetNodeManualDisabledByPort(context.Background(), "canary", storedNodes[1].ID, false); err != nil {
		t.Fatalf("expected enable to succeed, got %v", err)
	}
	statuses, err = repo.ListNodeRuntimeStatusesByPort(context.Background(), "canary")
	if err != nil {
		t.Fatalf("list statuses failed: %v", err)
	}
	for _, status := range statuses {
		if status.NodeID == storedNodes[1].ID && status.ManualDisabled {
			t.Fatal("expected node to be re-enabled")
		}
	}
}

func TestRefreshSubscriptionPreservesActiveNodeAndUpdatesFetchResult(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "refresh-success.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "refresh-success.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{
		{Key: config.DefaultPortKey},
		{Key: "canary", HTTPListenAddr: "127.0.0.1", HTTPListenPort: 8777, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 8780, RuntimeMode: "pool", PoolAlgorithm: "balance"},
	}

	repo, storedNodes := seedRepository(t, cfg.DBPath)
	state, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get runtime state failed: %v", err)
	}
	state.CurrentActiveNodeID = storedNodes[1].ID
	state.SelectionMode = "manual_locked"
	state.LastSwitchReason = "manual_switch"
	state.LastSwitchAt = "2026-04-12T07:23:17Z"
	if err := repo.UpdateRuntimeState(context.Background(), *state); err != nil {
		t.Fatalf("update runtime state failed: %v", err)
	}

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()

	canaryState, err := repo.GetPortRuntimeState(context.Background(), "canary")
	if err != nil {
		t.Fatalf("get canary runtime state failed: %v", err)
	}
	canaryState.CurrentActiveNodeID = storedNodes[0].ID
	canaryState.SelectionMode = "manual_locked"
	canaryState.LastSwitchReason = "manual_switch"
	canaryState.LastSwitchAt = "2026-04-12T07:23:17Z"
	if err := repo.UpdatePortRuntimeState(context.Background(), *canaryState); err != nil {
		t.Fatalf("update canary runtime state failed: %v", err)
	}
	if err := repo.SetPortNodeManualDisabled(context.Background(), "canary", storedNodes[1].ID, true); err != nil {
		t.Fatalf("disable canary node failed: %v", err)
	}
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: "canary", LaneKey: "lane-http-1", Protocol: "http", AssignedNodeID: storedNodes[0].ID, State: "ready", LastSwitchReason: "lane_allocator_assigned", LastSwitchAt: "2026-04-12T07:23:17Z"}); err != nil {
		t.Fatalf("seed canary lane failed: %v", err)
	}

	a.subscriptionService = &fakeSubscriptionRefresher{nodes: []model.Node{
		{SourceKey: "node-1", Name: "node-1", ProtocolType: "shadowsocks", Server: "1.1.1.1", Port: 1001, PayloadJSON: `{"type":"shadowsocks","server":"1.1.1.1","server_port":1001,"method":"aes-256-gcm","password":"p1"}`, Enabled: true},
		{SourceKey: "node-2", Name: "node-2", ProtocolType: "shadowsocks", Server: "2.2.2.2", Port: 1002, PayloadJSON: `{"type":"shadowsocks","server":"2.2.2.2","server_port":1002,"method":"aes-256-gcm","password":"p2"}`, Enabled: true},
		{SourceKey: "node-3", Name: "node-3", ProtocolType: "shadowsocks", Server: "3.3.3.3", Port: 1003, PayloadJSON: `{"type":"shadowsocks","server":"3.3.3.3","server_port":1003,"method":"aes-256-gcm","password":"p3"}`, Enabled: true},
	}}

	payload, err := a.RefreshSubscription(context.Background())
	if err != nil {
		t.Fatalf("expected RefreshSubscription to succeed, got %v", err)
	}
	if payload["last_added_nodes"] != 1 {
		t.Fatalf("expected 1 added node, got %#v", payload["last_added_nodes"])
	}
	if payload["last_removed_nodes"] != 0 {
		t.Fatalf("expected 0 removed nodes, got %#v", payload["last_removed_nodes"])
	}
	if payload["last_fetch_status"] != "success" {
		t.Fatalf("expected success fetch status, got %#v", payload["last_fetch_status"])
	}

	nodes, err := repo.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("list nodes failed: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected 3 stored nodes, got %d", len(nodes))
	}
	preservedNode1ID := int64(0)
	preservedNode2ID := int64(0)
	for _, node := range nodes {
		if node.SourceKey == "node-1" {
			preservedNode1ID = node.ID
		}
		if node.SourceKey == "node-2" {
			preservedNode2ID = node.ID
		}
	}
	if preservedNode1ID == 0 || preservedNode2ID == 0 {
		t.Fatal("expected preserved nodes to exist after refresh")
	}
	updatedState, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get updated runtime state failed: %v", err)
	}
	if updatedState.CurrentActiveNodeID != preservedNode2ID {
		t.Fatalf("expected default active node to preserve source key node-2, got %d", updatedState.CurrentActiveNodeID)
	}
	if updatedState.SelectionMode != "manual_locked" {
		t.Fatalf("expected default manual_locked to persist, got %s", updatedState.SelectionMode)
	}
	updatedCanaryState, err := repo.GetPortRuntimeState(context.Background(), "canary")
	if err != nil {
		t.Fatalf("get updated canary runtime state failed: %v", err)
	}
	if updatedCanaryState.CurrentActiveNodeID != preservedNode1ID {
		t.Fatalf("expected canary active node to preserve source key node-1, got %d", updatedCanaryState.CurrentActiveNodeID)
	}
	if updatedCanaryState.SelectionMode != "manual_locked" {
		t.Fatalf("expected canary manual_locked to persist, got %s", updatedCanaryState.SelectionMode)
	}
	statuses, err := repo.ListNodeRuntimeStatusesByPort(context.Background(), "canary")
	if err != nil {
		t.Fatalf("list canary statuses failed: %v", err)
	}
	for _, status := range statuses {
		if status.NodeID == preservedNode2ID && !status.ManualDisabled {
			t.Fatal("expected canary manual disabled flag to follow source key node-2")
		}
	}
	lanes, err := repo.ListRequestLaneStatesByPort(context.Background(), "canary")
	if err != nil {
		t.Fatalf("list canary lanes failed: %v", err)
	}
	foundLane := false
	for _, lane := range lanes {
		if lane.LaneKey == "lane-http-1" && lane.AssignedNodeID == preservedNode1ID {
			foundLane = true
		}
	}
	if !foundLane {
		t.Fatal("expected canary lane to preserve source key node-1 assignment")
	}
}

func TestRefreshSubscriptionFailureKeepsExistingNodes(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "refresh-failure.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "refresh-failure.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""

	repo, _ := seedRepository(t, cfg.DBPath)
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()

	a.subscriptionService = &fakeSubscriptionRefresher{err: errors.New("refresh failed")}
	_, err = a.RefreshSubscription(context.Background())
	if err == nil || err.Error() != "refresh failed" {
		t.Fatalf("expected refresh failed error, got %v", err)
	}

	sub, err := repo.GetPrimarySubscription(context.Background())
	if err != nil {
		t.Fatalf("get primary subscription failed: %v", err)
	}
	if sub.LastFetchStatus != "error" {
		t.Fatalf("expected error fetch status, got %s", sub.LastFetchStatus)
	}
	if sub.LastFetchError != "refresh failed" {
		t.Fatalf("expected refresh failed fetch error, got %s", sub.LastFetchError)
	}
	nodes, err := repo.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("list nodes failed: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected existing nodes to remain, got %d", len(nodes))
	}
}

func TestRunHealthCheckRandomPoolSelectsAnotherHealthyNode(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.RuntimeMode = "pool"
	cfg.PoolAlgorithm = "random"
	dbPath := filepath.Join(tmpDir, "pool-random-health.db")
	repo, storedNodes := seedRepository(t, dbPath)
	first := storedNodes[0]
	second := storedNodes[1]

	status, err := repo.ListNodeRuntimeStatuses(context.Background())
	if err != nil {
		t.Fatalf("list statuses failed: %v", err)
	}
	for _, item := range status {
		item.State = "active"
		if err := repo.UpsertNodeRuntimeStatus(context.Background(), item); err != nil {
			t.Fatalf("update status failed: %v", err)
		}
	}
	state, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get runtime state failed: %v", err)
	}
	state.CurrentActiveNodeID = first.ID
	state.SelectionMode = "auto"
	if err := repo.UpdateRuntimeState(context.Background(), *state); err != nil {
		t.Fatalf("update runtime state failed: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	healthSwitcher := &fakeSwitcher{}
	activeSwitcher := &fakeSwitcher{}
	proc := &runtime.Process{Binary: writeFakeSingBox(t, tmpDir), Config: filepath.Join(tmpDir, "pool-random-health.json")}
	if err := proc.Start(); err != nil {
		t.Fatalf("start fake runtime failed: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = proc.Stop(shutdownCtx)
	}()
	a := &App{
		Config:   cfg,
		Runtime:  proc,
		repo:     repo,
		checker:  pool.NewChecker(pool.CheckerConfig{ProbeURL: ts.URL, SelectorSwitcher: healthSwitcher}),
		switcher: activeSwitcher,
	}

	a.runHealthCheck(context.Background())

	updatedState, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get updated runtime state failed: %v", err)
	}
	if updatedState.CurrentActiveNodeID != second.ID {
		t.Fatalf("expected random rotate to node %d, got %d", second.ID, updatedState.CurrentActiveNodeID)
	}
	if updatedState.LastSwitchReason != "pool_random_rotate" {
		t.Fatalf("expected pool_random_rotate, got %s", updatedState.LastSwitchReason)
	}
}

func TestRunHealthCheckBalancePoolPrefersHigherScoreNode(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.RuntimeMode = "pool"
	cfg.PoolAlgorithm = "balance"
	dbPath := filepath.Join(tmpDir, "pool-balance-health.db")
	repo, storedNodes := seedRepository(t, dbPath)
	first := storedNodes[0]
	second := storedNodes[1]

	statuses, err := repo.ListNodeRuntimeStatuses(context.Background())
	if err != nil {
		t.Fatalf("list statuses failed: %v", err)
	}
	for _, item := range statuses {
		item.State = "active"
		if item.NodeID == second.ID {
			item.Score = 99
			item.Tier = "L1"
		}
		if item.NodeID == first.ID {
			item.Score = 80
			item.Tier = "L1"
		}
		if err := repo.UpsertNodeRuntimeStatus(context.Background(), item); err != nil {
			t.Fatalf("update status failed: %v", err)
		}
	}
	state, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get runtime state failed: %v", err)
	}
	state.CurrentActiveNodeID = first.ID
	state.SelectionMode = "auto"
	state.RuntimeMode = "pool"
	state.PoolAlgorithm = "balance"
	if err := repo.UpdateRuntimeState(context.Background(), *state); err != nil {
		t.Fatalf("update runtime state failed: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	healthSwitcher := &fakeSwitcher{}
	activeSwitcher := &fakeSwitcher{}
	proc := &runtime.Process{Binary: writeFakeSingBox(t, tmpDir), Config: filepath.Join(tmpDir, "pool-balance-health.json")}
	if err := proc.Start(); err != nil {
		t.Fatalf("start fake runtime failed: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = proc.Stop(shutdownCtx)
	}()
	a := &App{
		Config:   cfg,
		Runtime:  proc,
		repo:     repo,
		checker:  pool.NewChecker(pool.CheckerConfig{ProbeURL: ts.URL, SelectorSwitcher: healthSwitcher}),
		switcher: activeSwitcher,
	}

	a.runHealthCheck(context.Background())

	updatedState, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get updated runtime state failed: %v", err)
	}
	if updatedState.CurrentActiveNodeID != second.ID {
		t.Fatalf("expected balance rotate to node %d, got %d", second.ID, updatedState.CurrentActiveNodeID)
	}
	if updatedState.LastSwitchReason != "pool_balance_rotate" {
		t.Fatalf("expected pool_balance_rotate, got %s", updatedState.LastSwitchReason)
	}
}

func TestRunHealthCheckSequentialPoolRotatesHealthyNodes(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.RuntimeMode = "pool"
	cfg.PoolAlgorithm = "sequential"
	dbPath := filepath.Join(tmpDir, "pool-health.db")
	repo, storedNodes := seedRepository(t, dbPath)
	first := storedNodes[0]
	second := storedNodes[1]

	status, err := repo.ListNodeRuntimeStatuses(context.Background())
	if err != nil {
		t.Fatalf("list statuses failed: %v", err)
	}
	for _, item := range status {
		item.State = "active"
		if err := repo.UpsertNodeRuntimeStatus(context.Background(), item); err != nil {
			t.Fatalf("update status failed: %v", err)
		}
	}
	state, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get runtime state failed: %v", err)
	}
	state.CurrentActiveNodeID = first.ID
	state.SelectionMode = "auto"
	if err := repo.UpdateRuntimeState(context.Background(), *state); err != nil {
		t.Fatalf("update runtime state failed: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	healthSwitcher := &fakeSwitcher{}
	activeSwitcher := &fakeSwitcher{}
	proc := &runtime.Process{Binary: writeFakeSingBox(t, tmpDir), Config: filepath.Join(tmpDir, "pool-health.json")}
	if err := proc.Start(); err != nil {
		t.Fatalf("start fake runtime failed: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = proc.Stop(shutdownCtx)
	}()
	a := &App{
		Config:   cfg,
		Runtime:  proc,
		repo:     repo,
		checker:  pool.NewChecker(pool.CheckerConfig{ProbeURL: ts.URL, SelectorSwitcher: healthSwitcher}),
		switcher: activeSwitcher,
	}

	a.runHealthCheck(context.Background())

	updatedState, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get updated runtime state failed: %v", err)
	}
	if updatedState.CurrentActiveNodeID != second.ID {
		t.Fatalf("expected sequential rotate to node %d, got %d", second.ID, updatedState.CurrentActiveNodeID)
	}
	if updatedState.LastSwitchReason != "pool_sequential_rotate" {
		t.Fatalf("expected pool_sequential_rotate, got %s", updatedState.LastSwitchReason)
	}
}

func TestRunHealthCheckKeepsManualLockedOnFailover(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "health.db")
	repo, storedNodes := seedRepository(t, dbPath)
	first := storedNodes[0]
	second := storedNodes[1]

	status, err := repo.ListNodeRuntimeStatuses(context.Background())
	if err != nil {
		t.Fatalf("list statuses failed: %v", err)
	}
	for _, item := range status {
		if item.NodeID == first.ID {
			item.State = "active"
			if err := repo.UpsertNodeRuntimeStatus(context.Background(), item); err != nil {
				t.Fatalf("update first status failed: %v", err)
			}
		}
		if item.NodeID == second.ID {
			item.State = "active"
			if err := repo.UpsertNodeRuntimeStatus(context.Background(), item); err != nil {
				t.Fatalf("update second status failed: %v", err)
			}
		}
	}
	state, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get runtime state failed: %v", err)
	}
	state.CurrentActiveNodeID = first.ID
	state.SelectionMode = "manual_locked"
	if err := repo.UpdateRuntimeState(context.Background(), *state); err != nil {
		t.Fatalf("update runtime state failed: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	healthSwitcher := &fakeSwitcher{failTag: map[string]error{fmt.Sprintf("node-%d", first.ID): fmt.Errorf("probe failed")}}
	activeSwitcher := &fakeSwitcher{}
	proc := &runtime.Process{Binary: writeFakeSingBox(t, tmpDir), Config: filepath.Join(tmpDir, "health-check.json")}
	if err := proc.Start(); err != nil {
		t.Fatalf("start fake runtime failed: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = proc.Stop(shutdownCtx)
	}()
	a := &App{
		Runtime:  proc,
		repo:     repo,
		checker:  pool.NewChecker(pool.CheckerConfig{ProbeURL: ts.URL, SelectorSwitcher: healthSwitcher}),
		switcher: activeSwitcher,
	}

	a.runHealthCheck(context.Background())

	updatedState, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get updated runtime state failed: %v", err)
	}
	if updatedState.CurrentActiveNodeID != second.ID {
		t.Fatalf("expected failover to node %d, got %d", second.ID, updatedState.CurrentActiveNodeID)
	}
	if updatedState.SelectionMode != "manual_locked" {
		t.Fatalf("expected manual_locked to persist, got %s", updatedState.SelectionMode)
	}
	if updatedState.LastSwitchReason != "manual_locked_failover" {
		t.Fatalf("expected manual_locked_failover, got %s", updatedState.LastSwitchReason)
	}
	if got, want := strings.Join(activeSwitcher.calls, ","), fmt.Sprintf("active-http:node-%d,active-socks:node-%d", second.ID, second.ID); got != want {
		t.Fatalf("expected selector calls %s, got %s", want, got)
	}
}

func writeFakeSingBox(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-sing-box.sh")
	content := "#!/bin/sh\ncase \"$1\" in\n  check) exit 0 ;;\n  run) trap 'exit 0' INT TERM; while true; do sleep 1; done ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake sing-box failed: %v", err)
	}
	return path
}

func TestNewStartsDispatcherWhenEnabled(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "dispatcher.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "dispatcher.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{{Key: config.DefaultPortKey, HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28091, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28092}}
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 28081
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 28082

	repo, _ := seedRepository(t, cfg.DBPath)
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-1", Protocol: "http", AssignedNodeID: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed default http lane failed: %v", err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("parse backend url failed: %v", err)
	}
	portState, err := repo.GetPortRuntimeState(context.Background(), config.DefaultPortKey)
	if err != nil {
		t.Fatalf("get default runtime state failed: %v", err)
	}
	if portState.CurrentActiveNodeID == 0 {
		portState.CurrentActiveNodeID = 1
		if err := repo.UpdatePortRuntimeState(context.Background(), *portState); err != nil {
			t.Fatalf("update default runtime state failed: %v", err)
		}
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed with dispatcher, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()
	a.dispatcher.ports = []config.PortConfig{{Key: config.DefaultPortKey, HTTPListenAddr: backendURL.Hostname(), HTTPListenPort: mustBasePortForLane(t, mustPortFromURL(t, backendURL), "lane-http-1"), SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28092}}
	if a.dispatcher == nil {
		t.Fatal("expected dispatcher to be initialized")
	}
	resp, err := http.Get("http://127.0.0.1:28081")
	if err != nil {
		t.Fatalf("dispatcher request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected dispatcher 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Port"); got != config.DefaultPortKey {
		t.Fatalf("expected dispatcher to select default port, got %s", got)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Lane"); got != "lane-http-1" {
		t.Fatalf("expected dispatcher to select lane-http-1, got %s", got)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Target"); got != backendURL.Host {
		t.Fatalf("expected dispatcher target %s, got %s", backendURL.Host, got)
	}
	state, err := repo.GetRequestLaneState(context.Background(), config.DefaultPortKey, "lane-http-1")
	if err != nil {
		t.Fatalf("get lane state failed: %v", err)
	}
	if state.LastUsedAt == "" {
		t.Fatal("expected lane last_used_at to be updated")
	}
	if state.State != "ready" {
		t.Fatalf("expected lane state ready, got %s", state.State)
	}
}

func TestDispatcherStickyKeySelectsStableLane(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "dispatcher-sticky.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "dispatcher-sticky.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{{Key: config.DefaultPortKey, HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28201, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28202}}
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 28203
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 28204

	repo, _ := seedRepository(t, cfg.DBPath)
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-1", Protocol: "http", AssignedNodeID: 1, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-1 failed: %v", err)
	}
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-2", Protocol: "http", AssignedNodeID: 2, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-2 failed: %v", err)
	}
	lane1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Lane", "lane-http-1")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer lane1.Close()
	lane2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Lane", "lane-http-2")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer lane2.Close()
	lane1URL, _ := url.Parse(lane1.URL)
	lane2URL, _ := url.Parse(lane2.URL)

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed with sticky dispatcher, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()
	a.dispatcher.ports = []config.PortConfig{{
		Key:             config.DefaultPortKey,
		HTTPListenAddr:  "127.0.0.1",
		HTTPListenPort:  27200,
		SOCKSListenAddr: "127.0.0.1",
		SOCKSListenPort: 27201,
		Lanes: []config.LaneConfig{
			{Key: "lane-http-1", Protocol: "http", ListenAddr: lane1URL.Hostname(), ListenPort: mustPortFromURL(t, lane1URL), Weight: 1},
			{Key: "lane-http-2", Protocol: "http", ListenAddr: lane2URL.Hostname(), ListenPort: mustPortFromURL(t, lane2URL), Weight: 1},
		},
	}}

	req1, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:28203", nil)
	req1.Header.Set("X-ProxyPools-Sticky-Key", "user-42")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("first sticky request failed: %v", err)
	}
	defer resp1.Body.Close()
	laneKey1 := resp1.Header.Get("X-ProxyPools-Dispatcher-Lane")
	if laneKey1 == "" {
		t.Fatal("expected dispatcher lane header on first sticky request")
	}

	req2, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:28203", nil)
	req2.Header.Set("X-ProxyPools-Sticky-Key", "user-42")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second sticky request failed: %v", err)
	}
	defer resp2.Body.Close()
	laneKey2 := resp2.Header.Get("X-ProxyPools-Dispatcher-Lane")
	if laneKey1 != laneKey2 {
		t.Fatalf("expected sticky lane to remain stable, got %s then %s", laneKey1, laneKey2)
	}
}

func TestDispatcherRuleSelectsLaneByHost(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "dispatcher-rule-host.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "dispatcher-rule-host.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{{Key: config.DefaultPortKey, HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28301, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28302}}
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 28303
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 28304
	cfg.Dispatcher.Rules = []config.DispatcherRuleConfig{{
		Name:          "host-route",
		Host:          "api.example.com",
		TargetPortKey: config.DefaultPortKey,
		TargetLaneKey: "lane-http-2",
	}}

	repo, _ := seedRepository(t, cfg.DBPath)
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-1", Protocol: "http", AssignedNodeID: 1, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-1 failed: %v", err)
	}
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-2", Protocol: "http", AssignedNodeID: 2, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-2 failed: %v", err)
	}
	lane1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Lane", "lane-http-1")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer lane1.Close()
	lane2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Lane", "lane-http-2")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer lane2.Close()
	lane1URL, _ := url.Parse(lane1.URL)
	lane2URL, _ := url.Parse(lane2.URL)

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed with host rule dispatcher, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()
	if a.dispatcher == nil {
		t.Fatal("expected dispatcher to be initialized")
	}
	a.dispatcher.ports = []config.PortConfig{{
		Key:             config.DefaultPortKey,
		HTTPListenAddr:  "127.0.0.1",
		HTTPListenPort:  27300,
		SOCKSListenAddr: "127.0.0.1",
		SOCKSListenPort: 27301,
		Lanes: []config.LaneConfig{
			{Key: "lane-http-1", Protocol: "http", ListenAddr: lane1URL.Hostname(), ListenPort: mustPortFromURL(t, lane1URL), Weight: 1},
			{Key: "lane-http-2", Protocol: "http", ListenAddr: lane2URL.Hostname(), ListenPort: mustPortFromURL(t, lane2URL), Weight: 1},
		},
	}}

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:28303", nil)
	req.Host = "api.example.com:443"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("host rule request failed: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Lane"); got != "lane-http-2" {
		t.Fatalf("expected host rule to select lane-http-2, got %s", got)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Target"); got != lane2URL.Host {
		t.Fatalf("expected host rule target %s, got %s", lane2URL.Host, got)
	}
	if got := resp.Header.Get("X-Upstream-Lane"); got != "lane-http-2" {
		t.Fatalf("expected host rule upstream lane-http-2, got %s", got)
	}
}

func TestDispatcherRuleSelectsLaneByHeader(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "dispatcher-rule-header.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "dispatcher-rule-header.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{{Key: config.DefaultPortKey, HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28401, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28402}}
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 28403
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 28404
	cfg.Dispatcher.Rules = []config.DispatcherRuleConfig{{
		Name:          "header-route",
		HeaderName:    "X-Tenant",
		HeaderValue:   "blue",
		TargetPortKey: config.DefaultPortKey,
		TargetLaneKey: "lane-http-2",
	}}

	repo, _ := seedRepository(t, cfg.DBPath)
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-1", Protocol: "http", AssignedNodeID: 1, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-1 failed: %v", err)
	}
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-2", Protocol: "http", AssignedNodeID: 2, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-2 failed: %v", err)
	}
	lane1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Lane", "lane-http-1")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer lane1.Close()
	lane2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Lane", "lane-http-2")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer lane2.Close()
	lane1URL, _ := url.Parse(lane1.URL)
	lane2URL, _ := url.Parse(lane2.URL)

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed with header rule dispatcher, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()
	if a.dispatcher == nil {
		t.Fatal("expected dispatcher to be initialized")
	}
	a.dispatcher.ports = []config.PortConfig{{
		Key:             config.DefaultPortKey,
		HTTPListenAddr:  "127.0.0.1",
		HTTPListenPort:  27400,
		SOCKSListenAddr: "127.0.0.1",
		SOCKSListenPort: 27401,
		Lanes: []config.LaneConfig{
			{Key: "lane-http-1", Protocol: "http", ListenAddr: lane1URL.Hostname(), ListenPort: mustPortFromURL(t, lane1URL), Weight: 1},
			{Key: "lane-http-2", Protocol: "http", ListenAddr: lane2URL.Hostname(), ListenPort: mustPortFromURL(t, lane2URL), Weight: 1},
		},
	}}

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:28403", nil)
	req.Header.Set("X-Tenant", "blue")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("header rule request failed: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Lane"); got != "lane-http-2" {
		t.Fatalf("expected header rule to select lane-http-2, got %s", got)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Target"); got != lane2URL.Host {
		t.Fatalf("expected header rule target %s, got %s", lane2URL.Host, got)
	}
	if got := resp.Header.Get("X-Upstream-Lane"); got != "lane-http-2" {
		t.Fatalf("expected header rule upstream lane-http-2, got %s", got)
	}
}

func TestDispatcherRuleFallbackRetriesNextLane(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "dispatcher-rule-fallback.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "dispatcher-rule-fallback.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{{Key: config.DefaultPortKey, HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28501, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28502}}
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 28503
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 28504
	cfg.Dispatcher.Rules = []config.DispatcherRuleConfig{{
		Name:          "host-route",
		Host:          "api.example.com",
		TargetPortKey: config.DefaultPortKey,
		TargetLaneKey: "lane-http-1",
	}}

	repo, _ := seedRepository(t, cfg.DBPath)
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-1", Protocol: "http", AssignedNodeID: 1, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-1 failed: %v", err)
	}
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-2", Protocol: "http", AssignedNodeID: 2, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-2 failed: %v", err)
	}
	failureLane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer failureLane.Close()
	successLane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Lane", "lane-http-2")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer successLane.Close()
	failureURL, _ := url.Parse(failureLane.URL)
	successURL, _ := url.Parse(successLane.URL)

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed with rule fallback dispatcher, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()
	if a.dispatcher == nil {
		t.Fatal("expected dispatcher to be initialized")
	}
	a.dispatcher.ports = []config.PortConfig{{
		Key:             config.DefaultPortKey,
		HTTPListenAddr:  "127.0.0.1",
		HTTPListenPort:  27500,
		SOCKSListenAddr: "127.0.0.1",
		SOCKSListenPort: 27501,
		Lanes: []config.LaneConfig{
			{Key: "lane-http-1", Protocol: "http", ListenAddr: failureURL.Hostname(), ListenPort: mustPortFromURL(t, failureURL), Weight: 1},
			{Key: "lane-http-2", Protocol: "http", ListenAddr: successURL.Hostname(), ListenPort: mustPortFromURL(t, successURL), Weight: 1},
		},
	}}

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:28503", nil)
	req.Host = "api.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("rule fallback request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected rule fallback request 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Lane"); got != "lane-http-2" {
		t.Fatalf("expected rule fallback to lane-http-2, got %s", got)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Target"); got != successURL.Host {
		t.Fatalf("expected rule fallback target %s, got %s", successURL.Host, got)
	}
	if got := resp.Header.Get("X-Upstream-Lane"); got != "lane-http-2" {
		t.Fatalf("expected rule fallback upstream lane-http-2, got %s", got)
	}
	failedState, err := repo.GetRequestLaneState(context.Background(), config.DefaultPortKey, "lane-http-1")
	if err != nil {
		t.Fatalf("get failed lane state failed: %v", err)
	}
	if failedState.LastErrorAt == "" {
		t.Fatal("expected targeted lane last_error_at to be updated after fallback")
	}
	successState, err := repo.GetRequestLaneState(context.Background(), config.DefaultPortKey, "lane-http-2")
	if err != nil {
		t.Fatalf("get success lane state failed: %v", err)
	}
	if successState.LastUsedAt == "" {
		t.Fatal("expected fallback lane last_used_at to be updated")
	}
}

func TestDispatcherRuleOverridesStickyKey(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "dispatcher-rule-sticky-override.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "dispatcher-rule-sticky-override.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{{Key: config.DefaultPortKey, HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28601, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28602}}
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 28603
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 28604
	cfg.Dispatcher.Rules = []config.DispatcherRuleConfig{{
		Name:          "host-route",
		Host:          "api.example.com",
		TargetPortKey: config.DefaultPortKey,
		TargetLaneKey: "lane-http-2",
	}}

	repo, _ := seedRepository(t, cfg.DBPath)
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-1", Protocol: "http", AssignedNodeID: 1, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-1 failed: %v", err)
	}
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-2", Protocol: "http", AssignedNodeID: 2, Weight: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed lane-http-2 failed: %v", err)
	}
	lane1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Lane", "lane-http-1")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer lane1.Close()
	lane2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Lane", "lane-http-2")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer lane2.Close()
	lane1URL, _ := url.Parse(lane1.URL)
	lane2URL, _ := url.Parse(lane2.URL)

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed with sticky override dispatcher, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()
	if a.dispatcher == nil {
		t.Fatal("expected dispatcher to be initialized")
	}
	a.dispatcher.ports = []config.PortConfig{{
		Key:             config.DefaultPortKey,
		HTTPListenAddr:  "127.0.0.1",
		HTTPListenPort:  27600,
		SOCKSListenAddr: "127.0.0.1",
		SOCKSListenPort: 27601,
		Lanes: []config.LaneConfig{
			{Key: "lane-http-1", Protocol: "http", ListenAddr: lane1URL.Hostname(), ListenPort: mustPortFromURL(t, lane1URL), Weight: 1},
			{Key: "lane-http-2", Protocol: "http", ListenAddr: lane2URL.Hostname(), ListenPort: mustPortFromURL(t, lane2URL), Weight: 1},
		},
	}}

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:28603", nil)
	req.Host = "api.example.com"
	req.Header.Set("X-ProxyPools-Sticky-Key", "force-lane-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sticky override request failed: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Lane"); got != "lane-http-2" {
		t.Fatalf("expected host rule to override sticky and select lane-http-2, got %s", got)
	}
	if got := resp.Header.Get("X-Upstream-Lane"); got != "lane-http-2" {
		t.Fatalf("expected host rule to override sticky upstream lane-http-2, got %s", got)
	}
}

func TestDispatcherFallbackRetriesNextPort(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "dispatcher-fallback.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "dispatcher-fallback.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{
		{Key: config.DefaultPortKey, HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28093, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28094},
		{Key: "standby", HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28095, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28096, RuntimeMode: "pool", PoolAlgorithm: "balance"},
	}
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 28085
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 28086
	cfg.Dispatcher.Algorithm = "sequential"

	repo, storedNodes := seedRepository(t, cfg.DBPath)
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-http-1", Protocol: "http", AssignedNodeID: storedNodes[0].ID, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed default lane failed: %v", err)
	}
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: "standby", LaneKey: "lane-http-1", Protocol: "http", AssignedNodeID: storedNodes[1].ID, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed standby lane failed: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed with dispatcher fallback, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()

	state, err := repo.GetRuntimeState(context.Background())
	if err != nil {
		t.Fatalf("get default runtime state failed: %v", err)
	}
	state.CurrentActiveNodeID = storedNodes[0].ID
	if err := repo.UpdateRuntimeState(context.Background(), *state); err != nil {
		t.Fatalf("update default runtime state failed: %v", err)
	}
	canaryState, err := repo.GetPortRuntimeState(context.Background(), "standby")
	if err != nil {
		t.Fatalf("get standby runtime state failed: %v", err)
	}
	canaryState.CurrentActiveNodeID = storedNodes[1].ID
	if err := repo.UpdatePortRuntimeState(context.Background(), *canaryState); err != nil {
		t.Fatalf("update standby runtime state failed: %v", err)
	}
	_ = a.dispatcher.rebuildSnapshot(context.Background())

	failureBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer failureBackend.Close()
	successBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "canary")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer successBackend.Close()
	failureURL, _ := url.Parse(failureBackend.URL)
	successURL, _ := url.Parse(successBackend.URL)
	if a.dispatcher == nil {
		t.Fatal("expected dispatcher to be initialized")
	}
	a.dispatcher.ports = []config.PortConfig{
		{Key: config.DefaultPortKey, HTTPListenAddr: failureURL.Hostname(), HTTPListenPort: mustBasePortForLane(t, mustPortFromURL(t, failureURL), "lane-http-1"), SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28094},
		{Key: "standby", HTTPListenAddr: successURL.Hostname(), HTTPListenPort: mustBasePortForLane(t, mustPortFromURL(t, successURL), "lane-http-1"), SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28096, RuntimeMode: "pool", PoolAlgorithm: "balance"},
	}

	resp, err := http.Get("http://127.0.0.1:28085")
	if err != nil {
		t.Fatalf("dispatcher fallback request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected fallback request 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Port"); got != "standby" {
		t.Fatalf("expected fallback to standby, got %s", got)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Lane"); got != "lane-http-1" {
		t.Fatalf("expected fallback lane lane-http-1, got %s", got)
	}
	if got := resp.Header.Get("X-ProxyPools-Dispatcher-Target"); got != successURL.Host {
		t.Fatalf("expected fallback target %s, got %s", successURL.Host, got)
	}
	if got := resp.Header.Get("X-Upstream"); got != "canary" {
		t.Fatalf("expected canary upstream header, got %s", got)
	}
	laneState, err := repo.GetRequestLaneState(context.Background(), "standby", "lane-http-1")
	if err != nil {
		t.Fatalf("get standby lane state failed: %v", err)
	}
	if laneState.LastUsedAt == "" {
		t.Fatal("expected standby lane last_used_at to be updated")
	}
	fallbackState, err := repo.GetRequestLaneState(context.Background(), config.DefaultPortKey, "lane-http-1")
	if err != nil {
		t.Fatalf("get default lane state failed: %v", err)
	}
	if fallbackState.LastErrorAt == "" {
		t.Fatal("expected default lane last_error_at to be updated after fallback")
	}

	secondResp, err := http.Get("http://127.0.0.1:28085")
	if err != nil {
		t.Fatalf("second dispatcher fallback request failed: %v", err)
	}
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected second fallback request 204, got %d", secondResp.StatusCode)
	}
	if got := secondResp.Header.Get("X-ProxyPools-Dispatcher-Port"); got != "standby" {
		t.Fatalf("expected second request to stay on standby, got %s", got)
	}

	events, err := a.ListEventLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("list events failed: %v", err)
	}
	fallbackCount := 0
	for _, event := range events {
		if event["event_type"] == "dispatcher_fallback" || (event["message"] != nil && strings.Contains(event["message"].(string), "dispatcher fallback")) {
			fallbackCount++
		}
	}
	if fallbackCount == 0 {
		t.Fatal("expected dispatcher_fallback event to be recorded")
	}

	secondEvents, err := a.ListEventLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("list events after second request failed: %v", err)
	}
	secondFallbackCount := 0
	for _, event := range secondEvents {
		if event["event_type"] == "dispatcher_fallback" || (event["message"] != nil && strings.Contains(event["message"].(string), "dispatcher fallback")) {
			secondFallbackCount++
		}
	}
	if secondFallbackCount != fallbackCount {
		t.Fatalf("expected second request to avoid new fallback events, got %d then %d", fallbackCount, secondFallbackCount)
	}
}
func TestDispatcherSOCKSRelay(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmpDir, "dispatcher-socks.db")
	cfg.SingboxConfigPath = filepath.Join(tmpDir, "dispatcher-socks.json")
	cfg.SingboxBinary = writeFakeSingBox(t, tmpDir)
	cfg.SubscriptionURL = ""
	cfg.Ports = []config.PortConfig{{Key: config.DefaultPortKey, HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28101, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: 28102}}
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 28103
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 28104

	repo, _ := seedRepository(t, cfg.DBPath)
	if err := repo.UpsertRequestLaneState(context.Background(), model.RequestLaneState{PortKey: config.DefaultPortKey, LaneKey: "lane-socks-1", Protocol: "socks", AssignedNodeID: 1, State: "ready", LastSwitchReason: "lane_allocator_assigned"}); err != nil {
		t.Fatalf("seed default socks lane failed: %v", err)
	}
	portState, err := repo.GetPortRuntimeState(context.Background(), config.DefaultPortKey)
	if err != nil {
		t.Fatalf("get default runtime state failed: %v", err)
	}
	if portState.CurrentActiveNodeID == 0 {
		portState.CurrentActiveNodeID = 1
		if err := repo.UpdatePortRuntimeState(context.Background(), *portState); err != nil {
			t.Fatalf("update default runtime state failed: %v", err)
		}
	}

	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target failed: %v", err)
	}
	defer target.Close()
	go func() {
		conn, err := target.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("pong"))
	}()

	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream socks failed: %v", err)
	}
	defer upstream.Close()
	go serveFakeSOCKSUpstream(t, upstream, target.Addr().String())

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("expected New to succeed with dispatcher socks, got %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()
	upstreamAddr := upstream.Addr().(*net.TCPAddr)
	a.dispatcher.ports = []config.PortConfig{{Key: config.DefaultPortKey, HTTPListenAddr: "127.0.0.1", HTTPListenPort: 28101, SOCKSListenAddr: "127.0.0.1", SOCKSListenPort: mustBasePortForLane(t, upstreamAddr.Port, "lane-socks-1")}}

	client, err := net.Dial("tcp", "127.0.0.1:28104")
	if err != nil {
		t.Fatalf("dial dispatcher socks failed: %v", err)
	}
	defer client.Close()
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write socks greeting failed: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read socks greeting reply failed: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("expected no-auth reply, got %v", reply)
	}
	host, portStr, _ := net.SplitHostPort(target.Addr().String())
	ip := net.ParseIP(host).To4()
	portNum, _ := strconv.Atoi(portStr)
	request := []byte{0x05, 0x01, 0x00, 0x01, ip[0], ip[1], ip[2], ip[3], byte(portNum >> 8), byte(portNum)}
	if _, err := client.Write(request); err != nil {
		t.Fatalf("write socks connect request failed: %v", err)
	}
	connectReply := make([]byte, 10)
	if _, err := io.ReadFull(client, connectReply); err != nil {
		t.Fatalf("read socks connect reply failed: %v", err)
	}
	if connectReply[1] != 0x00 {
		t.Fatalf("expected connect success, got %v", connectReply)
	}
	payload := make([]byte, 4)
	if _, err := io.ReadFull(client, payload); err != nil {
		t.Fatalf("read proxied payload failed: %v", err)
	}
	state, err := repo.GetRequestLaneState(context.Background(), config.DefaultPortKey, "lane-socks-1")
	if err != nil {
		t.Fatalf("get socks lane state failed: %v", err)
	}
	if state.LastUsedAt == "" {
		t.Fatal("expected socks lane last_used_at to be updated")
	}
}

func serveFakeSOCKSUpstream(t *testing.T, ln net.Listener, targetAddr string) {
	t.Helper()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			defer conn.Close()
			buf := make([]byte, 262)
			if _, err := io.ReadAtLeast(conn, buf[:2], 2); err != nil {
				return
			}
			nMethods := int(buf[1])
			if _, err := io.ReadFull(conn, buf[2:2+nMethods]); err != nil {
				return
			}
			if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
				return
			}
			if _, err := io.ReadAtLeast(conn, buf[:4], 4); err != nil {
				return
			}
			addrLen := 0
			switch buf[3] {
			case 0x01:
				addrLen = 4
			case 0x03:
				if _, err := io.ReadFull(conn, buf[4:5]); err != nil {
					return
				}
				addrLen = int(buf[4]) + 1
			case 0x04:
				addrLen = 16
			}
			start := 4
			if buf[3] == 0x03 {
				start = 5
			}
			if _, err := io.ReadFull(conn, buf[start:start+addrLen+2]); err != nil {
				return
			}
			if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x1f, 0x90}); err != nil {
				return
			}
			targetConn, err := net.Dial("tcp", targetAddr)
			if err != nil {
				return
			}
			defer targetConn.Close()
			go func() {
				_, _ = io.Copy(targetConn, conn)
				_ = targetConn.Close()
			}()
			_, _ = io.Copy(conn, targetConn)
		}(conn)
	}
}

func seedRepository(t *testing.T, dbPath string) (*sqliteRepo.Repository, []model.Node) {
	t.Helper()
	repo, err := sqliteRepo.New(dbPath)
	if err != nil {
		t.Fatalf("new repo failed: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	sub := &model.Subscription{Name: "default", URL: "https://example.com/sub", Enabled: true}
	if err := repo.UpsertSubscription(context.Background(), sub); err != nil {
		t.Fatalf("upsert subscription failed: %v", err)
	}
	storedSub, err := repo.GetPrimarySubscription(context.Background())
	if err != nil {
		t.Fatalf("get primary subscription failed: %v", err)
	}
	storedNodes, err := repo.ReplaceNodesForSubscription(context.Background(), storedSub.ID, []model.Node{
		{SourceKey: "node-1", Name: "node-1", ProtocolType: "shadowsocks", Server: "1.1.1.1", Port: 1001, PayloadJSON: `{"type":"shadowsocks","server":"1.1.1.1","server_port":1001,"method":"aes-256-gcm","password":"p1"}`, Enabled: true},
		{SourceKey: "node-2", Name: "node-2", ProtocolType: "shadowsocks", Server: "2.2.2.2", Port: 1002, PayloadJSON: `{"type":"shadowsocks","server":"2.2.2.2","server_port":1002,"method":"aes-256-gcm","password":"p2"}`, Enabled: true},
	})
	if err != nil {
		t.Fatalf("replace nodes failed: %v", err)
	}
	return repo, storedNodes
}

func mustPortFromURL(t *testing.T, value *url.URL) int {
	t.Helper()
	port, err := strconv.Atoi(value.Port())
	if err != nil {
		t.Fatalf("parse port from url failed: %v", err)
	}
	return port
}

func mustBasePortForLane(t *testing.T, lanePort int, laneKey string) int {
	t.Helper()
	offset, err := laneOffset(laneKey)
	if err != nil {
		t.Fatalf("resolve lane offset failed: %v", err)
	}
	return lanePort - 1000 - offset
}
