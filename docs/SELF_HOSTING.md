# Self-hosting / 自部署指南

这份文档描述从源码打包、安装 Controller、配置 Agent 接入 URL、再安装 Agent 的最小闭环。Zeno 保持轻量：一个 Go Controller、一个 SQLite 数据库、一个静态 Web UI；Agent 由独立的 Zeno-Agent 仓库发布。

## 1. 构建发布包

在构建机仓库根目录执行：

```bash
scripts/package-release.sh
```

发布包输出到：

```text
build/releases/zeno-<sha>-linux-amd64.tar.gz
```

包内包含：

- `zeno-controller`
- `web/`
- `scripts/`
- `packaging/systemd/`
- `README.md`
- `docs/`
- `REVISION`

正式部署前不要跳过测试；`--skip-tests` 只适合验证打包流程。

## 2. 安装 / 更新 Controller

把发布包上传到 Controller 机器，然后执行：

```bash
sudo scripts/deploy-local-release.sh \
  --archive /tmp/zeno-<sha>-linux-amd64.tar.gz \
  --install-dir /opt/zeno \
  --controller-addr 0.0.0.0:18980 \
  --seed-preview
```

默认目录：

```text
/opt/zeno/current
/opt/zeno/releases
/opt/zeno/data/zeno.db
/opt/zeno/data/admin-token
/opt/zeno/data/agent-token
```

更新脚本的安全顺序固定为：切换 `current` → 重启 Controller → 等 `/ready`。Controller readiness 失败会回滚到旧 release。

## 3. 登录 Admin

首次部署的 bootstrap Admin 密码默认保存在 Controller 机器：

```bash
sudo cat /opt/zeno/data/admin-token
```

打开 `/dashboard`，使用账号 `admin` 和上面的 bootstrap 密码登录。登录后建议进入后台“账户”页修改账号和密码；修改后旧 bootstrap token 不再作为后台 API 凭据，系统会使用数据库里的账号/密码和 session。

Admin API 只接受请求头：

```http
X-Admin-Token: <session-token>
```

不要把 Admin/session token 放在 URL query string 里。

## 4. 配置 Agent 接入 URL

如果只在 Controller 本机预览，可以留空。

准备给其它服务器安装 Agent 前，在后台“设置”里填写：

```text
Agent 接入 URL = https://zeno.example.com
```

这个 URL 会写进后台生成的 Agent 安装命令：

- Agent 二进制下载地址
- `-controller-url`

要求：

- 允许 `http://` 或 `https://`。
- 必须能被目标 Agent 服务器访问。
- 不允许用户名密码、query、fragment。
- 建议正式使用公网 HTTPS。

Zeno 不自动改 DNS、Caddy、Nginx 或防火墙；公网入口由部署者按自己的基础设施配置。

## 5. 添加服务器并安装 Agent

后台流程：

1. 在“服务器”里添加服务器。
2. 填名称、地区、账单周期、流量口径、月重置日、配额等展示字段。
3. 打开该服务器编辑弹窗。
4. 点击“复制安装命令”。
5. 按目标系统选择 Linux / macOS / Windows。
6. 复制命令到目标服务器执行；Windows 需要管理员 PowerShell，macOS 需要 sudo 权限。

注意：复制安装命令会复用该服务器已保存的 Agent token；只有旧数据没有可复用 token 时，首次复制才会生成一个随机 token。

Agent 安装器和多平台 release 来自 Zeno-Agent 仓库；后台生成的命令会下载匹配系统和架构的 Agent release。

## 6. 验证

Controller：

```bash
curl -fsS http://127.0.0.1:18980/health
curl -fsS http://127.0.0.1:18980/ready
systemctl is-active zeno-controller.service
```

Agent：

```bash
systemctl is-active zeno-agent.service
journalctl -u zeno-agent.service --since '5 minutes ago' --no-pager
```

Admin/API：

```bash
ADMIN_TOKEN=$(sudo cat /opt/zeno/data/admin-token)
curl -fsS -H "X-Admin-Token: $ADMIN_TOKEN" http://127.0.0.1:18980/api/admin/v1/nodes
curl -fsS http://127.0.0.1:18980/api/public/v1/summary
```

期望看到：

- `/health` 返回轻量存活状态；`/ready` 验证 SQLite 可读写。
- Controller 是 `active`，Agent 服务在目标节点正常运行。
- Zeno-Agent 日志出现上报 host/state/probe target 的记录。
- Public summary 中新服务器从 `no_data` 变为 `online` 或 `warning`。

## 7. 备份和恢复

最小备份范围：

```text
/opt/zeno/data/zeno.db
/opt/zeno/data/admin-token
/opt/zeno/data/agent-token
```

建议用 SQLite 一致性备份，或停 Controller 后备份 `/opt/zeno/data/`。恢复时保持目录 `0700`、数据库/WAL/SHM 和 token 文件 `0600`。

## 8. 当前边界

当前版本不包含：

- 远程终端、文件管理、远程命令执行、脚本任务。
- Nezha / Komari / Kulin 兼容层。
- 多渠道通知、Webhook、自定义通知模板、通知组。
- 服务器分组、备注。
- Logo/背景图只使用 URL 或站内静态路径。

## Docker Compose 一键安装

公开部署推荐优先使用仓库根目录的 `install.sh`：

```bash
bash <(curl -fsSL https://zeno.shuijiao.de)
```

脚本行为：

- 默认部署到 `/opt/zeno`。
- 默认监听 `127.0.0.1:18980`。
- 重复执行时会保留现有 data/secrets，并在改写配置前备份 `.env`、`docker-compose.yml`、`data`、`secrets` 到 `/opt/zeno/backups/install-YYYYmmdd-HHMMSS/`。
- 如果已有 `.env`，未显式传入的 `ZENO_IMAGE`、`ZENO_HOST_PORT`、`ZENO_CONTAINER_NAME`、`TZ` 会沿用旧值。
- `/health` 未通过时会打印 `docker compose ps` 和最近日志，并提示备份目录。

详细升级和回滚见 [`UPGRADE.md`](UPGRADE.md)。
