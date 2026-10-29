#!/usr/bin/env bash
# Взгляд дежурного снаружи процесса: раз в N секунд печатает, сколько у PID
# TCP-сокетов к порту по состояниям и сколько у него открытых socket-fd.
# На Linux эти два числа расходятся, когда сокет получил RST: TCP уже нет,
# а дескриптор остался.
#
#   scripts/watch.sh <PID> [PORT] [INTERVAL]
set -euo pipefail
pid=$1
port=${2:-${LAB_PORT:-18080}}
every=${3:-5}
start=$(date +%s)
while kill -0 "$pid" 2>/dev/null; do
  t=$(( $(date +%s) - start ))
  if [[ $(uname) == Linux ]]; then
    states=$(ss -Htanp "dport = :$port" 2>/dev/null | grep "pid=$pid," | awk '{print $1}' | sort | uniq -c | awk '{printf "%s=%s ", $2, $1}')
    fds=$(find /proc/"$pid"/fd -lname 'socket:*' 2>/dev/null | wc -l)
  else
    states=$(lsof -nP -a -p "$pid" -iTCP:"$port" 2>/dev/null | awk 'NR>1 {gsub(/[()]/, "", $NF); print $NF}' | sort | uniq -c | awk '{printf "%s=%s ", $2, $1}')
    fds=$(lsof -nP -a -p "$pid" -i 2>/dev/null | awk 'NR>1' | wc -l)
  fi
  printf '+%4ds  сокеты к :%s: %-28s socket-fd у процесса: %s\n' "$t" "$port" "${states:-нет}" "$(echo $fds)"
  sleep "$every"
done
