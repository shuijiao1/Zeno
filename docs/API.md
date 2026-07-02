# API 草案

JiaoProbe API 全新设计，不兼容旧系统。

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

Admin API 第一版只给管理 UI 或 CLI 使用，需要 `X-Admin-Token`。

- `GET /api/admin/v1/nodes`
- `POST /api/admin/v1/nodes`
- `PATCH /api/admin/v1/nodes/{node_id}`
- `POST /api/admin/v1/nodes/{node_id}/rotate-token`
- `GET /api/admin/v1/probe-targets`
- `POST /api/admin/v1/probe-targets`
- `PATCH /api/admin/v1/probe-targets/{target_id}`

Admin API 返回中必须隐藏 token 原文。
