#!/usr/bin/env bash
# Starts a disposable PostgreSQL for the store contract tests and prints the
# DSN to export as ZRCIN_TEST_PG_DSN. The container is throwaway: its data
# lives on a tmpfs and `--rm` removes it on stop. Nothing here touches a
# production instance; the port (15931) is loopback-only.
#
# Usage:
#   export ZRCIN_TEST_PG_DSN=$(bash scripts/test-pg.sh)      # start
#   bash scripts/test-pg.sh --stop                            # stop
set -euo pipefail

NAME=zrcin-test-pg
PORT=15931

case "${1:-}" in
  --stop)
    docker rm -f "$NAME" >/dev/null
    exit 0
    ;;
esac

if docker ps --format '{{.Names}}' | grep -qx "$NAME"; then
  echo "postgres://postgres:zrcin-test@127.0.0.1:$PORT/zrcin_test?sslmode=disable"
  exit 0
fi

docker run -d --name "$NAME" --rm \
  -e POSTGRES_PASSWORD=zrcin-test -e POSTGRES_DB=zrcin_test \
  -p "127.0.0.1:$PORT:5432" \
  --tmpfs /var/lib/postgresql/data:rw,size=1g \
  postgres:16-alpine >/dev/null

for _ in $(seq 1 30); do
  if docker exec "$NAME" pg_isready -U postgres -d zrcin_test >/dev/null 2>&1; then
    docker exec "$NAME" psql -U postgres -d zrcin_test \
      -c "CREATE EXTENSION IF NOT EXISTS pg_trgm" >/dev/null
    echo "postgres://postgres:zrcin-test@127.0.0.1:$PORT/zrcin_test?sslmode=disable"
    exit 0
  fi
  sleep 1
done
echo "zrcin-test-pg did not become ready in 30s" >&2
exit 1
