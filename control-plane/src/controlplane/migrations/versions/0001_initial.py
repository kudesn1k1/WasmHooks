"""Initial control-plane schema.

Revision ID: 0001
Revises:
Create Date: 2026-10-09
"""

from collections.abc import Sequence
from typing import Any

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision: str = "0001"
down_revision: str | None = None
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

UUID = postgresql.UUID(as_uuid=True)
JSONB = postgresql.JSONB()
TS = sa.DateTime(timezone=True)


def _created_at() -> sa.Column[Any]:
    return sa.Column("created_at", TS, nullable=False, server_default=sa.func.now())


def _updated_at() -> sa.Column[Any]:
    return sa.Column("updated_at", TS, nullable=False, server_default=sa.func.now())


def _uuid_pk() -> sa.Column[Any]:
    return sa.Column("id", UUID, primary_key=True, server_default=sa.text("gen_random_uuid()"))


def upgrade() -> None:
    # True only for a JSON object whose values are all strings: the data
    # plane decodes binding config into map[string]string.
    op.execute(
        """
        CREATE FUNCTION jsonb_string_values(doc jsonb) RETURNS boolean
        LANGUAGE plpgsql IMMUTABLE STRICT AS $$
        BEGIN
            IF jsonb_typeof(doc) <> 'object' THEN
                RETURN false;
            END IF;
            RETURN NOT EXISTS (
                SELECT 1 FROM jsonb_each(doc) AS e WHERE jsonb_typeof(e.value) <> 'string'
            );
        END
        $$
        """
    )

    # True only for a one-dimensional array without NULL elements: the
    # snapshot carries these columns as lists of strings.
    op.execute(
        """
        CREATE FUNCTION flat_text_array(arr text[]) RETURNS boolean
        LANGUAGE plpgsql IMMUTABLE STRICT AS $$
        BEGIN
            IF coalesce(array_ndims(arr), 1) <> 1 THEN
                RETURN false;
            END IF;
            RETURN array_position(arr, NULL) IS NULL;
        END
        $$
        """
    )

    op.create_table(
        "config_state",
        sa.Column("id", sa.Integer, primary_key=True, autoincrement=False),
        sa.Column("version", sa.BigInteger, nullable=False),
        sa.CheckConstraint("id = 1", name="single_row"),
        sa.CheckConstraint("version >= 1", name="version_positive"),
    )
    op.create_table(
        "config_changes",
        sa.Column("version", sa.BigInteger, primary_key=True, autoincrement=False),
        sa.Column("kind", sa.Text, nullable=False),
        sa.Column("payload", JSONB, nullable=False),
        _created_at(),
    )
    op.create_table(
        "api_keys",
        sa.Column("id", sa.Text, primary_key=True),
        sa.Column("name", sa.Text, nullable=False, unique=True),
        sa.Column("sha256", sa.Text, nullable=False, unique=True),
        _created_at(),
        sa.Column("revoked_at", TS),
        sa.CheckConstraint("sha256 ~ '^[0-9a-f]{64}$'", name="sha256_format"),
        sa.CheckConstraint("name <> ''", name="name_not_empty"),
    )
    op.create_table(
        "tenants",
        _uuid_pk(),
        sa.Column("external_id", sa.Text, nullable=False, unique=True),
        sa.Column("name", sa.Text, nullable=False),
        sa.Column("rate_limit_rps", sa.Integer, nullable=False, server_default="0"),
        sa.Column("concurrency_limit", sa.Integer, nullable=False, server_default="0"),
        _created_at(),
        _updated_at(),
        sa.CheckConstraint("external_id <> ''", name="external_id_not_empty"),
        sa.CheckConstraint("rate_limit_rps >= 0", name="rate_limit_non_negative"),
        sa.CheckConstraint("concurrency_limit >= 0", name="concurrency_limit_non_negative"),
    )
    op.create_table(
        "hooks",
        _uuid_pk(),
        sa.Column("name", sa.Text, nullable=False, unique=True),
        sa.Column("def_version", sa.BigInteger, nullable=False),
        sa.Column("input_schema", JSONB, nullable=False),
        sa.Column("output_schema", JSONB, nullable=False),
        sa.Column("timeout_ms", sa.BigInteger, nullable=False),
        sa.Column("memory_max_pages", sa.Integer, nullable=False),
        sa.Column("allowed_host_functions", sa.ARRAY(sa.Text), nullable=False, server_default="{}"),
        sa.Column("allowed_effect_types", sa.ARRAY(sa.Text), nullable=False, server_default="{}"),
        sa.Column("sample_input", JSONB, nullable=False),
        _created_at(),
        _updated_at(),
        sa.CheckConstraint(
            r"name ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$' AND length(name) <= 128",
            name="name_format",
        ),
        sa.CheckConstraint("def_version >= 1", name="def_version_positive"),
        sa.CheckConstraint("timeout_ms BETWEEN 1 AND 30000", name="timeout_range"),
        sa.CheckConstraint("memory_max_pages BETWEEN 16 AND 16384", name="memory_range"),
        sa.CheckConstraint("jsonb_typeof(input_schema) = 'object'", name="input_schema_object"),
        sa.CheckConstraint("jsonb_typeof(output_schema) = 'object'", name="output_schema_object"),
        sa.CheckConstraint("jsonb_typeof(sample_input) = 'object'", name="sample_input_object"),
        sa.CheckConstraint(
            "flat_text_array(allowed_host_functions)", name="allowed_host_functions_flat"
        ),
        sa.CheckConstraint(
            "flat_text_array(allowed_effect_types)", name="allowed_effect_types_flat"
        ),
    )
    op.create_table(
        "modules",
        _uuid_pk(),
        sa.Column("tenant_id", UUID, sa.ForeignKey("tenants.id"), nullable=False),
        sa.Column("hook_id", UUID, sa.ForeignKey("hooks.id"), nullable=False),
        sa.Column("content_hash", sa.Text, nullable=False),
        sa.Column("size_bytes", sa.BigInteger, nullable=False),
        sa.Column("status", sa.Text, nullable=False),
        sa.Column("validation_report", JSONB),
        sa.Column("uploaded_at", TS, nullable=False, server_default=sa.func.now()),
        sa.CheckConstraint("content_hash ~ '^sha256:[0-9a-f]{64}$'", name="content_hash_format"),
        sa.CheckConstraint("size_bytes > 0", name="size_positive"),
        sa.CheckConstraint(
            "status IN ('uploaded', 'validating', 'validated', 'rejected', 'archived')",
            name="status_enum",
        ),
        sa.UniqueConstraint("tenant_id", "hook_id", "content_hash"),
        sa.UniqueConstraint("id", "tenant_id", "hook_id"),
    )
    op.create_table(
        "bindings",
        sa.Column("tenant_id", UUID, sa.ForeignKey("tenants.id"), primary_key=True),
        sa.Column("hook_id", UUID, sa.ForeignKey("hooks.id"), primary_key=True),
        sa.Column("active_module_id", UUID),
        sa.Column("config", JSONB, nullable=False, server_default=sa.text("'{}'::jsonb")),
        sa.Column("config_version", sa.BigInteger, nullable=False, server_default="1"),
        _updated_at(),
        sa.ForeignKeyConstraint(
            ["active_module_id", "tenant_id", "hook_id"],
            ["modules.id", "modules.tenant_id", "modules.hook_id"],
        ),
        sa.CheckConstraint("jsonb_string_values(config)", name="config_strings"),
        sa.CheckConstraint("config_version >= 1", name="config_version_positive"),
    )

    op.execute("INSERT INTO config_state (id, version) VALUES (1, 1)")


def downgrade() -> None:
    for table in (
        "bindings",
        "modules",
        "hooks",
        "tenants",
        "api_keys",
        "config_changes",
        "config_state",
    ):
        op.drop_table(table)
    op.execute("DROP FUNCTION jsonb_string_values(jsonb)")
    op.execute("DROP FUNCTION flat_text_array(text[])")
