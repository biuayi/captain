# REQ-CHANGE-002 — 签到记录位置 + 凭据安全（明文禁止）

> 来源：用户 2026-05-16 追加。新增文件（不并发改 REQUIREMENTS/PROGRESS，避免覆盖）。
> 状态：**已记录，待实现**（用户要求"先记着，待会再做"）。

## 1. 签到记录用户位置（待实现）

- 用户签到（`checkin` step 提交）时记录用户位置。
- v1 拟定（待 codex/用户确认，不阻塞）：
  - 前端 H5 用 `navigator.geolocation` 取经纬度（需用户授权），随 checkin 提交 `{lat,lng,accuracy}`。
  - 后端落库：`participation_step_record.payload` 先存（最小改动）；或 `participation` 加 `checkin_lat/checkin_lng/checkin_accuracy`（需迁移）。倾向后者便于导出/统计。
  - 拒授权策略：位置可空，不阻断签到（除非活动要求强制，作为活动配置项 v1.x）。
  - 合规：位置属个人信息（PIPL），需明示用途、最小必要、可不采集；导出含位置需脱敏选项（v1.x）。
- 关联：大屏计数语义不变；位置仅作登记/风控旁证。

## 2. 凭据安全：存储与传输禁止明文（约束，部分已满足）

| 项 | 现状 | 待办 |
|---|---|---|
| 密码**存储** | ✅ 已合规：organizer/admin 用 `bcrypt`（`bcrypt.GenerateFromPassword` / `CompareHashAndPassword`），DB 仅存 `password_hash`，无明文列 | 维持；新增账号体系沿用 |
| 密码**日志** | ✅ 已合规：登录处不打印密码；统一错误响应不回显凭据 | 评审时复查不新增泄漏 |
| 密码**传输** | ⚠️ 缺口：当前 demo 走 HTTP（本机）。登录 body 明文 JSON | **生产强制 HTTPS/TLS**（部署项）：反代/网关 TLS 终止；HSTS；`Secure` cookie（已对 session cookie 在 HTTPS 下置 Secure） |
| token/pepper/secret | ✅ 走 env；默认值启动告警 | 生产必须改强随机（已有 WARNING） |

**结论**：存储/日志已非明文，符合"不可用明文"对存储的要求；传输明文是部署层问题，列为上线必做项（见 PROGRESS 任务）。本条不涉及代码功能缺陷，属部署与合规约束。

## 3. 字段/URL 混淆（待实现，含取舍说明）

用户要求：用户名、密码、token 字段混淆；管理员登录后台 URL 混淆。

**取舍（必须先认知）**：真正的传输/存储防护是 **TLS + bcrypt（已具备）**；字段名与 URL 混淆属 *security-through-obscurity*，仅提高扫描/自动化攻击门槛，不替代 TLS。会牺牲 API 契约可读性与 OpenAPI 直观性。用户已明确要，作为纵深防御接受，但 **TLS 仍是上线前提**（T-081）。

v1 拟定方案（待实现/可调整）：
- **字段混淆**：登录请求体字段名由直白 `login_name/password` 改为不透明键（如 `u`/`p` 或配置化映射）；token 不再用 `token`/`Authorization: Bearer` 直白命名，改自定义头/字段名（配置化）。值层面可加一层可逆编码（base64/约定变换）增加门槛——但**不当作加密**（加密靠 TLS）。前后端契约同步（OpenAPI 标注内部字段名）。
- **管理员后台 URL 混淆**：`/admin` 与 `/api/v1/admin/*` 路径前缀改为来自 env 的不可猜 slug（如 `/__c7f3.../...`），不在公开文档暴露；普通用户/活动方接口路径不变。错误响应对未知路径统一 404，不泄漏存在性。
- 不做过度：不自造加密、不混淆全部业务字段（仅凭据与管理面），避免过度工程。

## 4. 动作（PROGRESS 已建任务）

- T-080 签到位置采集与落库（待实现：小迁移 + 前端 geolocation + 提交字段 + 导出列）
- T-081 上线前 TLS/HTTPS 强制 + 安全复查（部署，M5）
- T-082 凭据字段名/值混淆（登录与 token，配置化，前后端契约同步）
- T-083 管理员后台 URL/路径混淆（env slug，未知路径统一 404）
