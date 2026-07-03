# API 草案

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
  "agent_version": "0.1.0"
}
```

### POST /api/agent/v1/state

请求：

```json
{
  "ts": 1782990000,
  "cpu_percent": 12.3,
  "memory_used_bytes": 751619276,
  "memory_total_bytes": 2147483648,
  "disk_used_bytes": 8589934592,
  "disk_total_bytes": 42949672960,
  "net_in_total_bytes": 123456789,
  "net_out_total_bytes": 987654321,
  "net_in_speed_bps": 10240,
  "net_out_speed_bps": 20480,
  "uptime_seconds": 86400
}
```

### GET /api/agent/v1/probe-targets

响应：

```json
{
  "targets": [
    {
      "id": "google-dns",
      "name": "Google DNS",
      "type": "ping",
      "address": "8.8.8.8",
      "count": 20,
      "timeout_ms": 1000,
      "interval_sec": 60
    },
    {
      "id": "telegram-dc5",
      "name": "Telegram DC5",
      "type": "tcping",
      "address": "149.154.171.255",
      "port": 443,
      "count": 10,
      "timeout_ms": 1000,
      "interval_sec": 60
    }
  ]
}
```

### POST /api/agent/v1/probe-results

请求：

```json
{
  "rounds": [
    {
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

## Public API

### GET /api/public/v1/summary

首页使用，返回节点卡片所需数据。

### GET /api/public/v1/nodes/{node_id}/latency

查询某节点延迟图数据。数据来自 Agent 上报的 probe rounds。

参数：

```text
range=1h|1d|7d|30d
```

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

响应字段重点：

```json
{
  "node_id": "hytron",
  "range": "1h",
  "points": [
    {
      "ts": "2026-07-03T01:20:00Z",
      "cpu_percent": 18.75,
      "memory_used_bytes": 4294967296,
      "memory_total_bytes": 8589934592,
      "disk_used_bytes": 42949672960,
      "disk_total_bytes": 171798691840,
      "net_in_total_bytes": 1000000,
      "net_out_total_bytes": 2000000,
      "net_in_speed_bps": 2048.5,
      "net_out_speed_bps": 1024.25,
      "uptime_seconds": 3601
    }
  ]
}
```

## Admin API

Admin API 第一版只给管理 UI 或 CLI 使用，需要请求头：

```http
X-Admin-Token: <admin-token>
```

安全要求：

- admin token 不放 query string。
- Controller 启动时通过 `-admin-token` 或 `-admin-token-file` 配置，内部只比较 hash。
- Admin API 也必须使用显式 DTO，不能返回 `token_hash`、token 原文或 secret 字段。

### GET /api/admin/v1/nodes

节点管理列表，返回 enabled + disabled 节点、状态、地区、计费模式、配额、last seen、host info 和 agent version。

### POST /api/admin/v1/nodes

新增服务器。Zeno 的服务器接入流程是先在后台添加服务器并编辑名称/地区/配额等管理字段，然后在该服务器编辑区获取 Agent 安装命令。

请求：

```json
{
  "display_name": "New Server",
  "country_code": "HK",
  "region": "Hong Kong",
  "monthly_quota_bytes": 1099511627776
}
```

响应返回新节点 DTO，但不会返回 Agent token 原文或 token hash。新节点默认 `status=no_data`，并自动分配当前启用的探针目标。

### PATCH /api/admin/v1/nodes/{node_id}

更新节点可编辑管理字段。不会返回 token 原文或 token hash。

请求：

```json
{
  "display_name": "Hytron",
  "country_code": "HK",
  "region": "Hong Kong",
  "monthly_quota_bytes": 1099511627776,
  "disabled": false
}
```

字段均可部分提交；`monthly_quota_bytes: null` 表示清空月配额。`display_name` 不能为空。

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
    "billing_mode": "both",
    "monthly_quota_bytes": 1099511627776,
    "created_at": "2026-07-02T00:00:00Z",
    "updated_at": "2026-07-03T00:00:00Z"
  }
}
```

### GET /api/admin/v1/probe-targets

探针目标管理列表，返回 enabled + disabled 目标及分配到哪些节点。不会返回 Agent token、token hash 或 secret 字段。

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
      "timeout_ms": 1200,
      "interval_sec": 60,
      "enabled": true,
      "assignments": [
        {
          "node_id": "hytron",
          "node_display_name": "Hytron",
          "enabled": true
        }
      ]
    }
  ]
}
```

### POST /api/admin/v1/probe-targets

新增探针目标。新目标默认分配到现有节点；响应仍不包含 Agent 凭据。

### PATCH /api/admin/v1/probe-targets/{target_id}

更新探针目标配置或节点分配。`assignments` 省略表示不改变分配；传入时按 `node_id` 更新启用状态。

### GET /api/admin/v1/notification-channels

通知渠道管理列表。渠道凭据只在写入时提交，响应只返回 `credential_set` 标记，不返回凭据原文。

```json
{
  "channels": [
    {
      "id": "telegram-home",
      "name": "Telegram Home",
      "type": "telegram",
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

新增通知渠道。当前支持 `telegram` 和 `webhook` 两类；`credential` 可以是 Telegram Bot 凭据或 Webhook URL，只保存用于后续发送，响应不会返回。

```json
{
  "name": "Telegram Home",
  "type": "telegram",
  "destination": "7579942307",
  "credential": "***",
  "enabled": true
}
```

### PATCH /api/admin/v1/notification-channels/{channel_id}

部分更新通知渠道。省略 `credential` 时保留旧凭据；传入新 `credential` 时覆盖旧凭据。

### DELETE /api/admin/v1/notification-channels/{channel_id}

删除通知渠道。成功返回 `204 No Content`；不存在返回 `404`。响应不会返回凭据原文。

### GET /api/admin/v1/notification-types

通知类型配置列表。当前保留三类中性事件：上线、离线、异常；默认关闭，通知发送逻辑会读取这里的启用状态。

```json
{
  "types": [
    {"event_type": "node_online", "label": "上线", "enabled": false},
    {"event_type": "node_offline", "label": "离线", "enabled": false},
    {"event_type": "probe_unhealthy", "label": "异常", "enabled": false}
  ]
}
```

### PATCH /api/admin/v1/notification-types/{event_type}

启用或关闭某个通知类型。

```json
{
  "enabled": true
}
```

## 通知发送

当前发送逻辑挂在 Agent 心跳状态变化上：

- `no_data` / `offline` / `warning` → `online`：触发 `node_online`。
- 非 `offline` → `offline`：触发 `node_offline`。
- 非 `warning` → `warning`：触发 `probe_unhealthy`。
- 状态未变化时不重复发送。

发送前同时要求：对应 `notification_types.enabled = 1`，且至少一个 `notification_channels.enabled = 1`。

渠道语义：

- `telegram`：`destination` 是 chat id，`credential` 是 Bot 凭据；发送 `sendMessage`。
- webhook：优先把 destination 当 Webhook URL；credential 作为 Authorization 请求头的 Bearer 值发送。若 destination 不是 URL 且 credential 是 URL，则把 credential 当 Webhook URL 使用。

通知发送失败不会阻塞 Agent 心跳/状态写入；凭据不会出现在 JSON 响应或通知 payload body 中。

Admin API 返回中必须隐藏 token 原文、token hash、通知渠道凭据原文和 secret 字段。
