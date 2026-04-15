package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"proxypools/internal/config"
	"proxypools/internal/model"
	"proxypools/internal/pool"
	"proxypools/internal/runtime"
	sqliteRepo "proxypools/internal/storage/sqlite"
	"proxypools/internal/subscription"
	"proxypools/internal/web"
)

type subscriptionRefresher interface {
	Refresh(ctx context.Context, subscriptionURL string) ([]model.Node, error)
}

type App struct {
	Config                 config.Config
	Server                 *http.Server
	Runtime                *runtime.Process
	SubscriptionConfigured bool

	repo                *sqliteRepo.Repository
	checker             *pool.Checker
	switcher            pool.SelectorSwitcher
	dispatcher          *dispatcherRelay
	scheduler           pool.Scheduler
	subscriptionService subscriptionRefresher
	refreshMu           sync.Mutex
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
}

type dispatcherStatusService interface {
	GetDispatcherStatus(ctx context.Context) (map[string]any, error)
}

func New(cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SingboxConfigPath), 0o755); err != nil {
		return nil, err
	}

	ctx := context.Background()
	repo, err := sqliteRepo.New(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := repo.Migrate(ctx); err != nil {
		return nil, err
	}

	if runtimeState, err := repo.GetRuntimeState(ctx); err == nil {
		if runtimeState.RuntimeMode != "" {
			cfg.RuntimeMode = runtimeState.RuntimeMode
		}
		if runtimeState.PoolAlgorithm != "" {
			cfg.PoolAlgorithm = runtimeState.PoolAlgorithm
		}
	}
	if err := syncConfiguredPorts(ctx, repo, cfg); err != nil {
		return nil, err
	}

	proc := &runtime.Process{Binary: cfg.SingboxBinary, Config: cfg.SingboxConfigPath}
	service := subscription.NewService(nil)

	appCtx, cancel := context.WithCancel(context.Background())
	a := &App{
		Config:              cfg,
		Runtime:             proc,
		repo:                repo,
		subscriptionService: service,
		scheduler: pool.Scheduler{
			SubscriptionEvery: time.Duration(cfg.SubscriptionRefreshInterval) * time.Second,
			HealthEvery:       time.Duration(cfg.HealthCheckInterval) * time.Second,
		},
		cancel: cancel,
	}

	if cfg.SubscriptionURL != "" {
		sub := &model.Subscription{Name: "default", URL: cfg.SubscriptionURL, Enabled: true}
		if err := repo.UpsertSubscription(ctx, sub); err != nil {
			return nil, err
		}
		if _, err := a.RefreshSubscription(ctx); err != nil {
			return nil, err
		}
	} else {
		storedNodes, err := repo.ListNodes(ctx)
		if err != nil {
			return nil, err
		}
		a.SubscriptionConfigured = len(storedNodes) > 0
		if a.SubscriptionConfigured {
			activeNodeID := storedNodes[0].ID
			if runtimeState, err := repo.GetRuntimeState(ctx); err == nil && runtimeState.CurrentActiveNodeID > 0 {
				for _, node := range storedNodes {
					if node.ID == runtimeState.CurrentActiveNodeID {
						activeNodeID = runtimeState.CurrentActiveNodeID
						break
					}
				}
			}
			if err := a.applyNodesToRuntime(ctx, storedNodes, activeNodeID); err != nil {
				return nil, err
			}
		}
	}

	clash := runtime.NewClashAPI("http://127.0.0.1:9090", "")
	checker := pool.NewChecker(pool.CheckerConfig{
		ProbeURL:         "http://www.gstatic.com/generate_204",
		HealthProxyURL:   fmt.Sprintf("http://127.0.0.1:%d", cfg.HealthListenPort),
		SelectorSwitcher: clash,
	})

	a.checker = checker
	a.switcher = clash

	if cfg.Dispatcher.Enabled {
		dispatcher, err := newDispatcherRelay(cfg, repo)
		if err != nil {
			return nil, err
		}
		a.dispatcher = dispatcher
	}

	defaultPort := cfg.DefaultPort()
	handler := web.NewRouter(web.Dependencies{
		AdminUsername:           cfg.AdminUsername,
		AdminPasswordHash:       cfg.AdminPasswordHash,
		Runtime:                 proc,
		ConfigPath:              cfg.SingboxConfigPath,
		AdminListen:             fmt.Sprintf("%s:%d", cfg.AdminListenAddr, cfg.AdminListenPort),
		HTTPListen:              fmt.Sprintf("%s:%d", defaultPort.HTTPListenAddr, defaultPort.HTTPListenPort),
		SOCKSListen:             fmt.Sprintf("%s:%d", defaultPort.SOCKSListenAddr, defaultPort.SOCKSListenPort),
		HealthListen:            fmt.Sprintf("%s:%d", cfg.HealthListenAddr, cfg.HealthListenPort),
		RuntimeMode:             cfg.RuntimeMode,
		PoolAlgorithm:           cfg.PoolAlgorithm,
		Ports:                   cfg.ResolvedPorts(),
		SubscriptionConfigured:  a.SubscriptionConfigured,
		RuntimeStateProvider:    repo,
		ManualSwitchService:     a,
		SubscriptionService:     a,
		NodeAdminService:        a,
		RuntimeAdminService:     a,
		EventLogService:         a,
		DispatcherStatusService: a,
	})
	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.AdminListenAddr, cfg.AdminListenPort),
		Handler: handler,
	}
	a.Server = server
	if a.dispatcher != nil {
		a.dispatcher.SetRepo(repo)
	}

	if a.SubscriptionConfigured {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			a.scheduler.Start(appCtx, func(ctx context.Context) {
				_, _ = a.RefreshSubscription(ctx)
			}, a.runHealthCheck)
			<-appCtx.Done()
		}()
	}

	return a, nil
}

func (a *App) GetPrimarySubscription(ctx context.Context) (map[string]any, error) {
	sub, err := a.repo.GetPrimarySubscription(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]any{
				"id":                 0,
				"name":               "default",
				"url":                "",
				"enabled":            false,
				"last_fetch_at":      "",
				"last_fetch_status":  "not_configured",
				"last_fetch_error":   "",
				"last_added_nodes":   0,
				"last_removed_nodes": 0,
			}, nil
		}
		return nil, err
	}
	return subscriptionPayload(sub), nil
}

func (a *App) GetDispatcherStatus(ctx context.Context) (map[string]any, error) {
	if a.dispatcher == nil {
		return map[string]any{
			"enabled": false,
		}, nil
	}
	return a.dispatcher.status(ctx)
}

func (a *App) RefreshSubscription(ctx context.Context) (map[string]any, error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()

	sub, err := a.repo.GetPrimarySubscription(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("subscription not configured")
		}
		return nil, err
	}
	if sub.URL == "" {
		return nil, fmt.Errorf("subscription url is empty")
	}

	previousNodes, err := a.repo.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	previousNodeKeys := make(map[string]struct{}, len(previousNodes))
	sourceKeyByNodeID := make(map[int64]string, len(previousNodes))
	for _, node := range previousNodes {
		previousNodeKeys[node.SourceKey] = struct{}{}
		sourceKeyByNodeID[node.ID] = node.SourceKey
	}

	portStates, err := a.repo.ListPortRuntimeStates(ctx)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	portStateByKey := make(map[string]model.PortRuntimeState, len(portStates))
	for _, state := range portStates {
		portStateByKey[state.PortKey] = state
	}

	preferredSourceKeyByPort := make(map[string]string, len(portStateByKey))
	manualDisabledSourceKeysByPort := make(map[string]map[string]struct{}, len(a.Config.ResolvedPorts()))
	laneAssignmentsByPort := make(map[string]map[string]laneAssignment, len(a.Config.ResolvedPorts()))
	for _, port := range a.Config.ResolvedPorts() {
		state, ok := portStateByKey[port.Key]
		if !ok {
			state = model.PortRuntimeState{PortKey: port.Key}
			state.RuntimeMode = port.RuntimeMode
			state.PoolAlgorithm = port.PoolAlgorithm
			state.SelectionMode = "auto"
			portStateByKey[port.Key] = state
		}
		if sourceKey := sourceKeyByNodeID[state.CurrentActiveNodeID]; sourceKey != "" {
			preferredSourceKeyByPort[port.Key] = sourceKey
		}
		statuses, err := a.repo.ListNodeRuntimeStatusesByPort(ctx, port.Key)
		if err != nil {
			return nil, err
		}
		disabledKeys := make(map[string]struct{})
		for _, status := range statuses {
			if !status.ManualDisabled {
				continue
			}
			if sourceKey := sourceKeyByNodeID[status.NodeID]; sourceKey != "" {
				disabledKeys[sourceKey] = struct{}{}
			}
		}
		manualDisabledSourceKeysByPort[port.Key] = disabledKeys
		lanes, err := a.repo.ListRequestLaneStatesByPort(ctx, port.Key)
		if err != nil {
			return nil, err
		}
		laneAssignments := make(map[string]laneAssignment, len(lanes))
		for _, lane := range lanes {
			assignment := laneAssignment{
				Protocol:         lane.Protocol,
				Weight:           lane.Weight,
				LastSwitchReason: lane.LastSwitchReason,
				LastSwitchAt:     lane.LastSwitchAt,
				LastUsedAt:       lane.LastUsedAt,
				LastErrorAt:      lane.LastErrorAt,
				State:            lane.State,
			}
			if sourceKey := sourceKeyByNodeID[lane.AssignedNodeID]; sourceKey != "" {
				assignment.SourceKey = sourceKey
			}
			laneAssignments[lane.LaneKey] = assignment
		}
		laneAssignmentsByPort[port.Key] = laneAssignments
	}

	fetchedNodes, err := a.subscriptionService.Refresh(ctx, sub.URL)
	fetchedAt := time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		_ = a.repo.CreateEventLog(ctx, model.EventLog{EventType: "subscription_refresh", Level: "error", Message: err.Error(), MetadataJSON: `{"status":"error"}`})
		_ = a.repo.UpdateSubscriptionFetchResult(ctx, sub.ID, fetchedAt, "error", err.Error(), 0, 0)
		return nil, err
	}
	if len(fetchedNodes) == 0 {
		err = fmt.Errorf("subscription parsed zero nodes")
		_ = a.repo.UpdateSubscriptionFetchResult(ctx, sub.ID, fetchedAt, "error", err.Error(), 0, 0)
		return nil, err
	}

	fetchedNodeKeys := make(map[string]struct{}, len(fetchedNodes))
	added := 0
	for _, node := range fetchedNodes {
		fetchedNodeKeys[node.SourceKey] = struct{}{}
		if _, ok := previousNodeKeys[node.SourceKey]; !ok {
			added++
		}
	}
	removed := 0
	for sourceKey := range previousNodeKeys {
		if _, ok := fetchedNodeKeys[sourceKey]; !ok {
			removed++
		}
	}

	storedNodes, err := a.repo.ReplaceNodesForSubscription(ctx, sub.ID, fetchedNodes)
	if err != nil {
		_ = a.repo.CreateEventLog(ctx, model.EventLog{EventType: "subscription_refresh", Level: "error", Message: err.Error(), MetadataJSON: `{"status":"error"}`})
		_ = a.repo.UpdateSubscriptionFetchResult(ctx, sub.ID, fetchedAt, "error", err.Error(), 0, 0)
		return nil, err
	}
	defaultActiveNodeID := storedNodes[0].ID
	newNodeIDBySourceKey := make(map[string]int64, len(storedNodes))
	for _, node := range storedNodes {
		newNodeIDBySourceKey[node.SourceKey] = node.ID
	}

	for _, port := range a.Config.ResolvedPorts() {
		state := portStateByKey[port.Key]
		state.PortKey = port.Key
		if state.SelectionMode == "" {
			state.SelectionMode = "auto"
		}
		if state.RuntimeMode == "" {
			state.RuntimeMode = port.RuntimeMode
		}
		if state.PoolAlgorithm == "" {
			state.PoolAlgorithm = port.PoolAlgorithm
		}
		if state.LastSwitchReason == "" {
			state.LastSwitchReason = "subscription_refresh"
		}
		if state.LastSwitchAt == "" {
			state.LastSwitchAt = fetchedAt
		}

		if preferredSourceKey, ok := preferredSourceKeyByPort[port.Key]; ok {
			if mappedNodeID, ok := newNodeIDBySourceKey[preferredSourceKey]; ok {
				state.CurrentActiveNodeID = mappedNodeID
			} else {
				state.CurrentActiveNodeID = defaultActiveNodeID
				state.SelectionMode = "auto"
				state.LastSwitchReason = "subscription_refresh_reselect"
				state.LastSwitchAt = fetchedAt
			}
		} else {
			state.CurrentActiveNodeID = defaultActiveNodeID
			state.SelectionMode = "auto"
			state.LastSwitchReason = "subscription_refresh_reselect"
			state.LastSwitchAt = fetchedAt
		}
		state.RestartRequired = false
		if err := a.repo.UpdatePortRuntimeState(ctx, state); err != nil {
			_ = a.repo.UpdateSubscriptionFetchResult(ctx, sub.ID, fetchedAt, "error", err.Error(), 0, 0)
			return nil, err
		}
		for sourceKey := range manualDisabledSourceKeysByPort[port.Key] {
			nodeID, ok := newNodeIDBySourceKey[sourceKey]
			if !ok {
				continue
			}
			if err := a.repo.SetPortNodeManualDisabled(ctx, port.Key, nodeID, true); err != nil {
				_ = a.repo.UpdateSubscriptionFetchResult(ctx, sub.ID, fetchedAt, "error", err.Error(), 0, 0)
				return nil, err
			}
		}
		for laneKey, assignment := range laneAssignmentsByPort[port.Key] {
			assignedNodeID := int64(0)
			laneState := assignment.State
			if assignment.SourceKey != "" {
				if mappedNodeID, ok := newNodeIDBySourceKey[assignment.SourceKey]; ok {
					assignedNodeID = mappedNodeID
					if laneState == "" {
						laneState = "ready"
					}
				}
			}
			if err := a.repo.UpsertRequestLaneState(ctx, model.RequestLaneState{
				PortKey:          port.Key,
				LaneKey:          laneKey,
				Protocol:         assignment.Protocol,
				AssignedNodeID:   assignedNodeID,
				Weight:           assignment.Weight,
				State:            laneState,
				LastSwitchReason: assignment.LastSwitchReason,
				LastSwitchAt:     assignment.LastSwitchAt,
				LastUsedAt:       assignment.LastUsedAt,
				LastErrorAt:      assignment.LastErrorAt,
			}); err != nil {
				_ = a.repo.UpdateSubscriptionFetchResult(ctx, sub.ID, fetchedAt, "error", err.Error(), 0, 0)
				return nil, err
			}
		}
	}

	if err := a.applyNodesToRuntime(ctx, storedNodes, defaultActiveNodeID); err != nil {
		_ = a.repo.UpdateSubscriptionFetchResult(ctx, sub.ID, fetchedAt, "error", err.Error(), 0, 0)
		return nil, err
	}
	if err := a.reconcileLaneStates(ctx, fetchedAt); err != nil {
		_ = a.repo.UpdateSubscriptionFetchResult(ctx, sub.ID, fetchedAt, "error", err.Error(), 0, 0)
		return nil, err
	}

	a.SubscriptionConfigured = true
	if a.dispatcher != nil {
		_ = a.dispatcher.rebuildSnapshot(ctx)
	}
	_ = a.repo.CreateEventLog(ctx, model.EventLog{EventType: "subscription_refresh", Level: "info", Message: "subscription refreshed", MetadataJSON: fmt.Sprintf(`{"added":%d,"removed":%d}`, added, removed)})
	if err := a.repo.UpdateSubscriptionFetchResult(ctx, sub.ID, fetchedAt, "success", "", added, removed); err != nil {
		return nil, err
	}
	updated, err := a.repo.GetPrimarySubscription(ctx)
	if err != nil {
		return nil, err
	}
	return subscriptionPayload(updated), nil
}

func (a *App) applyNodesToRuntime(ctx context.Context, nodes []model.Node, activeNodeID int64) error {
	ports, err := buildRuntimePorts(ctx, a.repo, a.Config, activeNodeID)
	if err != nil {
		a.Runtime.RecordApplyResult("build_error")
		return err
	}
	configContent, err := runtime.BuildConfig(runtime.BuildInput{
		HTTPListenAddr:   a.Config.HTTPListenAddr,
		HTTPPort:         a.Config.HTTPListenPort,
		SOCKSListenAddr:  a.Config.SOCKSListenAddr,
		SOCKSPort:        a.Config.SOCKSListenPort,
		HealthListenAddr: a.Config.HealthListenAddr,
		HealthPort:       a.Config.HealthListenPort,
		Nodes:            nodes,
		ActiveNodeID:     activeNodeID,
		Ports:            ports,
	})
	if err != nil {
		a.Runtime.RecordApplyResult("build_error")
		return err
	}
	if err := a.Runtime.WriteConfig(configContent); err != nil {
		a.Runtime.RecordApplyResult("write_error")
		return err
	}
	if err := a.Runtime.Check(ctx); err != nil {
		a.Runtime.RecordApplyResult("check_error")
		return err
	}
	if a.Runtime.IsRunning() {
		if err := a.Runtime.Stop(ctx); err != nil {
			a.Runtime.RecordApplyResult("stop_error")
			return err
		}
	}
	if err := a.Runtime.Start(); err != nil {
		a.Runtime.RecordApplyResult("start_error")
		return err
	}
	a.Runtime.RecordApplyResult("applied")
	return nil
}

func subscriptionPayload(sub *model.Subscription) map[string]any {
	return map[string]any{
		"id":                 sub.ID,
		"name":               sub.Name,
		"url":                sub.URL,
		"enabled":            sub.Enabled,
		"last_fetch_at":      sub.LastFetchAt,
		"last_fetch_status":  sub.LastFetchStatus,
		"last_fetch_error":   sub.LastFetchError,
		"last_added_nodes":   sub.LastAddedNodes,
		"last_removed_nodes": sub.LastRemovedNodes,
	}
}

func (a *App) ListEventLogs(ctx context.Context, limit int) ([]map[string]any, error) {
	items, err := a.repo.ListEventLogs(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"id":              item.ID,
			"event_type":      item.EventType,
			"level":           item.Level,
			"message":         item.Message,
			"related_node_id": item.RelatedNodeID,
			"metadata_json":   item.MetadataJSON,
			"created_at":      item.CreatedAt,
		})
	}
	return result, nil
}

func (a *App) runHealthCheck(ctx context.Context) {
	if a.Runtime == nil || !a.Runtime.IsRunning() {
		return
	}

	statuses, err := a.repo.ListNodeRuntimeStatuses(ctx)
	if err != nil {
		return
	}
	for i, status := range statuses {
		updated, err := a.checker.ProbeNode(fmt.Sprintf("node-%d", status.NodeID), status)
		if err != nil {
			updated.ConsecutiveFailures = status.ConsecutiveFailures + 1
			_ = a.repo.CreateEventLog(ctx, model.EventLog{EventType: "health_check_failed", Level: "warn", Message: err.Error(), RelatedNodeID: status.NodeID})
		}
		scored := pool.Score(updated)
		statuses[i] = scored.NodeRuntimeStatus
		if statusesEqual(status, scored.NodeRuntimeStatus) {
			continue
		}
		_ = a.repo.UpsertNodeRuntimeStatus(ctx, scored.NodeRuntimeStatus)
	}
	for _, port := range a.Config.ResolvedPorts() {
		state, err := a.repo.GetPortRuntimeState(ctx, port.Key)
		if err != nil {
			continue
		}
		portStatuses, err := a.repo.ListNodeRuntimeStatusesByPort(ctx, port.Key)
		if err != nil {
			continue
		}
		if err := a.reconcilePortRuntime(ctx, port, state, portStatuses); err != nil {
			continue
		}
	}
	_ = a.reconcileLaneStates(ctx, time.Now().UTC().Format(time.RFC3339))
	if a.dispatcher != nil {
		_ = a.dispatcher.rebuildSnapshot(ctx)
	}
}

func (a *App) reconcilePortRuntime(ctx context.Context, port config.PortConfig, state *model.PortRuntimeState, statuses []model.NodeRuntimeStatus) error {
	current := state.CurrentActiveNodeID
	if current <= 0 {
		return nil
	}
	currentHealthy := false
	for _, status := range statuses {
		if status.NodeID == current && status.State == "active" && !status.ManualDisabled {
			currentHealthy = true
			break
		}
	}

	switchReason := ""
	var next model.NodeRuntimeStatus
	var ok bool

	switch {
	case state.SelectionMode == "manual_locked":
		if !currentHealthy {
			next, ok = pool.SelectSequentialNext(current, statuses)
			if !ok {
				next, ok = pool.SelectNext(current, statuses)
			}
			if ok {
				switchReason = "manual_locked_failover"
			}
		}
	case port.RuntimeMode == "pool" && port.PoolAlgorithm == "sequential":
		next, ok = pool.SelectSequentialNext(current, statuses)
		if ok {
			if currentHealthy {
				switchReason = "pool_sequential_rotate"
			} else {
				switchReason = "pool_sequential_failover"
			}
		}
	case port.RuntimeMode == "pool" && port.PoolAlgorithm == "random":
		next, ok = pool.SelectRandomNext(current, statuses)
		if ok {
			if currentHealthy {
				switchReason = "pool_random_rotate"
			} else {
				switchReason = "pool_random_failover"
			}
		}
	case port.RuntimeMode == "pool" && port.PoolAlgorithm == "balance":
		next, ok = pool.SelectBalanceNext(current, statuses)
		if ok {
			if currentHealthy {
				switchReason = "pool_balance_rotate"
			} else {
				switchReason = "pool_balance_failover"
			}
		}
	case !currentHealthy:
		next, ok = pool.SelectNext(current, statuses)
		if ok {
			switchReason = "health_check_failover"
		}
	default:
		return nil
	}

	if !ok {
		return nil
	}
	httpGroup, socksGroup := selectorGroupTags(port.Key)
	if err := a.switcher.SwitchSelector(httpGroup, fmt.Sprintf("node-%d", next.NodeID)); err != nil {
		return err
	}
	_ = a.switcher.SwitchSelector(socksGroup, fmt.Sprintf("node-%d", next.NodeID))
	state.CurrentActiveNodeID = next.NodeID
	if switchReason == "health_check_failover" {
		state.SelectionMode = "auto"
	}
	state.RuntimeMode = port.RuntimeMode
	state.PoolAlgorithm = port.PoolAlgorithm
	state.LastSwitchReason = switchReason
	state.LastSwitchAt = time.Now().UTC().Format(time.RFC3339)
	if err := a.repo.UpdatePortRuntimeState(ctx, *state); err != nil {
		return err
	}
	return a.repo.CreateEventLog(ctx, model.EventLog{EventType: switchReason, Level: "warn", Message: fmt.Sprintf("active node switched for port %s after health check cycle", port.Key), RelatedNodeID: next.NodeID, MetadataJSON: portMetadata(port.Key)})
}

func (a *App) buildLaneStates(ctx context.Context, portKey string, statuses []model.NodeRuntimeStatus, now string) ([]model.RequestLaneState, error) {
	existing, err := a.repo.ListRequestLaneStatesByPort(ctx, portKey)
	if err != nil {
		return nil, err
	}
	existingByKey := make(map[string]model.RequestLaneState, len(existing))
	for _, lane := range existing {
		existingByKey[lane.LaneKey] = lane
	}
	lanes := []model.RequestLaneState{
		{PortKey: portKey, LaneKey: "lane-http-1", Protocol: "http"},
		{PortKey: portKey, LaneKey: "lane-socks-1", Protocol: "socks"},
	}
	usedNodeIDs := make(map[int64]struct{})
	for i := range lanes {
		if existingLane, ok := existingByKey[lanes[i].LaneKey]; ok {
			lanes[i].Weight = existingLane.Weight
			lanes[i].LastSwitchReason = existingLane.LastSwitchReason
			lanes[i].LastSwitchAt = existingLane.LastSwitchAt
			lanes[i].LastUsedAt = existingLane.LastUsedAt
			lanes[i].LastErrorAt = existingLane.LastErrorAt
			if lanes[i].Weight <= 0 {
				lanes[i].Weight = 1
			}
			if existingLane.AssignedNodeID > 0 {
				for _, status := range statuses {
					if status.NodeID == existingLane.AssignedNodeID && status.State == "active" && !status.ManualDisabled {
						lanes[i].AssignedNodeID = existingLane.AssignedNodeID
						lanes[i].State = "ready"
						usedNodeIDs[existingLane.AssignedNodeID] = struct{}{}
						break
					}
				}
			}
		}
	}
	candidates := make([]model.NodeRuntimeStatus, 0, len(statuses))
	for _, status := range statuses {
		if status.State == "active" && !status.ManualDisabled {
			candidates = append(candidates, status)
		}
	}
	for i := range lanes {
		if lanes[i].AssignedNodeID > 0 {
			continue
		}
		selectedID := int64(0)
		for _, candidate := range candidates {
			if _, used := usedNodeIDs[candidate.NodeID]; used {
				continue
			}
			selectedID = candidate.NodeID
			break
		}
		if selectedID == 0 && len(candidates) > 0 {
			selectedID = candidates[0].NodeID
		}
		if selectedID > 0 {
			lanes[i].AssignedNodeID = selectedID
			if lanes[i].Weight <= 0 {
				lanes[i].Weight = 1
			}
			lanes[i].State = "ready"
			lanes[i].LastSwitchReason = "lane_allocator_assigned"
			lanes[i].LastSwitchAt = now
			usedNodeIDs[selectedID] = struct{}{}
		} else {
			lanes[i].State = "idle"
		}
	}
	return lanes, nil
}

func (a *App) reconcileLaneStates(ctx context.Context, now string) error {
	for _, port := range a.Config.ResolvedPorts() {
		statuses, err := a.repo.ListNodeRuntimeStatusesByPort(ctx, port.Key)
		if err != nil {
			return err
		}
		lanes, err := a.buildLaneStates(ctx, port.Key, statuses, now)
		if err != nil {
			return err
		}
		for _, lane := range lanes {
			if err := a.repo.UpsertRequestLaneState(ctx, lane); err != nil {
				return err
			}
		}
	}
	return nil
}

func statusesEqual(a, b model.NodeRuntimeStatus) bool {
	return a.NodeID == b.NodeID &&
		a.State == b.State &&
		a.Tier == b.Tier &&
		a.Score == b.Score &&
		a.LatencyMS == b.LatencyMS &&
		a.RecentSuccessRate == b.RecentSuccessRate &&
		a.ConsecutiveFailures == b.ConsecutiveFailures &&
		a.LastCheckAt == b.LastCheckAt &&
		a.LastSuccessAt == b.LastSuccessAt &&
		a.LastFailureAt == b.LastFailureAt &&
		a.CooldownUntil == b.CooldownUntil &&
		a.ManualDisabled == b.ManualDisabled
}

type laneAssignment struct {
	SourceKey        string
	Protocol         string
	Weight           int
	LastSwitchReason string
	LastSwitchAt     string
	LastUsedAt       string
	LastErrorAt      string
	State            string
}

func (a *App) SetNodeManualDisabled(ctx context.Context, nodeID int64, disabled bool) error {
	return a.SetNodeManualDisabledByPort(ctx, config.DefaultPortKey, nodeID, disabled)
}

func (a *App) SetNodeManualDisabledByPort(ctx context.Context, portKey string, nodeID int64, disabled bool) error {
	portKey = normalizedPortKey(portKey)
	nodes, err := a.repo.ListNodes(ctx)
	if err != nil {
		return err
	}
	nodeExists := false
	for _, node := range nodes {
		if node.ID == nodeID && !node.Removed && node.Enabled {
			nodeExists = true
			break
		}
	}
	if !nodeExists {
		return fmt.Errorf("node not found")
	}
	if _, err := portConfigByKey(a.Config, portKey); err != nil {
		return err
	}
	if err := a.repo.SetPortNodeManualDisabled(ctx, portKey, nodeID, disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("node status not found")
		}
		return err
	}
	if a.dispatcher != nil {
		_ = a.dispatcher.rebuildSnapshot(ctx)
	}
	action := "node_enabled"
	message := "node enabled"
	if disabled {
		action = "node_disabled"
		message = "node disabled"
	}
	if portKey != config.DefaultPortKey {
		message = fmt.Sprintf("%s on port %s", message, portKey)
	}
	_ = a.repo.CreateEventLog(ctx, model.EventLog{EventType: action, Level: "info", Message: message, RelatedNodeID: nodeID, MetadataJSON: portMetadata(portKey)})
	return nil
}

func (a *App) UpdateRuntimeSettings(ctx context.Context, runtimeMode string, poolAlgorithm string) error {
	return a.UpdateRuntimeSettingsByPort(ctx, config.DefaultPortKey, runtimeMode, poolAlgorithm)
}

func (a *App) UpdateRuntimeSettingsByPort(ctx context.Context, portKey string, runtimeMode string, poolAlgorithm string) error {
	portKey = normalizedPortKey(portKey)
	candidate := a.Config
	updateConfigPortSettings(&candidate, portKey, runtimeMode, poolAlgorithm)
	if err := candidate.Validate(); err != nil {
		return err
	}

	state, err := a.repo.GetPortRuntimeState(ctx, portKey)
	if err != nil {
		return err
	}
	state.RuntimeMode = runtimeMode
	state.PoolAlgorithm = poolAlgorithm
	state.LastSwitchReason = "runtime_settings_updated"
	state.LastSwitchAt = time.Now().UTC().Format(time.RFC3339)
	if err := a.repo.UpdatePortRuntimeState(ctx, *state); err != nil {
		return err
	}
	if a.dispatcher != nil {
		_ = a.dispatcher.rebuildSnapshot(ctx)
	}
	updateConfigPortSettings(&a.Config, portKey, runtimeMode, poolAlgorithm)
	message := fmt.Sprintf("runtime mode=%s, pool algorithm=%s", runtimeMode, poolAlgorithm)
	if portKey != config.DefaultPortKey {
		message = fmt.Sprintf("%s, port=%s", message, portKey)
	}
	_ = a.repo.CreateEventLog(ctx, model.EventLog{EventType: "runtime_settings_updated", Level: "info", Message: message, MetadataJSON: portMetadata(portKey)})
	return nil
}

func (a *App) UnlockSelection(ctx context.Context) error {
	return a.UnlockSelectionByPort(ctx, config.DefaultPortKey)
}

func (a *App) UnlockSelectionByPort(ctx context.Context, portKey string) error {
	portKey = normalizedPortKey(portKey)
	portCfg, err := portConfigByKey(a.Config, portKey)
	if err != nil {
		return err
	}
	state, err := a.repo.GetPortRuntimeState(ctx, portKey)
	if err != nil {
		return err
	}
	state.SelectionMode = "auto"
	state.RuntimeMode = portCfg.RuntimeMode
	state.PoolAlgorithm = portCfg.PoolAlgorithm
	state.LastSwitchReason = "manual_unlock"
	state.LastSwitchAt = time.Now().UTC().Format(time.RFC3339)
	if err := a.repo.UpdatePortRuntimeState(ctx, *state); err != nil {
		return err
	}
	if a.dispatcher != nil {
		_ = a.dispatcher.rebuildSnapshot(ctx)
	}
	message := "selection unlocked"
	if portKey != config.DefaultPortKey {
		message = fmt.Sprintf("selection unlocked on port %s", portKey)
	}
	_ = a.repo.CreateEventLog(ctx, model.EventLog{EventType: "manual_unlock", Level: "info", Message: message, MetadataJSON: portMetadata(portKey)})
	return nil
}

func (a *App) SwitchNode(ctx context.Context, nodeID int64) error {
	return a.SwitchNodeByPort(ctx, config.DefaultPortKey, nodeID)
}

func (a *App) SwitchNodeByPort(ctx context.Context, portKey string, nodeID int64) error {
	portKey = normalizedPortKey(portKey)
	portCfg, err := portConfigByKey(a.Config, portKey)
	if err != nil {
		return err
	}
	if !a.SubscriptionConfigured {
		return fmt.Errorf("subscription runtime not configured")
	}
	if a.Runtime == nil || !a.Runtime.IsRunning() {
		return fmt.Errorf("runtime is not running")
	}

	nodes, err := a.repo.ListNodes(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, node := range nodes {
		if node.ID == nodeID && !node.Removed && node.Enabled {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("node not found")
	}

	statuses, err := a.repo.ListNodeRuntimeStatusesByPort(ctx, portKey)
	if err != nil {
		return err
	}
	statusFound := false
	for _, status := range statuses {
		if status.NodeID == nodeID {
			statusFound = true
			if status.ManualDisabled {
				return fmt.Errorf("node is manually disabled")
			}
			if status.State != "active" {
				return fmt.Errorf("node is not active")
			}
		}
	}
	if !statusFound {
		return fmt.Errorf("node status not found")
	}

	httpGroup, socksGroup := selectorGroupTags(portKey)
	if err := a.switcher.SwitchSelector(httpGroup, fmt.Sprintf("node-%d", nodeID)); err != nil {
		return err
	}
	if err := a.switcher.SwitchSelector(socksGroup, fmt.Sprintf("node-%d", nodeID)); err != nil {
		return err
	}

	state, err := a.repo.GetPortRuntimeState(ctx, portKey)
	if err != nil {
		return err
	}
	state.CurrentActiveNodeID = nodeID
	state.SelectionMode = "manual_locked"
	state.RuntimeMode = portCfg.RuntimeMode
	state.PoolAlgorithm = portCfg.PoolAlgorithm
	state.RestartRequired = false
	state.LastSwitchReason = "manual_switch"
	state.LastSwitchAt = time.Now().UTC().Format(time.RFC3339)
	if err := a.repo.UpdatePortRuntimeState(ctx, *state); err != nil {
		return err
	}
	if a.dispatcher != nil {
		_ = a.dispatcher.rebuildSnapshot(ctx)
	}
	message := "node switched manually"
	if portKey != config.DefaultPortKey {
		message = fmt.Sprintf("node switched manually on port %s", portKey)
	}
	_ = a.repo.CreateEventLog(ctx, model.EventLog{EventType: "manual_switch", Level: "info", Message: message, RelatedNodeID: nodeID, MetadataJSON: portMetadata(portKey)})
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	if a.dispatcher != nil {
		_ = a.dispatcher.Close()
	}
	if a.Runtime != nil {
		_ = a.Runtime.Stop(ctx)
	}
	if a.Server != nil {
		return a.Server.Shutdown(ctx)
	}
	return nil
}

func syncConfiguredPorts(ctx context.Context, repo *sqliteRepo.Repository, cfg config.Config) error {
	existing, err := repo.ListPortRuntimeStates(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	existingByKey := make(map[string]model.PortRuntimeState, len(existing))
	for _, item := range existing {
		existingByKey[item.PortKey] = item
	}
	baseState, err := repo.GetRuntimeState(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, port := range cfg.ResolvedPorts() {
		state, ok := existingByKey[port.Key]
		if !ok {
			state = model.PortRuntimeState{PortKey: port.Key}
			if baseState != nil {
				state.RuntimeState = *baseState
			}
		}
		if state.SelectionMode == "" {
			state.SelectionMode = "auto"
		}
		if !ok {
			state.RuntimeMode = port.RuntimeMode
			state.PoolAlgorithm = port.PoolAlgorithm
		} else {
			if state.RuntimeMode == "" {
				state.RuntimeMode = port.RuntimeMode
			}
			if state.PoolAlgorithm == "" {
				state.PoolAlgorithm = port.PoolAlgorithm
			}
		}
		if err := repo.UpdatePortRuntimeState(ctx, state); err != nil {
			return err
		}
	}
	return nil
}

func buildRuntimePorts(ctx context.Context, repo *sqliteRepo.Repository, cfg config.Config, fallbackActiveNodeID int64) ([]runtime.PortBuildInput, error) {
	ports := cfg.ResolvedPorts()
	result := make([]runtime.PortBuildInput, 0, len(ports))
	for _, port := range ports {
		activeNodeID := fallbackActiveNodeID
		state, err := repo.GetPortRuntimeState(ctx, port.Key)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		} else if state.CurrentActiveNodeID > 0 {
			activeNodeID = state.CurrentActiveNodeID
		}
		lanes, err := repo.ListRequestLaneStatesByPort(ctx, port.Key)
		if err != nil {
			return nil, err
		}
		laneInputs := make([]runtime.LaneBuildInput, 0, len(lanes))
		for _, lane := range lanes {
			lanePort, err := laneListenPort(port, lane.Protocol, lane.LaneKey)
			if err != nil {
				return nil, err
			}
			laneInputs = append(laneInputs, runtime.LaneBuildInput{
				Key:          lane.LaneKey,
				Protocol:     lane.Protocol,
				ListenAddr:   laneListenAddr(port, lane.Protocol, lane.LaneKey),
				ListenPort:   lanePort,
				ActiveNodeID: lane.AssignedNodeID,
			})
		}
		result = append(result, runtime.PortBuildInput{
			Key:             port.Key,
			HTTPListenAddr:  port.HTTPListenAddr,
			HTTPPort:        port.HTTPListenPort,
			SOCKSListenAddr: port.SOCKSListenAddr,
			SOCKSPort:       port.SOCKSListenPort,
			ActiveNodeID:    activeNodeID,
			Lanes:           laneInputs,
		})
	}
	return result, nil
}

func configuredLaneBinding(port config.PortConfig, protocol string, laneKey string) (config.LaneConfig, bool) {
	for _, lane := range port.Lanes {
		if lane.Key == laneKey && lane.Protocol == protocol {
			return lane, true
		}
	}
	return config.LaneConfig{}, false
}

func laneListenAddr(port config.PortConfig, protocol string, laneKey string) string {
	if lane, ok := configuredLaneBinding(port, protocol, laneKey); ok && lane.ListenAddr != "" {
		return lane.ListenAddr
	}
	if protocol == "socks" {
		return port.SOCKSListenAddr
	}
	return port.HTTPListenAddr
}

func laneListenPort(port config.PortConfig, protocol string, laneKey string) (int, error) {
	if lane, ok := configuredLaneBinding(port, protocol, laneKey); ok && lane.ListenPort > 0 {
		return lane.ListenPort, nil
	}
	offset, err := laneOffset(laneKey)
	if err != nil {
		return 0, err
	}
	base := port.HTTPListenPort
	if protocol == "socks" {
		base = port.SOCKSListenPort
	}
	return base + 1000 + offset, nil
}

func laneOffset(laneKey string) (int, error) {
	parts := strings.Split(laneKey, "-")
	if len(parts) == 0 {
		return 0, fmt.Errorf("invalid lane key")
	}
	last := parts[len(parts)-1]
	value, err := strconv.Atoi(last)
	if err != nil {
		return 0, nil
	}
	return value, nil
}

func selectorGroupTags(portKey string) (string, string) {
	if normalizedPortKey(portKey) == config.DefaultPortKey {
		return "active-http", "active-socks"
	}
	return "active-http-" + portKey, "active-socks-" + portKey
}

func normalizedPortKey(portKey string) string {
	if portKey == "" {
		return config.DefaultPortKey
	}
	return portKey
}

func portConfigByKey(cfg config.Config, portKey string) (config.PortConfig, error) {
	portKey = normalizedPortKey(portKey)
	for _, port := range cfg.ResolvedPorts() {
		if port.Key == portKey {
			return port, nil
		}
	}
	return config.PortConfig{}, fmt.Errorf("port not found")
}

func updateConfigPortSettings(cfg *config.Config, portKey string, runtimeMode string, poolAlgorithm string) {
	portKey = normalizedPortKey(portKey)
	if portKey == config.DefaultPortKey {
		cfg.RuntimeMode = runtimeMode
		cfg.PoolAlgorithm = poolAlgorithm
	}
	ports := cfg.ResolvedPorts()
	for i := range ports {
		if ports[i].Key == portKey {
			ports[i].RuntimeMode = runtimeMode
			ports[i].PoolAlgorithm = poolAlgorithm
		}
	}
	cfg.Ports = ports
}

func portMetadata(portKey string) string {
	return fmt.Sprintf(`{"port_key":%q}`, normalizedPortKey(portKey))
}

type dispatcherRelay struct {
	cfg        config.DispatcherConfig
	ports      []config.PortConfig
	repo       *sqliteRepo.Repository
	httpLn     net.Listener
	socksLn    net.Listener
	httpSrv    *http.Server
	httpClient *http.Client
	closeOnce  sync.Once
	snapshotMu sync.RWMutex
	snapshot   dispatcherSnapshot
}

type dispatcherSnapshot struct {
	candidates []pool.DispatcherCandidate
	indexByKey map[string]int
}

func (d *dispatcherRelay) updateSnapshotCandidate(candidate pool.DispatcherCandidate, healthy bool, score float64, lastFailureAt string) {
	key := dispatcherCandidateKey(candidate)
	d.snapshotMu.Lock()
	defer d.snapshotMu.Unlock()
	idx, ok := d.snapshot.indexByKey[key]
	if !ok || idx < 0 || idx >= len(d.snapshot.candidates) {
		return
	}
	updated := d.snapshot.candidates[idx]
	updated.Weight = candidate.Weight
	updated.Protocol = candidate.Protocol
	if healthy {
		updated.HealthyNodeCount = 1
		updated.CurrentActiveSet = true
		updated.CurrentActiveScore = score
		updated.LastFailureAt = lastFailureAt
	} else {
		updated.HealthyNodeCount = 0
		updated.CurrentActiveSet = false
		updated.CurrentActiveScore = 0
		updated.LastFailureAt = lastFailureAt
	}
	d.snapshot.candidates[idx] = updated
}

type laneTarget struct {
	Candidate pool.DispatcherCandidate
	URL       *url.URL
}

func newDispatcherRelay(cfg config.Config, repo *sqliteRepo.Repository) (*dispatcherRelay, error) {
	if !cfg.Dispatcher.Enabled {
		return nil, nil
	}
	httpLn, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Dispatcher.HTTPListenAddr, cfg.Dispatcher.HTTPListenPort))
	if err != nil {
		return nil, err
	}
	socksLn, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Dispatcher.SOCKSListenAddr, cfg.Dispatcher.SOCKSListenPort))
	if err != nil {
		_ = httpLn.Close()
		return nil, err
	}
	d := &dispatcherRelay{
		cfg:        cfg.Dispatcher,
		ports:      cfg.ResolvedPorts(),
		repo:       repo,
		httpLn:     httpLn,
		socksLn:    socksLn,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	if err := d.rebuildSnapshot(context.Background()); err != nil {
		_ = httpLn.Close()
		_ = socksLn.Close()
		return nil, err
	}
	d.httpSrv = &http.Server{Handler: http.HandlerFunc(d.serveHTTP)}
	go func() {
		_ = d.httpSrv.Serve(httpLn)
	}()
	go func() {
		for {
			conn, err := socksLn.Accept()
			if err != nil {
				return
			}
			go d.serveSOCKS(conn)
		}
	}()
	return d, nil
}

func (d *dispatcherRelay) SetRepo(repo *sqliteRepo.Repository) {
	d.repo = repo
	_ = d.rebuildSnapshot(context.Background())
}

func (d *dispatcherRelay) Close() error {
	var err error
	d.closeOnce.Do(func() {
		if d.httpSrv != nil {
			err = d.httpSrv.Close()
		}
		if d.httpLn != nil {
			if closeErr := d.httpLn.Close(); err == nil {
				err = closeErr
			}
		}
		if d.socksLn != nil {
			if closeErr := d.socksLn.Close(); err == nil {
				err = closeErr
			}
		}
	})
	return err
}

func (d *dispatcherRelay) serveHTTP(w http.ResponseWriter, r *http.Request) {
	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	_ = r.Body.Close()
	ctx := r.Context()
	for _, rule := range d.cfg.Rules {
		if !dispatcherRuleMatches(rule, r) {
			continue
		}
		if rule.TargetPortKey != "" && rule.TargetLaneKey != "" {
			ctx = context.WithValue(ctx, dispatcherTargetLaneContextKey{}, dispatcherTargetLane{
				PortKey:  normalizedPortKey(rule.TargetPortKey),
				LaneKey:  rule.TargetLaneKey,
				Protocol: "http",
			})
			break
		}
	}
	if stickyKey := r.Header.Get("X-ProxyPools-Sticky-Key"); stickyKey != "" {
		if _, ok := targetLaneFromContext(ctx); !ok {
			ctx = context.WithValue(ctx, dispatcherStickyKeyContextKey{}, stickyKey)
		}
	}

	attempted := map[string]struct{}{}
	currentLaneKey := ""
	var lastErr error
	for {
		candidate, err := d.selectCandidate(ctx, currentLaneKey)
		if err != nil {
			if lastErr != nil {
				http.Error(w, lastErr.Error(), http.StatusBadGateway)
				return
			}
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if _, seen := attempted[dispatcherCandidateKey(candidate)]; seen {
			if lastErr != nil {
				http.Error(w, lastErr.Error(), http.StatusBadGateway)
				return
			}
			http.Error(w, "dispatcher fallback exhausted", http.StatusBadGateway)
			return
		}
		attempted[dispatcherCandidateKey(candidate)] = struct{}{}
		resp, targetURL, err := d.forwardHTTP(r, requestBody, candidate)
		if err != nil {
			now := time.Now().UTC().Format(time.RFC3339)
			_ = d.recordFallbackEvent(r.Context(), candidate.PortKey, err)
			_ = d.repo.UpdateRequestLaneError(r.Context(), candidate.PortKey, candidate.LaneKey, now, "error")
			d.updateSnapshotCandidate(candidate, false, 0, now)
			lastErr = err
			currentLaneKey = dispatcherCandidateKey(candidate)
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339)
		_ = d.repo.UpdateRequestLaneUsage(r.Context(), candidate.PortKey, candidate.LaneKey, now, "ready")
		d.updateSnapshotCandidate(candidate, true, candidate.CurrentActiveScore, candidate.LastFailureAt)
		defer resp.Body.Close()
		copyHeaders(w.Header(), resp.Header)
		w.Header().Set("X-ProxyPools-Dispatcher-Port", candidate.PortKey)
		w.Header().Set("X-ProxyPools-Dispatcher-Lane", candidate.LaneKey)
		w.Header().Set("X-ProxyPools-Dispatcher-Target", targetURL.Host)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}
}

func (d *dispatcherRelay) serveSOCKS(conn net.Conn) {
	defer conn.Close()
	buffer := make([]byte, 262)
	if _, err := io.ReadAtLeast(conn, buffer[:2], 2); err != nil {
		return
	}
	if buffer[0] != 0x05 {
		return
	}
	nMethods := int(buffer[1])
	if nMethods > 0 {
		if _, err := io.ReadFull(conn, buffer[2:2+nMethods]); err != nil {
			return
		}
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	if _, err := io.ReadAtLeast(conn, buffer[:4], 4); err != nil {
		return
	}
	if buffer[0] != 0x05 {
		return
	}
	if buffer[1] != 0x01 {
		_, _ = conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	addrLen := 0
	switch buffer[3] {
	case 0x01:
		addrLen = 4
	case 0x03:
		if _, err := io.ReadFull(conn, buffer[4:5]); err != nil {
			return
		}
		addrLen = int(buffer[4]) + 1
	case 0x04:
		addrLen = 16
	default:
		_, _ = conn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	toRead := addrLen + 2
	start := 4
	if buffer[3] == 0x03 {
		start = 5
	}
	if _, err := io.ReadFull(conn, buffer[start:start+toRead]); err != nil {
		return
	}
	requestPayload := append([]byte{}, buffer[:start+toRead]...)
	currentLaneKey := ""
	lastErr := error(nil)
	attempted := map[string]struct{}{}
	for {
		candidate, err := d.selectCandidate(context.Background(), currentLaneKey)
		if err != nil {
			_, _ = conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return
		}
		if _, seen := attempted[dispatcherCandidateKey(candidate)]; seen {
			_ = lastErr
			_, _ = conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return
		}
		attempted[dispatcherCandidateKey(candidate)] = struct{}{}
		targetConn, err := d.dialSOCKSLane(candidate)
		if err != nil {
			now := time.Now().UTC().Format(time.RFC3339)
			_ = d.recordFallbackEvent(context.Background(), candidate.PortKey, err)
			_ = d.repo.UpdateRequestLaneError(context.Background(), candidate.PortKey, candidate.LaneKey, now, "error")
			d.updateSnapshotCandidate(candidate, false, 0, now)
			lastErr = err
			currentLaneKey = dispatcherCandidateKey(candidate)
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339)
		_ = d.repo.UpdateRequestLaneUsage(context.Background(), candidate.PortKey, candidate.LaneKey, now, "ready")
		d.updateSnapshotCandidate(candidate, true, candidate.CurrentActiveScore, candidate.LastFailureAt)
		defer targetConn.Close()
		if _, err := targetConn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			return
		}
		if _, err := io.ReadFull(targetConn, buffer[:2]); err != nil {
			return
		}
		if buffer[1] != 0x00 {
			return
		}
		if _, err := targetConn.Write(requestPayload); err != nil {
			return
		}
		if _, err := io.ReadAtLeast(targetConn, buffer[:4], 4); err != nil {
			return
		}
		replyLen := 10
		switch buffer[3] {
		case 0x03:
			if _, err := io.ReadFull(targetConn, buffer[4:5]); err != nil {
				return
			}
			replyLen = 7 + int(buffer[4])
		case 0x04:
			replyLen = 22
		}
		if replyLen > 4 {
			if _, err := io.ReadFull(targetConn, buffer[4:replyLen]); err != nil {
				return
			}
		}
		if _, err := conn.Write(buffer[:replyLen]); err != nil {
			return
		}
		go func() {
			_, _ = io.Copy(targetConn, conn)
			_ = targetConn.Close()
		}()
		_, _ = io.Copy(conn, targetConn)
		return
	}
}

func (d *dispatcherRelay) dialSOCKSLane(candidate pool.DispatcherCandidate) (net.Conn, error) {
	for _, port := range d.ports {
		if port.Key != normalizedPortKey(candidate.PortKey) {
			continue
		}
		lanePort, err := laneListenPort(port, candidate.Protocol, candidate.LaneKey)
		if err != nil {
			return nil, err
		}
		return net.Dial("tcp", fmt.Sprintf("%s:%d", laneListenAddr(port, candidate.Protocol, candidate.LaneKey), lanePort))
	}
	return nil, fmt.Errorf("dispatcher socks target lane not found")
}

func (d *dispatcherRelay) forwardHTTP(r *http.Request, requestBody []byte, candidate pool.DispatcherCandidate) (*http.Response, *url.URL, error) {
	target, err := d.targetHTTPLane(candidate)
	if err != nil {
		return nil, nil, err
	}
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, target.URL.String()+r.URL.RequestURI(), bytes.NewReader(requestBody))
	if err != nil {
		return nil, nil, err
	}
	proxyReq.Header = r.Header.Clone()
	proxyReq.Host = target.URL.Host
	proxyReq.Header.Set("X-ProxyPools-Dispatcher-Port", candidate.PortKey)
	proxyReq.Header.Set("X-ProxyPools-Dispatcher-Lane", candidate.LaneKey)
	resp, err := d.httpClient.Do(proxyReq)
	if err != nil {
		return nil, target.URL, err
	}
	if resp.StatusCode >= http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, target.URL, fmt.Errorf("upstream %s returned %d: %s", target.URL.Host, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp, target.URL, nil
}

func (d *dispatcherRelay) recordFallbackEvent(ctx context.Context, portKey string, err error) error {
	if d.repo == nil {
		return nil
	}
	return d.repo.CreateEventLog(ctx, model.EventLog{
		EventType:    "dispatcher_fallback",
		Level:        "warn",
		Message:      fmt.Sprintf("dispatcher fallback from port %s: %v", portKey, err),
		MetadataJSON: portMetadata(portKey),
	})
}

func (d *dispatcherRelay) rebuildSnapshot(ctx context.Context) error {
	if d.repo == nil {
		return fmt.Errorf("dispatcher repository unavailable")
	}
	states, err := d.repo.ListPortRuntimeStates(ctx)
	if err != nil {
		return err
	}
	candidates := make([]pool.DispatcherCandidate, 0, len(states))
	for _, state := range states {
		statuses, err := d.repo.ListNodeRuntimeStatusesByPort(ctx, state.PortKey)
		if err != nil {
			return err
		}
		scoreByNodeID := make(map[int64]model.NodeRuntimeStatus, len(statuses))
		for _, status := range statuses {
			scoreByNodeID[status.NodeID] = status
		}
		lanes, err := d.repo.ListRequestLaneStatesByPort(ctx, state.PortKey)
		if err != nil {
			return err
		}
		for _, lane := range lanes {
			candidate := pool.DispatcherCandidate{PortKey: state.PortKey, LaneKey: lane.LaneKey, Protocol: lane.Protocol, Weight: lane.Weight}
			status, ok := scoreByNodeID[lane.AssignedNodeID]
			if ok && status.State == "active" && !status.ManualDisabled && lane.AssignedNodeID > 0 && lane.State == "ready" {
				candidate.HealthyNodeCount = 1
				candidate.CurrentActiveSet = true
				candidate.CurrentActiveScore = status.Score
				candidate.LastFailureAt = status.LastFailureAt
			}
			candidates = append(candidates, candidate)
		}
	}
	indexByKey := make(map[string]int, len(candidates))
	for i, candidate := range candidates {
		indexByKey[dispatcherCandidateKey(candidate)] = i
	}
	d.snapshotMu.Lock()
	d.snapshot = dispatcherSnapshot{candidates: candidates, indexByKey: indexByKey}
	d.snapshotMu.Unlock()
	return nil
}

func (d *dispatcherRelay) snapshotCandidates() []pool.DispatcherCandidate {
	d.snapshotMu.RLock()
	defer d.snapshotMu.RUnlock()
	return append([]pool.DispatcherCandidate(nil), d.snapshot.candidates...)
}

func (d *dispatcherRelay) selectPortKey(ctx context.Context, currentPortKey string) (string, error) {
	candidate, err := d.selectCandidate(ctx, currentPortKey)
	if err != nil {
		return "", err
	}
	return candidate.PortKey, nil
}

func (d *dispatcherRelay) selectCandidate(ctx context.Context, currentLaneKey string) (pool.DispatcherCandidate, error) {
	candidates := d.snapshotCandidates()
	if stickyKey := stickyKeyFromContext(ctx); stickyKey != "" {
		if selected, ok := pool.SelectStickyDispatcherLane(stickyKey, candidates); ok {
			return selected, nil
		}
	}
	if target, ok := targetLaneFromContext(ctx); ok {
		if selected, ok := selectTargetDispatcherLane(currentLaneKey, target, candidates); ok {
			return selected, nil
		}
	}
	var selected pool.DispatcherCandidate
	var ok bool
	switch d.cfg.Algorithm {
	case "random":
		selected, ok = pool.SelectRandomDispatcherLane(currentLaneKey, candidates)
	case "balance":
		selected, ok = pool.SelectBalanceDispatcherLane(currentLaneKey, candidates)
	default:
		selected, ok = pool.SelectSequentialDispatcherLane(currentLaneKey, candidates)
	}
	if !ok {
		return pool.DispatcherCandidate{}, fmt.Errorf("no healthy dispatcher lane available")
	}
	return selected, nil
}

func selectTargetDispatcherLane(currentLaneKey string, target dispatcherTargetLane, candidates []pool.DispatcherCandidate) (pool.DispatcherCandidate, bool) {
	for _, candidate := range candidates {
		if normalizedPortKey(candidate.PortKey) != normalizedPortKey(target.PortKey) {
			continue
		}
		if candidate.LaneKey != target.LaneKey {
			continue
		}
		if target.Protocol != "" && candidate.Protocol != target.Protocol {
			continue
		}
		if candidate.HealthyNodeCount <= 0 {
			continue
		}
		if currentLaneKey != "" && dispatcherCandidateKey(candidate) == currentLaneKey {
			continue
		}
		return candidate, true
	}
	return pool.DispatcherCandidate{}, false
}

func dispatcherRuleMatches(rule config.DispatcherRuleConfig, r *http.Request) bool {
	if rule.Host != "" && !strings.EqualFold(rule.Host, requestHostWithoutPort(r.Host)) {
		return false
	}
	if rule.HeaderName != "" && r.Header.Get(rule.HeaderName) != rule.HeaderValue {
		return false
	}
	return true
}

func requestHostWithoutPort(value string) string {
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return strings.Trim(value, "[]")
}

type dispatcherTargetLane struct {
	PortKey  string
	LaneKey  string
	Protocol string
}

func targetLaneFromContext(ctx context.Context) (dispatcherTargetLane, bool) {
	value, ok := ctx.Value(dispatcherTargetLaneContextKey{}).(dispatcherTargetLane)
	return value, ok
}

func stickyKeyFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(dispatcherStickyKeyContextKey{}).(string); ok {
		return value
	}
	return ""
}

type dispatcherTargetLaneContextKey struct{}

type dispatcherStickyKeyContextKey struct{}

func (d *dispatcherRelay) status(ctx context.Context) (map[string]any, error) {
	candidate, err := d.selectCandidate(ctx, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"enabled":                true,
		"http_listen":            fmt.Sprintf("%s:%d", d.cfg.HTTPListenAddr, d.cfg.HTTPListenPort),
		"socks_listen":           fmt.Sprintf("%s:%d", d.cfg.SOCKSListenAddr, d.cfg.SOCKSListenPort),
		"algorithm":              d.cfg.Algorithm,
		"selected_port_key":      candidate.PortKey,
		"selected_lane_key":      candidate.LaneKey,
		"selected_healthy_nodes": candidate.HealthyNodeCount,
		"selected_score":         candidate.CurrentActiveScore,
	}, nil
}

func (d *dispatcherRelay) targetHTTPLane(candidate pool.DispatcherCandidate) (*laneTarget, error) {
	for _, port := range d.ports {
		if port.Key != normalizedPortKey(candidate.PortKey) {
			continue
		}
		lanePort, err := laneListenPort(port, candidate.Protocol, candidate.LaneKey)
		if err != nil {
			return nil, err
		}
		return &laneTarget{
			Candidate: candidate,
			URL:       &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", laneListenAddr(port, candidate.Protocol, candidate.LaneKey), lanePort)},
		}, nil
	}
	return nil, fmt.Errorf("dispatcher target lane not found")
}

func dispatcherCandidateKey(candidate pool.DispatcherCandidate) string {
	return candidate.PortKey + ":" + candidate.LaneKey + ":" + candidate.Protocol
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
