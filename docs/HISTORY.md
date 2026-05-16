# HISTORY — 已完成任务归档

> 完成的任务从 PROGRESS 移入此处（用户流程规则）。两仓库镜像，canonical=check-in-kiosk。
> 格式：任务 · 结论/证据（commit / 验证）。

## M0 需求与方案（DONE）

- **T-001** 原始需求整理写入两仓库 `docs/REQUIREMENTS.md`（原文+角色+系统+功能+选型+存储+开放问题）。
- **T-002** 跨端多 Agent 协作看板 `docs/PROGRESS.md` 建立（协作约定/快照/里程碑/任务/变更日志）。
- **T-003** 与 codex 完成 3 轮头脑风暴并回填 `REQUIREMENTS §9`；codex 第3轮终审无异议。
- **T-004** `docs/ARCHITECTURE.md` 架构设计（模块化单体/三接口域/鉴权防刷/流程引擎schema/实时计数/NATS/数据模型/部署）。
- **T-005** `docs/GIT_CONVENTION.md`（§0 强制 commit+push）+ `docs/CODING_STANDARDS.md` + 两仓库 `AGENTS.md`。

## M1/M2 后端垂直切片（DONE，captain）

- **T-061** 后端垂直切片：扫码`event_token`→`device-session`(HMAC cookie)→签到(PG唯一约束权威幂等)→Redis计数+pub/sub+SSE(≤2/s节流)+10s对账→NATS JetStream异步导出CSV→活动方/超管分域鉴权(bcrypt)→寻道大千主题种子。`go build/vet/gofmt/test` 全绿；`make up && make smoke` 端到端通过；commit `d405a01`→…→`c088026`。
- **codex review 修复**：realtime pub/sub 关闭 panic(HIGH)、cookie Secure/默认密钥告警(HIGH)、submit IP 限流+mint deviceHash 键(MED)、计数本地 apply 兜底(MED)。见 `docs/CODEX-REVIEW-001.md`。commit `90a4516`。
- **压测**：30000 用户/2000 并发，30000 成功签到 0 失败 0 限流，~1458 签到/s；Redis 热计数与 PG 10s 对账自动纠正至 30001（设计的最终一致性验证）。
- **B-1** git push 安全分类器拦截 → 用户授权后直推解决。
- **B-2** docker 守护进程拉镜像超时 → 配 `/etc/docker/daemon.json`+systemd drop-in 代理(127.0.0.1:7897)+宿主预编译运行时镜像，解决；`make up && make smoke` 通过。
- **视觉**：大屏/移动/admin 亮色国风 + 官网真实素材（内嵌 `/assets/news-bg.png` 水墨山水 + `/assets/nav-top.png` 页眉）；红十字会公益联动 + 累计爱心值；rAF 平滑滚动消除 SSE 节流卡顿；新增 `/admin` 内嵌管理 demo 页。commit 至 `bee0e7f`。

## REQ-CHANGE-001 指纹身份+白名单（DONE，captain）

- **T-071/T-072** 迁移 0002(participation 部分索引，codex DB优化)/0003(`event_whitelist_entry` + participant fingerprint/type/whitelist 列+约束)；`internal/identity`(指纹服务端归一化+HMAC pepper)；repo 白名单查/claim(并发安全条件UPDATE)/CSV导入/列表 + `UpsertParticipantFull`；participation staff/external 分支(additive，无 participant_type 走 legacy 保住 demo+压测)；organizer 白名单 CSV 导入/列表；seed 幂等补种白名单。
- 容器验证：staff 命中 / 同设备幂等放行 / 换设备 `ENTRY_CLAIMED_ELSEWHERE` / `PHONE_MISMATCH` / CSV 导入 / 白名单回补 / legacy+external 兼容 / 计数去重语义不破。`go build/vet/test` 全绿。commit `c088026`。
- codex 设计评审定稿见 `docs/CODEX-REVIEW-001.md`（含 DB 优化 defer 论证、多 Agent 泳道）。

## M1/M3 前端 monorepo（DONE 脚手架+构建验证，check-in-kiosk）

- **T-011** npm workspaces monorepo：`shared / mobile / big-screen / admin`；统一 tsconfig.base、vite、dev 代理 `/api`+`/assets`→captain。
- **T-030** `@kiosk/shared`：契约类型(对齐 captain + REQ-CHANGE-001)、fetch API client（参与/活动方/超管全接口）、被动浏览器指纹采集（服务端重算）、deviceId/格式化。
- 验证：`npm install`(71包) OK；`npm run typecheck` 干净；mobile/big-screen/admin `vite build` 全绿(dist 产出)。commit `16e6f67`。
- 说明：T-031/032/033/034 三端 UI 已实现并构建通过，但**浏览器端到端联调未做**（需前端跑起来连后端栈），状态 REVIEW，见 PROGRESS。

## 安全硬化 + 大屏改版（DONE，captain `10fd961` / kiosk `c16ddca`）

- **T-083 / 凭据硬化（REQ-CHANGE-002）**：强随机 `CAPTAIN_TOKEN_SECRET`/`IDENTITY_PEPPER` + 强种子口令（`deploy/.env` gitignored，`make` 自动 openssl 生成）；管理后台路径混淆 `CAPTAIN_ADMIN_PATH`——`/admin` 与 `/api/v1/admin` 不再注册→404，控制台/admin API 走 env slug，webui 注入混淆基址。容器验证：`/admin`→404、`/api/v1/admin/login`→404、`/mgmt-xxxx`→200、无默认密钥告警。**公网暴露前置硬化完成**。
- **大屏改版（活动方反馈）**：弃红十字/数值爱心值 → 广东省游心公益基金会 + 一颗爱心；每签到（SSE 计数增长）爱心跳动一下；亮度=进度=`count/(预计×90%)`，90% 预计人数最亮；新增公开 `GET /api/v1/p/e/{id}/info`。embedded screen.html + React big-screen 同步。logo 待活动方提供。

## React 正式 UI + 大屏定稿 + 登录硬化 + Turnstile（DONE，captain `a8231f9` / kiosk `183f766`）

- **React 三端接为正式对外 UI**：captain 内嵌 check-in-kiosk 构建产物，路由 `/m/{id}`=mobile、`/{adminslug}`=admin（注入 `__ADMIN_SEG__` 适配路径混淆）、`/m-static /a-static /s-static` 静态；vite per-app base 隔离；公网隧道验证 200。临时内嵌 mobile/admin 页已退役。
- **大屏定稿（codex 隔离重设计）**：用户两次"丑"+"去大爱心"反馈 → codex 在**不读代码**的隔离 brief 下重设计：宣纸水墨、书法巨字 count、描金 SVG 进度环（取代大爱心）、墨点涟漪签到反馈、SSE 重连+beforeunload 清理。落为 `/screen`。
- **大屏扫码签到二维码**：`GET /api/v1/p/e/{id}/qr`（skip2/go-qrcode，按请求域名+活动ID 生成公网入场链接 PNG），大屏右下角"扫码签到"卡片；活动方亦可用。
- **mobile 游心 logo**：用户端同步加广东省游心公益基金会 logo+署名。
- **登录硬化（REQ-CHANGE-004 T-090）**：organizer/admin 登录恒定 ~3s 延迟 + 同账号连续 10 次失败锁定 10 分钟（Redis，独立域）；容器验证 3.05s/第11次 423。
- **Cloudflare Turnstile（T-091/092）**：服务端 siteverify 校验（organizer/admin 登录 + 参与 checkin）；前端挂件（shared 懒加载 + mobile签到 + admin登录），按 `/api/v1/p/config` 的 sitekey 渲染；`CAPTAIN_TURNSTILE_MODE` 默认 `off`（惰性、无回归，已验证），`enforce` + 真密钥即生效。**激活待用户提供 Turnstile sitekey/secret**（见 PROGRESS B-TS）。
- 凭据页泄漏行已移除（公网安全）。REQ 文档我方改 `004` 避让协作 Agent 的 `003`。
