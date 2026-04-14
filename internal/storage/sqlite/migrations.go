package sqlite

var migrationStatements = []string{
	`CREATE TABLE IF NOT EXISTS subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		url TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		last_fetch_at TEXT NOT NULL DEFAULT '',
		last_fetch_status TEXT NOT NULL DEFAULT '',
		last_fetch_error TEXT NOT NULL DEFAULT '',
		last_added_nodes INTEGER NOT NULL DEFAULT 0,
		last_removed_nodes INTEGER NOT NULL DEFAULT 0
	);`,
	`CREATE TABLE IF NOT EXISTS nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		source_subscription_id INTEGER NOT NULL,
		source_key TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		protocol_type TEXT NOT NULL,
		server TEXT NOT NULL,
		port INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		removed INTEGER NOT NULL DEFAULT 0
	);`,
	`CREATE TABLE IF NOT EXISTS node_runtime_status (
		node_id INTEGER PRIMARY KEY,
		state TEXT NOT NULL,
		tier TEXT NOT NULL,
		score REAL NOT NULL,
		latency_ms INTEGER NOT NULL,
		recent_success_rate REAL NOT NULL,
		consecutive_failures INTEGER NOT NULL,
		last_check_at TEXT NOT NULL,
		last_success_at TEXT NOT NULL,
		last_failure_at TEXT NOT NULL,
		cooldown_until TEXT NOT NULL,
		manual_disabled INTEGER NOT NULL DEFAULT 0
	);`,
	`CREATE TABLE IF NOT EXISTS runtime_state (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		current_active_node_id INTEGER NOT NULL DEFAULT 0,
		selection_mode TEXT NOT NULL DEFAULT 'auto',
		runtime_mode TEXT NOT NULL DEFAULT 'single_active',
		pool_algorithm TEXT NOT NULL DEFAULT 'sequential',
		restart_required INTEGER NOT NULL DEFAULT 0,
		last_switch_reason TEXT NOT NULL DEFAULT '',
		last_switch_at TEXT NOT NULL DEFAULT ''
	);`,
	`CREATE TABLE IF NOT EXISTS port_runtime_state (
		port_key TEXT PRIMARY KEY,
		current_active_node_id INTEGER NOT NULL DEFAULT 0,
		selection_mode TEXT NOT NULL DEFAULT 'auto',
		runtime_mode TEXT NOT NULL DEFAULT 'single_active',
		pool_algorithm TEXT NOT NULL DEFAULT 'sequential',
		restart_required INTEGER NOT NULL DEFAULT 0,
		last_switch_reason TEXT NOT NULL DEFAULT '',
		last_switch_at TEXT NOT NULL DEFAULT ''
	);`,
	`CREATE TABLE IF NOT EXISTS port_node_state (
		port_key TEXT NOT NULL,
		node_id INTEGER NOT NULL,
		manual_disabled INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (port_key, node_id)
	);`,
	`CREATE TABLE IF NOT EXISTS request_lane_state (
		port_key TEXT NOT NULL,
		lane_key TEXT NOT NULL,
		protocol TEXT NOT NULL,
		assigned_node_id INTEGER NOT NULL DEFAULT 0,
		weight INTEGER NOT NULL DEFAULT 1,
		state TEXT NOT NULL DEFAULT 'idle',
		last_switch_reason TEXT NOT NULL DEFAULT '',
		last_switch_at TEXT NOT NULL DEFAULT '',
		last_used_at TEXT NOT NULL DEFAULT '',
		last_error_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (port_key, lane_key)
	);`,
	`CREATE TABLE IF NOT EXISTS event_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_type TEXT NOT NULL,
		level TEXT NOT NULL,
		message TEXT NOT NULL,
		related_node_id INTEGER NOT NULL DEFAULT 0,
		metadata_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`,
}
