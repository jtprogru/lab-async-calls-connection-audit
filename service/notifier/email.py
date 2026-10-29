import aiohttp
MAIL_URL = "https://mail.example.com/send"

async def send_email(to: str, subject: str) -> None:
    session = aiohttp.ClientSession()
    async with session.post(MAIL_URL, json={"to": to, "subject": subject}) as resp:
        resp.raise_for_status()
