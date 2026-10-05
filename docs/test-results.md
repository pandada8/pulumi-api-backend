# 实际测试结果

验证日期：2026-10-06（Asia/Shanghai）。功能测试源码提交：`2e611c89d78ca2904dfdae076bb7c8420df4a936`；随后提交 `52a56d17b3db2ffebd894eb8086627c4872d145c` 仅将 Compose 的 PostgreSQL 健康检查改为 TCP。Go：1.26.5，linux/amd64。Pulumi CLI、Go language host、SDK 和 journal replayer 固定于 `21bf19ba40dba5dce0f565e8c614b5d8aa4dbb17`，构建版本标签 3.246.0。PostgreSQL 17 Alpine 使用 Compose 中记录的镜像 digest。

## 已执行命令

| 命令 | 退出码 | 实际结果/证据 |
|---|---:|---|
| `make verify` | 0 | 单元 race 测试、upstream client 合约、两 API 实例、原版 CLI 两种状态模式、故障、安全、Chromium、恢复；run `pulumid-050da57dace7` |
| `go test -race ./internal/...` | 0 | JSON 重复键/尾随值/数字精度、AES-GCM tamper/AAD、游标签名与作用域、rename、redaction、diff、journal pending/output/references |
| `docker build -t pulumid:local .` | 0 | 生产镜像包含 backend/backendctl；不编译 faulttest 控制端点 |
| 隔离 Compose 启动及 `scripts/smoke.py` | 0 | db→重复 migrate→bootstrap 两 token→api+worker→readyz→wire smoke→metrics；测试项目 `pulumid-compose-397` 已 down 并删除其专属 volume |
| `make build-binaries` | 0 | 最终本地二进制使用正常生产构建，未留下 faulttest binary |
| `python scripts/test.py contract`（生产 binary） | 0 | run `pulumid-8e7a2b1ba74e`，源码 `52a56d1`；生产构建的协议/租约/secret/journal/session 回归通过 |
| `docker compose config --quiet` | 0 | 最终 Compose 配置检查通过 |

`make verify` 的 Go 测试和两 API 实例均使用 race instrumentation；未发现 race。服务日志检查未发现测试 token 或 secret canary。详细执行日志、浏览器截图与结果 JSON 保存在各 runID 目录；交付附件只选择脱敏日志/截图/结果，不包含 master key、token、Pulumi credentials 或数据库备份。

## 已验证行为

- 原版 CLI：login/whoami、stack init/select/list、默认 service secret config、preview、up、输出读取、输入更新、replacement、refresh、独立 world 资源 import、history、stack export/import、rename、destroy、rm。full 与 journal 两种模式都完成完整链路。
- 两实例真实竞争 Start：只允许一个 writer；重复 Start 返回原 token/version；不同 Start payload 冲突；preview/dryRun 不分配正式版本、不写资源 state。
- gzip checkpoint、未知普通 state 字段、64 位以上 JSON 整数、历史 snapshot 不被后续 import 改写；`isInvalid` 无 deployment 保留恢复数据并禁止新的正式写入，显式 import 后可恢复。
- lease 续租不换 token；错误/跨 stack/取消/过期/撤销后写被拒绝；同状态 Complete 重放成功，不同状态冲突；reader 写和解密被拒绝；不同组织隔离。
- secrets 随机 nonce、篡改拒绝、stack UUID AAD 隔离、单个/批量往返、rename 后旧路径解密、同名删除重建后不能解密旧密文。
- journal begin 后 pending create 可读，success 后清除 pending，outputs 按 operation 引用更新；sequenceID 非连续且顺序与数值不同仍按 ingest order 重放；批次/条目重放去重；冲突序号、未知 kind、无效引用、缺字段拒绝。
- 205 条乱序 events 的分页/重放/冲突、签名游标篡改拒绝，legacy import 轮询终态正确收尾。
- 故障：提交后丢 HTTP 响应可重试；提交前失败无状态改变；journal rollback 不发布 speculative cache；锁前暂停期间 Cancel 会拒绝恢复的旧 writer；API/数据库重启后 ACK 状态可读；materialization 不覆盖后续 full state。
- 独立文件 world：provider 已创建资源但尚未返回时 kill CLI 及其 process group，backend 保留 pending create，world 资源仍存在；full/journal 均验证。provider delete 故障使 destroy 失败且资源保留，解除故障后正常 destroy。
- Chromium 141.0.7390.37：登录跳转、五类页面/CLI permalink、DOM、secret 隐藏、XSS 不执行、事件轮询、HttpOnly/SameSite cookie、无 localStorage token、桌面及窄屏截图。
- 隔离 restoredb：恢复逻辑备份、旋转 generation、旧 lease 拒绝、已 ACK export 一致、旧 secret 可解密、新 writer 可开始；源库 running update 未被修改。

## 验证边界

这些结果证明所列场景，不代表旧设计文档的每个编号用例已实现。真实 300 秒续租时长测试、完整命名故障矩阵、S3/DIY 与固定 provider 的端到端性能对照尚未执行。KMS、OAuth、ESC、schema-v4、delta 等未实现项没有被伪造为能力。大 stack 的 Start/冷重放/物化仍在 stack lock 内计算；完整实现差异见 `docs/supported-api.md`。

## API 存储性能实测

命令：`PULUMID_TEST_BACKEND=bin/backend-bench python scripts/test.py bench`，退出码 0，run `pulumid-b9c944a90af8`。每组 3 次 warmup、30 轮；十并发组每模式 300 个样本。代理分别在转发请求前/返回响应前延迟 10ms，实测请求往返中位数 22.63ms。资源 state 约 1KiB，变更率 1%，请求 gzip。

| 资源数 | 并发 stack | 模式 | gzip 上传均值 (bytes) | ACK p95 (ms) | export p95 (ms) | 单次更新 p95 (ms) |
|---:|---:|---|---:|---:|---:|---:|
| 1000 | 1 | full-api | 12000 | 166.5 | 34.1 | 277.8 |
| 1000 | 1 | journal-api | 482 | 63.6 | 115.0 | 257.5 |
| 10000 | 1 | full-api | 115977 | 1104.1 | 105.7 | 1495.6 |
| 10000 | 1 | journal-api | 2925 | 311.2 | 740.2 | 1192.2 |
| 1000 | 10 | full-api | 12000 | 424.7 | 66.6 | 1297.3 |
| 1000 | 10 | journal-api | 480 | 207.0 | 278.6 | 1264.2 |
| 10000 | 10 | full-api | 115978 | 3096.9 | 481.0 | 4513.2 |
| 10000 | 10 | journal-api | 2924 | 1219.6 | 2145.3 | 4409.4 |

这是 **API 存储微基准**：每次 1% 更新发送一次全量 checkpoint 或一个 journal batch，不是原版 CLI 每一步写入的端到端对照，也没有 S3/DIY 组。cold/warm 字段只代表连续两次 export；当前没有 export cache。DB WAL/RSS 未采集，相应 CSV 列为空。gzip 的样本数据较重复，压缩比不可外推到任意用户 state。

10,000 资源/十并发下，journal 压缩上传减少约 97.5%，ACK p95 从约 3097ms 降到 1220ms，但未达到 150ms ACK 和 1s export 目标。总更新时间收益较小，因为 Start 的完整 base snapshot 和 export 全量重放仍占时间。基准期间同机还在运行功能验证/镜像构建，且该正常产品 binary 编译早于最后 GUI/metrics 改动，因此这些测量仅用于定位，不归属于最终 release SHA；附件 environment.json 记录其 binary SHA256、8 CPU、Go/Linux、PostgreSQL digest 等环境信息。

后续优化优先级：减少批量 journal 的逐条 SQL lookup/insert 往返；把 cold replay/候选计算移出 stack lock；缓存提交后、固定 watermark 的 export；用独占环境重跑原规格 provider/CLI/S3-DIY 对照后再判断发布性能门槛。上述方向是代码检查与微基准支持的候选，不冒充完整瓶颈验证。CPU/内存 profile 及实际热点另附。

## CPU / 内存 profile

已执行 `go test ./internal/store -run '^$' -bench '^BenchmarkReplay$' -benchtime=2s -cpuprofile=test-results/journal.cpu -memprofile=test-results/journal.mem`，退出码 0。随后 `go tool pprof -top -nodecount=12 test-results/journal.cpu` 退出码 0。独立重放微基准：1,000 资源约 61.4ms/op、30.6MB 分配；10,000 资源约 585.8ms/op、307.3MB 分配、约 192 万次分配。CPU top 12 占样本约 75%，主要是 encoding/json 字符串扫描、Decoder.readValue、unquoteBytes、appendCompact/checkValid 和内存搬运。它解释了 export 热读仍然昂贵：当前每次冷式重放都重复解析/校验/序列化完整 deployment。此 profile 不包含数据库和 HTTP，不能据此量化 journal ACK 的 SQL 时间。原始 profile 和文本输出随附件交付。

测试后的检查确认没有本仓库 test-results/PULUMI_HOME 的存活子进程，所有本轮专属 PostgreSQL/Compose 容器均已清理。最终源码和 Git 提交在指定本地仓库中；未创建或推送远端仓库。
