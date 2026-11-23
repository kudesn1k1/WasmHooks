from pydantic import Field, SecretStr
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="DS_")

    # Installation API key of the marketplace: authenticates Invoke on the data
    # plane and the operator API (tenants, console sessions) on the control plane.
    api_key: SecretStr = Field(min_length=1)
    dataplane_url: str = "http://localhost:8080"
    control_plane_url: str = "http://localhost:8000"
    discount_hook: str = "checkout.discount"
    # The checkout's whole budget for the discount hook, network included.
    discount_deadline_ms: int = Field(200, ge=1, le=30000)
    control_plane_timeout_s: float = Field(5.0, gt=0)
