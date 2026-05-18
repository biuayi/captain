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
| SS-5 抽奖多奖池（14） | 进行中 |
| SS-6 大屏（7） | TODO |
| SS-7 记录与导出（11） | TODO |
| PF 集成/E2E/收尾（6） | TODO |

## 续做说明

- 验收基线每阶段尾跑 `go build ./... && go vet ./... && go test ./...`（含 docker pg/redis/nats）。
- DB 测试用 `internal/testdb`，无基础设施时 `t.Skip` 保持离线绿。
- 决策记 DESIGN §6；无法决策项与 codex 读原始需求后定（暂无）。
