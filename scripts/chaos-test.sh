#!/usr/bin/env sh
set -eu
API_URL="${API_URL:-http://localhost:8080}"
restore() { docker compose start postgres >/dev/null; }
trap restore EXIT INT TERM
curl --max-time 10 -fsS "$API_URL/health" | grep '"status":"healthy"'
docker compose stop postgres
code=$(curl --max-time 10 -s -o /dev/null -w '%{http_code}' "$API_URL/health" || true)
echo "Database stopped: /health=$code (expected 503)"
test "$code" = 503
docker compose start postgres
for attempt in $(seq 1 30); do
  code=$(curl --max-time 5 -s -o /dev/null -w '%{http_code}' "$API_URL/health" || true)
  if [ "$code" = 200 ]; then
    curl --max-time 10 -fsS "$API_URL/api/v1/branches" | grep '"BR-01"' >/dev/null
    echo "CHAOS PASS: /health 200 -> 503 -> 200; branch query recovered (attempt $attempt)"
    exit 0
  fi
  sleep 1
done
echo "CHAOS FAIL: recovery exceeded 30 attempts"
exit 1
