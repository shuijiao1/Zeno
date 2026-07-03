# Technical Design / 技术方案

## 总体架构

```text
Agent on VPS  ----HTTPS JSON---->  Controller API  ----SQLite----> Web UI
      |                                  |
      |                                  +--> Public API
      |                                  +--> Admin API
      |                                  +--> Notification dispatch
      +--> local collectors
      +--> tcping / ping / http_get probes
      +--> local retry cache (later)
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
- 执行 Webhook / Telegram 通知 dispatch。
- 记录 sanitized notification delivery history。
- 提供 Agent binary 下载和 install command 生成。

Controller 不暴露 Agent token、admin token hash、通知凭据或 bearer secret。即使 Admin API 已鉴权，响应也必须走 explicit DTO。

## Agent

Go 单二进制，当前职责：

- 保存本机 node_id / token / controller_url。
- 周期上报 heartbeat。
- 低频上报 host info。
- 高频上报 state。
- 采集 CPU、内存、磁盘、网络、uptime、load、swap、进程数、TCP 连接数。
- 拉取 probe targets。
- 按每个 target 的 `interval_sec` 调度探测。
- 执行 `tcping`、`ping`/ICMP、`http_get` 多样本探测。
- 上报 probe results。
- 后续加入本地 cache/replay。

Agent 不做远控，不接受命令执行。

## Web

Vite + React + TypeScript。

当前页面：

1. 前台主页：服务器卡片、流量/资源概览、延迟摘要。
2. 节点详情页：延迟目标按钮、Kulin-like 延迟图、资源历史图。
3. Admin 后台：概览、服务器、延迟监控、通知四个分区。
4. Admin 管理动作：服务器创建/编辑/安装命令、目标创建/编辑/删除/排序/分配、通知渠道/类型/测试发送/发送记录。

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

- 渠道：Webhook、Telegram。
- 事件：`node_online`、`node_offline`、`probe_unhealthy`、手动 `test_notification`。
- Agent heartbeat 触发的通知异步发送，不能阻塞 Agent 上报。
- Admin 手动测试发送同步返回 sanitized delivery，方便操作员立即验证配置。
- Delivery history 只记录事件、节点、渠道、状态和 sanitized 错误，不记录 webhook URL、bot token、bearer 或凭据原文。

## 部署

Hytron 预览部署：

```text
/opt/zeno/current -> /opt/zeno/releases/zeno-<timestamp>-<sha>
/opt/zeno/data/zeno.db
/opt/zeno/data/agent-token
/opt/zeno/data/admin-token
zeno-controller.service
zeno-agent.service
port 18980
```

部署顺序：

1. 本地测试和 build。
2. 打包 release。
3. 上传 Hytron `/tmp`。
4. 解压到 `/opt/zeno/releases/` 并切换 `current`。
5. `systemctl daemon-reload`。
6. 停 Agent。
7. 重启 Controller 并等待 `/health` OK。
8. 重启 Agent。
9. smoke Admin API / browser / services。
10. 清理本地 build 和远端 `/tmp`。

## 下一步设计重点

1. 小型告警规则引擎：资源/延迟阈值、持续时间、节点范围、通知渠道绑定。
2. 设置/外观配置：站点标题、头像、主题、背景，保持轻量。
3. 数据保留与维护：SQLite 样本保留策略和安全清理入口。
4. 安装/发布工具：固化 release/deploy/rollback，不依赖手工命令。
