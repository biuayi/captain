# REQ-CHANGE-003 — 内部活动：严格三要素签到 + 一次性设备绑定 + 后台实名可见

> 来源：用户 2026-05-17 ~00:55 追加（看护方 Claude 记录）。**新增文件，不并发改
> REQUIREMENTS/PROGRESS/ARCHITECTURE（避免覆盖正在编辑的共享文档）**。
> 本变更**细化并收紧 REQ-CHANGE-001**：本活动定性为「内部活动」，去匿名/外部路径。
> 主实现方请按 §3 动作清单并入，并把结论合并进正式文档（接 T-074 一并做）。

## 0. 用户原文（照录）

> 前端活动参与展示不对。现在修订为内部活动。
> 需要可以查看参与人名称，而不是昵称或者系统UID。可以看到用户名、什么时候签到的、
> 在哪里签到的、用的设备ID、网络IP，浏览器指纹。并展示用户填写的信息。
>
> 用户签到时，需要用户名+工号+手机号完全一致才可以签到成功。签到成功后，绑定设备ID、
> 浏览器指纹。不可以再次签到。如果用户再次签到，直接进入最终的页面。

附：用户给的现状导出片段显示 `identity_type=anon / staff_whitelist`、`identity_value` 空、
只有系统 UID——即当前实现仍按 REQ-CHANGE-001 的匿名/外部并存，与「内部活动实名」诉求不符。

## 1. v1 决策（Claude 拍板，用户已授权代为确认，可调整）

### 1.1 活动定性：内部活动 = 白名单强制、去匿名
- 本 demo 活动（寻道大千·周年庆典）`participant_mode = whitelist_only`（事件级配置）。
- **关闭 external/anon 路径**（代码保留 REQ-CHANGE-001 的 external 实现，仅由事件配置开关
  关闭，不删除——以备将来对外活动复用）。本事件不产生 `anon` 参与者。
- `participant_key` 统一走白名单：`HMAC(pepper,"staff:"+event_id+":"+whitelist_entry_id)`。

### 1.2 签到校验：三要素**完全一致**
- 用户提交 `name + employee_number + phone`（**完整手机号，非末四位**）。
- 三者与某条 `event_whitelist_entry` **全等**（trim 后精确匹配；phone 归一 E.164 后比对；
  name 建议 trim+全角半角空格归一，大小写对 employee_number 敏感）才算签到成功。
- 任一不匹配 → 失败，返回 `WHITELIST_MISMATCH`，不计数、不落参与、不泄漏「哪一项错」。

### 1.3 一次性绑定 + 不可重复签到
- 签到成功瞬间：该 whitelist entry `status: unused → claimed`，写入
  `claimed_fingerprint_hash`、`claimed_device_id`(device_uuid)、`claimed_at`、
  `claimed_participant_id`、`claimed_ip`。生成 participation + checkin step record（计数 +1）。
- **再次签到判定**（同 entry 已 `claimed`）：
  - 同设备/同指纹（device_id 或 fingerprint 命中已绑定值）→ **不重复计数**，直接跳到流程
    **最终页**（`result`/最后一个 step），幂等返回既有参与信息。
  - 不同设备/指纹 → 拒绝 `ENTRY_CLAIMED_ELSEWHERE`（防代签/冒用），不进流程、不计数。
- 大屏计数语义不变（完成 checkin 去重 `participant_key` 数）；一次性绑定天然去重。

### 1.4 后台/导出：实名 + 全维度可见（替换 anon/UID 展示）
后台「参与用户」列表与导出 CSV 列，**至少**包含：
`姓名(来自 whitelist entry，非 UID/昵称)`、`工号`、`手机号`、`签到时间(checkin_at)`、
`签到位置(经纬度/可读地址，来自 REQ-CHANGE-002 T-080)`、`设备ID(device_uuid)`、
`网络IP(claimed_ip / 请求 IP)`、`浏览器指纹(fingerprint_hash，展示可截断)`、
`用户填写的表单信息(participation_step_record 里 form step payload，逐字段展开)`、
`参与状态/最后步骤`。前端三端涉及参与展示处同步：展示实名，不再显示 anon/系统 UID。

## 2. 与 REQ-CHANGE-001 的差异（收紧点）

| 点 | REQ-CHANGE-001 | REQ-CHANGE-003（本次，覆盖） |
|---|---|---|
| 身份范围 | staff 白名单 + external 指纹并存 | **内部活动：仅白名单**，external 由事件配置关闭 |
| 校验强度 | name+employee_number 命中 + phone_last4 challenge | **name+employee_number+完整手机号 三者全等** |
| 重复签到 | 幂等通过 | **不可重复**；同设备→直跳最终页；异设备→拒绝 |
| 绑定 | 首次写 claimed_fingerprint_hash | 追加绑定 **device_id + ip**，一次性 |
| 后台可见 | 未强调 | **强制实名 + IP + 指纹 + 设备 + 位置 + 表单数据** |

## 3. ⚠️ 实现方动作清单

1. 迁移（新建 `0004_*.sql`，**勿改已应用的旧迁移**）：`event_whitelist_entry` 增
   `claimed_device_id text NULL`、`claimed_ip inet/text NULL`（已有 `claimed_fingerprint_hash`
   /`claimed_at`/`claimed_participant_id`）；`participation` 或 step record 确保可存
   `ip`、`device_id`、`fingerprint_hash`、签到经纬度（与 REQ-CHANGE-002 T-080 合并一张迁移）。
2. event 表/配置：加 `participant_mode`（`whitelist_only|mixed`），demo 事件设 `whitelist_only`。
3. `internal/participation` 签到校验：改为三要素完全一致；成功即一次性绑定 device_id+
   fingerprint+ip；再签到分支（同设备→跳最终 step 幂等；异设备→`ENTRY_CLAIMED_ELSEWHERE`）。
   新增错误码 `WHITELIST_MISMATCH`、复用 `ENTRY_CLAIMED_ELSEWHERE`。
4. 采集 IP（信任代理头策略要写明，如 `X-Forwarded-For` 取最左可信）；指纹/设备已在
   REQ-CHANGE-001；位置接 T-080。
5. organizer 后台参与列表 + 导出 CSV：join whitelist entry 出实名/工号/手机；加 IP/指纹/
   设备/位置/表单字段列；**停止输出 anon/UID-only**。
6. 前端（mobile）：签到表单收 姓名+工号+手机号；进入活动若该 entry 已被本设备 claimed →
   直接渲染最终页；admin 端参与展示改实名 + 上述维度。
7. 种子：demo 白名单含完整手机号样例若干；事件 `whitelist_only`。
8. 文档：随 T-074 把 REQ-CHANGE-001/002/003 一并并入 REQUIREMENTS/ARCHITECTURE。

## 4. 待用户晨间确认的假设（不阻塞实现）

- A1：「内部活动」=本事件白名单强制、彻底无匿名/外部；external 代码保留但配置关闭（不删）。
- A2：再次签到=同绑定设备/指纹时**静默跳到流程最终页**（不报错、不重复计数）；
  换设备视为冒用→拒绝（防代签优先于便利；员工真的换机需后台人工解绑/重绑）。
- A3：三要素「完全一致」按 trim 后精确匹配，phone 归一 E.164，name 归一空格/全半角，
  employee_number 区分大小写。如需更宽松（忽略 name 大小写等）请指出。
- A4：IP 取请求来源（经可信代理头）；位置依赖前端 geolocation 授权，拒授权时位置留空、
  不阻断签到（沿用 REQ-CHANGE-002）。
- A5：后台指纹/IP 等 PII 展示仅授权后台可见；导出含 PII 需留审计（沿用 ARCHITECTURE §10）。
