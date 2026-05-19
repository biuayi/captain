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
