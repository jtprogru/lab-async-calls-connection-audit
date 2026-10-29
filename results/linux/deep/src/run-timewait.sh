#!/usr/bin/env bash
# TIME_WAIT и orphan FIN_WAIT2 при разных tcp_fin_timeout, в отдельном netns,
# чтобы не трогать sysctl хоста. Запускать с sudo: sudo run-timewait.sh <fin_timeout>
set -u
FT=$1; NS=deep$FT; B=~/deep/bin; L=~/deep/logs/tw-ft$FT; mkdir -p $L
ip netns add $NS 2>/dev/null; ip netns exec $NS ip link set lo up
ip netns exec $NS sysctl -w net.ipv4.tcp_fin_timeout=$FT
X="ip netns exec $NS"
$X $B/target -addr 127.0.0.1:28080 -stats 127.0.0.1:28081 -idle 2s >$L/target.log 2>&1 &
TP=$!; sleep 0.5
# 1) клиент закрывает первым: curl на выходе закрывает keep-alive соединение -> TIME_WAIT у клиента
for i in $(seq 10); do $X curl -s -o /dev/null http://127.0.0.1:28080/ok; done
# 2) сервер закрывает первым по IdleTimeout, клиент держит CLOSE_WAIT -> orphan FIN_WAIT2 у сервера
$X $B/deepclient -port 28080 -ka off -n 5 -dur 100s -step 5s >$L/client.log 2>&1 &
CP=$!
t0=$(date +%s)
while (( $(date +%s) - t0 <= 100 )); do
  el=$(( $(date +%s) - t0 ))
  tw=$($X ss -tanH state time-wait | wc -l)
  fw2=$($X ss -tanH state fin-wait-2 | wc -l)
  cw=$($X ss -tanH state close-wait | wc -l)
  echo "+${el}s TIME_WAIT=$tw FIN_WAIT_2=$fw2 CLOSE_WAIT=$cw | $($X ss -tanoH state time-wait state fin-wait-2 | head -1 | grep -o 'timer:([^)]*)')"
  sleep 2
done >$L/timeline.log
kill $TP $CP 2>/dev/null; wait 2>/dev/null
ip netns del $NS
echo done ft=$FT
