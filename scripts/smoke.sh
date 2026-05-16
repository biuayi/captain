#!/usr/bin/env bash
# 端到端冒烟：验证垂直切片
#   组织方登录 → 取入场链接 → 扫码进入 → 签到 → 大屏计数+1 → 导出 → 下载CSV
# 依赖 demo 种子数据（CAPTAIN_SEED=true，默认开）。
set -euo pipefail
BASE="${BASE:-http://localhost:8080}"
JAR="$(mktemp)"
trap 'rm -f "$JAR"' EXIT

say(){ printf '\n\033[1;36m== %s\033[0m\n' "$*"; }
jget(){ sed -n 's/.*"'"$1"'":"\([^"]*\)".*/\1/p'; }

say "healthz"
curl -fsS "$BASE/healthz"; echo

say "活动方登录 xundao"
TOK=$(curl -fsS -X POST "$BASE/api/v1/org/login" \
  -H 'Content-Type: application/json' \
  -d '{"login_name":"xundao","password":"xundao123"}' | jget token)
[ -n "$TOK" ] && echo "organizer token ok"

say "列出活动"
EV=$(curl -fsS "$BASE/api/v1/org/events" -H "Authorization: Bearer $TOK" \
  | sed -n 's/.*"events":\[{"id":"\([^"]*\)".*/\1/p')
echo "event_id=$EV"

say "取入场链接（event_token）"
ENTRY=$(curl -fsS "$BASE/api/v1/org/events/$EV/entry" -H "Authorization: Bearer $TOK")
ET=$(echo "$ENTRY" | jget event_token)
echo "$ENTRY"

say "扫码进入（mint device-session）"
curl -fsS -c "$JAR" "$BASE/api/v1/p/e/$EV?et=$ET&d=smoke-device-001" >/dev/null
echo "session minted"

say "提交签到 step s1"
curl -fsS -b "$JAR" -X POST "$BASE/api/v1/p/e/$EV/steps/s1/submit" \
  -H 'Content-Type: application/json' -d '{}'; echo

say "再次签到（验证幂等，计数不应再+1）"
curl -fsS -b "$JAR" -X POST "$BASE/api/v1/p/e/$EV/steps/s1/submit" \
  -H 'Content-Type: application/json' -d '{}'; echo

say "当前大屏计数"
curl -fsS "$BASE/api/v1/p/e/$EV/count"; echo

say "活动方触发导出"
JOB=$(curl -fsS -X POST "$BASE/api/v1/org/events/$EV/export" \
  -H "Authorization: Bearer $TOK" | jget job_id)
echo "job_id=$JOB"

say "轮询导出状态"
for i in $(seq 1 20); do
  S=$(curl -fsS "$BASE/api/v1/org/exports/$JOB" -H "Authorization: Bearer $TOK")
  ST=$(echo "$S" | jget status)
  echo "  status=$ST"
  [ "$ST" = "done" ] && break
  [ "$ST" = "failed" ] && { echo "$S"; exit 1; }
  sleep 1
done

say "下载导出 CSV"
curl -fsS "$BASE/api/v1/org/exports/$JOB/download" -H "Authorization: Bearer $TOK" \
  | head -5

say "冒烟通过 ✅"
