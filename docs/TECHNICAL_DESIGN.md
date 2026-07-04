# Technical Design / 技术方案

## 总体架构

```text
Agent on VPS  ----HTTPS JSON---->  Controller API  ----SQLite----> Web UI
      |                                  |
      |                                  +--> Public API
      |                                  +--> Admin API
      |                                  +--> Telegram notification dispatch
      +--> local collectors
      +--> tcping / ping / http_get probes
```

Zeno 是全新实现，不兼容 Kulin / Nezha / Komari 的 API、数据库、Agent 协议或安装方式。

## Controller

Go 单二进制，当前职责：

- Agent 鉴权。
- 节点注册 / 管理。
- 接收 heartbeat、host、state、probe results 上报。
- 计算 public status：`online`、`offline`、`warning`、`no_data`。
- 计算月流量 delta。
- 写 SQLite。
- 提供 Public API 给前端。
- 提供 Admin API 给后台。
- 执行 Telegram 通知 dispatch。
- 提供 Agent binary 下载和 install command 生成。
- 执行通知类型 evaluator，维护内部规则命中状态。

Controller 不暴露 Agent token、admin token hash、通知凭据或 bearer secret。即使 Admin API 已鉴权，响应也必须走 explicit DTO。

## Agent

Go 单二进制，当前职责：

- 保存本机 node_id / token / controller_url。
- 周期上报 heartbeat。
- 低频上报 host info。
- 高频上报 state。
- 采集 CPU、内存、磁盘、网络、uptime、load、swap、进程数、TCP 连接数。
- best-effort 自动识别公网 IPv4 / IPv6 / GeoIP 国家码，并随 host report 上报。
- 拉取 probe targets。
- 按每个 target 的 `interval_sec` 调度探测。
- 执行 `tcping`、`ping`/ICMP、`http_get` 多样本探测。
- 上报 probe results。
- 后续可以加入本地 cache/replay。

Agent 不做远控，不接受命令执行。

## Web

Vite + React + TypeScript。

当前页面：

1. 前台主页：服务器卡片、流量/资源概览、延迟摘要、外观设置应用；不单独展示监控服务列表。
2. 节点详情页：延迟目标按钮、延迟图、资源历史图；资源历史包含 CPU、内存、磁盘、网络速率、系统负载、Swap、进程/TCP 连接和网络累计。
3. 服务详情页：同一监控服务在所有节点上的历史延迟曲线。
4. Admin 后台：单管理员登录、账户页修改账号/密码、退出登录、服务器、延迟监控、通知和外观设置；通知页只保留通知渠道和通知类型。
5. Admin 管理动作：服务器创建/编辑/安装命令复制、Agent 接入 URL、目标创建/编辑/删除/排序/分配、通知渠道/类型、通知类型作用范围。

UI 规则：保持已确认主页卡片、详情页密度和 Admin 分区结构；后台参考 Kulin 的清爽布局，但使用 Zeno 自己的视觉语言，不恢复旧介绍区。

## Admin 认证

- 单管理员账号默认是 `admin`，可在后台“账户”页修改账号名。
- 首次部署未设置 `admin_username` / `admin_password_hash` 时，bootstrap admin token 可作为登录密码；修改账号或密码后以 DB 中设置为准，旧 bootstrap token 不再作为后台 API 凭据。
- 登录成功返回 opaque session token，后续 Admin API 仍用 `X-Admin-Token`，但值是 session token。
- 修改密码会轮换 session 并清空旧 session；退出登录删除当前 session。
- 登录失败做内存限速，避免暴力尝试。

## 在线状态计算

Controller 根据 `last_seen_at` 和最近 probe results 计算：

```text
online: last_seen_at <= offline_threshold_sec，且无未恢复的 probe warning
warning: 最近有 heartbeat，但 accepted probe results 显示目标探测异常
offline: last_seen_at > offline_threshold_sec
no_data: 从未收到 heartbeat/state
```

关键规则：heartbeat/host/state 只证明 Agent 活着，不能清除 probe warning；成功的 probe round 才能清除对应 warning。通知事件要从前后 public status 变化推导，不能直接使用 heartbeat 原始状态。

## 月流量计算

Agent 上报累计 counter：

```text
net_in_total_bytes
net_out_total_bytes
```

Controller 计算 delta：

```text
delta_in = current_in_total - last_in_total
delta_out = current_out_total - last_out_total
```

规则：

- 首次 sample 只建立 baseline。
- delta < 0：counter reset，更新 baseline，不计入。
- delta 过大：丢弃或标记异常，避免脏数据污染月流量。
- billing mode：
  - `in`: billable = in_bytes
  - `out`: billable = out_bytes
  - `both`: billable = in_bytes + out_bytes
  - `max`: billable = max(in_bytes, out_bytes)
- `monthly_reset_day`：每台服务器可设置月流量重置日。重置日为 1 时按自然月；重置日大于 1 时，重置日前的样本计入上一个账单周期；月末不存在该日期时按当月最后一天计算。
- Public summary 会返回当前流量计费周期的 `monthly_period_start` / `monthly_period_end`，首页流量条直接展示这段周期范围，避免“本月”口径不清。

## 延迟 / 服务探测数据

每轮保留 summary + samples：

- summary 用于快速首页、服务详情定位和图表。
- samples 保留 raw ping/tcp/http 结果，便于后续扩展抖动、尖峰、loss 细节。

目标类型：

- `tcping`: TCP connect，必须有 port。
- `ping`: ICMP，不使用 port。
- `http_get`: HTTP/HTTPS GET，不使用 port；2xx/3xx 成功，4xx/5xx 作为 unhealthy 状态失败。

不要只存 avg。

Public summary 会返回 `services`，按后台探针目标显示顺序列出已启用服务；服务详情接口 `GET /api/public/v1/services/{target_id}/latency` 把同一目标按节点分线返回，前端直接复用延迟图表。

## 通知

当前通知模型保持简单：

- 渠道：Telegram-only。
- 事件：`node_online`、`node_offline`、`probe_unhealthy`、手动 `test_notification`。
- Agent heartbeat 触发的通知异步发送，不能阻塞 Agent 上报。
- Admin 手动测试发送同步返回本次 sanitized 结果，方便操作员立即验证配置。

## 通知类型

通知类型触发条件持久化为 `alert_rules`，Admin 文案统一放在“通知”下。

当前规则覆盖：

- CPU 使用率。
- 内存使用率。
- 磁盘使用率。
- 探测延迟。
- 探测丢包。
- 离线。
- 恢复。

规则支持：

- `enabled`。
- `threshold` / `threshold_unit`。
- `duration_sec`。
- `scope_node_ids`：为空表示全部服务器；非空时只作用于指定服务器。

`alert_rule_states` 用于 Controller 内部合并不同规则命中状态，避免某一类健康上报误清另一类仍活跃状态。

## 公网 IP / GeoIP

Agent 使用 tokenless、可替换的轻量 HTTP provider 自动发现公网 IPv4、IPv6 和国家码，并通过 `/api/agent/v1/host` 的 `public_ipv4`、`public_ipv6`、`country_code` 上报。

原则：

- 识别结果缓存，默认 6 小时刷新一次，避免每分钟心跳都访问外部 provider。
- IPv6 不可用时只上报 IPv4；IPv4 / IPv6 / GeoIP 任一失败都不影响 heartbeat、state 或 probe 上报。
- Controller 只用非空且合法的字段更新 `nodes.public_ipv4`、`nodes.public_ipv6`、`nodes.country_code`。
- Agent 省略字段或 provider 失败时，Controller 保留后台已有值，不清空。
- 不写死外部 token，不把 provider token 暴露给 Admin API。

## 设置 / 外观

设置保存在通用 `settings` 表中。

当前公开展示字段：

- `site_title`
- `site_subtitle`
- `logo_url`
- `theme`
- `desktop_background_url`
- `mobile_background_url`

兼容字段：`background_url` 会映射到 desktop background。不要重新拆出 `avatar_url`。

## 部署

Hytron 预览部署：

```text
/opt/zeno/current -> /opt/zeno/releases/zeno-<sha>-linux-amd64
/opt/zeno/data/zeno.db
/opt/zeno/data/agent-token
/opt/zeno/data/admin-token
zeno-controller.service
zeno-agent.service
port 18980
```

发布包由 `scripts/package-release.sh` 生成，目标机由 `scripts/deploy-local-release.sh` 安装/更新。`scripts/import-guko-servers.py` 可把 GUKO `server-manager/servers.json` 导入 Zeno Admin nodes，只同步展示元数据，不删除节点、不轮换 Agent token。

安全更新顺序：

1. 本地测试和 build。
2. 打包 release。
3. 上传目标机 `/tmp`。
4. 解压到 `/opt/zeno/releases/`。
5. 停 Agent。
6. 切 `/opt/zeno/current`。
7. 渲染 unit 并 `systemctl daemon-reload`。
8. 重启 Controller 并等待 `/health` OK。
9. Controller 健康后启动 Agent。
10. smoke Admin API / Agent journal / services。
11. 清理远端 `/tmp/zeno-*.tar.gz`。

Controller health 失败时必须回滚 symlink 和 unit，重启旧 Controller；不要在 Controller 不健康时启动 Agent。

## 下一步设计重点

1. 多节点铺 Agent：先确认 Controller 公网 HTTPS 入口，再小批量安装和 smoke。
2. UI polish：拖拽排序等。
