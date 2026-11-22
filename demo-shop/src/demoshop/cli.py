"""Command line of the demo shop: `demoshop <command>`."""

import argparse
import sys
from collections.abc import Sequence

import yaml

from demoshop.app import create_app
from demoshop.settings import Settings


def _openapi(_: argparse.Namespace) -> None:
    # Building the app connects to nothing; the settings only need to be valid.
    app = create_app(Settings(api_key="openapi-placeholder"))
    text = yaml.safe_dump(app.openapi(), sort_keys=False, allow_unicode=True)
    # UTF-8 and LF on every OS: the file is compared byte for byte in CI.
    sys.stdout.buffer.write(text.encode("utf-8"))


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="demoshop")
    commands = parser.add_subparsers(dest="command", required=True)
    openapi = commands.add_parser(
        "openapi", help="print the storefront API contract (api/demo-shop.openapi.yaml)"
    )
    openapi.set_defaults(run=_openapi)
    return parser


def main(argv: Sequence[str] | None = None) -> None:
    args = _parser().parse_args(argv)
    args.run(args)
