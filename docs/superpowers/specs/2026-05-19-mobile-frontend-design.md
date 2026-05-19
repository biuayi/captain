# check-in-kiosk · mobile 参与者端 v2 设计

> 子项目 1/3（mobile）。big-screen、admin 各自后续 设计→计划→实现 周期。
> 后端契约 = `captain/docs/openapi.yaml`（v2）+ `captain/docs/DESIGN.md`。
> 本 spec 经可视化伴侣 brainstorm 定稿（2026-05-19）。

## 0. 已定决策（brainstorm 结论）

| # | 决策 | 选择 |
|---|---|---|
| D1 | 现有 v1 前端 | **重置**：弃旧 packages，全新脚手架 |
| D2 | 技术栈 | React 18 + Vite + TS，静态 SPA（约束：dist 内嵌 captain Go 单二进制） |
| D3 | 首个子项目 | **mobile 参与者端**（最核心，覆盖最多 v2 API） |
| D4 | 视觉方向 | **B 活动喜庆暖色**（暖橙红 #e8462e/#f2922f、圆润、转盘氛围），主题 token 化 |
| D5 | 流程壳 | **③ 全屏沉浸逐屏 + 极简"续接"首屏**（非清单 Hub） |
| D6 | 渲染架构 | **固定环节组件**（R1 签到/R3 考试/R4 抽奖）+ **R2 问卷按 flow form.config.fields 动态渲染**；引擎只管 启用/顺序/门禁/续接 |

## 1. 架构与栈

- React 18 + Vite + TypeScript；新建 monorepo（npm workspaces），本期产出 `packages/mobile` + 必要的 `packages/shared`。
- `packages/shared`：`api`（强类型 client，对 `docs/openapi.yaml`）、`types`、`auth`（JWT 存取）、`theme`、`turnstile`、`ErrorBoundary`。
- React Router 管路由；数据层 = 轻封装 `fetch`（统一错误码映射，无重型状态库）。
- `vite build` 产物覆盖 `captain/internal/webui/embed/mobile/`，`cmd/server` 单二进制直接服务（保留现有 embed 管线）。

## 2. 路由与流程壳

- 入口 `/m/{event_id}?et=<event_token>`。
- 落地：`GET /api/v1/p/e/{id}?et=` → 得 `event` 元 + `flow` + `need_login` + 身份因子开关（见 §8 后端微调）。
- 未登录 → **登录屏**（沉浸全屏）。已登录（localStorage 有该 event 的 JWT）→ **续接首屏**：进度点 + 当前环节名 + "继续"按钮；R1 未满显示「今日已签 / 还需 N 天」。
- "继续" → `FlowShell` 据 flow 启用环节集 + 各环节完成态/门禁，算出"应做环节" → 全屏进入该环节屏。
- 每环节完成 → 回续接首屏（**不是清单**）→ 自动指向下一启用环节。全部启用环节完成 → **完成屏**（CTA「查看本人记录」）。
- 门禁：前端引擎据完成态阻止越级；后端 `409 stage_gated` 作兜底提示。

## 3. 组件分解

- `FlowShell` — 续接首屏、路由、门禁判定、进度点、环节调度。
- 固定环节组件：
  - `Login` — 因子表单（§6）；成功存 JWT。
  - `R1Checkin` — 多日：今日签到按钮 + `x/N` 进度；可选地理位置授权（geo 进 submit body）；今日已签则禁用并提示次日再来。
  - `R2Survey` — **动态**：按 `flow` 中该 step 的 `form.config.fields` 渲染 `text/textarea/select/phone/email/image`；`image` → `POST /uploads` 得 key 再随 submit。
  - `R3Exam` — `GET steps/{id}` 取题（不含答案）；单/多选作答；提交后展示得分/是否通过；尊重 `attemptLimit`。
  - `R4Lottery` — 转盘动画（暖色 B）；`POST .../draw`（幂等）；结果弹层；中 `grand` 文案提示「已推送大屏」。
  - `Result`、`MyRecords`（`GET /me/records`：各环节记录 + 考试成绩 + 抽奖结果）。
- shared：`apiClient`、`useAuth`、`ThemeProvider`、`Turnstile`、`ErrorBoundary`。

## 4. 数据流与 API

- 鉴权：`POST /p/e/{id}/login` → 参与者 JWT 存 `localStorage`（key 按 event 隔离）；受保护请求带 `Authorization: Bearer`。
- 失效：`401 session_revoked/session_superseded/no_session` → 清 token，回登录屏并提示。
- 环节接口：`GET /p/e/{id}/steps/{step}`、`POST .../submit`、`POST .../draw`、`GET .../result`、`GET /me`、`GET /me/records`、`POST /p/e/{id}/uploads`。
- mobile **不接 SSE**（SSE 属 big-screen）。
- 所有请求经 shared `apiClient`：注入 baseURL、Bearer、`X-Request-Id` 透传展示、错误码→中文提示表。

## 5. 主题 token

- 默认 = 风格 B 暖橙红 token 集：`--c-primary #e8462e`、`--c-accent #f2922f`、`--c-bg #fff7ef`、`--c-surface #fff`、`--radius`、`logo`、`title`。集中 `theme.ts` + CSS 变量，全组件只用变量。
- 预留按活动方覆盖钩子（event 元后续可带品牌色/logo）。**模板引擎本期不做**（YAGNI）。

## 6. 错误与边界

- 登录字段随 event 身份开关：`employee_number` + `phone_last4` **必填**；`name`/全 `phone`/`company` 按 `identity_require_name/phone`、`identity_multi_company` 决定是否渲染并必填。
- **后端微调依赖**（写入实现计划，captain 侧小改）：落地响应（`GET /p/e/{id}` 或 `/api/v1/p/config`）需暴露 `identity_require_name/identity_require_phone/identity_multi_company`，前端据此渲染必填项。
- 其余：R1 跨天（续接屏感知今日是否已签）；JWT 顶号/撤销→回登录；网络失败可重试；Turnstile 开启时登录/签到带 token（沿用 `/api/v1/p/config` 暴露 sitekey/mode）；上传 MIME/大小前端预校验（与后端白名单一致）；活动未开放/已结束/不存在的明确文案。

## 7. 测试

- 单元：Vitest + React Testing Library — 门禁判定、动态表单渲染、exam 计分展示、续接屏 R1 进度文案、错误码映射。
- E2E：Playwright 对**真 captain**（复用 `scripts/e2e.sh` 同款 docker pg/redis/nats + seed v2 demo，固定 demo 活动/白名单 E1001/手机后4位 1234）跑 登录→R1→R2→R3→R4→记录；无头截图供视觉走查迭代。
- 离线 CI：组件单测独立跑（不依赖后端）。
- 验收基线：`npm run typecheck` + `vite build` 净；Playwright e2e 绿；产物可覆盖 embed 且 `cmd/server` 服务正常。

## 8. 范围边界 & captain 侧改动

- 本期仅 `packages/mobile` + 必要 `shared`。big-screen、admin 后续各自周期。
- 不做通用流程渲染引擎、不做模板引擎。
- captain 侧仅两处：① 落地响应暴露身份因子开关；② 前端 build 后覆盖 `internal/webui/embed/mobile/`（embed 管线不变）。

## 9. 下一步

本 spec 经自检与你复核后 → `writing-plans` 拆 mobile 实现级任务（脚手架→shared→FlowShell→各环节组件→主题→测试→embed），逐任务实现 + 截图走查 + commit。
