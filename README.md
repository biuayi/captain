# captain

活动互动平台**后端**（Go，模块化单体）。前端仓库：`check-in-kiosk`。
需求与架构见 [`docs/`](docs/)：REQUIREMENTS / ARCHITECTURE / GIT_CONVENTION / CODING_STANDARDS / PROGRESS。

> ⚠️ 强制规则：每阶段性完成任务必须 `git commit` + `git push`（`docs/GIT_CONVENTION.md §0`）。

## 快速开始（本地，无需服务器）

```bash
make up                 # 拉起 postgres + redis + nats(JetStream) + captain
make logs | grep seed   # 看《寻道大千》周年庆 demo 的入场链接与大屏地址
make smoke              # 端到端冒烟：扫码→签到→计数→导出→下载
make down               # 停
```

启动后：

- 用户侧 H5（手机扫码）：`http://localhost:8080/m/{event_id}?et=...`（链接见 `make logs | grep seed`）
- 现场大屏（实时人数）：`http://localhost:8080/screen/{event_id}`
- 健康检查：`http://localhost:8080/healthz`

demo 账号：超管 `admin/admin123`，活动方 `xundao/xundao123`。

## 垂直切片（已实现）

`扫码 → device-session → 签到(幂等) → Redis计数+SSE推大屏 → 后台查看参与/导出 → CSV下载`，
含活动方/超管登录、6 类流程 step（checkin/form/game/charity/reward/result）、
NATS JetStream 异步导出、10s PG 对账。详见 `docs/ARCHITECTURE.md`。

## 接口域

| 前缀 | 调用方 | 鉴权 |
|---|---|---|
| `/api/v1/p`     | 普通用户 | event_token + device-session cookie |
| `/api/v1/org`   | 活动方   | Bearer（organizer） |
| `/api/v1/admin` | 超管     | Bearer（admin，独立域） |

## 本地开发（不走容器）

```bash
make tidy && make build && make vet && make test
# 需本机有 postgres/redis/nats，或仅跑 make up 起依赖后本地 go run ./cmd/server
```
