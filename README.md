# lab-async-calls-connection-audit

Код лабы к посту [«Кто открыл и не закрыл: схемы асинхронных вызовов как инструмент аудита»](https://jtprog.ru/posts/async-calls-connection-audit/). Здесь всё, из чего в посте получены цифры: тестовый сервер, клиенты на Go и Python с каждой утечкой по отдельности, синтетический сервис заказов для инвентаризации и линтеров, скрипты замеров и сырые выводы прогонов на macOS, в Docker и на живом Linux.

Идея простая: не верить на слово ни посту, ни документации. Каждая утечка запускается одной make-целью, а вывод показывает то же, что увидит дежурный в проде: сколько соединений принял сервер, в каком состоянии сокеты клиента, сколько у процесса горутин или задач.

## Что внутри

```text
go/cmd/target    тестовый сервер: /ok, /big, /slow?ms=N, /hang и статистика на отдельном порту
go/cmd/golab     клиент на Go, по режиму на каждую утечку из поста
python/          клиент на Python: pylab.py, callgraph.py и тест для pytest
service/         синтетический orders (Go) и notifier (Python) из шага 1 поста
scripts/         запуск с сервером, таймлайны, tcpdump, nstat, прогон всего набора
docker/          образ для Linux-прогона на macOS
results/         сырые выводы: macos/, docker/, linux/
```

## Что нужно

- Go 1.27 (прогоны сделаны на 1.27.3).
- [uv](https://docs.astral.sh/uv/): он сам поставит Python 3.14.6 и зависимости из `python/uv.lock`.
- `lsof` на macOS. На Linux сокеты считаются через `/proc`, а для внешних замеров пригодятся `ss`, `nstat` и `tcpdump`.
- Для `make service-*`: `ripgrep`, golangci-lint 2.x и `uvx` для ruff.
- Для Linux на macOS: Docker Desktop.

## Быстрый старт

```bash
make build py-sync
make go-pool        # пул на каждый вызов
make go-no-close    # тело ответа не закрыто: CLOSE_WAIT
make py-cancel      # отмена не доходит до фоновой задачи
make help           # все цели
```

Каждая цель `go-*` и `py-*` сама поднимает `target` с нужным `IdleTimeout` и гасит его после прогона, так что руками ничего держать не надо. Порты по умолчанию 18080 и 18081, меняются через `LAB_PORT` и `LAB_STATS`.

## Раздел поста → команда → что смотреть

| Раздел поста | Команда | Что смотреть |
| --- | --- | --- |
| Шаг 1. Инвентаризация | `make service-inventory` | два `rg` по `service/`: 10 строк Go и 5 строк Python, к `payments` ведут четыре разных клиента |
| Go: новый Transport на каждый вызов | `make go-pool`, `make go-dump` | 50 вызовов дают 50 соединений и 103 горутины, `client-per-call` и `shared` — одно соединение; в дампе 51 пара `readLoop`/`writeLoop` |
| Go: тело ответа не закрыто | `make go-no-close` | `ESTABLISHED=20` → `CLOSE_WAIT=20` через 2 с; на пятой секунде `Client.Timeout` всё прибивает |
| То же навсегда, с `DefaultClient` | `make go-no-close-long`, `make go-probe` | когда `CLOSE_WAIT` превращается в «TCP нет, дескриптор есть»: на macOS и Linux это разные секунды, см. ниже |
| Go: закрыть ещё не значит дочитать | `make go-body`, `make go-tw` | три байта возвращают соединение в пул, мегабайт его закрывает; `TIME_WAIT` после мегабайта не остаётся |
| Go: вызов без таймаута | `make go-hang` | пять запросов и пять сокетов висят 30 секунд и дальше |
| Четыре таймаута и их сообщения | `make go-errors` | «awaiting headers» даже для адреса, до которого соединение не дошло |
| Go: пул кончился, а ошибка показывает не туда | `make go-maxconns` | четвёртый запрос ждёт 2 с в собственной очереди, сервер обслужил три |
| Python: клиент на каждый вызов | `make py-pool`, `make py-unclosed` | 50 соединений вместо одного; без `aclose()` они живут до `gc.collect()`, и Python пишет `ResourceWarning: unclosed transport` |
| Python: незакрытый стрим и PoolTimeout | `make py-pool-timeout` | `httpx.PoolTimeout` через 2 с, сервер обслужил три запроса |
| Python: fire-and-forget | `make py-fire-and-forget` | `201 Created` клиенту и «Task exception was never retrieved» в логе |
| Python: отмена не доходит до фоновой задачи | `make py-cancel` | с `create_task` сервер ещё две секунды обслуживает ненужный запрос, с `TaskGroup` — нет |
| Python: синхронный вызов в асинхронном коде | `make py-blocking`, `make py-no-timeout` | `requests` превращает 10 параллельных запросов в 3 секунды очереди; debug-режим пишет `Executing <Task ...> took 0.306 seconds` |
| `asyncio.print_call_graph` и `python -m asyncio ps` | `make py-callgraph`, `scripts/asyncio-ps.sh` | у `order-0` есть «Awaited by», у `audit-0` нет; снаружи процесса без прав отказ |
| Pytest ловит незакрытый клиент | `make py-test`, `make py-test-strict` | по умолчанию оба теста зелёные, с `-W error` тест с утёкшим клиентом краснеет |
| Что ловят линтеры | `make service-lint` | `go vet`, golangci-lint (`bodyclose`, `noctx`, `errcheck`, `contextcheck`) и ruff (`RUF006`, `S113`, `ASYNC`) |

Номера строк в выводе Python совпадают с постом: `pylab.py:85` и `:87` у `send_receipt`, `:140` у `call` в `blocking`, `callgraph.py` строки 7, 11 и 25.

## Как мерить снаружи процесса

Режимы `golab` и `pylab.py` считают свои сокеты сами, но в проде у тебя будет только PID. Те же числа снаружи:

```bash
# macOS: состояния TCP-сокетов процесса к порту
lsof -nP -a -p "$PID" -iTCP:18080

# Linux: CLOSE_WAIT по удалённому адресу и дескрипторы процесса
ss -Htn state close-wait | awk '{print $NF}' | sort | uniq -c
find /proc/"$PID"/fd -lname 'socket:*' | wc -l

# раз в 5 секунд: сокеты по состояниям и число socket-fd, они расходятся после RST
scripts/watch.sh "$PID" 18080 5

# кто и когда шлёт FIN, keepalive-пробы и RST (нужен root)
scripts/rst-capture.sh 18080 bin/golab -for 100s probe

# прирост TCP-счётчиков ядра за время команды (Linux)
scripts/nstat.sh bin/golab -tw close-no-read-big
```

Дамп горутин в проде отдаёт `/debug/pprof/goroutine?debug=1`, если подключён `net/http/pprof`. Здесь его печатает `make go-dump`.

## Linux в Docker и на живом хосте

На macOS Linux-вариант проще всего гонять в Docker Desktop: у контейнеров своё ядро Linux, и сокеты ведут себя по-линуксовому.

```bash
make docker-go        # быстрые Go-режимы
make docker-long      # таймлайн CLOSE_WAIT и одно соединение под tcpdump
make docker-results   # всё сразу в results/docker/
make docker-shell     # шелл в контейнере, дальше те же make-цели
```

На живом Linux всё работает как есть: `make build py-sync`, потом любые цели. Для `scripts/rst-capture.sh` и целей `linux-*` нужен `sudo`.

Полный прогон с сырыми выводами по файлу на режим:

```bash
make results                                  # results/macos или results/linux
SETS="go long" FOR=2m scripts/run-all.sh out/ # только Go и длинные таймлайны на 2 минуты
```

## Linux: куда девается CLOSE_WAIT

Главное расхождение с macOS. Двадцать запросов без `Close`, сервер закрывает простаивающие соединения через `IdleTimeout`, клиент остаётся в `CLOSE_WAIT`. Дальше на macOS все варианты переходят в `CLOSED` ровно через 60 секунд после FIN сервера, а на Linux время зависит от keepalive клиента, и без keepalive переход не наступает вообще.

| Вариант | Режим `golab` | IdleTimeout сервера | Linux: RST, секунд после запросов | Linux: иногда | macOS: `CLOSED` |
| --- | --- | --- | --- | --- | --- |
| `http.DefaultClient`, keepalive 30s | `no-close-default` | 2s | ≈92 | ≈63 | ≈62 |
| `net.Dialer` без `KeepAlive`, Go ставит 15s | `no-close-dialer` | 2s | ≈79 | ≈63 | ≈62 |
| `KeepAlive: 10 * time.Second` | `no-close-ka10` | 2s | ≈73 | — | ≈62 |
| `http.DefaultClient` | `no-close-default` | 10s | ≈101 | ≈71 | ≈70 |
| `KeepAlive: -1`, keepalive выключен | `no-close-nka` | 2s | никогда | — | ≈62 |

Колонка «иногда» — не опечатка. Каждый вариант я прогнал на Linux и в Docker от шести до десяти раз: архив `results/linux/deep/`, два полных прогона на хосте и два в Docker, повтор `results/linux/rerun/`. В `results/linux/` и `results/docker/` лежит последний прогон из каждой пары. KeepAlive 10s каждый раз давал ≈73 секунды, без keepalive RST не пришёл ни разу. У `DefaultClient` с `IdleTimeout` 2s и 10s RST один раз из шести пришёл на целый период пробы раньше, у варианта с 15-секундными пробами — три раза из восьми. Почему так, объяснено ниже, в пятом пункте.

Что происходит на Linux, по tcpdump и исходникам ядра:

1. Через `IdleTimeout` сервер закрывает соединение и шлёт FIN. Клиент отвечает ACK и уходит в `CLOSE_WAIT`: ядро ждёт, пока приложение вызовет `Close`.
2. Серверный сокет осиротел, приложение его уже закрыло. Ядро превращает его в timewait-сокет с подсостоянием `FIN_WAIT2`, `ss` показывает его как `FIN-WAIT-2` с таймером `timewait`. Живёт он `net.ipv4.tcp_fin_timeout`, по умолчанию 60 секунд, и умирает молча, без RST.
3. Пока он жив, он отвечает ACK на keepalive-пробы клиента, и клиент спокойно сидит в `CLOSE_WAIT`.
4. Первая проба после его смерти получает RST от ядра сервера: сокета уже нет. Клиентский сокет пропадает из `/proc/net/tcp`, из `ss -a` и из `lsof -iTCP`, а дескриптор остаётся. `golab` показывает это как «дескрипторов без TCP-соединения: 20», `lsof -p` — как `sock … protocol: TCP` без адресов.
5. Отсюда и числа в таблице: 2 или 10 секунд `IdleTimeout`, плюс 60 секунд `tcp_fin_timeout`, плюс время до следующей пробы. Отвеченная проба сбрасывает таймер простоя, поэтому у `DefaultClient` пробы идут раз в 30 секунд, а не раз в 15. Периоды 30, 15 и 10 секунд делят 60 нацело, и одна из проб приходится ровно на момент, когда серверный сокет умирает. Таймеры ядра чуть плывут: в tcpdump пробы с периодом 15 секунд идут на +17,0, +32,4, +47,8 и +63,1 секунды от запроса. Успела проба до смерти сокета — получила ACK, и RST придёт со следующей. Не успела — RST приходит сразу, на 62–63-й секунде после запросов.

Без keepalive пакета, на который можно ответить RST, нет, и `CLOSE_WAIT` висит, пока жив процесс. На macOS RST приходит через 60 секунд после FIN сервера при любом keepalive, в том числе выключенном: похоже, ядро XNU само шлёт RST, когда истекает осиротевший `FIN_WAIT_2` (`net.inet.tcp.fin_timeout`).

Горутин во всех вариантах 43 до конца прогона: ядро свою часть сделало, а `persistConn` и дескриптор принадлежат приложению. Поэтому счётчик `CLOSE_WAIT` даёт только нижнюю границу утечки, а надёжнее считать дескрипторы и горутины.

Где это в ядре (тег v7.3):

- [`TCP_TIMEWAIT_LEN` и `TCP_FIN_TIMEOUT`](https://github.com/torvalds/linux/blob/v7.3/include/net/tcp.h#L140-L147): 60 секунд, зашиты константой;
- [`tcp_fin_time()`](https://github.com/torvalds/linux/blob/v7.3/include/net/tcp.h#L1891-L1901): берёт `linger2` или sysctl `tcp_fin_timeout`;
- [осиротевший `FIN_WAIT1` получил ACK](https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp_input.c#L7341-L7354): `tcp_time_wait(sk, TCP_FIN_WAIT2, tmo)`;
- [пакет в timewait-сокет с подсостоянием `FIN_WAIT2`](https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp_minisocks.c#L132-L184): вне окна — ACK, это и есть ответ на keepalive-пробу;
- [настоящий `TIME_WAIT`](https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp_minisocks.c#L373-L383): `timeo = TCP_TIMEWAIT_LEN`, sysctl не участвует;
- [`tcp_keepalive_timer`](https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp_timer.c#L816-L846): в `CLOSE_WAIT` пробы шлются;
- [`tcp_fin_timeout` в ip-sysctl.rst](https://github.com/torvalds/linux/blob/v7.3/Documentation/networking/ip-sysctl.rst#L534-L544).

### TIME_WAIT и `tcp_fin_timeout`

`net.ipv4.tcp_fin_timeout` укорачивает осиротевший `FIN_WAIT_2`, а `TIME_WAIT` не трогает. Проверено в отдельных network namespace: 10 соединений, которые закрывает клиент, и 5, которые закрывает сервер.

| `tcp_fin_timeout` | `TIME_WAIT` пропал | осиротевший `FIN-WAIT-2` пропал |
| --- | --- | --- |
| 60 | через 61–64 с | через 66–68 с: 2 с `IdleTimeout` + 60 |
| 10 | через 61–64 с, без изменений | через 10–12 с: 2 + 10 |

`Close` недочитанного мегабайта заканчивается RST от клиента, а не FIN: в буфере приёма остались данные, и ядро обрывает соединение. В `nstat` это `TcpExtTCPAbortOnClose 50` и `TcpOutRsts 50` на 50 запросов, в tcpdump нет ни одного FIN, `TIME_WAIT` нет ни на одной стороне. Код ядра: [`__tcp_close`, непрочитанные данные](https://github.com/torvalds/linux/blob/v7.3/net/ipv4/tcp.c#L3173-L3186).

### Цели для Linux

Нужны `sudo`, `tcpdump`, `ss`, `nstat`, `iproute2` и `curl`.

```bash
make linux-closewait   # пять вариантов из таблицы параллельно, tcpdump на lo, DUR=300
make linux-timewait    # tcp_fin_timeout 60 и 10, каждый в своём netns
make linux-bigclose    # Close недочитанного мегабайта и трёх байт, свой netns
make linux-deep        # всё сразу
```

Логи ложатся в `results/linux/rerun/`, захваты `cap.pcap` в git не попадают, а одно соединение крупным планом сохраняется текстом в `one-conn.txt`. `linux-timewait` и `linux-bigclose` создают network namespace `lab-tw60`, `lab-tw10` и `lab-big` и удаляют их в конце. `sysctl -w net.ipv4.tcp_fin_timeout` выполняется внутри namespace: у каждого netns свой `tcp_fin_timeout`, и sysctl хоста эти цели не трогают. Проверить можно до и после: `sysctl net.ipv4.tcp_fin_timeout`.

## macOS, Docker и Linux рядом

| Режим | macOS 27, XNU | Docker Desktop, ядро 6.10 linuxkit | Ubuntu 26.04, ядро 7.0 |
| --- | --- | --- | --- |
| `transport-per-call` | 50 соединений, 103 горутины | так же | так же |
| `no-close`, `Client.Timeout` 5s | `CLOSE_WAIT=20` на 3-й секунде, на 6-й сокетов нет | так же | так же |
| `no-close-default`, через 4 минуты | `CLOSED=20` с 62-й секунды, в `lsof` видны как `(CLOSED)` | TCP нет, 20 дескрипторов: с 63-й секунды в последнем прогоне, с 93-й в предыдущем | TCP нет, 20 дескрипторов, с 92-й секунды |
| `no-close-nka` | `CLOSED=20` с 62-й секунды | `CLOSE_WAIT=20` все 4 минуты | `CLOSE_WAIT=20` все 4 минуты |
| `graceful-close` и `close-no-read-big`, `TIME_WAIT` через 1 с | не измерить: `netstat` не показывает TCP | 50 у клиента после `graceful-close`, 0 после мегабайта | 50 у клиента после `graceful-close`, 0 после мегабайта |
| `close-no-read-big` | новое соединение на каждый запрос | так же, `TcpExtTCPAbortOnClose` +50 | так же, `TcpExtTCPAbortOnClose` +50 |
| `errors`, dial в `10.255.255.1` | «awaiting headers» через 2 с | так же | так же |
| `python -m asyncio ps` без прав | отказ с отсылкой к документации | работает: в контейнере ты root | отказ: «Failed to find the PyRuntime section», `ptrace_scope=1` |
| остальные режимы | совпадают с постом | совпадают | совпадают |

Сырые выводы лежат в `results/<среда>/<режим>.txt`, у каждого шапка с датой, ОС, ядром и версиями Go, Python и httpx. В `results/linux/deep/` — архив отдельного прогона с tcpdump по каждому варианту, `TIME_WAIT` в netns и разбором `ss` и Yama.

## Оговорки

- Абсолютные числа на твоей машине будут другими: время до RST на Linux зависит от того, когда пришлась проба (см. гонку выше), а `blocking` — от загрузки. Порядок величин и форма кривых те же.
- На моём macOS 27.0.1 `netstat -an` перестал показывать TCP-сокеты, поэтому `TIME_WAIT` на macOS `golab -tw` оценивает по приросту `sysctl net.inet.tcp.pcbcount`. Это число общее на хост и шумит от любых других соединений. Точные числа по `TIME_WAIT` — на Linux.
- `dial в никуда` ведёт себя так, только если у хоста есть маршрут до `10.255.255.1` и пакеты туда тихо теряются. Без маршрута будет мгновенное «network is unreachable».
- `python -m asyncio ps` на Linux упирается в Yama. При `kernel.yama.ptrace_scope=1` (по умолчанию в Ubuntu) ptrace разрешён только к потомкам, поэтому к соседнему процессу того же пользователя без `sudo` не пустит, и сообщение об ошибке при этом сбивает с толку. Варианты из [документации Python](https://docs.python.org/3.14/howto/remote_debugging.html#permission-requirements): `sudo`, запуск цели через `setpriv --ptracer any` или `ptrace_scope=0`. Про уровни Yama — [в документации ядра](https://www.kernel.org/doc/html/latest/admin-guide/LSM/Yama.html).
- `make py-test-strict` завершается с ошибкой, так и задумано. Pytest записывает находку как ERROR в teardown теста с утёкшим клиентом: предупреждение рождается в `gc.collect()` из фикстуры.
- `golab` считает сокеты через `lsof` на macOS и через `/proc` на Linux. На других ОС режимы работают, но сокеты показываются как `n/a`.

## Лицензия

MIT, см. [LICENSE](LICENSE).
