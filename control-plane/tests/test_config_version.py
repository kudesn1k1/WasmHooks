import asyncio

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncEngine

from controlplane.configstate.version import bump_config_version
from controlplane.tables import config_changes, config_state


async def current_version(engine: AsyncEngine) -> int:
    async with engine.connect() as conn:
        return int((await conn.execute(select(config_state.c.version))).scalar_one())


async def test_bump_increments_and_records_change(engine: AsyncEngine) -> None:
    async with engine.begin() as conn:
        assert await bump_config_version(conn, "hook.created", {"name": "a"}) == 2

    assert await current_version(engine) == 2
    async with engine.connect() as conn:
        rows = (await conn.execute(select(config_changes))).mappings().all()
    assert [(r["version"], r["kind"], r["payload"]) for r in rows] == [
        (2, "hook.created", {"name": "a"})
    ]


async def test_rollback_leaves_no_trace(engine: AsyncEngine) -> None:
    async with engine.connect() as conn:
        tx = await conn.begin()
        await bump_config_version(conn, "test", {})
        await tx.rollback()

    assert await current_version(engine) == 1
    async with engine.connect() as conn:
        assert (await conn.execute(select(config_changes))).first() is None


async def test_bump_waits_for_previous_commit(engine: AsyncEngine) -> None:
    # The trap behind decision D20: versions must follow commit order, so the
    # second writer has to wait for the first one to commit.
    async with engine.connect() as c1, engine.connect() as c2:
        t1 = await c1.begin()
        assert await bump_config_version(c1, "test", {"n": 1}) == 2

        async def second() -> int:
            async with c2.begin():
                return await bump_config_version(c2, "test", {"n": 2})

        task = asyncio.create_task(second())
        done, _ = await asyncio.wait({task}, timeout=0.3)
        assert not done, "second writer must wait for the first commit"
        await t1.commit()
        assert await task == 3


async def test_concurrent_bumps_hand_out_every_version_once(engine: AsyncEngine) -> None:
    async def one() -> int:
        async with engine.begin() as conn:
            return await bump_config_version(conn, "test", {})

    versions = await asyncio.gather(*(one() for _ in range(20)))
    assert sorted(versions) == list(range(2, 22))
    assert await current_version(engine) == 21
