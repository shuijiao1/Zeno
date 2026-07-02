# Technical Design / 技术方案

## 总体架构

```text
Agent on VPS  ----HTTPS JSON---->  Controller API  ----SQLite----> Web UI
      |                                  |
      |                                  +--> Public API
      +--> local collectors              +--> Admin API
      +--> ping/tcping probes
      +--> local retry cache (later)
```

## Controller

推荐 Go 单二进制。

职责：

- Agent 鉴权。
- 节点注册 / 管理。
- 接收 host/state/probe 上报。
- 计算在线状态。
- 计算月流量 delta。
- 写 SQLite。
- 提供 Public API 给前端。
- 提供 Admin API 给管理界面或 CLI。

## Agent

推荐 Go 单二进制。

职责：

- 保存本机 node_id / token / controller_url。
- 周期上报 heartbeat。
- 低频上报 host info。
- 高频上报 state。
- 拉取 probe targets。
- 执行 ping / tcping 多样本探测。
- 上报 probe results。
- 后续加入本地 cache/replay。

Agent 不做远控，不接受命令执行。

## Web

推荐 Vite + React + TypeScript。

开发顺序：

1. mock 数据复刻首页大卡片。
2. mock 数据复刻延迟图。
3. 接 Public API。
4. 后台管理 UI。

图表库第一版可用 ECharts，若体积或观感不满意再换 uPlot。

## 在线状态计算

Controller 根据 `last_seen_at` 和最近 state/probe 计算：

```text
online: last_seen_at <= offline_threshold_sec
offline: last_seen_at > offline_threshold_sec
no_data: 从未收到 heartbeat/state
warning: 最近有 heartbeat 但 probe loss 或资源超阈值
```

默认：

```text
heartbeat interval: 30s
offline threshold: 90s
```

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

## 延迟数据

每轮保留 summary + samples：

- summary 用于快速首页和图表。
- samples 用于后续 SmokePing-like 效果、抖动、尖峰、loss 细节。

不要只存 avg。

## 第一阶段产物

第一阶段不写后端，先锁定：

- 项目边界。
- 展示效果。
- 首页卡片规格。
- 延迟统计口径。
- 延迟图表规格。
- API 草案。
- 数据模型草案。
- 安全边界。

下一阶段再用 mock 数据做 Web UI。
