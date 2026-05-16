# CODE-REVIEW-002 — 两仓库审查（2026-05-17）

> 注：codex 后台 review 任务长时间未产出（疑卡死，同首次大屏重设计现象）。
> 为不阻塞，由 Claude 做务实 review；codex 若后续产出可另附补充。

## HIGH（已修）

- **H1 cookie Secure 经隧道失效** — `internal/participation/handler.go` device-session
  cookie 仅 `r.TLS!=nil` 才置 Secure；公网经 cloudflared/反代时 TLS 在边缘终止，
  captain 看到 http → Secure 永不置位，session cookie 可能明文链路传输。
  **已修**：改为 `r.TLS!=nil || X-Forwarded-Proto==https`。
- **H2 导出 CSV 公式注入** — 用户可控登记字段写入 CSV，Excel/Sheets 打开时
  `=`/`+`/`-`/`@` 前缀会被当公式执行。**已修**（上一提交）：`csvSafe()` 前缀加 `'`。

## MED

- **M1 ListParticipants LATERAL 无专用索引**（`internal/repo/repo.go`）— 每行子查询
  按 `participation_id+step_type` 过滤、`occurred_at desc` 排序，原唯一键不覆盖。
  **已修**：迁移 0005 增 `idx_psr_partn_type_time`。
- **M2 导出全量进内存** — `internal/export/export.go` 把全部参与者与整份 CSV
  buffer 进 `bytes.Buffer`。2万级可接受；百万级需流式直写存储。**记录待办**
  （v1.x：分页游标 + 流式 io.Pipe 到 storage），非当前规模问题。
- **M3 限流/锁定 Redis 不可用时 fail-open** — `loginguard`/`httpx.RateLimiter`
  Redis 故障时放行（避免误锁全站）。属有意取舍；生产应监控 Redis 健康并告警。
- **M4 /qr 与参与匿名端点无鉴权**（设计如此）— event_token 本就印在二维码=公开；
  匿名参与是产品要求。已有分级限流 + device-session + 幂等兜底。可接受，
  建议生产对 /qr 增 per-IP 限流（当前无）。**记录待办**。

## LOW

- L1 `_ =` 吞错集中在 RecordStep/publish 等副作用路径，主链路（计数/幂等）错误已处理；可接受。
- L2 localFS `path()` 已 `filepath.Clean`，key 均内部 uuid，无遍历面。
- L3 CORS 未设置：前端由 captain 同源托管，生产拓扑无需 CORS；dev 用 vite 代理。无需改。
- L4 screen.html 用 textContent（codex 版）渲染数字，无 XSS；React 端用户数据走 JSX 文本节点已转义。

## 结论

无遗留 HIGH（H1/H2 已修）。M1 已修；M2/M4 记为 v1.x 待办（非当前 2万 规模问题）。
整体安全/正确性在当前规模达标。`go build/vet/test` 全绿。
