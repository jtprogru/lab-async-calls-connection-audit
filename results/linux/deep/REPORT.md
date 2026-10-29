<!--
Архив прогона Linux deep-dive, сложен как есть, без правок текста ниже.

- Прогон шёл на портах 28080 и выше (28080, 28090 … 28120 для вариантов A–E,
  28080 внутри отдельных netns для TIME_WAIT и bigclose), параллельно с основным
  набором лабы на 18080+. Хост: Ubuntu 26.04.1, ядро 7.0.0-34-generic.
- `src/` — исходники и скрипты именно этого прогона. В репозитории они стали
  `scripts/linux/*.sh` и режимами `golab`: deepclient -ka default-client → no-close-default,
  off → no-close-nka, 10s → no-close-ka10, go-default → no-close-dialer;
  bigclose -path /big → close-no-read-big, /ok → close-no-read. Повторить: `make linux-deep`.
- Из `logs/logs/bigclose/` не положен `big.pcap` (13 МБ мегабайтных тел из «x»):
  флаги и счётчики из него есть в `big.state.txt`.
-->

# Linux deep-dive: CLOSE_WAIT, RST, TIME_WAIT, ss, Yama

Хост: Ubuntu 26.04.1, ядро 7.0.0-34-generic, iproute2 6.19.0, Go 1.27.3, Python 3.14.4, util-linux 2.41.3. Дата прогона 2026-10-29.
Скрипты: `src/` (deepclient, bigclose, target, run-closewait.sh, run-timewait.sh, run-bigclose.sh). Сырые логи: `logs/logs/{A..E,tw-ft10,tw-ft60,bigclose}`, выжимки `logs/*.txt`.
Пункт 2 гонялся в отдельных netns (`ip netns`), sysctl хоста не менялся; Yama на 1 мин переключалась в 0 и возвращена в 1.

## 1. no-close на Linux: кто и когда шлёт RST

20 запросов без Close, сервер `IdleTimeout`, наблюдение 300 с. Клиентские сокеты по `/proc/self/fd` + `/proc/net/tcp`.

| вариант | keepalive клиента (факт по tcpdump) | FIN сервера | CLOSE_WAIT → нет в таблице TCP | RST (tcpdump, одно соединение) |
|---|---|---|---|---|
| A DefaultClient, idle 2s | idle 30s, пробы каждые 30 с | +2.0 с | между +90 и +95 с | 12:42:36.401 = +90.4 с после FIN сервера, в ответ на 3-ю пробу |
| B KeepAlive:-1, idle 2s | нет | +2.0 с | никогда: CLOSE_WAIT=20 все 300 с | только при выходе процесса: FIN клиента → RST |
| C KeepAlive:10s, idle 2s | idle 10s, дальше каждые ~15.4 с | +2.0 с | между +70 и +75 с | +71.5 с после FIN |
| D DefaultClient, idle 10s | 30 с | +10.0 с | между +100 и +105 с | +90.6 с после FIN |
| E Dialer без KeepAlive (Go 15s), idle 2s | 15 с | +2.0 с | между +75 и +80 с | +76.6 с после FIN |

Механика (A, `logs/tcpdump-one-conn.txt`):
```
12:41:05.961 server > client [F.]            сервер закрыл по IdleTimeout, у сервера orphan FIN_WAIT2
12:41:06.001 client > server [.] ack 121      клиент: CLOSE_WAIT
12:41:35.985 client > server [.] (проба)  ->  12:41:35.985 server > client [.] ack 99   отвечает timewait-сокет сервера
12:42:06.193 client > server [.] (проба)  ->  12:42:06.193 server > client [.] ack 99   ещё жив (60.2 с после FIN)
12:42:36.401 client > server [.] (проба)  ->  12:42:36.401 server > client [R]          сокета сервера уже нет: RST
```
- На сервере orphan FIN_WAIT2 сразу превращается в timewait-сокет с подсостоянием FIN_WAIT2: `ss` показывает `FIN-WAIT-2 ... timer:(timewait,31sec,0)`, живёт `tcp_fin_timeout` = 60 с и умирает молча, RST сам не шлёт.
- Пока он жив, он отвечает на keepalive-пробы клиента ACK'ом (out-of-window ACK), поэтому клиент спокойно сидит в CLOSE_WAIT.
- RST клиент получает от ядра сервера в ответ на первый пакет после смерти timewait-сокета. Без keepalive и без трафика такого пакета нет → CLOSE_WAIT вечно (B).
- Go: `Dialer.KeepAlive` задаёт только idle; интервал по умолчанию 15 с, `DefaultTransport` ставит KeepAlive 30s (src/net/tcpsock.go newTCPConn, src/net/dial.go defaultTCPKeepAliveInterval). Отвеченная проба сбрасывает idle, поэтому у A и D период фактически 30 с, у C/E — 15 с.
- После RST (A, +107…+300 с): `client fds sockets: 20`, горутин 41 (без порта статистики; в посте 43 с ним), в `/proc/net/tcp` записей нет; `ss -a` по порту — только LISTEN; `lsof -nP -a -p PID -iTCP` — 0 строк; `lsof -nP -p PID` — `deepclien 2542 user 6u sock 0,10 0t0 23154 protocol: TCP` без адресов. `nstat TcpEstabResets` вырос на 80 (1 → 81: 4 варианта × 20).
- Отличие от macOS (из фактчека): там переход на ~62-й секунде у всех вариантов, независимо от keepalive. На Linux время зависит от keepalive клиента: «≈60 с после FIN сервера + до следующей пробы».

## 2. TIME_WAIT и tcp_fin_timeout (netns, `logs/logs/tw-ft{60,10}/timeline.log`)

10× curl (клиент закрывает первым → TIME_WAIT у клиента) + 5 соединений, которые сервер закрывает по idle 2s (orphan FIN_WAIT2 у сервера).

| tcp_fin_timeout | TIME_WAIT пропал | orphan FIN-WAIT-2 пропал |
|---|---|---|
| 60 | +61…+64 с (timer: 59sec → 428ms → нет) | +66…+68 с (2 с idle + 60 + грануляция) |
| 10 | +61…+64 с, без изменений | +10…+12 с (2 + 10) |

close-no-read-big (`logs/logs/bigclose/big.state.txt`, 50 × GET /big, `Close` без чтения): nstat в netns `TcpActiveOpens 50, TcpOutRsts 50, TcpExtTCPAbortOnClose 50, TcpEstabResets 100`; в tcpdump ни одного FIN, только `[R.]` client→server; через 2 с на порту только LISTEN — TIME_WAIT нет ни на одной стороне. Ядро шлёт RST, потому что в буфере приёма остались непрочитанные данные (net/ipv4/tcp.c ниже). Для сравнения /ok (3 байта): 2 соединения на 50 запросов, RST 0.

## 3. Конвейер `ss -Htn state close-wait | awk '{print $NF}'` (`logs/ss-pipeline.txt`)

С фильтром `state` колонка State не печатается: `Recv-Q Send-Q Local Peer`, т.е. `$NF` — адрес пира. Подтверждено: `20 127.0.0.1:28120 …` по 20 на каждый порт.
`-p` дописывает `users:(("deepclient",pid=2542,fd=9))` → `$NF` уникален на каждый сокет. `-e` дописывает `timer… uid ino sk cgroup -->`. `-o` дописывает `timer:(keepalive,11sec,0)` → группировка по таймеру. `-i` даёт вторую строку на сокет (табом, `cubic wscale…`): 200 строк вместо 100, каждую `awk` считает отдельным соединением. Формулировка в посте верна.

## 4. Yama и `python -m asyncio ps` (`logs/yama-asyncio-ps.txt`)

ptrace_scope=1 (дефолт Ubuntu). Целевой процесс — не потомок.
- тот же пользователь: `Error retrieving tasks: Failed to find the PyRuntime section in process 5188 on Linux platform`, exit 1 (и для `ps`, и для `pstree`). Сообщение вводит в заблуждение: секция есть, нет доступа к памяти.
- `sudo python3 -m asyncio ps 5188` / `pstree` — работает, таблица задач и дерево (audit-0 без awaiter).
- ptrace_scope=0 → работает без sudo; обратно 1 → снова та же ошибка. Значит, причина — Yama.
- `setpriv --ptracer any python3 stuck.py` (util-linux 2.41.3) → `asyncio ps` работает без sudo.
Python docs (3.14, Remote debugging → Permission requirements → Linux): «In general, you can debug your own processes, but … Some Linux distributions enable ptrace restrictions, aka "Yama"»; предлагают `setpriv --ptracer any python3` или `echo 0 | sudo tee /proc/sys/kernel/yama/ptrace_scope`; для чужих процессов — `sudo -E`.
Yama (kernel.org admin-guide/LSM/Yama): 0 — классика, любой процесс того же uid; 1 — только потомки (или кто объявлен через `prctl(PR_SET_PTRACER)`); 2 — только с `CAP_SYS_PTRACE`; 3 — ptrace запрещён совсем, до перезагрузки.

## 5. Ссылки на ядро (тег v7.3, последний релиз 7.x; содержимое сверено с git.kernel.org)

- https://github.com/torvalds/linux/blob/v7.3/include/net/tcp.h#L140-L147 — `#define TCP_TIMEWAIT_LEN (60*HZ)` и `TCP_FIN_TIMEOUT TCP_TIMEWAIT_LEN` («BSD style FIN_WAIT2 deadlock breaker… 60sec»).
- https://github.com/torvalds/linux/blob/v7.3/include/net/tcp.h#L1891-L1901 — `tcp_fin_time()`: берёт `linger2` или sysctl `tcp_fin_timeout`.
- https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp_input.c#L7341-L7354 — orphan в FIN_WAIT1 получил ACK → `tcp_time_wait(sk, TCP_FIN_WAIT2, tmo)` с `tmo = tcp_fin_time()` (наш случай).
- https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp.c#L3256-L3274 — то же в `__tcp_close`, если сокет уже в FIN_WAIT2 на момент close.
- https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp_minisocks.c#L373-L383 — в `tcp_time_wait()` для настоящего TIME_WAIT `timeo = TCP_TIMEWAIT_LEN`, sysctl не участвует.
- https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp_minisocks.c#L132-L184 — пакет в timewait-сокет с подсостоянием FIN_WAIT2: вне окна → ACK (L138-L144, это ответ на keepalive-пробу), новые данные → RST, FIN → настоящий TIME_WAIT на `TCP_TIMEWAIT_LEN`.
- https://github.com/torvalds/linux/blob/v7.3/Documentation/networking/ip-sysctl.rst#L534-L544 — `tcp_fin_timeout`: сколько orphan-соединение живёт в FIN_WAIT_2, default 60.
- https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp_timer.c#L816-L846 — `tcp_keepalive_timer`: пробы не шлются только в CLOSE и SYN_SENT, CLOSE_WAIT пробы шлёт (`tcp_write_wakeup`, L844).
- https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp.c#L3173-L3186 — close с непрочитанными данными: `TCPAbortOnClose` и RST вместо FIN (RFC 2525, 2.17).
