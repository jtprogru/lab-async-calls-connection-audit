import asyncio

from notifier.email import send_email
from notifier.payments import payment_status
from notifier.sms import send_sms


async def on_order(event: dict) -> None:
    if await payment_status(event["order_id"]) != "paid":
        return
    # обе задачи без сохранённой ссылки
    asyncio.create_task(send_sms(event["phone"], "Заказ оплачен"))
    asyncio.create_task(send_email(event["email"], "Заказ оплачен"))
