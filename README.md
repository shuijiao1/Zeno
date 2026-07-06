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
- 月流量：按网卡累计 counter 的 delta 计算，不用速度积分；支持入站/出站/合计/取较大值和每台服务器独立重置日，首页会展示当前计费周期范围。
- 延迟/服务探测：`tcping`、`ping`/ICMP、`http_get`，每轮保留 summary 和 raw samples，并可按服务查看所有节点历史。
- Public API：主页 summary、服务多节点历史、节点延迟、节点状态历史、公开外观设置；外观图片只配置 URL / 站内静态路径。
- Web UI：主页服务器卡片、优化后的首页顶部概览、服务详情页、节点详情页、延迟图、资源历史图、移动端紧凑布局；首页不再单独展示监控服务列表。
- Admin API/UI：单管理员登录、账户页修改账号/密码、退出登录、登录失败限速、服务器、服务器排序、Agent 安装命令轮换/复制、Agent 接入 URL、探针目标、探针目标排序、节点分配、通知、外观设置；外观和 Agent 接入 URL 保存前会先校验格式。
- 服务器元数据：到期日、账单周期、显示顺序（后台可整理，也可在编辑里调整）、国家码/国旗、公网 IPv4、公网 IPv6；Agent 可 best-effort 自动识别公网 IPv4 / IPv6 / 国家码。
- 通知：Telegram-only 渠道，支持离线、异常、测试发送；通知页只展示已添加的通知类型，可通过“添加通知类型”弹窗选择 CPU / 内存 / 磁盘 / 离线通知，并支持按服务器范围生效。
- 发布工具：Docker Compose 自托管、一键安装脚本、Linux amd64 传统 release 打包、GUKO 服务器清单导入脚本。
- Hytron 预览部署：`/opt/zeno`，Controller 通过 Docker Compose 运行并仅绑定 `127.0.0.1:18980`，公网走反代域名。

## 当前不做

- 远程终端、文件管理、远程命令执行、脚本任务。
- NAT、DDNS、多租户、OAuth/复杂角色。
- 旧系统迁移兼容或旧 Agent 协议适配。
- Nezha/Komari/Kulin API、DB 或插件兼容层。
- 多渠道通知、通用 HTTP 通知、自定义 headers/body、通知模板、通知组。
- 服务器分组、备注。

## 安装 Controller

推荐使用 Docker Compose，一条命令安装 / 更新 Controller：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/shuijiao1/Zeno/main/install.sh)
```

默认部署结构：

```text
/opt/zeno/docker-compose.yml
/opt/zeno/.env
/opt/zeno/data/zeno.db
/opt/zeno/secrets/zeno_admin_token
/opt/zeno/secrets/zeno_agent_token
```

容器默认只把 Controller 绑定到 `127.0.0.1:18980`。公网访问请用 Caddy/Nginx 反代到本机端口，不要直接暴露 `18980`。

自定义参数可用环境变量：

```bash
ZENO_INSTALL_DIR=/opt/zeno \
ZENO_HOST_PORT=18980 \
ZENO_IMAGE=ghcr.io/shuijiao1/zeno:latest \
bash <(curl -fsSL https://raw.githubusercontent.com/shuijiao1/Zeno/main/install.sh)
```

## 安装 Agent

Agent 已拆到独立仓库：[`shuijiao1/Zeno-Agent`](https://github.com/shuijiao1/Zeno-Agent)。

在 Admin 后台创建 / 编辑服务器，生成该节点安装命令后直接执行即可。安装命令会调用 Agent 仓库的一键脚本，下载对应架构的 `zeno-agent` Release，并写入 `zeno-agent.service`。

手工安装示例：

```bash
curl -fsSL https://raw.githubusercontent.com/shuijiao1/Zeno-Agent/main/install.sh | sudo env \
  ZENO_CONTROLLER_URL=https://zeno.example.com \
  ZENO_NODE_ID=<node-id> \
  ZENO_AGENT_TOKEN=<agent-token> \
  ZENO_AGENT_VERSION=v0.1.0 \
  bash
```

Agent 安装脚本只安装 / 更新 `zeno-agent.service`，不会修改 Controller。

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
