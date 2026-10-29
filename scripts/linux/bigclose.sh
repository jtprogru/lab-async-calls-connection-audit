#!/usr/bin/env bash
# Close недочитанного ответа: мегабайт против трёх байт. Кто шлёт FIN, кто RST
# и остаётся ли TIME_WAIT. Отдельный netns, чтобы nstat и ss видели только
# этот прогон. Запускать от root: sudo scripts/linux/bigclose.sh
set -u
root=$(cd "$(dirname "$0")/../.." && pwd)
BIN=${BIN:-$root/bin}
port=${LAB_PORT:-28080}
NS=lab-big
L=${LOGS:-$root/results/linux/rerun}/bigclose
mkdir -p "$L"
X="ip netns exec $NS"
export LAB_PORT=$port LAB_STATS=$((port + 1))
ip netns add "$NS" 2>/dev/null
$X ip link set lo up
$X "$BIN/target" >"$L/target.log" 2>&1 &
TP=$!
sleep 0.5
for mode in close-no-read-big close-no-read; do
  $X nstat -n >/dev/null
  $X tcpdump -i lo -nn -U -w "$L/$mode.pcap" "tcp port $port" >/dev/null 2>&1 &
  DP=$!
  sleep 1
  $X "$BIN/golab" -hold 3s "$mode" >"$L/$mode.client.log" 2>&1 &
  CP=$!
  sleep 2
  {
    echo "### $mode: состояния на порту $port через 2 с, клиент ещё жив"
    $X ss -tanH "( sport = :$port or dport = :$port )" | awk -v p="$port" '{print $1, ($4 ~ ":"p"$" ? "server" : "client")}' | sort | uniq -c
    echo "### прирост nstat"
    $X nstat -z TcpOutRsts TcpEstabResets TcpExtTCPAbortOnClose TcpActiveOpens TcpPassiveOpens | sed 1d
  } >"$L/$mode.state.txt"
  wait "$CP"
  sleep 1
  kill "$DP"
  wait "$DP" 2>/dev/null
  {
    echo "### $mode: флаги в захвате"
    tcpdump -r "$L/$mode.pcap" -nn 2>/dev/null | grep -o 'Flags \[[A-Z.]*\]' | sort | uniq -c
    echo "### кто слал R и F"
    tcpdump -r "$L/$mode.pcap" -nn 2>/dev/null | awk -v p="$port" '/Flags \[(R|R\.|F\.)\]/ {split($3, a, "."); side = (a[5] == p) ? "server->client" : "client->server"; print side, $7}' | sort | uniq -c
  } >>"$L/$mode.state.txt"
done
kill "$TP"
wait 2>/dev/null
ip netns del "$NS"
echo "готово: $L"
