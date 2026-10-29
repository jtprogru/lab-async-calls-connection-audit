"""Тест, который ловит незакрытый httpx-клиент.

По умолчанию оба теста зелёные: ResourceWarning молча проглатывается.
С флагами из Makefile (py-test-strict) первый тест падает:

    pytest -W error::ResourceWarning -W error::pytest.PytestUnraisableExceptionWarning
"""

import asyncio
import gc

import httpx
import pytest

from pylab import BASE


@pytest.fixture(autouse=True)
def collect_garbage():
    # Предупреждение рождается в финализаторе транспорта, поэтому сборщик
    # надо позвать явно, пока тест ещё не закончился.
    yield
    gc.collect()


def test_client_leaks():
    async def main():
        client = httpx.AsyncClient()  # aclose() никто не вызовет
        (await client.get(BASE + "/ok")).raise_for_status()

    asyncio.run(main())


def test_client_closed():
    async def main():
        async with httpx.AsyncClient() as client:
            (await client.get(BASE + "/ok")).raise_for_status()

    asyncio.run(main())
