package web

import (
	"context"
	"embed"
	"io/fs"
	"net/http"

	"proxypools/internal/config"
	"proxypools/internal/model"
	"proxypools/internal/runtime"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed static/*
var staticFS embed.FS

type RuntimeStateProvider interface {
	RuntimeSummary(ctx context.Context) (map[string]any, error)
}

type PortRuntimeStateProvider interface {
	RuntimeSummaryByPort(ctx context.Context, portKey string) (map[string]any, error)
	ListPortRuntimeStates(ctx context.Context) ([]model.PortRuntimeState, error)
}

type ManualSwitchService interface {
	SwitchNode(ctx context.Context, nodeID int64) error
}

type PortManualSwitchService interface {
	SwitchNodeByPort(ctx context.Context, portKey string, nodeID int64) error
}

type SubscriptionService interface {
	GetPrimarySubscription(ctx context.Context) (map[string]any, error)
	RefreshSubscription(ctx context.Context) (map[string]any, error)
}

type NodeAdminService interface {
	SetNodeManualDisabled(ctx context.Context, nodeID int64, disabled bool) error
}

type PortNodeAdminService interface {
	SetNodeManualDisabledByPort(ctx context.Context, portKey string, nodeID int64, disabled bool) error
}

type RuntimeAdminService interface {
	UnlockSelection(ctx context.Context) error
	UpdateRuntimeSettings(ctx context.Context, runtimeMode string, poolAlgorithm string) error
}

type PortRuntimeAdminService interface {
	UnlockSelectionByPort(ctx context.Context, portKey string) error
	UpdateRuntimeSettingsByPort(ctx context.Context, portKey string, runtimeMode string, poolAlgorithm string) error
}

type EventLogService interface {
	ListEventLogs(ctx context.Context, limit int) ([]map[string]any, error)
}

type DispatcherStatusService interface {
	GetDispatcherStatus(ctx context.Context) (map[string]any, error)
}

type Dependencies struct {
	AdminUsername           string
	AdminPasswordHash       string
	Runtime                 *runtime.Process
	ConfigPath              string
	AdminListen             string
	HTTPListen              string
	SOCKSListen             string
	HealthListen            string
	RuntimeMode             string
	PoolAlgorithm           string
	Ports                   []config.PortConfig
	SubscriptionConfigured  bool
	RuntimeStateProvider    RuntimeStateProvider
	ManualSwitchService     ManualSwitchService
	SubscriptionService     SubscriptionService
	NodeAdminService        NodeAdminService
	RuntimeAdminService     RuntimeAdminService
	EventLogService         EventLogService
	DispatcherStatusService DispatcherStatusService
}

func NewRouter(dep Dependencies) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	staticFiles, _ := fs.Sub(staticFS, "static")

	r.Get("/healthz", healthHandler)

	r.Group(func(private chi.Router) {
		private.Use(BasicAuth(AuthConfig{Username: dep.AdminUsername, PasswordHash: dep.AdminPasswordHash}))
		private.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))
		private.Get("/", func(w http.ResponseWriter, r *http.Request) {
			data, err := fs.ReadFile(staticFiles, "index.html")
			if err != nil {
				http.Error(w, "index not found", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		})
		private.Get("/api/runtime", runtimeHandler(dep))
		private.Get("/api/dispatcher", dispatcherStatusHandler(dep))
		private.Get("/api/ports", portsHandler(dep))
		private.Get("/api/ports/{portKey}/runtime", runtimeByPortHandler(dep))
		private.Get("/api/subscription", subscriptionHandler(dep))
		private.Get("/api/events", eventsHandler(dep))
		private.Post("/api/runtime/settings", runtimeSettingsHandler(dep))
		private.Post("/api/ports/{portKey}/runtime/settings", runtimeSettingsByPortHandler(dep))
		private.Post("/api/subscription/refresh", refreshSubscriptionHandler(dep))
		private.Post("/api/runtime/unlock", unlockSelectionHandler(dep))
		private.Post("/api/ports/{portKey}/runtime/unlock", unlockSelectionByPortHandler(dep))
		private.Post("/api/nodes/{id}/enable", enableNodeHandler(dep))
		private.Post("/api/nodes/{id}/disable", disableNodeHandler(dep))
		private.Post("/api/nodes/{id}/switch", switchNodeHandler(dep))
		private.Post("/api/ports/{portKey}/nodes/{id}/enable", enableNodeByPortHandler(dep))
		private.Post("/api/ports/{portKey}/nodes/{id}/disable", disableNodeByPortHandler(dep))
		private.Post("/api/ports/{portKey}/nodes/{id}/switch", switchNodeByPortHandler(dep))
	})

	return r
}
