# Latency Chart Spec / 延迟图表规格

延迟图表必须保持当前 Kulin / Nezha-like 方向：多目标同图、细线、保留尖峰、丢包可见。

## 默认视图

- 多个 latency target 在同一张图。
- 每个 target 一条线。
- y 轴单位：ms。
- 绘制坐标轴上限为 5000ms；探测超时仍按目标配置（默认 1000ms）执行，丢包计算继续使用真实成功/失败样本，不因为绘制上限改变。
- x 轴：时间。
- tooltip：时间、target、latency、loss。
- 默认不做会抹掉尖峰的平滑。

## 线条

- 细线，接近 1px。
- 不加厚重 glow。
- 不默认显示点。
- 不用过度圆滑曲线。
- 允许断点表示无数据或 100% loss。

## 丢包展示

丢包必须可见，不能只藏在 tooltip 里。

允许方式：

- loss marker。
- 红色点 / 红色短线。
- 背景轻微红色区间。
- 断线 + tooltip loss。

要求：

- 部分丢包显示 loss percentage。
- 全部丢包该时间点不能画成 0ms。
- 无数据和丢包要区分。

## 数据输入

```ts
interface LatencyChartPoint {
  ts: string
  targetId: string
  targetName: string
  medianMs: number | null
  avgMs: number | null
  minMs: number | null
  maxMs: number | null
  lossPercent: number
  samples: Array<number | null>
}
```

默认 line value：优先 `medianMs`。

如果一个 round 全部失败：

```text
medianMs=null
lossPercent=100
```

图上显示丢包 marker，不显示 0ms latency。

## 时间范围

第一版至少支持：

- 1h
- 6h
- 24h
- 7d

聚合原则：

- 短时间范围尽量使用原始 round。
- 长时间范围可以聚合，但必须保留 loss 和代表性尖峰。
- 聚合时不能只取 avg，应考虑 median / max / loss。

## 验收样例

验收数据必须覆盖：

- 稳定低延迟 target。
- 高延迟 target。
- 带尖峰 target。
- 部分丢包 target。
- 短暂全丢包 target。

这样才能看出图表是否把尖峰和丢包抹掉。
