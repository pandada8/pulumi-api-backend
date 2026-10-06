2026-10-06 评审修复，代码提交 `ea94a7d`、`4b57acd`。

- 路径查询获得 stack 行锁后，以新的 READ COMMITTED statement 重新解析名称，避免等待锁期间的重命名使旧路径继续获准写入。
- RebuiltBaseState 允许纯 refresh 后继续写入；回放后重置操作引用并更新 base 索引范围。有新资源的 rebuild 保持终结语义。
- journal ACK 前校验资源、引擎操作类型（包括 importing）、基线校验和及完整候选回放。拒绝批次不插入日志，不推进 count/head。移除不再使用的引用缓存。
- 非 dry-run Start 将完整当前状态持久化为 authoritative head，保证前一个 journal 尚未 materialize 时，后续 full 更新失败、取消或过期的历史导出仍包含完整基线。

验证结果：

- `make verify` 通过，run `pulumid-035d8292644c`，源码 `4b57acd8a1c09b230ab932f045bbce33e03a99d4`。包含 race 单元测试、独立连接等待行锁的重命名回归、双 API 契约测试、真实固定版本 CLI 的 journal/full 生命周期及 `up --refresh`、浏览器、故障和恢复测试。
- 无测试钩子的生产构建执行 `PULUMID_TEST_BACKEND=bin/backend-production python3 scripts/test.py contract` 通过，run `pulumid-c11f8dd0b0f1`，同一源码提交。包含无效 state、操作、provider 引用和混合批次拒绝，以及无 worker 时失败、取消、过期历史导出回归。
- 固定 Pulumi 源码 `21bf19ba40dba5dce0f565e8c614b5d8aa4dbb17`；Go `1.26.5`。

完整候选回放发生在持锁的上传事务内，会增加 ACK 延迟，随着日志增长重复回放的成本也会增加。本次修复未重新执行性能基准；此前测试文档中的性能结果不能代表这版代码，原性能目标仍待验证和优化。
