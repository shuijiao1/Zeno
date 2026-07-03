# Zeno

Zeno 是一个从零实现的轻量服务器探针 / 在线监控系统。

## 核心原则

- **全新实现**：新 Agent、新 Controller、新数据库、新 API、新前端、新部署方式。
- **不兼容旧系统**：不兼容 Kulin / Nezha / Komari 的 API、DB、Agent 协议或安装方式。
- **展示效果锁定**：首页大卡片、延迟统计口径、延迟图表风格必须保持当前 Kulin/Nezha-like 的关键效果。
- **轻量优先**：第一版只做服务器状态、月流量、ping/tcping 多样本延迟探测。

## MVP 范围

MVP 做：

- Agent 主动向 Controller 上报。
- Host 基础信息：系统、内核、架构、CPU、内存、磁盘、启动时间、Agent 版本。
- State 实时状态：CPU、内存、磁盘、网络累计流量、上下行速度、uptime。
- 月流量：按网卡累计 counter 的 delta 计算，不用速度积分。
- 延迟监控：只做 `ping` 和 `tcping`，每轮保留 raw samples。
- 前端：先用 mock 数据锁定首页卡片和延迟图效果，再接真实 API。

MVP 不做：远程终端、文件管理、远程命令执行、脚本任务、NAT、DDNS、多租户、旧系统迁移兼容。

## 文档入口

- `docs/NON_GOALS.md`：明确不做什么。
- `docs/DISPLAY_LOCK.md`：展示效果锁定总则。
- `docs/HOME_CARD_SPEC.md`：首页服务器大卡片规格。
- `docs/LATENCY_STATS_SPEC.md`：延迟统计口径。
- `docs/LATENCY_CHART_SPEC.md`：延迟图表规格。
- `docs/DATA_MODEL.md`：SQLite 第一版数据模型。
- `docs/API.md`：Agent / Public / Admin API 草案。
- `docs/SECURITY.md`：安全边界。
- `docs/TECHNICAL_DESIGN.md`：总体技术方案。
- `docs/PHASE1_PLAN.md`：第一阶段执行计划。

## 技术路线

- Controller：Go，SQLite。
- Agent：Go，单二进制，systemd。
- Web：Vite + React + TypeScript，图表先用 ECharts 或 uPlot。
- 通信：Agent 主动 HTTPS JSON API。
