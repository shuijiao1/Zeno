# Zeno

[![CI](https://github.com/shuijiao1/Zeno/actions/workflows/ci.yml/badge.svg)](https://github.com/shuijiao1/Zeno/actions/workflows/ci.yml)
[![Docker](https://github.com/shuijiao1/Zeno/actions/workflows/docker.yml/badge.svg)](https://github.com/shuijiao1/Zeno/actions/workflows/docker.yml)
[![Release](https://img.shields.io/github/v/release/shuijiao1/Zeno?color=2563eb)](https://github.com/shuijiao1/Zeno/releases)
[![License](https://img.shields.io/github/license/shuijiao1/Zeno)](LICENSE)

**Zeno is a lightweight, self-hosted server monitoring dashboard.**

It has two parts: a Controller for the web UI, APIs, SQLite storage and notifications, and an Agent that runs on each server to report host metrics, traffic and probe results. Zeno is designed for a clean personal server status page that is easy to deploy and operate.

[简体中文](README.md) · [English](README.en.md)

---

## Features

- **Server overview**: online status, OS info, CPU, memory, disk, load, uptime and Agent version.
- **Live resource metrics**: upload/download speed, traffic counters, CPU / memory / disk usage and history.
- **Monthly traffic accounting**: calculated from network counter deltas, with per-node reset day and billing mode.
- **Latency and service probes**: ICMP Ping, TCP Ping and HTTP GET with latency, packet loss and history charts.
- **Public status page**: server cards, top summary, node details and service latency details for desktop and mobile.
- **Admin dashboard**: manage servers, probe targets, notifications, branding, Agent endpoint and account settings.
- **Telegram notifications**: offline/recovery, resource threshold and test notifications with per-server scope.
- **Docker Compose deployment**: Controller binds to `127.0.0.1:18980` by default and is intended to sit behind Caddy or Nginx.

---

## Scope

Zeno focuses on lightweight monitoring. It is not a remote-control platform:

- No remote terminal, remote command execution, file manager or script runner.
- No compatibility layer for Nezha, Komari or Kulin APIs, databases or Agent protocols.
- No built-in multi-tenancy, OAuth, complex RBAC or notification template system.

---

## Install Controller

Prepare a server with Docker and Docker Compose v2, then run:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/shuijiao1/Zeno/main/install.sh)
```

Default layout:

```text
/opt/zeno/docker-compose.yml
/opt/zeno/.env
/opt/zeno/data/zeno.db
/opt/zeno/secrets/zeno_admin_token
/opt/zeno/secrets/zeno_agent_token
```

The Controller listens on:

```text
http://127.0.0.1:18980
```

> Do not expose `18980` directly. Put it behind Caddy, Nginx or another HTTPS reverse proxy.

Optional environment variables:

```bash
ZENO_INSTALL_DIR=/opt/zeno \
ZENO_HOST_PORT=18980 \
ZENO_IMAGE=ghcr.io/shuijiao1/zeno:latest \
bash <(curl -fsSL https://raw.githubusercontent.com/shuijiao1/Zeno/main/install.sh)
```

### Caddy example

```caddyfile
zeno.example.com {
    reverse_proxy 127.0.0.1:18980
}
```

---

## Install Agent

The Agent is maintained in a separate repository: [`shuijiao1/Zeno-Agent`](https://github.com/shuijiao1/Zeno-Agent).

Recommended flow: create a server in the Zeno admin dashboard, choose Linux / macOS / Windows, and run the generated install command on the target server. The command downloads the matching Agent release; Linux installs `zeno-agent.service`, macOS installs a LaunchDaemon, and Windows installs the `zeno-agent` service.

Manual example:

```bash
curl -fsSL https://raw.githubusercontent.com/shuijiao1/Zeno-Agent/main/install.sh | sudo env \
  ZENO_CONTROLLER_URL=https://zeno.example.com \
  ZENO_NODE_ID=<node-id> \
  ZENO_AGENT_TOKEN=<agent-token> \
  ZENO_AGENT_VERSION=v0.1.0 \
  bash
```

Run the Windows command from an elevated PowerShell window. The macOS command requires sudo privileges.

The Agent only reports metrics and probe results. It does not modify the Controller or expose a remote command channel.

---

## Update

For Docker Compose deployments:

```bash
cd /opt/zeno
docker compose pull
docker compose up -d
```

To pin a specific version:

```bash
sed -i 's#^ZENO_IMAGE=.*#ZENO_IMAGE=ghcr.io/shuijiao1/zeno:v0.1.3#' /opt/zeno/.env
cd /opt/zeno
docker compose pull
docker compose up -d
```

Health check:

```bash
curl -fsS http://127.0.0.1:18980/health
```

---

## Data and security

- SQLite database: `/opt/zeno/data/zeno.db`.
- Admin and Agent tokens: `/opt/zeno/secrets/`, expected to stay `600`.
- Back up `/opt/zeno/data` and `/opt/zeno/secrets` regularly.
- Keep the Controller bound locally and expose it through an HTTPS reverse proxy.

---

## Development

```bash
go test ./...
npm --prefix web ci
npm --prefix web test -- --run
npm --prefix web run build
```

Build a local Controller binary:

```bash
CGO_ENABLED=0 go build -o zeno-controller ./cmd/controller
```

---

## Stack

- Controller: Go + SQLite
- Agent: Go + systemd
- Web: React + TypeScript + Vite
- Deployment: Docker Compose
- Communication: Agent-initiated HTTPS/JSON reporting, with separate Public/Admin/Agent APIs

---

## License

MIT
