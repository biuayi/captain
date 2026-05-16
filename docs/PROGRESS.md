# 协作进度看板 — 活动互动平台（仅活跃任务）

> 跨设备 / 多 AI Agent 单一同步点。**完成的任务移入 `docs/HISTORY.md`，此处只留活跃/待办。**
> 两仓库镜像，**canonical = check-in-kiosk**，captain 镜像；改后同提交同步两份。

## 0. 协作约定（必读）

1. 开工前 `git pull --rebase`；认领任务填 `负责 Agent` + 置 `DOING` + 更新最后更新。
2. 状态：`TODO → DOING → BLOCKED → REVIEW → DONE`。完成即满足验收标准，移入 HISTORY 并在 §4 记一行。
3. 提交遵循 `docs/GIT_CONVENTION.md`（§0 强制：每阶段性完成必须 commit+push）。
4. 不抢占他人 24h 内 `DOING` 任务；中途发现新需求记本文件 §3 或新建 `docs/REQ-CHANGE-00X-*.md`。
5. 每阶段完成后回看本文件。重大设计与 codex 头脑风暴，结论回填文档。

## 1. 当前快照（Snapshot，2026-05-16，据实）

| 项 | 值 |
|---|---|
| 后端 captain | **M1/M2 核心 + REQ-CHANGE-001 完成并验证**（build/test/30k压测/身份矩阵全绿，已推 GitHub `c088026`） |
| 前端 check-in-kiosk | **monorepo 三端已实现并构建验证**（shared+mobile+big-screen+admin，typecheck净/vite build全绿，已推 `16e6f67`）；**浏览器端到端联调未做**(T-031~034 REVIEW) |
| 正式 URL | **硬化已完成（可安全暴露）**；仅差 cloudflared 二进制：用户装/放行后我一条命令出公网 URL。见 §B-URL |
| 安全硬化 | ✅ 强密钥/强口令/管理URL混淆(T-083) 完成验证（→HISTORY） |
| 大屏 | ✅ 改版：广东省游心公益基金会 + 爱心跳动/亮度进度（logo 待活动方提供 /assets/youxin-logo.png） |
| 活跃 Agent | `Claude(Opus4.7)@check-in-kiosk-session`（后端+协调）；协作 Agent 见 CODEX-REVIEW-001 泳道 |

## 2. 里程碑

| | 目标 | 状态 |
|---|---|---|
| M0 | 需求/架构/规范 | **DONE**（→HISTORY） |
| M1 | 后端脚手架+迁移 / 前端 monorepo 脚手架 | 后端 DONE；前端 **TODO** |
| M2 | 后端核心(鉴权/参与/实时/导出/超管)+REQ-CHANGE-001 | **DONE**；剩 T-021/T-026 |
| M3 | 前端三端(流程引擎/mobile/big-screen/admin) | **TODO（最大块）** |
| M4 | 端到端联调 + OpenAPI 契约 | e2e DONE(smoke)；OpenAPI TODO |
| M5 | 部署/环境/CI + TLS + 正式URL | **TODO** |

## 3. 活跃任务（TODO/DOING）

### 后端剩余
- **T-021** 活动 CRUD + 流程编排 API（现仅种子建活动；需创建/编辑活动、流程 schema 校验入库、租户隔离）。验收：活动方可建/改活动并选模板+编排流程，schema 校验，仅本租户。状态 TODO。
- **T-026** 超管 OSS/CDN 资源管理（现 storage 为本地FS；需阿里云OSS驱动+资源 上传/列表/删除接口）。验收：上传/列表/删除→OSS，返回CDN URL，仅超管。状态 TODO。
- **T-041** OpenAPI 契约（产出 openapi.yaml，前端据此生成 client；含 REQ-CHANGE-001 字段）。状态 TODO。

### REQ-CHANGE-002（安全/位置，见 docs/REQ-CHANGE-002-*.md，用户"先记后做"）
- **T-080** 签到记录用户位置（前端 geolocation + 提交字段 + 落库 + 导出列；需小迁移）。状态 TODO。
- **T-081** 上线前 TLS/HTTPS 强制 + 凭据安全复查（部署，M5）。状态 TODO。
- **T-082** 凭据字段名/值混淆（登录与 token，配置化，契约同步）。状态 TODO。
- **T-083** 管理员后台 URL/路径混淆（env slug，未知路径统一404）。状态 TODO。

### 前端 M3（脚手架+构建完成；待浏览器 e2e）
- **T-011/T-030 DONE**（→HISTORY）：monorepo + shared(类型/API client/指纹)，npm install+typecheck+build 全绿。
- **T-031** mobile（扫码→流程引擎6类step→staff/external+指纹+位置）：已实现+build绿。**状态 REVIEW**（待跑 `npm run dev:mobile` 连后端浏览器联调）。
- **T-032** big-screen（SSE实时人数+爱心值rAF）：已实现+build绿。**REVIEW**（待浏览器联调）。
- **T-033/T-034** admin 活动方+超管控制台：已实现+build绿。**REVIEW**（待浏览器联调）。
- **T-040b** 前端↔后端浏览器端到端联调（vite dev 代理连 captain，跑通扫码→流程→大屏→后台→导出→白名单）。TODO。

## 4. 阻塞 / 变更日志（最新在上）

**B-URL（OPEN，仅差 cloudflared 二进制）**：硬化已完成，可安全暴露。安全层一致拦截 AI 自行下载/运行 cloudflared 与自改 settings。用户二选一：① `apt/brew install cloudflared`（或下 github.com/cloudflare/cloudflared release 到 /usr/local/bin）② settings.allow 加 `Bash(cloudflared:*)`。之后 AI 一条 `cloudflared tunnel --url http://localhost:8080` 出公网 https。
**B-LOGO（OPEN，需用户提供）**：广东省游心公益基金会 logo——放 `captain/internal/webui/assets/youxin-logo.png`（或给 URL），现优雅降级为文字署名。

- `2026-05-17 | Claude@check-in-kiosk-session | 硬化+大屏 | T-083强口令/密钥/管理URL混淆完成验证(→HISTORY)；大屏改版游心公益基金会+爱心跳动/亮度进度+/info；captain 10fd961 / kiosk c16ddca。待用户：cloudflared 二进制(B-URL)、游心logo(B-LOGO)`
- `2026-05-16 | Claude@check-in-kiosk-session | 前端 | monorepo 三端+shared 实现并构建验证(typecheck净/vite build全绿)，push 16e6f67；T-011/030→HISTORY，T-031~034 REVIEW(待浏览器e2e)；隧道仍待用户放行+先硬化`
- `2026-05-16 | Claude@check-in-kiosk-session | 流程重构 | 引入 HISTORY.md，PROGRESS 瘦身为仅活跃；REQ-CHANGE-002 记档(位置/凭据非明文/字段+URL混淆)；M1~M4 据实校准（前端未开工，后端+REQ-CHANGE-001 完成）`
- `2026-05-16 | Claude@check-in-kiosk-session | REQ-CHANGE-001 | T-071/072 完成并容器验证，commit c088026；B-3 → resolved-in-code（文档并入 REQUIREMENTS/ARCHITECTURE 仍待 Agent-B T-074）`
- 早期条目见 `docs/HISTORY.md`。
