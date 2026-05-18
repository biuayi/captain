# 进度看板 — captain 服务端 v2 重做

> 单一权威设计 = [`docs/DESIGN.md`](DESIGN.md)（2026-05-19，取代全部旧文档）。
> 工作方式：演进式重构；DESIGN.md → 200+ 实现级任务（writing-plans）→ 逐任务实现+自测+commit。
> 旧需求/架构/REQ-CHANGE/HISTORY/REVIEW/openapi 已移出工作区（git 历史留底）。

## 状态

| 阶段 | 状态 |
|---|---|
| 全量精读现有代码 | DONE |
| 权威设计 docs/DESIGN.md | DONE（待用户过目） |
| 清理遗留文档 | DONE |
| 拆 200+ 实现任务（writing-plans） | TODO |
| 自主实现 + 自测 + 逐任务 commit | TODO |

## 子系统依赖链（实现排程）

`SS-0 基座 → SS-1 模板 / SS-2 身份 → SS-3 编排 → SS-4 运行时R1-R3 → SS-5 抽奖R4/AB → SS-6 大屏 → SS-7 记录导出`

任务清单由 writing-plans 生成后在此登记（编号、状态、验收点、对应 commit）。
