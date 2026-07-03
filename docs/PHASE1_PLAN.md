# Phase 1 Plan / 第一阶段计划

> 当前阶段目标：只创建规格和技术方案文档，不写业务代码。

## 目标

把 Zeno 的硬约束锁死，避免后续实现时又滑向旧系统兼容、重型探针或 UI 随手改版。

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

## 下一阶段：Mock Web UI

下一阶段才开始写前端代码，且只使用 mock 数据。

任务拆分：

1. 初始化 `web/`：Vite + React + TypeScript。
2. 创建 mock nodes 和 latency 数据。
3. 实现基础 layout 和主题变量。
4. 实现 `ServerCard`。
5. 实现 `ResourceBar` / `TrafficBar`。
6. 实现 `LatencyChart` mock 版。
7. 实现 Home 页面三列卡片。
8. 截图或本地渲染验证桌面 / 移动端。

## 不进入下一阶段前的检查

- 是否仍然明确“不兼容旧系统”。
- 是否锁定首页大卡片展示效果。
- 是否锁定延迟统计口径。
- 是否锁定延迟图表风格。
- 是否没有加入远程命令执行等高风险功能。
