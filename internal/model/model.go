package model

type RuntimeState struct {
	CurrentActiveNodeID int64
	SelectionMode       string
	RuntimeMode         string
	PoolAlgorithm       string
	RestartRequired     bool
	LastSwitchReason    string
	LastSwitchAt        string
}

type PortRuntimeState struct {
	PortKey string
	RuntimeState
}

type PortNodeState struct {
	PortKey        string
	NodeID         int64
	ManualDisabled bool
}

type RequestLaneState struct {
	PortKey          string `json:"port_key"`
	LaneKey          string `json:"lane_key"`
	Protocol         string `json:"protocol"`
	AssignedNodeID   int64  `json:"assigned_node_id"`
	Weight           int    `json:"weight"`
	State            string `json:"state"`
	LastSwitchReason string `json:"last_switch_reason"`
	LastSwitchAt     string `json:"last_switch_at"`
	LastUsedAt       string `json:"last_used_at"`
	LastErrorAt      string `json:"last_error_at"`
}

type Subscription struct {
	ID               int64
	Name             string
	URL              string
	Enabled          bool
	LastFetchAt      string
	LastFetchStatus  string
	LastFetchError   string
	LastAddedNodes   int
	LastRemovedNodes int
}

type Node struct {
	ID                   int64
	SourceSubscriptionID int64
	SourceKey            string
	Name                 string
	ProtocolType         string
	Server               string
	Port                 int
	PayloadJSON          string
	Enabled              bool
	Removed              bool
}

type NodeRuntimeStatus struct {
	NodeID              int64
	State               string
	Tier                string
	Score               float64
	LatencyMS           int
	RecentSuccessRate   float64
	ConsecutiveFailures int
	LastCheckAt         string
	LastSuccessAt       string
	LastFailureAt       string
	CooldownUntil       string
	ManualDisabled      bool
}

type NodeHealthDetail struct {
	ID                  int64   `json:"id"`
	Name                string  `json:"name"`
	ProtocolType        string  `json:"protocol_type"`
	Server              string  `json:"server"`
	Port                int     `json:"port"`
	State               string  `json:"state"`
	Tier                string  `json:"tier"`
	Score               float64 `json:"score"`
	LatencyMS           int     `json:"latency_ms"`
	RecentSuccessRate   float64 `json:"recent_success_rate"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	CooldownUntil       string  `json:"cooldown_until"`
	ManualDisabled      bool    `json:"manual_disabled"`
	IsActive            bool    `json:"is_active"`
}

type EventLog struct {
	ID            int64  `json:"id"`
	EventType     string `json:"event_type"`
	Level         string `json:"level"`
	Message       string `json:"message"`
	RelatedNodeID int64  `json:"related_node_id"`
	MetadataJSON  string `json:"metadata_json"`
	CreatedAt     string `json:"created_at"`
}

func RuntimeSummary(currentActiveNodeID int64, selectionMode string, restartRequired bool, healthyNodes int, totalNodes int, lastSwitchReason string, lastSwitchAt string) map[string]any {
	return map[string]any{
		"current_active_node_id": currentActiveNodeID,
		"selection_mode":         selectionMode,
		"restart_required":       restartRequired,
		"healthy_nodes":          healthyNodes,
		"total_nodes":            totalNodes,
		"last_switch_reason":     lastSwitchReason,
		"last_switch_at":         lastSwitchAt,
	}
}
