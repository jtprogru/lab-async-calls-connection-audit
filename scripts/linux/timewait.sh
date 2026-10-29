#!/usr/bin/env bash
# TIME_WAIT и осиротевший FIN_WAIT_2 при разном tcp_fin_timeout.
#
# Всё идёт в отдельном network namespace: net.ipv4.tcp_fin_timeout там свой,
# sysctl хоста не меняется. Запускать от root:
#
#   sudo scripts/linux/timewait.sh 60
#   sudo scripts/linux/timewait.sh 10
set -u
FT=${1:?usage: timewait.sh <tcp_fin_timeout>}
root=$(cd "$(dirname "$0")/../.." && pwd)
BIN=${BIN:-$root/bin}
port=${LAB_PORT:-28080}
NS=lab-tw$FT
L=${LOGS:-$root/results/linux/rerun}/tw-ft$FT
mkdir -p "$L" || exit 1
# Каталоги создаёт root; отдаём их тому, кто звал sudo, иначе closewait.sh
# от обычного пользователя не сможет писать рядом.
if [[ -n ${SUDO_USER:-} ]]; then
  chown "$SUDO_USER": "$(dirname "$L")" "$L"
fi
X="ip netns exec $NS"
ip netns add "$NS" 2>/dev/null
$X ip link set lo up
$X sysctl -w net.ipv4.tcp_fin_timeout="$FT"
$X env LAB_PORT="$port" LAB_STATS=$((port + 1)) "$BIN/target" -idle 2s >"$L/target.log" 2>&1 &
TP=$!
sleep 0.5
# 1) Клиент закрывает первым: curl на выходе закрывает keep-alive -> TIME_WAIT у клиента.
for _ in $(seq 10); do $X curl -s -o /dev/null "http://127.0.0.1:$port/ok"; done
# 2) Сервер закрывает первым по IdleTimeout, клиент держит CLOSE_WAIT -> осиротевший FIN_WAIT_2 у сервера.
$X env LAB_PORT="$port" LAB_STATS=$((port + 1)) "$BIN/golab" -n 5 -for 100s -step 5s no-close-nka >"$L/client.log" 2>&1 &
CP=$!
t0=$(date +%s)
while (($(date +%s) - t0 <= 100)); do
  el=$(($(date +%s) - t0))
  tw=$($X ss -tanH state time-wait | wc -l)
  fw2=$($X ss -tanH state fin-wait-2 | wc -l)
  cw=$($X ss -tanH state close-wait | wc -l)
  echo "+${el}s TIME_WAIT=$tw FIN_WAIT_2=$fw2 CLOSE_WAIT=$cw | $($X ss -tanoH state time-wait state fin-wait-2 | head -1 | grep -o 'timer:([^)]*)')"
  sleep 2
done >"$L/timeline.log"
kill "$TP" "$CP" 2>/dev/null
wait 2>/dev/null
ip netns del "$NS"
[[ -n ${SUDO_USER:-} ]] && chown -R "$SUDO_USER": "$L"
echo "готово: tcp_fin_timeout=$FT ($L)"
