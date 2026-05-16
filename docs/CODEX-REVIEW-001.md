# CODEX-REVIEW-001 — 后端切片审核 + REQ-CHANGE-001 协调（2026-05-16）

codex 对 `Claude@check-in-kiosk-session` 实现的后端垂直切片做了 review，并就 REQ-CHANGE-001（指纹+白名单）给出协调/分期与多 Agent 防撞方案。本文件为权威记录，供协作 Agent 对齐（codex 指派 Agent-B 在 T-074 assimilate 进 ARCHITECTURE/REQUIREMENTS）。

## A. 切片代码审核结论与处置

| 严重度 | 问题 | 处置 |
|---|---|---|
| HIGH | `realtime` Redis pub/sub 通道关闭时 `msg=nil` 解引用 panic | **已修**：`msg, ok := <-msgs` 守卫 + 关闭即返回 |
| HIGH | session cookie 缺 `Secure`；event/session/auth 共用一个 HMAC 默认弱密钥 | **已修**：HTTPS 时置 `Secure`；默认密钥启动告警；密钥分用途/分轮换列入 v1.x |
| MEDIUM | submit 无 IP 限流；mint 以原始 `d=` 为键可轮换绕过 | **已修**：submit 加 IP 桶；mint 改用 `deviceHash` 为键 |
| MEDIUM | `INCR` 成功但 `Publish` 失败 → 大屏停到 10s 对账 | **已修**：`OnCheckin`/`reconcile` 本地 `apply` 兜底，publish 失败本机仍即时推 |
| LOW | SSE hub 关闭竞态 | codex 复核为**伪命题**（cancel/flush 均经 `m.mu` 串行）；新增事件类型时注意 |
| LOW | 多租户校验 | 现有事件路由均校验 `ev.OrganizerID != orgID`；新白名单接口须沿用 |

修复后 `go build/vet/gofmt/test` 全绿。

## B. REQ-CHANGE-001 协调 / 分期（codex 定稿）

- **保命原则**：改动 **additive + mixed-mode**；种子 demo 活动保持 **external-open**，`扫码→签到→计数→SSE→导出` 原路径不变（明早 demo 优先）。
- **v1 MUST**：新增 `event_whitelist_entry` 表；`participant` 加 `fingerprint_hash/participant_type/whitelist_entry_id`；指纹服务端在 submit 时重算；staff = 命中白名单 + `phone_last4` 挑战 + 一次性设备绑定；新增错误码；organizer 白名单导入 + 基础查询。
- **v1 MUST NOT**：不要把 QR 落地换成"仅指纹 bootstrap"——指纹只在 submit 期做身份解析。
- **v1.x DEFER**：碰撞审计、跨 event 名单复用、员工自助换机、organizer rebind/reset、external 硬"已在他设备登记"拒绝、demo/seed 改造为 staff-first。
- **device_uuid**：保留作会话连续性/限流补充/取证旁证；启用指纹+白名单后**不再做主去重键**。

## C. 指纹作硬身份——风险立场（codex）

浏览器指纹**不可靠**作硬身份/跨设备拒绝键：碰撞中高（升级/隐私模式/WebView 变化/同型企业机）、规避高（清数据/换 WebView/反指纹）、合规高（PIPL 个人信息，需明示告知/最小必要/留存删除）。**结论**：仅作 external 去重与反作弊的弱补充信号，不作硬拒绝闸门。已记入风险，建议回写 REQUIREMENTS。

## D. 多 Agent 防撞泳道（codex 指派，写入 PROGRESS 后生效）

| Lane | Owner | 独占文件范围 | 说明 |
|---|---|---|---|
| T-070 规范/看板拆解 | Agent-B | `docs/REQ-CHANGE-001-*`、`docs/PROGRESS.md` | 仅文档/看板，不写码 |
| T-071 身份 schema/repo | Agent-A（主实现） | `internal/store/migrations/*`、`internal/repo/*` | 表/字段/数据契约 |
| T-072 参与身份路径 | Agent-A（主实现） | `internal/participation/*` + 指纹规范化包 | submit 期身份分支+错误码 |
| T-073 organizer 白名单 ops | Agent-B | `internal/organizer/*` | CSV 导入/列表/状态；只消费 A 的契约不改 |
| T-074 demo/seed/docs 并入 | Agent-B | `internal/seed/*`、`docs/ARCHITECTURE.md`、`docs/REQUIREMENTS.md` | 待 T-071/T-073 接口冻结(REVIEW)后开始 |

分支约定：`agent-a/req001-core`、`agent-b/req001-ops`；某 lane `DOING` 时他人不得改其文件范围。
> 主实现会话（本 Claude）认领 **T-071 / T-072**；T-070/073/074 留给协作 Agent，避免重复实现。
