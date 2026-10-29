#!/usr/bin/env bash
# Close недочитанного мегабайта: FIN или RST, и где копится TIME_WAIT.
# Отдельный netns, чтобы счётчики nstat и ss видели только этот прогон. sudo run-bigclose.sh
set -u
NS=deepbig; B=/home/user/deep/bin; L=/home/user/deep/logs/bigclose; mkdir -p $L
ip netns add $NS 2>/dev/null; ip netns exec $NS ip link set lo up
X="ip netns exec $NS"
$X $B/target -addr 127.0.0.1:28080 -stats 127.0.0.1:28081 >$L/target.log 2>&1 &
TP=$!; sleep 0.5
for path in /big /ok; do
  tag=${path#/}
  $X nstat -n >/dev/null   # сбросить историю nstat в этом netns
  $X tcpdump -i lo -nn -U -w $L/$tag.pcap "tcp port 28080" >/dev/null 2>&1 &
  DP=$!; sleep 1
  $X sudo -u user $B/bigclose -port 28080 -path $path -n 50 -hold 3s >$L/$tag.client.log 2>&1 &
  CP=$!; sleep 2
  {
    echo "### $path: ss states on port 28080 (2s after requests, client alive)"
    $X ss -tanH "( sport = :28080 or dport = :28080 )" | awk '{print $1, ($4 ~ /:28080$/ ? "server" : "client")}' | sort | uniq -c
    echo "### nstat delta"
    $X nstat -z TcpOutRsts TcpEstabResets TcpExtTCPAbortOnClose TcpActiveOpens TcpPassiveOpens | sed 1d
  } >$L/$tag.state.txt
  wait $CP
  sleep 1; kill $DP; wait $DP 2>/dev/null
  echo "### $path: flags in capture" >>$L/$tag.state.txt
  tcpdump -r $L/$tag.pcap -nn 2>/dev/null | grep -o 'Flags \[[A-Z.]*\]' | sort | uniq -c >>$L/$tag.state.txt
  echo "### $path: who sent R / F" >>$L/$tag.state.txt
  tcpdump -r $L/$tag.pcap -nn 2>/dev/null | awk '/Flags \[(R|R\.|F\.)\]/{split($3,a,"."); side=(a[5]=="28080")?"server->client":"client->server"; print side, $7}' | sort | uniq -c >>$L/$tag.state.txt
done
kill $TP; wait 2>/dev/null; ip netns del $NS; echo done
