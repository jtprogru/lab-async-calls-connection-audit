#!/usr/bin/env bash
# Таймлайн CLOSE_WAIT на Linux: сервер target с IdleTimeout, клиент без Close,
# tcpdump на lo и снимки ss обеих сторон раз в 5 с.
# usage: run-closewait.sh <name> <port> <idle> <ka> <dur_s>
set -u
name=$1 port=$2 idle=$3 ka=$4 dur=${5:-300}
G=~/deep/bin; L=~/deep/logs/$name; mkdir -p "$L"
"$G/target" -addr 127.0.0.1:$port -stats 127.0.0.1:$((port+1)) -idle "$idle" >"$L/target.log" 2>&1 &
TP=$!
sleep 0.5
sudo tcpdump -i lo -nn -U -w "$L/cap.pcap" "tcp port $port" >"$L/tcpdump.log" 2>&1 &
DP=$!
sleep 1
"$G/deepclient" -port "$port" -ka "$ka" -dur "${dur}s" >"$L/client.log" 2>&1 &
CP=$!
sleep 0.3
( t0=$(date +%s.%N)
  while kill -0 $CP 2>/dev/null; do
    now=$(date +%s.%N); el=$(awk -v a=$now -v b=$t0 'BEGIN{printf "%.1f", a-b}')
    echo "=== +${el}s $(date +%T.%3N)"
    ss -tanoH "( sport = :$port or dport = :$port )" | awk '{print $1, $4, $5, $6, $7, $8}' | sort | uniq -c | sort -rn | head -8
    echo "client fds sockets: $(ls -l /proc/$CP/fd 2>/dev/null | grep -c socket:)"
    nstat -saz TcpEstabResets TcpOutRsts TcpExtTCPAbortOnClose TcpExtTCPAbortOnData 2>/dev/null | awk 'NR>1{printf "%s=%s ", $1, $2} END{print ""}'
    i=$((${i:-0}+1))
    if (( i % 12 == 0 )); then
      echo "--- detail: lsof -nP -a -p CP -iTCP"; lsof -nP -a -p $CP -iTCP 2>&1 | head -6
      echo "--- detail: lsof -nP -p CP | grep sock/IPv"; lsof -nP -p $CP 2>&1 | grep -E 'sock|IPv' | head -6
      echo "--- detail: lsof -nP -a -p CP -iTCP | wc -l = $(lsof -nP -a -p $CP -iTCP 2>/dev/null | wc -l)"
      echo "--- detail: ss -tanpoH all for client pid"; ss -tanpoH | grep "pid=$CP," | head -4
      echo "--- detail: ss -a count for port: $(ss -taH "( sport = :$port or dport = :$port )" | wc -l)"
    fi
    sleep 5
  done ) >"$L/ss.log" 2>&1
# финальные снимки, пока клиент ещё был жив — снимаем перед выходом отдельно
kill $TP 2>/dev/null; sleep 1; sudo kill $DP 2>/dev/null; wait 2>/dev/null
echo done "$name"
