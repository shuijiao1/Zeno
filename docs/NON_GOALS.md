# Zeno Non-Goals / 明确不做

本项目是全新的轻量探针系统，不是旧系统重构，也不是兼容层。

## 不做旧系统兼容

第一版明确不做：

- 不 fork Nezha。
- 不 fork Kulin。
- 不 fork Komari。
- 不迁移旧 Kulin 数据库。
- 不兼容旧 Kulin API。
- 不兼容旧 Kulin Agent 协议。
- 不兼容 Nezha API。
- 不兼容 Komari API。
- 不为了旧系统写 adapter / compatibility layer。
- 不做旧数据迁移工具作为第一目标。

旧系统只允许作为：

- 展示效果参考。
- 延迟统计口径参考。
- UI 验收参考。

## 不做高风险远控功能

MVP 不做任何远程控制能力：

- 远程终端。
- 文件管理。
- 远程命令执行。
- 任意脚本执行。
- 任务下发 / cron 下发。
- SSH 代理。
- NAT / DDNS。

Agent 只做采集、探测、上报、拉取探测配置。

## 不做重型平台能力

MVP 不做：

- 多租户权限系统。
- 复杂 RBAC。
- 插件系统。
- 复杂 HTTP/DNS/MTR/Trace 探测。
- 分布式任务编排。
- Prometheus 兼容导出。
- Kubernetes / 容器编排监控。

这些以后可以讨论，但不能污染第一版。

## 第一版判断标准

如果某功能不能直接服务于以下目标，就不进 MVP：

1. 判断服务器在线 / 离线。
2. 展示服务器基础资源状态。
3. 展示月流量条。
4. 展示 ping/tcping 延迟、丢包、抖动。
5. 保持当前 Kulin 关键展示效果。
