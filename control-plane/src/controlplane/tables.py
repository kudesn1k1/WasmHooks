"""Database schema of the control plane, as SQLAlchemy Core tables.

The constraints here mirror the data plane's snapshot validation
(dataplane/internal/config: Parse): a snapshot built from these tables can
never be rejected by the data plane, because a rejected snapshot would stop
config propagation for good (decision D22).
"""

from sqlalchemy import (
    ARRAY,
    BigInteger,
    CheckConstraint,
    Column,
    DateTime,
    ForeignKey,
    ForeignKeyConstraint,
    Integer,
    MetaData,
    Table,
    Text,
    UniqueConstraint,
    func,
    text,
)
from sqlalchemy.dialects.postgresql import JSONB, UUID

metadata = MetaData(
    naming_convention={
        "pk": "pk_%(table_name)s",
        "fk": "fk_%(table_name)s_%(column_0_N_name)s",
        "uq": "uq_%(table_name)s_%(column_0_N_name)s",
        "ck": "ck_%(table_name)s_%(constraint_name)s",
    }
)

# Single row holding the snapshot version. Writers increment it under the
# row lock, so versions are handed out in commit order (decision D20).
config_state = Table(
    "config_state",
    metadata,
    Column("id", Integer, primary_key=True, autoincrement=False),
    Column("version", BigInteger, nullable=False),
    CheckConstraint("id = 1", name="single_row"),
    CheckConstraint("version >= 1", name="version_positive"),
)

# Audit of snapshot-visible changes; version comes from config_state.
config_changes = Table(
    "config_changes",
    metadata,
    Column("version", BigInteger, primary_key=True, autoincrement=False),
    Column("kind", Text, nullable=False),
    Column("payload", JSONB, nullable=False),
    Column("created_at", DateTime(timezone=True), nullable=False, server_default=func.now()),
)

api_keys = Table(
    "api_keys",
    metadata,
    Column("id", Text, primary_key=True),
    Column("name", Text, nullable=False, unique=True),
    Column("sha256", Text, nullable=False, unique=True),
    Column("created_at", DateTime(timezone=True), nullable=False, server_default=func.now()),
    Column("revoked_at", DateTime(timezone=True)),
    CheckConstraint("sha256 ~ '^[0-9a-f]{64}$'", name="sha256_format"),
    CheckConstraint("name <> ''", name="name_not_empty"),
)

tenants = Table(
    "tenants",
    metadata,
    Column("id", UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")),
    Column("external_id", Text, nullable=False, unique=True),
    Column("name", Text, nullable=False),
    Column("rate_limit_rps", Integer, nullable=False, server_default="0"),
    Column("concurrency_limit", Integer, nullable=False, server_default="0"),
    Column("created_at", DateTime(timezone=True), nullable=False, server_default=func.now()),
    Column("updated_at", DateTime(timezone=True), nullable=False, server_default=func.now()),
    CheckConstraint("external_id <> ''", name="external_id_not_empty"),
    CheckConstraint("rate_limit_rps >= 0", name="rate_limit_non_negative"),
    CheckConstraint("concurrency_limit >= 0", name="concurrency_limit_non_negative"),
)

hooks = Table(
    "hooks",
    metadata,
    Column("id", UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")),
    Column("name", Text, nullable=False, unique=True),
    Column("def_version", BigInteger, nullable=False),
    Column("input_schema", JSONB, nullable=False),
    Column("output_schema", JSONB, nullable=False),
    Column("timeout_ms", BigInteger, nullable=False),
    Column("memory_max_pages", Integer, nullable=False),
    Column("allowed_host_functions", ARRAY(Text), nullable=False, server_default="{}"),
    Column("allowed_effect_types", ARRAY(Text), nullable=False, server_default="{}"),
    Column("sample_input", JSONB, nullable=False),
    Column("created_at", DateTime(timezone=True), nullable=False, server_default=func.now()),
    Column("updated_at", DateTime(timezone=True), nullable=False, server_default=func.now()),
    CheckConstraint(
        r"name ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$' AND length(name) <= 128",
        name="name_format",
    ),
    CheckConstraint("def_version >= 1", name="def_version_positive"),
    CheckConstraint("timeout_ms BETWEEN 1 AND 30000", name="timeout_range"),
    CheckConstraint("memory_max_pages BETWEEN 16 AND 16384", name="memory_range"),
    CheckConstraint("jsonb_typeof(input_schema) = 'object'", name="input_schema_object"),
    CheckConstraint("jsonb_typeof(output_schema) = 'object'", name="output_schema_object"),
    CheckConstraint("jsonb_typeof(sample_input) = 'object'", name="sample_input_object"),
)

modules = Table(
    "modules",
    metadata,
    Column("id", UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")),
    Column("tenant_id", UUID(as_uuid=True), ForeignKey("tenants.id"), nullable=False),
    Column("hook_id", UUID(as_uuid=True), ForeignKey("hooks.id"), nullable=False),
    Column("content_hash", Text, nullable=False),
    Column("size_bytes", BigInteger, nullable=False),
    Column("status", Text, nullable=False),
    Column("validation_report", JSONB),
    Column("uploaded_at", DateTime(timezone=True), nullable=False, server_default=func.now()),
    CheckConstraint("content_hash ~ '^sha256:[0-9a-f]{64}$'", name="content_hash_format"),
    CheckConstraint("size_bytes > 0", name="size_positive"),
    CheckConstraint(
        "status IN ('uploaded', 'validating', 'validated', 'rejected', 'archived')",
        name="status_enum",
    ),
    UniqueConstraint("tenant_id", "hook_id", "content_hash"),
    # Target of the bindings FK below: lets the database itself refuse to
    # activate another tenant's module or a module of another hook.
    UniqueConstraint("id", "tenant_id", "hook_id"),
)

bindings = Table(
    "bindings",
    metadata,
    Column("tenant_id", UUID(as_uuid=True), ForeignKey("tenants.id"), primary_key=True),
    Column("hook_id", UUID(as_uuid=True), ForeignKey("hooks.id"), primary_key=True),
    Column("active_module_id", UUID(as_uuid=True)),
    Column("config", JSONB, nullable=False, server_default=text("'{}'::jsonb")),
    Column("config_version", BigInteger, nullable=False, server_default="1"),
    Column("updated_at", DateTime(timezone=True), nullable=False, server_default=func.now()),
    ForeignKeyConstraint(
        ["active_module_id", "tenant_id", "hook_id"],
        ["modules.id", "modules.tenant_id", "modules.hook_id"],
    ),
    # The data plane decodes config into map[string]string; a non-string
    # value would make the whole snapshot unparsable. jsonb_string_values is
    # created by the initial migration: true only for an object whose values
    # are all strings.
    CheckConstraint("jsonb_string_values(config)", name="config_strings"),
    CheckConstraint("config_version >= 1", name="config_version_positive"),
)
