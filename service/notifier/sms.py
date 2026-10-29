import requests

SMS_URL = "https://sms.example.com/send"


async def send_sms(phone: str, text: str) -> None:
    resp = requests.post(SMS_URL, json={"phone": phone, "text": text})
    resp.raise_for_status()
