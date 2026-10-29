#!/usr/bin/env bash
# Таймлайн CLOSE_WAIT на Linux: target с IdleTimeout, golab без Close,
# tcpdump на lo и снимки ss обеих сторон раз в 5 секунд.
#
#   scripts/linux/closewait.sh <имя> <idle> <режим golab>
#   LAB_PORT=28080 DUR=300 scripts/linux/closewait.sh A 2s no-close-default
#
# Нужен sudo для tcpdump. Логи: $LOGS/<имя>/{client,ss,tcpdump,target}.log и cap.pcap.
set -u
name=$1 idle=$2 mode=$3
root=$(cd "$(dirname "$0")/../.." && pwd)
BIN=${BIN:-$root/bin}
port=${LAB_PORT:-28080}
dur=${DUR:-300}
L=${LOGS:-$root/results/linux/rerun}/$name
mkdir -p "$L" || exit 1
export LAB_PORT=$port LAB_STATS=$((port + 1))

"$BIN/target" -idle "$idle" >"$L/target.log" 2>&1 &
TP=$!
sleep 0.5
# shellcheck disable=SC2024 # лог tcpdump пишем от своего пользователя, так и задумано
sudo tcpdump -i lo -nn -U -w "$L/cap.pcap" "tcp port $port" >"$L/tcpdump.log" 2>&1 &
DP=$!
sleep 1
"$BIN/golab" -for "${dur}s" -step 5s "$mode" >"$L/client.log" 2>&1 &
CP=$!
sleep 0.3
(
  t0=$(date +%s.%N)
  i=0
  while kill -0 "$CP" 2>/dev/null; do
    now=$(date +%s.%N)
    el=$(awk -v a="$now" -v b="$t0" 'BEGIN{printf "%.1f", a-b}')
    echo "=== +${el}s $(date +%T.%3N)"
    ss -tanoH "( sport = :$port or dport = :$port )" | awk '{print $1, $4, $5, $6, $7, $8}' | sort | uniq -c | sort -rn | head -8
    echo "client fds sockets: $(find /proc/"$CP"/fd -lname 'socket:*' 2>/dev/null | wc -l)"
    nstat -saz TcpEstabResets TcpOutRsts TcpExtTCPAbortOnClose TcpExtTCPAbortOnData 2>/dev/null | awk 'NR>1{printf "%s=%s ", $1, $2} END{print ""}'
    i=$((i + 1))
    if ((i % 12 == 0)); then
      echo "--- detail: lsof -nP -a -p CP -iTCP"; lsof -nP -a -p "$CP" -iTCP 2>&1 | head -6
      echo "--- detail: lsof -nP -p CP | grep sock/IPv"; lsof -nP -p "$CP" 2>&1 | grep -E 'sock|IPv' | head -6
      echo "--- detail: ss -tanpoH для клиента"; ss -tanpoH | grep "pid=$CP," | head -4
      echo "--- detail: ss -a по порту: $(ss -taH "( sport = :$port or dport = :$port )" | wc -l)"
    fi
    sleep 5
  done
) >"$L/ss.log" 2>&1
kill "$TP" 2>/dev/null
sleep 1
sudo kill "$DP" 2>/dev/null
wait 2>/dev/null
# Одно соединение крупным планом: первый клиентский порт из захвата.
cport=$(sudo tcpdump -nn -r "$L/cap.pcap" 2>/dev/null | awk -v p="$port" '$5 ~ "\\."p":$" {split($3, a, "."); print a[5]; exit}')
{
  echo "## $name: клиентский порт $cport"
  sudo tcpdump -nn -tttt -r "$L/cap.pcap" "port $cport" 2>/dev/null
} >"$L/one-conn.txt"
echo "готово: $name ($L)"
