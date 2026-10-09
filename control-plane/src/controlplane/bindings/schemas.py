from pydantic import BaseModel, ConfigDict


class BindingUpdate(BaseModel):
    model_config = ConfigDict(extra="forbid")

    # Switching the active version; a rollback is the previous id.
    active_module_id: str | None = None
    # Added to the frontend mocks: user story 3.2.
    config: dict[str, str] | None = None
