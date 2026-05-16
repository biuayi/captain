# FINDING-001 — 大屏无显示根因：Cloudflare 隧道缓冲 SSE（后端无错）

> 来源：用户报「大屏无法显示签到进度」(公网 trycloudflare URL)。看护方 Claude 排查。
> 新增独立文件（不并发改正式文档）。**ACTION REQUIRED：实现方按 §3 打小补丁并随
> 下次重建镜像发布**（实现方掌控 build/deploy 管线，故由实现方落地，看护方不擅自重启容器）。

## 1. 结论（差分证据，高置信）
- **本地 SSE 正常**：`curl -Ns http://localhost:8080/api/v1/p/e/{id}/stream` 立即返回
  `data: {"event_id":...,"count":20008,...}`，headers 含 `text/event-stream`、`X-Accel-Buffering: no`、chunked。
  → 后端 `pa.Stream` / `httpx.ServeSSE` / `statusWriter` / `Recover` 全部正确，连接即发当前快照。
- **公网 SSE（经 Cloudflare quick tunnel）异常**：headers 正常返回（`server: cloudflare`、
  `content-type: text/event-stream`），但 **12s 内 body 0 字节**，无任何 `data:` 帧。
- 本地与公网唯一差异 = Cloudflare 隧道 → **根因：Cloudflare 缓冲了 SSE 响应体**。
  源端首帧仅 ~92B 后静默至 25s 心跳，Cloudflare 缓冲阈值/idle 未达到 → 不下发首帧 →
  浏览器 EventSource 收到 headers 但收不到 data → 大屏计数停在 0/空。
- 旁注：该 demo event `count=20008`（被之前 30k 压测污染）。展示前建议用干净 event 或重置计数
  （`redis-cli SET count:{eventId}:checkin 0` + 清 participation），与本 bug 无关但影响观感。

## 2. 排除项（已验证不是这些）
- 不是鉴权/Turnstile：Turnstile 仅 gate `submit`，stream/count 无鉴权。
- 不是 AccessLog/Recover 包装：`statusWriter` 正确透传 Write + 委派 Flush；Recover 不包装 w。
- 不是 SSE handler bug：本地即时收到快照。
- 不是前端页面 bug：`/screen/{id}` 内嵌页 `EventSource(.../stream)` + `onmessage`→`.count`
  字段名匹配；`/info` 取名称/预计人数正常。隧道修好后页面即可渲染。

## 3. 修复（小补丁，实现方落地；对所有反代/CDN 都更稳健，非 hack）
`internal/httpx/sse.go` `ServeSSE`：写完 200 headers 首次 `flusher.Flush()` 后，
(a) 立即发一段 ~2KB 注释填充穿透代理缓冲；(b) 心跳 ticker 由 25s 改 1s。

```go
// 写 headers + flush 之后，进入 select 循环之前：
fmt.Fprintf(w, ": %s\n\n", strings.Repeat(" ", 2048)) // 穿透 Cloudflare/反代缓冲
flusher.Flush()
// ...
beat := time.NewTicker(1 * time.Second) // was 25 * time.Second
```
需 `import "strings"`。心跳注释 `: ping\n\n` 已有，1s 频率可让 Cloudflare 持续 flush。
（可选增强，非必须）大屏页对 `/api/v1/p/e/{id}/count` 每 ~3s 轮询兜底，SSE 不通时仍出数。

## 4. 用户即时绕过（无需改码，现在就能演示）
后端完全正常。**在本机/局域网直接访问 `http://localhost:8080/screen/{eventId}`
（或本机 LAN IP:8080）** —— 不经 Cloudflare，SSE 正常，大屏即时出数。
公网 URL 待 §3 补丁随实现方下次重建镜像后即恢复。
