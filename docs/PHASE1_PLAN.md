# Phase 1 Plan / 第一阶段计划

> 这是早期阶段计划文档。Phase 1 的规格锁定已经完成，Zeno 现在已进入可运行预览和后台管理迭代阶段。

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
- `docs/TECHNICAL_DESIGN.md`

## 当前状态

Phase 1 文档和 mock Web UI 已不再是当前工作终点。当前 Zeno 已具备：

- Go Controller + SQLite。
- Go Agent + systemd。
- Public API 和 Admin API。
- React 前台和后台。
- Agent 主机状态、资源历史、月流量、延迟/服务探测。
- `tcping`、`ping`/ICMP、`http_get` 探针目标。
- Admin 服务器、探针目标、节点分配和通知配置。
- Telegram-only 通知 dispatch、测试发送和 sanitized delivery history。
- Hytron 预览部署：`/opt/zeno`，`zeno-controller.service`，`zeno-agent.service`。

## 当前迭代原则

1. 继续按薄切片推进：后端测试、最小实现、前端/API 边界、构建验证、部署 smoke。
2. 保持绿色项目边界：不加旧系统兼容，不引入远控/命令执行。
3. 保持已确认 UI：主页卡片、详情页密度、Admin 分区和列表/弹窗结构不随手重设计。
4. Admin API 响应继续使用 explicit DTO，不返回 token、secret、hash、credential、bearer 等敏感字段。
5. 每个部署切片结束时清理 build/tmp，确认 Hytron 服务 active、`/health` OK、本地 git clean。

## 下一批建议切片

1. 小型告警规则引擎：从资源/延迟阈值规则开始，绑定现有通知渠道和 delivery history。
2. 设置/外观配置：站点标题、头像、主题、背景等轻量配置，不做复杂多用户。
3. 数据保留与维护：SQLite 样本保留策略和安全清理入口。
4. 安装/发布工具：固化 Hytron deploy pattern，并完善 Agent 多平台安装/升级。
