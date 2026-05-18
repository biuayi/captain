# captain v2 实施主路线图

> **For agentic workers:** REQUIRED SUB-SKILL: 用 superpowers:subagent-driven-development 逐任务执行。每个子系统在轮到它时，由执行子代理依据本路线图条目 + [docs/DESIGN.md](../../DESIGN.md) + 当时代码库现状，生成「全代码 bite-sized TDD 详细计划」存 `docs/superpowers/plans/2026-05-19-ssN-<name>.md` 再执行。本文件是 200+ 任务的权威索引与勾选追踪。

**Goal:** 按 DESIGN.md 演进式重构 captain，落地 SS-0~SS-7 共 8 子系统（企业内部活动平台后端）。

**Architecture:** Go 模块化单体；PG(真) + Redis(热) + NATS JetStream(异步) + 对象存储。保留已验证骨架（三接口域/令牌/计数实时/迁移器/限流/登录守卫），逐模块 reuse·refactor·replace。

**Tech Stack:** Go 1.25, pgx/v5, go-redis/v9, nats.go jetstream, golang.org/x/crypto, 内嵌迁移器, HMAC 令牌, AES-256-GCM。

**执行纪律（用户指令）：** 每子任务完成即 `commit`；无法决策项 → 与 codex 一起读原始需求后决策；每子系统结束跑该阶段验收（build/vet/test/相关 smoke）。分支 `feat/v2-redesign`。

**全局验收基线：** `go build ./... && go vet ./... && go test ./...` 全绿；`scripts/smoke.sh` 全绿；2万级计数压测不退化。

**TDD 约定：** 每任务步序 = 写失败测试 → 跑验证失败 → 最小实现 → 跑验证通过 → commit。仓储/算法任务必须含并发竞争测试（沿用 `MarkCheckin`/`ClaimWhitelist` 条件 UPDATE 范式）。

**依赖链：** P0 基座 → SS-0 → (SS-1 ∥ SS-2) → SS-3 → SS-4 → SS-5 → SS-6 → SS-7 → PF 集成。

---

## Phase P0 — 跨切面基座（先行，所有子系统依赖）

- [x] **P0-01** token 增 `Role=participant` 常量与校验路径 — Modify `internal/token/token.go` | 验收: 可签发/校验 participant 令牌 | 测试 `internal/token/token_test.go` 新增用例
- [x] **P0-02** token Claims 增 `JTI string` + 生成（随机 16B base64url） — Modify `internal/token/token.go` | 验收: Sign 自动填 JTI，Verify 透出 | 测试: 两次 Sign JTI 不同
- [x] **P0-03** token Claims 增 `Perm map[string]bool` + `PermVersion int` 字段（omitempty） — Modify `internal/token/token.go` | 验收: 往返序列化保真 | 测试: 编解码断言
- [x] **P0-04** 新增 `internal/cryptobox`：AES-256-GCM `Seal/Open(key,plaintext)`，key 来自 32B — Create `internal/cryptobox/cryptobox.go` | 验收: 往返一致、篡改报错 | 测试 `cryptobox_test.go`（含 nonce 唯一、tamper）
- [x] **P0-05** config 增 `ConfigKey`(`CAPTAIN_CONFIG_KEY`)、`OpenParticipation`(bool,默认 false) — Modify `internal/config/config.go` | 验收: 默认值正确，弱默认告警 | 测试: env 覆盖
- [x] **P0-06** httpx 增 `RequestID` 中间件（透传 `X-Request-Id` 或生成）写入 ctx+响应头 — Create `internal/httpx/requestid.go`, Modify `cmd/server/main.go` | 验收: 每响应带 X-Request-Id | 测试: 有/无入站头两路径
- [x] **P0-07** httpx 错误信封加 `request_id`（从 ctx 取） — Modify `internal/httpx/response.go` | 验收: Fail 输出含 request_id | 测试: 断言字段
- [x] **P0-08** 迁移 0006 文件：`organizer` 加权限位/软删/perm_version + `platform_config` + `audit_log` — Create `internal/store/migrations/0006_platform_base.sql` | 验收: 迁移幂等可重入跑通 | 测试: migrate 后 `\d` 断言列存在
- [x] **P0-09** 新增 `internal/audit`：`Repo.Append(ctx, entry)` 写 `audit_log`（append-only） — Create `internal/audit/audit.go`, Modify `internal/repo/repo.go` | 验收: 落库成功 | 测试: 插入+查询
- [x] **P0-10** audit 查询 `List(action,from,to,limit)` — Modify `internal/repo/repo.go` | 验收: 过滤+倒序 | 测试: 多条过滤断言
- [x] **P0-11** 新增 `internal/platformcfg`：读 `platform_config` 解密 + 进程内缓存 + env fallback — Create `internal/platformcfg/platformcfg.go` | 验收: DB 有则用 DB，无则 env | 测试: 两路径
- [x] **P0-12** httpx 新增统一鉴权辅助 `AuthClaims(r, sig, role)` 复用三域 — Create `internal/httpx/authz.go` | 验收: 角色不符返回 false | 测试: 角色矩阵
- [x] **P0-13** httpx 新增 `OrgPermMiddleware`（校验 JWT.Perm + Redis perm_version，新鲜则放行，过期回 `token_stale`） — Create `internal/httpx/orgperm.go` | 验收: 版本不一致 401 token_stale；Redis 挂 fail-open 信任 JWT | 测试: 三路径
- [x] **P0-14** repo 增 `OrganizerPermVersion(ctx,id)` + Redis `perm:org:{id}` 读写封装 — Modify `internal/repo/repo.go`, `internal/loginguard`? 否→ Create `internal/orgperm/cache.go` | 验收: 读写一致、缺省=1 | 测试: 命中/回源
- [x] **P0-15** 测试基建：`internal/store` 测试用迁移跑库 helper（docker compose pg）+ `scripts/testdb.sh` — Create `scripts/testdb.sh` | 验收: 一键起测试库跑迁移 | 测试: 脚本退出码 0
- [x] **P0-16** P0 阶段验收：build/vet/test 全绿 + 提交基座 — | 验收: CI 三连绿 | 测试: 全量 `go test ./...`

## Phase SS-0 — 平台基座（超管：账号/权限/配置/DB导出/审计）

- [x] **SS0-01** repo `ListOrganizers` 改含权限位/状态/排除软删 — Modify `internal/repo/repo.go`, `internal/domain/models.go`(Organizer 加 Perm/DeletedAt) | 验收: 软删不返回 | 测试: 插入软删后列表断言
- [x] **SS0-02** repo `SoftDeleteOrganizer(id)`（置 deleted_at + 名下 event 不可登录标记） — Modify `internal/repo/repo.go` | 验收: 幂等、连带 | 测试: 软删后登录被拒
- [x] **SS0-03** admin `DELETE /organizers/{id}` handler + 路由 + 审计 — Modify `internal/admin/handler.go`, `cmd/server/main.go` | 验收: 仅超管、写 audit | 测试: 鉴权+审计断言
- [x] **SS0-04** repo `ResetOrganizerPassword(id,hash)` — Modify `internal/repo/repo.go` | 验收: 更新成功 | 测试: 新密码可登录
- [x] **SS0-05** admin `POST /organizers/{id}/password` + 审计 — Modify `internal/admin/handler.go`, `cmd/server/main.go` | 验收: bcrypt 重置 | 测试: 旧失效新生效
- [x] **SS0-06** repo `SetOrganizerPermissions(id,perms)` + perm_version+1 + 失效 `perm:org:{id}` — Modify `internal/repo/repo.go` | 验收: 版本自增、缓存失效 | 测试: 版本递增断言
- [x] **SS0-07** admin `PATCH /organizers/{id}/permissions` + 审计 — Modify `internal/admin/handler.go`, `cmd/server/main.go` | 验收: 仅 3 个布尔位、即时生效 | 测试: 改后旧 JWT token_stale
- [x] **SS0-08** 组织方登录把权限位+perm_version写入 JWT — Modify `internal/organizer/handler.go` | 验收: JWT 携带 Perm/PermVersion | 测试: 解码断言
- [x] **SS0-09** organizer 受保护路由挂 `OrgPermMiddleware`（建活动需 can_create_event 等映射表） — Modify `cmd/server/main.go`, `internal/organizer/handler.go` | 验收: 无权限 403 perm_denied | 测试: 权限矩阵
- [x] **SS0-10** repo `platform_config` Upsert/Get（值经 cryptobox 加密、masked 计算） — Modify `internal/repo/repo.go` | 验收: 密文落库、masked 仅尾4 | 测试: 加解密往返
- [x] **SS0-11** admin `GET /config`（回各 key set 与 masked，无明文） — Modify `internal/admin/handler.go`, `cmd/server/main.go` | 验收: 不泄明文 | 测试: 响应无明文断言
- [x] **SS0-12** admin `PUT /config/{key}`（加密写库 + 审计 + 失效 platformcfg 缓存） — Modify `internal/admin/handler.go` | 验收: 写后 GET 显示 set=true | 测试: 往返
- [x] **SS0-13** turnstile/storage 启动改经 platformcfg 取凭据（env fallback） — Modify `cmd/server/main.go`, `internal/turnstile`, `internal/storage` 装配 | 验收: DB 配置优先生效 | 测试: 注入桩
- [x] **SS0-14** export_job 增 `kind`（迁移补列，0006 或 0006b）+ domain — Modify `0006_*.sql`, `internal/domain/models.go`, `internal/repo/repo.go` | 验收: 既有 csv job 兼容(kind 默认 participants) | 测试: 旧路径回归
- [x] **SS0-15** DB 导出 worker：消费 `export.requested`(kind=db_dump) → `pg_dump --no-owner` 流 → storage → 签名链接 — Modify `internal/export/export.go` | 验收: 产出可下载 dump | 测试: 小库 dump 冒烟
- [x] **SS0-16** admin `POST /db-export` `GET /db-export/{job}` + 审计 + 仅超管 — Modify `internal/admin/handler.go`, `cmd/server/main.go` | 验收: job 流转 pending→done | 测试: 状态机
- [x] **SS0-17** admin `GET /audit?action=&from=&to=` — Modify `internal/admin/handler.go`, `cmd/server/main.go` | 验收: 过滤+分页 | 测试: 数据集断言
- [x] **SS0-18** seed 改造：超管/活动方账号带默认权限位；移除旧寻道大千专属耦合（保留 demo 可控） — Modify `internal/seed/seed.go` | 验收: 启动 seed 不报错、账号有权限位 | 测试: seed_test 调整
- [x] **SS0-19** SS-0 阶段验收：build/vet/test + smoke（超管增删改权限/配置/审计）全绿 — | 验收: 三连绿+smoke | 测试: 扩展 smoke 段

## Phase SS-1 — 模板注册与分发

- [x] **SS1-01** 迁移 0007：`template` + `template_asset` + `event.flow_template_code` — Create `internal/store/migrations/0007_templates.sql` | 验收: 幂等跑通 | 测试: 列断言
- [x] **SS1-02** domain `Template`/`TemplateAsset` — Modify `internal/domain/models.go` | 验收: 字段齐 | 测试: 编译
- [x] **SS1-03** storage 接口加 `SignedURL(key, ttl)`（local=代理下载 token；aliyun=OSS 私有签名） — Modify `internal/storage/storage.go`, `storage_aliyun.go` | 验收: 两驱动可签 | 测试: local 桩、aliyun 接口契约
- [x] **SS1-04** repo 模板 CRUD（create/list by kind/update status/soft delete，全局 vs 租户定制可见性） — Modify `internal/repo/repo.go` | 验收: 可见性正确 | 测试: 全局+租户混合
- [x] **SS1-05** admin `GET/POST /templates` `PUT /templates/{id}` `DELETE /templates/{id}` + 审计 — Modify `internal/admin/handler.go`, `cmd/server/main.go` | 验收: 仅超管、状态机 | 测试: 鉴权+流转
- [x] **SS1-06** asset 上传 `POST /templates/{id}/assets`（multipart，MIME/大小白名单）→ storage — Modify `internal/admin/handler.go` | 验收: 非白名单 415 | 测试: 合法/非法 MIME
- [x] **SS1-07** organizer `GET /org/templates?kind=`（全局 published ∪ 本租户 published） — Modify `internal/organizer/handler.go`, `cmd/server/main.go` | 验收: 越租户不可见 | 测试: 隔离断言
- [x] **SS1-08** event 创建/更新校验 `screen_template_code`/`flow_template_code` ∈ 可选集 — Modify `internal/organizer/handler.go`, `internal/repo/repo.go` | 验收: 非法模板 400 | 测试: 合法/非法
- [x] **SS1-09** Redis `tpl:list:{kind}` 缓存 + 写失效 — Modify `internal/repo` 或 Create `internal/templatecache` | 验收: 命中/失效正确，fail-open | 测试: 命中+写后失效
- [x] **SS1-10** SS-1 阶段验收：build/vet/test + smoke（超管传模板/活动方选模板）全绿 — | 验收: 三连绿+smoke | 测试: smoke 段

## Phase SS-2 — 参与者身份与登录

- [x] **SS2-01** 迁移 0008：whitelist + `company`/`claimed_jwt_jti`；放宽 `phone_number`/`name` 可空、保留 `phone_last4` 必填；替换唯一键含 `company_norm`；event + `identity_require_name/require_phone/multi_company`/`timezone`/`strict_fingerprint` — Create `internal/store/migrations/0008_identity_factors.sql` | 验收: 约束变更幂等(DROP IF EXISTS+重建)、单企业空串键兼容 | 测试: 迁移后约束/唯一键断言
- [x] **SS2-02** identity 增 `company_norm(s)`（lower(trim) 空串归一） — Modify `internal/identity/identity.go` | 验收: 归一正确 | 测试: 多用例
- [x] **SS2-03** repo whitelist 导入改可变表头解析 `employee_number,phone_last4[,name][,phone][,company]`；给全号则派生/校验后4位 — Modify `internal/repo/repo.go`, `internal/organizer/handler.go`(ImportWhitelist) | 验收: 缺必填行跳过并计数；冲突 ON CONFLICT | 测试: 各列组合
- [x] **SS2-04** organizer 导入时写 event 身份开关(`identity_require_*`/`multi_company`)并校验名单列与开关一致 — Modify `internal/organizer/handler.go`, `internal/repo/repo.go` | 验收: 列/开关不一致 400 | 测试: 一致/不一致
- [x] **SS2-05** repo 因子化白名单匹配 `MatchWhitelist(eventID, factors)`（按 company/phone/name 开关组合 WHERE，必校 employee_number+phone_last4） — Modify `internal/repo/repo.go` | 验收: 必校命中、可选按开关 | 测试: 因子矩阵
- [x] **SS2-06** loginguard 增 scope 支持 `participant:{event_id}`（无需改实现，约定 key） — Modify `internal/organizer`? 否→ 使用现有 Guard | 验收: 独立计数 | 测试: 锁定独立
- [x] **SS2-07** participation `POST /p/e/{id}/login`：限流+守卫+turnstile+因子校验→upsert participant(staff,复用 UpsertParticipantFull)+ClaimWhitelist 绑指纹+签 participant JWT(写 claimed_jwt_jti) — Modify `internal/participation/handler.go`, `cmd/server/main.go` | 验收: 错误码齐(bad_credentials/missing_factor/...) | 测试: 成功+各失败
- [x] **SS2-08** participation `POST /p/e/{id}/logout`：jti 加入 `sess:p:{jti}` 撤销集 — Modify `internal/participation/handler.go`, `cmd/server/main.go` | 验收: 登出后令牌失效 | 测试: 登出后 401
- [x] **SS2-09** participation JWT 校验中间件含 jti 撤销检查 + 与 whitelist.claimed_jwt_jti 一致校验（顶号失效） — Modify `internal/participation/handler.go` | 验收: 旧 jti 被顶替即失效 | 测试: 双登录后旧失效
- [x] **SS2-10** participation `GET /p/e/{id}/me`（当前参与者 + 流程进度占位，SS-4 完善） — Modify `internal/participation/handler.go` | 验收: 返回身份 | 测试: 鉴权
- [x] **SS2-11** `GET /p/e/{id}`(Bootstrap) refactor：不再签 device-session，返回 event 元 + flow + 需登录标记 — Modify `internal/participation/handler.go` | 验收: 无 cookie、含 flow | 测试: 响应结构
- [x] **SS2-12** D3 告警：repo `AddWarning` + `participation_warning`（迁移并入 0008 或 0008b） — Modify `0008_*.sql`, `internal/repo/repo.go` | 验收: 落库 | 测试: 插入查询
- [x] **SS2-13** 指纹不一致策略：默认记 `participation_warning`；`event.strict_fingerprint` 则登录/提交拒绝并告警 — Modify `internal/participation/handler.go` | 验收: 两模式行为 | 测试: 宽松/严格
- [x] **SS2-14** repo 解绑事务 `UnbindWhitelist(entryID)`：清 claimed_* + status=unused + null 旧 participant.whitelist_entry_id + 撤 jti — Modify `internal/repo/repo.go` | 验收: 可重新登录绑定新设备 | 测试: 解绑后重登
- [x] **SS2-15** organizer `POST /org/events/{id}/whitelist/{entry}/unbind` + 租户校验 + 审计 — Modify `internal/organizer/handler.go`, `cmd/server/main.go` | 验收: 仅本租户、写 audit | 测试: 跨租户拒绝
- [x] **SS2-16** legacy open participation 收束到 `CAPTAIN_OPEN_PARTICIPATION=off` 默认关闭 — Modify `internal/participation/handler.go`, `cmd/server/main.go` | 验收: 默认匿名路径关闭 | 测试: off/on
- [x] **SS2-17** SS-2 阶段验收：build/vet/test + smoke（导入名单→登录→顶号→解绑→重登）全绿 — | 验收: 三连绿+smoke | 测试: smoke 段

## Phase SS-3 — 活动与内容编排

- [x] **SS3-01** flow schema v2：Step 增 `Stage`(R1-R4/none)；类型扩 `exam`/`lottery`；保留 6 旧类型 — Modify `internal/flow/flow.go` | 验收: 解析新字段 | 测试: 解析用例
- [x] **SS3-02** flow 校验：stage 合法、每 stage 类型绑定(R1=checkin/R2=form/R3=exam/R4=lottery)、entry/next 引用合法、checkin.days>=0 — Modify `internal/flow/flow.go`, `internal/flow/flow_test.go` | 验收: 非法 schema 报错 | 测试: 正反例
- [x] **SS3-03** flow 顺序门禁判定 `CanEnter(stage, doneStages)`（仅串已启用 stage） — Modify `internal/flow/flow.go` | 验收: 跳过未启用、串已启用 | 测试: 组合矩阵
- [x] **SS3-04** flow per-step config 形状校验（form.fields/exam.config/lottery.config） — Modify `internal/flow/flow.go` | 验收: 缺字段报错 | 测试: 每类型
- [x] **SS3-05** 迁移 0009：`exam_question` — Create `internal/store/migrations/0009_exam.sql` | 验收: 幂等 | 测试: 列断言
- [x] **SS3-06** repo exam 题库 import(覆盖式)/list — Modify `internal/repo/repo.go` | 验收: 覆盖旧题、随机题数<=题量校验 | 测试: 导入+越界拒绝
- [x] **SS3-07** organizer `POST /org/events/{id}/exam/import` `GET .../exam` + 审计 — Modify `internal/organizer/handler.go`, `cmd/server/main.go` | 验收: 仅本租户 | 测试: 鉴权+往返
- [x] **SS3-08** 迁移 0010：`lottery_pool`/`lottery_membership`/`lottery_prize`/`lottery_rig_entry`/`lottery_result` — Create `internal/store/migrations/0010_lottery.sql` | 验收: 幂等、UNIQUE/CHECK 就位 | 测试: 约束断言
- [x] **SS3-09** repo lottery 配置：pools upsert / prizes upsert(绑 pool) — Modify `internal/repo/repo.go` | 验收: prize 必属某 pool | 测试: 越界拒绝
- [x] **SS3-10** organizer `POST /org/events/{id}/lottery/pools` `/prizes` + 审计 — Modify `internal/organizer/handler.go`, `cmd/server/main.go` | 验收: is_default 至多一个 | 测试: 多 default 拒绝
- [x] **SS3-11** repo 成员→奖池指派导入（CSV，UNIQUE 强制互斥）+ rig 导入（校验 prize∈成员所属池） — Modify `internal/repo/repo.go` | 验收: 互斥、跨池内定拒绝 | 测试: 冲突/非法
- [x] **SS3-12** organizer `POST .../lottery/membership/import` `/rig/import` + 审计 — Modify `internal/organizer/handler.go`, `cmd/server/main.go` | 验收: 审计每次导入 | 测试: 往返+审计
- [x] **SS3-13** event 创建/更新支持 timezone/identity flags/模板（整合 SS-1/SS-2 字段） — Modify `internal/organizer/handler.go`, `internal/repo/repo.go` | 验收: 字段落库校验 | 测试: 边界
- [x] **SS3-14** CreateFlow/UpdateFlow 走 flow v2 校验 — Modify `internal/organizer/handler.go` | 验收: 非法 flow 400 | 测试: 正反
- [x] **SS3-15** SS-3 阶段验收：build/vet/test + smoke（建活动→配 R1-R4→导题/奖池/名册）全绿 — | 验收: 三连绿+smoke | 测试: smoke 段

## Phase SS-4 — 用户参与流程运行时（R1/R2/R3）

- [x] **SS4-01** submit 鉴权改 participant JWT（去 device-session 依赖），保留限流 — Modify `internal/participation/handler.go` | 验收: 无 JWT 401 | 测试: 鉴权
- [x] **SS4-02** 迁移 0011：`participation` + data_field_1/2/device_id/current_stage/stage_done/completed_all/completed_at + 索引 — Create `internal/store/migrations/0011_records_funnel.sql` | 验收: 幂等+索引 | 测试: 列/索引断言
- [x] **SS4-03** 迁移 0008 已含 `checkin_day`；repo `MarkCheckinDay(participation, day, geo)` 幂等(ON CONFLICT) — Modify `internal/repo/repo.go` | 验收: 同日去重 | 测试: 并发同日仅一条
- [x] **SS4-04** R1 完成判定 `distinct day >= days` + 标记 stage_done.R1 + 推进 current_stage（同事务） — Modify `internal/participation/handler.go`, `internal/repo/repo.go` | 验收: days=0 跳过、N 日门禁 | 测试: 1日/多日/0日
- [x] **SS4-05** 顺序门禁中间逻辑：提交某 stage 前校验前序已启用 stage 完成，否则 409 stage_gated — Modify `internal/participation/handler.go` | 验收: 越级拒绝 | 测试: 门禁矩阵
- [x] **SS4-06** R2 form 记录 + `POST /p/e/{id}/uploads`（multipart→storage，MIME/大小白名单）→ key — Modify `internal/participation/handler.go`, `cmd/server/main.go` | 验收: 非法类型 415 | 测试: 合法/非法
- [x] **SS4-07** R2 提交写 data_field_1(文本汇总)/data_field_2(OSS key) + stage_done.R2 — Modify `internal/participation/handler.go`, `internal/repo/repo.go` | 验收: 字段落库 | 测试: 断言
- [x] **SS4-08** R3 exam 取题 `GET /p/e/{id}/steps/{step}`（确定性选题 by participant+step，不下发 correct） — Modify `internal/participation/handler.go` | 验收: 同人稳定、无答案泄露 | 测试: 确定性+无 correct
- [x] **SS4-09** R3 判分（单/多选累加 score，passed=score>=passScore，attemptLimit） + 记录 + stage_done.R3 — Modify `internal/participation/handler.go`, `internal/repo/repo.go` | 验收: 判分正确、超次拒绝 | 测试: 判分矩阵
- [x] **SS4-10** 每环节必记录到 participation_step_record（统一封装） — Modify `internal/participation/handler.go` | 验收: R1-R4 各落记录 | 测试: 全环节断言
- [x] **SS4-11** 参与人数口径改：完成≥1启用环节即计入；realtime 触发点接“首个启用环节完成” — Modify `internal/participation/handler.go`, `internal/realtime/realtime.go`, `internal/repo/repo.go`(CheckinCount→ParticipatedCount) | 验收: 口径正确 | 测试: 各启用组合
- [x] **SS4-12** 完成人数 completed_all + completed_at（最后启用 stage 完成时置） — Modify `internal/participation/handler.go`, `internal/repo/repo.go` | 验收: 仅全完成置 true | 测试: 部分/全
- [x] **SS4-13** realtime 10s 对账 SQL 改“参与人数”口径 — Modify `internal/realtime/realtime.go`, `internal/repo/repo.go` | 验收: 对账纠偏正确 | 测试: 偏移注入纠偏
- [x] **SS4-14** `GET /p/e/{id}/me` 完善流程进度（current_stage/各 stage done/下一步） — Modify `internal/participation/handler.go` | 验收: 进度准确 | 测试: 多阶段
- [x] **SS4-15** SS-4 阶段验收：build/vet/test + smoke（登录→R1多日门禁→R2图片→R3计分）全绿 — | 验收: 三连绿+smoke | 测试: smoke 段

## Phase SS-5 — 在线抽奖（多奖池/池内内定/审计）

- [x] **SS5-01** 新增 `internal/lottery`：纯函数 `Resolve(pool, rig, prizesRemaining, rng)` → (prize|miss, resolvedBy) — Create `internal/lottery/lottery.go` | 验收: 内定优先、池内加权随机、空库存→miss | 测试 `lottery_test.go` 算法矩阵
- [x] **SS5-02** repo 用户奖池定位（membership→pool；缺失→is_default；仍无→nil） — Modify `internal/repo/repo.go` | 验收: 三路径 | 测试: 各路径
- [x] **SS5-03** repo 原子扣库存 `DrawPrize`：`UPDATE lottery_prize SET drawn=drawn+1 WHERE id=? AND pool_id=? AND drawn<stock RETURNING`（复用条件 UPDATE 范式） — Modify `internal/repo/repo.go` | 验收: 并发不超卖 | 测试: 高并发竞争(N goroutine) 总中=库存
- [x] **SS5-04** repo `lottery_result` 幂等 upsert（UNIQUE(event,step,participant)） + 命中内定占用记录 — Modify `internal/repo/repo.go` | 验收: 重复抽返回同结果 | 测试: 重复请求一致
- [x] **SS5-05** participation `POST /p/e/{id}/steps/{step}/draw`：门禁→Redis `lot:lock` SETNX 快路径→事务结算→审计→（grand 时 publish prize.won） — Modify `internal/participation/handler.go`, `cmd/server/main.go` | 验收: 一人一抽幂等 | 测试: 并发同人仅一抽
- [x] **SS5-06** participation `GET /p/e/{id}/steps/{step}/result` — Modify `internal/participation/handler.go` | 验收: 返回本人结果 | 测试: 鉴权+内容
- [x] **SS5-07** audit：每抽 append `lottery_draw`(pool/resolved_by/prize) — Modify `internal/participation/handler.go`, `internal/audit` | 验收: 每抽一条 | 测试: 计数断言
- [x] **SS5-08** organizer `GET /org/events/{id}/lottery/summary`（各池余量/分布/内定命中，审计访问） — Modify `internal/organizer/handler.go`, `cmd/server/main.go` | 验收: 数字正确 | 测试: 构造数据断言
- [x] **SS5-09** lottery 审计导出：export_job kind=lottery_audit → CSV(逐人逐步) → storage → 签名 — Modify `internal/export/export.go`, `internal/repo/repo.go` | 验收: CSV 全量 + 防注入 | 测试: 行数/内容
- [x] **SS5-10** organizer `POST /org/events/{id}/lottery/audit/export` `GET` 状态 + 审计 — Modify `internal/organizer/handler.go`, `cmd/server/main.go` | 验收: 流转+下载 | 测试: 状态机
- [x] **SS5-11** Redis `lot:stock:*` 热读余量（PG 为准、fail-open） — Modify `internal/repo` 或 `internal/lottery` 缓存层 | 验收: 展示值近似、回源准确 | 测试: 缓存/回源
- [x] **SS5-12** 未指派且无默认池 → resolved_by=miss + 告警审计 — Modify `internal/participation/handler.go` | 验收: miss 路径 | 测试: 无池场景
- [x] **SS5-13** 新增 `cmd/lotterystress` 抽奖并发压测器 — Create `cmd/lotterystress/main.go` | 验收: 不超卖、幂等 | 测试: 跑通报告
- [x] **SS5-14** SS-5 阶段验收：build/vet/test + 并发竞争测试 + smoke（多池/内定/随机/审计导出）全绿 — | 验收: 三连绿+竞争+smoke | 测试: smoke 段

## Phase SS-6 — 活动大屏实时服务

- [ ] **SS6-01** realtime Snapshot→typed Envelope `{type,count?,winner?,ts}`，保留顶层 count 向后兼容 — Modify `internal/realtime/realtime.go` | 验收: 旧大屏仍读 count | 测试: 兼容断言
- [ ] **SS6-02** realtime `OnParticipated` 取代裸 OnCheckin 触发（接 SS-4 口径） — Modify `internal/realtime/realtime.go`, `internal/participation/handler.go` | 验收: 计数触发点正确 | 测试: 触发断言
- [ ] **SS6-03** realtime 消费 NATS `prize.won` → `win:{event}` capped LIST(LTRIM) + 广播 winner 信封 — Modify `internal/realtime/realtime.go`, `cmd/server/main.go` | 验收: 中大奖滚动 | 测试: 注入 prize.won
- [ ] **SS6-04** SSE 连接建立即发 count 快照 +（可选）最近 winners（transient 不回放） — Modify `internal/realtime/realtime.go` | 验收: 重连发快照 | 测试: 重连断言
- [ ] **SS6-05** `milestone` 信封（完成人数等低频附带，可选） — Modify `internal/realtime/realtime.go` | 验收: 低频不刷屏 | 测试: 节流
- [ ] **SS6-06** Stream/count/info/qr 保留回归（确保 refactor 无破坏） — Modify tests | 验收: 旧端点 200 | 测试: 回归
- [ ] **SS6-07** SS-6 阶段验收：build/vet/test + screenstress 不退化 + smoke（中奖推大屏）全绿 — | 验收: 三连绿+压测+smoke | 测试: smoke 段

## Phase SS-7 — 活动记录查看与导出

- [ ] **SS7-01** repo `ListParticipants` refactor：JOIN whitelist 取 name/company/employee/phone_last4/phone；含 device_id/fingerprint/data_field_1/2/current_stage — Modify `internal/repo/repo.go` | 验收: 字段齐 | 测试: 数据断言
- [ ] **SS7-02** organizer `GET /org/events/{id}/participants` refactor：字段对齐 + can_view_records + PII 审计 + 手机脱敏 — Modify `internal/organizer/handler.go` | 验收: 无权限 403、脱敏 | 测试: 权限+脱敏
- [ ] **SS7-03** export 列对齐（含 company/phone_last4/全号/device/fingerprint/data_field/动态 form 键并集）+ 防注入(保留 csvSafe)+BOM — Modify `internal/export/export.go` | 验收: 列齐、注入安全 | 测试: 列+注入用例
- [ ] **SS7-04** export 受 can_export_records + 审计 — Modify `internal/organizer/handler.go` | 验收: 无权限 403 | 测试: 权限
- [ ] **SS7-05** repo 漏斗聚合 `EventStats`（参与人数/完成人数/各 current_stage GROUP BY + 累计到达/完成 Ri） — Modify `internal/repo/repo.go` | 验收: 2万级 GROUP BY 走索引 | 测试: 构造分布断言
- [ ] **SS7-06** organizer `GET /org/events/{id}/stats` — Modify `internal/organizer/handler.go`, `cmd/server/main.go` | 验收: 数字与明细一致 | 测试: 一致性
- [ ] **SS7-07** repo/organizer `GET /org/events/{id}/warnings` 列表（D3） + can_view_records — Modify `internal/repo/repo.go`, `internal/organizer/handler.go` | 验收: 含工号/姓名/类型/时间 | 测试: 数据断言
- [ ] **SS7-08** 告警导出 `POST /org/events/{id}/warnings/export`（异步 CSV→storage→签名） — Modify `internal/export/export.go`, `internal/organizer/handler.go` | 验收: 可下载 | 测试: 状态机
- [ ] **SS7-09** participation `GET /p/e/{id}/me/records`（本人各 step 记录+exam 成绩+抽奖结果） — Modify `internal/participation/handler.go`, `cmd/server/main.go` | 验收: 仅本人 | 测试: 越权拒绝
- [ ] **SS7-10** export download 走 storage.SignedURL（OSS 私有读，短 TTL）；local 代理 — Modify `internal/organizer/handler.go`, `internal/storage` | 验收: 链接短时有效 | 测试: 过期失效
- [ ] **SS7-11** SS-7 阶段验收：build/vet/test + smoke（看记录/漏斗/告警/导出/本人记录）全绿 — | 验收: 三连绿+smoke | 测试: smoke 段

## Phase PF — 集成 / E2E / 收尾

- [ ] **PF-01** `scripts/smoke.sh` 重写全链路：登录→R1多日门禁→R2图片→R3计分→R4多池抽奖(普通/内定)→中奖推大屏→活动方看记录/漏斗/告警→导出CSV→用户查本人 — Modify `scripts/smoke.sh` | 验收: 全链路 0 退出 | 测试: 脚本
- [ ] **PF-02** 多租户隔离回归测试集（跨租户越权全 404/403） — Create `internal/.../tenant_isolation_test.go` | 验收: 全部拒绝 | 测试: 矩阵
- [ ] **PF-03** 并发竞争总测：抽奖不超卖/一人一抽、whitelist claim、多日签到、奖池互斥 UNIQUE — Create 竞争测试 | 验收: 无超卖/无重复 | 测试: 高并发
- [ ] **PF-04** 门控集成节点标注与脚本（Turnstile enforce / 阿里云 OSS / DB dump）——需真 token，给出 env 清单与安全交接说明 — Create `docs/INTEGRATION-GATED.md` | 验收: 文档可操作 | 测试: 评审
- [ ] **PF-05** OpenAPI 重生成（反映 v2 全部端点） — Create `docs/openapi.yaml` | 验收: 与路由一致 | 测试: 端点对账脚本
- [ ] **PF-06** 全局验收：build/vet/test 全绿 + smoke 全绿 + screenstress/lotterystress 不退化 + PROGRESS 收尾 — Modify `docs/PROGRESS.md` | 验收: 全绿、看板更新 | 测试: 全量

---

## 自检（spec 覆盖 / 占位 / 类型一致）

**Spec 覆盖**：DESIGN.md §SS-0~SS-7 + 跨切面(§3) + 迁移序列(§5) + 决策表(§6) 均有对应 Phase/任务（P0 跨切面；SS-0~7 一一对应；迁移 0006-0011 映射至 SS0-08/SS1-01/SS2-01/SS3-05/SS3-08/SS4-02；D2-D10 体现在 SS2/SS4/SS5/SS0 任务）。无遗漏需求。

**占位扫描**：本文件为多子系统**主索引**（skill 允许拆分多计划）；每条目含 Files/验收/测试，非 TBD。详细全代码 TDD 步骤由各子系统执行前即时生成 `docs/superpowers/plans/2026-05-19-ssN-*.md`（依赖在前任务已落地的真实代码，避免投机式占位）。

**类型一致**：repo 方法命名贯穿一致（`MatchWhitelist`/`UnbindWhitelist`/`DrawPrize`/`EventStats`/`ParticipatedCount`）；迁移号 0006-0011 与 DESIGN.md §5 一致；令牌 `Role=participant`+`JTI`+`Perm`+`PermVersion` 在 P0 定义后被 SS-0/SS-2 引用一致。
