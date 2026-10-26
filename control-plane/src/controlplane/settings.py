from pydantic import Field, SecretStr, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="CP_")

    database_url: str
    internal_token: SecretStr = Field(min_length=16)
    tenant_jwt_secret: SecretStr
    tenant_token_ttl_s: int = Field(3600, gt=0)
    snapshot_poll_interval_s: float = Field(0.5, gt=0)
    s3_endpoint: str = "http://localhost:9000"
    s3_bucket: str = "modules"
    s3_access_key: SecretStr = SecretStr("minioadmin")
    s3_secret_key: SecretStr = SecretStr("minioadmin")
    s3_region: str = "us-east-1"
    dataplane_internal_url: str = "http://localhost:8081"
    max_module_bytes: int = Field(10 * 1024 * 1024, gt=0)

    @field_validator("tenant_jwt_secret")
    @classmethod
    def _secret_long_enough(cls, value: SecretStr) -> SecretStr:
        if len(value.get_secret_value()) < 32:
            raise ValueError("tenant_jwt_secret must be at least 32 characters")
        return value
