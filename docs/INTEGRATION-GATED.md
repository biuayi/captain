# 门控集成节点（需真凭据，PF-04）

> 绝大多数功能与全部单测/集成测试在**无真凭据**下可跑（默认关闭/桩/本地FS）。
> 以下三处需真凭据做**联调验证**，凭据只进 gitignored `deploy/.env`，**不进代码/日志/对话**。

| 节点 | 触发功能 | env（写 deploy/.env） | 验证方式 |
|---|---|---|---|
| Cloudflare Turnstile | 参与者登录 / 签到人机校验（SS-2/SS-4）；超管/活动方登录 | `CAPTAIN_TURNSTILE_MODE=enforce`、`CAPTAIN_TURNSTILE_SITEKEY=`、`CAPTAIN_TURNSTILE_SECRET=`（或经超管 `PUT /{adminSlug}/config/cloudflare_turnstile_*` 加密入库，优先生效） | 登录带真 token 通过；缺/错 token 403 captcha_failed |
| 阿里云 OSS | 模板资源(SS-1)/R2上传(SS-4)/导出下载(SS-7) 私有读签名 | `CAPTAIN_STORAGE_DRIVER=aliyun`、`CAPTAIN_OSS_ENDPOINT/BUCKET/KEY_ID/KEY_SECRET`（或超管 config 入库） | 上传→对象在 OSS；下载走 SignedURL 短时有效 |
| 阿里云 CDN / 短信 | 超管可设置 token（G3，原始需求"阿里云 access token=OSS/CDN/短信"）。配置槽位已就绪（`aliyun_cdn_domain`、`aliyun_sms_*`，加密入库）。**当前 R1-R4 流程无功能消费 CDN/SMS**——槽位为后续 CDN 分发/短信通知预留，不影响本期验收 | 超管 `PUT /{adminSlug}/config/aliyun_cdn_domain` 等 | GET /config 显示 set=true+掩码；接入 CDN/SMS 时读取 |
| pg_dump | 超管数据库导出（SS0-15/16） | 运行环境 PATH 含 `pg_dump`（部署镜像装 postgresql-client） | `POST /{adminSlug}/db-export` → job done → 可下载 .sql |

**安全交接**：到联调阶段由 Hertz 自行写入 `deploy/.env`，或提供最小权限/可吊销的临时 key；平台动态密钥经超管后台 `PUT /config/{key}` AES-256-GCM 加密落库，API 永不回显明文（只 set+尾4位掩码）。

## 性能/压测门控（需活服务，非 CI 离线项，G4）

并发**正确性**已自动覆盖：`go test -race ./internal/repo/ ./internal/realtime/ ./internal/orgperm/` 全绿（抽奖原子不超卖、whitelist 单 claim、checkin_day 幂等、advisory 锁、计数对账——无数据竞争）。

**2万级吞吐回归**需对运行中的 `cmd/server` 跑（非离线 CI）：

```
docker compose -f deploy/docker-compose.yml up -d   # 起全栈含 captain（seed 出 demo 活动/event_id）
go run ./cmd/screenstress  -base http://localhost:8080 -event <event_id> -n 20000   # 大屏 SSE/计数
go run ./cmd/lotterystress -dsn <pg_dsn> -event <event_id> -step r4 -n 20000        # 抽奖不超卖
```

验收：计数/SSE 不退化、`lotterystress` win≤库存且无 err。属上线前手动门控（依赖部署环境资源），不计入 `go test ./...`。
