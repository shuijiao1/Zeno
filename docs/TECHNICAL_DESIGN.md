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
- 记录 sanitized notification delivery history。
- 提供 Agent binary 下载和 install command 生成。
- 执行状态规则 evaluator，维护当前异常状态。

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

1. 前台主页：服务器卡片、流量/资源概览、延迟摘要、外观设置应用。
2. 节点详情页：延迟目标按钮、延迟图、资源历史图。
3. Admin 后台：概览、服务器、延迟监控、通知、状态规则、当前异常、数据维护、外观设置。
4. Admin 管理动作：服务器创建/编辑/安装命令复制、目标创建/编辑/删除/排序/分配、通知渠道/类型/测试发送/发送记录、状态规则范围、维护清理。

UI 规则：保持已确认主页卡片、详情页密度和 Admin 分区结构，不因数据/API 改动顺手重设计。

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
- `monthly_reset_day`：每台服务器可设置月流量重置日。重置日为 1 时按自然月；重置日大于 1 时，重置日前的样本计入上一个账单周期。

## 延迟 / 服务探测数据

每轮保留 summary + samples：

- summary 用于快速首页和图表。
- samples 用于后续 SmokePing-like 效果、抖动、尖峰、loss 细节。

目标类型：

- `tcping`: TCP connect，必须有 port。
- `ping`: ICMP，不使用 port。
- `http_get`: HTTP/HTTPS GET，不使用 port；2xx/3xx 成功，4xx/5xx 作为 unhealthy 状态失败。

不要只存 avg。

## 通知

当前通知模型保持简单：

- 渠道：Telegram-only。
- 事件：`node_online`、`node_offline`、`probe_unhealthy`、手动 `test_notification`。
- Agent heartbeat 触发的通知异步发送，不能阻塞 Agent 上报。
- Admin 手动测试发送同步返回 sanitized delivery，方便操作员立即验证配置。
- Delivery history 只记录事件、节点、渠道、状态和 sanitized 错误，不记录 chat id、Bot Token 或凭据原文。

## 状态规则 / 当前异常

状态规则持久化为 `alert_rules`，但 Admin 文案使用中性词“状态规则”。

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

当前异常由 `alert_rule_states` 记录，但展示时会结合规则是否启用、节点是否禁用、当前阈值、当前 scope 重新计算，避免阈值或范围调整后留下 stale active。

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

## 数据维护

数据维护通过 Admin API 暴露：

- retention 设置：state samples、probe rounds/samples、notification deliveries。
- candidate counts。
- dry-run cleanup。
- confirmed cleanup。
- 自动清理：`maintenance_enabled=true` 时 Controller 按 `-maintenance-interval` 定期执行同一套确认清理逻辑，默认 24 小时检查一次；默认关闭，避免首次部署误删历史。

默认安全：只统计和清理可再生历史样本/发送记录，不清理节点、token、规则、设置或正式配置。

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
2. 安装文档和自部署说明打磨。
3. 后续服务监控状态页 / 历史页：暂缓。
