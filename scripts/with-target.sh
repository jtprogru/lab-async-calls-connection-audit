#!/usr/bin/env bash
# Поднимает target с нужным IdleTimeout, ждёт порт статистики, запускает
# команду и гасит target. Порты берутся из LAB_PORT и LAB_STATS.
#
#   scripts/with-target.sh 2s bin/golab no-close
set -euo pipefail
idle=$1
shift
root=$(cd "$(dirname "$0")/.." && pwd)
"$root/bin/target" -idle "$idle" 2>/dev/null &
pid=$!
trap 'kill $pid 2>/dev/null || true; wait $pid 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do
  if curl -fsS "http://${LAB_ADDR:-127.0.0.1}:${LAB_STATS:-18081}/stats" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
"$@"
