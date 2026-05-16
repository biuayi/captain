# REQ-CHANGE-004 — 登录硬化 + Cloudflare 人机认证

> 来源：用户 2026-05-17 追加。新增文件（不并发改 REQUIREMENTS/PROGRESS）。

## 1. 服务端登录硬化（无外部依赖，立即实现+验证）

- **恒定延迟**：organizer/admin 登录，无论密码对错（含账号锁定/Turnstile 失败），**统一等待 3s 才返回结果**（抗时序侧信道 + 拖慢爆破）。
- **失败锁定**：同一账号连续 **10 次失败 → 锁定 10 分钟**，锁定期内任何登录直接拒绝（仍走 3s 延迟）。成功登录清零失败计数。Redis 实现：`lg:fail:{role}:{login}`（计数，滑动 TTL）、`lg:lock:{role}:{login}`（锁，TTL 10m）。
- 作用域：`organizer` 与 `admin` 两个登录域分别独立计数。

## 2. Cloudflare Turnstile 人机认证（配置化）

- **前端**：用户签到（mobile `checkin` step 提交前）与**管理后台登录**（活动方/超管）加入 Cloudflare Turnstile 挂件，提交时带 `turnstile_token`。
- **服务端**：对上述请求调用 `https://challenges.cloudflare.com/turnstile/v0/siteverify`（secret+token+remoteip）校验；`enforce` 模式校验失败即拒绝（走 3s 延迟）。
- **配置**（env）：`CAPTAIN_TURNSTILE_MODE=off|enforce`（默认 `off`）、`CAPTAIN_TURNSTILE_SITEKEY`、`CAPTAIN_TURNSTILE_SECRET`。前端 sitekey 由 `/api/v1/p/config` 暴露（公开，仅 sitekey 与 mode）。
- **现实约束（必须知会）**：Turnstile 是 Cloudflare 远程服务，挂件需加载 `challenges.cloudflare.com/turnstile/v0/api.js`，服务端 siteverify 需出网到 Cloudflare。当前 WSL 出网受 fake-ip/代理限制（与 docker.io 同源问题），容器内 siteverify 需经宿主代理。故**默认 `off`**（集成代码完备但不阻断现有 demo/压测/隧道）；上线由你提供 Turnstile 站点密钥 + 保证出网后置 `enforce`。这是部署/合规前置项，非代码缺陷。
- `off` 模式：前端不渲染挂件、后端跳过校验，行为与现状一致（向后兼容，保住已验证链路）。

## 3. 动作 / 任务
- T-090 服务端登录 3s 延迟 + 10/10min 锁定（Redis）—— 立即实现验证。
- T-091 Turnstile 配置 + 服务端 siteverify + `/api/v1/p/config` 暴露 sitekey/mode。
- T-092 前端：admin 登录 + mobile checkin 挂载 Turnstile（mode!=off 时），提交带 token。
- 验收：mode=off 时全链路与现状一致；3s 延迟与锁定可 curl 复现；enforce + 真密钥 + 出网时人机校验生效。
