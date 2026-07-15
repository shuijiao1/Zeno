# Zeno API

Zeno API 全新设计，不兼容旧系统。

## 认证约定

Agent 请求头：

```http
Authorization: Bearer <agent-token>
X-Node-ID: <node-id>
X-Agent-Version: <version>
```

安全要求：

- token 不放 query string。
- Controller 只存 token hash。
- 认证失败返回 401。
- disabled node 返回 403。

Admin 登录：

- `POST /api/admin/v1/login` 使用单管理员账号（默认 `admin`，可在后台账户页修改）+ 密码换取 opaque session token。
- 后续 Admin API 继续通过 `X-Admin-Token: <session-token>` 调用；兼容首次部署时的 bootstrap admin token。
- `GET /api/admin/v1/account` 返回当前单管理员账号。
- `POST /api/admin/v1/account` 修改账号和可选新密码，会轮换 session；改过账号或密码后，旧 bootstrap token 不再作为后台 API 凭据使用。
- `POST /api/admin/v1/logout` 注销当前 session。
- 登录失败有内存限速，避免暴力尝试。

### POST /api/admin/v1/login

```json
{
  "username": "admin",
  "password": "current-password"
}
```

响应：

```json
{
  "username": "admin",
  "token": "opaque-session-token"
}
```

### GET /api/admin/v1/account

请求头：

```http
X-Admin-Token: <session-token>
```

响应：

```json
{
  "account": {
    "username": "admin"
  }
}
```

### POST /api/admin/v1/account

请求头：

```http
X-Admin-Token: <session-token>
```

请求：

```json
{
  "username": "admin",
  "current_password": "current-password",
  "new_password": "new-password-or-empty"
}
```

响应同登录，会返回新的 session token。`new_password` 留空时只修改账号。

### POST /api/admin/v1/logout

请求头：

```http
X-Admin-Token: <session-token>
```

成功返回 `204`。

## Agent API

### POST /api/agent/v1/heartbeat

请求：

```json
{
  "now": 1782990000
}
```

响应：

```json
{
  "ok": true,
  "server_time": 1782990000
}
```

### POST /api/agent/v1/host

请求：

```json
{
  "hostname": "hytron",
  "os_name": "Debian",
  "os_version": "13",
  "kernel": "6.12.x",
  "arch": "x86_64",
  "virtualization": "kvm",
  "cpu_model": "AMD EPYC",
  "cpu_cores": 2,
  "memory_total_bytes": 2147483648,
  "disk_total_bytes": 42949672960,
  "boot_time": 1782900000,
  "agent_version": "0.1.0",
  "public_ipv4": "198.51.100.8",
  "public_ipv6": "2001:db8::8",
  "country_code": "JP"
}
```

`public_ipv4`、`public_ipv6`、`country_code` 由 Agent 轻量自动识别后 best-effort 上报。字段可省略；Controller 只用非空且合法的值更新节点元数据，识别失败不会清空后台已有 IPv4 / IPv6 / 国家码。

### POST /api/agent/v1/state

请求：

```json
{
  "ts": 1782990000,
  "cpu_percent": 12.3,
  "load1": 0.42,
  "load5": 0.35,
  "load15": 0.28,
  "memory_used_bytes": 751619276,
  "memory_total_bytes": 2147483648,
  "swap_used_bytes": 268435456,
  "swap_total_bytes": 1073741824,
  "disk_used_bytes": 8589934592,
  "disk_total_bytes": 42949672960,
  "net_in_total_bytes": 123456789,
  "net_out_total_bytes": 987654321,
  "net_in_speed_bps": 10240,
  "net_out_speed_bps": 20480,
  "process_count": 88,
  "tcp_connection_count": 34,
  "uptime_seconds": 86400
}
```

`load1` / `load5` / `load15`、`swap_*`、`process_count`、`tcp_connection_count` 是新 Agent 上报字段；旧 Agent 省略时会按 `null` 存储/展示，不会伪装成 0。`tcp_connection_count` 统计 `/proc/net/tcp*` 的连接表数据行，包含监听等 TCP socket 行。

### GET /api/agent/v1/probe-targets

响应：

```json
{
  "version": 12,
  "targets": [
    {
      "id": "google-dns",
      "name": "Google DNS",
      "type": "ping",
      "address": "8.8.8.8",
      "count": 20,
      "timeout_ms": 600,
      "interval_sec": 30
    },
    {
      "id": "telegram-dc5",
      "name": "Telegram DC5",
      "type": "tcping",
      "address": "149.154.171.255",
      "port": 443,
      "count": 10,
      "timeout_ms": 600,
      "interval_sec": 30
    },
    {
      "id": "zeno-health",
      "name": "Zeno Health",
      "type": "http_get",
      "address": "https://example.com/health",
      "count": 2,
      "timeout_ms": 600,
      "interval_sec": 30
    }
  ]
}
```

`version` 是 Controller 当前探针配置版本；Admin 对探针目标的新增、编辑、删除会在同一数据库事务里更新配置和递增该版本。Agent 应把本次下发配置对应的 `version` 原样作为 `config_version` 随 `/api/agent/v1/probe-results` 上报。

探针目标类型：

- `tcping`：TCP 连接探测，必须带 `port`。
- `ping`：ICMP ping，不带 `port`。
- `http_get`：HTTP/HTTPS GET 探测，`address` 必须是完整 `http://` 或 `https://` URL，不带 `port` 字段；2xx/3xx 算成功，4xx/5xx 作为失败样本记录 `http_status_<code>`。

Controller 对下发给单个节点的探针配置做资源上限：最多 32 个启用目标；单目标 `count` 为 1–32，`timeout_ms` 为 100–5000，`interval_sec` 为 5–3600；单目标 `count * timeout_ms` 不超过 60000ms 且不超过 `interval_sec`，单节点一轮总预算不超过 120000ms。迁移前遗留的超大配置会在下发/本地 collector 执行前裁剪到这些范围。

### POST /api/agent/v1/probe-results

请求：

```json
{
  "config_version": 12,
  "rounds": [
    {
      "round_id": "9f8b765f7a0c41a9823f478786e8dbb9",
      "target_id": "google-dns",
      "ts": 1782990000,
      "type": "ping",
      "sent": 20,
      "received": 20,
      "loss_percent": 0,
      "min_ms": 0.51,
      "avg_ms": 0.66,
      "median_ms": 0.63,
      "max_ms": 1.2,
      "stddev_ms": 0.12,
      "samples": [
        {"seq": 1, "success": true, "latency_ms": 0.62},
        {"seq": 2, "success": false, "latency_ms": null, "error": "timeout"}
      ]
    }
  ]
}
```

`config_version` 必须来自最近一次 `/api/agent/v1/probe-targets` 响应的 `version`。滚动升级期间，上一版 Agent 使用的顶层 `version` 仍作为别名接收，并执行相同版本校验；若 `version` 与 `config_version` 同时提供且非零值不同，请求会被拒绝。非零版本上报时，Controller 会在同一个 SQLite 事务边界内比较当前配置版本、读取当前启用目标，并写入整批 round/sample；若配置已变化，返回 `409 Conflict` 和 `{"error":"stale_probe_config"}`，整批结果不会写入任何 round/sample，Agent 应重新拉取探针配置后再上报。两个版本字段均为 `0` 或均省略只用于旧 Agent 兼容：Controller 无法判断旧配置是否过期，因此不做版本比较，但仍会在同一事务里按当前启用目标、类型和 `count` 校验整批数据；任一 round 不符合当前配置时整批拒绝且零写入。

`round_id` 由 Agent 为每轮探测生成，并在重试同一请求时保持不变；Controller 用它去重精确重试，同时允许同一秒内保存不同探测轮次。旧版 Agent 未提供该字段时，Controller 仍使用样本摘要兼容去重。单次上报最多 32 个 round；每个 round 的样本数不能超过该目标下发的 `count`，且硬上限为 32。

## Public API

### GET /api/public/v1/settings

读取公开站点配置。首页启动时会先读取该接口，用于品牌标题、头像/Logo、副标题、主题、Agent 接入 URL、电脑端/手机端背景图，以及管理员配置的自定义 CSS。头像/Logo 只用 `logo_url` 一个字段，不再拆出额外头像字段。图片字段只保存 URL / 站内静态路径，不存图片二进制。响应只包含公开展示字段，不包含 Admin token、Agent token、token hash、通知渠道凭据、secret 或 credential 原文。

默认值：

```json
{
  "site_title": "Zeno",
  "site_subtitle": "服务器运行概览",
  "logo_url": "/assets/logo/id.png",
  "theme": "system",
  "agent_controller_url": "",
  "background_url": "",
  "desktop_background_url": "",
  "mobile_background_url": "",
  "appearance_preset": "default",
  "card_opacity": 0.72,
  "card_blur": 0,
  "card_radius": 20,
  "border_strength": 0.26,
  "shadow_strength": 0.22,
  "background_overlay": 0,
  "theme_color": "#2563eb",
  "custom_code": ""
}
```

### GET /api/public/v1/summary

首页使用，返回节点卡片所需数据。节点按后台 `display_order ASC, id ASC` 排序；`expiry_label` 来自后台节点的 `expiry_date`，没有配置时为空，前端按永久展示。前端会把 summary 写入 localStorage，携带 `storedAt`，短 TTL 内作为新鲜数据；超过短 TTL 仍可作为兜底展示，但必须标出“数据已过期 / 最后更新”，同时展示 WS/HTTP 当前状态。

节点响应示例：

```json
{
  "nodes": [
    {
      "id": "hytron",
      "display_name": "Hytron",
      "status": "online",
      "country_code": "HK",
      "expiry_label": "2026-08-01",
      "cpu_percent": 12.5,
      "memory_used_bytes": 1073741824,
      "memory_total_bytes": 2147483648,
      "disk_used_bytes": 10737418240,
      "disk_total_bytes": 42949672960,
      "net_in_speed_bps": 1024,
      "net_out_speed_bps": 2048,
      "net_in_total_bytes": 4096,
      "net_out_total_bytes": 8192,
      "billing_mode": "both",
      "monthly_reset_day": 15,
      "monthly_period_start": "2026-06-15",
      "monthly_period_end": "2026-07-14",
      "monthly_billable_bytes": 1099511627776,
      "monthly_quota_bytes": 2199023255552
    }
  ],
  "services": [
    {
      "id": "google",
      "name": "Google",
      "type": "http_get",
      "address": "https://www.google.com/generate_204",
      "assigned_node_count": 10,
      "reporting_node_count": 9,
      "median_ms": 1.2,
      "loss_percent": 0,
      "updated_at": "2026-07-04T12:00:00Z"
    }
  ],
  "latency_points": []
}
```

`monthly_period_start` / `monthly_period_end` 是当前流量计费周期的 UTC 日期范围，按该节点 `monthly_reset_day` 计算；`monthly_billable_bytes` 也取同一周期。

`services` 是公开服务详情页使用的探针目标摘要。它按后台探针目标显示顺序返回已启用目标，`assigned_node_count` 是分配且启用的节点数量，`reporting_node_count` 是最近 24 小时内有上报的节点数量，延迟/丢包取该服务最新一条探测结果；前台首页不单独展示监控服务列表。

### GET /api/public/v1/services/{target_id}/latency

查询某个监控服务在所有节点上的历史延迟。前端把每个节点作为一条曲线，用于服务详情页。`range` 支持 `1h`、`1d`、`7d`、`30d`；未登录前台只提供 `1h` / `1d`，登录后才显示 `7d` / `30d`。若后端对长范围返回 401，前端会清除本地 Admin token 并提示重新登录。

```json
{
  "target": {
    "id": "google",
    "name": "Google",
    "type": "http_get",
    "address": "https://www.google.com/generate_204",
    "assigned_node_count": 10,
    "reporting_node_count": 9,
    "median_ms": 1.2,
    "loss_percent": 0,
    "updated_at": "2026-07-04T12:00:00Z"
  },
  "range": "1d",
  "points": [
    {
      "ts": "2026-07-04T12:00:00Z",
      "node_id": "hytron",
      "node_name": "Hytron",
      "median_ms": 1.2,
      "loss_percent": 0
    }
  ]
}
```

### GET /api/public/v1/nodes/{node_id}/latency

查询某节点延迟图数据。数据来自 Agent 上报的 probe rounds。

参数：

```text
range=1h|1d|7d|30d
```

未登录前台只显示 `1h` / `1d`；登录后才显示 `7d` / `30d`。若长范围请求返回 401，前端清除本地 Admin token 并提示重新登录。详情页优先用 WS 实时更新，WS 不可用/超时/失败时再发轻量 HTTP fallback，避免一进入页面就 HTTP+WS 重复请求。

响应字段重点：

```json
{
  "node_id": "hytron",
  "range": "1h",
  "points": [
    {
      "ts": "2026-07-03T01:20:00Z",
      "target_id": "google-dns",
      "target_name": "Google DNS",
      "median_ms": 0.8,
      "loss_percent": 0
    }
  ]
}
```

### GET /api/public/v1/nodes/{node_id}/state

查询某节点 Agent 状态历史，用于后续资源/网络历史图。数据来自 Agent 上报的 `state_samples`。

参数：

```text
range=1h|1d|7d|30d
```

未登录前台只显示 `1h` / `1d`；登录后才显示 `7d` / `30d`。若长范围请求返回 401，前端清除本地 Admin token 并提示重新登录。详情页优先用 WS 实时更新，WS 不可用/超时/失败时再发轻量 HTTP fallback，避免一进入页面就 HTTP+WS 重复请求。

响应字段重点：

```json
{
  "node_id": "hytron",
  "range": "1h",
  "points": [
    {
      "ts": "2026-07-03T01:20:00Z",
      "cpu_percent": 18.75,
      "load1": 0.42,
      "load5": 0.35,
      "load15": 0.28,
      "memory_used_bytes": 4294967296,
      "memory_total_bytes": 8589934592,
      "swap_used_bytes": 536870912,
      "swap_total_bytes": 2147483648,
      "disk_used_bytes": 42949672960,
      "disk_total_bytes": 171798691840,
      "net_in_total_bytes": 1000000,
      "net_out_total_bytes": 2000000,
      "net_in_speed_bps": 2048.5,
      "net_out_speed_bps": 1024.25,
      "process_count": 88,
      "tcp_connection_count": 34,
      "uptime_seconds": 3601
    }
  ]
}
```

新指标字段可能为 `null`：旧 Agent 或迁移前历史采样没有对应值时，Public API 保持空值，前端不会显示成 0。`tcp_connection_count` 的语义同 Agent 上报字段：统计 `/proc/net/tcp*` 连接表数据行。

## Admin API

Admin API 第一版只给管理 UI 或 CLI 使用，需要请求头：

```http
X-Admin-Token: <admin-token>
```

安全要求：

- admin token 不放 query string。
- Controller 启动时通过 `-admin-token` 或 `-admin-token-file` 配置，内部只比较 hash。
- Admin API 也必须使用显式 DTO，不能返回 `token_hash`、token 原文或 secret 字段。

### GET /api/admin/v1/settings

读取后台可编辑的站点配置。响应包装在 `settings` 字段下，只返回公开可展示字段，不返回任何凭据或 hash。

```json
{
  "settings": {
    "site_title": "Zeno",
    "site_subtitle": "服务器运行概览",
    "logo_url": "/assets/logo/id.png",
    "theme": "system",
    "agent_controller_url": "",
    "background_url": "",
    "desktop_background_url": "",
    "mobile_background_url": "",
    "appearance_preset": "default",
    "card_opacity": 0.72,
    "card_blur": 0,
    "card_radius": 20,
    "border_strength": 0.26,
    "shadow_strength": 0.22,
    "background_overlay": 0,
    "theme_color": "#2563eb",
    "custom_code": "",
    "updated_at": "2026-07-04T12:00:00Z"
  }
}
```

### PATCH /api/admin/v1/settings

部分更新站点配置。所有字段均可省略，提交后 Controller 会 trim 文本并持久化到 SQLite `settings` 表。

请求：

```json
{
  "site_title": "水饺监控",
  "site_subtitle": "VPS 状态总览",
  "logo_url": "/assets/logo/custom.png",
  "theme": "dark",
  "agent_controller_url": "https://zeno.example.com",
  "background_url": "https://example.com/desktop-bg.webp",
  "desktop_background_url": "https://example.com/desktop-bg.webp",
  "mobile_background_url": "https://example.com/mobile-bg.webp",
  "appearance_preset": "gaussian_blur",
  "card_opacity": 0.58,
  "card_blur": 18,
  "card_radius": 24,
  "border_strength": 0.34,
  "shadow_strength": 0.34,
  "background_overlay": 0.08,
  "theme_color": "#6366f1",
  "custom_code": "<style>.home-top-card { border-color: #2563eb; }</style>"
}
```

约束：

- `site_title` 不能为空，最长 64 个字符。
- `site_subtitle` 可为空，最长 140 个字符。
- `theme` 只能是 `system`、`dark` 或 `light`。
- `agent_controller_url` 可为空；非空时不得包含用户名密码、query 或 fragment。远程地址默认使用 `https://`；真实 loopback host 可使用 `http://`，没有反向代理时也允许 `http://<直接 IP>:<显式端口>`。后者会由 Agent 安装器持久化显式 insecure opt-in，并以明文传输 enrollment/runtime bearer token；主机名 HTTP 和没有显式端口的远程 HTTP 会被拒绝。为空时可回退到当前后台地址，但仍执行同一规则。
- `logo_url` 必须是站内绝对路径（如 `/assets/logo/id.png`）或 `https://` URL；当前首页/后台头部头像与 Logo 都使用这一字段。
- `background_url` 是旧兼容字段，当前等价于电脑端背景图；`background_url`、`desktop_background_url`、`mobile_background_url` 均可为空，非空时必须是站内绝对路径或 `https://` URL。手机端背景留空时前端跟随电脑端背景。
- `appearance_preset` 只能是 `default` 或 `gaussian_blur`。预设会在前端作为默认参数组合，具体样式仍由下面的数值字段控制。
- `card_opacity` 范围 `0.2`–`1`；`card_blur` 范围 `0`–`40`；`card_radius` 范围 `8`–`36`；`border_strength` / `shadow_strength` 范围 `0`–`1`；`background_overlay` 范围 `0`–`0.8`；`theme_color` 必须是 `#RRGGBB`。
- `custom_code` 可为空，当前作为 CSS-only 外观扩展注入公开页面：前端只提取 `<style>` 内容或纯 CSS 文本，不执行 `<script>`，也不挂载 HTML 事件处理器；最长 60000 字符。这里是公开展示样式，不要写入 token、secret 或 credential。
- 图片只通过 URL / 站内静态路径引用，不把外观图片写入数据库。
- 后台保存前会先做同口径的客户端校验，减少提交后才被 API 拒绝的情况。
- 响应仍只返回公开展示字段，不返回 Admin token、Agent token、token hash、secret、credential 或任何凭据值。

### GET /api/admin/v1/nodes

节点管理列表，返回 enabled + disabled 节点、状态、地区、到期日、账单周期、显示顺序、公网 IPv4/IPv6、流量计费口径、月流量重置日、配额、last seen、host info 和 agent version。列表按 `display_order ASC, id ASC` 排序；后台 UI 的服务器列表只展示名称、状态、公网 IP、Agent 和编辑操作；IPv4/IPv6 分行显示且不加 v4/v6 前缀；显示顺序可通过整理顺序或编辑表单写回 `display_order`。

响应字段重点：

```json
{
  "nodes": [
    {
      "id": "hytron",
      "display_name": "Hytron",
      "status": "online",
      "country_code": "HK",
      "region": "Hong Kong",
      "disabled": false,
      "billing_mode": "both",
      "monthly_reset_day": 1,
      "expiry_date": "2026-08-01",
      "billing_cycle": "月付",
      "display_order": 10,
      "public_ipv4": "198.51.100.8",
      "public_ipv6": "2001:db8::8",
      "monthly_quota_bytes": 1099511627776,
      "last_seen_at": "2026-07-03T00:00:00Z",
      "created_at": "2026-07-02T00:00:00Z",
      "updated_at": "2026-07-03T00:00:00Z",
      "agent_version": "a0cd835"
    }
  ]
}
```

### POST /api/admin/v1/nodes

新增服务器。Zeno 的服务器接入流程是先在后台添加服务器并编辑名称/地区/配额等管理字段，然后点击“复制安装命令”。

请求：

```json
{
  "display_name": "New Server",
  "country_code": "HK",
  "region": "Hong Kong",
  "expiry_date": "2026-08-01",
  "billing_cycle": "月付",
  "billing_mode": "both",
  "monthly_reset_day": 1,
  "display_order": 10,
  "public_ipv4": "198.51.100.8",
  "public_ipv6": "2001:db8::8",
  "monthly_quota_bytes": 1099511627776
}
```

响应返回新节点 DTO，但不会返回 Agent token 原文或 token hash。新节点默认 `status=no_data`，并自动分配当前启用的探针目标。`billing_mode` 可选 `both`、`in`、`out`、`max`，默认 `both`；`monthly_reset_day` 范围 1–31，默认 1。`expiry_date` 为空时清空到期日；非空时必须是 `YYYY-MM-DD`。`display_order` 必须是非负整数；`public_ipv4` / `public_ipv6` 为空可省略，非空时会校验 IP 版本。

首次复制安装命令时会为没有安装 token 的节点生成一个随机 Agent token 并保存；之后复制同一节点的安装命令会复用这个 token，不会轮换已在线 Agent。后台 UI 提供 Linux / macOS / Windows 三种命令和复制按钮。命令中的 Controller 地址优先使用站点设置里的 `agent_controller_url`；未设置时才使用当前后台请求地址。未显式配置 `-agent-version` 时，安装脚本默认下载 Zeno-Agent 最新 release。

### PATCH /api/admin/v1/nodes/{node_id}

更新节点可编辑管理字段。不会返回 token 原文或 token hash。

请求：

```json
{
  "display_name": "Hytron",
  "country_code": "HK",
  "region": "Hong Kong",
  "expiry_date": "2026-08-01",
  "billing_cycle": "月付",
  "billing_mode": "max",
  "monthly_reset_day": 15,
  "display_order": 10,
  "public_ipv4": "198.51.100.8",
  "public_ipv6": "2001:db8::8",
  "monthly_quota_bytes": 1099511627776,
  "home_probe_target_id": "cloudflare",
  "probe_target_ids": ["cloudflare", "google"],
  "disabled": false
}
```

字段均可部分提交；`monthly_quota_bytes: null` 表示清空月配额；`expiry_date` / `billing_cycle` / `public_ipv4` / `public_ipv6` 提交空字符串表示清空。`billing_mode` 可选 `both`（入站+出站）、`in`（只算入站）、`out`（只算出站）、`max`（入/出取较大）；`monthly_reset_day` 范围 1–31。`display_name` 不能为空，`expiry_date` 非空时必须是 `YYYY-MM-DD`，`display_order` 必须是非负整数，公网 IP 会分别校验 IPv4 / IPv6。

编辑服务器时可同时提交 `probe_target_ids`，后端会在同一事务内替换该服务器的延迟监控关联并更新 `home_probe_target_id`，避免前端为每个目标分别发送 PATCH。首页目标非空时必须包含在 `probe_target_ids` 中；空数组表示取消全部关联。

响应：

```json
{
  "node": {
    "id": "hytron",
    "display_name": "Hytron",
    "status": "online",
    "country_code": "HK",
    "region": "Hong Kong",
      "disabled": false,
      "billing_mode": "max",
      "monthly_reset_day": 15,
      "expiry_date": "2026-08-01",
    "billing_cycle": "月付",
    "display_order": 10,
    "public_ipv4": "198.51.100.8",
    "public_ipv6": "2001:db8::8",
    "monthly_quota_bytes": 1099511627776,
    "created_at": "2026-07-02T00:00:00Z",
    "updated_at": "2026-07-03T00:00:00Z"
  }
}
```

### GET /api/admin/v1/probe-targets

探针目标管理列表，返回 enabled + disabled 目标、显示顺序及分配到哪些节点。列表按 `display_order ASC, id ASC` 排序；后台列表操作只保留编辑目标，显示顺序可通过整理顺序或编辑表单写回 `display_order`。不会返回 Agent token、token hash 或 secret 字段。

响应：

```json
{
  "targets": [
    {
      "id": "hytron-local",
      "name": "Hytron",
      "type": "tcping",
      "address": "127.0.0.1",
      "port": 18980,
      "count": 3,
      "timeout_ms": 600,
      "interval_sec": 30,
      "display_order": 10,
      "enabled": true,
      "assignments": [
        {
          "node_id": "hytron",
          "node_display_name": "Hytron",
          "enabled": true
        }
      ]
    },
    {
      "id": "zeno-health",
      "name": "Zeno Health",
      "type": "http_get",
      "address": "https://example.com/health",
      "port": null,
      "count": 2,
      "timeout_ms": 600,
      "interval_sec": 30,
      "display_order": 20,
      "enabled": true,
      "assignments": []
    }
  ]
}
```

目标类型规则：`tcping` 必须提交有效 `port`；`ping`/`icmp` 会归一成 `ping` 且 `port` 为 `null`；`http`/`https`/`http_get` 会归一成 `http_get`，`address` 必须是完整 URL，`port` 为 `null`。资源上限同 Agent 下发口径：单节点最多 32 个启用目标，`count` 1–32，`timeout_ms` 100–5000，`interval_sec` 5–3600，且单目标/单节点 round 预算不能超限；超限的新增或编辑请求会返回 `400`。

### POST /api/admin/v1/probe-targets

新增探针目标。新目标默认分配到现有节点；响应仍不包含 Agent 凭据。

HTTP GET 示例：

```json
{
  "name": "Zeno Health",
  "type": "http_get",
  "address": "https://example.com/health",
  "port": null,
  "count": 2,
  "timeout_ms": 600,
  "interval_sec": 30,
  "display_order": 20
}
```

### PATCH /api/admin/v1/probe-targets/{target_id}

更新探针目标配置、显示顺序或节点分配。`display_order` 必须是非负整数；`assignments` 省略表示不改变分配，传入时按 `node_id` 更新启用状态。

切换到 `http_get` 时必须同时保证最终 `address` 是完整 URL；Controller 会清空旧 TCP `port` 并以 `null` 返回。切回 `tcping` 时必须提交有效 `port`。

### DELETE /api/admin/v1/probe-targets/{target_id}

删除探针目标。成功返回 `204 No Content`；不存在返回 `404`。删除会同时清理该目标的节点分配和历史 probe round/sample 记录。响应不会返回 Agent token、token hash、secret 或任何凭据字段。

### GET /api/admin/v1/alert-rules

通知类型规则库存。Controller 启动或迁移时会 seed 一组默认规则。后台通知页只展示已启用/已添加的规则，未启用的预置规则通过“添加通知类型”弹窗选择。规则默认作用于全部服务器；`scope_node_ids` 非空时只作用于这些服务器。响应只包含规则配置、作用范围和通知事件标签，不返回 admin token、Agent token、token hash、通知渠道凭据、secret 或 credential 原文。

默认规则覆盖：CPU、内存、磁盘、离线通知。资源规则映射到 `probe_unhealthy` / 异常；离线规则映射到 `node_offline` / 离线。

Controller 会在 Agent 上报时实际使用这些规则：

- `/api/agent/v1/state` 会按启用的资源规则评估 `cpu_percent`、内存使用率、磁盘使用率。资源规则的 `duration_sec` 表示统计窗口，默认按 5 分钟平均值超过阈值时把节点公共状态置为 `warning` 并进入 `probe_unhealthy` 通知链路。
- `/api/agent/v1/probe-results` 只写入探针历史，不再通过延迟或丢包阈值改变节点公共状态。
- 资源规则命中状态会记录在 Controller 内部的 `alert_rule_states` 表，用来避免某一类健康上报误清另一类仍活跃的异常；`alert_rule_states` 只作为 Controller 内部命中状态存储。
- 如果规则配置了 `scope_node_ids`，Agent 上报、规则命中和通知发送都会只对这些服务器生效；空数组表示全部服务器。离线规则的 `duration_sec` 同时作为公共在线/离线状态与离线通知的心跳超时时间，默认 30 秒；presence WebSocket 和服务探测结果不覆盖页面在线状态；Controller 启动后先留出一个完整心跳窗口，再每 5 秒补扫一次过期 `last_seen_at`，把漏掉的离线状态落库并进入同一条 `node_offline` 通知链路。
- 通知发送同时要求：状态转换存在、对应通知类型启用、至少一条映射到该事件类型且对该服务器生效的规则启用、且存在启用并配置好的通知渠道。

```json
{
  "rules": [
    {
      "id": "cpu_high",
      "name": "CPU 使用率",
      "category": "resource",
      "metric": "cpu_percent",
      "comparator": ">=",
      "threshold": 90,
      "threshold_unit": "%",
      "duration_sec": 300,
      "enabled": true,
      "notification_event_type": "probe_unhealthy",
      "notification_label": "异常",
      "description": "",
      "scope_node_ids": [],
      "created_at": "2026-07-03T00:00:00Z",
      "updated_at": "2026-07-03T00:00:00Z"
    },
    {
      "id": "node_offline",
      "name": "离线通知",
      "category": "liveness",
      "metric": "heartbeat_age_sec",
      "comparator": ">=",
      "threshold": 30,
      "threshold_unit": "s",
      "duration_sec": 30,
      "enabled": true,
      "notification_event_type": "node_offline",
      "notification_label": "离线",
      "description": "",
      "scope_node_ids": ["hytron"],
      "created_at": "2026-07-03T00:00:00Z",
      "updated_at": "2026-07-03T00:00:00Z"
    }
  ]
}
```

### PATCH /api/admin/v1/alert-rules/{rule_id}

部分更新通知类型规则的安全可调字段。当前允许调整启用状态、阈值、统计窗口/确认时间和作用服务器范围；启用状态在 Admin 中表现为添加 / 移除通知类型。规则 id、名称、指标、比较符、通知事件类型等结构性字段由 seed/代码控制。`scope_node_ids` 省略表示保持原范围不变，空数组表示作用于全部服务器，非空数组表示只作用于这些服务器；数组里的 node id 必须存在且不能重复。前端保存通知类型时会等待该请求成功后才关闭弹窗；如后端后续提供原子化“规则 + 通知事件类型”接口，应优先改用原子接口，当前兼容路径失败时会留在弹窗并显示短错误。

请求：

```json
{
  "enabled": true,
  "threshold": 85,
  "duration_sec": 300,
  "scope_node_ids": ["hytron", "backup"]
}
```

字段均可部分提交；`threshold` 和 `duration_sec` 必须是非负数。成功响应返回更新后的单条规则：

```json
{
  "rule": {
    "id": "cpu_high",
    "name": "CPU 使用率",
    "category": "resource",
    "metric": "cpu_percent",
    "comparator": ">=",
    "threshold": 85,
    "threshold_unit": "%",
    "duration_sec": 300,
    "enabled": true,
    "notification_event_type": "probe_unhealthy",
    "notification_label": "异常",
    "description": "",
    "scope_node_ids": ["hytron", "backup"],
    "created_at": "2026-07-03T00:00:00Z",
    "updated_at": "2026-07-03T00:05:00Z"
  }
}
```

关系说明：`alert_rules` 决定“什么时候形成某类状态事件”以及是否发送；`notification_channels` 决定发送到哪些启用渠道。`notification_types` 仅保留为旧 API 兼容层，不再作为发送前置条件。

### GET /api/admin/v1/notification-channels

Telegram 通知渠道管理列表。Zeno 当前只支持 Telegram 一个通知渠道类型；API 不暴露 `type` / `channel_type`，也不返回已保存的 Bot Token。后台只能通过 `credential_set` 判断是否已配置凭据；编辑弹窗的 Token 输入框留空表示保留原值。

```json
{
  "channels": [
    {
      "id": "telegram-home",
      "name": "Telegram Home",
      "destination": "7579942307",
      "credential_set": true,
      "enabled": true,
      "created_at": "2026-07-03T00:00:00Z",
      "updated_at": "2026-07-03T00:00:00Z"
    }
  ]
}
```

### POST /api/admin/v1/notification-channels

新增 Telegram 通知渠道。`destination` 是 Telegram chat id；`credential` 是 Telegram Bot Token，只写入保存用于后续 `sendMessage`，任何列表/创建/更新响应都不会回显原文，只返回 `credential_set` 表示是否已配置。请求不接受 `type` 字段。

```json
{
  "name": "Telegram Home",
  "destination": "7579942307",
  "credential": "***",
  "enabled": true
}
```

### PATCH /api/admin/v1/notification-channels/{channel_id}

部分更新 Telegram 通知渠道。省略 `credential`（后台编辑框留空时也会省略）会保留旧 Bot Token；传入新 `credential` 时覆盖旧 Bot Token。响应仍只返回 `credential_set`，不返回 `credential` 原文。请求不接受 `type` 字段。

### DELETE /api/admin/v1/notification-channels/{channel_id}

删除通知渠道。成功返回 `204 No Content`；不存在返回 `404`。

### POST /api/admin/v1/notification-channels/{channel_id}/test

显式测试某个已启用的 Telegram 通知渠道。这个接口只在后台管理员点击“测试发送”时调用，同步发送一条合成的 `test_notification` / `测试发送` 事件。

响应只返回本次测试发送结果 DTO，不返回渠道 Bot Token、token 原文、secret、credential、Authorization header 或任何 hash。

```json
{
  "delivery": {
    "event_type": "test_notification",
    "label": "测试发送",
    "node_id": "admin-test",
    "node_name": "Zeno",
    "previous_status": "test",
    "status": "test",
    "channel_id": "telegram-home",
    "channel_name": "Telegram Home",
    "success": true,
    "created_at": "2026-07-03T00:10:00Z"
  }
}
```

### PATCH /api/admin/v1/notification-types/{event_type}

兼容旧后台的一对一通知类型开关，仅支持 `node_offline` 和 `renewal_due`，并同步更新同名 alert rule。新的后台应使用 `/api/admin/v1/alert-rules/{rule_id}` 精确启用/关闭单条规则。`probe_unhealthy` 由 CPU、内存、磁盘等多条独立规则共享，调用此兼容接口会返回 `410 Gone`，避免成功但不生效或误批量修改。

```json
{
  "enabled": true
}
```

## 通知发送

状态类发送逻辑挂在 Agent 状态变化、资源状态上报和探测结果上报之后：

- 非 `offline` → `offline`：触发 `node_offline`。
- 非 `warning` → `warning`：触发 `probe_unhealthy`。
- `/api/agent/v1/state` 会依据启用的 CPU/内存/磁盘通知类型规则触发或保持 `warning`。
- `/api/agent/v1/probe-results` 只写入探针历史，不触发异常通知。
- 状态未变化时不重复发送。
- `renewal_due` 续费提醒由 Controller 独立低频定时任务扫描，不挂在 heartbeat/host/state 高频请求上。扫描会在同一 SQLite 事务中完成按日 claim 和 outbox delivery 创建；Controller 崩溃后由 outbox 继续发送，且并发扫描不会重复创建同一天/同到期日的提醒。

发送前要求：对应 `alert_rules.enabled = 1`，且至少一个 `notification_channels.enabled = 1` 并已配置 Telegram chat id 和 Bot Token。显式 `POST /notification-channels/{channel_id}/test` 是管理员手动测试，但仍要求渠道处于启用状态并已配置凭据。

渠道语义：Telegram-only。`destination` 是 chat id，`credential` 是 Bot Token；Controller 调用 Telegram Bot API 的 `sendMessage`。

通知发送失败不会阻塞 Agent 心跳/状态写入；凭据不会出现在 JSON 响应或通知 payload body 中。

Admin API 返回中必须隐藏 token 原文、token hash、通知渠道凭据原文和 secret 字段。
