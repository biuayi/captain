# 代码编程规范（强制）

> 适用 check-in-kiosk（前端）与 captain（后端）两仓库。多 Agent / 跨设备协作下统一风格、降低冲突。
> 与用户全局 CLAUDE.md 一致：**简单优先、外科手术式改动、不做需求外的事、改前先想**。

## 0. 通则（所有语言）

- **最小实现**：解决问题的最小代码，无投机性抽象、无未要求的"灵活性"、不为不可能的场景写错误处理。
- **外科手术式改动**：只动必须动的；不顺手"优化"无关代码/注释/格式；风格服从既有代码。
- **命名**：见名知意，不缩写到模糊；包/模块名小写无下划线（Go）/ kebab 或 camel（TS）。
- **注释**：解释"为什么"，不解释"是什么"；关键决策注明对应 `docs/ARCHITECTURE.md` 章节。
- **错误处理**：错误必须处理或显式忽略（`_ =`）并说明；对外统一错误响应 `{code,message,request_id}`。
- **密钥/配置**：一律走环境变量；禁止硬编码密钥；`.env` 不提交，提交 `.env.example`。
- **不提交**：构建产物、`/data/`、密钥、`vendor/`（除非锁定）、IDE 配置。
- **每个改动行可追溯到一个需求/任务**（看板 T-0XX）。

## 1. 后端 Go（captain）

- 版本与工具：`go 1.23+`；提交前必须 `gofmt`/`go vet` 干净、`go build ./...` 通过、`go test ./...` 通过。
- 目录：按 `internal/<模块>` 分层（见 ARCHITECTURE §8）；`internal/` 禁止被外部 import。
- 依赖：克制。新增第三方依赖需在提交说明里给理由；优先标准库。
- SQL 只在 `internal/repo`；每个活动方维度查询**显式传 `organizer_id`**（多租户防御）。
- 并发：`context.Context` 贯穿；不泄漏 goroutine；channel 有明确关闭方。
- 错误：`fmt.Errorf("...: %w", err)` 包装传递；handler 层翻译为统一错误响应；不 `panic`（已有 Recover 中间件兜底）。
- 日志：`log` 标准库，单行结构化（`模块: 事件 key=val`）；不打印密钥/PII 明文。
- 命名：导出标识符有 doc 注释（以标识符名开头）；包注释一句话讲职责。
- 测试：核心逻辑（流程引擎校验、token、计数幂等）必须有单测。

## 2. 前端 React + Vite（check-in-kiosk）

- TypeScript 严格模式；ESLint + Prettier 统一（配置后置于仓库根，禁止个体覆盖）。
- monorepo：pnpm workspace；`shared` 放复用组件/类型/API client/流程渲染引擎，三 app 不复制粘贴。
- 组件：函数组件 + hooks；单组件单职责；props 显式类型，禁止 `any`（确需用 `unknown` + 收窄）。
- 流程引擎：前端按 `step.type` dispatch 到 renderer，契约严格对齐 ARCHITECTURE §4；新增 step 类型先改文档再改码。
- API：client 由后端 OpenAPI 生成，禁止手写漂移；网络错误统一处理。
- 样式：主题 token 集中管理（寻道大千：水墨国风配色见 ARCHITECTURE §4），禁止散落魔法色值。
- 状态：能局部就不全局；副作用收敛在 hooks。

## 3. 提交前自检清单（Definition of Done）

- [ ] 改动可追溯到看板任务 T-0XX，且满足该任务验收标准
- [ ] 后端：`go build ./...` + `go vet ./...` + `go test ./...` 通过；前端：`pnpm build` + `lint` 通过
- [ ] 无新增硬编码密钥；`.env.example` 已同步
- [ ] 文档随码更新（REQUIREMENTS/ARCHITECTURE/PROGRESS 受影响处）
- [ ] 遵循提交规范 `docs/GIT_CONVENTION.md` 并 **commit + push**（见强制规则）
- [ ] 在 `docs/PROGRESS.md` 更新任务状态与 §5 变更日志（两仓库镜像同步）

## 4. 评审

- 实现完成交 codex review/审核；卡壳或重大设计分歧与 codex 头脑风暴，结论回填 REQUIREMENTS/ARCHITECTURE。
