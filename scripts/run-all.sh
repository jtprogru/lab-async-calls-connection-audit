#!/usr/bin/env bash
# Прогоняет всю лабу и складывает сырые выводы в каталог, по файлу на режим.
#
#   scripts/run-all.sh results/linux
#   SETS="go long" FOR=2m scripts/run-all.sh results/docker
#
# SETS — какие наборы гнать: go, py, service, long (по умолчанию все).
# FOR  — длительность длинных таймлайнов (по умолчанию 4m).
# Длинные режимы идут параллельно, каждый со своим target на своих портах.
set -uo pipefail

out=${1:?usage: run-all.sh <каталог>}
root=$(cd "$(dirname "$0")/.." && pwd)
sets=${SETS:-go py service long}
FOR=${FOR:-4m}
mkdir -p "$out"
out=$(cd "$out" && pwd)
cd "$root"

os=$(uname)
has() { [[ " $sets " == *" $1 "* ]]; }
py() { (cd "$root/python" && uv run --frozen "$@"); }
sudo_ok() { [[ $(id -u) -eq 0 ]] || sudo -n true 2>/dev/null; }

header() {
  echo "# дата: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  if [[ $os == Darwin ]]; then
    echo "# ОС: macOS $(sw_vers -productVersion), ядро $(uname -r) $(uname -m)"
  else
    echo "# ОС: $(. /etc/os-release && echo "$PRETTY_NAME"), ядро $(uname -r) $(uname -m)"
    echo "# net.ipv4.tcp_fin_timeout=$(cat /proc/sys/net/ipv4/tcp_fin_timeout)"
  fi
  echo "# $(go version)"
  echo "# Python $(py python -c 'import sys,httpx,requests; print(sys.version.split()[0], "httpx", httpx.__version__, "requests", requests.__version__)' 2>/dev/null)"
  echo "# команда: $*"
  echo
}

# run <файл> <idle> <команда...>: поднимает target и пишет вывод с шапкой.
run() {
  local name=$1 idle=$2
  shift 2
  { header "$@"; scripts/with-target.sh "$idle" "$@"; } >"$out/$name.txt" 2>&1
  echo "  $name"
}

if has go; then
  echo "go:"
  run go-transport-per-call 0 bin/golab -dump transport-per-call
  run go-client-per-call 0 bin/golab client-per-call
  run go-shared 0 bin/golab shared
  run go-graceful-close 0 bin/golab -tw graceful-close
  run go-no-close 2s bin/golab no-close
  run go-read-no-close 0 bin/golab read-no-close
  run go-close-no-read 0 bin/golab close-no-read
  if [[ $os == Linux ]]; then
    run go-close-no-read-big 0 scripts/nstat.sh bin/golab -tw close-no-read-big
  else
    run go-close-no-read-big 0 bin/golab -tw close-no-read-big
  fi
  run go-hang 0 bin/golab hang
  run go-errors 0 bin/golab errors
  run go-maxconns 0 bin/golab maxconns
  run go-maxconns-notimeout 0 bin/golab maxconns-notimeout
fi

if has py; then
  echo "py:"
  for m in pool unclosed pool-timeout fire-and-forget cancel blocking no-timeout; do
    run "py-$m" 0 bash -c "cd python && uv run --frozen python pylab.py $m"
  done
  run py-unclosed-W-always 0 bash -c 'cd python && uv run --frozen python -W always::ResourceWarning pylab.py unclosed 2>&1 | sort | uniq -c | sort -rn'
  run py-blocking-X-dev 0 bash -c 'cd python && uv run --frozen python -X dev pylab.py blocking'
  run py-callgraph 0 bash -c 'cd python && uv run --frozen python callgraph.py'
  run py-pytest 0 bash -c 'cd python && uv run --frozen pytest -q'
  run py-pytest-strict 0 bash -c 'cd python && uv run --frozen pytest -q -W error::ResourceWarning -W error::pytest.PytestUnraisableExceptionWarning'
  # Снаружи процесса: python -m asyncio ps без прав и с sudo.
  ps_check='cd python && (uv run --frozen python callgraph.py 15 >${TMPDIR:-/tmp}/lab-cg.out &) ; sleep 2;
    pid=$(awk "/^PID/ {print \$2}" ${TMPDIR:-/tmp}/lab-cg.out); venv=$(uv run --frozen python -c "import sys; print(sys.executable)");
    echo "## python -m asyncio ps $pid (обычный пользователь)"; "$venv" -m asyncio ps "$pid" 2>&1 | head -8;
    echo "## sudo python -m asyncio ps $pid"; if [ "$(id -u)" -eq 0 ] || sudo -n true 2>/dev/null; then sudo -n "$venv" -m asyncio ps "$pid" 2>&1 | head -8; else echo "sudo без пароля нет, пропущено"; fi;
    if [ -r /proc/sys/kernel/yama/ptrace_scope ]; then echo "## kernel.yama.ptrace_scope=$(cat /proc/sys/kernel/yama/ptrace_scope)"; fi; sleep 14'
  run py-asyncio-ps 0 bash -c "$ps_check"
fi

if has service; then
  echo "service:"
  { header make service-inventory; make -s service-inventory; } >"$out/service-inventory.txt" 2>&1 && echo "  service-inventory"
  { header make service-lint; make -s service-lint; } >"$out/service-lint.txt" 2>&1; echo "  service-lint"
fi

if has long; then
  echo "long (FOR=$FOR, параллельно):"
  long() {
    local name=$1 port=$2 idle=$3
    shift 3
    { header "LAB_PORT=$port idle=$idle $*"; LAB_PORT=$port LAB_STATS=$((port + 1)) scripts/with-target.sh "$idle" "$@"; } >"$out/$name.txt" 2>&1
    echo "  $name"
  }
  capture=()
  if [[ $os == Linux ]] && sudo_ok && command -v tcpdump >/dev/null; then
    capture=(scripts/rst-capture.sh)
  fi
  long long-no-close-default 18090 2s bin/golab -raw -for "$FOR" no-close-default &
  long long-no-close-nka 18092 2s bin/golab -raw -for "$FOR" no-close-nka &
  long long-no-close-ka10 18094 2s bin/golab -raw -for "$FOR" no-close-ka10 &
  long long-no-close-default-idle10 18096 10s bin/golab -for "$FOR" no-close-default &
  if [[ ${#capture[@]} -gt 0 ]]; then
    long long-probe-default-ka 18098 2s "${capture[@]}" 18098 bin/golab -for 100s probe &
    long long-probe-no-ka 18100 2s "${capture[@]}" 18100 bin/golab -for 100s -ka -1s probe &
  else
    long long-probe-default-ka 18098 2s bin/golab -for 100s probe &
    long long-probe-no-ka 18100 2s bin/golab -for 100s -ka -1s probe &
  fi
  wait
fi
echo "готово: $out"
