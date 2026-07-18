# Upgrade and rollback / 升级与回滚

Zeno Controller 推荐并支持通过仓库一键安装器管理 Docker Compose 部署。数据库迁移会在 Controller 启动时自动执行；升级必须同时保护 SQLite 数据和文件型 secrets。

## 1. 安装器备份

重复运行 `install.sh` 时，安装器会在修改当前安装前完成停服前路径/磁盘/镜像预检，然后停止旧容器并创建一致性离线备份：

```text
/opt/zeno/backups/install-YYYYmmdd-HHMMSS-PID/
```

备份包含 `.env`、`docker-compose.yml`、`data/` 和 `secrets/`，并带完整性 marker 和 SHA-256 manifest。默认保留最近 **5** 份完整安装备份，可通过 `ZENO_BACKUP_KEEP_COUNT` 调整。安装器会在备份副本和当前数据库上执行 SQLite `quick_check`，启动后等待 `/ready`；失败时使用固定的旧镜像 ID 和完整备份自动恢复。

生产环境仍应把这些目录纳入独立的定期异机备份。

## 2. 使用明确版本升级

先从 Release notes 选择明确的 SemVer tag 或镜像 digest。不要以 mutable tag 作为升级或回滚目标。

```bash
sudo env ZENO_IMAGE=ghcr.io/shuijiao1/zeno:vX.Y.Z \
  bash -o pipefail -c 'curl -fsSL https://zeno.shuijiao.de | bash'
```

将示例版本替换为目标版本。官方镜像安装默认校验 GitHub build provenance；不要用手工 `docker compose pull/up` 绕过安装器的备份、digest 固定、attestation 和自动恢复流程。

升级后验证：

```bash
docker inspect zeno --format 'image={{.Config.Image}} health={{.State.Health.Status}} version={{index .Config.Labels "org.opencontainers.image.version"}} revision={{index .Config.Labels "org.opencontainers.image.revision"}}'
curl -fsS http://127.0.0.1:18980/health
curl -fsS http://127.0.0.1:18980/ready
curl -fsS http://127.0.0.1:18980/api/public/v1/summary
```

## 3. 回滚

如果刚执行的升级未通过 readiness，安装器会自动恢复旧配置、数据、secrets 和升级前固定的镜像。

需要人工恢复历史备份时：

1. 确认备份目录包含 `.zeno-backup-complete`，并在备份目录运行 `sha256sum -c MANIFEST.sha256`。
2. 停止 Controller，另存当前失败现场。
3. 原样恢复该备份中的 `.env`、`docker-compose.yml`、`data/` 和 `secrets/`。
4. 恢复权限：`data/` 归 `10001:10001`；`secrets/` 归 `root:10001`，目录模式 `0750`、文件模式 `0640`。
5. 以备份 `BACKUP_INFO` 中记录的不可变 rollback image reference 启动，并验证 `/ready`。

权限修复示例（仅在确认恢复内容和路径后执行）：

```bash
cd /opt/zeno
chown -R 10001:10001 data
find data -type d -exec chmod 700 {} +
find data -type f \( -name '*.db' -o -name '*.db-wal' -o -name '*.db-shm' -o -name '*-wal' -o -name '*-shm' \) -exec chmod 600 {} +
chown -R 0:10001 secrets
find secrets -type d -exec chmod 750 {} +
find secrets -type f -exec chmod 640 {} +
```

> 回滚数据库前必须停止 Controller。不要覆盖运行中的 SQLite 文件，也不要把 `secrets/` chown 给容器运行 UID。

## 4. Agent 升级

Controller 升级通常不要求同步升级 Agent。只有 Release notes 明确说明 Agent 协议或功能要求时，才重新运行后台生成的 Zeno-Agent 安装命令。

Agent 安装器和多平台 release 来自独立 Zeno-Agent 仓库；公开安装命令通常不需要设置 `ZENO_AGENT_VERSION`。

## 5. 安装器版本与校验边界

默认 `https://zeno.shuijiao.de` 是发布方维护的便利入口；镜像版本可以固定，但该 URL 当前没有由仓库独立控制的 `/vX.Y.Z` 安装器路由。仓库 tag 的 raw URL 可作为审计/下载来源，例如：

```text
https://raw.githubusercontent.com/shuijiao1/Zeno/vX.Y.Z/install.sh
```

tag URL 的稳定性依赖已发布 tag 不被移动；HTTPS 只保护传输，仓库当前也没有为 `install.sh` 单独发布签名。因此 raw URL **不是默认一键命令**。在高保证环境中，先从 tag 下载到本地、人工审查并按独立可信渠道记录的 SHA-256 校验后再以 root 执行。要提供第一方、版本化且签名/摘要可验证的短 URL，仍需发布入口（Cloudflare Worker）增加不可变版本路由和校验元数据；这不能只靠本仓库文档完成。

## English summary

Use the installer for upgrades; it creates a stopped, complete backup, checks SQLite, pins the previous image ID, waits for readiness, and restores the complete snapshot on failure. Upgrade and rollback to an immutable `vX.Y.Z` or digest—not `latest`—and restore `.env`, Compose, `data/`, and `secrets/` from the same backup. Raw GitHub tag URLs are useful for audit/download but are not the default one-liner and do not provide a separate installer signature. A first-party immutable versioned short URL remains blocked on the Cloudflare Worker publishing layer.
