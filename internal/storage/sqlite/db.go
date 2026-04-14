package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"proxypools/internal/config"
	"proxypools/internal/model"

	_ "modernc.org/sqlite"
)

type Repository struct {
	db *sql.DB
}

func New(dsn string) (*Repository, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)

	return &Repository{db: db}, nil
}

func (r *Repository) Migrate(ctx context.Context) error {
	for _, stmt := range migrationStatements {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	alterStatements := []string{
		`ALTER TABLE runtime_state ADD COLUMN last_switch_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE runtime_state ADD COLUMN last_switch_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE runtime_state ADD COLUMN runtime_mode TEXT NOT NULL DEFAULT 'single_active'`,
		`ALTER TABLE runtime_state ADD COLUMN pool_algorithm TEXT NOT NULL DEFAULT 'sequential'`,
		`ALTER TABLE subscriptions ADD COLUMN last_added_nodes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE subscriptions ADD COLUMN last_removed_nodes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE request_lane_state ADD COLUMN weight INTEGER NOT NULL DEFAULT 1`,
	}
	for _, stmt := range alterStatements {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
				return err
			}
		}
	}
	if _, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO runtime_state(id, current_active_node_id, selection_mode, runtime_mode, pool_algorithm, restart_required, last_switch_reason, last_switch_at) VALUES (1, 0, 'auto', 'single_active', 'sequential', 0, '', '')`); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO port_runtime_state(port_key, current_active_node_id, selection_mode, runtime_mode, pool_algorithm, restart_required, last_switch_reason, last_switch_at)
		SELECT ?, current_active_node_id, selection_mode, runtime_mode, pool_algorithm, restart_required, last_switch_reason, last_switch_at
		FROM runtime_state WHERE id = 1
	`, config.DefaultPortKey); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO port_node_state(port_key, node_id, manual_disabled)
		SELECT ?, node_id, manual_disabled FROM node_runtime_status
	`, config.DefaultPortKey); err != nil {
		return err
	}
	return nil
}

func (r *Repository) ListNodes(ctx context.Context) ([]model.Node, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, source_subscription_id, source_key, name, protocol_type, server, port, payload_json, enabled, removed FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []model.Node
	for rows.Next() {
		var node model.Node
		var enabled, removed int
		if err := rows.Scan(&node.ID, &node.SourceSubscriptionID, &node.SourceKey, &node.Name, &node.ProtocolType, &node.Server, &node.Port, &node.PayloadJSON, &enabled, &removed); err != nil {
			return nil, err
		}
		node.Enabled = enabled == 1
		node.Removed = removed == 1
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}
