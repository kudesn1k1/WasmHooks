"""Checks a hook definition must pass before it is stored.

The data plane compiles these schemas in Go (santhosh-tekuri/jsonschema,
RE2 regular expressions) with no network access. A schema that Python's
jsonschema accepts but Go cannot compile would make every call of the hook
fail, so the rules here are the stricter of the two:

- draft 2020-12 only (`$schema` absent or the 2020-12 URI);
- `$ref` only as a JSON pointer into the same schema ("#" or "#/..."), and
  the target must exist; no `$id`, `$anchor` or dynamic references;
- `pattern` and `patternProperties` keys in RE2 syntax: no lookaround, no
  backreferences, no atomic groups or possessive quantifiers.

The sample input must match the input schema: it is the payload every
uploaded script is tried on.
"""

import re
from collections.abc import Iterator
from typing import Any
from urllib.parse import unquote

from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError
from referencing.exceptions import Unresolvable

from controlplane.errors import ProblemError
from controlplane.hooks.schemas import HookSpec

INVALID_HOOK = "Невалидное определение хука"

DRAFT_2020_12 = "https://json-schema.org/draft/2020-12/schema"
_UNSUPPORTED_KEYWORDS = ("$id", "$anchor", "$dynamicRef", "$dynamicAnchor", "$recursiveRef")
# Values under these keywords are data, not schemas.
_DATA_KEYWORDS = {"const", "enum", "examples", "default"}
# Python re syntax that RE2 (Go) does not support.
_NOT_RE2 = re.compile(r"\(\?=|\(\?!|\(\?<=|\(\?<!|\(\?>|\(\?P=|\\[1-9]|\\k<|[*+?}]\+")


def normalized(spec: HookSpec) -> HookSpec:
    """Allowed lists are sets: sorted, without duplicates, so equal
    definitions compare equal and snapshots are deterministic."""
    return spec.model_copy(
        update={
            "allowed_host_functions": sorted(set(spec.allowed_host_functions)),
            "allowed_effect_types": sorted(set(spec.allowed_effect_types)),
        }
    )


def check_hook_spec(spec: HookSpec) -> None:
    for field, schema in (
        ("input_schema", spec.input_schema),
        ("output_schema", spec.output_schema),
    ):
        problem = _schema_problem(schema)
        if problem is not None:
            raise ProblemError(422, INVALID_HOOK, f"{field}: {problem}")

    try:
        error = next(Draft202012Validator(spec.input_schema).iter_errors(spec.sample_input), None)
    except Unresolvable as exc:  # pragma: no cover - refs are resolved above
        raise ProblemError(422, INVALID_HOOK, f"input_schema: {exc}") from exc
    if error is not None:
        path = "/" + "/".join(str(p) for p in error.absolute_path)
        raise ProblemError(422, INVALID_HOOK, f"sample_input: {path}: {error.message}")


def _schema_problem(schema: dict[str, Any]) -> str | None:
    dialect = schema.get("$schema", DRAFT_2020_12)
    if dialect != DRAFT_2020_12:
        return f"only draft 2020-12 is supported, $schema must be {DRAFT_2020_12!r}"
    try:
        Draft202012Validator.check_schema(schema)
    except SchemaError as exc:
        return exc.message
    for key, value in _walk(schema):
        if key in _UNSUPPORTED_KEYWORDS:
            return f"{key} is not supported"
        if key == "$ref":
            if not isinstance(value, str) or not (value == "#" or value.startswith("#/")):
                return f"only local $ref ('#' or '#/...') is allowed, got {value!r}"
            if not _pointer_exists(schema, value):
                return f"$ref {value!r} points to nothing"
        if key == "pattern" and isinstance(value, str):
            problem = _regex_problem(value)
            if problem:
                return problem
        if key == "patternProperties" and isinstance(value, dict):
            for pattern in value:
                problem = _regex_problem(pattern)
                if problem:
                    return problem
    return None


def _regex_problem(pattern: str) -> str | None:
    try:
        re.compile(pattern)
    except re.error as exc:
        return f"pattern {pattern!r} is not a valid regular expression: {exc}"
    if _NOT_RE2.search(pattern):
        return (
            f"pattern {pattern!r} uses syntax the data plane (RE2) does not support: "
            "lookaround, backreferences, atomic groups or possessive quantifiers"
        )
    return None


def _walk(node: Any) -> Iterator[tuple[str, Any]]:
    """Yields every (key, value) of every object in a schema, skipping data
    under const/enum/examples/default."""
    if isinstance(node, dict):
        for key, value in node.items():
            yield key, value
            if key not in _DATA_KEYWORDS:
                yield from _walk(value)
    elif isinstance(node, list):
        for item in node:
            yield from _walk(item)


def _pointer_exists(schema: dict[str, Any], ref: str) -> bool:
    node: Any = schema
    for raw in ref[2:].split("/") if ref != "#" else []:
        token = unquote(raw).replace("~1", "/").replace("~0", "~")
        if isinstance(node, dict) and token in node:
            node = node[token]
        elif isinstance(node, list) and token.isdigit() and int(token) < len(node):
            node = node[int(token)]
        else:
            return False
    return True
