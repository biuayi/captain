# captain 服务端权威设计文档

> 状态：**唯一权威基准（2026-05-19）**。本文取代并作废 `REQUIREMENTS.md`、`ARCHITECTURE.md`、`REQ-CHANGE-00*.md`、旧 `PROGRESS.md`/`HISTORY.md`（移出工作区，git 历史留底）。
> 演进策略：**保留已 30k 压测验证的骨架，按本文逐模块 reuse / refactor / replace**。
> 后续：本文 → 拆 200+ 实现级任务（writing-plans）→ 逐任务实现+自测+commit。

---

## 0. 场景与角色（定位收敛）

本平台**仅用于企业内部活动**。

| 角色 | 是谁 | 边界 |
|---|---|---|
| 超管 Super-Admin | 我们（开发者/平台方） | 只管活动方账号、平台配置、模板；不碰活动业务数据内容 |
| 活动方 Organizer | 举办活动的企业公司 | 多租户隔离；建/管活动、传员工名单、配流程内容、看/导记录；权限受超管开关控制 |
| 用户 Participant | 参与活动的企业员工 | 必须在活动方导入的员工名单内，强身份登录；只参与与查看本人记录 |

**关键定位影响**：参与者**非匿名**。原 `匿名 device_uuid / external 指纹` 公开参与模型**降级为 legacy**（仅 demo，受 `CAPTAIN_OPEN_PARTICIPATION` 开关，默认 off）。正式模型 = **名单白名单 + 强身份登录 → 参与者 JWT**。

---

## 1. 总体架构

- 后端 `captain`：Go 模块化单体，单进程多模块（**保留**）。
- 存储：PostgreSQL（事实来源）+ Redis（热路径/限流/计数/会话态）+ NATS JetStream（持久异步）+ 对象存储（local FS / 阿里云 OSS，接口隔离，**保留**）。
- 三接口域（**保留前缀**）：

| 域 | 前缀 | 调用方 | 鉴权 |
|---|---|---|---|
| participation | `/api/v1/p` | 用户(H5) / 大屏 | 参与者 JWT（登录签发）；大屏只读匿名 |
| organizer | `/api/v1/org` | 活动方后台 | 活动方 JWT + 租户中间件 + 权限位中间件 |
| admin | `/api/v1/{adminSlug}` | 超管后台 | 超管 JWT；路径段 env 混淆（**保留 T-083**） |

错误信封（**保留**）：`{"code":"<machine>","message":"<human>"}`，附 `X-Request-Id`（新增，见 §3.6）。

---

## 2. 演进式重构总账（模块级 reuse / refactor / replace）

| 模块 | 现状 | 处置 | 说明 |
|---|---|---|---|
| `token` | HMAC 紧凑令牌, kinds: event/session/auth | **refactor** | 新增 Role `participant`；auth 令牌承载 `perm`（活动方权限位快照） |
| `identity` | 服务端重算指纹 + 白名单 | **reuse** | 指纹降级为"防共享辅助信号"，不再作主键；`Last4`→保留，新增全手机号匹配 |
| `loginguard` | 3s 恒延迟 + 10/10min 锁定 (org/admin) | **reuse+extend** | 新增 scope `participant:{event_id}` |
| `turnstile` | CF 校验 off/enforce | **reuse** | 应用到参与者登录与签到 |
| `realtime` | Redis 计数+pubsub+SSE+对账 | **refactor** | SSE 负载升级为 typed 信封（count / winner / milestone） |
| `flow` | 线性 6 step 校验 | **refactor** | step 扩 R1 多日签到/exam/lottery 配置；新增"完成门禁"校验 |
| `participation` | 落地+device-session+submit | **refactor** | 新增登录端点；submit 改 participant-JWT 鉴权；门禁；上传端点 |
| `organizer` | 活动/流程 CRUD+白名单+导出 | **refactor+extend** | 权限位中间件；模板选择；exam/lottery 配置；A/B 名册导入；记录字段对齐 |
| `admin` | 活动方 增/列/启停 | **extend** | 删除、权限位、平台配置、模板管理、DB 导出 |
| `repo` | 全部 SQL | **extend** | 新查询；保持"每租户查询显式带 organizer_id"铁律 |
| `storage` | local FS / 阿里云 OSS 接口 | **reuse+extend** | 新增 `SignedURL(key, ttl)`（OSS 私有读签名；local 走代理下载） |
| `export` | NATS→CSV→存储 | **refactor** | 列对齐 §SS-7；新增 job `kind`（participants / db_dump） |
| `config` | env 加载 | **extend** | 新增配置项；平台动态配置改 DB+加密（见 SS-0） |
| `seed` | demo 寻道大千 | **refactor** | 改造为新模型（登录名单 + R1~R4 完整 demo 活动） |
| `httpx` | 路由/中间件/SSE/限流 | **reuse+extend** | 新增权限中间件、Request-Id、统一鉴权辅助 |
| `webui` | 内嵌 React/大屏 | **reuse** | 不在本期后端设计范围（前端独立仓库） |

无"近 greenfield"丢弃；所有现有验证过的并发安全写法（`MarkCheckin` 条件 UPDATE、`ClaimWhitelist` 条件 UPDATE、Redis fail-open）作为**复用范式**沿用到新并发点（抽奖扣库存、A/B 命中）。

---

## 3. 跨切面设计

### 3.1 鉴权与令牌
- 令牌统一 `token.Signer`（HMAC-SHA256，**保留**）。新增 `Claims.Perm map[string]bool`（活动方权限位快照，签发时写入，过期前有效；改权限即时生效靠 §SS-0 的 Redis 版本号校验）。
- 参与者 JWT：`Kind=auth, Role=participant, Subject=participant_id, EventID, exp=min(now+8h, event_end+2h)`。Bearer 头携带（替代 device-session cookie；cookie 路径 legacy 保留）。
- 活动方/超管 JWT：`Role=organizer|admin, exp=now+12h`（**保留**）。

### 3.2 多租户隔离（**保留铁律**）
`repo` 中每个活动方作用域查询显式接收 `organizer_id` 并出现在 `WHERE`；handler 二次校验 `resource.OrganizerID == ctxOrg`。新表一律带 `organizer_id` 或可经 `event_id` 关联回租户并在查询层强制。

### 3.3 配置与密钥（安全红线）
- 静态基础设施配置：env（`config.go` 扩展），dev 有弱默认且启动告警（**保留模式**）。
- **动态平台密钥**（Cloudflare token、阿里云 AK/SK）：存 DB `platform_config`，**AES-256-GCM 加密落库**，密钥来自 `CAPTAIN_CONFIG_KEY`（env，生产必填）。API 永不回显明文（只回 `set=true/false` 与掩码尾 4 位）。绝不进代码、日志、对话。

### 3.4 缓存约定（Redis）
统一前缀：`rl:`(限流) `lg:`(登录守卫) `count:`(计数) `rt:`(pubsub) 已存在 → **保留**。新增：`perm:org:{id}`(权限位版本) `sess:p:{jti}`(参与者会话撤销集，可选) `lot:stock:{event}:{step}:{prize}`(奖品余量热读) `lot:lock:*`(抽奖并发) `tpl:list`(模板列表缓存) `win:{event}`(大屏中奖滚动列表，capped LIST)。所有 Redis 读 **fail-open 到 PG**（**保留范式**），PG 永远是事实来源。

### 3.5 消息（NATS JetStream，Stream `CAPTAIN`，at-least-once，消费幂等）
保留 `export.requested/completed`、`checkin.submitted`、`participant.step_completed`。新增 `prize.won`（SS-5→SS-6 大屏）、`audit.appended`（审计落库，可选异步）。消费者幂等键沿用 step record / job id / lottery_result id。

### 3.6 可观测与审计
- 新增 `X-Request-Id`（入站透传或生成；进 access log 与错误信封）。
- 审计表 `audit_log`（见 §4）：超管账号操作、平台配置变更、A/B 名册导入、抽奖内定命中、导出/DB 导出、记录查看（PII 访问留痕）。append-only。

### 3.7 测试策略（自测，开发后执行）
- 单元：flow 校验/门禁、exam 判分、抽奖算法（含内定/库存竞争）、identity 归一、token、加密配置。
- 仓储：用 `pgxpool` 连测试库（docker compose）跑迁移后做并发竞争测试（抽奖扣库存、A/B 命中、whitelist claim、多日签到去重）。
- 集成/E2E：扩展 `scripts/smoke.sh` 覆盖 `登录→R1多日签到门禁→R2问卷含图片→R3考试计分→R4抽奖(普通/内定)→中奖推大屏→活动方看记录→导出CSV→用户查本人记录`。
- 门控集成（需真 token，标注节点）：Turnstile enforce、阿里云 OSS、DB dump。
- 验收基线：`go build ./... && go vet ./... && go test ./...` 全绿；smoke 全绿；2万级计数压测不退化（保留 `cmd/screenstress`/`cmd/loadtest`，新增抽奖并发压测 `cmd/lotterystress`）。

---

## 4. 数据模型总览

**保留表**：`organizer` `admin_user` `flow_config` `event` `participant` `participation` `participation_step_record` `export_job` `event_whitelist_entry`（及 0002/0004/0005 索引、0003 指纹列）。

**演进（迁移 0006+，全部 additive / 可重入 `IF NOT EXISTS`）**：

| 变更 | 对象 | 内容 |
|---|---|---|
| 0006 | `organizer` | + `can_create_event bool NOT NULL DEFAULT true`、`can_view_records bool DEFAULT true`、`can_export_records bool DEFAULT true`、`deleted_at timestamptz`（软删）、`perm_version int NOT NULL DEFAULT 1` |
| 0006 | `platform_config`(新) | `key text PK, value_enc bytea NOT NULL, masked text, updated_by uuid, updated_at timestamptz` |
| 0006 | `audit_log`(新) | `id uuid pk, actor_role text, actor_id uuid, action text, target text, meta jsonb, request_id text, created_at timestamptz`（append-only，索引 `(created_at desc)`、`(action, created_at desc)`） |
| 0007 | `template`(新) | `id uuid pk, kind text CHECK in(screen,flow_page), code text, name text, version int, status text, organizer_id uuid NULL(REFERENCES organizer), manifest jsonb, created_at; UNIQUE(code, version); INDEX(kind,status)` （`organizer_id NULL`=全局，非空=该租户定制） |
| 0007 | `template_asset`(新) | `id uuid pk, template_id uuid fk, storage_key text, mime text, size bigint, role text, created_at` |
| 0007 | `event` | + `flow_template_code text`（用户侧流程页模板，screen 沿用 `screen_template_code`）；`screen_template_code` 保留兼容 |
| 0008 | `event_whitelist_entry` | + `claimed_jwt_jti text`（当前有效会话标记，防一号多端）；语义保留，登录改为**全手机号**匹配（已存 `phone_number`） |
| 0008 | `checkin_day`(新) | `id uuid pk, participation_id uuid fk, day_date date, checked_at timestamptz, lat/lng/accuracy double precision NULL; UNIQUE(participation_id, day_date)`（R1 多日签到去重与门禁） |
| 0009 | `exam_question`(新) | `id uuid pk, event_id uuid fk, step_id text, idx int, stem text, options jsonb, correct jsonb, score int, multi bool; INDEX(event_id, step_id, idx)` |
| 0009 | `participation_step_record` | 复用承载 exam 结果 payload（`{answers,score,passed,picked[]}`）；不新表 |
| 0010 | `lottery_prize`(新) | `id uuid pk, event_id uuid fk, step_id text, code text, name text, level text CHECK in(grand,normal,none), stock int NOT NULL, drawn int NOT NULL DEFAULT 0, weight int, image_key text; UNIQUE(event_id,step_id,code); CHECK(drawn<=stock)` |
| 0010 | `lottery_rig_entry`(新) | `id uuid pk, event_id uuid fk, step_id text, employee_number text, prize_code text, pool text CHECK in(A,B), created_by uuid, created_at; UNIQUE(event_id,step_id,employee_number)` |
| 0010 | `lottery_result`(新) | `id uuid pk, event_id uuid fk, step_id text, participant_id uuid fk, prize_id uuid NULL fk, prize_level text, resolved_by text CHECK in(rig,random,miss), drawn_at timestamptz; UNIQUE(event_id,step_id,participant_id)`（抽奖幂等 + 防重抽） |
| 0011 | `participation` | + `data_field_1 text`（通用文本，记录栏一）、`data_field_2 text`（OSS 资源 key，记录栏二）、`device_id text`（设备标识，导出可见）；`fingerprint_hash` 已在 `participant`（0003） |

> 设计原则：能复用 `participation_step_record` 的（form/game/charity/reward/exam 结果）不新建表；需要**强约束/高频并发/独立生命周期**的（多日签到去重、奖品库存、内定名册、抽奖幂等、模板、平台配置、审计）才建表。与 0002 注释里"participant/participation 不合并"的既有判断一致。

---

## SS-0 平台基座：租户 / 账号 / 权限 / 配置（超管）

**功能**
1. 活动方账号：创建、列表、启用/停用、**删除（软删 `deleted_at`，名下活动连带置不可登录、数据保留）**、重置密码。
2. 活动方权限位：`can_create_event` / `can_view_records` / `can_export_records` 开关；改动 `perm_version+1` 并失效缓存，下次请求即时生效。
3. 平台配置：Cloudflare token、阿里云 AK/SK、（可选）短信签名/模板；加密落库；只写不回显明文。
4. 数据库导出：触发逻辑导出（`pg_dump --no-owner` 流）→ 对象存储 → 短时签名链接；`export_job.kind='db_dump'`；仅超管；审计。

**接口**（`/api/v1/{adminSlug}`，超管 JWT；登录沿用现 `ad.Login` 硬化）

| 方法 路径 | 说明 |
|---|---|
| `GET /organizers` | 列表（含权限位、状态、未软删）**保留+扩字段** |
| `POST /organizers` | 创建 **保留** |
| `POST /organizers/{id}/status` | 启停 **保留** |
| `DELETE /organizers/{id}` | 软删 **新增** |
| `POST /organizers/{id}/password` | 重置密码 **新增** |
| `PATCH /organizers/{id}/permissions` | `{can_create_event,can_view_records,can_export_records}` **新增** |
| `GET /config` | 各密钥 `set` 与掩码（不回明文）**新增** |
| `PUT /config/{key}` | 写入（AES-GCM 加密落库）**新增** |
| `POST /db-export` / `GET /db-export/{job_id}` | DB 导出触发/状态 **新增** |
| `GET /audit?action=&from=&to=` | 审计查询 **新增** |

**数据库**：迁移 0006（`organizer` 权限列+软删+perm_version、`platform_config`、`audit_log`）。

**缓存**：`perm:org:{id}` → `perm_version`（int）。活动方鉴权中间件比对 JWT 内 `perm_version` 与 Redis；不一致则回 `token_stale`(401) 要求重登换新令牌（避免长 JWT 携旧权限）。fail-open：Redis 挂则信任 JWT 快照。

**与现有代码**：`admin.Handler` extend；`repo` 加 organizer 软删/权限/config/audit 查询；`config.go` 加 `CAPTAIN_CONFIG_KEY`；新增 `internal/cryptobox`（AES-GCM 封装）；新增 `internal/platformcfg`（读 DB 解密 + 缓存，供 turnstile/storage 启动时取真凭据，替代纯 env——env 仍作 fallback/bootstrap）。

**决策与假设**：① 删除=软删（企业数据留痕合规优先）；② 平台配置改 DB 是为"超管在后台设置 token"原始需求，env 保留为 bootstrap/dev；③ 权限即时生效用 perm_version 而非短 JWT（避免频繁登录）。均可调。

---

## SS-1 模板注册与分发（超管上传/管理；活动方选用）

**功能**
1. 超管上传/管理多款**大屏模板**与**用户侧流程页模板**：元信息(manifest:配置schema/预览图/适配step类型) + 静态资源(图/字体/封面)入对象存储。
2. 模板可全局或**活动方定制**（`organizer_id` 非空仅该租户可见）；状态 `draft|published|disabled`；版本化。
3. 活动方建/改活动时选 `screen_template_code` 与 `flow_template_code`，仅可选「全局 published」∪「本租户定制 published」。
4. 安全红线（**保留 P2 决策**）：模板=配置驱动 + 静态资源；**禁止上传可执行包**；资源 MIME/大小白名单校验。

**接口**

| 方法 路径 | 域 | 说明 |
|---|---|---|
| `GET /{adminSlug}/templates?kind=` | admin | 列表 |
| `POST /{adminSlug}/templates` | admin | 建模板(manifest) |
| `PUT /{adminSlug}/templates/{id}` | admin | 改 / 发布 / 停用 |
| `POST /{adminSlug}/templates/{id}/assets` | admin | 上传资源(multipart→storage，MIME 白名单) |
| `DELETE /{adminSlug}/templates/{id}` | admin | 停用（软） |
| `GET /org/templates?kind=` | org | 活动方可选模板（全局+本租户 published） |

**数据库**：迁移 0007（`template`、`template_asset`、`event.flow_template_code`）。

**缓存**：`tpl:list:{kind}` 短 TTL（60s）列表缓存，写操作主动失效。

**与现有代码**：`storage` 加 `SignedURL`；`admin`/`organizer` extend；`event` 校验模板归属（替换现 `screen_template_code` 自由文本默认 `ink-wash-default`：迁移期保留默认值兼容，新建活动强制选已注册模板）。

**决策与假设**：流程页/大屏模板表合一（`kind` 区分），结构同构，减表。可调。

---

## SS-2 参与者身份与登录（白名单强身份）

**功能**
1. 活动方导入员工名单（**保留** CSV `employee_number,name,phone`）。
2. 用户登录：四元组 = **名单内 用户ID/姓名 + 工号 + 手机号 + 活动ID(path)**。校验 `event_whitelist_entry`：`(event_id, employee_number)` 命中且 `name` 全等且 **全手机号 `phone_number` 全等**。成功 → upsert participant（staff 路径，复用 `UpsertParticipantFull`）+ 绑定指纹（复用 `ClaimWhitelist` 条件 UPDATE）+ 签发参与者 JWT。
3. 一号一端：登录写 `claimed_jwt_jti`，旧会话失效（防借号）；指纹不一致仅告警不强阻断（员工换设备常见），可由活动方配置严格模式。
4. 登录硬化：`loginguard` scope `participant:{event_id}`（恒延迟+失败锁定）；可选 Turnstile。
5. legacy 公开/匿名/external 路径 → `CAPTAIN_OPEN_PARTICIPATION=off` 默认关闭，仅 demo。

**接口**（`/api/v1/p`）

| 方法 路径 | 说明 |
|---|---|
| `GET /p/e/{event_id}?et=` | 落地：校验 event_token，回 event 元信息 + flow（**refactor**：不再签 device-session，改返回"需登录") |
| `POST /p/e/{event_id}/login` | **新增** `{employee_number,name,phone,fingerprint,turnstile_token}` → `{token, participant:{id,name}}`；错误码 `bad_credentials/account_locked/captcha_failed/event_inactive` |
| `POST /p/e/{event_id}/logout` | **新增** 失效当前 jti |
| `GET /p/e/{event_id}/me` | **新增** 当前参与者 + 流程进度 |

**数据库**：迁移 0008（`event_whitelist_entry.claimed_jwt_jti`）。复用 0003 白名单表/指纹列。

**缓存**：`lg:*`（**复用** loginguard）；`sess:p:{jti}` 撤销标记（logout/顶号，TTL=令牌寿命）。

**与现有代码**：`token` 加 `participant` role + `jti`；`participation` 加登录/登出/me；`identity` 复用（指纹归一+Hash 作防共享信号，写 `participant.fingerprint_hash`）；`loginguard` 加 scope；`repo` 加 `WhitelistByEmployee` 全手机号校验变体 + jti 绑定。

**决策与假设**：① "用户ID"= 名单条目（工号是业务主键，姓名+全手机号为校验因子，活动ID来自 path）——四元组满足；② 全手机号匹配（比旧 `phone_last4` 强，名单已存全号）；③ 指纹换设备不硬阻断（默认宽松，活动方可设严格）。均可调；②③ 属安全权衡，文档登记，若活动方有合规要求可与 codex 复议。

---

## SS-3 活动与内容编排（活动方）

**功能**
1. 活动 CRUD（**保留**：起止时间、预计人数、状态 draft/active/ended、租户隔离、flow 归属校验）+ 选模板（SS-1）。受 `can_create_event` 权限位中间件控制。
2. 流程编排：线性流（**保留无分支**），step 类型扩展 + "完成门禁"。flow_config schema v2：

```
step.type ∈ { checkin, form, game, charity, reward, result, exam, lottery }
step.gate ∈ { none(默认) | completeRequired }   // 门禁：未完成不得进入后续 step
checkin.config = { mode:"single"|"multiday", days:int(>=0), timezone:"Asia/Shanghai",
                   requireAllToProceed:bool, countTowardAttendance:true }   // days=0 跳过签到
form.config    = { fields:[{id,type:text|phone|email|select|image|textarea, label, required, options?, maxSizeKB?}] }  // R2 问卷，image→上传
exam.config    = { bankRef:true, mode:"all"|"random", randomCount:int, passScore:int, attemptLimit:int, shuffle:bool }  // R3，题目在 exam_question
lottery.config = { abPoolEnabled:bool, drawLimit:1, grandPushToScreen:bool, prizesRef:true }  // R4，奖项在 lottery_prize
```
3. R3 题库：活动方导入题目（CSV/JSON：题干、选项、正确项、分值、单/多选）→ `exam_question`。
4. R4 奖项配置 + **A/B 内定名册**导入（CSV `employee_number,prize_code,pool`）→ `lottery_prize` / `lottery_rig_entry`；导入即审计。
5. 名单导入（**保留** whitelist import）。

**接口**（`/api/v1/org`，活动方 JWT + 租户 + 权限中间件）

保留：`login/events/flows(CRUD)/events(CRUD)/events/{id}/status/entry/whitelist(import|list)`。新增：

| 方法 路径 | 说明 |
|---|---|
| `GET /org/templates` | 可选模板（SS-1） |
| `POST /org/events/{id}/exam/import` | 导入题库（覆盖 step） |
| `GET /org/events/{id}/exam` | 查看题库 |
| `POST /org/events/{id}/lottery/prizes` | 配置奖项 |
| `POST /org/events/{id}/lottery/rig/import` | 导入 A/B 内定名册（审计） |
| `GET /org/events/{id}/lottery/summary` | 奖项余量/已抽/内定数 |

**数据库**：0007(event.flow_template_code) / 0009(exam_question) / 0010(lottery_*)。flow schema v2 经 `flow.Parse` 强校验（新增 step 类型与 config 形状校验、门禁引用合法、days>=0、随机题数<=题库量）。

**缓存**：无新增热缓存；导入为低频写。`tpl:list` 见 SS-1。

**与现有代码**：`flow` refactor（类型/配置/门禁校验）；`organizer` extend（exam/lottery/rig/模板）；`repo` 加题库/奖项/名册批量插入（沿用 `InsertWhitelist` 逐行 `ON CONFLICT DO NOTHING` 范式 + CSV 注入防护 `csvSafe` 复用思路于导出侧）。

**决策与假设**：① "活动内容灵活自定义"= flow schema v2 配置驱动（非任意代码）；② R1 `days` 为"需签到的去重日历日数"，时区固定 `Asia/Shanghai`，活动方只配数量不配具体日期（更简单稳健）；③ exam 随机题在参与者维度确定性抽取（按 `participant_id+step` 种子，刷新稳定）。均可调；②若活动方要"指定具体日期签到"再与 codex 复议扩展。

---

## SS-4 用户参与流程运行时（R1 签到 / R2 问卷 / R3 考试）

**功能**
1. 提交鉴权改为**参与者 JWT**（refactor 现 device-session）；保留限流（submit:participant/ip）、Turnstile（签到）。
2. **R1 多日签到门禁**：每次签到落 `checkin_day(participation_id, day_date)`（`Asia/Shanghai` 当日，`ON CONFLICT DO NOTHING` 幂等，复用 `MarkCheckin` 条件写范式）；首次某日签到 → 计数+广播（**保留** `RT.OnCheckin`，去重口径=完成签到门禁的去重 participant）。`distinct day >= days` 前，后续 `gate=completeRequired` step 提交回 `409 step_gated`。`days=0` 跳过。
3. **R2 问卷**：`form` step 记录到 `participation_step_record`（**保留**）；新增图片：`POST /p/e/{id}/uploads`（参与者 JWT，multipart，MIME/大小白名单）→ storage（OSS/local）→ 返回 key；payload 存 key；其一写入 `participation.data_field_2`（记录栏二=OSS 位置），文本类汇总写 `data_field_1`（记录栏一）。
4. **R3 考试**：`exam` step 拉题（按 mode/randomCount 确定性选题，不下发 correct）；提交服务端判分（单/多选，`score` 累加，`passed=score>=passScore`），`attemptLimit` 控制；结果入 `participation_step_record`（payload `{picked,score,passed}`）+ 审计可选。
5. 计数/对账/SSE：**保留** realtime 全链路；签到口径调整为多日门禁完成后才算"参与人数"（在 `CheckinCount` 改为 `count(distinct participation where 满足门禁)` 或维持"完成首日即计数"——见决策）。

**接口**（`/api/v1/p`，参与者 JWT）

| 方法 路径 | 说明 |
|---|---|
| `POST /p/e/{id}/steps/{step_id}/submit` | **refactor** 鉴权改 JWT；按 type 分派：checkin(多日)/form/game/charity/reward/result/exam；门禁校验 |
| `POST /p/e/{id}/uploads` | **新增** 图片/文件上传→storage→key |
| `GET /p/e/{id}/steps/{step_id}` | **新增** 取 step 运行态（exam 取题、签到进度 x/N、门禁状态） |
| `GET /p/e/{id}/count` `/info` `/qr` `/stream` | **保留**（大屏只读） |

**数据库**：0008(checkin_day) / 0009(exam) / 0011(participation.data_field_1/2,device_id)。

**缓存**：复用 `count:`/`rt:`/`rl:`。新增 `exam:pick:{participation}:{step}` 缓存确定性选题集（TTL=活动期，可省，纯算法可重算）。

**与现有代码**：`participation` refactor（鉴权源、门禁、上传、exam 取题判分、checkin 多日）；`flow` 提供门禁判定；`realtime` reuse；`repo` 加 checkin_day/exam 查询、data_field 写入；`storage` reuse。

**决策与假设**：① "参与人数"口径=**完成 R1 全部签到门禁**的去重 participant（贴合"多天签到完成才进下一环节"，对账 SQL 改 distinct 满足门禁者）——若活动方要"首日即计数"为可调开关；② exam 不下发答案、服务端判分（防作弊）；③ 上传走后端中转入 storage（v1 简单可靠；预签名直传为后续优化）。①属口径决策，影响大屏数字，登记并默认按"门禁完成计数"，可与 codex/活动方复议。

---

## SS-5 在线抽奖 R4 + A/B 奖池 + 中奖推大屏

**功能**
1. 用户在 `lottery` step 抽奖：**每参与者每 step 仅一次**（`lottery_result` `UNIQUE(event,step,participant)` 幂等，重复请求回同一结果）。前置门禁可配（如需先完成 R1~R3）。
2. 结算算法（事务内，PG 为准，复用条件 UPDATE 并发范式）：
   - **内定优先**：参与者 `employee_number` 命中 `lottery_rig_entry(event,step)` → 指定 `prize_code`、`pool`，`resolved_by='rig'`。
   - **否则随机**：在未被内定占用的剩余库存上按 `weight` 加权随机；`UPDATE lottery_prize SET drawn=drawn+1 WHERE id=? AND drawn<stock RETURNING`（原子扣减，失败则回退到"未中奖 miss/兜底奖"）。
   - 写 `lottery_result`，审计 `audit_log(action='lottery_draw', meta={resolved_by,prize,pool})`。
3. **中大奖推大屏**：`prize.level='grand'` 且 `grandPushToScreen` → 发 `prize.won`（NATS）→ realtime 注入 `win:{event}` 滚动列表 + SSE 广播 `{type:"winner",name,prize}`（姓名可按活动方配置脱敏，企业内部场景默认显示——满足"给领导人情世故"的展示诉求，但**内定与展示均审计留痕**）。
4. 活动方可查抽奖汇总、内定命中明细（仅活动方/超管，审计访问）。

**接口**

| 方法 路径 | 域 | 说明 |
|---|---|---|
| `POST /p/e/{id}/steps/{step_id}/draw` | p | 抽奖（参与者 JWT，幂等） |
| `GET /p/e/{id}/steps/{step_id}/result` | p | 本人抽奖结果 |
| `GET /org/events/{id}/lottery/summary` | org | 余量/分布/内定命中（审计） |

**数据库**：0010（`lottery_prize`/`lottery_rig_entry`/`lottery_result`）。

**缓存**：`lot:stock:{event}:{step}:{prize}` 热读余量（展示用，PG 为准）；`lot:lock:{event}:{step}:{participant}` `SETNX` 防并发重复抽（快路径，PG `UNIQUE` 兜底，复用 whitelist/checkin 范式）；`win:{event}` capped LIST（最近 N 中奖，断连即发，**保留 realtime 重连无回放**思路）。fail-open 到 PG。

**与现有代码**：新增 `internal/lottery`（算法+结算，纯函数可单测）；`participation` 挂 draw 路由分派 lottery step；`realtime` refactor 支持 winner 信封；`repo` 加抽奖事务查询（原子扣减条件 UPDATE = `MarkCheckin`/`ClaimWhitelist` 同范式）；`organizer` 加 summary。

**决策与假设**：① A/B 语义=活动方导入 `employee_number→prize_code,pool` 名册，内定者必中指定奖（"人情世故"显式落地），其余加权随机——**透明设计 + 全程审计**（合规可追溯，避免暗箱不可查）；② 未中奖给 `level='none'` 谢谢参与而非报错；③ 抽奖原子性以 PG 条件 UPDATE 为准、Redis 仅热读与快路径锁。①敏感，已登记理由与审计保障；若需更强（如双人复核内定名册）与 codex 复议。

---

## SS-6 活动大屏实时服务

**功能**（**大部分 reuse**）
1. 实时参与人数：**保留** Redis `INCR` + pub/sub + 进程内 SSE Hub（≤2/s 节流）+ 10s PG 对账 + 重连即发快照。
2. SSE 负载升级为 typed 信封：`{type:"count",count,ts}` / `{type:"winner",name,prize,ts}` / `{type:"milestone",...}`（向后兼容：旧大屏只认 count 字段，保留顶层 `count`）。
3. 中奖滚动：`win:{event}` capped LIST 提供最近 N 条；连接建立即发 count 快照 +（可选）最近中奖（transient，不持久回放）。
4. 多大屏实例：**保留** Redis pub/sub 跨实例扇出。
5. 大屏只读匿名（保留 `/p/e/{id}/count|info|stream|qr`），不需登录。

**接口**：`GET /p/e/{id}/stream`（**refactor** 信封）、`/count`、`/info`、`/qr` **保留**。

**数据库**：无新增（`lottery_result` 已在 0010）。

**缓存**：`count:`/`rt:` **保留**；新增 `win:{event}`（LTRIM capped）。

**与现有代码**：`realtime` refactor（Snapshot→Envelope，`OnCheckin` 口径接 SS-4 门禁，`OnPrizeWon` 新增消费 `prize.won`）；`participation.Stream` reuse。

**决策与假设**：信封向后兼容（保留顶层 count）以零回归；中奖展示为 transient（断连丢历史可接受，符合"实时滚动"语义）。可调。

---

## SS-7 活动记录查看与导出

**功能**
1. 活动方按活动看参与明细，字段**对齐原始需求**：记录ID(`participation.id`)、用户名(`whitelist.name`)、工号(`employee_number`)、手机号(脱敏显示/导出全号)、参与时间(首次/各步)、用户位置(`checkin_day`/`participation` geo)、**用户设备ID**(`participation.device_id`，导出可见)、**用户浏览器指纹**(`participant.fingerprint_hash`，导出可见)、**数据栏一**(`data_field_1` 文本)、**数据栏二**(`data_field_2` OSS 位置→签名链接)。受 `can_view_records` 控制；查看记 PII 审计。
2. 导出：**保留** NATS→CSV→对象存储→签名下载（CSV BOM + 防公式注入 `csvSafe` **保留**）；列对齐上表 + 动态登记字段（**保留** form 键并集）；受 `can_export_records` 控制；导出记审计。
3. 用户查本人记录：`GET /p/e/{id}/me/records`（参与者 JWT）→ 各 step 记录 + exam 成绩 + 抽奖结果。
4. DB 导出属 SS-0（超管），与此分离。

**接口**

| 方法 路径 | 域 | 说明 |
|---|---|---|
| `GET /org/events/{id}/participants` | org | **refactor** 字段对齐 + 权限位 + 审计 |
| `POST /org/events/{id}/export` `GET /org/exports/{job_id}[/download]` | org | **保留+refactor** 列对齐 + 权限位 |
| `GET /p/e/{id}/me/records` | p | **新增** 本人记录 |

**数据库**：0011（`participation.data_field_1/2,device_id`）。

**缓存**：无（导出异步、列表低频）。签名链接 TTL 短（如 10min）。

**与现有代码**：`repo.ListParticipants` refactor（JOIN whitelist 取 name/工号/手机、device_id、fingerprint、data_field）；`export` refactor 列；`organizer` 加权限位与审计、本人记录在 `participation`；`storage.SignedURL` 用于 data_field_2 与导出下载（OSS 私有读）。

**决策与假设**：① 手机号列表页脱敏(`138****1234`)、导出明文（贴合原始需求"导出可见"）；② 导出格式 CSV(UTF-8 BOM, Excel 可读)，xlsx 列后续可选；③ data_field_1/2 为通用承载（活动方流程产生的代表性文本/资源），由运行时按流程语义填充。可调。

---

## 5. 迁移序列（embedded migrator，单文件单事务，可重入）

`0006` 平台基座 → `0007` 模板 → `0008` 参与者登录+多日签到 → `0009` 考试题库 → `0010` 抽奖+A/B → `0011` 记录字段。全部 `IF NOT EXISTS`/`ADD COLUMN IF NOT EXISTS`/`ON CONFLICT`，对空表或既有数据均安全；生产若已有热数据，索引注释延续 0002 的 CONCURRENTLY 提示。

## 6. 决策与假设登记（汇总，均"可调"，标★为敏感/建议 codex 复议触发条件）

| # | 决策 | 默认 | 复议触发 |
|---|---|---|---|
| D1 | 参与者非匿名，强身份登录 | 名单+四元组→JWT | — |
| D2 | "用户ID"=名单条目，四元组=姓名+工号+全手机+活动ID | 全手机号匹配 | 活动方无全手机号数据时 |
| D3★ | 指纹换设备不硬阻断 | 宽松，活动方可严格 | 客户合规要求强绑定 |
| D4 | R1 days=去重日历日数，时区 Asia/Shanghai，仅配数量 | 不配具体日期 | 要求"指定日期签到" |
| D5★ | 参与人数口径=完成 R1 门禁的去重人 | 门禁完成计数 | 活动方要首日即计数 |
| D6 | exam 服务端判分、不下发答案、确定性随机题 | 防作弊 | — |
| D7★ | A/B=内定名册必中+其余加权随机+全程审计 | 透明可追溯 | 要求双人复核/更强管控 |
| D8 | 删除活动方=软删 | 数据留痕 | 要求硬删合规 |
| D9 | 平台密钥 DB 加密存储，env 仅 bootstrap | AES-GCM | — |
| D10 | 模板=配置+静态资源，禁可执行包 | 安全红线 | — |

> 若实现期遇 D 表外的真正分叉项：按指令与 codex 一起读原始需求后决策并回填本表。

## 7. 下一步

本文经自检与你过目后 → `writing-plans` 拆 200+ 实现级子任务（按 SS-0→SS-7 依赖链 + 迁移序列排程，每任务含验收点与测试），逐任务实现 + 自测 + commit。
