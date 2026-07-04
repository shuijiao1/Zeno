# Release Packaging / 安装更新工具

Zeno 的 Linux 发布闭环由三个脚本和两个 systemd 模板组成：

```text
scripts/package-release.sh
scripts/deploy-local-release.sh
scripts/install-agent.sh
packaging/systemd/zeno-controller.service
packaging/systemd/zeno-agent.service
```

## 发布包结构

`package-release.sh` 生成：

```text
build/releases/zeno-<sha>-linux-amd64.tar.gz
└── zeno-<sha>-linux-amd64/
    ├── REVISION
    ├── zeno-controller
    ├── zeno-agent
    ├── web/
    ├── scripts/
    │   ├── deploy-local-release.sh
    │   └── install-agent.sh
    └── packaging/systemd/
        ├── zeno-controller.service
        └── zeno-agent.service
```

构建内容：

- `zeno-controller`：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`。
- `zeno-agent`：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`。
- `web/`：`npm --prefix web run build` 生成的静态文件。
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
  --controller-url http://127.0.0.1:18980 \
  --node-id hytron \
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
/etc/systemd/system/zeno-agent.service
```

token 文件处理规则：

- 已存在且非空：保留原值，只修正权限为 `0600`。
- 不存在或为空：生成随机值。
- token 不输出到终端或日志。

## 安全更新顺序

`deploy-local-release.sh` 固定按以下顺序执行：

1. 解压 release 到 `/opt/zeno/releases/<release>`。
2. 备份当前 systemd unit。
3. 渲染新 systemd unit。
4. 记录旧 `/opt/zeno/current`。
5. 停止 `zeno-agent.service`。
6. 切换 `current` symlink。
7. `systemctl daemon-reload`。
8. 重启 `zeno-controller.service`。
9. 等待 `/health` 成功。
10. Controller 健康后启动 `zeno-agent.service`。
11. 验证 Controller / Agent 均 active。

失败回滚：

- 如果 Controller 重启或 health check 失败，脚本会把 `current` 切回旧 release。
- 旧 release 没有新模板时，恢复部署前备份的 systemd unit。
- 回滚后重启旧 Controller，health 成功后再恢复 Agent。

## 单独安装 Agent

非 Controller 节点使用：

```bash
sudo scripts/install-agent.sh \
  --controller-url https://example.com \
  --node-id <node-id> \
  --token <agent-token>
```

或使用已存在 token 文件：

```bash
sudo scripts/install-agent.sh \
  --controller-url https://example.com \
  --node-id <node-id> \
  --token-file /opt/zeno/data/agent-token
```

`install-agent.sh` 只安装/重启 `zeno-agent.service`，不创建 Controller，不修改 `/opt/zeno/current`。

## 本地 dry-run

两个安装脚本都支持 `--dry-run`：

```bash
scripts/deploy-local-release.sh \
  --archive build/releases/zeno-<sha>-linux-amd64.tar.gz \
  --install-dir /tmp/zeno-dry-run \
  --dry-run
```

`--dry-run` 会解包、创建 token 文件、渲染 unit 到 `<install-dir>/systemd-dry-run/`，但不会调用 `systemctl`，也不会切换真实服务。
