"""Checks a hook definition must pass before it is stored.

The data plane compiles these schemas with no network access, so a schema
must be valid draft 2020-12 and may only reference itself ("#..."). The
sample input must match the input schema: it is the payload every uploaded
script is tried on.
"""

from collections.abc import Iterator
from typing import Any

from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError

from controlplane.errors import ProblemError
from controlplane.hooks.schemas import HookSpec

INVALID_HOOK = "Невалидное определение хука"


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
        try:
            Draft202012Validator.check_schema(schema)
        except SchemaError as exc:
            raise ProblemError(422, INVALID_HOOK, f"{field}: {exc.message}") from exc
        for ref in _refs(schema):
            if not ref.startswith("#"):
                raise ProblemError(
                    422, INVALID_HOOK, f"{field}: only local $ref ('#...') is allowed, got {ref!r}"
                )

    error = next(Draft202012Validator(spec.input_schema).iter_errors(spec.sample_input), None)
    if error is not None:
        path = "/" + "/".join(str(p) for p in error.absolute_path)
        raise ProblemError(422, INVALID_HOOK, f"sample_input: {path}: {error.message}")


def _refs(node: Any) -> Iterator[str]:
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "$ref" and isinstance(value, str):
                yield value
            else:
                yield from _refs(value)
    elif isinstance(node, list):
        for item in node:
            yield from _refs(item)
