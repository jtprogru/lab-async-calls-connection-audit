#!/usr/bin/env bash
# python -m asyncio ps снаружи процесса: сначала от своего пользователя, потом
# от root. На Linux с kernel.yama.ptrace_scope=1 (по умолчанию в Ubuntu) первый
# вызов получает отказ, хотя процесс свой: Yama пускает ptrace только к потомкам.
# Запускать рядом с работающим target.
set -uo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root/python" || exit 1
py=$(uv run --frozen python -c 'import sys; print(sys.executable)')
"$py" callgraph.py 15 >/dev/null &
pid=$!
sleep 2
if [[ -r /proc/sys/kernel/yama/ptrace_scope ]]; then
  echo "## kernel.yama.ptrace_scope=$(cat /proc/sys/kernel/yama/ptrace_scope)"
fi
echo "## python -m asyncio ps $pid (uid $(id -u))"
"$py" -m asyncio ps "$pid" 2>&1 | head -8
echo "## python -m asyncio ps $pid от root"
if [[ $(id -u) -eq 0 ]]; then
  echo "уже root, см. выше"
elif sudo -n true 2>/dev/null; then
  sudo -n "$py" -m asyncio ps "$pid" 2>&1 | head -8
else
  echo "sudo без пароля нет, пропущено"
fi
kill "$pid" 2>/dev/null
wait "$pid" 2>/dev/null
