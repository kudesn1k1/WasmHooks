FROM python:3.13-slim
COPY --from=ghcr.io/astral-sh/uv:0.12.24 /uv /usr/local/bin/uv
WORKDIR /app
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy PATH="/app/.venv/bin:$PATH"
COPY pyproject.toml uv.lock ./
RUN uv sync --locked --no-dev --no-install-project
COPY alembic.ini ./
COPY src ./src
RUN uv sync --locked --no-dev
EXPOSE 8000
# The data plane always holds a long-poll open; without a bounded graceful
# shutdown every stop would wait for it and end in SIGKILL.
CMD ["uvicorn", "--factory", "controlplane.app:create_app_from_env", "--host", "0.0.0.0", "--port", "8000", "--timeout-graceful-shutdown", "3"]
