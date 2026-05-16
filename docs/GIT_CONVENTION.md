# Git 提交与协作规范

> 适用于 check-in-kiosk 与 captain 两仓库。多 Agent / 跨设备协作下保持历史清晰可追溯。

## 0. 强制项目规则（MANDATORY，所有 Agent 与人类必须遵守）

> 多个 AI Agent 跨设备并行协作。未提交的工作 = 会丢失/会冲突的工作。

1. **每阶段性完成任务，必须 `git commit` + `git push`**。"阶段性完成"= 任一可独立描述、且不破坏构建的进展（一个看板任务 T-0XX 完成、或其中一个可编译的子里程碑）。不得长时间在本地堆积未推送的改动。
2. 提交前满足 `docs/CODING_STANDARDS.md` §3 自检清单（后端 `go build ./...` 必须通过；纯文档/配置改动豁免构建项）。
3. 同一提交内同步更新 `docs/PROGRESS.md`（canonical=check-in-kiosk，captain 为镜像）任务状态与 §5 变更日志。
4. 推送前先 `git pull --rebase`（多 Agent 并行，避免分叉）。冲突就地解决，不强推 `--force`（除非协调一致）。
5. 远程为 SSH：`git@github.com:biuayi/<repo>.git`（已存在）。分支 `main`。
6. 违反本规则导致他人工作丢失/冲突，视为高优先级事故，在 PROGRESS §4 记录。

## 1. 提交信息格式（Conventional Commits 变体）

```
<type>(<scope>): <subject>

<body 可选：为什么这么改>

<footer 可选：Refs T-021 / BREAKING CHANGE: ...>
```

- **type**：`feat` 新功能 · `fix` 修复 · `docs` 文档 · `refactor` 重构 · `test` 测试 · `chore` 构建/杂项 · `perf` 性能 · `build` 构建系统/依赖 · `ci` CI。
- **scope**（建议带上，对应模块/任务）：
  - captain：`auth` `event` `flow` `participation` `realtime` `export` `organizer` `admin` `store` `storage` `deploy` `config`
  - check-in-kiosk：`big-screen` `mobile` `admin` `shared` `build`
  - 通用：`docs` `progress`
- **subject**：祈使句、≤60 字符、结尾不加句号；中文英文均可，保持一项目一致。
- **footer**：关联看板任务用 `Refs T-0XX`；完成用 `Closes T-0XX`。

示例：
```
feat(participation): add device-session mint and checkin idempotency

Refs T-022
```
```
docs(progress): mark T-003 codex brainstorm done; lock M0 decisions

Closes T-003
```

## 2. AI 协作署名

AI 生成/参与的提交在 footer 追加：
```
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
```
codex 参与的实现/审核在 body 注明 `Reviewed-by: codex` 或 `Co-designed-with: codex`。

## 3. 分支

- `main`：可运行基线。本期单人/多 Agent 通宵构建允许直接提交到 `main`（小步快提）。
- 后续多人协作切 `feat/T-0XX-<slug>`，PR 合并。

## 4. 提交粒度

- 小步提交，一个提交只做一件可描述的事。
- 每完成一个看板任务（T-0XX）至少一个提交，并在同提交更新两仓库的 `docs/PROGRESS.md` 镜像。
- 不提交：构建产物、`vendor/`（除非锁定）、密钥、`.env`（提交 `.env.example`）。

## 5. 远程

- 两仓库 GitHub **private**（专有业务）。
- 远程名 `origin`，默认分支 `main`。
- 推送前确保本地可编译（captain：`go build ./...`）。
