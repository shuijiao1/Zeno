# Data Model / SQLite 数据模型

Zeno 使用 SQLite。schema 不兼容任何旧系统。

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
  expiry_date TEXT,
  billing_cycle TEXT,
  display_order INTEGER NOT NULL DEFAULT 0,
  public_ipv4 TEXT,
  public_ipv6 TEXT,
  billing_mode TEXT NOT NULL DEFAULT 'both',
  monthly_quota_bytes INTEGER,
  monthly_reset_day INTEGER NOT NULL DEFAULT 1,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  last_seen_at INTEGER
);
```

说明：

- `country_code` 用于国旗展示。
- `expiry_date` / `billing_cycle` 用于后台和首页展示到期/账单信息。
- `display_order` 控制首页卡片和后台列表排序。
- `public_ipv4` / `public_ipv6` 可由后台编辑，也会由新 Agent best-effort 自动识别后上报；识别失败不会清空已有值。
- `billing_mode` 控制月流量口径：`both`、`in`、`out`、`max`。
- `monthly_reset_day` 控制账单周期从每月第几天开始，范围 1–31。
- `token_hash` 只存 hash，不通过 API 返回。

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
  load1 REAL,
  load5 REAL,
  load15 REAL,
  memory_used_bytes INTEGER,
  memory_total_bytes INTEGER,
  swap_used_bytes INTEGER,
  swap_total_bytes INTEGER,
  disk_used_bytes INTEGER,
  disk_total_bytes INTEGER,
  net_in_total_bytes INTEGER,
  net_out_total_bytes INTEGER,
  net_in_speed_bps REAL,
  net_out_speed_bps REAL,
  process_count INTEGER,
  tcp_connection_count INTEGER,
  uptime_seconds INTEGER
);

CREATE INDEX idx_state_samples_node_ts ON state_samples(node_id, ts);
```

## traffic_monthly

月流量累计。Controller 根据 state 的累计 counter delta 更新；`month` 是该节点当前计费周期开始月份（例如 `monthly_reset_day=15` 且当前周期为 2026-06-15 至 2026-07-14 时，`month=2026-06`）。

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

## probe_targets

探测目标。

```sql
CREATE TABLE probe_targets (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  address TEXT NOT NULL,
  port INTEGER,
  count INTEGER NOT NULL,
  timeout_ms INTEGER NOT NULL,
  interval_sec INTEGER NOT NULL,
  display_order INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

`type` 当前支持：

- `tcping`：TCP connect，必须带 `port`。
- `ping`：ICMP ping，不带 `port`。
- `http_get`：HTTP/HTTPS GET，不带 `port`。

`display_order` 控制后台延迟监控列表、Agent 目标下发顺序和同一时间点的 Public latency series 展示顺序。

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

## probe_rounds / probe_samples

每轮探测摘要和 raw samples。

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

CREATE TABLE probe_samples (
  round_id INTEGER NOT NULL REFERENCES probe_rounds(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  success INTEGER NOT NULL,
  latency_ms REAL,
  error TEXT,
  PRIMARY KEY (round_id, seq)
);
```

## notification_channels / notification_types / notification_deliveries

通知当前是 Telegram-only 产品路径。SQLite 内部仍保留 `type` / `channel_type` 兼容列，但 Admin API/UI 不暴露多渠道概念。

```sql
CREATE TABLE notification_channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  destination TEXT NOT NULL,
  credential TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE notification_types (
  event_type TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
);

CREATE TABLE notification_deliveries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_type TEXT NOT NULL,
  label TEXT NOT NULL,
  node_id TEXT NOT NULL,
  node_name TEXT NOT NULL,
  previous_status TEXT NOT NULL,
  status TEXT NOT NULL,
  channel_id TEXT NOT NULL,
  channel_name TEXT NOT NULL,
  channel_type TEXT NOT NULL,
  success INTEGER NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

CREATE INDEX idx_notification_deliveries_created_at ON notification_deliveries(created_at DESC, id DESC);
```

`credential` 不通过 Admin API 响应返回；发送记录只返回脱敏字段。

## alert_rules / alert_rule_node_scopes / alert_rule_states

通知类型触发条件和当前异常。

```sql
CREATE TABLE alert_rules (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  category TEXT NOT NULL,
  metric TEXT NOT NULL,
  comparator TEXT NOT NULL,
  threshold REAL NOT NULL,
  threshold_unit TEXT NOT NULL,
  duration_sec INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  notification_event_type TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX idx_alert_rules_sort_order ON alert_rules(sort_order ASC, id ASC);

CREATE TABLE alert_rule_node_scopes (
  rule_id TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
  node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (rule_id, node_id)
);

CREATE INDEX idx_alert_rule_node_scopes_node ON alert_rule_node_scopes(node_id, rule_id);

CREATE TABLE alert_rule_states (
  node_id TEXT NOT NULL REFERENCES nodes(id),
  rule_id TEXT NOT NULL REFERENCES alert_rules(id),
  active INTEGER NOT NULL DEFAULT 0,
  first_seen_at INTEGER,
  last_seen_at INTEGER,
  last_value REAL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (node_id, rule_id)
);

CREATE INDEX idx_alert_rule_states_node_active ON alert_rule_states(node_id, active);
```

`alert_rule_node_scopes` 没有记录时表示规则作用于全部服务器；有记录时只作用于指定服务器。

## settings

全局设置，包含站点标题、Logo、主题、桌面/手机背景图配置。

```sql
CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
```

当前主要 key：

- `site_title`
- `site_subtitle`
- `logo_url`
- `theme`
- `background_url`（兼容）
- `desktop_background_url`
- `mobile_background_url`

## 迁移策略

`ensureSchema` 会在启动时创建缺失表，并通过 additive `ALTER TABLE ... ADD COLUMN` 补齐新增列。现阶段只做向前兼容 additive migration，不兼容旧系统 DB。
