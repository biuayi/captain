# AGENTS.md — captain（后端）

> 给任何参与本仓库的 AI Agent（Claude / codex / 其他）与人类的导航与协作约定。
> 配套前端仓库：`check-in-kiosk`（React monorepo）。

## 这是什么

活动互动平台**后端**：Go，**模块化单体**，仓库名 `captain`。
三接口域：`participation`（普通用户扫码参与）/ `organizer`（活动方后台）/ `admin`（超管）。
存储：PostgreSQL + Redis + NATS JetStream + 对象存储（本地 FS / 阿里云 OSS）。

**当前阶段**：本期重点。目标是垂直切片端到端可跑：
`扫码 → checkin → Redis 计数 +1 → SSE 推大屏 → 后台可见 → 导出 CSV`。

## ⚠️ 强制规则（MANDATORY）

**每阶段性完成任务，必须 `git commit` + `git push`。** 多 AI Agent 跨设备并行，未推送=会丢失/冲突。详见 `docs/GIT_CONVENTION.md §0`。推送前 `git pull --rebase`；`go build ./...` 必须通过才提交代码。

## 必读文档（开工前）

- `docs/REQUIREMENTS.md` — 需求基线（§9 头脑风暴定稿、§10 产品决策、§11 寻道大千首版）
- `docs/ARCHITECTURE.md` — **架构权威**：接口域、鉴权/防刷、流程引擎 schema、实时计数、NATS subjects、数据模型、目录结构
- `docs/PROGRESS.md` — 协作看板（认领任务、更新状态）
- `docs/GIT_CONVENTION.md` — 提交规范 + **§0 强制规则**
- `docs/CODING_STANDARDS.md` — 代码编程规范（强制）+ §3 提交前自检清单

## 构建与运行

```
make up        # docker compose 拉起 postgres/redis/nats/captain
make smoke     # 跑 scripts/smoke.sh 端到端冒烟
go build ./... # 推送前必须通过
go test ./...  # 单测
```

启动自动跑迁移 + 种子（demo organizer/admin/event + checkin 流程）。

## 协作协议（多 Agent / 跨设备）

1. 改动前 `git pull`；在 `docs/PROGRESS.md` 认领任务（负责 Agent、置 DOING）。
2. `PROGRESS.md` canonical 在 check-in-kiosk，本仓库为镜像，同一提交同步两份。
3. 提交遵循 `docs/GIT_CONVENTION.md`，关联 `Refs T-0XX`，AI 提交署名。
4. 完成才置 DONE，并在 PROGRESS.md §5 记一行。
5. 卡壳/重大分歧 → 与 codex 头脑风暴，结论回填 REQUIREMENTS/ARCHITECTURE。
6. 实现完成后交 codex review/审核。

## 工程约定

- HTTP 路由 chi；PG 用 pgx；迁移用 goose（嵌入）；Redis go-redis v9；NATS nats.go(JetStream)。
- 对象存储走 `internal/storage` 接口，本地 FS 实现可用；阿里云 OSS 实现可后补，不阻塞。
- 统一错误响应 `{code,message,request_id}`；统一 `/api/v1` 前缀。
- 密钥/JWT secret 走 env；`.env.example` 提交，`.env` 不提交。
- 遵循用户全局 CLAUDE.md：简单优先、外科手术式改动、不做需求外的事。

## 不要做

- 不把流程引擎做成通用低代码（v1 线性四步，见 ARCHITECTURE §4）。
- 不让 organizer 与 admin 共用鉴权域（物理分表分域）。
- 不在 Redis 上堆持久队列语义（持久异步用 NATS JetStream）。
- 不提交密钥/构建产物/.env。


<claude-mem-context>
# Memory Context

# [captain] recent context, 2026-05-17 12:09am GMT+8

No previous sessions found.
</claude-mem-context>