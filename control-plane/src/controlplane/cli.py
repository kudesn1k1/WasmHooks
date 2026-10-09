"""Command line of the control plane: `controlplane <command>`.

Commands talk to the database directly and are meant for deployment and
development: migrations, the first API key, the OpenAPI export, dev tools.
"""

import argparse
import asyncio
import sys
from collections.abc import Sequence
from pathlib import Path

import yaml
from alembic import command
from alembic.config import Config

from controlplane.app import create_app
from controlplane.auth.keys import create_api_key
from controlplane.db import create_engine
from controlplane.devtools.seed import seed_demo
from controlplane.devtools.validate_module import validate_module
from controlplane.dpclient.client import DataPlaneError, DataPlaneUnavailable
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


def _openapi(_: argparse.Namespace) -> None:
    # Building the app connects to nothing; the settings only need to be valid.
    app = create_app(
        Settings(
            database_url="postgresql+asyncpg://openapi@localhost/none",
            internal_token="openapi",  # noqa: S106 - placeholder, nothing is served
            tenant_jwt_secret="openapi-" + "x" * 32,
        )
    )
    text = yaml.safe_dump(app.openapi(), sort_keys=False, allow_unicode=True)
    # UTF-8 and LF on every OS: the file is compared byte for byte in CI.
    sys.stdout.buffer.write(text.encode("utf-8"))


def _validate_module(args: argparse.Namespace) -> None:
    try:
        report = asyncio.run(validate_module(Settings(), Path(args.file), args.hook))
    except (DataPlaneUnavailable, DataPlaneError) as exc:
        print(f"validation request failed: {exc}", file=sys.stderr)
        raise SystemExit(2) from exc
    # Exit code 0 whenever the data plane answered, whatever the verdict.
    print(report.model_dump_json(indent=2))


def _seed_demo(args: argparse.Namespace) -> None:
    print("dev-only: bypasses the upload flow (T3-T5)", file=sys.stderr)
    module_hash = asyncio.run(
        seed_demo(Settings(), Path(args.wasm), tenant=args.tenant, hook_name=args.hook)
    )
    print(module_hash)


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

    openapi = commands.add_parser(
        "openapi", help="print the console API contract (api/control-plane.openapi.yaml)"
    )
    openapi.set_defaults(run=_openapi)

    validate = commands.add_parser(
        "validate-module", help="dev: put a wasm file into storage and validate it"
    )
    validate.add_argument("file")
    validate.add_argument("--hook", required=True)
    validate.set_defaults(run=_validate_module)

    seed = commands.add_parser(
        "seed-demo", help="dev-only: tenant, validated module and binding, bypassing the API"
    )
    seed.add_argument("--wasm", default="/fixtures/discount.wasm")
    seed.add_argument("--tenant", default="merchant-a")
    seed.add_argument("--hook", default="checkout.discount")
    seed.set_defaults(run=_seed_demo)

    return parser


def main(argv: Sequence[str] | None = None) -> None:
    args = _parser().parse_args(argv)
    args.run(args)
