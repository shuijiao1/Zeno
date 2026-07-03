# Home Card Spec / 首页服务器大卡片规格

首页大卡片是 Zeno 第一优先级展示目标。第一阶段先用 mock 数据复刻，不接真实后端。

## 桌面布局

- 服务器卡片网格：桌面端默认三列。
- 卡片之间留足间距，不挤、不像廉价小方块 dashboard。
- 卡片宽度自适应容器。
- 移动端单列，平板可两列。

建议断点：

```text
>= 1200px: 3 columns
768px - 1199px: 2 columns
< 768px: 1 column
```

## 卡片头部

头部包含：

- OS 图标。
- 在线状态点。
- 国家 / 地区旗帜。
- 服务器显示名。
- 可选副标题：地区、运营商或标签。

位置关系：

```text
[OS Icon] [Status Dot] [Flag] Server Name
                         optional subtitle
```

状态颜色：

- Online：绿色。
- Warning / degraded：黄色或橙色。
- Offline：红色。
- No data：灰色。

## 资源条

必须包含：

- CPU 使用率。
- 内存使用率 / 总量。
- 硬盘使用率 / 总量。
- 月流量使用率 / quota。

每行结构：

```text
Label          value / capacity
[progress bar]
```

颜色阈值建议：

- 0-69%：绿色。
- 70-84%：橙色。
- >=85%：红色。

月流量条按 `billable_bytes / quota_bytes` 计算，不按速度积分。

## 网络流量信息

必须展示：

- 当前下载速度。
- 当前上传速度。
- 总接收流量。
- 总发送流量。
- 本月计费流量。

展示层级：速度优先，总量次之。

## 延迟摘要

每张卡片需要展示当前选中的延迟目标摘要：

- target 名称。
- 最新 median 或 avg latency。
- loss percent。
- 状态颜色。

如果没有数据：显示 `No data`，不能伪造 0ms。

## 数据字段草案

```ts
interface HomeCardNode {
  id: string
  displayName: string
  status: 'online' | 'warning' | 'offline' | 'no_data'
  os: 'debian' | 'ubuntu' | 'centos' | 'alpine' | 'linux' | 'unknown'
  countryCode?: string
  subtitle?: string
  cpuPercent: number | null
  memoryUsedBytes: number | null
  memoryTotalBytes: number | null
  diskUsedBytes: number | null
  diskTotalBytes: number | null
  netInSpeedBps: number | null
  netOutSpeedBps: number | null
  netInTotalBytes: number | null
  netOutTotalBytes: number | null
  monthlyBillableBytes: number | null
  monthlyQuotaBytes: number | null
  latencySummary?: LatencySummary
}

interface LatencySummary {
  targetId: string
  targetName: string
  medianMs: number | null
  avgMs: number | null
  lossPercent: number | null
  updatedAt: string
}
```
