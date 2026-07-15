# Zeno

[![CI](https://github.com/shuijiao1/Zeno/actions/workflows/ci.yml/badge.svg)](https://github.com/shuijiao1/Zeno/actions/workflows/ci.yml)
[![Docker](https://github.com/shuijiao1/Zeno/actions/workflows/docker.yml/badge.svg)](https://github.com/shuijiao1/Zeno/actions/workflows/docker.yml)
[![Release](https://img.shields.io/github/v/release/shuijiao1/Zeno?color=2563eb)](https://github.com/shuijiao1/Zeno/releases)
[![License](https://img.shields.io/github/license/shuijiao1/Zeno)](LICENSE)

**Zeno 是一个轻量、自托管的服务器监控面板。**

它由 Controller 和 Agent 两部分组成：Controller 负责 Web 面板、API、SQLite 数据和通知；Agent 运行在服务器上，主动上报主机状态、流量和探测结果。Zeno 适合用来做一套清爽、可控、易部署的个人服务器在线状态页。

[简体中文](README.md) · [English](README.en.md)

---

## 特性

- **服务器状态总览**：在线状态、系统信息、CPU、内存、磁盘、负载、启动时间和 Agent 版本。
- **实时资源指标**：上下行速度、永久累计流量、CPU / 内存 / 磁盘占用和历史趋势。永久累计流量由 Controller 持久化，服务器或网卡重启后不会归零。
- **月流量统计**：按网卡 counter delta 计算，支持入站、出站、合计、取较大值和每台服务器独立重置日。
- **延迟与服务探测**：支持 ICMP Ping、TCP Ping 和 HTTP GET，可查看节点到目标的延迟、丢包和历史曲线。
- **公开状态页**：服务器卡片、顶部概览、节点详情和服务延迟详情，适配桌面与移动端。
- **后台管理**：服务器、探测目标、通知、外观、Agent 接入地址和账号密码管理。
- **Telegram 通知**：支持离线、资源阈值和测试通知，可按服务器范围生效。
- **Docker Compose 部署**：Controller 默认只监听本机 `127.0.0.1:18980`，适合放在 Caddy / Nginx 后面。

---

## 边界

Zeno 专注轻量监控，不做远控平台：

- 不提供远程终端、远程命令、文件管理或脚本任务。
- 不兼容 Nezha / Komari / Kulin 的 API、数据库或 Agent 协议。
- 不内置多租户、OAuth、复杂权限或通知模板系统。

---

## 快速安装 Controller

准备一台安装了 Docker 和 Docker Compose v2 的服务器，然后执行：

```bash
bash <(curl -fsSL https://zeno.shuijiao.de)
```

默认部署到：

```text
/opt/zeno/docker-compose.yml
/opt/zeno/.env
/opt/zeno/data/zeno.db
/opt/zeno/secrets/zeno_admin_token
/opt/zeno/secrets/zeno_agent_token
```

安装完成后，Controller 会监听：

```text
http://127.0.0.1:18980
```

> 不建议直接暴露 `18980`。请使用 Caddy / Nginx / 其他反代，把公网域名转发到 `127.0.0.1:18980`。

可选环境变量：

```bash
ZENO_INSTALL_DIR=/opt/zeno \
ZENO_HOST_PORT=18980 \
ZENO_IMAGE=ghcr.io/shuijiao1/zeno:v0.9.0 \
bash <(curl -fsSL https://zeno.shuijiao.de)
```

### Caddy 示例

```caddyfile
zeno.example.com {
    reverse_proxy 127.0.0.1:18980
}
```

---

## 安装 Agent

Agent 已拆分到独立仓库：[`shuijiao1/Zeno-Agent`](https://github.com/shuijiao1/Zeno-Agent)。

推荐方式：在 Zeno 后台创建服务器，选择 Linux / macOS / Windows，复制后台生成的安装命令到目标服务器执行。该命令会自动下载匹配系统和架构的 Agent Release；Linux 会安装 `zeno-agent.service`，macOS 会安装 LaunchDaemon，Windows 会安装 `zeno-agent` 服务。

手动安装示例：

```bash
ZENO_CONTROLLER_URL=https://zeno.example.com \
ZENO_NODE_ID=<node-id> \
ZENO_AGENT_TOKEN=<agent-token> \
ZENO_INSTALL_URL=https://zeno.shuijiao.de/agent/install.sh \
bash -o pipefail -c 'curl -fsSL "$ZENO_INSTALL_URL" | sudo env ZENO_CONTROLLER_URL="$ZENO_CONTROLLER_URL" ZENO_NODE_ID="$ZENO_NODE_ID" ZENO_AGENT_TOKEN="$ZENO_AGENT_TOKEN" bash'
```

Windows 需要在管理员 PowerShell 中执行后台生成的命令；macOS 需要具备 sudo 权限。

Agent 只负责上报，不会修改 Controller，也不会打开远程命令入口。

---

## 更新

完整升级和回滚说明见 [`docs/UPGRADE.md`](docs/UPGRADE.md)。

使用明确版本重新运行安全安装器；它会执行 provenance 校验、离线备份、SQLite 检查和失败自动恢复：

```bash
sudo env ZENO_IMAGE=ghcr.io/shuijiao1/zeno:v0.9.0 \
  bash -o pipefail -c 'curl -fsSL https://zeno.shuijiao.de | bash'
```

检查健康状态：

```bash
curl -fsS http://127.0.0.1:18980/ready
```

---

## 数据与安全

- SQLite 数据库默认位于 `/opt/zeno/data/zeno.db`。
- 管理员 token 和 Agent token 默认位于 `/opt/zeno/secrets/`，secret 文件应保持 `root:10001`、`0640`。
- 官方 Compose 以非 root UID/GID `10001:10001` 运行；`data/` 由该 UID/GID 持有，`secrets/` 由 root 持有并只向运行组开放只读权限，一键安装器会自动加固既有目录。
- 建议定期备份 `/opt/zeno/data` 和 `/opt/zeno/secrets`。
- Controller 默认本机监听；公网访问应通过 HTTPS 反向代理。
- 公网部署、token 轮换和安全边界见 [`docs/SECURITY.md`](docs/SECURITY.md)。

---

## 开发

```bash
go test ./...
npm --prefix web ci
npm --prefix web test -- --run
npm --prefix web run build
```

构建本地 Controller：

```bash
CGO_ENABLED=0 go build -o zeno-controller ./cmd/controller
```

---

## 技术栈

- Controller：Go + SQLite
- Agent：独立 Zeno-Agent release（Linux systemd / macOS LaunchDaemon / Windows service）
- Web：React + TypeScript + Vite
- 部署：Docker Compose
- 通信：Agent 主动 HTTPS/JSON 上报；受控网络可显式 opt-in 使用“直接 IP + 显式端口”HTTP，Public/Admin API 与 Agent API 分离

---

## License

MIT
