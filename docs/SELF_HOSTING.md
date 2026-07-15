# Self-hosting / 自部署指南

Zeno Controller 官方自部署方式是 Docker Compose 一键安装器。Controller 包含 Go API、SQLite 数据库和静态 Web UI；Agent 由独立的 Zeno-Agent 仓库发布。

## 1. 安装 Controller

准备一台已安装 Docker 和 Docker Compose v2 的 Linux 服务器，执行：

```bash
sudo bash -o pipefail -c 'curl -fsSL https://zeno.shuijiao.de | bash'
```

默认目录：

```text
/opt/zeno/.env
/opt/zeno/docker-compose.yml
/opt/zeno/data/zeno.db
/opt/zeno/secrets/
/opt/zeno/backups/
```

默认监听 `127.0.0.1:18980`，Controller 容器以固定非 root 用户 `10001:10001` 运行。不要把该端口直接暴露到公网；请通过 Caddy、Nginx 或其他 HTTPS 反向代理提供访问。

指定安装目录、端口或明确镜像版本时，把变量传给安装器：

```bash
sudo env \
  ZENO_INSTALL_DIR=/opt/zeno \
  ZENO_HOST_PORT=18980 \
  ZENO_IMAGE=ghcr.io/shuijiao1/zeno:vX.Y.Z \
  bash -o pipefail -c 'curl -fsSL https://zeno.shuijiao.de | bash'
```

重复运行同一安装器会执行镜像 provenance 校验、停服前预检、一致性离线备份、SQLite `quick_check`、原子配置替换、readiness 检查和失败自动恢复。升级与恢复细节见 [`UPGRADE.md`](UPGRADE.md)。

## 2. 配置 HTTPS 入口

Caddy 示例：

```caddyfile
zeno.example.com {
    reverse_proxy 127.0.0.1:18980
}
```

验证 Controller：

```bash
curl -fsS http://127.0.0.1:18980/health
curl -fsS http://127.0.0.1:18980/ready
curl -fsS http://127.0.0.1:18980/api/public/v1/summary
```

## 3. 登录 Admin

首次安装的 bootstrap Admin 凭据保存在：

```bash
sudo cat /opt/zeno/secrets/zeno_admin_token
```

打开 `/dashboard`，使用账号 `admin` 和 bootstrap 凭据登录。登录后建议在后台“账户”页修改账号和密码。

Admin API 只接受请求头：

```http
X-Admin-Token: <session-token>
```

不要把 Admin/session token 放进 URL query string、日志或 issue。

## 4. 配置 Agent 接入 URL

在后台“设置”中填写 Agent 能访问的 HTTPS 地址：

```text
https://zeno.example.com
```

该地址会写入后台生成的 Agent 安装命令。远程 Agent 默认必须使用 HTTPS；为兼容没有反向代理的受控网络，`http://<直接 IP>:<显式端口>` 也可使用，Agent 安装器会在服务配置中持久化显式 insecure opt-in，并警告 enrollment/runtime bearer token 将以明文传输。主机名 HTTP、没有显式端口的远程 HTTP 仍会被拒绝；loopback HTTP 可正常使用。URL 不得包含用户名、密码、query 或 fragment。

Zeno 不自动修改 DNS、反向代理或防火墙。

## 5. 添加服务器并安装 Agent

1. 在后台“服务器”中添加服务器。
2. 填写名称、地区、账单周期、流量口径、月重置日和配额等展示字段。
3. 打开服务器编辑弹窗，点击“复制安装命令”。
4. 选择 Linux、macOS 或 Windows，并在目标机器执行命令。

Agent 安装器和多平台 release 来自独立的 [Zeno-Agent](https://github.com/shuijiao1/Zeno-Agent) 仓库。Linux Agent 使用其自己的 `zeno-agent.service`；macOS 使用 LaunchDaemon；Windows 使用系统服务。

## 6. 验证 Agent

Linux Agent 示例：

```bash
systemctl is-active zeno-agent.service
journalctl -u zeno-agent.service --since '5 minutes ago' --no-pager
```

Public summary 中新服务器应从 `no_data` 变为 `online` 或 `warning`。

## 7. 数据、权限与备份

最小备份范围：

```text
/opt/zeno/.env
/opt/zeno/docker-compose.yml
/opt/zeno/data/
/opt/zeno/secrets/
```

安装器默认保留最近 5 份完整安装备份。`data/` 由运行 UID/GID `10001:10001` 持有；`secrets/` 目录应为 `root:10001`、模式 `0750`，secret 文件应为 `root:10001`、模式 `0640`。不要把 secrets 改为运行用户所有。

## 8. 产品边界

当前版本不提供远程终端、文件管理、远程命令执行或脚本任务，也不提供 Nezha、Komari、Kulin 的兼容 API。
