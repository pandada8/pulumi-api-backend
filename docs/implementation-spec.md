这次将报告改为**实现规格 v1**：实现者按下列顺序建仓、建表、写函数和跑测试，不再自行决定核心行为。正文中的 MUST 表示验收必需；标为扩展的内容不属于 v1。所有命令是最终仓库应提供的命令契约，除文末明确列出的 SQL 检查外，本轮没有实现或运行该产品。

本规格取代上一份方案中仍需实现者选择的部分。固定选择如下：**Go + PostgreSQL 17 + 服务端 HTML 模板/原生 JS；全量 checkpoint、journal v1 和 GUI 必须完成；delta 可在其后增加。** 初版不拆微服务，不引入 Redis、消息队列、前端构建工具或对象存储。这样身份、写租约、状态与日志的正确性只有一个数据库事务边界。

**1. 明确定义最终产物和兼容范围**

最终仓库必须包含 `backend`、`backendctl` 两个二进制、数据库迁移、Docker Compose、GUI、测试 provider、自动化测试、操作手册。用户执行本地启动步骤后，用**未修改**的 Pulumi CLI 完成 login、stack init/ls/select、secret config、preview、up、refresh、资源 import、stack export/import、history、rename、destroy、rm；点击 CLI 输出的链接能看到正确的 stack/update/preview 页面。

固定 Pulumi 源码 SHA：`21bf19ba40dba5dce0f565e8c614b5d8aa4dbb17`。应用依赖、测试 CLI 和参考重放器必须来自同一 SHA。不要把“本机恰好安装的 CLI”当作该 SHA 构建的 CLI。

v1 支持 deployment schema **3**、journal **1**、当前客户端的 `Accept: application/vnd.pulumi+9`。三者不可混用。先限制到传统 IaC 资源；不支持 v4 专有的 snippets、views、hooks、taint、replaceWith、refreshBeforeUpdate，检测到请求中使用这些字段即明确 422，不能删除字段后存盘。即使客户端把 envelope 降级成 v3，也要检查实际字段，防止静默丢失新特性。旧字段中的 `pendingReplacement`、`deletedWith` 必须支持。

远程执行、ESC/cloud config、Policy Packs、Registry、OAuth、组织邀请和跨组织 stack transfer 不在 v1。非空 teams、非空 cloud config 明确 422。service secrets 默认流程必须可用，不能通过要求所有用户改用 passphrase 来省略它。

可用性目标：单数据库、两个 API 实例可正确工作，服务重启可恢复已确认状态。同步副本、KMS 托管密钥和 S3 归档属于后续生产增强；v1 不承诺单机磁盘全部损毁后零数据丢失。

**2. 仓库布局和固定技术选择**

```text
cmd/backend/main.go             # serve / worker / migrate
cmd/backendctl/main.go          # bootstrap / token / member / restore-generation
internal/config/config.go
internal/httpapi/{router,middleware,errors,identity,stacks,updates,state,secrets,events}.go
internal/auth/{token,session,authorization}.go
internal/store/{db,tx,stacks,updates,snapshots,journal,events,keys,jobs}.go
internal/core/{stacks,updates,checkpoint,journal,materialize,secrets,events}.go
internal/pulumicompat/{types,validation,rename,replayer,redact,fixtures}.go
internal/web/{routes,viewmodels}.go
internal/web/templates/{layout,login,stacks,stack,update,resources,history}.html
internal/web/static/{app.css,app.js}
internal/worker/{jobs,expiry}.go
migrations/001_initial.sql
tests/{contract,integration,e2e,fault,security,web,bench}/
tests/provider/                 # 独立的可注入故障 provider
tests/world/                    # provider 外部资源的持久化模拟服务
tests/projects/{basic,dependencies,replacement,reference,large}/
testdata/{wire,journal,checkpoints}/
scripts/{dev-init,build-cli,test-contract,test-e2e,test-fault,bench,backup,restore}.sh
third_party/pulumi/             # Git submodule，固定上述 SHA
compose.yaml
Makefile
README.md
docs/{supported-api,operations,test-results}.md
```

Go 工具链固定 `1.26.5`；HTTP 用 `net/http`，数据库用 `database/sql` + `github.com/lib/pq v1.12.0`，UUID 用 `github.com/google/uuid v1.6.0`，可选 delta 用 `github.com/hexops/gotextdiff v1.0.3`。提交 go.mod/go.sum，不在 CI 中自动升级依赖。

主模块同时 replace `github.com/pulumi/pulumi/pkg/v3` 到 `./third_party/pulumi/pkg`、`sdk/v3` 到 `./third_party/pulumi/sdk`。上游模块中的 replace 不会自动成为主模块的 replace，主模块需同步该 SHA 必需的替换项，例如 clipboard 的替换。`scripts/build-cli.sh` 在 `third_party/pulumi/pkg` 内构建 `./cmd/pulumi` 到仓库 `bin/pulumi`，并输出 SHA、Go 版本、CLI 版本到 `test-results/build.json`。

禁止 handler 直接拼 SQL。handler 做鉴权/解码，core 定义事务行为，store 执行参数化 SQL。禁止以进程内 mutex 作为跨实例写锁；允许 mutex 保护可丢弃缓存。

配置字段固定：

| 环境变量 | 默认/约束 |
|---|---|
| `BACKEND_LISTEN` | `:8080` |
| `BACKEND_PUBLIC_URL` | 必填；外部 API 根 URL，无尾部 `/` |
| `BACKEND_CONSOLE_URL` | 默认与 PUBLIC_URL 相同 |
| `BACKEND_DATABASE_URL` | 必填，不打印到日志 |
| `BACKEND_MASTER_KEY_FILE` | 必填；32 字节随机根密钥、文件权限 0600、只读挂载 |
| `BACKEND_ENABLE_JOURNAL` | 默认 false；达到阶段验收后才设 true |
| `BACKEND_ENABLE_DELTA` | 默认 false；v1 允许一直关闭 |
| `BACKEND_DEV_HTTP` | 默认 false；仅本机测试设 true，允许 HTTP/cookie 非 Secure |
| `BACKEND_TEST_FAULTS` | 默认 false；正式构建不编入故障控制端点 |

常量固定：lease 300s、worker 每 5s 检查过期、session 8h、事件收尾窗口 30s。HTTP header 读取超时 5s；普通 handler 60s，export/materialize 120s；数据库 lock_timeout 5s、statement_timeout 30s。以后改变这些常量必须同步测试。

**3. 数据库迁移：使用以下完整初始结构**

所有 UUID 由 Go 创建；`schema_version=3` 是本规格明确的兼容限制。数据库不能保存解密后的用户 secret。snapshot 原文用 bytea，JSONB 仅用于不要求字节保持的元信息。

```sql
BEGIN;
CREATE TABLE schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE TABLE service_meta (singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), generation uuid NOT NULL);
CREATE TABLE principals (id uuid PRIMARY KEY, login text NOT NULL UNIQUE, display_name text NOT NULL, disabled boolean NOT NULL DEFAULT false);
CREATE TABLE organizations (id uuid PRIMARY KEY, name text NOT NULL UNIQUE);
CREATE TABLE memberships (
  org_id uuid NOT NULL REFERENCES organizations(id), principal_id uuid NOT NULL REFERENCES principals(id),
  role text NOT NULL CHECK(role IN ('reader','writer','admin')), can_decrypt boolean NOT NULL DEFAULT false,
  PRIMARY KEY(org_id,principal_id)
);
CREATE TABLE api_tokens (
  id uuid PRIMARY KEY, principal_id uuid NOT NULL REFERENCES principals(id), digest bytea NOT NULL UNIQUE CHECK(octet_length(digest)=32),
  can_write boolean NOT NULL, can_decrypt boolean NOT NULL, expires_at timestamptz, revoked_at timestamptz
);
CREATE TABLE stacks (
  id uuid PRIMARY KEY, org_id uuid NOT NULL REFERENCES organizations(id), tags jsonb NOT NULL DEFAULT '{}',
  last_version bigint NOT NULL DEFAULT 0 CHECK(last_version>=0), fence bigint NOT NULL DEFAULT 0 CHECK(fence>=0),
  active_update_id uuid, deleted_at timestamptz, created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(org_id,id)
);
CREATE TABLE stack_names (
  org_id uuid NOT NULL, project text NOT NULL, name text NOT NULL, stack_id uuid NOT NULL,
  is_current boolean NOT NULL, PRIMARY KEY(org_id,project,name),
  FOREIGN KEY(org_id,stack_id) REFERENCES stacks(org_id,id)
);
CREATE UNIQUE INDEX one_current_name ON stack_names(stack_id) WHERE is_current;
CREATE TABLE snapshots (
  id uuid PRIMARY KEY, stack_id uuid NOT NULL REFERENCES stacks(id), schema_version integer NOT NULL CHECK(schema_version=3),
  raw_bytes bytea NOT NULL, sha256 bytea NOT NULL CHECK(octet_length(sha256)=32), is_invalid boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(stack_id,id)
);
CREATE TABLE updates (
  id uuid PRIMARY KEY, stack_id uuid NOT NULL REFERENCES stacks(id), actor_token_id uuid NOT NULL REFERENCES api_tokens(id),
  kind text NOT NULL CHECK(kind IN ('update','preview','refresh','destroy','import','rename')),
  dry_run boolean NOT NULL, status text NOT NULL CHECK(status IN ('created','running','succeeded','failed','cancelled')),
  mode text NOT NULL CHECK(mode IN ('full','delta','journal','none')), request jsonb NOT NULL,
  base_snapshot_id uuid, final_snapshot_id uuid, version bigint, fence bigint,
  lease_digest bytea CHECK(lease_digest IS NULL OR octet_length(lease_digest)=32), lease_generation uuid, lease_expires_at timestamptz,
  start_request_digest bytea, start_response_encrypted bytea, complete_status text, reason text,
  journal_count bigint NOT NULL DEFAULT 0 CHECK(journal_count>=0), event_count bigint NOT NULL DEFAULT 0 CHECK(event_count>=0),
  delta_sequence bigint NOT NULL DEFAULT 0 CHECK(delta_sequence>=0), event_closed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(), started_at timestamptz, ended_at timestamptz,
  UNIQUE(stack_id,id),
  FOREIGN KEY(stack_id,base_snapshot_id) REFERENCES snapshots(stack_id,id),
  FOREIGN KEY(stack_id,final_snapshot_id) REFERENCES snapshots(stack_id,id)
);
CREATE UNIQUE INDEX one_running_update ON updates(stack_id) WHERE status='running';
CREATE UNIQUE INDEX unique_history_version ON updates(stack_id,version) WHERE NOT dry_run AND version IS NOT NULL;
ALTER TABLE stacks ADD CONSTRAINT active_update_belongs_to_stack FOREIGN KEY(id,active_update_id)
  REFERENCES updates(stack_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE stack_heads (
  stack_id uuid PRIMARY KEY REFERENCES stacks(id), generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0),
  snapshot_id uuid NOT NULL, journal_update_id uuid, journal_upto bigint NOT NULL DEFAULT 0 CHECK(journal_upto>=0),
  CHECK(journal_update_id IS NOT NULL OR journal_upto=0),
  FOREIGN KEY(stack_id,snapshot_id) REFERENCES snapshots(stack_id,id),
  FOREIGN KEY(stack_id,journal_update_id) REFERENCES updates(stack_id,id)
);
CREATE TABLE checkpoint_receipts (
  stack_id uuid NOT NULL, update_id uuid NOT NULL, sequence_number bigint NOT NULL CHECK(sequence_number>0),
  target_sha256 bytea NOT NULL CHECK(octet_length(target_sha256)=32), snapshot_id uuid NOT NULL,
  PRIMARY KEY(update_id,sequence_number),
  FOREIGN KEY(stack_id,update_id) REFERENCES updates(stack_id,id),
  FOREIGN KEY(stack_id,snapshot_id) REFERENCES snapshots(stack_id,id)
);
CREATE TABLE journal_entries (
  update_id uuid NOT NULL REFERENCES updates(id), sequence_id bigint NOT NULL CHECK(sequence_id>0),
  ingest_order bigint NOT NULL CHECK(ingest_order>0), operation_id bigint NOT NULL CHECK(operation_id>=0),
  payload bytea NOT NULL, digest bytea NOT NULL CHECK(octet_length(digest)=32),
  PRIMARY KEY(update_id,sequence_id), UNIQUE(update_id,ingest_order)
);
CREATE TABLE engine_events (
  update_id uuid NOT NULL REFERENCES updates(id), sequence bigint NOT NULL CHECK(sequence>=0),
  ingest_order bigint NOT NULL CHECK(ingest_order>0), event_type text NOT NULL, urn text, payload jsonb NOT NULL,
  digest bytea NOT NULL CHECK(octet_length(digest)=32), PRIMARY KEY(update_id,sequence), UNIQUE(update_id,ingest_order)
);
CREATE TABLE materializations (
  stack_id uuid NOT NULL, update_id uuid NOT NULL, upto bigint NOT NULL, replayer_version text NOT NULL,
  snapshot_id uuid NOT NULL, PRIMARY KEY(update_id,upto,replayer_version),
  FOREIGN KEY(stack_id,update_id) REFERENCES updates(stack_id,id),
  FOREIGN KEY(stack_id,snapshot_id) REFERENCES snapshots(stack_id,id)
);
CREATE TABLE stack_keys (
  stack_id uuid NOT NULL REFERENCES stacks(id), version integer NOT NULL CHECK(version>0),
  kek_id text NOT NULL, wrapped_key bytea NOT NULL, active boolean NOT NULL,
  PRIMARY KEY(stack_id,version)
);
CREATE UNIQUE INDEX one_active_key ON stack_keys(stack_id) WHERE active;
CREATE TABLE web_sessions (
  digest bytea PRIMARY KEY CHECK(octet_length(digest)=32), token_id uuid NOT NULL REFERENCES api_tokens(id),
  csrf_digest bytea NOT NULL CHECK(octet_length(csrf_digest)=32), expires_at timestamptz NOT NULL
);
CREATE TABLE jobs (
  id uuid PRIMARY KEY, job_key text NOT NULL UNIQUE, kind text NOT NULL, payload jsonb NOT NULL,
  status text NOT NULL CHECK(status IN ('pending','running','done')), attempts integer NOT NULL DEFAULT 0,
  available_at timestamptz NOT NULL DEFAULT clock_timestamp(), locked_until timestamptz, last_error text
);
CREATE TABLE audit_log (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, at timestamptz NOT NULL DEFAULT clock_timestamp(),
  principal_id uuid, org_id uuid, stack_id uuid, update_id uuid, action text NOT NULL, result text NOT NULL, request_id uuid NOT NULL
);
CREATE INDEX updates_history ON updates(stack_id,version DESC) WHERE NOT dry_run;
CREATE INDEX updates_expiry ON updates(lease_expires_at) WHERE status='running';
CREATE INDEX jobs_pending ON jobs(available_at) WHERE status IN ('pending','running');
INSERT INTO schema_migrations(version) VALUES (1);
COMMIT;
```

附加实现规则：

- 同一事务创建 stack、current name、空 snapshot、head 和 stack key；不能留下没有 head 的 stack。
- snapshots、journal_entries、checkpoint_receipts 业务上只插入、不原地修改。v1 不做在线历史 GC，先保证备份恢复。
- `stack_heads` 是统一的读取描述符：`journal_update_id=NULL` 时直接读 snapshot；非 NULL 时从 snapshot 这个原始 base 重放该 update 的 `ingest_order<=journal_upto`。`generation` 每次改变权威描述符递增。
- update 的 `base_snapshot_id` 开始后不变；`journal_count` 是服务端接收位置，不是客户端 sequenceID。
- 正式 history version 从 1 开始，失败也占版本；preview/dryRun 返回当前 last_version，但不增加它。实际 checkpoint 次数与 history version 无关。
- `stack_names` 的旧名字仅供历史 service secrets 解密寻址，不接受新的 state 更新；被 rename 的旧名字在 stack 存活时保留，不能被其他 stack 占用。delete 时删除该 stack 的所有名字，保留 tombstone、历史和 key；再 init 同名得到新 UUID，旧密文不能被新 stack 解密。
- `service_meta` 由首次初始化写一行 generation。恢复备份后、接入流量前更换该值，使所有旧 lease 失效。

事务使用 READ COMMITTED，写入锁序固定 `stack → update → head`。鉴权读取涉及 token/membership 时，在同一事务使用共享行锁，避免撤销与授权写同时越过检查；管理命令不要拿着 token/membership 独占锁再去反向锁 stack。`clock_timestamp()` 在取得锁后读取，不用事务开始时的时间判断 lease。

**4. HTTP 共通规则和错误表**

中间件顺序：request ID → 压缩字节限流 → gzip 解压 → 解压后限流 → JSON 合法性/重复 key 检查 → 身份 → endpoint 权限 → handler。所有 JSON decoder 使用 `UseNumber`；读取后必须确认 EOF，拒绝第二个 JSON 值。未声明的普通字段允许保留/忽略以便兼容；journal 的协议结构字段使用严格版本校验，不能将未知 kind 当 no-op。

状态请求最多压缩后 32MiB、解压后 128MiB；journal 单批最多 16MiB/32MiB、1,000 条；events 最多 4MiB/16MiB、1,000 条；其他请求 1MiB/4MiB。超过限制在提交前返回 413。gzip 内容损坏返回 400，未知 Content-Encoding 返回 415。

普通 API：`Authorization: token <random token>`。update 写 API：`Authorization: update-token <random lease>`。鉴权 token 不得进入 URL、访问日志或错误消息。返回错误一律 `{"code":HTTP状态,"message":"稳定的说明"}`，Content-Type application/json；生产不返回堆栈。

| 情况 | HTTP/处理 |
|---|---|
| 缺失/错误普通 token | 401 |
| 有效身份但缺少操作权限 | 403；无权查看的 org/stack/update 可统一返回 404，避免泄漏存在性 |
| 过期/被取消/跨 stack 的 update token | 403 |
| 路由对象不存在 | 404 |
| running 冲突、名字冲突、同序号不同内容、非法状态转移 | 409 |
| 不支持 schema/feature/kind、非空 teams/cloud config | 422 |
| 非 force 删除仍有资源 | 400，message 精确为 `Bad Request: Stack still contains resources.` |
| 数据库锁超时、暂时不可用 | 503；不得返回伪成功 |
| 完整性异常、持久数据 hash 不符、重放内部失败 | 500，报警并保留原数据；不得自动覆盖/返回旧 snapshot |

有 JSON 响应的成功接口返回 200+有效 JSON；创建对象统一使用 200，避免实现者随意返回空 201/204。无响应对象的写操作返回 204。`HEAD` 无响应体。正常列表返回空数组 `[]`，不返回 null。

权限公式：读取要求当前 membership；写入要求 membership 为 writer/admin **且** api_token.can_write；解密要求 membership.can_decrypt **且** api_token.can_decrypt。管理员不自动越过 can_decrypt。lease 再继承创建者 token 的有效性及写权限，不因已取得 lease 永久绕过撤销。

**5. 必须实现的路由及 handler 行为**

缩写 `P=/api/stacks/{org}/{project}`、`S=P/{stack}`、`U=S/update/{id}`。`id` 必须属于路径定位到的稳定 stack UUID。创建 preview/refresh/destroy 后，所有后续路径仍为 U，不能按创建种类拼路径。

| 方法/路由 | handler 输入与输出 | 具体动作 |
|---|---|---|
| `GET /api/user` | `{id,githubLogin,name,organizations:[{githubLogin,name}]}` | githubLogin 使用 principal.login，组织按名称排序，只返回当前成员组织 |
| `GET /api/user/organizations/default` | `{GitHubLogin:"org",Messages:[]}` | 按名称排序取第一个组织；无组织为空字符串；大小写按该 SHA 类型 |
| `GET /api/capabilities` | `{capabilities:[...]}` | 首版只在加密批处理通过后返回 `{"capability":"batch-encrypt"}`；不声明 schema4/Policy/AI；journal 不通过此接口协商 |
| `GET /api/user/stacks` | `{stacks:[{id,orgName,projectName,stackName,lastUpdate?,resourceCount?,links:{self}}],continuationToken?}` | 仅 current names、未删除 stacks；project/organization/tagName/tagValue 过滤；每页100；按 stack UUID稳定排序 |
| `HEAD P` | 200/404 | 有任一可访问 current stack 的 project 视为存在 |
| `POST P` | `{stackName,tags?,teams?,state?,config?}` → `{messages:[]}` | 验证名字；同名/保留别名409；创建空 state 或导入给定 state；详见初始化算法 |
| `GET S` | `{id,orgName,projectName,stackName,activeUpdate,version,tags,currentOperation?}` | activeUpdate 仅返回有效 running update ID，否则空；不返回 config，防止触发 ESC 路径 |
| `PATCH S/tags` | 标签 map → 204 | 整体替换；活跃更新时409；保留任意合法 Pulumi tag 名称 |
| `DELETE S?force=true/false` | 无 body → 204 | 活跃更新409；非 force 时只有 root stack 资源可删除，其他资源包括 provider/component 均算有资源；pending operations 非空也拒绝；设 deleted_at 并移除名字，不删历史/key |
| `GET S/export[/{version}]` | UntypedDeployment | 当前读 head；历史读指定非预览 update 的固定最终描述符；version不存在404；不泄漏其他 stack state |
| `POST S/import` | UntypedDeployment → `{updateId:"uuid"}` | 一次服务端事务完成导入，状态 succeeded；后续 GET U 返回终态。不是 `{updateID}` |
| `GET S/updates` | `{updates:[UpdateInfo...]}` | page>=1，pageSize默认100、最大100；按正式version降序，包括running/failed；不包含preview |
| `GET S/updates/latest` | `{info:UpdateInfo}` | 取最近已终结正式更新，找不到404；config/environment从该次 request取，import/rename继承先前配置 |
| `POST S/rename` | `{newName,newProject}` → 204 | 空字段沿用当前值；至少一项变化；同组织内执行，算法见后文 |
| `POST S/{update,preview,refresh,destroy}` | UpdateProgramRequest → `{updateID:"uuid",requiredPolicies:[],messages:[],aiSettings:{copilotIsEnabled:false}}` | 存请求，status=created；不锁住 stack；project name须匹配路径；preview路径或options.dryRun为true即dry_run |
| `POST U` | `{tags?,journalVersion?}` → StartUpdateResponse | 调用 Start，详见第6节 |
| `GET U` | `{status,events,continuationToken?}` | 状态与旧式stdout/stderr投影；import立即返回succeeded、events=[]、token省略 |
| `POST U/renew_lease` | `{duration:300,token:""}` → `{token,tokenExpiration}` | duration须1..300；服务端续租300s，防止客户端5min假设与短租不一致；忽略body遗留token，只用header |
| `PATCH U/checkpoint` | PatchUpdateCheckpointRequest → 204 | full模式，版本/状态保存算法见第7节 |
| `PATCH U/journalentries` | `{entries:[JournalEntry...]}` → 204 | 仅协商journal的非dry_run update；详见第8节 |
| `POST U/events/batch` | `{events:[EngineEvent...]}` → 204 | 不依赖 state mode；以client sequence去重，原子整批 |
| `GET U/events` | `{events:[...],continuationToken:string或null}` | type重复参数、urn、include_non_activated过滤；本实现事件均为activated，最后参数不改变结果 |
| `POST U/complete` | `{status:succeeded/failed/cancelled}` → 204 | 终结、冻结state描述符、释放写槽，详见第6节 |
| `POST U/cancel` | 普通token，无body → 204 | writer/admin取消；非lease鉴权；重复已cancelled成功，其他已终态409 |
| `POST S/encrypt`、`S/decrypt` | `{plaintext}`→`{ciphertext}`，反之 | JSON字节字段由Go自动base64；不要再额外encode一次 |
| `POST S/batch-encrypt` | `{plaintexts:[...]}`→`{ciphertexts:[...]}` | 数组顺序必须一致 |
| `POST S/batch-decrypt` | `{ciphertexts:[...]}`→`{plaintexts:{base64密文:base64明文}}` | map而不是数组；任一项失败整批返回错误 |
| `POST S/decrypt/log-decryption`、`.../log-batch-decryption` | `{secretName?或commandName?}` → 204 | 校验权限后写审计，不接收/保存明文 |

`GET /api/cli/version` 返回该SHA构建的合法semver作为`latestVersion,oldestWithoutWarning,latestDevVersion`；用上游 client.GetCLIVersionInfo 的类型做 fixture 校验。`GET /healthz` 固定200，`GET /readyz` 检查DB连接、迁移版本和根密钥可用性。其余 `/api/` 未实现路由返回404，不能落到GUI HTML。

名字使用上游 `tokens.ParseStackName`、`tokens.ValidateProjectName` 和 `validation.ValidateStackTags`；URL path逐段解码，拒绝解码后含 `/`、`.`、`..` 或NUL的名字。org由bootstrap创建，限制ASCII字母数字连字符、1..100字符。

列表游标格式固定为 `base64url(JSON).base64url(HMAC-SHA256)`，JSON包含`kind,principalID,scopeID,filterHash,lastPosition,generation,expiresAt`；HMAC key由根密钥派生独立purpose。有效期24h；篡改/跨用户/跨filter/过期返回400。stack列表位置是UUID；事件位置是ingest_order。不能把未授权org名字放进错误详情。

**6. Start、续租、Complete、Cancel 的逐步实现**

core方法签名固定为以下职责；其中 Actor 包含token ID、principal ID与scope，不包含可打印token字符串：

```text
Start(ctx, actor, stackID, updateID, req) (apitype.StartUpdateResponse, error)
Renew(ctx, lease, stackID, updateID, req) (apitype.RenewUpdateLeaseResponse, error)
Complete(ctx, lease, stackID, updateID, status) error
Cancel(ctx, actor, stackID, updateID) error
ResolveHead(ctx, stackID) (raw []byte, descriptor Head, err error)
```

`Start` 算法：

```text
1. 解析 Start 请求，规范化并计算 requestDigest；tags nil与空map必须区分。
2. 若update已running且由同一actor token发起、相同digest：
   验证权限/lease未过期后解密start_response_encrypted，返回原响应，不改version/fence。
   digest不同→409；已终态→409。
3. 调用ResolveHead获得当前完整字节及head.generation；在锁外准备候选snapshot、
   随机32字节lease、lease摘要、加密后的Start响应。
4. BEGIN；锁stack、该update、head；重新读取权限和数据库当前时间。
5. 检查stack未删除、update属于stack且status=created。
6. 若active update未过期→409；若已过期，ROLLBACK当前事务，
   在独立短事务内重新锁住stack、检查过期、执行ExpireLocked并COMMIT，
   再从第3步解析，不能使用旧base。
7. 若head.generation与第3步不同→ROLLBACK重做，最多5次，之后503。
8. 将ResolveHead生成的完整snapshot插入snapshots，作为本update base。
   只有已确认语义等价才可以将旧journal head压实为此snapshot；增加head.generation。
9. stacks.fence++；update.fence=新值，lease_generation=service_meta.generation。
10. 非dry_run：last_version++并分配；dry_run使用当前last_version。
11. 选择mode：dry_run=none；否则enable_journal且客户端journalVersion>=1→journal；
    否则enable_delta→delta；否则full。mode开始后不变。
12. status=running；lease_expiry=dbNow+300s；保存token摘要、Start request摘要、加密响应。
13. 只有req.tags非nil才整体替换tags。stacks.active_update_id=id。
14. COMMIT，成功后返回固定Start响应。
```

ResolveHead与事务必须重查的原因：重放可能较慢，锁不能一直占用；在重放期间其他写入可能已发生。步骤6中过期处理应独立短事务提交后再重试，不能不断回滚过期处理导致死循环。

`ValidateLeaseLocked` 每个state写必须调用，顺序：digest常量时间匹配 → 路径归属 → 创建者token未撤销且权限仍在 → service generation一致 → status=running → active_update_id一致 → fence一致 → dbNow<expiry。任一失败403，不能先写后检查。

`Renew`：锁stack/update，ValidateLeaseLocked，expiry=dbNow+300s，提交后返回**同一个token**及Unix秒expiration。不轮换token，避免客户端在响应丢失后失去续租凭证。lease不能用于读取别的stack或secret解密。

`Complete`：先验证lease摘要匹配、路径归属、创建者当前权限及service generation，再查complete_status。相同status的重复请求返回204，不要求租约仍在300s内，但创建者权限及service generation必须仍有效；不同status409。首次Complete执行ValidateLeaseLocked；full/delta把当前snapshot设为final_snapshot；journal冻结count并保留base+完整日志，插入`materialize:{updateID}:{count}`任务。设置terminal status、complete_status、ended_at、event_closed_at=now+30s，清active_update并增加stack.fence；原子提交。不能把journal物化失败当成可以返回旧state的理由。

`ExpireLocked/Cancel`：将running update置failed/cancelled，reason写lease_expired/user_cancelled；冻结相同的终态state描述符，journal安排materialize；保留已ACK state；清active update、fence++。过期worker每次取候选ID，再逐stack按同一锁序检查并处理；不能依赖worker及时运行，Start/write本身必须校验租期。

预览任何checkpoint/journal写均409，events允许。Start已经分配的失败version不回收。Create接口没有通用幂等键，相同payload两次创建得到两个ID；不要误合并用户连续两次相同up。

**7. State 的初始化、全量写和读取**

空stack使用真正可反序列化的空deployment，不要返回空HTTP或`{}`。构造 `apitype.ManifestV1{Time:UTC now,Version:<固定CLI semver>}`，Magic用其`NewMagic()`；deployment含manifest、resources空数组、pending_operations空数组，envelope为version=3。首次service secrets manager信息可由CLI后续写入，无需伪造加密provider。

注意字段是 `pending_operations`、`secrets_providers`；不是按Go名字推断出的 pendingOperations/secretsProviders。使用固定上游类型读取，但保存原始字节，不丢未知普通字段。

`SaveFull`：

```text
1. 解码 {isInvalid,version,features?,deployment?}。
2. version必须3；features必须空；检查实际新feature字段，出现则422。
3. deployment缺失且isInvalid=false→400。
   deployment缺失且isInvalid=true：复制当前snapshot内容并置is_invalid=true，不清空state。
   deployment存在：保留RawMessage，构造UntypedDeployment字节；明确存is_invalid。
4. 计算sha256，生成snapshot UUID。
5. BEGIN；锁stack/update/head；ValidateLeaseLocked；要求mode=full。
6. 插入不可变snapshot，head={snapshotID,NULL,0,generation+1}；COMMIT→204。
```

无法在旧式full协议中推导客户端因果顺序：没有sequenceNumber，服务端只保证原版CLI的单writer串行请求工作方式。不要声称实现了任意乱序下的exactly-once。对未知恢复状态不能“自动修复”或清除pending；`is_invalid`时export仍提供原始数据以便恢复，GUI显示异常，新的非preview写Start返回409直到显式import修复。

`ResolveHead`：在一个REPEATABLE READ只读事务中取head、base snapshot及journal<=watermark，然后结束事务；校验snapshot原文sha256；full直接返回字节；journal传给Replay。对该次读取开始后新提交的数据不作承诺，但**读取开始前已ACK的写必须包含**。hash不符500并报警，禁止退回上一版。

`Import`：格式/功能检查→读取当前head并在锁外验证→事务内重查generation及无active writer→分配version、建立kind=import且succeeded的update→写snapshot并切head→返回`updateId`。import失败不改head。新状态中的service密文必须属于目标stack；导入其他后端密文需要迁移工具先重加密，不能自动假定可解。passphrase等外部provider state可以opaque保存，但不在服务端尝试解密。

历史export：每个terminal update只能引用它自己冻结的final_snapshot或base+终态journal_count，不能读取stack当前head。正在running的指定version返回409，避免用户以为这是稳定历史。历史snapshot不因rename而批量重写。

`UpdateInfo` 必填 kind/startTime/message/environment/config/result/endTime/version。running result=`in-progress`，created=`not-started`，succeeded=`succeeded`，failed/cancelled=`failed`；时间Unix秒。resourceChanges仅在有可信summaryEvent时输出，无数据时省略，不能编造0。资源数量从状态投影计算，计数规则统一包含root/provider/component，GUI另显示custom资源数。

**8. Journal v1：完整持久化与重放规格**

它是v1快速更新的必做部分。不能只实现HTTP接收、不实现export恢复。kind固定0..7：begin、success、failure、refresh-success、outputs、write、secrets-manager、rebuilt-base-state。

`AppendJournal`：

```text
1. 校验条目数/体积/version=1/kind范围/sequenceID>0/operationID>=0。
2. 用保留数字精度的规范JSON生成payload与digest；批内同sequenceID同payload只保留一次，
   同sequenceID不同payload→409；保留第一次出现顺序。
3. 锁外读取当前已提交日志，检查引用和kind必需字段；准备候选验证结果。
4. BEGIN，锁stack/update/head，ValidateLeaseLocked，要求mode=journal。
5. 逐条查已有sequenceID：同digest跳过，不同digest整批409。
   已存在条目不重新获得ingest_order。
6. 如第3步的journal_count已变化，ROLLBACK重做；不能用旧缓存证明新批次有效。
7. 对新增条目按输入顺序分配 ++journal_count，插入journal_entries。
8. 如果没有新增条目，COMMIT→204，不改head generation。
9. head.snapshot_id=update.base_snapshot_id；head.journal_update_id=update.id；
   head.journal_upto=journal_count；generation++。
10. COMMIT后才204。提交失败必须丢弃未提交的验证/重放缓存。
```

接收顺序是`ingest_order`，唯一身份是`sequenceID`，资源操作配对是`operationID`，三者不同。客户端先生成sequenceID再序列化/入队，不能要求sequenceID连续，也不能因收到12后再收到11就丢掉11。固定SHA的HTTP journal发送器按批串行发送；对照上游SnapshotJournaler实际接收的条目顺序来验证，不重新按数值排序“纠正”它。

轻量验证器维护：base资源长度、已begin operation、已经产生新resource的operation映射、终止性rebuilt标记。`removeOld/pendingReplacementOld/deleteOld`为非负旧资源索引且在当前base范围；`removeNew/pendingReplacementNew/deleteNew`必须指向已产生resource的operation；未知引用400。kind=5必须newSnapshot非nil，kind=6必须secretsProvider非nil；kind=7需要按上游重放规则验证。验证器不是另写一套资源合并算法。

安全基线实现允许重建并试放整个candidate journal验证，再优化成缓存的引用校验；**达到性能阶段前必须去掉每批扫描整个stack的行为**。优化只缓存提交后的数据，cache key包含update ID和journal_count；跨实例或缓存丢失时从DB重建。测试版本可通过开关强制每次冷缓存，结果须一致。

`Replay(base, entries)` 必须这样封装：

```go
// 示意代码；需要补上输入校验和错误包装。
func Replay(base apitype.DeploymentV3, entries []apitype.JournalEntry) (apitype.TypedDeployment, error) {
    // base必须深拷贝；上游会修改内部map/slice/指针。
    cloned := DeepCopyDeployment(base)
    r := backend.NewJournalReplayer(&cloned)
    for _, e := range entries { // DB ingest_order升序
        if err := r.Add(e); err != nil { return apitype.TypedDeployment{}, err }
    }
    return r.GenerateDeployment()
}
```

封装外围必须验证非nil字段和引用，防止上游假定合法输入时panic。最后一道recover只用于将意外panic转成500+报警；不能把panic变成空snapshot或成功。基线schema3下GenerateDeployment如果产生v4/features，返回明确不支持，不降级丢字段。

`Materialize(updateID,upto)`：读取固定base及<=upto完整日志→Replay→序列化→校验→插入snapshot和materializations唯一行。并发同任务用唯一键读取已提交结果；失败重试，不影响已保存journal。只有update已terminal且count=upto才设置final_snapshot_id。若当前head仍指向同update及upto，可以CAS压实head；否则不动head，避免覆盖下个update。

运行中可以缓存“这个watermark的完整导出结果”，但**不能把结果当新base并删除前半日志**，因为old索引、新operation映射仍指向原始运行上下文。v1完全不删除journal。下一次Start通过ResolveHead获得完整snapshot作为新的base，因此不依赖后台任务完成时机。

**9. Rename、secret 加密和浏览器鉴权**

Rename不要直接在所有字符串中替换stack名字。实现Raw JSON局部变换，保留未知字段：

1. ResolveHead并保存generation，确认无active update；构造旧URN→新URN映射。只重写属于旧project/stack的资源；root stack名字按`newProject-newStack`生成。
2. 修改resources及pending_operations.resource中的`urn,parent,dependencies,propertyDependencies,deletedWith,provider`；provider引用使用上游`providers.ParseReference/NewReference`保留provider ID；aliases作为历史URN保留。
3. 对inputs/outputs中的已识别resource-reference编码，只修改其urn；普通字符串即使形似URN也不修改。ciphertext内部不触碰。
4. service secrets provider state中的url/owner/project/stack更新为当前服务与新名字；配置密文和state密文绑定稳定stack UUID，不需重加密。旧历史provider路径通过旧名字别名允许decrypt，仍须鉴权。
5. 事务内重查generation、active update和名字唯一性；原current name改为alias，新增/激活新current name；写新snapshot，分配kind=rename的成功history version，更新head；提交。

参考`pkg/resource/edit/operations.go:147`，但不能不检查就复制：该SHA存在对新字段的处理细节，typed重写也可能丢未知字段。v1拒绝v4字段使rename范围明确。必须有“应用字符串不变”“provider ID不变”“old历史secret仍能解密”测试。

根密钥由`backendctl bootstrap`生成到文件，数据库只保存包裹后的每stack数据密钥DEK。v1实现FileKEK，未来KMS只替换Wrap/Unwrap接口。派生用途键统一`HMAC-SHA256(master, "pulumi-backend/v1/"+purpose)`，purpose分别为`wrap`、`start-response`、`cursor`，不可混用。数据库主机备份与根密钥分开保存。

加密实现固定AES-256-GCM，用Go标准库`crypto/aes`和`crypto/cipher`，nonce用`crypto/rand`生成12字节；每次Encrypt新nonce。认证加密的标准库行为参考[Go cipher 文档](https://pkg.go.dev/crypto/cipher#NewGCM)。不要自行实现算法。

密文二进制格式：`PB01(4字节) | keyVersion(uint32大端) | nonce(12字节) | GCM ciphertext+tag`；AAD=`pb1:stack:<UUID>:key:<version>`。HTTP JSON中这个整个byte slice仅base64一次。wrap的AAD=`wrap:<stackUUID>:<version>:<kekID>`；Start响应加密AAD=`start:<updateUUID>:<generation>`。任何header/version/tag篡改都返回400通用Invalid ciphertext，不泄漏部分明文。

每stack初始随机DEK=32字节、version=1。`encrypt`用active key；`decrypt`按密文版本取对应key，并检查当前stack UUID的AAD。`batch-decrypt` map键必须是请求字节的base64原文。实现key轮换命令时新增版本并保留旧版本；不在v1做自动删除密钥。

UI登录：GET `/login`显示token表单；POST `/console/api/session`只接受同源Origin，通过token验证后生成32字节session ID和CSRF token，DB存摘要及源token ID；设置HttpOnly、SameSite=Lax、Path=/、8h cookie，正式HTTPS用Secure。浏览器不把API token写localStorage。每次session请求检查源token和membership，注销删除session。

需要登录的页面未登录跳转/login；API未登录返回401 JSON。所有有副作用的Web请求检查同源Origin和`X-CSRF-Token`。登录本身也必须校验Origin，避免login CSRF。模板使用`html/template`，JS输出日志只能用textContent，不能innerHTML。不要把未审计的event当HTML渲染。

**10. Events 与 GUI：固定页面、数据接口和显示规则**

事件上传完整批次一个事务：对update加锁，同sequence同digest跳过、不同digest409；新事件`++event_count`得到ingest_order。不要用全局自增ID作“提交顺序”，因为不同事务可能先分配后提交。

running时有效lease可写events；terminal时仅允许相同lease摘要、同generation且源token权限仍有效，在ended_at+30s内继续上传；仅事件允许这个窗口，state/renew不允许。Cancel/expiry立即封口，Complete正常收尾30s。超窗新事件409，超窗已持久事件的同内容重放可以204，不能改动state。

GET events按ingest_order分页100项，cursor记录扫描位置与filters；前端按sequence排序展示。没有新事件但未封口时返回非null cursor；封口且读尽返回null。旧式GET U从stdoutEvent/diagnosticEvent投影`{index,kind,fields:{text}}`，使用同一ingest cursor；对不产生文本的事件也推进扫描位置，避免死循环。

结构化事件的summaryEvent用于统计；没有summary时显示“统计不可用”。terminal与event收尾是两个状态，GUI在窗口内显示“更新已结束，日志仍在收集”；封口后不能声称CLI从未传来的日志已被找回。

页面与接口固定如下，Web接口可以共享core读取函数，但不要将API token暴露给页面：

| 页面 | Web API | 必须显示 |
|---|---|---|
| `/`、`/stacks` | `GET /console/api/stacks?cursor=` | org/project/stack、version、状态、资源数、更新时间；分页和project筛选 |
| `/{org}/{project}/{stack}` | `GET /console/api/stacks/{id}` | 当前state generation、running update、outputs、历史入口、只读资源列表 |
| `.../resources` | `GET /console/api/stacks/{id}/resources?cursor=` | URN/type/ID/parent/dependencies；每页100；点开看脱敏inputs/outputs |
| `.../updates/{version}` | `GET /console/api/stacks/{id}/updates/{version}` | 作者、kind、status、时间、message、config secret标记、资源统计、前后状态diff |
| `.../previews/{updateID}` | `GET /console/api/updates/{id}` | preview状态及事件，不显示为已提交state |
| 上述update详情内事件区 | `GET /console/api/updates/{id}/events?cursor=` | 每2s轮询，client sequence排序，重复事件不重复显示，网络错误可恢复 |

Web JSON统一`{data:<对象或数组>,nextCursor?:string}`，错误使用前述code/message。stack summary包含`id,org,project,name,version,headGeneration,status,resourceCount,updatedAt`；update包含`id,version,kind,dryRun,status,startedAt,endedAt,message,actor,eventsClosed`；resources条目包含`urn,type,id,custom,parent,dependencies,inputs,outputs`。

使用相同ResolveHead生成资源视图；v1不做异步索引，这样不需要实现另一个一致性系统。HTML页面有加载、空列表、403/404、网络错误四种状态。CSS固定顶部产品名、左侧stack导航、主区tabs（Overview/Resources/History），状态用文字+颜色，表格支持窄屏横滚，日志等宽字体。

资源diff按URN+ID及delete标记匹配，不能仅用URN，因为replacement期间可能同时存在旧新资源。输出create/delete/update/same及JSON属性路径差异；secret属性显示`[secret]`，不显示密文也不自动解密。普通配置secret=true只显示标记。识别Pulumi signature key及SecretSig递归隐藏值；`additionalSecretOutputs`也应隐藏对应属性。自由文本日志无法可靠反推未标记secret，限授权用户读取，报告中不要宣称能自动清除所有意外泄漏。

GUI首版只读，不提供“回滚到此版本”按钮；历史state回滚不是云资源回滚。管理和token撤销通过backendctl执行，减少Web写接口。

**11. Worker、运维命令和启动方式**

jobs获取使用短事务`SELECT ... FOR UPDATE SKIP LOCKED`，把pending或locked_until过期的任务置running、attempts++、locked_until=now+120s；锁外执行；成功置done，失败回pending，available_at延迟`min(2^attempts,300)s`，错误去敏保存。唯一job_key保证重复complete不建立不同逻辑任务。工作超过120s每30s续任务锁，但最终发布仍依赖业务CAS/唯一约束，不依赖“只有一个worker”。

`backend migrate`：取得固定数据库advisory lock，按序执行迁移并记录版本；重复执行为no-op。`backend serve`：启动时版本不匹配则退出，不能自动改生产schema。`backend worker`独立进程，退出不影响已提交state读取。

必须实现以下backendctl子命令：

```text
bootstrap --org demo --user admin --token-file .dev/admin.token
token create --user admin --write --decrypt --out .dev/cli.token
token revoke --id <uuid>
member set --org demo --user alice --role reader|writer|admin --decrypt=true|false
restore-generation
```

bootstrap创建service generation、principal、org、admin membership和token；已有同名对象不重复创建，根密钥文件存在则不覆盖。token输出到0600文件，不默认打印到stdout。撤销事务提交后所有后续lease检查都失败；worker随后终结关联running update。测试撤销与写请求的锁竞争，确保不存在“撤销成功后开始的新写仍成功”。

最终仓库必须支持：

```bash
make dev-init       # 生成 .dev 根密钥/本地密码；不覆盖已有值
make build         # 编译 backend、backendctl、测试用CLI
docker compose up -d db
make migrate
make bootstrap
docker compose up -d api worker
make smoke
```

Compose数据库使用postgres:17-alpine，首次验证后把镜像digest写入仓库；只把API端口绑定127.0.0.1:8080，数据库默认不向宿主机暴露。db用持久volume；api/worker同一只读根密钥挂载、同数据库。dev-init生成的.env只在开发环境加载、加入.gitignore。生产TLS在反向代理终止，PUBLIC_URL与CONSOLE_URL为https。

手动使用示例（最终实现应通过；先在另一终端执行`make dev-world`，该目标前台启动监听127.0.0.1:7071的测试world，数据目录为.dev/world）：

```bash
export PULUMI_ACCESS_TOKEN="$(cat .dev/cli.token)"
export PULUMI_CONSOLE_DOMAIN=localhost:8080
./bin/pulumi login http://localhost:8080
# 在 tests/projects/basic 中：
../../../bin/pulumi stack init demo/basic/dev
../../../bin/pulumi config set worldURL http://localhost:7071
../../../bin/pulumi config set message hello
../../../bin/pulumi config set password example-test-only --secret
../../../bin/pulumi preview --non-interactive
../../../bin/pulumi up --yes --non-interactive
```

自动测试必须另外设置临时PULUMI_HOME，不修改实现者真实credentials。日志只记录requestID、route名称、stack/update UUID、状态码、耗时、字节数；不记录Authorization、请求体、解密响应。metrics至少包含route延迟、ACK延迟、lease冲突/过期、journal条数、重放耗时、job失败、DB锁超时；不能把URN/stack名等高基数字段作为metric label。

备份脚本输出DB逻辑备份及manifest，manifest记录schema版本、代码SHA、根密钥ID（不含密钥）；根密钥走单独受控备份。restore脚本在隔离环境恢复DB+key，运行restore-generation，终结旧running update，再执行export与旧secret解密验收；不允许直接把恢复后的旧lease重新放行。数据库`fsync=on,synchronous_commit=on`，不能用UNLOGGED表保存state/journal。[PostgreSQL建表文档](https://www.postgresql.org/docs/18/sql-createtable.html)明确UNLOGGED表不具备崩溃安全性。

**12. 验证用测试 provider：不要依赖真实云资源测核心正确性**

实现名为`backendtest`、版本0.0.1的Go provider，二进制为`pulumi-resource-backendtest`。参考固定源码`tests/testprovider/main.go`的provider.Main、Configure及gRPC接口，复用protobuf类型。测试程序用Go SDK注册`backendtest:index:Item`，不需要生成完整SDK。

Item输入固定：`name:string`、`value:string`、`replaceKey:string`、`secretValue:string(optional)`。配置`worldURL`指向独立world服务。Check校验name必填并回传inputs；Diff的value变化为update，replaceKey变化为replace，其他相同无变化；Create生成UUID并持久化；Read读取或返回空ID表示已不存在；Update保留ID修改value；Delete幂等删除；Import通过Read获取外部world里预建对象。

world服务与backend数据库隔离，按附录D的单进程文件方案保存`id,name,value,replaceKey,secretValue`，HTTP提供CRUD和测试控制；只监听测试网络。错误注入由world控制：Create持久化后暂停响应、下一次Update失败、Delete失败、某资源Read返回不存在。控制key按testRunID隔离，不能让并行测试互相影响。

测试项目basic有一个Item及root outputs；dependencies有A→B→C；replacement验证旧新资源同时存在；reference创建两个stack验证StackReference；large按配置N批量创建Items。provider的真实副作用必须在CLI被SIGKILL后继续存在，否则不能证明pending operations恢复正确。

**13. 必须逐项写成自动化测试的用例**

下表的名称就是测试文件/函数命名依据。每个用例独立org/stack、随机种子固定并打印。测试报告必须列出每个ID的pass/fail，不能只显示一句“go test通过”。

| ID | 输入/故障动作 | 精确断言 |
|---|---|---|
| C01 身份 | 无token、错误token、合法token依次GET user | 401、401、200；githubLogin非空；组织不越权 |
| C02 空stack | create→GET→export→CLI select/preview | envelope schema3且CLI可解析；version=0；activeUpdate为空；重复create409 |
| C03 生命周期 | Create/Start/full-write/Complete | 同一ID后续路径均/update；version=1；终态后lease写403；history记录succeeded |
| C04 preview | preview和update+dryRun分别Start，尝试写state | 均409；head原文字节不变；正式history version不增加 |
| C05 导入 | POST合法state→立即GET U | 返回键updateId；status=succeeded；token省略；export语义等于导入；错误state不改变head |
| C06 gzip/JSON | 正常gzip、损坏gzip、超解压限制、两个JSON值、重复key | 200/204、400、413、400、400；失败时DB未新增任何state |
| C07 latest | 无history；完成一次带secret config的up | 先404，后{info:{config,...}}；secret ciphertext不转成明文 |
| L01 并发Start | 两API实例对同stack两个update同时Start | 恰好一个200、一个409；只有一行running；不同stack不互相阻塞 |
| L02 重复Start | 同update同body重复、再修改tags重复 | 同body响应version/token一致；不同body409；last_version只加1 |
| L03 租约 | DB测试时钟推进超过300s，旧token续租/写；启动新update | 旧请求403；新Start成功；新fence更大；旧写不能复活 |
| L04 迟到旧写 | hook暂停旧writer提交前，Cancel并新Start，再释放 | 在重新检查fence处403，head属于新writer |
| L05 完成重放 | complete响应丢失后同status重试，再不同status | 204、409；history只有一条；过期不阻止查询同complete receipt，但撤销token会拒绝 |
| F01 full崩溃 | 保存收到204后杀API，另实例export | hash与已ACK原文一致；不能空state或上一版本 |
| F02 未知提交 | DB commit后丢HTTP响应 | 重试不造成错误state；允许物理snapshot多份，但当前语义正确；明确full无通用乱序保证 |
| J01 三条基本日志 | begin(op1)、success(op1,resourceR)、outputs(R) | export有R最终outputs，pending为空；不按operationID丢success |
| J02 pending | begin已204后provider创建成功，success前杀CLI | world有资源；export保留pending create；无伪造success；后续显式refresh/import恢复 |
| J03 重放 | 同批两次、同seq不同payload、先seq12后11的独立操作 | 原记录不重复、冲突409、合法逆序ID接受；按ingest顺序与参考重放语义一致 |
| J04 原子413 | 大批触发413，客户端递归拆分后重试 | 大批不写任何条目；拆分后每个sequence恰好一条；最终export正确 |
| J05 引用 | out-of-range removeOld、未建立removeNew、缺secretsProvider | 400/422且整批不写；服务无panic；不能默认索引0 |
| J06 高风险kind | replacement、refresh-success、write、secrets-manager、rebuilt-base-state | 与上游参考输入的语义结果一致；old/new映射未错位 |
| J07 cache/压实 | 每批清缓存；complete后停worker；启动下一次up | export仍正确；下次base是终态完整state；旧worker恢复后不得覆盖新head |
| E01 事件乱序 | seq100先提交、seq50后提交，中间做一次分页 | 两个都能被读到；UI按50/100展示；重复100不重复显示 |
| E02 封口 | complete后10s上传，31s再传新事件 | 10s成功，31s409；封口前无新事件cursor仍非null，封口后读尽null |
| S01 secrets | 单条/批量加解密、重复密文、空字符串 | 原文往返一致；batch map键正确；空字符串合法；数据库和日志无测试secret明文 |
| S02 租户 | A组织token读/写/解密B、A密文发给同名Bstack | 拒绝；不能通过UUID、alias、旧lease绕过 |
| S03 撤销 | revoke token或移除write/decrypt权限后请求 | 后续请求立即拒绝；session同步失效；旧lease不绕过当前权限 |
| R01 rename | 有依赖/provider/secret的stack rename后preview | 资源无意外替换；provider ID不变；当前及旧历史secret可解；普通应用字符串不改 |
| R02 删除 | 非force有资源、仅root、force、重建同名stack | 正确错误文案；仅root可删；force不删world资源；重建UUID不同，旧密文不通用 |
| U01 GUI | CLI permalink、刷新、不同用户、含HTML诊断文本 | 正确页面；权限正确；XSS字符串作为文本；secret没有出现在DOM/网络明文字段 |
| B01 DB重启 | journal已ACK后重启Postgres并启动新API | 完整重放成功；只杀API不能替代此用例 |
| B02 备份恢复 | 备份DB+key→新实例恢复→更换generation | 历史export/secret可用；旧token lease不能写；错误key启动失败或解密明确失败 |

测试L03使用注入Clock接口推进时间，另留一个真实300s续租集成测试；不能把服务租期改短而仍声称兼容原CLI的5min假设。L04故障hook放在锁外候选计算后、再次加锁前，避免测试自身持有锁导致Cancel永远等待。

Schema校验和state判据：checkpoint/delta对比原文字节及sha256；journal对比resources/inputs/outputs/依赖/provider/pending/secrets provider语义。只归一化manifest时间及无语义的pending集合排列；资源数组不能任意排序。两次secret加密可能nonce不同，比较解密值及secret标记，不直接比较密文字节。上游reference与我们的实现共享逻辑时，仍需world资源清单这个独立判据，避免同一bug自证正确。

**14. 故障注入和测试执行契约**

测试构建在以下命名点提供可阻塞hook：`after_candidate_before_lock`、`before_db_commit`、`after_db_commit_before_response`、`after_journal_ack_before_provider_success`（由provider/world配合）、`after_complete_before_materialize`。控制服务由testRunID+hook名选择，支持block/release/close-connection；正式构建路由不存在。

每个测试记录：代码SHA、schema版本、CLI SHA、seed、API实例、请求ID、入库前后head descriptor、HTTP结果、export hash、world资源清单。不得记录真实token/secret；测试secret使用固定canary以检查意外日志泄漏。失败保存脱敏trace和DB测试快照到test-results，不自动删除证据。

最终Makefile必须有这些目标，缺一个就不算完成：

```text
make test-unit          # validators、加密envelope、游标、状态机纯逻辑
make test-contract      # 上游client对HTTP服务；所有C类
make test-integration   # 真Postgres、两API；L/F/J/E类
make test-e2e           # 固定bin/pulumi+测试provider的完整CLI链路
make test-security      # S类及登录/CSRF/XSS
make test-web           # 浏览器检查permalink和DOM，保存失败截图到测试目录
make test-fault         # 所有故障点、DB重启、缓存丢弃
make test-restore       # B02，隔离库，不操作用户真实数据库
make bench             # 输出CSV/JSON、分位数与环境manifest
make verify            # 顺序执行上述非bench目标，任何失败非0退出
```

单元/集成Go测试使用`go test -race`；E2E每个命令设超时和完整stderr采集，进程结束后检查无孤儿provider。UI浏览器测试工具可以单独放测试容器，不引入产品前端构建依赖。所有清理根据testRunID定位，禁止按进程名或扫描所有Docker容器删除。

**15. 性能测试：固定用例与结果格式**

先做功能，再测journal收益。基准组固定为`full-api`、`journal-api`、`s3-diy`，delta实现后加入`delta-api`；S3组使用固定版本的测试S3兼容服务并记录镜像digest，不把它的结果直接外推成AWS S3性能。

资源N=100/1,000/10,000；每资源有效state约1KiB或10KiB；变更率0/1%/10%/100%；RTT=1/20/80ms；并发stack=1/10/50。第一批只运行N=1,000和10,000、1KiB、1%变更、20ms RTT、并发1/10，之后扩充。用故障代理或网络命名空间注入双向延迟，并实际测ping/请求RTT，不能把单向delay误当RTT。

每组先warmup 3次，再测30次；同一随机种子资源集、同provider、相同数据库持久化设置。不能关闭checkpoint、fsync或加密来获得更好成绩。CSV每行字段：`mode,N,stateBytes,changeRatio,rttMs,concurrency,run,totalMs,persistWaitMs,uploadBytes,requestCount,ackP95Ms,exportColdMs,exportWarmMs,dbWalBytes,maxRssBytes`。

发布目标：10k资源/1%变化/20ms RTT时，journal上传字节较full减少≥80%、状态持久化等待减少≥50%；并发10、单批<=256KiB，journal ACK p95≤150ms；10k资源热export p95≤1s。都是待测目标，达不到要提交profile和原因，不能只改统计口径。冷重放单独报告；真实云资源总耗时只作补充，不拿云API波动解释存储错误。

**16. 实现任务顺序：每一步有出口，不能越级宣布完成**

| 顺序 | 新增/完成的文件 | 必须通过后才能继续 |
|---|---|---|
| T01 工程 | main/config/Compose/Makefile/migration、固定submodule | build、迁移可重复执行、readyz、DDL约束检查 |
| T02 身份 | bootstrap/token/authorization/session | C01、S02基本隔离；无鉴权不能访问任何state |
| T03 stack/state | stacks/head/snapshots/export/import/tags | C02/C05/C06/C07；空stack能被原版CLI加载 |
| T04 update/full | Start/Renew/Complete/Cancel/expiry、full写 | C03/C04、L01..L05、F01/F02；完整无secret CLI部署链 |
| T05 secrets/rename | keys/crypter/batch/alias/Raw rename | S01..S03、R01/R02；默认secret CLI部署链 |
| T06 events/history | events/cursors/history/summary | E01/E02；无事件不能伪造成功统计 |
| T07 GUI | templates/static/viewmodels | U01、test-web；三个CLI permalink全部有效 |
| T08 journal正确性 | append/replayer/materialize/jobs | J01..J07全部通过；开关关闭时仍可读取已有journal |
| T09 journal性能 | 提交后缓存、引用校验优化、bench | cold/cache结果一致；基准达到或如实报告未达目标 |
| T10 运维交付 | backup/restore、审计、文档 | B01/B02、make verify、两实例运行；不能只交本地demo |

实现者每完成一行，应更新`docs/test-results.md`，写真实命令、退出码、覆盖用例ID和未通过项；禁止只写“已添加测试”。测试provider在T03前完成基本CRUD，在T04前完成故障阻塞功能。

回退规则固定：journal故障时关闭对**新update**的协商，现有journal update仍需被识别和恢复；不得删除journal表、把现有mode改full，或让旧版本程序读取不了已写数据。迁移回S3需要从目标最新状态重新导出并正确处理密钥；不能拿迁移前旧备份回滚已经变化的云资源记录。

**17. 可选delta扩展的最低要求**

增加`PATCH U/checkpointverbatim`与`checkpointdelta`，advertise `delta-checkpoint-uploads-v2` version=2，cutoff初值1MiB。Start仅在journal未选中时选择delta。首次verbatim sequence=1；之后sequence=last+1；重复旧sequence只有目标hash相同才返回原成功receipt，否则409。收到未来跳号409。

delta解析为gotextdiff.TextEdit，验证位置边界和不重叠后在原始UTF-8字节上ApplyEdits，校验目标SHA256；成功事务同时写snapshot、receipt、delta_sequence、head。verbatim用嵌入的整个UntypedDeployment原始字节，不作格式化。失败不消费序号；delta已提交但响应丢失后，同sequence的verbatim若目标hash相同返回原成功，不重复应用。未知delta必须拒绝，不能当JSON Patch处理。

最少增加5个测试：Unicode字节偏移、hash不符、重复序号、跳号、delta→同序号verbatim回退。这个模块未实现时能力开关必须关闭，v1其余功能仍可验收。

**18. 本次报告自身已验证什么**

本轮实际将第3节DDL在临时 `postgres:17-alpine` 中执行成功，并测试了五项数据库约束：同stack不能两个running update、head不能引用其他stack snapshot、每stack只能一个current name、journal sequence唯一、event sequence不得为负；测试数据事务回滚，临时容器已清理。附录Python样例通过语法解析，三个JSON fixture通过JSON解析，正文SQL与实际执行文件一致。这证明迁移与样例的这些静态检查通过，不代表HTTP、CLI、journal重放或性能验收已通过。

源码复核入口：`pkg/backend/httpstate/client/client.go`（真实路由和鉴权调用）、`sdk/go/common/apitype/{updates,stacks,events,core,journal}.go`（JSON类型）、`pkg/backend/journal.go`（重放）、`pkg/engine/journal_snapshot.go`（ID与步骤关系）、`pkg/secrets/service/manager.go`（service provider密文约定）、`pkg/resource/edit/operations.go`（rename参考）、`tests/testprovider/main.go`（provider框架）。实现过程中与本规格有冲突时，以固定SHA的实际请求为兼容证据，新增回归测试并明确记录差异，不能无声“修正”客户端协议。

**附录A：Make目标的实际职责，避免启动脚本留空**

这些目标必须写成脚本，不要求使用者自己拼数据库连接串：

- `dev-init`：创建.gitignored的.dev；仅在文件不存在时生成32字节master.key及本地DB密码，chmod 0600；产生Compose使用的.env。BACKEND_DATABASE_URL在容器内指向`db:5432/backend`，不能写localhost。
- `build`：构建两个Go二进制、固定SHA的CLI及API Docker镜像。运行镜像包含`/app/backend`和`/app/backendctl`，默认ENTRYPOINT=`/app/backend`、CMD=`serve`。根密钥只由运行时挂载，不进入镜像。
- `migrate`：用`docker compose run --rm api migrate`执行。数据库不暴露宿主机端口，因此不能让host上的psql/二进制直连db这个容器DNS名。
- `bootstrap`：用`docker compose run --rm --user <宿主UID:GID> --entrypoint /app/backendctl`执行初始化与token create；额外挂载`.dev:/out`用于写admin.token和cli.token。API日常运行只挂载`.dev/master.key:/run/secrets/master.key:ro`，不挂载可写的整个.dev。
- `smoke`：从host运行附录B，API访问127.0.0.1:8080；之后运行basic CLI项目。确认数据库容器ready再迁移，ready等待有60s总超时并输出明确错误。
- `test-*`：为每次运行生成唯一Compose project名和独立volume，退出时只清理自己的资源。restore测试另开一个数据库，绝不DROP开发库。

README必须列明“这些命令从仓库根运行”。bootstrap创建两个文件；手工CLI使用cli.token，不把admin.token作日常凭证。

**附录B：可直接保存成 `scripts/smoke.py` 的协议样例**

此脚本只需Python标准库，在产品实现后运行；不是本轮已经执行的测试。它固定测试full模式，所以运行前设置BACKEND_ENABLE_JOURNAL=false、BACKEND_ENABLE_DELTA=false。把本脚本通过作为T04的一条出口，但不能替代provider E2E。

```python
import base64
import gzip
import hashlib
import json
import os
import time
import uuid
import urllib.request
from pathlib import Path

base = os.environ.get("BACKEND_PUBLIC_URL", "http://localhost:8080").rstrip("/")
token = Path(".dev/cli.token").read_text().strip()

def call(method, path, body=None, lease=None, compressed=False, expected=200):
    headers = {
        "Authorization": ("update-token " + lease) if lease else ("token " + token),
        "Accept": "application/vnd.pulumi+9",
        "Content-Type": "application/json",
    }
    data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
    if compressed:
        assert data is not None
        data = gzip.compress(data)
        headers["Content-Encoding"] = "gzip"
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=60) as response:
        assert response.status == expected, (method, path, response.status)
        raw = response.read()
    return json.loads(raw) if raw else None

who = call("GET", "/api/user")
assert who["githubLogin"]
name = "wire-" + uuid.uuid4().hex[:8]
p = "/api/stacks/demo/wire"
s = p + "/" + name
call("POST", p, {"stackName": name, "tags": {"test": "wire"}})
assert call("GET", s)["version"] == 0

create = call("POST", s + "/update", {
    "name": "wire", "runtime": "go", "main": "", "description": "wire test",
    "config": {},
    "options": {"dryRun": False, "parallel": 1, "color": "raw", "showNames": False},
    "metadata": {"message": "wire smoke", "environment": {}},
})
u = s + "/update/" + create["updateID"]
started = call("POST", u, {"journalVersion": 0, "tags": {"test": "wire"}})
lease = started["token"]
assert started["version"] == 1 and started.get("journalVersion", 0) == 0
assert call("GET", u).get("continuationToken") is not None

deployment = {
    "manifest": {
        "time": "2026-01-01T00:00:00Z", "version": "3.246.0",
        "magic": hashlib.sha256(b"3.246.0").hexdigest(),
    },
    "resources": [], "pending_operations": [],
}
call("PATCH", u + "/checkpoint", {
    "isInvalid": False, "version": 3, "deployment": deployment,
}, lease=lease, compressed=True, expected=204)
assert call("GET", s + "/export")["deployment"] == deployment

plain = base64.b64encode(b"test-secret-value").decode()
cipher = call("POST", s + "/encrypt", {"plaintext": plain})["ciphertext"]
assert call("POST", s + "/decrypt", {"ciphertext": cipher})["plaintext"] == plain
batch = call("POST", s + "/batch-decrypt", {"ciphertexts": [cipher]}, compressed=True)
assert batch["plaintexts"][cipher] == plain
assert call("POST", u + "/renew_lease", {"duration": 300, "token": ""}, lease=lease)["token"] == lease

call("POST", u + "/events/batch", {"events": [{
    "sequence": 0, "timestamp": int(time.time()),
    "stdoutEvent": {"message": "wire smoke", "color": "raw"},
}]}, lease=lease, compressed=True, expected=204)
assert len(call("GET", u + "/events")["events"]) == 1
call("POST", u + "/complete", {"status": "succeeded"}, lease=lease, expected=204)
call("POST", u + "/complete", {"status": "succeeded"}, lease=lease, expected=204)
assert call("GET", s)["activeUpdate"] == ""
assert call("GET", s + "/updates")["updates"][0]["result"] == "succeeded"

exported = call("GET", s + "/export/1")
imported = call("POST", s + "/import", exported, compressed=True)
result = call("GET", s + "/update/" + imported["updateId"])
assert result["status"] == "succeeded" and result.get("continuationToken") is None
call("DELETE", s + "?force=false", expected=204)
print("PASS: identity, stack, full state, lease, secrets, events, history, import")
```

**附录C：journal接收与重放的最小fixture**

用第7节空base，新建一个journal mode update，再分三次发送下列entries。时间相关manifest由重放器生成，不比较原始字节。注意base中没有资源，故removeOld/removeNew均为null。

```json
{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,"operation":{"type":"creating","resource":{"urn":"urn:pulumi:dev::wire::backendtest:index:Item::r","custom":true,"type":"backendtest:index:Item","inputs":{"name":"r"}}}}]}
```

第一批ACK后export的pending_operations含creating，resources不含已完成r。

```json
{"entries":[{"version":1,"kind":1,"sequenceID":2,"operationID":1,"removeOld":null,"removeNew":null,"state":{"urn":"urn:pulumi:dev::wire::backendtest:index:Item::r","custom":true,"id":"world-r","type":"backendtest:index:Item","inputs":{"name":"r"},"outputs":{"name":"r","value":"v1"}}}]}
```

第二批ACK后pending创建项消失，resources包含id=world-r、value=v1。第三批使用新operationID，但removeNew指向产生这个resource的operation=1：

```json
{"entries":[{"version":1,"kind":4,"sequenceID":3,"operationID":2,"removeOld":null,"removeNew":1,"state":{"urn":"urn:pulumi:dev::wire::backendtest:index:Item::r","custom":true,"id":"world-r","type":"backendtest:index:Item","inputs":{"name":"r"},"outputs":{"name":"r","value":"v2"}}}]}
```

第三批后只有一个world-r、value=v2；重复第三批不增加资源或journal_count。本fixture只验证journal协议和重放，不作为可部署的完整provider checkpoint：完整E2E的root/provider资源必须由真实CLI生成。

**附录D：basic 测试项目与provider接口清单**

`tests/projects/basic/Pulumi.yaml` 内容固定：

```yaml
name: basic
runtime: go
description: API backend compatibility fixture
```

该项目go.mod引用同一SHA的SDK，使用相对replace到`../../../third_party/pulumi/sdk`。`main.go`可以从下列内容开始；测试provider schema必须声明value输出字段，Configure声明AcceptSecrets，不能静默把secret丢掉：

```go
package main

import (
    "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
    "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type Item struct {
    pulumi.CustomResourceState
    Value pulumi.StringOutput `pulumi:"value"`
}

func main() {
    pulumi.Run(func(ctx *pulumi.Context) error {
        c := config.New(ctx, "")
        var provider pulumi.ProviderResourceState
        err := ctx.RegisterResource("pulumi:providers:backendtest", "test", pulumi.Map{
            "worldURL": pulumi.String(c.Require("worldURL")),
        }, &provider, pulumi.Version("0.0.1"))
        if err != nil { return err }
        var item Item
        err = ctx.RegisterResource("backendtest:index:Item", "r", pulumi.Map{
            "name": pulumi.String(ctx.Stack() + "-r"),
            "value": pulumi.String(c.Require("message")),
            "replaceKey": pulumi.String("initial"),
            "secretValue": c.RequireSecret("password"),
        }, &item, pulumi.Provider(&provider), pulumi.Version("0.0.1"))
        if err != nil { return err }
        ctx.Export("value", item.Value)
        return nil
    })
}
```

脚本应先配置`basic:worldURL`、`basic:message`及secret `basic:password`，再运行preview/up。手动示例也需先运行world服务并设置worldURL。provider安装进本次隔离PULUMI_HOME的`plugins/resource-backendtest-v0.0.1/pulumi-resource-backendtest`，chmod +x，不触碰用户真实插件目录。

测试provider除第12节CRUD外，必须实现：

| RPC | 具体返回 |
|---|---|
| GetPluginInfo | version=0.0.1 |
| GetSchema | JSON schema声明provider.worldURL；Item的四个输入和对应输出；name/value/replaceKey必填 |
| CheckConfig | 验证worldURL为测试环境允许的HTTP地址，返回原配置 |
| DiffConfig | worldURL变化返回需要replace的差异，其余none |
| Configure | 保存worldURL并返回AcceptSecrets=true；使用上游property编码工具，不直接把protobuf.Struct转map而丢掉secret包装 |
| Check | 输入缺name/value/replaceKey时返回对应CheckFailure；其余原样回传 |
| Diff | replaceKey变化→DIFF_SOME+replaces=[replaceKey]；value/secretValue变化→DIFF_SOME；其余DIFF_NONE |
| Create | preview=true时不写world、返回预测outputs；正式执行写world并返回非空UUID；secretValue保持secret属性 |
| Read | 根据req.ID读取world；不存在返回空ID；存在返回实际outputs和对应inputs |
| Update | preview只计算；正式执行保留ID更新world、返回outputs |
| Delete | world没有ID时也成功；故障开关开启则返回gRPC错误，不伪造成功 |

world服务仅测试用途、文件持久化；可用单进程持锁+临时文件写完fsync后rename实现，或单独数据库，但不能存内存后声称CLI崩溃时资源仍在。为减轻实现负担，固定v1测试world选择单进程文件实现：目录为`test-results/<runID>/world/`，一资源一JSON文件，CRUD通过mutex串行，删除时同步目录；测试只验证world进程存活时杀CLI，world自身断电耐久性不作为backend正确性证据。

最终验收不是“所有接口返回200”，而是：**固定原版CLI完成生命周期、故障后已ACK状态可恢复、默认secret可往返、GUI显示同一份权威状态、journal收益有可复现数据，且所有未支持项明确失败。**