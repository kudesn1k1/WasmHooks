from typing import Any

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException

PROBLEM_MEDIA_TYPE = "application/problem+json"
_NON_BODY_LOCS = {"query", "path", "header", "cookie"}


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
