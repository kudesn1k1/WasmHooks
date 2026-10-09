from typing import Any

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.openapi.utils import get_openapi
from fastapi.responses import JSONResponse
from pydantic import BaseModel
from starlette.exceptions import HTTPException as StarletteHTTPException

PROBLEM_MEDIA_TYPE = "application/problem+json"
_NON_BODY_LOCS = {"query", "path", "header", "cookie"}


class Problem(BaseModel):
    """RFC 9457 problem details: the body of every error of this API."""

    type: str = "about:blank"
    title: str
    status: int
    detail: str | None = None


def _problem_response_doc(description: str) -> dict[str, Any]:
    return {
        "description": description,
        "content": {PROBLEM_MEDIA_TYPE: {"schema": {"$ref": "#/components/schemas/Problem"}}},
    }


# Documented on every endpoint. FastAPI would otherwise describe 422 with its
# own HTTPValidationError shape, which this API never returns.
PROBLEM_RESPONSES: dict[int | str, dict[str, Any]] = {
    400: _problem_response_doc("Некорректные параметры запроса"),
    401: _problem_response_doc("Не авторизован"),
    422: _problem_response_doc("Некорректное тело запроса"),
}


class ProblemError(Exception):
    def __init__(self, status: int, title: str, detail: str | None = None) -> None:
        super().__init__(title)
        self.status = status
        self.title = title
        self.detail = detail


def problem_response(status: int, title: str, detail: str | None = None) -> JSONResponse:
    body: dict[str, Any] = {"type": "about:blank", "title": title, "status": status}
    if detail is not None:
        body["detail"] = detail
    return JSONResponse(body, status_code=status, media_type=PROBLEM_MEDIA_TYPE)


def install_error_handlers(app: FastAPI) -> None:
    @app.exception_handler(ProblemError)
    async def _problem(_: Request, exc: ProblemError) -> JSONResponse:
        return problem_response(exc.status, exc.title, exc.detail)

    @app.exception_handler(RequestValidationError)
    async def _validation(_: Request, exc: RequestValidationError) -> JSONResponse:
        errors = exc.errors()
        all_non_body = bool(errors) and all(e["loc"][0] in _NON_BODY_LOCS for e in errors)
        status = 400 if all_non_body else 422
        detail: str | None = None
        if errors:
            first = errors[0]
            loc = ".".join(str(part) for part in first["loc"])
            detail = f"{loc}: {first['msg']}"
        return problem_response(status, "Некорректный запрос", detail)

    @app.exception_handler(StarletteHTTPException)
    async def _http(_: Request, exc: StarletteHTTPException) -> JSONResponse:
        return problem_response(exc.status_code, str(exc.detail))

    @app.exception_handler(NotImplementedError)
    async def _not_implemented(_: Request, exc: NotImplementedError) -> JSONResponse:
        return problem_response(501, "Ещё не реализовано", str(exc))


def install_problem_schema(app: FastAPI) -> None:
    """Adds the Problem schema to the generated OpenAPI document: the error
    responses in PROBLEM_RESPONSES reference it, and no route returns it as a
    response model, so FastAPI would not register it on its own."""

    def openapi() -> dict[str, Any]:
        if app.openapi_schema is None:
            schema = get_openapi(
                title=app.title,
                version=app.version,
                description=app.description,
                routes=app.routes,
            )
            schema.setdefault("components", {}).setdefault("schemas", {})["Problem"] = (
                Problem.model_json_schema()
            )
            app.openapi_schema = schema
        return app.openapi_schema

    app.openapi = openapi  # type: ignore[method-assign]
