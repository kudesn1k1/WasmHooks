import pytest
from pydantic import ValidationError

from controlplane.cli import alembic_config
from controlplane.settings import Settings

GOOD = {
    "database_url": "postgresql+asyncpg://u:p@h/db",
    "internal_token": "a-long-enough-internal-token",
    "tenant_jwt_secret": "x" * 32,
}


def test_url_encoded_password_survives_alembic_config() -> None:
    # Review 1, I3: ConfigParser interpolation broke on '%'.
    url = "postgresql+asyncpg://user:p%40ss%2Fw%25rd@db:5432/cp"
    assert alembic_config(url).get_main_option("sqlalchemy.url") == url


@pytest.mark.parametrize(
    "override",
    [
        {"snapshot_poll_interval_s": 0},
        {"snapshot_poll_interval_s": -1},
        {"tenant_token_ttl_s": 0},
        {"internal_token": ""},
        {"internal_token": "short"},
        {"tenant_jwt_secret": "x" * 31},
    ],
)
def test_settings_reject_values_that_break_the_process(override: dict[str, object]) -> None:
    with pytest.raises(ValidationError):
        Settings(**(GOOD | override))  # type: ignore[arg-type]


def test_settings_accept_good_values() -> None:
    Settings(**GOOD)  # type: ignore[arg-type]
