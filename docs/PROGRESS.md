# 进度看板 — captain 服务端 v2 重做

> 单一权威设计 = [`docs/DESIGN.md`](DESIGN.md)。任务索引/勾选 = [`docs/superpowers/plans/2026-05-19-captain-v2-roadmap.md`](superpowers/plans/2026-05-19-captain-v2-roadmap.md)。
> 自主执行中：subagent/直接 TDD，逐任务 commit，状态以 git + 路线图勾选为准（跨上下文可续）。
> 续点 = 路线图中第一个 `- [ ]` 未勾选项。基础设施：`scripts/testdb.sh up`（pg/redis/nats）。

## 阶段状态

| 阶段 | 状态 |
|---|---|
| 探查/设计/路线图 | DONE（74aa371..47c03d7） |
| P0 跨切面基座（16） | DONE（token/cryptobox/config/httpx-reqid/orgperm/authz/迁移0006/audit/platformcfg/testdb；build·vet·test ./... 全绿，真库验证） |
| **SS-0 平台基座（19）** | **DONE**（账号/权限+版本/软删/改密/加密配置/审计/DB导出/迁移0006·0006b；admin 集成测试+真库全绿） |
| SS-1 模板（10） | DONE（迁移0007/storage SignedURL/admin模板CRUD+资源/org可见性+缓存；真库全绿） |
| SS-2 身份与登录（17） | DONE（迁移0008/因子登录/顶号/解绑/D3严格/legacy门控；登录流集成测试真库全绿） |
| SS-3 编排（15） | DONE（flow v2/exam/多奖池配置/event config；repo+迁移+flow 测试真库全绿） |
| SS-4 运行时 R1/R2/R3（15） | DONE（迁移0011/JWT提交/D5门禁/R1多日/R2上传/R3计分/漏斗；运行时集成测试真库全绿） |
| SS-5 抽奖多奖池（14） | DONE（lottery算法/原子抽奖/幂等/内定/审计导出；并发不超卖测试真库全绿） |
| SS-6 大屏（7） | DONE（typed信封/OnParticipated/prize.won消费/winner滚动；真库全绿） |
| SS-7 记录与导出（11） | DONE |
| PF 集成/E2E/收尾（6） | DONE（租户隔离/并发竞争/门控文档/openapi/smoke；build·vet·test ./... 全绿真库） |

## 续做说明

- 验收基线每阶段尾跑 `go build ./... && go vet ./... && go test ./...`（含 docker pg/redis/nats）。
- DB 测试用 `internal/testdb`，无基础设施时 `t.Skip` 保持离线绿。
- 决策记 DESIGN §6；无法决策项与 codex 读原始需求后定（暂无）。

## 审查后修复（2026-05-19，对照原始需求+DESIGN 的缺口审计）

- S1 /dl 无鉴权 → 改 HMAC 签名+过期校验（commit）
- S2 上传文件名 → storage.SafeName 净化（commit）
- G1 seed 仍 v1 → 升级为 v2 R1-R4+exam+多奖池 demo（commit）
- G2 device_id 永空 → submit 采集落库（commit）
- G4 -race 并发回归全绿；20k 压测登记为门控手动步（INTEGRATION-GATED）
- 注释漂移：修正参与包/主程序误导性注释；REQ-CHANGE/§N 历史引用作 provenance 保留
- 残余/范围（不修，已登记）：G3 阿里云CDN/SMS 槽位（无功能消费）、G5 smoke=套件、G6 前端 deferred、G7 每日geo未聚合、S3 Redis宕fail-open（DESIGN既定取舍）、S4 弱默认密钥仅告警（部署强制）
- 复验：go build/vet/test ./... 全绿（14 包，真库）

## 残余项补强（2026-05-19，第二轮）

- S4 → CAPTAIN_ENV=prod 拒弱/缺密钥启动（dev 仅告警，行为不变）
- S3 → loginguard 进程内回退计数（Redis 宕仍限暴破）+ CAPTAIN_LOGIN_FAILCLOSED 可选 fail-closed
- G3 → 阿里云 CDN/SMS 配置槽位补齐（超管可加密设置；无 in-flow 消费，文档登记）
- G7 → 记录/导出位置改取 checkin_day 每日 geo（COALESCE 覆盖，v2 位置不再空）
- G5 → scripts/e2e.sh 真 cmd/server 全链 e2e，**实跑通过**；smoke.sh 串联
- 复验：go build/vet/test ./... 全绿（15 包，真库）；e2e 活服务绿
- 仅剩范围说明 G6（前端 deferred，需 check-in-kiosk）——非后端缺陷

## mobile v2 前端完成 + embed 集成（2026-05-19，闭合 G6）

配套前端仓库 `check-in-kiosk`（分支 `feat/v2-mobile`）的 **mobile v2 参与者端 F0–F12 全部完成**，G6（前端 deferred）就此闭合。

- **前端门禁全绿（实跑）**：typecheck 净；vite build 产 dist（~67.6 KB gzip，0 sourcemap）；vitest **42 files / 445 tests**；F11 e2e（vite-preview）**4/4**；F12-03 内嵌 e2e **4/4**；详见 check-in-kiosk `docs/F12-acceptance.md`。
- **F8/F9/F10/F11 shared-vs-backend 契约修复（前端侧对齐冻结后端，后端 Go 未改）**：`LandingResp.identity?`（对齐 F2-01 ea30120 微调）；`StepState` 无 `completed`/`data`，`days_done` 顶层（对齐 `runtime.go` StepGet）；`DrawResult` 扁平形（`resolved_by`/`prize_level`/`repeat`，对齐 Draw/DrawResult）；`LoginReq` 故意不发 `fingerprint`（字符串会硬挂 Go JSON decode）；`Warning` 无 `id`（对齐 repo WarningRow）。前端类型以注释钉死后端契约；对账见 check-in-kiosk `docs/F12-acceptance.md` §F12-07（11/11 端点路由+openapi+字段一致，无剩余 mismatch）。
- **embed 集成**：check-in-kiosk `scripts/embed-mobile.sh` 构建 mobile 并刷新本仓 `internal/webui/embed/mobile/`（先清旧 hash 再拷贝，幂等）。`internal/webui/webui.go` 的 `//go:embed embed` 在 `go build ./cmd/server` 时把该目录编进单二进制；`GET /m/{event_id}` 服务 SPA、`GET /m-static/` 服务 hashed assets。F12-03 用真实 captain 二进制（重建后含新 embed）跑通同 4 个 e2e spec，截图证明生产 `/m/` 路径端到端可用。
- **F12-04 复验**：embed 刷新 + `go build` 后 `scripts/e2e.sh` 仍 `E2E OK`（embed 不破坏 Go build/API 链）。Go 代码、`scripts/e2e.sh` 均未改。
- 本次 captain 侧仅提交 `internal/webui/embed/mobile/`（构建产物，git-tracked）+ 本 `docs/PROGRESS.md`；F2 微调 ea30120/73b4c0b 早已在分支内。
- **push / PR / merge：PENDING USER DECISION** —— captain `feat/v2-redesign` 与 check-in-kiosk `feat/v2-mobile` 均仅本地提交，未 push、未开 PR。
- **下一子项目**：big-screen 前端（后端 typed SSE / prize.won 已就绪），其后 admin 前端；模板引擎本期不做。

## admin v2 前端完成 + 双路由 webui 接线 + embed 集成（2026-05-20）

配套前端仓库 `check-in-kiosk`（分支 `feat/v2-admin`）的 **admin v2 后台（活动方 + 超管）A0–A7 全部完成**。本次 captain 改动在新分支 `feat/admin-dual-route`（由 `main` 切出，**未 push**），是 admin 前端设计 spec §1.1 约定的**唯一后端改动 —— 纯 webui 接线，无业务逻辑**。

- **唯一后端改动（webui 接线，非业务逻辑）**：`cmd/server/main.go` 在既有 webui 路由块新增 `GET /console` → `webui.ReactIndex("admin", 注入 window.__ADMIN_MODE__="org";__ADMIN_SEG__="")`（活动方模式）；并把既有混淆超管路由 `GET /{seg}` 的注入扩展为 `window.__ADMIN_MODE__="super";__ADMIN_SEG__="{seg}"`（保留原 `__ADMIN_SEG__`，新增 `__ADMIN_MODE__`）。复用既有 `webui.ReactIndex` splice 机制与 `/a-static/` `ReactStatic("admin")` StripPrefix——与 mobile `GET /m/{event_id}` 同一机制。无新增 handler、无鉴权/路由表业务改动。新增 `internal/webui/webui_test.go` 钉死双路由注入与 `/a-static/` 资源（`go test ./internal/webui/` 绿）。
- **复验无回归**：`go build ./...` + `go vet ./...` 全绿；`internal/webui` 等单测绿；`scripts/e2e.sh`（API-only，未改）**实跑 `E2E OK`**（embed + webui 改动不破坏 Go build/API 链）。
- **embed 集成**：check-in-kiosk `scripts/embed-admin.sh`（mobile-F12 同款，路径安全护栏 `*/internal/webui/embed/admin`、幂等、清 stale）构建 `@kiosk/admin` 并刷新本仓 `internal/webui/embed/admin/`。`//go:embed embed` 在 `go build ./cmd/server` 时把该目录编进单二进制。A7-05 用真实 captain 二进制（含本次 webui 改动 + 新 embed）从 captain 自身 `/console`（活动方）与混淆 seg（超管）跑通 13 个 admin e2e spec（非 vite preview），截图证明生产单二进制双路由内嵌路径端到端可用。
- **对账**：admin 前端全部 org/超管端点对 `cmd/server/main.go` 路由 + `docs/openapi.yaml` 核验，形对 check-in-kiosk `docs/A6-reconcile.md`；唯一残差 = `/org/flows`（GET+POST）已路由+被前端使用但未登记进 `docs/openapi.yaml`（captain 文档缺口，前端已对冻结 Go 源对齐，非缺陷）。明细见 check-in-kiosk `docs/A7-acceptance.md`。
- 本次 captain 侧仅提交 `cmd/server/main.go` + `internal/webui/`（含 `webui_test.go` + `embed/admin/` 构建产物）+ 本 `docs/PROGRESS.md`；预存无关漂移 `D .claude/scheduled_tasks.lock` **未触碰、未 stage**。
- **push / PR / merge：PENDING USER DECISION** —— captain `feat/admin-dual-route` 与 check-in-kiosk `feat/v2-admin` 均仅本地提交，未 push、未开 PR。
- **下一子项目**：big-screen 前端（captain 仍服务旧 `screen.html`，后端 typed SSE / prize.won 已就绪）。
