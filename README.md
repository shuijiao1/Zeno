# Zeno

Zeno 是一个从零实现的轻量服务器探针 / 在线监控系统。

## 核心原则

- 全新实现：新 Agent、新 Controller、新数据库、新 API、新前端、新部署方式。
- 不兼容旧系统：不兼容 Kulin / Nezha / Komari 的 API、DB、Agent 协议或安装方式。
- 展示效果锁定：首页大卡片、延迟统计口径、延迟图表风格必须保持当前已确认效果。
- 轻量优先：优先做稳定在线监控、主机状态、月流量、延迟/HTTP 探测和必要后台管理；避免 Nezha 级远控复杂度。

## 当前已实现

- Agent 主动向 Controller 上报 heartbeat、host、state、probe results。
- Host 基础信息：系统、内核、架构、CPU、内存、磁盘、启动时间、Agent 版本。
- State 实时状态与历史：CPU、内存、磁盘、网络累计流量、上下行速度、uptime、load、swap、进程数、TCP 连接数。
- 月流量：按网卡累计 counter 的 delta 计算，不用速度积分。
- 延迟/服务探测：`tcping`、`ping`/ICMP、`http_get`，每轮保留 summary 和 raw samples。
- Public API：主页 summary、节点延迟、节点状态历史。
- Web UI：主页卡片、节点详情页、延迟图、资源历史图、移动端紧凑布局。
- Admin API/UI：服务器、探针目标、节点分配、通知渠道、通知类型、发送记录。
- 通知：Telegram-only 渠道，支持上线、离线、探测异常和手动测试发送；响应只暴露 sanitized delivery，不返回 Bot Token。
- 部署预览：Hytron 上以 `zeno-controller.service` / `zeno-agent.service` 运行，路径 `/opt/zeno`。

## 当前不做

- 远程终端、文件管理、远程命令执行、脚本任务。
- NAT、DDNS、多租户、OAuth/复杂角色。
- 旧系统迁移兼容或旧 Agent 协议适配。
- Nezha/Komari/Kulin API、DB 或插件兼容层。

## 下一批优先级

1. 小型告警规则引擎：在固定通知事件之外，支持资源/延迟阈值规则，但不要一次克隆 Kulin 全部复杂选项。
2. 设置/外观配置：站点标题、头像、主题、背景等轻量配置。
3. 数据保留与维护：SQLite 样本保留策略、清理任务和安全的维护入口。
4. 安装/发布工具：完善多平台 Agent 安装、升级和 release 打包流程。

## 文档入口

- `docs/NON_GOALS.md`：明确不做什么。
- `docs/DISPLAY_LOCK.md`：展示效果锁定总则。
- `docs/HOME_CARD_SPEC.md`：首页服务器大卡片规格。
- `docs/LATENCY_STATS_SPEC.md`：延迟统计口径。
- `docs/LATENCY_CHART_SPEC.md`：延迟图表规格。
- `docs/DATA_MODEL.md`：SQLite 数据模型。
- `docs/API.md`：Agent / Public / Admin API。
- `docs/SECURITY.md`：安全边界。
- `docs/TECHNICAL_DESIGN.md`：总体技术方案。
- `docs/PHASE1_PLAN.md`：早期阶段计划和当前阶段状态。

## 技术路线

- Controller：Go，SQLite。
- Agent：Go，单二进制，systemd。
- Web：Vite + React + TypeScript。
- 通信：Agent 主动 HTTPS/JSON API，上报路径与 Public/Admin API 分离。
