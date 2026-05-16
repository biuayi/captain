# REQ-CHANGE-001 — 身份策略变更：指纹识别 + 活动方预导入白名单

> 来源：用户 2026-05-16 ~22:00 追加需求（看护方 Claude 记录，已与 codex 头脑风暴定稿）。
> **本变更覆盖 REQUIREMENTS §10-P1**（原「匿名 device_uuid 为主」）。
> 此文件为**新增文件**（不改动主实现方正在编辑的 REQUIREMENTS/PROGRESS/ARCHITECTURE，避免并发覆盖）。
> 主实现方请在下一阶段把本文件结论并入正式文档并据此调整数据模型。

## 0. 用户原文（照录）

> 1. 用户扫描通常使用微信扫描，因为要确定用户唯一性，需要收集用户的微信ID。
>    但是我可能没办法和微信进行备案。如果有其他的可能，我想要通过记录用户的
>    浏览器指纹+设备信息判定唯一设备进行标识。
> 2. 用户签到时，需要填写姓名，公司工号。这里边也需要确定唯一性。为了有效性
>    校验，防止其他人搞破坏，必须要活动方提供 姓名 + 工号 + 用户手机 提前导入，
>    作为校验手段。

补充背景：活动面向「部分玩家 + 公司内部员工」——外部玩家**无工号**，需内外共存。

## 1. v1 决策（codex × Claude 定稿，用户已授权代为拍板，可后续调整）

### 1.1 身份与唯一性
- 拿不到微信 openid → 不依赖微信。`device_uuid` 降级为会话连续性辅助；
  **`participant_key` 仍是全链路唯一键 + 大屏去重键**。
- 浏览器指纹信号（被动采集，不采 IMEI/MAC/通讯录）：UA family+major、platform/OS、
  language、timezone、screen w/h/colorDepth、devicePixelRatio、hardwareConcurrency、
  deviceMemory、maxTouchPoints、WebGL vendor/renderer hash、Canvas hash、AudioContext hash。
- 服务端必须重算 canonical hash：`fingerprint_hash = HMAC-SHA256(server_pepper, normalized_signals)`，
  不信任前端直接提交的固定值。`server_pepper` 走 env。
- 容忍原则：**宁可少量误拆，不可轻易误合**；换设备/清缓存默认视为新用户；
  同设备同浏览器大版本升级仍命中。
- `participant_key` 推导：
  - 员工命中白名单：`HMAC(pepper, "staff:"+event_id+":"+whitelist_entry_id)`
  - 外部玩家（无白名单）：`HMAC(pepper, "external:"+event_id+":"+fingerprint_hash)`
  - 员工未过白名单校验：拒绝，不生成 staff key。

### 1.2 预导入白名单数据模型 — **新增表 `event_whitelist_entry`**
字段：`id`(uuid pk)、`event_id`(fk,notnull)、`organizer_id`(fk,notnull,冗余便于租户过滤)、
`employee_number`(text,notnull)、`name`(text,notnull)、`phone_number`(text,notnull,E.164归一)、
`phone_last4`(text,派生缓存)、`status`(enum `unused`/`claimed`/`blocked`,default `unused`)、
`claimed_participant_id`(uuid,null,fk)、`claimed_fingerprint_hash`(text,null)、
`claimed_at`(timestamptz,null)、`blocked_reason`(text,null)、`import_batch_id`(text,null)、
`created_at`/`updated_at`。
- 唯一键：`uniq(event_id, employee_number)`。`name` 不做唯一键，仅交叉校验。
- 匹配：`employee_number` 主查 → `name` 精确校验 → `phone_last4` 二次验证。
- 内外共存：同一 event 支持 mixed mode；员工走 whitelist-required，外部玩家走 open-enrollment（不要求工号）。

### 1.3 校验与防破坏流程
- 用户先选 `participant_type = staff | external`。
- staff：必填 `name + employee_number`，命中白名单后再校验 `phone_last4`（challenge）；
  首次通过即把 entry 置 `claimed` 并写 `claimed_fingerprint_hash`（一次性设备绑定）。
- external：不填工号，走 fingerprint 身份。
- 同一 entry 后续来自不同 `fingerprint_hash` → 默认拒绝（"已在其他设备登记"）；
  同设备重试幂等放行。换机 v1 仅后台人工解绑/重绑（防代签），自助申诉为 v1.x。
- 限流 key：`fingerprint_hash` 优先，`device_uuid` 补充。`event_token` 与分级限流不变。
- 大屏计数定义不变（完成 checkin 去重 `participant_key` 数），仅 key 来源变化。

## 2. 对锁定设计的差异清单（实现方据此纠偏）

| 变更点 | 内容 | v1/v1.x |
|---|---|---|
| `participant` 表 | 新增 `fingerprint_hash text`、`participant_type enum(staff/external)`、`whitelist_entry_id uuid null` | **v1** |
| `identity_type` 枚举 | 由 `anon/phone` 扩为含 `staff_whitelist / external_fingerprint` | **v1** |
| 新增表 | `event_whitelist_entry`（见 §1.2），必关联 event | **v1** |
| `flow_config.schema_json` `form` step | config 增 `participantTypeSelector`、`staffFields(name,employee_number,phone_last4)`、`externalFields` 最小 shape | **v1** |
| 参与提交接口 | 接受 `participant_type` + fingerprint payload + staff 的 `phone_last4`；新增错误码 `WHITELIST_MISS`/`PHONE_MISMATCH`/`ENTRY_CLAIMED_ELSEWHERE`/`ENTRY_BLOCKED` | **v1** |
| organizer 后台接口 | 新增白名单 import / list / detail / status_reset / rebind | **v1** |
| 指纹碰撞审计、跨 event 名单复用、员工换机自助申诉 | — | v1.x |

## 3. ⚠️ 实现方动作清单（最重要）

1. `internal/store/migrations/0001_init.sql`：新增 `event_whitelist_entry` 表；
   `participant` 加 `fingerprint_hash` / `participant_type` / `whitelist_entry_id`；
   `identity_type` 用 text 不用 PG enum（便于后续扩展）。**幂等/可重入种子**。
2. `internal/participation`：参与提交支持 `participant_type` 分支 + 白名单校验 + 指纹 hash（服务端重算）。
3. `internal/organizer`：白名单 CSV 导入 + 列表/重置/重绑接口。
4. 种子「寻道大千·周年庆典」补一份示例白名单（几条 staff + 允许 external）。
5. 完成后并入正式 REQUIREMENTS（覆盖 §10-P1）/ ARCHITECTURE（§3/§5/§7）/ PROGRESS（新任务 + §5 changelog）。

## 4. 待用户晨间确认的假设（codex+Claude 暂定，不阻塞实现）

- A1：外部玩家完全开放报名（仅指纹去重，不要求任何登记）——若活动要求外部玩家也登记姓名/手机，需补。
- A2：员工换机 v1 不自助放行（防代签），仅后台人工重绑——若现场员工换机频繁，体验差，可能要放宽。
- A3：`phone_last4` 作为防冒用 challenge 足够（非完整手机号/OTP）——安全等级中。要更强则上短信 OTP（v1.x）。
- A4：白名单按 event 粒度导入（不跨 event 复用）。
