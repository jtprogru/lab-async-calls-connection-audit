import httpx


async def payment_status(order_id: str) -> str:
    async with httpx.AsyncClient() as client:  # новый пул на каждый вызов
        resp = await client.get(f"http://payments.internal/status/{order_id}")
        resp.raise_for_status()
        return resp.json()["status"]
