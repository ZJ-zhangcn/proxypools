package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"proxypools/internal/config"

	"github.com/go-chi/chi/v5"
)

type runtimeSettingsRequest struct {
	RuntimeMode   string `json:"runtime_mode"`
	PoolAlgorithm string `json:"pool_algorithm"`
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func runtimeHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeRuntimeResponse(w, r, dep, defaultPortKey())
	}
}

func runtimeByPortHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeRuntimeResponse(w, r, dep, chi.URLParam(r, "portKey"))
	}
}

func writeRuntimeResponse(w http.ResponseWriter, r *http.Request, dep Dependencies, portKey string) {
	w.Header().Set("Content-Type", "application/json")
	status := map[string]any{
		"mode":                       "auto",
		"selection_mode":             "auto",
		"runtime_mode":               dep.RuntimeMode,
		"pool_algorithm":             dep.PoolAlgorithm,
		"restart_required":           false,
		"running":                    false,
		"config_path":                dep.ConfigPath,
		"admin_listen":               dep.AdminListen,
		"http_listen":                dep.HTTPListen,
		"socks_listen":               dep.SOCKSListen,
		"health_listen":              dep.HealthListen,
		"subscription_configured":    dep.SubscriptionConfigured,
		"last_error":                 "",
		"last_apply_at":              "",
		"last_apply_status":          "",
		"last_subscription_fetch_at": "",
		"last_subscription_status":   "",
		"last_health_check_at":       "",
		"last_switch_reason":         "",
		"last_switch_at":             "",
		"port_key":                   normalizePortKeyParam(portKey),
	}
	if port, ok := dependencyPortByKey(dep, portKey); ok {
		status["http_listen"] = port.HTTPListenAddr + ":" + strconv.Itoa(port.HTTPListenPort)
		status["socks_listen"] = port.SOCKSListenAddr + ":" + strconv.Itoa(port.SOCKSListenPort)
		status["runtime_mode"] = port.RuntimeMode
		status["pool_algorithm"] = port.PoolAlgorithm
	}
	if dep.Runtime != nil {
		snapshot := dep.Runtime.Snapshot(dep.SubscriptionConfigured)
		status["running"] = snapshot.Running
		status["restart_required"] = snapshot.RestartRequired
		status["config_path"] = snapshot.ConfigPath
		status["subscription_configured"] = snapshot.SubscriptionConfigured
		status["last_error"] = snapshot.LastError
		status["last_apply_at"] = snapshot.LastApplyAt
		status["last_apply_status"] = snapshot.LastApplyStatus
		if snapshot.PID > 0 {
			status["pid"] = snapshot.PID
		}
	}
	if dep.SubscriptionService != nil {
		if subscription, err := dep.SubscriptionService.GetPrimarySubscription(r.Context()); err == nil {
			if v, ok := subscription["last_fetch_at"]; ok {
				status["last_subscription_fetch_at"] = v
			}
			if v, ok := subscription["last_fetch_status"]; ok {
				status["last_subscription_status"] = v
			}
		}
	}
	if provider, ok := dep.RuntimeStateProvider.(PortRuntimeStateProvider); ok {
		if summary, err := provider.RuntimeSummaryByPort(r.Context(), normalizePortKeyParam(portKey)); err == nil {
			for k, v := range summary {
				status[k] = v
			}
			if selectionMode, ok := summary["selection_mode"]; ok {
				status["mode"] = selectionMode
				status["selection_mode"] = selectionMode
			}
		}
	} else if dep.RuntimeStateProvider != nil {
		if summary, err := dep.RuntimeStateProvider.RuntimeSummary(r.Context()); err == nil {
			for k, v := range summary {
				status[k] = v
			}
			if selectionMode, ok := summary["selection_mode"]; ok {
				status["mode"] = selectionMode
				status["selection_mode"] = selectionMode
			}
		}
	}
	_ = json.NewEncoder(w).Encode(status)
}

func portsHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		items := make([]map[string]any, 0, len(dep.Ports))
		for _, port := range dep.Ports {
			items = append(items, map[string]any{
				"key":            port.Key,
				"name":           port.Name,
				"http_listen":    port.HTTPListenAddr + ":" + strconv.Itoa(port.HTTPListenPort),
				"socks_listen":   port.SOCKSListenAddr + ":" + strconv.Itoa(port.SOCKSListenPort),
				"runtime_mode":   port.RuntimeMode,
				"pool_algorithm": port.PoolAlgorithm,
				"is_default":     port.Key == defaultPortKey(),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}
}

func dispatcherStatusHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dep.DispatcherStatusService == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false})
			return
		}
		status, err := dep.DispatcherStatusService.GetDispatcherStatus(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	}
}

func subscriptionHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dep.SubscriptionService == nil {
			http.Error(w, "subscription service unavailable", http.StatusServiceUnavailable)
			return
		}
		subscription, err := dep.SubscriptionService.GetPrimarySubscription(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(subscription)
	}
}

func runtimeSettingsHandler(dep Dependencies) http.HandlerFunc {
	return runtimeSettingsByPortInternal(dep, defaultPortKey())
}

func runtimeSettingsByPortHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runtimeSettingsByPortInternal(dep, chi.URLParam(r, "portKey"))(w, r)
	}
}

func runtimeSettingsByPortInternal(dep Dependencies, portKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dep.RuntimeAdminService == nil {
			http.Error(w, "runtime admin unavailable", http.StatusServiceUnavailable)
			return
		}
		var req runtimeSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if svc, ok := dep.RuntimeAdminService.(PortRuntimeAdminService); ok {
			if err := svc.UpdateRuntimeSettingsByPort(r.Context(), normalizePortKeyParam(portKey), req.RuntimeMode, req.PoolAlgorithm); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			if err := dep.RuntimeAdminService.UpdateRuntimeSettings(r.Context(), req.RuntimeMode, req.PoolAlgorithm); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func refreshSubscriptionHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dep.SubscriptionService == nil {
			http.Error(w, "subscription service unavailable", http.StatusServiceUnavailable)
			return
		}
		subscription, err := dep.SubscriptionService.RefreshSubscription(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(subscription)
	}
}

func eventsHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dep.EventLogService == nil {
			http.Error(w, "event log service unavailable", http.StatusServiceUnavailable)
			return
		}
		events, err := dep.EventLogService.ListEventLogs(r.Context(), 20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": events})
	}
}

func unlockSelectionHandler(dep Dependencies) http.HandlerFunc {
	return unlockSelectionByPortInternal(dep, defaultPortKey())
}

func unlockSelectionByPortHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		unlockSelectionByPortInternal(dep, chi.URLParam(r, "portKey"))(w, r)
	}
}

func unlockSelectionByPortInternal(dep Dependencies, portKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dep.RuntimeAdminService == nil {
			http.Error(w, "runtime admin unavailable", http.StatusServiceUnavailable)
			return
		}
		if svc, ok := dep.RuntimeAdminService.(PortRuntimeAdminService); ok {
			if err := svc.UnlockSelectionByPort(r.Context(), normalizePortKeyParam(portKey)); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			if err := dep.RuntimeAdminService.UnlockSelection(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func enableNodeHandler(dep Dependencies) http.HandlerFunc {
	return nodeManualDisabledHandler(dep, defaultPortKey(), false)
}

func disableNodeHandler(dep Dependencies) http.HandlerFunc {
	return nodeManualDisabledHandler(dep, defaultPortKey(), true)
}

func enableNodeByPortHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeManualDisabledHandler(dep, chi.URLParam(r, "portKey"), false)(w, r)
	}
}

func disableNodeByPortHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeManualDisabledHandler(dep, chi.URLParam(r, "portKey"), true)(w, r)
	}
}

func nodeManualDisabledHandler(dep Dependencies, portKey string, disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dep.NodeAdminService == nil {
			http.Error(w, "node admin unavailable", http.StatusServiceUnavailable)
			return
		}
		nodeID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || nodeID <= 0 {
			http.Error(w, "invalid node id", http.StatusBadRequest)
			return
		}
		if svc, ok := dep.NodeAdminService.(PortNodeAdminService); ok {
			if err := svc.SetNodeManualDisabledByPort(r.Context(), normalizePortKeyParam(portKey), nodeID, disabled); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			if err := dep.NodeAdminService.SetNodeManualDisabled(r.Context(), nodeID, disabled); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func switchNodeHandler(dep Dependencies) http.HandlerFunc {
	return switchNodeByPortInternal(dep, defaultPortKey())
}

func switchNodeByPortHandler(dep Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switchNodeByPortInternal(dep, chi.URLParam(r, "portKey"))(w, r)
	}
}

func switchNodeByPortInternal(dep Dependencies, portKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dep.ManualSwitchService == nil {
			http.Error(w, "manual switch unavailable", http.StatusServiceUnavailable)
			return
		}
		nodeID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || nodeID <= 0 {
			http.Error(w, "invalid node id", http.StatusBadRequest)
			return
		}
		if svc, ok := dep.ManualSwitchService.(PortManualSwitchService); ok {
			if err := svc.SwitchNodeByPort(r.Context(), normalizePortKeyParam(portKey), nodeID); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			if err := dep.ManualSwitchService.SwitchNode(r.Context(), nodeID); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func defaultPortKey() string {
	return config.DefaultPortKey
}

func normalizePortKeyParam(portKey string) string {
	if portKey == "" {
		return defaultPortKey()
	}
	return portKey
}

func dependencyPortByKey(dep Dependencies, portKey string) (config.PortConfig, bool) {
	portKey = normalizePortKeyParam(portKey)
	for _, port := range dep.Ports {
		if port.Key == portKey {
			return port, true
		}
	}
	return config.PortConfig{}, false
}
