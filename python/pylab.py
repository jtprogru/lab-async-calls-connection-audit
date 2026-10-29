"""Python-половина лабы: утечки httpx и asyncio из поста.

Ходит в тот же Go target, что и golab. Запуск: uv run python pylab.py <режим>.
"""

import asyncio
import collections
import gc
import json
import os
import subprocess
import sys
import time
import urllib.request

import httpx
import requests

HOST = os.environ.get("LAB_ADDR", "127.0.0.1")
PORT = int(os.environ.get("LAB_PORT", "18080"))
BASE = f"http://{HOST}:{PORT}"
STATS = f"http://{HOST}:{os.environ.get('LAB_STATS', '18081')}"


def stats() -> dict:
    with urllib.request.urlopen(STATS + "/stats") as r:
        return json.load(r)


def reset() -> None:
    with urllib.request.urlopen(STATS + "/reset") as r:
        r.read()


def sockets() -> str:
    """TCP-сокеты этого процесса к порту target: lsof на macOS, /proc на Linux."""
    if sys.platform == "linux":
        return _sockets_proc()
    out = subprocess.run(
        ["lsof", "-nP", "-a", "-p", str(os.getpid()), f"-iTCP:{PORT}"],
        capture_output=True, text=True,
    ).stdout
    c = collections.Counter(
        line.rsplit("(", 1)[1].rstrip(")") for line in out.splitlines()[1:] if "(" in line
    )
    return " ".join(f"{k}={v}" for k, v in sorted(c.items())) or "нет"


def _sockets_proc() -> str:
    states = {"01": "ESTABLISHED", "06": "TIME_WAIT", "08": "CLOSE_WAIT", "07": "CLOSE"}
    by_inode = {}
    for path in ("/proc/net/tcp", "/proc/net/tcp6"):
        with open(path) as f:
            for line in f.readlines()[1:]:
                p = line.split()
                by_inode[p[9]] = (states.get(p[3], p[3]), int(p[2].rsplit(":", 1)[1], 16))
    c = collections.Counter()
    for fd in os.listdir("/proc/self/fd"):
        try:
            link = os.readlink(f"/proc/self/fd/{fd}")
        except OSError:
            continue
        if link.startswith("socket:["):
            state, rport = by_inode.get(link[8:-1], (None, None))
            if rport == PORT:
                c[state] += 1
    return " ".join(f"{k}={v}" for k, v in sorted(c.items())) or "нет"


def line(label: str) -> None:
    print(f"{label:<20} сервер принял соединений: {stats()['accepted']}, сокеты клиента: {sockets()}", flush=True)


T0 = time.monotonic()


def log(msg: str) -> None:
    print(f"[{time.monotonic() - T0:4.1f} с] {msg}", flush=True)


# ---------------------------------------------------------------- fire-and-forget
# Ответ клиенту ушёл, а чек брошен в create_task без ссылки: его ошибку никто не ждёт.


async def send_receipt(order_id: int) -> None:
    await asyncio.sleep(0.1)
    raise RuntimeError(f"SMS-провайдер ответил 503 для заказа {order_id}")


async def create_order(order_id: int) -> str:
    asyncio.create_task(send_receipt(order_id))  # fire-and-forget
    return "201 Created"


async def fire_and_forget() -> None:
    log("handler вернул: " + await create_order(42))
    await asyncio.sleep(1)
    log("ждём ещё секунду, ошибки никто не видел")
    await asyncio.sleep(1)
    log("конец")


# ---------------------------------------------------------------- клиент на вызов


async def pool() -> None:
    reset()
    for _ in range(50):
        async with httpx.AsyncClient() as client:  # новый пул на каждый вызов
            (await client.get(BASE + "/ok")).raise_for_status()
    await asyncio.sleep(0.2)
    line("client-per-call")

    reset()
    async with httpx.AsyncClient() as client:
        for _ in range(50):
            (await client.get(BASE + "/ok")).raise_for_status()
        await asyncio.sleep(0.2)
        line("shared")


async def unclosed() -> None:
    reset()
    for _ in range(20):
        client = httpx.AsyncClient()  # aclose() никто не вызовет
        (await client.get(BASE + "/ok")).raise_for_status()
    await asyncio.sleep(0.2)
    line("client-unclosed")
    gc.collect()
    await asyncio.sleep(0.2)
    line("  после gc.collect()")


# ---------------------------------------------------------------- синхронный вызов в async


async def blocking() -> None:
    url = BASE + "/slow?ms=300"

    async def call():
        return requests.get(url, timeout=5)  # синхронный вызов внутри async def

    t = time.monotonic()
    await asyncio.gather(*(call() for _ in range(10)))
    print(f"requests  10 «параллельных» запросов по 300 мс: {time.monotonic() - t:.2f} с", flush=True)

    async with httpx.AsyncClient() as client:
        t = time.monotonic()
        await asyncio.gather(*(client.get(url) for _ in range(10)))
        print(f"httpx     10 «параллельных» запросов по 300 мс: {time.monotonic() - t:.2f} с", flush=True)

    t = time.monotonic()
    await asyncio.gather(*(asyncio.to_thread(requests.get, url, timeout=5) for _ in range(10)))
    print(f"to_thread 10 «параллельных» запросов по 300 мс: {time.monotonic() - t:.2f} с", flush=True)


# ---------------------------------------------------------------- пул кончился


async def pool_timeout() -> None:
    reset()
    limits = httpx.Limits(max_connections=3)
    timeout = httpx.Timeout(5.0, pool=2.0)
    async with httpx.AsyncClient(limits=limits, timeout=timeout) as client:
        for _ in range(3):
            req = client.build_request("GET", BASE + "/ok")
            await client.send(req, stream=True)  # ответ не закрыт: соединение занято
        t = time.monotonic()
        try:
            await client.get(BASE + "/ok")
        except httpx.PoolTimeout as e:
            print(f"четвёртый запрос через {time.monotonic() - t:.1f} с: httpx.PoolTimeout: {e!r}")
        print(f"сервер обслужил запросов: {stats()['served']}")

    reset()
    async with httpx.AsyncClient(limits=limits, timeout=timeout) as client:
        for _ in range(10):
            async with client.stream("GET", BASE + "/ok"):
                pass  # закрыт, но не дочитан
        print(f"исправлено через async with: 10 запросов прошли, сервер принял соединений: {stats()['accepted']}")


# ---------------------------------------------------------------- отмена


async def audit_call(client: httpx.AsyncClient) -> None:
    try:
        await client.get(BASE + "/slow", params={"ms": 3000})
        log("audit: запрос в фоне всё-таки завершился")
    except asyncio.CancelledError:
        log("audit: отменён вместе с родителем")
        raise


async def handler_detached(client):
    asyncio.create_task(audit_call(client))  # живёт своей жизнью
    await client.get(BASE + "/slow", params={"ms": 3000})


async def handler_structured(client):
    async with asyncio.TaskGroup() as tg:  # дети не переживут родителя
        tg.create_task(audit_call(client))
        await client.get(BASE + "/slow", params={"ms": 3000})


async def cancel() -> None:
    global T0

    async def probe():
        for at in (1.3, 3.8):
            await asyncio.sleep(at - (time.monotonic() - T0))
            log(f"висит запросов на сервере: {stats()['inflight']}")

    for title, handler in (("create_task", handler_detached), ("TaskGroup", handler_structured)):
        print("#", title, flush=True)
        async with httpx.AsyncClient(timeout=10) as client:
            T0 = time.monotonic()
            p = asyncio.create_task(probe())
            try:
                async with asyncio.timeout(1):
                    await handler(client)
            except TimeoutError:
                log("handler отменён по таймауту 1 с")
            await p
        print(flush=True)


# ---------------------------------------------------------------- requests без таймаута


def no_timeout() -> None:
    import threading

    result = []

    def go():
        try:
            requests.get(BASE + "/hang")  # timeout не задан
            result.append("вернулся")
        except Exception as e:  # noqa: BLE001
            result.append(repr(e))

    th = threading.Thread(target=go, daemon=True)
    th.start()
    th.join(8)
    print(f"requests.get(/hang) без timeout через 8 с: поток жив = {th.is_alive()}, "
          f"висит запросов на сервере: {stats()['inflight']}")


MODES = {
    "pool": pool,
    "unclosed": unclosed,
    "pool-timeout": pool_timeout,
    "fire-and-forget": fire_and_forget,
    "cancel": cancel,
    "blocking": blocking,
}

if __name__ == "__main__":
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    if mode == "no-timeout":
        no_timeout()
    elif mode in MODES:
        asyncio.run(MODES[mode]())
    else:
        sys.exit("usage: pylab.py {" + ",".join([*MODES, "no-timeout"]) + "}")
