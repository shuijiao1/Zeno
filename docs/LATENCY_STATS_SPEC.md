# Latency Stats Spec / 延迟统计口径

延迟统计是硬约束。JiaoProbe 必须保留 raw samples，不能只存 avg。

## Probe round

每个 Agent 对每个 target 按固定 interval 执行一轮探测。

每轮包含：

- `sent`
- `received`
- `loss_percent`
- `min_ms`
- `avg_ms`
- `median_ms`
- `max_ms`
- `stddev_ms`
- `samples`
- `error`

## Sample 结构

```ts
interface ProbeSample {
  seq: number
  success: boolean
  latencyMs: number | null
  error?: 'timeout' | 'dns_error' | 'connect_error' | 'permission_error' | 'unknown'
}
```

规则：

- 成功 sample：`success=true`，`latencyMs` 为非负数。
- 超时 sample：`success=false`，`latencyMs=null`，计入 sent，不计入 avg/min/median/max。
- DNS / connect 错误 sample：同样计入 sent 和 loss。

## Loss

```text
received = success sample count
loss_percent = (sent - received) / sent * 100
```

边界：

- `sent=0` 是非法 round，应拒绝入库。
- 全部失败时，`loss_percent=100`，min/avg/median/max/stddev 全部为 null。

## Min / Avg / Median / Max

只对成功 sample 的 latency 计算。

- min：成功 latency 最小值。
- max：成功 latency 最大值。
- avg：成功 latency 算术平均值。
- median：成功 latency 排序后的中位数。
  - 奇数：中间值。
  - 偶数：中间两个值平均。

## Stddev

对成功 sample 使用总体标准差：

```text
sqrt(sum((x - avg)^2) / n)
```

如果成功样本数为 0：`stddev_ms=null`。
如果成功样本数为 1：`stddev_ms=0`。

## ping 和 tcping

MVP 支持：

- `ping`：ICMP echo，多样本。
- `tcping`：TCP connect latency，多样本。

两者使用同一统计口径。

默认建议：

```text
ping count: 20
ping timeout: 1000ms
tcping count: 10
tcping timeout: 1000ms
interval: 30s or 60s
```

## 必测用例

纯函数 `ComputeProbeStats(samples)` 至少覆盖：

1. 全部成功。
2. 部分 timeout。
3. 全部 timeout。
4. 奇数样本 median。
5. 偶数样本 median。
6. 单个成功样本 stddev。
7. 尖峰保留。
8. sent 为 0 的非法输入。
9. ping 和 tcping 使用同一统计口径。
