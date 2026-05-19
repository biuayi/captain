#!/usr/bin/env bash
# G5: live full-chain E2E against a real cmd/server process.
# Boots captain (seeded v2 demo) on dockerized pg/redis/nats, then drives:
#   participant login → R1 checkin → R2 form → R3 exam → R4 draw
#   → organizer login → stats/participants.
# Exit non-zero on any failed step. Self-contained (kills the server it spawns).
set -euo pipefail
cd "$(dirname "$0")/.."

scripts/testdb.sh up

# Fresh isolated DB so seed.Run takes the full v2 demo path (the shared test
# DB is polluted by go test). Server runs migrations on startup.
docker exec deploy-postgres-1 psql -U captain -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS captain_e2e WITH (FORCE);" \
  -c "CREATE DATABASE captain_e2e;" >/dev/null

PORT=18080
BASE="http://localhost:${PORT}"
LOG="$(mktemp)"
export CAPTAIN_HTTP_ADDR=":${PORT}"
export CAPTAIN_PUBLIC_BASE_URL="$BASE"
export CAPTAIN_PG_DSN="postgres://captain:captain@localhost:5432/captain_e2e?sslmode=disable"
export CAPTAIN_REDIS_ADDR="localhost:6379"
export CAPTAIN_NATS_URL="nats://localhost:4222"
export CAPTAIN_SEED=true
export CAPTAIN_SEED_ORG_PW="e2eorgpw"
export CAPTAIN_SEED_ADMIN_PW="e2eadminpw"

go build -o /tmp/captain-e2e ./cmd/server
/tmp/captain-e2e >"$LOG" 2>&1 &
SRV=$!
cleanup() { kill "$SRV" 2>/dev/null || true; }
trap cleanup EXIT

for i in $(seq 1 40); do
  curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 0.5
done
curl -fsS "$BASE/healthz" >/dev/null

EVENT_ID=$(grep -oE 'event_id=[0-9a-f-]+' "$LOG" | head -1 | cut -d= -f2)
ET=$(grep -oE 'event_token=[A-Za-z0-9._-]+' "$LOG" | head -1 | cut -d= -f2)
[ -n "$EVENT_ID" ] && [ -n "$ET" ] || { echo "FAIL: no seeded event in log"; cat "$LOG"; exit 1; }
echo "event_id=$EVENT_ID"

jget() { python3 -c "import sys,json;print(json.load(sys.stdin).get('$1',''))"; }
P="$BASE/api/v1/p/e/$EVENT_ID"

# landing (public)
curl -fsS "$P?et=$ET" >/dev/null

# participant login (seed whitelist E1001 / phone_last4 1234)
TOK=$(curl -fsS -XPOST "$P/login" -H 'Content-Type: application/json' \
  -d '{"employee_number":"E1001","phone_last4":"1234"}' | jget token)
[ -n "$TOK" ] || { echo "FAIL: participant login"; exit 1; }
AUTH=(-H "Authorization: Bearer $TOK")

# R1 checkin (days=1 → completes R1)
SC=$(curl -fsS -XPOST "${AUTH[@]}" "$P/steps/r1/submit" -d '{"geo":{"lat":31.2,"lng":121.4,"accuracy":5}}' | jget stage_complete)
[ "$SC" = "True" ] || { echo "FAIL: R1 stage_complete=$SC"; exit 1; }

# R2 form (+ data fields)
curl -fsS -XPOST "${AUTH[@]}" "$P/steps/r2/submit" -d '{"fields":{"opinion":"good","photo_image":"uploads/x"}}' >/dev/null

# R3 exam (seed answer index 1 correct)
PASS=$(curl -fsS -XPOST "${AUTH[@]}" "$P/steps/r3/submit" -d '{"answers":{"0":[1]}}' | jget passed)
[ "$PASS" = "True" ] || { echo "FAIL: R3 passed=$PASS"; exit 1; }

# R4 draw (idempotent)
RB=$(curl -fsS -XPOST "${AUTH[@]}" "$P/steps/r4/draw" -d '{}' | jget resolved_by)
[ -n "$RB" ] || { echo "FAIL: R4 draw"; exit 1; }
echo "draw resolved_by=$RB"

# own records
curl -fsS "${AUTH[@]}" "$P/me/records" | grep -q '"steps"' || { echo "FAIL: me/records"; exit 1; }

# organizer login + stats/participants
OTOK=$(curl -fsS -XPOST "$BASE/api/v1/org/login" -H 'Content-Type: application/json' \
  -d '{"login_name":"demo","password":"e2eorgpw"}' | jget token)
[ -n "$OTOK" ] || { echo "FAIL: organizer login"; exit 1; }
OA=(-H "Authorization: Bearer $OTOK")
curl -fsS "${OA[@]}" "$BASE/api/v1/org/events/$EVENT_ID/stats" | grep -q 'participated' \
  || { echo "FAIL: org stats"; exit 1; }
curl -fsS "${OA[@]}" "$BASE/api/v1/org/events/$EVENT_ID/participants" | grep -q '"total"' \
  || { echo "FAIL: org participants"; exit 1; }

echo "E2E OK — login→R1→R2→R3→R4→records→org stats/participants all passed"
