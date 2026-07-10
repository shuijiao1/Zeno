# Release Packaging / 安装更新工具

Zeno Controller 的 Linux 发布闭环由两个脚本和一个 systemd 模板组成：

```text
scripts/package-release.sh
scripts/deploy-local-release.sh
scripts/import-guko-servers.py
packaging/systemd/zeno-controller.service
```

> Standalone Agent 已拆分到 Zeno-Agent 仓库发布；本仓库不再构建或打包 `cmd/agent` / `internal/agent`。

## 发布包结构

`package-release.sh` 生成：

```text
build/releases/zeno-<sha>-linux-amd64.tar.gz
└── zeno-<sha>-linux-amd64/
    ├── REVISION
    ├── zeno-controller
    ├── README.md
    ├── docs/
    ├── web/
    ├── scripts/
    │   ├── deploy-local-release.sh
    │   └── import-guko-servers.py
    └── packaging/systemd/
        └── zeno-controller.service
```

构建内容：

- `zeno-controller`：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`。
- `web/`：`npm --prefix web run build` 生成的静态文件。
- `README.md` / `docs/`：自部署、API、数据模型、安全边界和发布工具文档。
- `REVISION`：当前 Git short SHA 或 `--sha` 指定值。

默认打包前会执行：

```bash
go test ./...
npm --prefix web test -- --run
npm --prefix web run build
```

需要只验证打包流程时可使用 `--skip-tests`，但正式发布/部署前仍必须跑完整测试。默认不允许 dirty working tree，避免 `REVISION` 与内容不一致；临时验证可显式加 `--allow-dirty`。

## Controller 安装 / 更新

目标机执行：

```bash
sudo scripts/deploy-local-release.sh \
  --archive /tmp/zeno-<sha>-linux-amd64.tar.gz \
  --install-dir /opt/zeno \
  --controller-addr 0.0.0.0:18980 \
  --seed-preview
```

默认路径：

```text
/opt/zeno/releases/
/opt/zeno/current
/opt/zeno/data/zeno.db
/opt/zeno/data/agent-token
/opt/zeno/data/admin-token
/etc/systemd/system/zeno-controller.service
```

可选 `--agent-binary <path>` / `--agent-version <version>` 只用于让 Controller 后台继续提供外部 Zeno-Agent 的下载入口；Agent 本身不再由本仓库 release 包管理。

token 文件处理规则：

- 已存在且非空：保留原值，只修正权限为 `0600`。
- 不存在或为空：生成随机值。
- token 不输出到终端或日志。
- `admin-token` 是首次登录 bootstrap 密码；后台“账户”页修改账号或密码后，Controller 会使用 SQLite 中的 `admin_username` / `admin_password_hash` 和 session，旧 bootstrap token 不再作为后台 API 凭据。

## 安全更新顺序

`deploy-local-release.sh` 固定按以下顺序执行：

1. 解压 release 到 `/opt/zeno/releases/<release>`。
2. 备份当前 systemd unit。
3. 渲染新 systemd unit。
4. 记录旧 `/opt/zeno/current`。
5. 切换 `current` symlink。
6. `systemctl daemon-reload`。
7. 重启 `zeno-controller.service`。
8. 等待 `/ready` 成功。
9. 验证 Controller active。

失败回滚：

- 如果 Controller 重启或 readiness check 失败，脚本会把 `current` 切回旧 release。
- 旧 release 没有新模板时，恢复部署前备份的 systemd unit。
- 回滚后重启旧 Controller 并等待 `/ready`。

## 单独安装 Agent

非 Controller 节点请使用 Zeno-Agent 仓库发布的安装命令。Zeno 后台“设置”里的 `Agent 接入 URL` 会影响后台生成的安装命令；准备给其它服务器安装 Agent 前，应先把它设为可被目标服务器访问的公网 HTTPS Controller 地址。

## GUKO 服务器清单导入

`scripts/import-guko-servers.py` 可把 `server-manager/servers.json` 同步到 Zeno Admin nodes：

```bash
python3 scripts/import-guko-servers.py \
  --servers-json /path/to/server-manager/servers.json \
  --controller-url http://127.0.0.1:18980 \
  --admin-token-file /opt/zeno/data/admin-token
```

默认 dry-run；确认后加 `--apply`。脚本只创建/更新节点展示元数据，不删除节点，不调用 install-command，不轮换 Agent token。

## 本地 dry-run

```bash
scripts/deploy-local-release.sh \
  --archive build/releases/zeno-<sha>-linux-amd64.tar.gz \
  --install-dir /tmp/zeno-dry-run \
  --dry-run
```

`--dry-run` 会解包、创建 token 文件、渲染 unit 到 `<install-dir>/systemd-dry-run/`，但不会调用 `systemctl`，也不会切换真实服务。
