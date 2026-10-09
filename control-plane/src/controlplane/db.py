from sqlalchemy.ext.asyncio import AsyncEngine, create_async_engine


def create_engine(url: str) -> AsyncEngine:
    # Bounded waits: a database that stops answering must not stall the
    # version watcher or a request until TCP gives up.
    return create_async_engine(
        url, pool_pre_ping=True, connect_args={"timeout": 5, "command_timeout": 15}
    )
