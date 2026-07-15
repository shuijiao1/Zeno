# Security / 安全边界

Zeno 的安全原则：Agent 只采集和上报，不接受远程命令。

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
- 使用官方 Docker Compose 安装器和固定非 root 运行用户。
- SQLite 文件权限限制在运行用户内，secrets 保持 root 持有并只向运行组开放只读权限。

## 通知渠道凭据

- 通知渠道写入时可以提交 Telegram Bot Token。
- 已登录 Admin 的通知渠道管理响应也不返回已保存的 Telegram Bot Token；只返回 `credential_set` 表示是否已配置。后台编辑弹窗的 Token 输入框留空时保留原值，输入新值才覆盖。
- 渠道凭据不进入 URL query string，不写入日志，不放到 Telegram 汇报里。
- 通知 payload body 不包含渠道凭据。
- Telegram Bot Token 只用于 Telegram API 请求路径，不进入 Admin JSON 响应、通知正文或测试发送结果。
- 通知发送失败不阻塞 Agent 心跳、状态和探测数据写入，避免通知渠道故障拖垮采集入口。
- 渠道凭据在 SQLite 中使用外部 32-byte key 加密；ciphertext 带 key id。Controller 也支持 key ring：
  旧 key 留在 ring 时旧密文可读，新写入只使用 `active_key_id`，因此可以先部署新旧双 key、改写/轮换
  渠道凭据，再移除旧 key。authority key ring 在验证旧 key 后会原子地把 DB fingerprint 绑定推进到
  active key；DB 只保存 key id/fingerprint，不保存 authority key。
- key ring 文件必须是普通文件且不得对 group/other 开放，JSON 格式为
  `{"active_key_id":"2026-07","keys":{"2026-07":"<key>","2026-01":"<old-key>"}}`。
  credential key value 可使用 32-byte raw、hex 或 base64；分别通过
  `--notification-credential-keyring-file` / `ZENO_NOTIFICATION_CREDENTIAL_KEYRING_FILE` 和
  `--notification-authority-keyring-file` / `ZENO_NOTIFICATION_AUTHORITY_KEYRING_FILE` 配置。
- SQLite 文件权限仍是第一道边界；后续如加入专用服务用户，应继续限制 DB 读取权限。

## 日志红线

日志里不得出现：

- Agent token 原文。
- Admin token 原文。
- 安装命令中的 token。
- Authorization header。

摘要和文档里也不得保留真实 token。

## 公网部署 checklist

公开给别人使用或部署到公网前，建议逐项确认：

- Controller 仍只监听 `127.0.0.1:18980`，不要直接把 18980 暴露到公网。
- 公网入口必须走 HTTPS 反向代理，例如 Caddy / Nginx / Cloudflare Tunnel。
- `/opt/zeno/data` 和 `/opt/zeno/secrets` 只允许 root 或服务用户读取。
- Telegram Bot Token、Admin session、Agent token 不要写入 issue、截图、日志或公开文档。
- SQLite 和 secrets 已纳入定期备份。
- 已保存反代配置和 `/opt/zeno/.env`，方便回滚。

## Token 恢复与轮换

### Admin 密码 / session

首次安装生成的 bootstrap token 位于：

```text
/opt/zeno/secrets/zeno_admin_token
```

如果已在后台修改过账号密码，应优先使用后台账户登录。忘记密码时，可以在停机备份数据库后按文档或后续恢复工具重置管理员密码；不要把 bootstrap token 暴露到公网或 issue。

### Agent token

每台服务器应使用独立 Agent token。怀疑泄露时：

1. 在后台重新生成或重建该服务器的 Agent 接入凭据。
2. 在目标机器重新运行后台复制的 Agent 安装命令。
3. 确认旧 Agent 已无法继续上报，且新 Agent 正常在线。

### Telegram Bot Token

怀疑泄露时，应在 BotFather 轮换 Token，然后在 Zeno 后台通知渠道里更新。公开 issue 里只写“已设置/未设置”，不要贴原文。

## 备份范围

至少备份：

```text
/opt/zeno/.env
/opt/zeno/docker-compose.yml
/opt/zeno/data/
/opt/zeno/secrets/
```

升级和重跑 `install.sh` 前，安装脚本会自动创建 `/opt/zeno/backups/install-YYYYmmdd-HHMMSS/`。恢复 SQLite 前请先停止 Controller。
