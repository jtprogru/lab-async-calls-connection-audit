import asyncio, os

async def audit():
    await asyncio.Event().wait()  # висит без хозяина

async def handle_order():
    await asyncio.sleep(3600)

async def main():
    asyncio.create_task(audit(), name="audit-0")
    await asyncio.create_task(handle_order(), name="order-0")

print(os.getpid(), flush=True)
asyncio.run(main())
