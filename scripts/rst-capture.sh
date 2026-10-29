#!/usr/bin/env bash
# Снимает трафик на порт target через tcpdump, пока работает команда, и
# печатает пакеты с относительным временем. Видно, кто и когда шлёт FIN,
# keepalive-пробы и RST. Нужен root: на Linux — sudo, в Docker — --cap-add NET_RAW.
#
#   scripts/rst-capture.sh <PORT> <команда...>
set -euo pipefail
port=$1
shift
# Файл не создаём заранее: tcpdump от root не откроет чужой файл в /tmp
# (fs.protected_regular), поэтому пишем в свежий временный каталог.
tmp=$(mktemp -d)
pcap=$tmp/capture.pcap
sudo=""
[[ $(id -u) -ne 0 ]] && sudo="sudo"
iface=lo
[[ $(uname) == Darwin ]] && iface=lo0
$sudo tcpdump -i "$iface" -nn -U -w "$pcap" "tcp port $port" 2>/dev/null &
tcpdump_pid=$!
sleep 1
"$@"
sleep 1
$sudo kill "$tcpdump_pid" 2>/dev/null || true
wait "$tcpdump_pid" 2>/dev/null || true
echo "## tcpdump: tcp port $port, время от первого пакета"
$sudo tcpdump -nn -ttttt -r "$pcap" 2>/dev/null
$sudo rm -rf "$tmp"
