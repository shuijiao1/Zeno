# Self-hosting / 自部署指南

这份文档描述从源码打包、安装 Controller、配置 Agent 接入 URL、再安装 Agent 的最小闭环。Zeno 保持轻量：一个 Go Controller、一个 SQLite 数据库、一个 Go Agent、一个静态 Web UI。

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
- `zeno-agent`
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
  --controller-url http://127.0.0.1:18980 \
  --node-id hytron \
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

更新脚本的安全顺序固定为：停止 Agent → 切换 `current` → 重启 Controller → 等 `/health` → 启动 Agent。Controller health 失败会回滚到旧 release。

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
4. 点击“轮换并生成安装命令”。
5. 按目标系统选择 Linux / macOS / Windows。
6. 复制命令到目标服务器执行；Windows 需要管理员 PowerShell，macOS 需要 sudo 权限。

注意：生成安装命令会轮换该服务器的 Agent token。已经在线的服务器必须执行新命令后才会继续上报。

单独安装脚本也可直接使用：

```bash
sudo scripts/install-agent.sh \
  --controller-url https://zeno.example.com \
  --node-id <node-id> \
  --token <agent-token>
```

## 6. 验证

Controller：

```bash
curl -fsS http://127.0.0.1:18980/health
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

- `/health` 返回 `{"ok":true}`。
- Controller / Agent 都是 `active`。
- Agent 日志出现 `reported host/state and ... probe target(s)`。
- Public summary 中新服务器从 `no_data` 变为 `online` 或 `warning`。

## 7. 备份和恢复

最小备份范围：

```text
/opt/zeno/data/zeno.db
/opt/zeno/data/admin-token
/opt/zeno/data/agent-token
```

建议在升级前备份 `/opt/zeno/data/`。恢复时保持文件权限只允许服务用户/root 读取 token 文件。

## 8. 当前边界

当前版本不包含：

- 远程终端、文件管理、远程命令执行、脚本任务。
- Nezha / Komari / Kulin 兼容层。
- 多渠道通知、Webhook、自定义通知模板、通知组。
- 服务器分组、备注。
- Logo/背景图只使用 URL 或站内静态路径。
