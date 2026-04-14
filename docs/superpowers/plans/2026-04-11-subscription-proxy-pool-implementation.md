# Subscription Proxy Pool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个单用户、Docker 优先部署的订阅代理池服务，消费 Clash/V2Ray 常见订阅，对外稳定提供固定 HTTP 与 SOCKS5 代理入口，并通过管理员后台管理订阅、节点、切换与配置。

**Architecture:** 使用 Go 构建控制面，负责订阅拉取、解析、归一化、评分、分层、切换、Web UI 和 SQLite 持久化；使用 sing-box 作为数据面，承载所有上游节点与对外入口。优先通过 sing-box 的 selector/clash api 在不 reload 的情况下切换活动节点；当订阅变化导致节点集合变化时再生成配置并 reload。

**Tech Stack:** Go 1.22+, chi, html/template + 内嵌静态资源, modernc.org/sqlite, gopkg.in/yaml.v3, sing-box v1.12.x, Docker / Docker Compose

---

## File Structure

### Create
- `go.mod`
- `cmd/proxypools/main.go`
- `internal/app/app.go`
- `internal/model/model.go`
- `internal/config/config.go`
- `internal/storage/sqlite/db.go`
- `internal/storage/sqlite/migrations.go`
- `internal/storage/sqlite/repository.go`
- `internal/subscription/fetcher.go`
- `internal/subscription/service.go`
- `internal/parser/clash.go`
- `internal/parser/sharelink.go`
- `internal/parser/normalize.go`
- `internal/pool/scorer.go`
- `internal/pool/selector.go`
- `internal/pool/checker.go`
- `internal/pool/scheduler.go`
- `internal/runtime/builder.go`
- `internal/runtime/process.go`
- `internal/runtime/clashapi.go`
- `internal/web/router.go`
- `internal/web/auth.go`
- `internal/web/handlers.go`
- `internal/web/static/index.html`
- `internal/web/static/app.js`
- `internal/web/static/styles.css`
- `Dockerfile`
- `docker-compose.yml`
- `.env.example`
- `internal/config/config_test.go`
- `internal/storage/sqlite/db_test.go`
- `internal/parser/parser_test.go`
- `internal/pool/scorer_test.go`
- `internal/pool/selector_test.go`
- `internal/runtime/builder_test.go`
- `internal/runtime/clashapi_test.go`
- `internal/web/router_test.go`
- `tests/e2e/proxy_stack_test.go`

### Responsibility Notes
- `internal/model`：统一的数据结构，不放业务逻辑。
- `internal/config`：环境变量与运行参数，区分热生效和需重启项。
- `internal/storage/sqlite`：所有 SQLite 初始化与读写。
- `internal/subscription` + `internal/parser`：订阅抓取、解析、归一化。
- `internal/pool`：评分、分层、节点选择、健康检查与定时调度。
- `internal/runtime`：sing-box 配置生成、进程管理、clash api 切换。
- `internal/web`：后台路由、认证、中间件、页面和 JSON API。
- `internal/web/static`：最小管理员后台静态资源，直接供 `internal/web` 通过 `go:embed` 打包。

### Runtime Design Lock-ins
- 用户流量使用两个固定入口：`http-in`、`socks-in`。
- 健康检查使用一个单独的本地专用入口：`health-in`，避免探活流量影响用户入口。
- sing-box 配置中保留：
  - 每个节点一个 outbound
  - `active-http` selector
  - `active-socks` selector
  - `health-check` selector
- 日常切换优先调用 clash api 修改 selector，不 reload。
- 只有订阅变化、端口变化、认证结构变化等才触发 config rebuild + `sing-box check` + reload。

### Task 1: Bootstrap the Go service skeleton

**Files:**
- Create: `go.mod`
- Create: `cmd/proxypools/main.go`
- Create: `internal/app/app.go`
- Create: `internal/model/model.go`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Test: `internal/app/app_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package config_test

import (
	"testing"

	"proxypools/internal/config"
)

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected default config to be valid, got %v", err)
	}
	if cfg.HTTPListenPort != 7777 {
		t.Fatalf("expected default HTTP port 7777, got %d", cfg.HTTPListenPort)
	}
	if cfg.SOCKSListenPort != 7780 {
		t.Fatalf("expected default SOCKS port 7780, got %d", cfg.SOCKSListenPort)
	}
}
```

```go
package app_test

import (
	"testing"

	"proxypools/internal/app"
	"proxypools/internal/config"
)

func TestNewReturnsApp(t *testing.T) {
	cfg := config.Default()
	a, err := app.New(cfg)
	if err != nil {
		t.Fatalf("expected app.New to succeed, got %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil app")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go test ./internal/config ./internal/app -v
```

Expected: FAIL with errors like `package proxypools/internal/config is not in std` or `undefined: config.Default`.

- [ ] **Step 3: Write the minimal implementation**

```go
module proxypools

go 1.22

require github.com/go-chi/chi/v5 v5.2.1
```

```go
package model

type RuntimeState struct {
	CurrentActiveNodeID int64
	SelectionMode       string
	RestartRequired     bool
}
```

```go
package config

import "fmt"

type Config struct {
	AdminListenAddr             string
	AdminListenPort             int
	HTTPListenAddr              string
	HTTPListenPort              int
	SOCKSListenAddr             string
	SOCKSListenPort             int
	HealthListenAddr            string
	HealthListenPort            int
	DBPath                      string
	SingboxBinary               string
	SingboxConfigPath           string
	SubscriptionRefreshInterval int
	HealthCheckInterval         int
	AdminUsername               string
	AdminPasswordHash           string
}

func Default() Config {
	return Config{
		AdminListenAddr:             "0.0.0.0",
		AdminListenPort:             8080,
		HTTPListenAddr:              "0.0.0.0",
		HTTPListenPort:              7777,
		SOCKSListenAddr:             "0.0.0.0",
		SOCKSListenPort:             7780,
		HealthListenAddr:            "127.0.0.1",
		HealthListenPort:            19090,
		DBPath:                      "data/proxypools.db",
		SingboxBinary:               "sing-box",
		SingboxConfigPath:           "data/sing-box.json",
		SubscriptionRefreshInterval: 900,
		HealthCheckInterval:         60,
		AdminUsername:               "admin",
	}
}

func (c Config) Validate() error {
	if c.HTTPListenPort <= 0 || c.SOCKSListenPort <= 0 || c.AdminListenPort <= 0 {
		return fmt.Errorf("ports must be positive")
	}
	if c.DBPath == "" {
		return fmt.Errorf("db path is required")
	}
	if c.SingboxBinary == "" {
		return fmt.Errorf("sing-box binary is required")
	}
	return nil
}
```

```go
package app

import "proxypools/internal/config"

type App struct {
	Config config.Config
}

func New(cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &App{Config: cfg}, nil
}
```

```go
package main

import (
	"log"

	"proxypools/internal/app"
	"proxypools/internal/config"
)

func main() {
	cfg := config.Default()
	_, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("proxypools bootstrapped")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go test ./internal/config ./internal/app -v
```

Expected: PASS with both tests green.

- [ ] **Step 5: Commit**

```bash
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || git init
git add go.mod cmd/proxypools/main.go internal/app/app.go internal/model/model.go internal/config/config.go internal/config/config_test.go internal/app/app_test.go
git commit -m "chore: bootstrap proxypools service"
```

Expected: one commit created with the bootstrap skeleton.

### Task 2: Add SQLite schema and repository layer

**Files:**
- Modify: `go.mod`
- Create: `internal/storage/sqlite/db.go`
- Create: `internal/storage/sqlite/migrations.go`
- Create: `internal/storage/sqlite/repository.go`
- Modify: `internal/model/model.go`
- Test: `internal/storage/sqlite/db_test.go`

- [ ] **Step 1: Write the failing storage test**

```go
package sqlite_test

import (
	"context"
	"testing"

	"proxypools/internal/model"
	sqliteRepo "proxypools/internal/storage/sqlite"
)

func TestMigrateAndSaveSubscription(t *testing.T) {
	repo, err := sqliteRepo.New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("new repo failed: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	sub := model.Subscription{Name: "default", URL: "https://example.com/sub"}
	if err := repo.UpsertSubscription(context.Background(), &sub); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	got, err := repo.GetPrimarySubscription(context.Background())
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.URL != sub.URL {
		t.Fatalf("expected url %s, got %s", sub.URL, got.URL)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
go test ./internal/storage/sqlite -v
```

Expected: FAIL because `sqliteRepo.New`, `Migrate`, `UpsertSubscription`, and `GetPrimarySubscription` do not exist.

- [ ] **Step 3: Write the minimal implementation**

```go
require modernc.org/sqlite v1.34.5
```

```go
package model

type Subscription struct {
	ID              int64
	Name            string
	URL             string
	Enabled         bool
	LastFetchAt     string
	LastFetchStatus string
	LastFetchError  string
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
	NodeID               int64
	State                string
	Tier                 string
	Score                float64
	LatencyMS            int
	RecentSuccessRate    float64
	ConsecutiveFailures  int
	LastCheckAt          string
	LastSuccessAt        string
	LastFailureAt        string
	CooldownUntil        string
	ManualDisabled       bool
}
```

```go
package sqlite

import (
	"context"
	"database/sql"

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
	return &Repository{db: db}, nil
}

func (r *Repository) Migrate(ctx context.Context) error {
	for _, stmt := range migrationStatements {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
```

```go
package sqlite

var migrationStatements = []string{
	`CREATE TABLE IF NOT EXISTS subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		url TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		last_fetch_at TEXT NOT NULL DEFAULT '',
		last_fetch_status TEXT NOT NULL DEFAULT '',
		last_fetch_error TEXT NOT NULL DEFAULT ''
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
		restart_required INTEGER NOT NULL DEFAULT 0
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
```

```go
package sqlite

import (
	"context"
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
	row := r.db.QueryRowContext(ctx, `SELECT id, name, url, enabled, last_fetch_at, last_fetch_status, last_fetch_error FROM subscriptions ORDER BY id LIMIT 1`)
	var sub model.Subscription
	var enabled int
	if err := row.Scan(&sub.ID, &sub.Name, &sub.URL, &enabled, &sub.LastFetchAt, &sub.LastFetchStatus, &sub.LastFetchError); err != nil {
		return nil, err
	}
	sub.Enabled = enabled == 1
	return &sub, nil
}
```

- [ ] **Step 4: Run the storage tests**

Run:
```bash
go test ./internal/storage/sqlite -v
```

Expected: PASS with migration and upsert test green.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/model/model.go internal/storage/sqlite/db.go internal/storage/sqlite/migrations.go internal/storage/sqlite/repository.go internal/storage/sqlite/db_test.go
git commit -m "feat: add sqlite persistence layer"
```

### Task 3: Implement subscription fetch and parser normalization

**Files:**
- Modify: `go.mod`
- Create: `internal/subscription/fetcher.go`
- Create: `internal/subscription/service.go`
- Create: `internal/parser/clash.go`
- Create: `internal/parser/sharelink.go`
- Create: `internal/parser/normalize.go`
- Test: `internal/parser/parser_test.go`

- [ ] **Step 1: Write the failing parser tests**

```go
package parser_test

import (
	"testing"

	"proxypools/internal/parser"
)

func TestParseClashSubscription(t *testing.T) {
	input := []byte("proxies:\n  - name: hk-1\n    type: vmess\n    server: hk.example.com\n    port: 443\n    uuid: 11111111-1111-1111-1111-111111111111\n")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse clash failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Name != "hk-1" {
		t.Fatalf("expected node name hk-1, got %s", nodes[0].Name)
	}
}

func TestParseShareLinks(t *testing.T) {
	input := []byte("ss://YWVzLTI1Ni1nY206cGFzc0BleGFtcGxlLmNvbTo4Mzg4#jp-1\n")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse share link failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].ProtocolType != "shadowsocks" {
		t.Fatalf("expected shadowsocks, got %s", nodes[0].ProtocolType)
	}
}
```

- [ ] **Step 2: Run the parser tests to verify they fail**

Run:
```bash
go test ./internal/parser -v
```

Expected: FAIL because `ParseSubscription` does not exist.

- [ ] **Step 3: Write the minimal implementation**

```go
require gopkg.in/yaml.v3 v3.0.1
```

```go
package parser

import "proxypools/internal/model"

type RawNode struct {
	Name         string
	ProtocolType string
	Server       string
	Port         int
	PayloadJSON  string
	SourceKey    string
}

func ParseSubscription(input []byte) ([]model.Node, error) {
	if looksLikeClash(input) {
		return parseClash(input)
	}
	return parseShareLinks(input)
}
```

```go
package parser

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"

	"gopkg.in/yaml.v3"
	"proxypools/internal/model"
)

type clashFile struct {
	Proxies []map[string]any `yaml:"proxies"`
}

func parseClash(input []byte) ([]model.Node, error) {
	var file clashFile
	if err := yaml.Unmarshal(input, &file); err != nil {
		return nil, err
	}
	result := make([]model.Node, 0, len(file.Proxies))
	for _, proxy := range file.Proxies {
		payload, _ := json.Marshal(proxy)
		key := sha1.Sum(payload)
		result = append(result, model.Node{
			SourceKey:    hex.EncodeToString(key[:]),
			Name:         proxy["name"].(string),
			ProtocolType: proxy["type"].(string),
			Server:       proxy["server"].(string),
			Port:         proxy["port"].(int),
			PayloadJSON:  string(payload),
			Enabled:      true,
		})
	}
	return result, nil
}

func looksLikeClash(input []byte) bool {
	return len(input) >= 8 && string(input[:8]) == "proxies:"
}
```

```go
package parser

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"proxypools/internal/model"
)

func parseShareLinks(input []byte) ([]model.Node, error) {
	lines := strings.Split(strings.TrimSpace(string(input)), "\n")
	result := make([]model.Node, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "ss://") {
			node, err := parseSS(line)
			if err != nil {
				return nil, err
			}
			result = append(result, node)
		}
	}
	return result, nil
}

func parseSS(line string) (model.Node, error) {
	raw := strings.TrimPrefix(line, "ss://")
	parts := strings.SplitN(raw, "#", 2)
	decoded, err := base64.RawStdEncoding.DecodeString(strings.SplitN(parts[0], "@", 2)[0])
	if err != nil {
		return model.Node{}, err
	}
	serverPart := strings.SplitN(parts[0], "@", 2)[1]
	serverBits := strings.Split(serverPart, ":")
	port, err := strconv.Atoi(serverBits[1])
	if err != nil {
		return model.Node{}, err
	}
	name, _ := url.QueryUnescape(parts[1])
	payload := map[string]string{
		"cipher_and_password": string(decoded),
		"server":              serverBits[0],
		"port":                serverBits[1],
	}
	payloadJSON, _ := json.Marshal(payload)
	key := sha1.Sum([]byte(line))
	return model.Node{
		SourceKey:    hex.EncodeToString(key[:]),
		Name:         name,
		ProtocolType: "shadowsocks",
		Server:       serverBits[0],
		Port:         port,
		PayloadJSON:  string(payloadJSON),
		Enabled:      true,
	}, nil
}
```

```go
package subscription

import (
	"context"
	"io"
	"net/http"
	"time"
)

type Fetcher struct {
	Client *http.Client
}

func NewFetcher() *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: 15 * time.Second}}
}

func (f *Fetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
```

- [ ] **Step 4: Run parser tests**

Run:
```bash
go test ./internal/parser -v
```

Expected: PASS for both clash and share-link parsing tests.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/subscription/fetcher.go internal/subscription/service.go internal/parser/clash.go internal/parser/sharelink.go internal/parser/normalize.go internal/parser/parser_test.go
git commit -m "feat: add subscription fetch and parser normalization"
```

### Task 4: Generate sing-box configuration and runtime controller

**Files:**
- Create: `internal/runtime/builder.go`
- Create: `internal/runtime/process.go`
- Create: `internal/runtime/clashapi.go`
- Test: `internal/runtime/builder_test.go`
- Test: `internal/runtime/clashapi_test.go`

- [ ] **Step 1: Write the failing runtime tests**

```go
package runtime_test

import (
	"strings"
	"testing"

	"proxypools/internal/model"
	"proxypools/internal/runtime"
)

func TestBuildConfigIncludesFixedInboundsAndSelectors(t *testing.T) {
	nodes := []model.Node{{ID: 1, Name: "hk-1", ProtocolType: "vmess", Server: "hk.example.com", Port: 443, PayloadJSON: `{"uuid":"11111111-1111-1111-1111-111111111111","type":"vmess","server":"hk.example.com","port":443}`}}
	cfg, err := runtime.BuildConfig(runtime.BuildInput{HTTPPort: 7777, SOCKSPort: 7780, HealthPort: 19090, Nodes: nodes, ActiveNodeID: 1})
	if err != nil {
		t.Fatalf("build config failed: %v", err)
	}
	if !strings.Contains(cfg, `"tag":"http-in"`) {
		t.Fatal("expected http-in inbound")
	}
	if !strings.Contains(cfg, `"tag":"health-check"`) {
		t.Fatal("expected health-check selector")
	}
}
```

```go
package runtime_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"proxypools/internal/runtime"
)

func TestSwitchSelectorCallsClashAPI(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/proxies/active-http" {
			called = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	client := runtime.NewClashAPI(ts.URL, "")
	if err := client.SwitchSelector("active-http", "node-1"); err != nil {
		t.Fatalf("switch failed: %v", err)
	}
	if !called {
		t.Fatal("expected selector switch request")
	}
}
```

- [ ] **Step 2: Run the runtime tests to verify they fail**

Run:
```bash
go test ./internal/runtime -v
```

Expected: FAIL because `BuildConfig`, `BuildInput`, and `NewClashAPI` do not exist.

- [ ] **Step 3: Write the minimal implementation**

```go
package runtime

import (
	"encoding/json"
	"fmt"

	"proxypools/internal/model"
)

type BuildInput struct {
	HTTPPort     int
	SOCKSPort    int
	HealthPort   int
	Nodes        []model.Node
	ActiveNodeID int64
}

func BuildConfig(in BuildInput) (string, error) {
	outbounds := make([]map[string]any, 0, len(in.Nodes)+4)
	for _, node := range in.Nodes {
		var payload map[string]any
		if err := json.Unmarshal([]byte(node.PayloadJSON), &payload); err != nil {
			return "", err
		}
		payload["tag"] = fmt.Sprintf("node-%d", node.ID)
		outbounds = append(outbounds, payload)
	}
	selectorTags := []string{}
	for _, node := range in.Nodes {
		selectorTags = append(selectorTags, fmt.Sprintf("node-%d", node.ID))
	}
	root := map[string]any{
		"log": map[string]any{"level": "info", "timestamp": true},
		"inbounds": []map[string]any{
			{"type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": in.HTTPPort},
			{"type": "socks", "tag": "socks-in", "listen": "0.0.0.0", "listen_port": in.SOCKSPort},
			{"type": "http", "tag": "health-in", "listen": "127.0.0.1", "listen_port": in.HealthPort},
		},
		"outbounds": append(outbounds,
			map[string]any{"type": "selector", "tag": "active-http", "outbounds": selectorTags, "default": fmt.Sprintf("node-%d", in.ActiveNodeID), "interrupt_exist_connections": false},
			map[string]any{"type": "selector", "tag": "active-socks", "outbounds": selectorTags, "default": fmt.Sprintf("node-%d", in.ActiveNodeID), "interrupt_exist_connections": false},
			map[string]any{"type": "selector", "tag": "health-check", "outbounds": selectorTags, "default": fmt.Sprintf("node-%d", in.ActiveNodeID), "interrupt_exist_connections": false},
			map[string]any{"type": "direct", "tag": "direct"},
		),
		"route": map[string]any{
			"rules": []map[string]any{
				{"inbound": []string{"http-in"}, "outbound": "active-http"},
				{"inbound": []string{"socks-in"}, "outbound": "active-socks"},
				{"inbound": []string{"health-in"}, "outbound": "health-check"},
			},
			"final": "direct",
		},
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller": "127.0.0.1:9090",
				"secret": "",
			},
		},
	}
	buf, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}
```

```go
package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
)

type ClashAPI struct {
	baseURL string
	secret  string
	client  *http.Client
}

func NewClashAPI(baseURL, secret string) *ClashAPI {
	return &ClashAPI{baseURL: baseURL, secret: secret, client: &http.Client{}}
}

func (c *ClashAPI) SwitchSelector(group, name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequest(http.MethodPut, c.baseURL+"/proxies/"+group, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return err
	}
	return nil
}
```

```go
package runtime

import (
	"context"
	"os"
	"os/exec"
)

type Process struct {
	Binary string
	Config string
}

func (p Process) Check(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, p.Binary, "check", "-c", p.Config)
	return cmd.Run()
}

func (p Process) WriteConfig(content string) error {
	return os.WriteFile(p.Config, []byte(content), 0o644)
}
```

- [ ] **Step 4: Run runtime tests**

Run:
```bash
go test ./internal/runtime -v
```

Expected: PASS for config builder and clash api tests.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/builder.go internal/runtime/process.go internal/runtime/clashapi.go internal/runtime/builder_test.go internal/runtime/clashapi_test.go
git commit -m "feat: add sing-box runtime integration"
```

### Task 5: Implement scoring, tiering, and node selector

**Files:**
- Create: `internal/pool/scorer.go`
- Create: `internal/pool/selector.go`
- Test: `internal/pool/scorer_test.go`
- Test: `internal/pool/selector_test.go`
- Modify: `internal/model/model.go`

- [ ] **Step 1: Write the failing score and selector tests**

```go
package pool_test

import (
	"testing"

	"proxypools/internal/model"
	"proxypools/internal/pool"
)

func TestScoreMapsHealthyNodeToL1(t *testing.T) {
	status := model.NodeRuntimeStatus{LatencyMS: 200, RecentSuccessRate: 1.0, ConsecutiveFailures: 0}
	scored := pool.Score(status)
	if scored.Tier != "L1" {
		t.Fatalf("expected L1, got %s", scored.Tier)
	}
}

func TestSelectNextPrefersSameTier(t *testing.T) {
	statuses := []model.NodeRuntimeStatus{
		{NodeID: 1, State: "cooldown", Tier: "L1", Score: 90},
		{NodeID: 2, State: "active", Tier: "L1", Score: 88},
		{NodeID: 3, State: "active", Tier: "L2", Score: 80},
	}
	next, ok := pool.SelectNext(1, statuses)
	if !ok {
		t.Fatal("expected a replacement node")
	}
	if next.NodeID != 2 {
		t.Fatalf("expected node 2, got %d", next.NodeID)
	}
}
```

- [ ] **Step 2: Run the pool tests to verify they fail**

Run:
```bash
go test ./internal/pool -run 'TestScoreMapsHealthyNodeToL1|TestSelectNextPrefersSameTier' -v
```

Expected: FAIL because `Score` and `SelectNext` do not exist.

- [ ] **Step 3: Write the minimal implementation**

```go
package pool

import "proxypools/internal/model"

type ScoredStatus struct {
	model.NodeRuntimeStatus
}

func Score(in model.NodeRuntimeStatus) ScoredStatus {
	score := 100.0
	score -= float64(in.LatencyMS) / 20
	score += in.RecentSuccessRate * 20
	score -= float64(in.ConsecutiveFailures * 15)
	in.Score = score
	switch {
	case score >= 90:
		in.Tier = "L1"
	case score >= 70:
		in.Tier = "L2"
	default:
		in.Tier = "L3"
	}
	if in.ConsecutiveFailures >= 3 {
		in.State = "cooldown"
	} else if in.State == "" {
		in.State = "active"
	}
	return ScoredStatus{NodeRuntimeStatus: in}
}
```

```go
package pool

import (
	"sort"

	"proxypools/internal/model"
)

func SelectNext(currentNodeID int64, statuses []model.NodeRuntimeStatus) (model.NodeRuntimeStatus, bool) {
	byTier := map[string][]model.NodeRuntimeStatus{"L1": {}, "L2": {}, "L3": {}}
	currentTier := "L3"
	for _, status := range statuses {
		if status.NodeID == currentNodeID {
			currentTier = status.Tier
		}
		if status.State == "active" && !status.ManualDisabled {
			byTier[status.Tier] = append(byTier[status.Tier], status)
		}
	}
	order := []string{currentTier, "L1", "L2", "L3"}
	seen := map[string]bool{}
	for _, tier := range order {
		if seen[tier] {
			continue
		}
		seen[tier] = true
		candidates := byTier[tier]
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
		for _, candidate := range candidates {
			if candidate.NodeID != currentNodeID {
				return candidate, true
			}
		}
	}
	return model.NodeRuntimeStatus{}, false
}
```

- [ ] **Step 4: Run the pool tests**

Run:
```bash
go test ./internal/pool -run 'TestScoreMapsHealthyNodeToL1|TestSelectNextPrefersSameTier' -v
```

Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/pool/scorer.go internal/pool/selector.go internal/pool/scorer_test.go internal/pool/selector_test.go internal/model/model.go
git commit -m "feat: add node scoring and selection"
```

### Task 6: Implement health checker and background scheduler

**Files:**
- Create: `internal/pool/checker.go`
- Create: `internal/pool/scheduler.go`
- Modify: `internal/runtime/clashapi.go`
- Test: `internal/pool/checker_test.go`

- [ ] **Step 1: Write the failing checker test**

```go
package pool_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"proxypools/internal/model"
	"proxypools/internal/pool"
)

type fakeSwitcher struct {
	group string
	name  string
}

func (f *fakeSwitcher) SwitchSelector(group, name string) error {
	f.group = group
	f.name = name
	return nil
}

func TestProbeNodeMeasuresLatencyAndSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	switcher := &fakeSwitcher{}
	checker := pool.NewChecker(pool.CheckerConfig{ProbeURL: ts.URL, SelectorSwitcher: switcher})
	status, err := checker.ProbeNode("node-1", model.NodeRuntimeStatus{NodeID: 1, State: "active"})
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if switcher.group != "health-check" || switcher.name != "node-1" {
		t.Fatalf("expected health-check selector switch to node-1, got %s/%s", switcher.group, switcher.name)
	}
	if status.LatencyMS <= 0 {
		t.Fatalf("expected positive latency, got %d", status.LatencyMS)
	}
	if status.RecentSuccessRate != 1 {
		t.Fatalf("expected success rate 1, got %v", status.RecentSuccessRate)
	}
}
```

- [ ] **Step 2: Run the checker test to verify it fails**

Run:
```bash
go test ./internal/pool -run TestProbeNodeMeasuresLatencyAndSuccess -v
```

Expected: FAIL because `NewChecker` and `ProbeNode` do not exist, and the checker is expected to probe through the dedicated `health-in` path.

- [ ] **Step 3: Write the minimal implementation**

```go
package pool

import (
	"net/http"
	"net/url"
	"time"

	"proxypools/internal/model"
)

type SelectorSwitcher interface {
	SwitchSelector(group, name string) error
}

type CheckerConfig struct {
	ProbeURL        string
	HealthProxyURL  string
	SelectorSwitcher SelectorSwitcher
}

type Checker struct {
	client    *http.Client
	probeURL  string
	switcher  SelectorSwitcher
}

func NewChecker(cfg CheckerConfig) *Checker {
	transport := &http.Transport{}
	if cfg.HealthProxyURL != "" {
		proxyURL, _ := url.Parse(cfg.HealthProxyURL)
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &Checker{
		client:   &http.Client{Timeout: 10 * time.Second, Transport: transport},
		probeURL: cfg.ProbeURL,
		switcher: cfg.SelectorSwitcher,
	}
}

func (c *Checker) ProbeNode(outboundTag string, in model.NodeRuntimeStatus) (model.NodeRuntimeStatus, error) {
	if c.switcher != nil {
		if err := c.switcher.SwitchSelector("health-check", outboundTag); err != nil {
			in.ConsecutiveFailures++
			in.State = "cooldown"
			return in, err
		}
	}
	started := time.Now()
	resp, err := c.client.Get(c.probeURL)
	if err != nil {
		in.ConsecutiveFailures++
		in.State = "cooldown"
		return in, err
	}
	defer resp.Body.Close()
	in.LatencyMS = int(time.Since(started).Milliseconds())
	in.RecentSuccessRate = 1
	in.ConsecutiveFailures = 0
	in.State = "active"
	return in, nil
}
```

```go
package pool

import (
	"context"
	"time"
)

type Scheduler struct {
	SubscriptionEvery time.Duration
	HealthEvery       time.Duration
}

func (s Scheduler) Start(ctx context.Context, refresh func(context.Context), check func(context.Context)) {
	refreshTicker := time.NewTicker(s.SubscriptionEvery)
	healthTicker := time.NewTicker(s.HealthEvery)
	go func() {
		defer refreshTicker.Stop()
		defer healthTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-refreshTicker.C:
				refresh(ctx)
			case <-healthTicker.C:
				check(ctx)
			}
		}
	}()
}
```

- [ ] **Step 4: Run pool tests**

Run:
```bash
go test ./internal/pool -v
```

Expected: PASS including scorer, selector, and checker tests.

- [ ] **Step 5: Commit**

```bash
git add internal/pool/checker.go internal/pool/scheduler.go internal/pool/checker_test.go internal/runtime/clashapi.go
git commit -m "feat: add health checker and scheduler"
```

### Task 7: Build the admin web UI and authenticated API

**Files:**
- Create: `internal/web/router.go`
- Create: `internal/web/auth.go`
- Create: `internal/web/handlers.go`
- Create: `internal/web/static/index.html`
- Create: `internal/web/static/app.js`
- Create: `internal/web/static/styles.css`
- Test: `internal/web/router_test.go`

- [ ] **Step 1: Write the failing router/auth tests**

```go
package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"proxypools/internal/web"
)

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
	r := web.NewRouter(web.Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run the web tests to verify they fail**

Run:
```bash
go test ./internal/web -v
```

Expected: FAIL because `NewRouter` and `Dependencies` do not exist.

- [ ] **Step 3: Write the minimal implementation**

```go
package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

type AuthConfig struct {
	Username     string
	PasswordHash string
}

func HashPassword(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func BasicAuth(cfg AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(cfg.Username)) != 1 || subtle.ConstantTimeCompare([]byte(HashPassword(pass)), []byte(cfg.PasswordHash)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="admin"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

```go
package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed static/*
var staticFS embed.FS

type Dependencies struct {
	AdminUsername     string
	AdminPasswordHash string
}

func NewRouter(dep Dependencies) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	staticFiles, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(staticFiles, "index.html")
		if err != nil {
			http.Error(w, "index not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	})
	r.Group(func(private chi.Router) {
		private.Use(BasicAuth(AuthConfig{Username: dep.AdminUsername, PasswordHash: dep.AdminPasswordHash}))
		private.Get("/api/runtime", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"mode": "auto", "restart_required": false})
		})
	})
	return r
}
```

```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <title>ProxyPools Admin</title>
    <link rel="stylesheet" href="/static/styles.css" />
  </head>
  <body>
    <main>
      <h1>ProxyPools Admin</h1>
      <section id="runtime"></section>
    </main>
    <script src="/static/app.js"></script>
  </body>
</html>
```

```javascript
async function loadRuntime() {
  const response = await fetch('/api/runtime', { headers: { Accept: 'application/json' } });
  const data = await response.json();
  document.getElementById('runtime').textContent = `mode=${data.mode} restart_required=${data.restart_required}`;
}
loadRuntime();
```

```css
body {
  font-family: system-ui, sans-serif;
  margin: 0;
  padding: 24px;
  background: #0b1020;
  color: #f4f7fb;
}
main {
  max-width: 960px;
  margin: 0 auto;
}
```

- [ ] **Step 4: Run the web tests**

Run:
```bash
go test ./internal/web -v
```

Expected: PASS with `/healthz` public and `/api/runtime` protected.

- [ ] **Step 5: Commit**

```bash
git add internal/web/router.go internal/web/auth.go internal/web/handlers.go internal/web/router_test.go internal/web/static/index.html internal/web/static/app.js internal/web/static/styles.css
git commit -m "feat: add authenticated admin web ui"
```

### Task 8: Wire the full application, Docker stack, and end-to-end test

**Files:**
- Modify: `cmd/proxypools/main.go`
- Modify: `internal/app/app.go`
- Create: `Dockerfile`
- Create: `docker-compose.yml`
- Create: `.env.example`
- Test: `tests/e2e/proxy_stack_test.go`

- [ ] **Step 1: Write the failing end-to-end smoke test**

```go
package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"proxypools/internal/config"
	"proxypools/internal/web"
)

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
	_ = config.Default()
}
```

- [ ] **Step 2: Run the end-to-end smoke test to verify it fails**

Run:
```bash
go test ./tests/e2e -v
```

Expected: FAIL until the application wiring and module imports are correct.

- [ ] **Step 3: Write the minimal implementation**

```go
package app

import (
	"context"
	"net/http"

	"proxypools/internal/config"
	sqliteRepo "proxypools/internal/storage/sqlite"
	"proxypools/internal/subscription"
	"proxypools/internal/web"
)

type App struct {
	Config config.Config
	Server *http.Server
}

func New(cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	repo, err := sqliteRepo.New(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := repo.Migrate(context.Background()); err != nil {
		return nil, err
	}
	_ = subscription.NewFetcher()
	handler := web.NewRouter(web.Dependencies{AdminUsername: cfg.AdminUsername, AdminPasswordHash: cfg.AdminPasswordHash})
	server := &http.Server{Addr: cfg.AdminListenAddr + ":8080", Handler: handler}
	return &App{Config: cfg, Server: server}, nil
}
```

```go
package main

import (
	"log"

	"proxypools/internal/app"
	"proxypools/internal/config"
)

func main() {
	cfg := config.Default()
	a, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("proxypools admin listening on %s", a.Server.Addr)
	if err := a.Server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
```

```dockerfile
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/proxypools ./cmd/proxypools

FROM ghcr.io/sagernet/sing-box:v1.12.12
WORKDIR /app
COPY --from=build /out/proxypools /usr/local/bin/proxypools
COPY .env.example /app/.env.example
EXPOSE 8080 7777 7780
CMD ["proxypools"]
```

```yaml
services:
  proxypools:
    build: .
    ports:
      - "8080:8080"
      - "7777:7777"
      - "7780:7780"
    volumes:
      - ./data:/app/data
    env_file:
      - .env
```

```bash
ADMIN_USERNAME=admin
ADMIN_PASSWORD_HASH=8c6976e5b5410415bde908bd4dee15dfb16f1a3e0c8f8f0f8f3f94a639a6a39d
HTTP_LISTEN_PORT=7777
SOCKS_LISTEN_PORT=7780
ADMIN_LISTEN_PORT=8080
SINGBOX_BINARY=sing-box
DB_PATH=data/proxypools.db
```

- [ ] **Step 4: Run the full test suite and Docker smoke build**

Run:
```bash
go test ./... -v
docker compose config
```

Expected: `go test` PASS and `docker compose config` renders without errors.

- [ ] **Step 5: Commit**

```bash
git add cmd/proxypools/main.go internal/app/app.go Dockerfile docker-compose.yml .env.example tests/e2e/proxy_stack_test.go
git commit -m "feat: wire proxypools app and docker stack"
```

## Final Verification Checklist

- [ ] `go test ./... -v`
- [ ] `sing-box check -c data/sing-box.json`
- [ ] `docker compose up --build`
- [ ] Open `http://127.0.0.1:8080/healthz` and verify `200 OK`
- [ ] Verify `/api/runtime` returns `401` without credentials
- [ ] Verify `/api/runtime` returns `200` with admin credentials
- [ ] Add one test subscription and confirm nodes are parsed into SQLite
- [ ] Force one node into cooldown and verify selector picks same-tier replacement first
- [ ] Change a hot-reloadable setting and verify it applies without restart
- [ ] Change a restart-only setting and verify UI marks `restart_required=true`

## Spec Coverage Check

- 单用户、单主订阅：Task 2, Task 3, Task 7
- Docker 部署：Task 8
- HTTP + SOCKS5 固定入口：Task 4, Task 8
- 自动订阅更新：Task 3, Task 6
- 健康检查、评分、分层、自动切换：Task 5, Task 6
- 管理后台与认证：Task 7
- 尽量热更新、必要时重启：Task 4, Task 8
- SQLite 持久化与事件日志：Task 2

## Notes for Execution

- 优先先把测试写出来再写实现，保持每个 task 都能独立通过测试。
- 切换活动节点时先走 clash api selector 切换；只有节点集合变化时才重建配置并 reload。
- 健康检查必须通过 `health-in` 本地入口做探测，不要复用用户入口，也不要通过切换用户 selector 做探测。
- 如果仓库还没有 git，Task 1 的 commit step 会先执行 `git init`。
