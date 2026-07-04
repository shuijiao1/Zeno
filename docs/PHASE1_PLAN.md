# Phase 1 Plan / 第一阶段计划

> 这是早期阶段计划文档。Phase 1 的规格锁定已经完成，Zeno 现在已进入可运行预览、后台管理和部署工具固化阶段。

## Phase 1 原始目标

把 Zeno 的硬约束锁死，避免后续实现时滑向旧系统兼容、重型探针或 UI 随手改版。

## 已创建文档

- `README.md`
- `docs/NON_GOALS.md`
- `docs/DISPLAY_LOCK.md`
- `docs/HOME_CARD_SPEC.md`
- `docs/LATENCY_STATS_SPEC.md`
- `docs/LATENCY_CHART_SPEC.md`
- `docs/DATA_MODEL.md`
- `docs/API.md`
- `docs/SECURITY.md`
- `docs/SELF_HOSTING.md`
- `docs/TECHNICAL_DESIGN.md`
- `docs/RELEASE_PACKAGING.md`

## 当前状态

Zeno 当前已经具备完整预览闭环：

- Go Controller + SQLite。
- Go Agent + systemd。
- Public API 和 Admin API。
- React 前台和后台。
- Agent 主机状态、资源历史、月流量（含首页计费周期范围）、延迟/服务探测。
- `tcping`、`ping`/ICMP、`http_get` 探针目标。
- Admin 概览当前异常计数和最近通知发送失败数、服务器、服务器排序、Agent 安装命令复制、Agent 接入 URL、探针目标、探针目标排序、节点分配、通知、外观设置和当前异常。
- Telegram-only 通知 dispatch、测试发送和 sanitized delivery history。
- 服务器元数据：到期日、账单周期、显示顺序（后台可上移/下移/整理）、国家码/国旗、公网 IPv4、公网 IPv6，Agent 可自动识别公网 IP / GeoIP。
- 通知类型触发条件支持按服务器范围生效。
- Linux amd64 发布包（含 README/docs）、systemd 模板、本机 Controller 更新脚本、单独 Agent 安装脚本、GUKO 服务器清单导入脚本和自部署指南。
- Hytron 预览部署：`/opt/zeno`，`zeno-controller.service`，`zeno-agent.service`。

## 当前迭代原则

1. 继续按功能包推进：后端、前端、文档、测试、commit、必要时部署 smoke。
2. 保持绿色项目边界：不加旧系统兼容，不引入远控/命令执行。
3. 保持已确认 UI：主页卡片、详情页密度、Admin 分区和列表/弹窗结构不随手重设计。
4. Admin / Public API 响应继续使用 explicit DTO，不返回 token、secret、hash、credential、bearer 等敏感字段。
5. 部署切片结束时清理 build/tmp，确认 Hytron 服务 active、`/health` OK、本地 git clean。

## 下一批建议切片

1. 多节点铺 Agent：在 Controller 公网 HTTPS 入口确认后逐台安装，先少量 smoke，再批量。
2. 后续服务监控状态页 / 历史页：暂缓，确认需要后再做。
3. UI polish：外观 URL 输入体验、拖拽排序等，放在主线闭环之后。
