#!/usr/bin/env bash
# Пять вариантов CLOSE_WAIT параллельно, каждый на своём порту:
#   A  DefaultClient (KeepAlive 30s), IdleTimeout сервера 2s
#   B  keepalive выключен, 2s
#   C  KeepAlive 10s, 2s
#   D  DefaultClient, IdleTimeout 10s
#   E  net.Dialer без KeepAlive (Go ставит 15s), 2s
# Порты: BASE_PORT, BASE_PORT+10, ... (по умолчанию от 28080). Нужен sudo.
set -u
here=$(cd "$(dirname "$0")" && pwd)
base=${BASE_PORT:-28080}
LAB_PORT=$base "$here/closewait.sh" A 2s no-close-default &
LAB_PORT=$((base + 10)) "$here/closewait.sh" B 2s no-close-nka &
LAB_PORT=$((base + 20)) "$here/closewait.sh" C 2s no-close-ka10 &
LAB_PORT=$((base + 30)) "$here/closewait.sh" D 10s no-close-default &
LAB_PORT=$((base + 40)) "$here/closewait.sh" E 2s no-close-dialer &
wait
