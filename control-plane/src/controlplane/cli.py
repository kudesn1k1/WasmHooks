"""Command line of the control plane: `controlplane <command>`.

Commands talk to the database directly and are meant for deployment and
development: migrations, the first API key, the OpenAPI export, dev tools.
"""

import argparse
import asyncio
import sys
from collections.abc import Sequence

from alembic import command
from alembic.config import Config

from controlplane.auth.keys import create_api_key
from controlplane.db import create_engine
from controlplane.settings import Settings


def alembic_config(url: str) -> Config:
    cfg = Config()
    cfg.set_main_option("script_location", "controlplane:migrations")
    cfg.set_main_option("sqlalchemy.url", url)
    return cfg


def _migrate(_: argparse.Namespace) -> None:
    command.upgrade(alembic_config(Settings().database_url), "head")


async def _create_api_key(name: str) -> None:
    engine = create_engine(Settings().database_url)
    try:
        async with engine.begin() as conn:
            key = await create_api_key(conn, name)
    finally:
        await engine.dispose()
    # The token goes alone to stdout so scripts can capture it; it is never
    # shown again.
    print(key.token)
    print(f"created key {key.id} ({key.name})", file=sys.stderr)


def _apikey_create(args: argparse.Namespace) -> None:
    asyncio.run(_create_api_key(args.name))


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="controlplane")
    commands = parser.add_subparsers(dest="command", required=True)

    migrate = commands.add_parser("migrate", help="apply database migrations")
    migrate.set_defaults(run=_migrate)

    apikey = commands.add_parser("apikey", help="installation API keys")
    apikey_commands = apikey.add_subparsers(dest="apikey_command", required=True)
    create = apikey_commands.add_parser("create", help="create a key and print it once")
    create.add_argument("--name", required=True)
    create.set_defaults(run=_apikey_create)

    return parser


def main(argv: Sequence[str] | None = None) -> None:
    args = _parser().parse_args(argv)
    args.run(args)
