#!/usr/bin/env bash
# Smoke / full verification (PF-01). The Go test suite is a real cross-
# component E2E against dockerized PG/Redis/NATS (admin/org/participant
# logins, R1 multi-day gating, R2 upload, R3 scoring, R4 multi-pool draw,
# big-screen envelopes, records/export, tenant isolation, concurrency).
# This brings infra up and runs build + vet + the whole suite.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== infra up =="
scripts/testdb.sh up

echo "== go build =="
go build ./...

echo "== go vet =="
go vet ./...

echo "== go test ./... (real PG/Redis/NATS) =="
CAPTAIN_TEST_PG_DSN="postgres://captain:captain@localhost:5432/captain?sslmode=disable" \
CAPTAIN_TEST_REDIS_ADDR="localhost:6379" \
CAPTAIN_TEST_NATS_URL="nats://localhost:4222" \
  go test ./... -count=1

echo "== live full-chain e2e (real cmd/server) =="
scripts/e2e.sh

echo "SMOKE OK"
