# Data Model / 第一版 SQLite 数据模型

JiaoProbe 第一版使用 SQLite。schema 不兼容任何旧系统。

## nodes

服务器 / Agent 节点。

```sql
CREATE TABLE nodes (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'no_data',
  country_code TEXT,
  region TEXT,
  billing_mode TEXT NOT NULL DEFAULT 'both',
  monthly_quota_bytes INTEGER,
  monthly_reset_day INTEGER NOT NULL DEFAULT 1,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  last_seen_at INTEGER
);
```

## host_info

相对低频上报的主机信息。

```sql
CREATE TABLE host_info (
  node_id TEXT PRIMARY KEY REFERENCES nodes(id),
  hostname TEXT,
  os_name TEXT,
  os_version TEXT,
  kernel TEXT,
  arch TEXT,
  virtualization TEXT,
  cpu_model TEXT,
  cpu_cores INTEGER,
  memory_total_bytes INTEGER,
  disk_total_bytes INTEGER,
  boot_time INTEGER,
  agent_version TEXT,
  updated_at INTEGER NOT NULL
);
```

## state_samples

实时资源状态样本。

```sql
CREATE TABLE state_samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id TEXT NOT NULL REFERENCES nodes(id),
  ts INTEGER NOT NULL,
  cpu_percent REAL,
  memory_used_bytes INTEGER,
  memory_total_bytes INTEGER,
  disk_used_bytes INTEGER,
  disk_total_bytes INTEGER,
  net_in_total_bytes INTEGER,
  net_out_total_bytes INTEGER,
  net_in_speed_bps REAL,
  net_out_speed_bps REAL,
  uptime_seconds INTEGER
);

CREATE INDEX idx_state_samples_node_ts ON state_samples(node_id, ts);
```

## traffic_monthly

月流量累计。Controller 根据 state 的累计 counter delta 更新。

```sql
CREATE TABLE traffic_monthly (
  node_id TEXT NOT NULL REFERENCES nodes(id),
  month TEXT NOT NULL,
  in_bytes INTEGER NOT NULL DEFAULT 0,
  out_bytes INTEGER NOT NULL DEFAULT 0,
  billable_bytes INTEGER NOT NULL DEFAULT 0,
  last_in_total_bytes INTEGER,
  last_out_total_bytes INTEGER,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (node_id, month)
);
```

计算规则见 `LATENCY_STATS_SPEC.md` 和技术设计文档中的流量章节。

## probe_targets

探测目标。

```sql
CREATE TABLE probe_targets (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL, -- ping | tcping
  address TEXT NOT NULL,
  port INTEGER,
  count INTEGER NOT NULL,
  timeout_ms INTEGER NOT NULL,
  interval_sec INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

## node_probe_targets

哪些节点执行哪些探测目标。

```sql
CREATE TABLE node_probe_targets (
  node_id TEXT NOT NULL REFERENCES nodes(id),
  target_id TEXT NOT NULL REFERENCES probe_targets(id),
  enabled INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (node_id, target_id)
);
```

## probe_rounds

每轮探测摘要。

```sql
CREATE TABLE probe_rounds (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id TEXT NOT NULL REFERENCES nodes(id),
  target_id TEXT NOT NULL REFERENCES probe_targets(id),
  ts INTEGER NOT NULL,
  type TEXT NOT NULL,
  sent INTEGER NOT NULL,
  received INTEGER NOT NULL,
  loss_percent REAL NOT NULL,
  min_ms REAL,
  avg_ms REAL,
  median_ms REAL,
  max_ms REAL,
  stddev_ms REAL,
  error TEXT
);

CREATE INDEX idx_probe_rounds_node_target_ts ON probe_rounds(node_id, target_id, ts);
```

## probe_samples

每轮 raw samples。

```sql
CREATE TABLE probe_samples (
  round_id INTEGER NOT NULL REFERENCES probe_rounds(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  success INTEGER NOT NULL,
  latency_ms REAL,
  error TEXT,
  PRIMARY KEY (round_id, seq)
);
```

## notification_channels

通知渠道。`credential` 用于后续发送，不通过 Admin API 响应返回；展示层只能看到 `credential_set`。

```sql
CREATE TABLE notification_channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL, -- telegram | webhook
  destination TEXT NOT NULL,
  credential TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

## notification_types

通知类型开关。

```sql
CREATE TABLE notification_types (
  event_type TEXT PRIMARY KEY, -- node_online | node_offline | probe_unhealthy
  enabled INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
);
```

## settings

全局设置。

```sql
CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
```
