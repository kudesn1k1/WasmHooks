"""Command line of the control plane: `controlplane <command>`.

Commands talk to the database directly and are meant for deployment and
development: migrations, the first API key, the OpenAPI export, dev tools.
"""

import argparse
from collections.abc import Sequence

from alembic import command
from alembic.config import Config

from controlplane.settings import Settings


def alembic_config(url: str) -> Config:
    cfg = Config()
    cfg.set_main_option("script_location", "controlplane:migrations")
    cfg.set_main_option("sqlalchemy.url", url)
    return cfg


def _migrate(_: argparse.Namespace) -> None:
    command.upgrade(alembic_config(Settings().database_url), "head")


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="controlplane")
    commands = parser.add_subparsers(dest="command", required=True)

    migrate = commands.add_parser("migrate", help="apply database migrations")
    migrate.set_defaults(run=_migrate)

    return parser


def main(argv: Sequence[str] | None = None) -> None:
    args = _parser().parse_args(argv)
    args.run(args)
