# Controller probe-results 可靠接收复核

基线：`v1.0.0` / `9d4de9851c2cdeb525f4be0447b6e8a73ce3c6ac`。

## 基线事实

- `probe_config_meta.version` 是全局配置版本；Admin 的目标新增、编辑、删除/禁用与版本递增位于同一 SQLite 写事务。Agent 拉取目标时，目标集合和版本取自同一读快照。
- `/api/agent/v1/probe-results` 在 Handler 做请求形状、round/target 重复、样本序号和错误字节预算校验；Store 再做预算、当前配置版本、当前 node-target assignment、类型及目标 `count` 校验。
- Store 先取得 SQLite writer reservation，再在同一事务中比较非零 `config_version`、加载当前目标并插入整批 round/sample；任一错误回滚整批。版本不符返回 `409 stale_probe_config`。
- `probe_rounds` 有唯一部分索引 `idx_probe_rounds_agent_id(node_id, agent_round_id)`，并有旧 Agent 使用的唯一索引 `idx_probe_rounds_idempotency(node_id,target_id,ts,type,idempotency_key)`；`probe_samples` 以 `(round_id,seq)` 为主键。原 `payload_hash` 只覆盖目标归一化后的 samples。
- Agent 写入经过按 node 的 HTTP token bucket/并发配额，并进入全局 128、单 node 16 的有界 round-robin 单 writer 队列。队列/配额满返回 429。遥测磁盘高水位返回 507；heartbeat 保留恢复通道。
- probe-results 只写探针历史，不产生通知候选。

## 基线问题

事务已经提交但 HTTP 响应丢失后，重试会先经过当前时间窗口、当前 `config_version`、当前 target assignment 和磁盘高水位校验。积压期间配置变化或目标删除/禁用会把数据库中已经存在的精确 round 重试误判为 stale/invalid；旧 timestamp 也会在到期后被 Handler 提前拒绝。相同 round id 的冲突原先落成 500，不能与暂时性服务端失败区分。逐 round 查重也没有显式保证使用 node/round 唯一索引。

## 修复后的最小语义

1. Handler 仍先拒绝不可能曾成功提交的请求形状：非法/重复 round id、重复 target、样本序号、硬上限和错误预算。
2. Store 取得 writer reservation 后，用一次 `node_id + agent_round_id IN (...)` 索引查询分类整批；查询显式带部分索引谓词。
3. 新写入的 `payload_hash` 使用 `v2:` 前缀，覆盖 trim 后的 target id/type、timestamp 及完整 sample payload，且在 latency cap/target-dependent timeout 转换前计算。
4. 已存在 v2 round 且 payload hash 相同记为幂等；不同则整批 `409 {"error":"probe_round_conflict"}`，零写入。旧无前缀 hash 保留 metadata + legacy sample hash 兼容比较。
5. 全批均幂等时，在 stale config、当前 target、timestamp 和磁盘高水位检查之前成功返回。响应保留 `ok`/`accepted`，新增 `inserted`/`idempotent` 便于明确确认。
6. 混合批只跳过已确认的精确 round；任何新 round 仍必须通过磁盘保护、当前配置/assignment/type/count/timestamp 校验。为容纳 Agent 的持久积压，probe 新 round 的过去时间窗单独放宽到 72 小时（未来仍严格限制 5 分钟），state/heartbeat 的 10 分钟窗口不变。任一失败时事务回滚，整批不写新数据。
7. 单 writer、公平有界队列、HTTP 配额、507 保护及通知边界不变；全幂等批不重复触发 cache/publish/通知候选副作用；故障注入只存在于 `_test.go` 的 httptest/store wrapper，不暴露生产接口。

## 边界

幂等确认依赖对应 `probe_rounds` 仍存在；历史保留或目标删除 worker 已物理清除 round 后，Controller 不再声称该 round 已提交。v1 行的 hash 是 target-dependent 的旧格式：通常可兼容精确重试，但若旧 target 归一化曾改写样本且 target 已不可用，无法从旧行恢复原始请求字节；新 v2 行没有这个歧义。
