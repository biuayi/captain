# 架构设计 — 活动互动平台

> 基于 REQUIREMENTS.md §9/§10 的头脑风暴定稿。两仓库同步，canonical = check-in-kiosk。
> 创建：2026-05-16

## 1. 总体

- 后端 `captain`：Go，**模块化单体**，单进程多模块。
- 前端 `check-in-kiosk`：React + Vite monorepo（big-screen / mobile / admin / shared）。**本期重点是后端，前端后续迭代。**
- 存储：PostgreSQL（事实来源）+ Redis（热路径瞬态）+ NATS JetStream（持久异步）+ 对象存储（导出/资源）。
- 本地无服务器：`docker compose` 拉起 postgres / redis / nats / captain；对象存储用本地 FS 实现（接口隔离，生产换阿里云 OSS）。

## 2. 三接口域（统一前缀 `/api/v1`）

| 域 | 前缀 | 调用方 | 鉴权 |
|---|---|---|---|
| participation | `/api/v1/p` | mobile / big-screen | event_token（标识活动）+ device-session（HttpOnly cookie）|
| organizer | `/api/v1/org` | 活动方后台 | organizer 登录 → JWT；租户中间件强制 `organizer_id` 作用域 |
| admin | `/api/v1/admin` | 超管后台 | admin 登录 → JWT（独立用户域）|

错误响应统一：`{"code":"<machine_code>","message":"<human>","request_id":"..."}`。

## 3. 鉴权与防刷

- **event_token**：HMAC 签名串，claims `{v,purpose:event_entry,event_id,iat,nbf,exp,kid}`，有效期至 `event_end+24h`。仅证明 QR 属于该活动，不防重放。
- **device-session**：落地 `GET /p/e/{event_id}?et=` 校验 event_token 后签发；HttpOnly cookie；claims `{sid,event_id,device_uuid_hash,kind:participant_session,iat,exp}`；15min 滑动 idle、8h 绝对、`event_end+2h` 硬过期。
- **device_uuid**：前端 localStorage 持久化 UUID，随首次落地上报；服务端作为无强身份时的 `participant_key`。
- **幂等键**：`submit:{event_id}:{step_id}:{participant_key}`，`participant_key = sha256(identity)` 若该 step 已采集强身份否则 `device_uuid`。Redis `SET k v NX EX (event_end+24h)` 快路径；PG 唯一约束兜底。
- **限流**（Redis 计数器，滑窗）：入口 IP 120/min·burst240、device 30/min；签发 device 10/min；提交 device·event 6/min·burst10、IP 60/min·burst120。
- **降级**：被攻击 → 收紧可疑 IP、入口加 JS 挑战、大屏推送放宽到 1/s、Redis 不健康降级 PG-only 幂等。

## 4. 用户侧流程引擎

线性流，无分支（v1）。step 类型 6 种：`checkin` / `form` / `game` / `charity` / `reward` / `result`（`charity`/`reward` 为《寻道大千》周年庆需求新增，见 REQUIREMENTS §11）。"闯关" = 多个 `game` step 顺序串联，不引入分支。

主题（寻道大千周年庆）：水墨国风 + 修仙玄幻 + 丑萌小妖；配色 墨黑 #1a1a1a / 宣纸米白 #f5f0e6 / 朱砂红 #c0392b / 竹青 #4a7c59 / 描金 #c9a227。主题信息放在每个 step 的 `config.theme` 与 event 级 `screen_template_code`，前端据此渲染，后端不耦合样式。

`flow_config.schema_json`：

```json
{
  "version": 1,
  "flowId": "string",
  "name": "string",
  "entryStepId": "string",
  "steps": [
    { "id": "string", "type": "checkin|form|game|result",
      "title": "string", "required": true, "skippable": false,
      "nextStepId": "string|null", "config": { } }
  ]
}
```

per-step config：
- `checkin`: `{countTowardAttendance:true, dedupeMode:"participant_key", buttonText}`
- `form`: `{fields:[{id,type:text|phone|email|select,label,required,placeholder,options?}]}`
- `game`: `{gameType:"quiz_single_choice", attemptLimit, question, options[], correctOptionIndex}`
- `charity`: `{title, body(公益宣传文案), imageUrl?, ctaText:"我要参与公益", pledgeField?}`
- `reward`: `{title, body, rewardImageUrl?, redeemCode?, redeemNote(领取说明)}`
- `result`: `{mode:"game_outcome", successTitle, winTitle, loseTitle}`

前端按 `type` dispatch 到 renderer；后端按 `type` 校验 + 记录 step record。`landing` 不属流程引擎（入场层处理）。

## 5. 大屏实时计数

- 定义：参与人数 = 完成 `checkin` 的去重 `participant_key` 数 / event。
- 写：accepted checkin → PG 唯一约束 `(event_id, participant_key)` on participation；新行 → Redis `INCR count:{event_id}:checkin` → publish `rt:event:{event_id}`。
- 推：SSE `GET /api/v1/p/events/{id}/stream`，订阅 redis channel，全量快照 `{event_id,count,version,ts}`，≤2/s 节流合并。
- 对账：每 10s 活跃 event：PG `COUNT(distinct participant_key)` vs Redis，偏移则重置 Redis 并立即推校正快照。
- 重连：SSE 连接即刻发当前快照（Redis→PG fallback），无需回放。

## 6. NATS JetStream

| subject | 生产 | 消费 | 用途 |
|---|---|---|---|
| `checkin.submitted` | participation | realtime/analytics | 解耦计数副作用、审计 |
| `participant.step_completed` | participation | analytics | 行为留痕 |
| `export.requested` | organizer | export worker | 异步导出 |
| `export.completed` | export worker | organizer 通知/状态 | 完成回写 |

Stream `CAPTAIN`，at-least-once，消费者幂等（按 step record / job id）。

## 7. 数据模型（核心表）

| 表 | 关键字段 | 约束 |
|---|---|---|
| `organizer` | id, name, login_name(uniq), password_hash, status, created_at | |
| `admin_user` | id, login_name(uniq), password_hash, status, created_at | 与 organizer 物理分表 |
| `event` | id, organizer_id(fk), name, status, start_at, end_at, expected_count, screen_template_code, flow_config_id(fk), created_at | start_at<end_at |
| `flow_config` | id, organizer_id, name, version, schema_json(jsonb), published, created_at | |
| `template` | id, kind(screen/game), code(uniq), name, config_schema(jsonb), status | v1 仅内置种子 |
| `asset` | id, owner_type, owner_id, storage_key, url, mime, size, status, created_at | OSS/本地 FS |
| `participant` | id, event_id(fk), participant_key, identity_type(anon/phone), identity_value, profile(jsonb), first_seen_at | uniq(event_id, participant_key) |
| `participation` | id, event_id, participant_id(fk), checkin_at, status, last_step_id | uniq(event_id, participant_id) |
| `participation_step_record` | id, participation_id(fk), step_id, step_type, payload(jsonb), occurred_at | uniq(participation_id, step_id) |
| `export_job` | id, organizer_id, event_id, format, status(pending/running/done/failed), storage_key, error, requested_at, finished_at | |

## 8. 后端目录（captain）

```
cmd/server/main.go          组装与启动
internal/config             env 配置
internal/httpx              router/中间件/错误/SSE
internal/store              pg(pgx)/redis/nats 客户端 + 迁移
internal/storage            对象存储接口 + local FS 实现 + aliyun OSS（stub）
internal/auth               token(event/session/jwt)、密码、限流、租户
internal/event              活动 CRUD
internal/flow               流程 schema 校验
internal/participation      landing/session/step submit/checkin
internal/realtime           计数 + pubsub + SSE + 对账
internal/export             导出 job + worker
internal/organizer          活动方接口装配
internal/admin              超管接口装配
migrations/                 SQL 迁移（goose 嵌入）
deploy/                     docker-compose / Dockerfile
scripts/smoke.sh            端到端冒烟
```

## 9. 部署（本地）

`docker compose up` → postgres:5432 / redis:6379 / nats:4222 / captain:8080。
captain 启动自动跑迁移 + 种子（1 个 demo organizer/admin、1 个 demo event + checkin 流程）。
冒烟脚本 `scripts/smoke.sh` 跑完整切片并打印结果。

## 10. 安全与合规留痕

- 密码 bcrypt；JWT/HMAC 密钥来自 env（dev 有默认，生产必填）。
- 普通用户匿名为主，手机号为可选弱身份，导出含 PII 时后台操作留审计（v1 记 export_job + 谁触发）。
- 详细见 binary/secret 审计在上线前单独做（参考 iot 经验）。
