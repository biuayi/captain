#!/usr/bin/env bash
# Bring up the local test infra (postgres/redis/nats) and wait until ready.
# Migrations are applied by the Go test helper (internal/testdb) on first use.
# Usage: scripts/testdb.sh [up|down]
set -euo pipefail
cd "$(dirname "$0")/.."
CMP="docker compose -f deploy/docker-compose.yml"

case "${1:-up}" in
  up)
    $CMP up -d postgres redis nats
    for i in $(seq 1 30); do
      if docker exec deploy-postgres-1 pg_isready -U captain >/dev/null 2>&1; then
        echo "postgres ready"; break
      fi
      sleep 1
    done
    docker exec deploy-redis-1 redis-cli ping >/dev/null && echo "redis ready"
    echo "test infra up"
    ;;
  down)
    $CMP stop postgres redis nats
    echo "test infra stopped"
    ;;
  *)
    echo "usage: $0 [up|down]" >&2; exit 2 ;;
esac
