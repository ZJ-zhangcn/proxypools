# proxypools

`proxypools` 是一个基于 Go 的自托管代理池控制面，负责订阅拉取、节点解析、健康检查、自动切换、统一入口调度和 Web 管理；实际的代理数据面由 `sing-box` 承载。

它的目标不是实现一套新的代理协议，而是把“节点池管理”和“稳定对外代理入口”工程化：

- 上游消费订阅节点
- 下游对外暴露固定 HTTP / SOCKS5 入口
- 控制面持续维护可用节点、切换策略和运行状态
- 管理员通过 Web 界面和 JSON API 观察与控制整个系统

> 当前状态：核心功能已完成，测试可通过，适合作为单机自托管版本使用与继续演进。

---

## 核心能力

- 订阅拉取与刷新
- Clash / 常见分享链接节点解析与归一化
- SQLite 持久化运行态、节点状态和事件日志
- 节点健康检查与评分
- 自动切换与手动锁定切换
- multi-port 入口模型
- lane 级数据面分配与状态跟踪
- dispatcher 统一 HTTP / SOCKS5 入口
- lane 权重调度
- sticky lane 调度
- Host / Header 请求级分流
- 目标 lane 失败后的 fallback
- Basic Auth 保护的中文 Web 管理面
- JSON API 管理接口

---

## 架构概览

```text
                +---------------------------+
                |       Web UI / API        |
                |   chi router + BasicAuth  |
                +-------------+-------------+
                              |
                              v
+---------------------------------------------------------------+
|                    proxypools (Go control plane)              |
|                                                               |
|  Subscription  Runtime State  Dispatcher  Health Check        |
|  Refresh       + Port/Lane     Selector    + Reconcile        |
|                Runtime          (seq/random/balance/sticky)   |
|                                                               |
|           SQLite Repository (runtime/event/subscription)      |
+----------------------------+----------------------------------+
                             |
                             | build sing-box config
                             v
                   +----------------------+
                   |       sing-box       |
                   | HTTP/SOCKS inbounds  |
                   |  outbound selectors  |
                   +----------------------+
```

### 组件分工

- **控制面（Go）**
  - 拉取订阅
  - 维护节点注册表和运行状态
  - 做健康检查、评分和自动切换
  - 生成 sing-box 配置并驱动运行时
  - 暴露管理页面和 API

- **数据面（sing-box）**
  - 承载上游节点出站
  - 暴露固定 HTTP / SOCKS5 入口
  - 响应控制面的 selector / 配置更新

- **存储层（SQLite）**
  - 订阅信息
  - 节点数据
  - 运行时状态
  - port / lane 状态
  - 事件日志

---

## 快速开始

## 方式一：Docker Compose（推荐）

项目自带 `Dockerfile` 和 `docker-compose.yml`。

### 1）准备配置

仓库内提供了 `.env.example`，默认 `docker-compose.yml` 会直接加载它：

```yaml
env_file:
  - .env.example
```

你至少需要检查并修改这些值：

- `ADMIN_USERNAME`
- `ADMIN_PASSWORD_HASH`
- `SUBSCRIPTION_URL`
- `SINGBOX_BINARY`（Docker 镜像内默认已提供）
- `DB_PATH`

建议不要直接把生产配置长期保存在 `.env.example`；更稳妥的做法是复制出你自己的 env 文件，并把 `docker-compose.yml` 里的 `env_file` 改成私有文件名。

### 2）启动

```bash
docker compose up --build
```

### 3）访问

默认端口：

- 管理面：`http://127.0.0.1:8080`
- 默认 HTTP 代理：`127.0.0.1:7777`
- 默认 SOCKS5 代理：`127.0.0.1:7780`

健康检查：

```bash
curl http://127.0.0.1:8080/healthz
```

期望返回：

```text
ok
```

---

## 方式二：本地开发运行

### 前置条件

- Go 1.22+
- 本机可执行 `sing-box`
- 或通过 `SINGBOX_BINARY` 指定 `sing-box` 路径

### 启动

```bash
go run ./cmd/proxypools
```

如果本机缺少 `sing-box`，启动会失败，这是预期行为。

---

## 配置说明

配置入口在 `internal/config/config.go`，默认值与环境变量覆盖逻辑都定义在这里。

## 常用环境变量

| 变量 | 说明 | 默认值 |
|---|---|---|
| `ADMIN_USERNAME` | 管理面用户名 | `admin` |
| `ADMIN_PASSWORD_HASH` | 管理面密码哈希（SHA256 十六进制） | 无，必须设置 |
| `SUBSCRIPTION_URL` | 订阅地址 | 空 |
| `ADMIN_LISTEN_ADDR` | 管理面监听地址 | `0.0.0.0` |
| `ADMIN_LISTEN_PORT` | 管理面端口 | `8080` |
| `HTTP_LISTEN_ADDR` | 默认 HTTP 代理监听地址 | `0.0.0.0` |
| `HTTP_LISTEN_PORT` | 默认 HTTP 代理端口 | `7777` |
| `SOCKS_LISTEN_ADDR` | 默认 SOCKS5 监听地址 | `0.0.0.0` |
| `SOCKS_LISTEN_PORT` | 默认 SOCKS5 端口 | `7780` |
| `HEALTH_LISTEN_ADDR` | 本地健康检查监听地址 | `127.0.0.1` |
| `HEALTH_LISTEN_PORT` | 本地健康检查端口 | `19090` |
| `SINGBOX_BINARY` | `sing-box` 可执行文件路径 | `sing-box` |
| `SINGBOX_CONFIG_PATH` | 生成的 sing-box 配置路径 | `data/sing-box.json` |
| `DB_PATH` | SQLite 路径 | `data/proxypools.db` |
| `SUBSCRIPTION_REFRESH_INTERVAL` | 订阅刷新周期（秒） | `900` |
| `HEALTH_CHECK_INTERVAL` | 健康检查周期（秒） | `60` |
| `RUNTIME_MODE` | 运行模式 | `single_active` |
| `POOL_ALGORITHM` | port 内节点算法 | `sequential` |
| `DISPATCHER_ENABLED` | 是否启用 dispatcher | `false` |
| `DISPATCHER_HTTP_LISTEN_ADDR` | dispatcher HTTP 监听地址 | `0.0.0.0` |
| `DISPATCHER_HTTP_LISTEN_PORT` | dispatcher HTTP 端口 | `7777` |
| `DISPATCHER_SOCKS_LISTEN_ADDR` | dispatcher SOCKS 监听地址 | `0.0.0.0` |
| `DISPATCHER_SOCKS_LISTEN_PORT` | dispatcher SOCKS 端口 | `7780` |
| `DISPATCHER_ALGORITHM` | dispatcher lane 算法 | `sequential` |
| `PORTS_JSON` | 多端口 / lane 编排 JSON | 空 |

### 运行模式与算法

- `RUNTIME_MODE`
  - `single_active`
  - `pool`

- `POOL_ALGORITHM`
  - `sequential`
  - `random`
  - `balance`

- `DISPATCHER_ALGORITHM`
  - `sequential`
  - `random`
  - `balance`

> 注意：默认 dispatcher 端口与默认代理端口相同；如果开启 dispatcher，必须把 dispatcher 监听端口改到不冲突的值，否则配置校验会直接失败。

### 生成管理面密码哈希

macOS / Linux 可用：

```bash
printf 'your-password' | shasum -a 256 | awk '{print $1}'
```

把输出写到 `ADMIN_PASSWORD_HASH`。

---

## `PORTS_JSON` 示例

如果不提供 `PORTS_JSON`，系统会自动生成一个 `default` port，并自动合成基础 lanes。

如果你需要显式定义多 port / lane，可使用：

```json
[
  {
    "key": "default",
    "name": "默认入口",
    "http_listen_addr": "0.0.0.0",
    "http_listen_port": 7777,
    "socks_listen_addr": "0.0.0.0",
    "socks_listen_port": 7780,
    "runtime_mode": "pool",
    "pool_algorithm": "balance",
    "lanes": [
      {
        "key": "lane-http-1",
        "protocol": "http",
        "listen_addr": "127.0.0.1",
        "listen_port": 8778,
        "weight": 3
      },
      {
        "key": "lane-http-2",
        "protocol": "http",
        "listen_addr": "127.0.0.1",
        "listen_port": 8779,
        "weight": 1
      },
      {
        "key": "lane-socks-1",
        "protocol": "socks",
        "listen_addr": "127.0.0.1",
        "listen_port": 8781,
        "weight": 1
      }
    ]
  },
  {
    "key": "canary",
    "name": "灰度入口",
    "http_listen_addr": "0.0.0.0",
    "http_listen_port": 8877,
    "socks_listen_addr": "0.0.0.0",
    "socks_listen_port": 8880,
    "runtime_mode": "pool",
    "pool_algorithm": "random"
  }
]
```

校验会检查：

- port key 唯一性
- lane key / 协议组合唯一性
- 监听地址和端口冲突
- runtime / algorithm 取值合法性
- `default` port 必须存在

---

## Dispatcher / lane / 请求级分流

这是当前项目最重要的差异化能力之一。

### Dispatcher 统一入口

启用 dispatcher 后，请求可以先进入统一入口，再由 dispatcher 选择具体 lane：

- HTTP 统一入口
- SOCKS5 统一入口
- lane 选择失败后支持 fallback

### lane 调度能力

当前已经支持：

- 顺序轮转
- 随机分配
- balance 分配
- sticky lane
- lane 权重
- lane 级 telemetry（使用时间、错误时间）

### Sticky

HTTP 请求支持通过请求头传入 sticky key：

```text
X-ProxyPools-Sticky-Key
```

相同 key 会尽量命中同一条 lane；当该 lane 不可用时，会 fallback 到其他健康 lane。

### Host / Header 请求级分流

当前代码已经支持按以下规则把请求定向到指定 lane：

- `Host`
- `HeaderName + HeaderValue`

并且行为已经收口为：

- 规则命中优先于 sticky
- 目标 lane 不健康或转发失败时，允许 fallback 到其他健康 lane
- 相关行为已有测试覆盖

### 当前限制

`DispatcherRuleConfig` 已在运行时模型中实现并有测试覆盖，但**规则的外部配置入口目前还没有通过环境变量或 Web API 暴露出来**。也就是说：

- 请求级分流能力在代码层是可用的
- 但如果你想把规则做成外部可配置项，目前还需要继续扩展配置加载或管理面

---

## Web 管理面

管理面默认使用 Basic Auth 保护，入口是：

```text
/
```

健康检查接口公开：

```text
/healthz
```

前端页面已经支持：

- 运行状态查看
- port 切换
- 节点启用 / 禁用
- 手动切换活动节点
- dispatcher 状态查看
- lane 状态查看

页面文案以简体中文为主。

---

## API 概览

以下接口来自 `internal/web/router.go`。

### 公开接口

- `GET /healthz`

### 需要 Basic Auth 的接口

#### 查询接口

- `GET /api/runtime`
- `GET /api/dispatcher`
- `GET /api/ports`
- `GET /api/ports/{portKey}/runtime`
- `GET /api/subscription`
- `GET /api/events`

#### 控制接口

- `POST /api/runtime/settings`
- `POST /api/ports/{portKey}/runtime/settings`
- `POST /api/subscription/refresh`
- `POST /api/runtime/unlock`
- `POST /api/ports/{portKey}/runtime/unlock`
- `POST /api/nodes/{id}/enable`
- `POST /api/nodes/{id}/disable`
- `POST /api/nodes/{id}/switch`
- `POST /api/ports/{portKey}/nodes/{id}/enable`
- `POST /api/ports/{portKey}/nodes/{id}/disable`
- `POST /api/ports/{portKey}/nodes/{id}/switch`

---

## 测试与验证

运行全部测试：

```bash
go test ./...
```

构建检查：

```bash
go build ./...
```

当前仓库已经验证通过：

- `go test ./...`
- `go build ./...`

并且关键场景已有测试覆盖：

- dispatcher HTTP / SOCKS relay
- sticky lane
- Host / Header 规则选 lane
- 规则优先于 sticky
- 目标 lane 失败后的 fallback
- config 校验
- e2e 代理栈

---

## 目录结构

```text
cmd/proxypools/              程序入口
internal/app/                应用编排、调度、健康检查、订阅刷新
internal/config/             配置加载与校验
internal/model/              统一数据模型
internal/parser/             订阅解析与归一化
internal/pool/               评分、选择、健康检查、dispatcher selector
internal/runtime/            sing-box 配置构建与进程管理
internal/storage/sqlite/     SQLite 持久化层
internal/subscription/       订阅服务
internal/web/                路由、鉴权、处理器、静态页面
tests/e2e/                   端到端测试
docs/                        设计与实施文档
```

---

## 适用场景

适合：

- 单用户 / 自托管代理池
- VPS 场景下长期提供固定代理入口
- 想把订阅节点运行时管理做成可观测、可控制的控制面
- 想在现有 sing-box 能力之上做编排而不是重造内核

不适合：

- 多租户生产 SaaS
- 大规模分布式调度
- 复杂权限系统
- 开箱即用的集群高可用方案

---

## 当前已知限制

- 请求级分流规则尚未暴露成 env / Web 可配置项
- 项目目前没有单独附带 `LICENSE`
- 当前更偏单机自托管架构，不是多实例集群方案
- Docker Compose 示例仍以仓库内示例 env 文件为入口，生产使用建议自行拆分私有环境文件

---

## 后续可继续扩展的方向

- Web 侧增加 dispatcher rules 可视化配置
- 补充 Header + Sticky 组合场景的显式回归测试
- 增加更完整的部署、监控、日志文档
- 支持更丰富的规则路由输入
- 增强导入/导出配置能力

---

## 相关文档

- 设计文档：`docs/superpowers/specs/2026-04-11-subscription-proxy-pool-design.md`
- 实施计划：`docs/superpowers/plans/2026-04-11-subscription-proxy-pool-implementation.md`

如果你准备继续把它做成更完整的公开项目，建议下一步先补：`LICENSE`、发布说明、示例配置和部署文档。