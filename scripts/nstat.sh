#!/usr/bin/env bash
# Показывает, как изменились TCP-счётчики ядра за время команды (только Linux).
# TcpExtTCPAbortOnData — закрыли сокет с непрочитанными данными, ядро ответило
# RST. TcpEstabResets — RST, полученный в ESTABLISHED или CLOSE_WAIT.
#
#   scripts/nstat.sh <команда...>
set -euo pipefail
counters="TcpEstabResets TcpOutRsts TcpExtTCPAbortOnData TcpExtTCPAbortOnClose TcpExtTCPAbortOnTimeout TcpExtTW"
nstat -n >/dev/null
"$@"
sleep 1
echo "## nstat: прирост счётчиков за время команды"
# shellcheck disable=SC2086
nstat -z $counters | awk 'NR>1 {printf "%-26s %s\n", $1, $2}'
