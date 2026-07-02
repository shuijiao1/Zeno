# Security / 安全边界

JiaoProbe 的安全原则：Agent 只采集和上报，不接受远程命令。

## Agent 认证

- 每台机器独立 token。
- Controller 只保存 token hash。
- token 只通过 `Authorization: Bearer` 传递。
- token 不进入 URL query string。
- token 不写入日志。
- Admin API 不返回 token 原文。

## 无远程执行

MVP 明确没有：

- command exec。
- shell。
- 文件管理。
- 脚本执行。
- 任务下发。

Controller 下发的只有探测配置：target、type、address、port、count、timeout、interval。

## 探测限制

为了避免被滥用成扫描器：

- MVP 仅支持 ping / tcping。
- tcping 必须显式配置 port。
- count、timeout、interval 有上限 / 下限。
- 不支持任意 HTTP payload。
- 不支持端口范围扫描。
- 不支持批量目标动态下发。

建议限制：

```text
min interval: 30s
max count ping: 20
max count tcping: 10
max timeout: 5000ms
```

## Public API

Public API 只返回展示所需数据：

- 节点显示名。
- 在线状态。
- 资源百分比。
- 延迟摘要。
- 聚合后的流量信息。

不返回：

- token hash。
- admin token。
- agent 本地路径。
- 原始私有配置。
- 任何安装凭据。

## 部署建议

生产部署必须：

- HTTPS。
- Controller 不裸奔公网高危端口。
- Admin API 设置强 token。
- SQLite 文件权限限制在服务用户内。
- systemd unit 使用专用用户时优先。

## 日志红线

日志里不得出现：

- Agent token 原文。
- Admin token 原文。
- 安装命令中的 token。
- Authorization header。

摘要和文档里也不得保留真实 token。
