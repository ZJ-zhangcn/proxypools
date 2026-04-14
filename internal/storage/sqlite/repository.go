package sqlite

import (
	"context"
	"database/sql"

	"proxypools/internal/config"
	"proxypools/internal/model"
)

func (r *Repository) UpsertSubscription(ctx context.Context, sub *model.Subscription) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO subscriptions(name, url, enabled)
		VALUES (?, ?, 1)
		ON CONFLICT(name) DO UPDATE SET url = excluded.url, enabled = 1
	`, sub.Name, sub.URL)
	return err
}

func (r *Repository) GetPrimarySubscription(ctx context.Context) (*model.Subscription, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, name, url, enabled, last_fetch_at, last_fetch_status, last_fetch_error, last_added_nodes, last_removed_nodes FROM subscriptions ORDER BY id LIMIT 1`)

	var sub model.Subscription
	var enabled int
	if err := row.Scan(&sub.ID, &sub.Name, &sub.URL, &enabled, &sub.LastFetchAt, &sub.LastFetchStatus, &sub.LastFetchError, &sub.LastAddedNodes, &sub.LastRemovedNodes); err != nil {
		return nil, err
	}

	sub.Enabled = enabled == 1
	return &sub, nil
}

func (r *Repository) UpdateSubscriptionFetchResult(ctx context.Context, subscriptionID int64, fetchedAt string, status string, fetchError string, added int, removed int) error {
	_, err := r.db.ExecContext(ctx, `UPDATE subscriptions SET last_fetch_at = ?, last_fetch_status = ?, last_fetch_error = ?, last_added_nodes = ?, last_removed_nodes = ? WHERE id = ?`, fetchedAt, status, fetchError, added, removed, subscriptionID)
	return err
}

func (r *Repository) SetNodeManualDisabled(ctx context.Context, nodeID int64, disabled bool) error {
	return r.SetPortNodeManualDisabled(ctx, config.DefaultPortKey, nodeID, disabled)
}

func (r *Repository) SetPortNodeManualDisabled(ctx context.Context, portKey string, nodeID int64, disabled bool) error {
	portKey = normalizePortKey(portKey)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO port_node_state(port_key, node_id, manual_disabled)
		SELECT ?, id, 0 FROM nodes WHERE id = ?
	`, portKey, nodeID); err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, `UPDATE port_node_state SET manual_disabled = ? WHERE port_key = ? AND node_id = ?`, boolToInt(disabled), portKey, nodeID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}

	if portKey == config.DefaultPortKey {
		statusResult, err := tx.ExecContext(ctx, `UPDATE node_runtime_status SET manual_disabled = ? WHERE node_id = ?`, boolToInt(disabled), nodeID)
		if err != nil {
			return err
		}
		statusAffected, err := statusResult.RowsAffected()
		if err != nil {
			return err
		}
		if statusAffected == 0 {
			return sql.ErrNoRows
		}
	}

	return tx.Commit()
}

func (r *Repository) GetPortNodeState(ctx context.Context, portKey string, nodeID int64) (*model.PortNodeState, error) {
	portKey = normalizePortKey(portKey)
	row := r.db.QueryRowContext(ctx, `SELECT port_key, node_id, manual_disabled FROM port_node_state WHERE port_key = ? AND node_id = ?`, portKey, nodeID)
	var state model.PortNodeState
	var manualDisabled int
	if err := row.Scan(&state.PortKey, &state.NodeID, &manualDisabled); err != nil {
		return nil, err
	}
	state.ManualDisabled = manualDisabled == 1
	return &state, nil
}

func (r *Repository) GetRequestLaneState(ctx context.Context, portKey string, laneKey string) (*model.RequestLaneState, error) {
	portKey = normalizePortKey(portKey)
	row := r.db.QueryRowContext(ctx, `SELECT port_key, lane_key, protocol, assigned_node_id, weight, state, last_switch_reason, last_switch_at, last_used_at, last_error_at FROM request_lane_state WHERE port_key = ? AND lane_key = ?`, portKey, laneKey)
	var state model.RequestLaneState
	if err := row.Scan(&state.PortKey, &state.LaneKey, &state.Protocol, &state.AssignedNodeID, &state.Weight, &state.State, &state.LastSwitchReason, &state.LastSwitchAt, &state.LastUsedAt, &state.LastErrorAt); err != nil {
		return nil, err
	}
	return &state, nil
}

func (r *Repository) ListRequestLaneStatesByPort(ctx context.Context, portKey string) ([]model.RequestLaneState, error) {
	portKey = normalizePortKey(portKey)
	rows, err := r.db.QueryContext(ctx, `SELECT port_key, lane_key, protocol, assigned_node_id, weight, state, last_switch_reason, last_switch_at, last_used_at, last_error_at FROM request_lane_state WHERE port_key = ? ORDER BY lane_key`, portKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.RequestLaneState
	for rows.Next() {
		var item model.RequestLaneState
		if err := rows.Scan(&item.PortKey, &item.LaneKey, &item.Protocol, &item.AssignedNodeID, &item.Weight, &item.State, &item.LastSwitchReason, &item.LastSwitchAt, &item.LastUsedAt, &item.LastErrorAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) UpsertRequestLaneState(ctx context.Context, state model.RequestLaneState) error {
	state.PortKey = normalizePortKey(state.PortKey)
	if state.Weight <= 0 {
		state.Weight = 1
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO request_lane_state(port_key, lane_key, protocol, assigned_node_id, weight, state, last_switch_reason, last_switch_at, last_used_at, last_error_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(port_key, lane_key) DO UPDATE SET
			protocol = excluded.protocol,
			assigned_node_id = excluded.assigned_node_id,
			weight = excluded.weight,
			state = excluded.state,
			last_switch_reason = excluded.last_switch_reason,
			last_switch_at = excluded.last_switch_at,
			last_used_at = excluded.last_used_at,
			last_error_at = excluded.last_error_at
	`, state.PortKey, state.LaneKey, state.Protocol, state.AssignedNodeID, state.Weight, state.State, state.LastSwitchReason, state.LastSwitchAt, state.LastUsedAt, state.LastErrorAt)
	return err
}

func (r *Repository) DeleteRequestLaneStatesByPort(ctx context.Context, portKey string) error {
	portKey = normalizePortKey(portKey)
	_, err := r.db.ExecContext(ctx, `DELETE FROM request_lane_state WHERE port_key = ?`, portKey)
	return err
}

func (r *Repository) UpdateRequestLaneUsage(ctx context.Context, portKey string, laneKey string, usedAt string, state string) error {
	portKey = normalizePortKey(portKey)
	_, err := r.db.ExecContext(ctx, `UPDATE request_lane_state SET last_used_at = ?, state = ? WHERE port_key = ? AND lane_key = ?`, usedAt, state, portKey, laneKey)
	return err
}

func (r *Repository) UpdateRequestLaneError(ctx context.Context, portKey string, laneKey string, errorAt string, state string) error {
	portKey = normalizePortKey(portKey)
	_, err := r.db.ExecContext(ctx, `UPDATE request_lane_state SET last_error_at = ?, state = ? WHERE port_key = ? AND lane_key = ?`, errorAt, state, portKey, laneKey)
	return err
}

func (r *Repository) CreateEventLog(ctx context.Context, event model.EventLog) error {
	metadataJSON := event.MetadataJSON
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO event_logs(event_type, level, message, related_node_id, metadata_json) VALUES (?, ?, ?, ?, ?)`, event.EventType, event.Level, event.Message, event.RelatedNodeID, metadataJSON)
	return err
}

func (r *Repository) ListEventLogs(ctx context.Context, limit int) ([]model.EventLog, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, event_type, level, message, related_node_id, metadata_json, created_at FROM event_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.EventLog
	for rows.Next() {
		var item model.EventLog
		if err := rows.Scan(&item.ID, &item.EventType, &item.Level, &item.Message, &item.RelatedNodeID, &item.MetadataJSON, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ReplaceNodesForSubscription(ctx context.Context, subscriptionID int64, nodes []model.Node) ([]model.Node, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	portKeys, err := listPortKeysTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(portKeys) == 0 {
		portKeys = []string{config.DefaultPortKey}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM port_node_state WHERE node_id IN (SELECT id FROM nodes WHERE source_subscription_id = ?)`, subscriptionID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_runtime_status WHERE node_id IN (SELECT id FROM nodes WHERE source_subscription_id = ?)`, subscriptionID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE source_subscription_id = ?`, subscriptionID); err != nil {
		return nil, err
	}

	stored := make([]model.Node, 0, len(nodes))
	for _, node := range nodes {
		node.SourceSubscriptionID = subscriptionID
		result, err := tx.ExecContext(ctx, `
			INSERT INTO nodes(source_subscription_id, source_key, name, protocol_type, server, port, payload_json, enabled, removed)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, node.SourceSubscriptionID, node.SourceKey, node.Name, node.ProtocolType, node.Server, node.Port, node.PayloadJSON, boolToInt(node.Enabled), boolToInt(node.Removed))
		if err != nil {
			return nil, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, err
		}
		node.ID = id
		stored = append(stored, node)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO node_runtime_status(node_id, state, tier, score, latency_ms, recent_success_rate, consecutive_failures, last_check_at, last_success_at, last_failure_at, cooldown_until, manual_disabled)
			VALUES (?, 'active', 'L3', 0, 0, 0, 0, '', '', '', '', 0)
		`, node.ID); err != nil {
			return nil, err
		}
		for _, portKey := range portKeys {
			if _, err := tx.ExecContext(ctx, `INSERT INTO port_node_state(port_key, node_id, manual_disabled) VALUES (?, ?, 0)`, portKey, node.ID); err != nil {
				return nil, err
			}
		}
	}

	if len(stored) > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO runtime_state(id, current_active_node_id, selection_mode, runtime_mode, pool_algorithm, restart_required, last_switch_reason, last_switch_at)
			VALUES (1, ?, 'auto', 'single_active', 'sequential', 0, 'initial_selection', CURRENT_TIMESTAMP)
			ON CONFLICT(id) DO UPDATE SET
				current_active_node_id = excluded.current_active_node_id,
				selection_mode = excluded.selection_mode,
				restart_required = excluded.restart_required,
				last_switch_reason = excluded.last_switch_reason,
				last_switch_at = excluded.last_switch_at
		`, stored[0].ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO port_runtime_state(port_key, current_active_node_id, selection_mode, runtime_mode, pool_algorithm, restart_required, last_switch_reason, last_switch_at)
			VALUES (?, ?, 'auto', 'single_active', 'sequential', 0, 'initial_selection', CURRENT_TIMESTAMP)
			ON CONFLICT(port_key) DO UPDATE SET
				current_active_node_id = excluded.current_active_node_id,
				selection_mode = excluded.selection_mode,
				restart_required = excluded.restart_required,
				last_switch_reason = excluded.last_switch_reason,
				last_switch_at = excluded.last_switch_at
		`, config.DefaultPortKey, stored[0].ID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}

func (r *Repository) ListNodeRuntimeStatuses(ctx context.Context) ([]model.NodeRuntimeStatus, error) {
	return r.ListNodeRuntimeStatusesByPort(ctx, config.DefaultPortKey)
}

func (r *Repository) ListNodeRuntimeStatusesByPort(ctx context.Context, portKey string) ([]model.NodeRuntimeStatus, error) {
	portKey = normalizePortKey(portKey)
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			s.node_id,
			s.state,
			s.tier,
			s.score,
			s.latency_ms,
			s.recent_success_rate,
			s.consecutive_failures,
			s.last_check_at,
			s.last_success_at,
			s.last_failure_at,
			s.cooldown_until,
			CASE WHEN p.node_id IS NULL THEN s.manual_disabled ELSE p.manual_disabled END AS manual_disabled
		FROM node_runtime_status s
		LEFT JOIN port_node_state p ON p.port_key = ? AND p.node_id = s.node_id
		ORDER BY s.node_id
	`, portKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.NodeRuntimeStatus
	for rows.Next() {
		var item model.NodeRuntimeStatus
		var manualDisabled int
		if err := rows.Scan(&item.NodeID, &item.State, &item.Tier, &item.Score, &item.LatencyMS, &item.RecentSuccessRate, &item.ConsecutiveFailures, &item.LastCheckAt, &item.LastSuccessAt, &item.LastFailureAt, &item.CooldownUntil, &manualDisabled); err != nil {
			return nil, err
		}
		item.ManualDisabled = manualDisabled == 1
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) UpsertNodeRuntimeStatus(ctx context.Context, status model.NodeRuntimeStatus) error {
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO node_runtime_status(node_id, state, tier, score, latency_ms, recent_success_rate, consecutive_failures, last_check_at, last_success_at, last_failure_at, cooldown_until, manual_disabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			state = excluded.state,
			tier = excluded.tier,
			score = excluded.score,
			latency_ms = excluded.latency_ms,
			recent_success_rate = excluded.recent_success_rate,
			consecutive_failures = excluded.consecutive_failures,
			last_check_at = excluded.last_check_at,
			last_success_at = excluded.last_success_at,
			last_failure_at = excluded.last_failure_at,
			cooldown_until = excluded.cooldown_until,
			manual_disabled = excluded.manual_disabled
	`, status.NodeID, status.State, status.Tier, status.Score, status.LatencyMS, status.RecentSuccessRate, status.ConsecutiveFailures, status.LastCheckAt, status.LastSuccessAt, status.LastFailureAt, status.CooldownUntil, boolToInt(status.ManualDisabled)); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO port_node_state(port_key, node_id, manual_disabled)
		VALUES (?, ?, ?)
		ON CONFLICT(port_key, node_id) DO UPDATE SET manual_disabled = excluded.manual_disabled
	`, config.DefaultPortKey, status.NodeID, boolToInt(status.ManualDisabled))
	return err
}

func (r *Repository) GetRuntimeState(ctx context.Context) (*model.RuntimeState, error) {
	portState, err := r.GetPortRuntimeState(ctx, config.DefaultPortKey)
	if err != nil {
		return nil, err
	}
	state := portState.RuntimeState
	return &state, nil
}

func (r *Repository) GetPortRuntimeState(ctx context.Context, portKey string) (*model.PortRuntimeState, error) {
	portKey = normalizePortKey(portKey)
	row := r.db.QueryRowContext(ctx, `SELECT port_key, current_active_node_id, selection_mode, runtime_mode, pool_algorithm, restart_required, last_switch_reason, last_switch_at FROM port_runtime_state WHERE port_key = ?`, portKey)
	var state model.PortRuntimeState
	var restartRequired int
	if err := row.Scan(&state.PortKey, &state.CurrentActiveNodeID, &state.SelectionMode, &state.RuntimeMode, &state.PoolAlgorithm, &restartRequired, &state.LastSwitchReason, &state.LastSwitchAt); err != nil {
		return nil, err
	}
	state.RestartRequired = restartRequired == 1
	return &state, nil
}

func (r *Repository) ListPortRuntimeStates(ctx context.Context) ([]model.PortRuntimeState, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT port_key, current_active_node_id, selection_mode, runtime_mode, pool_algorithm, restart_required, last_switch_reason, last_switch_at FROM port_runtime_state ORDER BY port_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.PortRuntimeState
	for rows.Next() {
		var item model.PortRuntimeState
		var restartRequired int
		if err := rows.Scan(&item.PortKey, &item.CurrentActiveNodeID, &item.SelectionMode, &item.RuntimeMode, &item.PoolAlgorithm, &restartRequired, &item.LastSwitchReason, &item.LastSwitchAt); err != nil {
			return nil, err
		}
		item.RestartRequired = restartRequired == 1
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) UpdateRuntimeState(ctx context.Context, state model.RuntimeState) error {
	return r.UpdatePortRuntimeState(ctx, model.PortRuntimeState{PortKey: config.DefaultPortKey, RuntimeState: state})
}

func (r *Repository) UpdatePortRuntimeState(ctx context.Context, state model.PortRuntimeState) error {
	state.PortKey = normalizePortKey(state.PortKey)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO port_runtime_state(port_key, current_active_node_id, selection_mode, runtime_mode, pool_algorithm, restart_required, last_switch_reason, last_switch_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(port_key) DO UPDATE SET
			current_active_node_id = excluded.current_active_node_id,
			selection_mode = excluded.selection_mode,
			runtime_mode = excluded.runtime_mode,
			pool_algorithm = excluded.pool_algorithm,
			restart_required = excluded.restart_required,
			last_switch_reason = excluded.last_switch_reason,
			last_switch_at = excluded.last_switch_at
	`, state.PortKey, state.CurrentActiveNodeID, state.SelectionMode, state.RuntimeMode, state.PoolAlgorithm, boolToInt(state.RestartRequired), state.LastSwitchReason, state.LastSwitchAt); err != nil {
		return err
	}

	if state.PortKey == config.DefaultPortKey {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO runtime_state(id, current_active_node_id, selection_mode, runtime_mode, pool_algorithm, restart_required, last_switch_reason, last_switch_at)
			VALUES (1, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				current_active_node_id = excluded.current_active_node_id,
				selection_mode = excluded.selection_mode,
				runtime_mode = excluded.runtime_mode,
				pool_algorithm = excluded.pool_algorithm,
				restart_required = excluded.restart_required,
				last_switch_reason = excluded.last_switch_reason,
				last_switch_at = excluded.last_switch_at
		`, state.CurrentActiveNodeID, state.SelectionMode, state.RuntimeMode, state.PoolAlgorithm, boolToInt(state.RestartRequired), state.LastSwitchReason, state.LastSwitchAt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *Repository) RuntimeSummary(ctx context.Context) (map[string]any, error) {
	return r.RuntimeSummaryByPort(ctx, config.DefaultPortKey)
}

func (r *Repository) RuntimeSummaryByPort(ctx context.Context, portKey string) (map[string]any, error) {
	state, err := r.GetPortRuntimeState(ctx, portKey)
	if err != nil {
		return nil, err
	}
	statuses, err := r.ListNodeRuntimeStatusesByPort(ctx, portKey)
	if err != nil {
		return nil, err
	}
	lanes, err := r.ListRequestLaneStatesByPort(ctx, portKey)
	if err != nil {
		return nil, err
	}
	nodes, err := r.ListNodes(ctx)
	if err != nil {
		return nil, err
	}

	statusByNodeID := make(map[int64]model.NodeRuntimeStatus, len(statuses))
	healthy := 0
	lastHealthCheckAt := ""
	for _, status := range statuses {
		statusByNodeID[status.NodeID] = status
		if status.State == "active" && !status.ManualDisabled {
			healthy++
		}
		if status.LastCheckAt > lastHealthCheckAt {
			lastHealthCheckAt = status.LastCheckAt
		}
	}

	details := make([]model.NodeHealthDetail, 0, len(nodes))
	for _, node := range nodes {
		status := statusByNodeID[node.ID]
		details = append(details, model.NodeHealthDetail{
			ID:                  node.ID,
			Name:                node.Name,
			ProtocolType:        node.ProtocolType,
			Server:              node.Server,
			Port:                node.Port,
			State:               status.State,
			Tier:                status.Tier,
			Score:               status.Score,
			LatencyMS:           status.LatencyMS,
			RecentSuccessRate:   status.RecentSuccessRate,
			ConsecutiveFailures: status.ConsecutiveFailures,
			CooldownUntil:       status.CooldownUntil,
			ManualDisabled:      status.ManualDisabled,
			IsActive:            node.ID == state.CurrentActiveNodeID,
		})
	}

	readyLanes := 0
	for _, lane := range lanes {
		if lane.AssignedNodeID > 0 && lane.State == "ready" {
			readyLanes++
		}
	}

	summary := model.RuntimeSummary(state.CurrentActiveNodeID, state.SelectionMode, state.RestartRequired, healthy, len(statuses), state.LastSwitchReason, state.LastSwitchAt)
	summary["port_key"] = normalizePortKey(portKey)
	summary["runtime_mode"] = state.RuntimeMode
	summary["pool_algorithm"] = state.PoolAlgorithm
	summary["last_health_check_at"] = lastHealthCheckAt
	summary["node_details"] = details
	summary["lane_count"] = len(lanes)
	summary["ready_lane_count"] = readyLanes
	summary["lane_details"] = lanes
	return summary, nil
}

func listPortKeysTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT port_key FROM port_runtime_state ORDER BY port_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []string
	for rows.Next() {
		var portKey string
		if err := rows.Scan(&portKey); err != nil {
			return nil, err
		}
		items = append(items, portKey)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func normalizePortKey(portKey string) string {
	if portKey == "" {
		return config.DefaultPortKey
	}
	return portKey
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
