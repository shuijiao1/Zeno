# Zeno

Zeno 是一个从零实现的轻量服务器探针 / 在线监控系统。

## 核心原则

- 全新实现：新 Agent、新 Controller、新数据库、新 API、新前端、新部署方式。
- 不兼容旧系统：不兼容 Kulin / Nezha / Komari 的 API、DB、Agent 协议或安装方式。
- 展示效果锁定：首页大卡片、延迟统计口径、延迟图表风格必须保持当前已确认效果。
- 轻量优先：优先做稳定在线监控、主机状态、月流量、延迟/HTTP 探测和必要后台管理；避免 Nezha 级远控复杂度。

## 当前已实现

- Controller：Go + SQLite，提供 Agent / Public / Admin API。
- Agent：独立 Go 二进制，主动上报 heartbeat、host、state、probe results。
- Host 基础信息：系统、内核、架构、CPU、内存、磁盘、启动时间、Agent 版本。
- State 实时状态与历史：CPU、内存、磁盘、网络累计流量、上下行速度、uptime、load、swap、进程数、TCP 连接数。
- 月流量：按网卡累计 counter 的 delta 计算，不用速度积分；支持入站/出站/合计/取较大值和每台服务器独立重置日。
- 延迟/服务探测：`tcping`、`ping`/ICMP、`http_get`，每轮保留 summary 和 raw samples。
- Public API：主页 summary、节点延迟、节点状态历史、公开外观设置；外观图片只配置 URL / 站内静态路径。
- Web UI：主页卡片、节点详情页、延迟图、资源历史图、移动端紧凑布局。
- Admin API/UI：服务器、服务器排序、探针目标、探针目标排序、节点分配、通知、状态规则、当前异常、数据维护、外观设置。
- 服务器元数据：到期日、账单周期、显示顺序（后台可上移/下移/整理）、国家码/国旗、公网 IPv4、公网 IPv6；Agent 可 best-effort 自动识别公网 IPv4 / IPv6 / 国家码。
- 通知：Telegram-only 渠道，支持上线、离线、异常、测试发送；发送记录脱敏。
- 状态规则：CPU / 内存 / 磁盘 / 探测延迟 / 丢包 / 离线 / 恢复，支持按服务器范围生效。
- 数据维护：retention 设置、候选清理统计、dry-run、确认清理。
- 发布工具：Linux amd64 release 打包、systemd 模板、本机安装/更新脚本、GUKO 服务器清单导入脚本、Controller health 失败回滚。
- Hytron 预览部署：`/opt/zeno`，`zeno-controller.service`，`zeno-agent.service`，端口 `18980`。

## 当前不做

- 远程终端、文件管理、远程命令执行、脚本任务。
- NAT、DDNS、多租户、OAuth/复杂角色。
- 旧系统迁移兼容或旧 Agent 协议适配。
- Nezha/Komari/Kulin API、DB 或插件兼容层。
- 多渠道通知、通用 HTTP 通知、自定义 headers/body、通知模板、通知组。
- 服务器分组、备注，以及服务监控状态页/历史页。

## 安装 Controller

先在构建机打包：

```bash
scripts/package-release.sh
```

脚本会输出 `build/releases/zeno-<sha>-linux-amd64.tar.gz`，包内包含 Controller、Agent、Web 静态文件、`REVISION`、systemd 模板和安装脚本。

在目标 Controller 机器上解压并执行包内脚本：

```bash
tar -xzf /tmp/zeno-<sha>-linux-amd64.tar.gz -C /tmp
sudo /tmp/zeno-<sha>-linux-amd64/scripts/deploy-local-release.sh \
  --archive /tmp/zeno-<sha>-linux-amd64.tar.gz \
  --install-dir /opt/zeno \
  --controller-addr 0.0.0.0:18980 \
  --controller-url http://127.0.0.1:18980 \
  --node-id hytron \
  --seed-preview
```

部署结构：

```text
/opt/zeno/current -> /opt/zeno/releases/zeno-<sha>-linux-amd64
/opt/zeno/data/zeno.db
/opt/zeno/data/agent-token
/opt/zeno/data/admin-token
/etc/systemd/system/zeno-controller.service
/etc/systemd/system/zeno-agent.service
```

`agent-token` / `admin-token` 已存在时会保留；缺失时由脚本生成并写入 `0600` 文件。

## 安装 Agent

对于非 Controller 机器，先在 Admin 后台创建服务器并生成该节点的 Agent token，然后把 release 包里的 `zeno-agent` 和脚本传到目标机，执行：

```bash
sudo scripts/install-agent.sh \
  --controller-url https://example.com \
  --node-id <node-id> \
  --token <agent-token>
```

也可以使用 token 文件：

```bash
sudo scripts/install-agent.sh \
  --controller-url https://example.com \
  --node-id <node-id> \
  --token-file /opt/zeno/data/agent-token
```

Agent 安装脚本只安装 `zeno-agent.service`，不会修改 Controller。

## 更新 Zeno

推荐更新顺序由 `scripts/deploy-local-release.sh` 固化：

1. 解压新 release 到 `/opt/zeno/releases/`。
2. 保存旧 `current` 指向和旧 systemd unit。
3. 停止 `zeno-agent.service`。
4. 切换 `/opt/zeno/current` 到新 release。
5. 写入新的 systemd unit 并 `daemon-reload`。
6. 重启 `zeno-controller.service`。
7. 等待 `http://127.0.0.1:18980/health` 成功。
8. Controller 健康后再启动 `zeno-agent.service`。
9. 如果 Controller health 失败，回滚 `current` 和 unit，重启旧 Controller。

## 文档入口

- `docs/NON_GOALS.md`：明确不做什么。
- `docs/DISPLAY_LOCK.md`：展示效果锁定总则。
- `docs/HOME_CARD_SPEC.md`：首页服务器大卡片规格。
- `docs/LATENCY_STATS_SPEC.md`：延迟统计口径。
- `docs/LATENCY_CHART_SPEC.md`：延迟图表规格。
- `docs/DATA_MODEL.md`：SQLite 数据模型。
- `docs/API.md`：Agent / Public / Admin API。
- `docs/SECURITY.md`：安全边界。
- `docs/TECHNICAL_DESIGN.md`：总体技术方案。
- `docs/RELEASE_PACKAGING.md`：发布包结构和安装/更新脚本说明。
- `docs/PHASE1_PLAN.md`：早期阶段计划和当前状态。

## 技术路线

- Controller：Go，SQLite。
- Agent：Go，单二进制，systemd。
- Web：Vite + React + TypeScript。
- 通信：Agent 主动 HTTPS/JSON API，上报路径与 Public/Admin API 分离。
