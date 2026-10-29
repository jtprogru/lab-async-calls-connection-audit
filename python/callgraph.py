import asyncio, os, sys, httpx

BASE = f"http://127.0.0.1:{os.environ.get('LAB_PORT', '18080')}"


async def audit(client):
    await client.get(BASE + "/hang")

async def handle_order(client, i):
    asyncio.create_task(audit(client), name=f"audit-{i}")
    await client.get(BASE + "/hang")


async def dump():
    await asyncio.sleep(0.5)
    for t in sorted(asyncio.all_tasks(), key=asyncio.Task.get_name):
        if t.get_name().startswith(("audit", "order")): asyncio.print_call_graph(t)


async def main():
    print("PID", os.getpid(), flush=True)
    async with httpx.AsyncClient(timeout=None) as client, asyncio.timeout(float(sys.argv[1]) if len(sys.argv) > 1 else 1):
        asyncio.create_task(dump(), name="dump")
        o = asyncio.create_task(handle_order(client, 0), name="order-0")
        await o


# Аргумент — сколько секунд держать задачи живыми. С большим значением
# процесс можно разглядывать снаружи: python -m asyncio ps <PID>.
try:
    asyncio.run(main())
except TimeoutError:
    pass
