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
- **Live resource metrics**: upload/download speed, controller-persisted lifetime traffic, CPU / memory / disk usage and history. Lifetime traffic survives server and interface restarts.
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
bash <(curl -fsSL https://zeno.shuijiao.de)
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
bash <(curl -fsSL https://zeno.shuijiao.de)
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
ZENO_CONTROLLER_URL=https://zeno.example.com \
ZENO_NODE_ID=<node-id> \
ZENO_AGENT_TOKEN=<agent-token> \
ZENO_INSTALL_URL=https://zeno.shuijiao.de/agent/install.sh \
bash -o pipefail -c 'curl -fsSL "$ZENO_INSTALL_URL" | sudo env ZENO_CONTROLLER_URL="$ZENO_CONTROLLER_URL" ZENO_NODE_ID="$ZENO_NODE_ID" ZENO_AGENT_TOKEN="$ZENO_AGENT_TOKEN" bash'
```

Run the Windows command from an elevated PowerShell window. The macOS command requires sudo privileges.

The Agent only reports metrics and probe results. It does not modify the Controller or expose a remote command channel.

---

## Update

See [`docs/UPGRADE.md`](docs/UPGRADE.md) for upgrade and rollback notes.

Run the safety installer again with an explicit version. It verifies provenance, creates an offline backup, checks SQLite, and automatically restores a failed upgrade:

```bash
sudo env ZENO_IMAGE=ghcr.io/shuijiao1/zeno:latest \
  bash -o pipefail -c 'curl -fsSL https://zeno.shuijiao.de | bash'
```

Health check:

```bash
curl -fsS http://127.0.0.1:18980/ready
```

---

## Data and security

- SQLite database: `/opt/zeno/data/zeno.db`.
- Admin and Agent tokens: `/opt/zeno/secrets/`; secret files should remain `root:10001` with mode `0640`.
- The official Compose stack runs as non-root UID/GID `10001:10001`. `data/` is owned by that UID/GID; `secrets/` remains root-owned and read-only to the runtime group. The installer hardens existing directories automatically.
- Back up `/opt/zeno/data` and `/opt/zeno/secrets` regularly.
- Keep the Controller bound locally and expose it through an HTTPS reverse proxy.
- See [`docs/SECURITY.md`](docs/SECURITY.md) for public deployment, token rotation and security boundaries.

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
- Agent: separate Zeno-Agent release (Linux systemd / macOS LaunchDaemon / Windows service)
- Web: React + TypeScript + Vite
- Deployment: Docker Compose
- Communication: agent-initiated HTTPS/JSON reporting; controlled networks may explicitly opt in to direct-IP HTTP with an explicit port, with separate Public/Admin/Agent APIs

---

## License

MIT
