from alembic.config import Config


def alembic_config(url: str) -> Config:
    cfg = Config()
    cfg.set_main_option("script_location", "controlplane:migrations")
    cfg.set_main_option("sqlalchemy.url", url)
    return cfg


def main() -> None:
    raise SystemExit("no commands yet")
