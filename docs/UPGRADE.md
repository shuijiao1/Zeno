# Upgrade and rollback / 升级与回滚

Zeno Controller 推荐使用 Docker Compose 部署。数据库迁移会在 Controller 启动时自动执行；升级前仍建议先备份数据和 secrets。

## 1. 备份

最小备份范围：

```text
/opt/zeno/.env
/opt/zeno/docker-compose.yml
/opt/zeno/data/
/opt/zeno/secrets/
```

如果使用 `install.sh` 重复安装或更新，脚本会在改写配置前停旧容器并创建一致性备份：

```text
/opt/zeno/backups/install-YYYYmmdd-HHMMSS/
```

备份包含 `.env`、`docker-compose.yml`、`data` 和 `secrets`；只保留最新 1 份。`data/` 权限为 `0700`，数据库/WAL/SHM 和 token 文件为 `0600`。脚本会通过 Controller 镜像执行 SQLite `quick_check`，启动后等待 `/ready`。

## 2. 使用明确版本更新

不要用不可回滚的 `latest` 流程。指定版本 tag 或 digest 后再更新：

```bash
sudo ZENO_IMAGE=ghcr.io/shuijiao1/zeno:latest bash install.sh
```

手工 Compose 更新也应先改 `.env` 中的 `ZENO_IMAGE` 为明确版本：

```bash
cd /opt/zeno
sed -i 's#^ZENO_IMAGE=.*#ZENO_IMAGE=ghcr.io/shuijiao1/zeno:<target-version>#' .env
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:18980/ready
```

## 3. 回滚

把 `.env` 里的镜像改回旧版本，然后重新拉起：

```bash
sed -i 's#^ZENO_IMAGE=.*#ZENO_IMAGE=ghcr.io/shuijiao1/zeno:<previous-version>#' /opt/zeno/.env
cd /opt/zeno
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:18980/ready
```

如果需要恢复配置或数据库，从安装脚本生成的备份目录复制回去：

```bash
cd /opt/zeno
docker compose stop zeno
rm -rf .env docker-compose.yml data secrets
cp -a backups/install-YYYYmmdd-HHMMSS/.env ./
cp -a backups/install-YYYYmmdd-HHMMSS/docker-compose.yml ./
cp -a backups/install-YYYYmmdd-HHMMSS/data ./
cp -a backups/install-YYYYmmdd-HHMMSS/secrets ./
chmod 700 data secrets
chmod 600 data/*.db data/*.db-wal data/*.db-shm secrets/* 2>/dev/null || true
docker compose up -d
```

> 回滚数据库前请先停止 Controller，避免运行中的 SQLite 文件被覆盖。

## 4. Agent 升级

Controller 升级通常不需要同步升级 Agent。只有 Release notes 明确说明 Agent 有新版本或协议变更时，才需要重新运行后台复制的 Zeno-Agent 安装命令。

Agent 安装器和多平台 release 来自 Zeno-Agent 仓库；公开安装命令一般不需要设置 `ZENO_AGENT_VERSION`。

## 5. 验证

```bash
docker inspect zeno --format 'image={{.Config.Image}} health={{.State.Health.Status}} version={{index .Config.Labels "org.opencontainers.image.version"}} revision={{index .Config.Labels "org.opencontainers.image.revision"}}'
curl -fsS http://127.0.0.1:18980/health
curl -fsS http://127.0.0.1:18980/ready
curl -fsS http://127.0.0.1:18980/api/public/v1/summary
```

期望：容器 healthy，`/health` 返回轻量存活状态，`/ready` 验证 SQLite 可读写，Public summary 能返回节点列表。
