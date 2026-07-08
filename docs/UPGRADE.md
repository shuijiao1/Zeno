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

如果使用 `install.sh` 重复安装或更新，脚本会在改写配置前创建：

```text
/opt/zeno/backups/install-YYYYmmdd-HHMMSS/
```

备份包含 `.env`、`docker-compose.yml`、`data` 和 `secrets`。脚本不会删除或清空现有 SQLite 数据库。

## 2. 使用 latest 更新

```bash
cd /opt/zeno
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:18980/health
```

## 3. 固定版本

```bash
sed -i 's#^ZENO_IMAGE=.*#ZENO_IMAGE=ghcr.io/shuijiao1/zeno:v0.2.3#' /opt/zeno/.env
cd /opt/zeno
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:18980/health
```

## 4. 回滚

把 `.env` 里的镜像改回旧版本，然后重新拉起：

```bash
sed -i 's#^ZENO_IMAGE=.*#ZENO_IMAGE=ghcr.io/shuijiao1/zeno:v0.2.2#' /opt/zeno/.env
cd /opt/zeno
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:18980/health
```

如果需要恢复配置或数据库，从安装脚本生成的备份目录复制回去：

```bash
cd /opt/zeno
docker compose down
cp -a backups/install-YYYYmmdd-HHMMSS/.env ./
cp -a backups/install-YYYYmmdd-HHMMSS/docker-compose.yml ./
cp -a backups/install-YYYYmmdd-HHMMSS/data ./
cp -a backups/install-YYYYmmdd-HHMMSS/secrets ./
docker compose up -d
```

> 回滚数据库前请先停止 Controller，避免运行中的 SQLite 文件被覆盖。

## 5. Agent 升级

Controller 升级通常不需要同步升级 Agent。只有 Release notes 明确说明 Agent 有新版本或协议变更时，才需要重新运行后台复制的 Agent 安装命令。

Agent 安装脚本默认使用 Zeno-Agent 最新 Release；如需固定版本，可设置 `ZENO_AGENT_VERSION=v0.1.11`。

## 6. 验证

```bash
docker inspect zeno --format 'image={{.Config.Image}} health={{.State.Health.Status}} version={{index .Config.Labels "org.opencontainers.image.version"}} revision={{index .Config.Labels "org.opencontainers.image.revision"}}'
curl -fsS http://127.0.0.1:18980/health
curl -fsS http://127.0.0.1:18980/api/public/v1/summary
```

期望：容器 healthy，`/health` 返回 `{"ok":true}`，Public summary 能返回节点列表。
