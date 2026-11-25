"""Error envelope and request (correlation) IDs (BL-20).

Every error response has the same shape as bot-engine's:

    {"error": "<code>", "detail": "<safe message>", "request_id": "<id>"}

``detail`` is a message chosen by this service. Upstream errors (Alpaca responses,
stack traces) are logged with the request ID and never returned. The request ID is
the caller's ``X-Request-ID`` when it is log-safe, otherwise a new UUID; it is echoed
in the response header and bound to every log line of the request.
"""

from __future__ import annotations

import re
import uuid
from typing import Any

import structlog
from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException
from starlette.types import ASGIApp, Message, Receive, Scope, Send

REQUEST_ID_HEADER = "X-Request-ID"
_SAFE_ID = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
_MAX_DETAIL = 300

logger = structlog.get_logger(__name__)

# Codes used when a route raises a plain HTTPException. Clients branch on codes.
_DEFAULT_CODES = {
    400: "invalid_request",
    401: "unauthorized",
    403: "forbidden",
    404: "not_found",
    405: "method_not_allowed",
    422: "invalid_request",
    429: "rate_limited",
    503: "service_unavailable",
}


class ApiError(Exception):
    """An error with a stable code and a message that is safe to return."""

    def __init__(
        self,
        status_code: int,
        code: str,
        detail: str,
        *,
        headers: dict[str, str] | None = None,
        **extra: str,
    ) -> None:
        super().__init__(detail)
        self.status_code = status_code
        self.code = code
        self.detail = detail
        self.headers = headers or {}
        self.extra = extra


def safe_request_id(raw: str | None) -> str:
    return raw if raw and _SAFE_ID.match(raw) else str(uuid.uuid4())


def request_id_of(request: Request) -> str:
    return str(getattr(request.state, "request_id", ""))


class RequestIDMiddleware:
    """Pure ASGI middleware: assigns the ID, binds it to logs, echoes the header."""

    def __init__(self, app: ASGIApp) -> None:
        self.app = app

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        raw = next(
            (v.decode("latin-1") for k, v in scope["headers"] if k == b"x-request-id"),
            None,
        )
        rid = safe_request_id(raw)
        scope.setdefault("state", {})["request_id"] = rid

        async def send_with_id(message: Message) -> None:
            if message["type"] == "http.response.start":
                headers = list(message.get("headers", []))
                if not any(k.lower() == b"x-request-id" for k, _ in headers):
                    headers.append((b"x-request-id", rid.encode("latin-1")))
                message["headers"] = headers
            await send(message)

        with structlog.contextvars.bound_contextvars(request_id=rid):
            await self.app(scope, receive, send_with_id)


def _response(
    request: Request,
    status_code: int,
    code: str,
    detail: str,
    headers: dict[str, str] | None = None,
    **extra: Any,
) -> JSONResponse:
    rid = request_id_of(request)
    body: dict[str, Any] = {"error": code, "detail": detail[:_MAX_DETAIL], "request_id": rid}
    body.update(extra)
    # Set the header here too: the 500 handler runs outside RequestIDMiddleware.
    return JSONResponse(body, status_code=status_code, headers={**(headers or {}), REQUEST_ID_HEADER: rid})


async def _api_error(request: Request, exc: Exception) -> JSONResponse:
    assert isinstance(exc, ApiError)
    return _response(request, exc.status_code, exc.code, exc.detail, exc.headers, **exc.extra)


async def _http_error(request: Request, exc: Exception) -> JSONResponse:
    assert isinstance(exc, StarletteHTTPException)
    code = _DEFAULT_CODES.get(exc.status_code, "http_error")
    detail = exc.detail if isinstance(exc.detail, str) else "request failed"
    return _response(request, exc.status_code, code, detail, dict(exc.headers or {}))


async def _validation_error(request: Request, exc: Exception) -> JSONResponse:
    assert isinstance(exc, RequestValidationError)
    errors = exc.errors()
    if errors:
        first = errors[0]
        loc = ".".join(str(p) for p in first.get("loc", ()) if p != "body")
        detail = f"{loc}: {first.get('msg', 'invalid value')}" if loc else str(first.get("msg"))
    else:
        detail = "invalid request"
    return _response(request, 422, "invalid_request", detail)


async def _unhandled(request: Request, exc: Exception) -> JSONResponse:
    logger.exception("http.unhandled_error", path=request.url.path, request_id=request_id_of(request))
    return _response(request, 500, "internal_error", "internal error")


def install(app: FastAPI) -> None:
    app.add_middleware(RequestIDMiddleware)
    app.add_exception_handler(ApiError, _api_error)
    app.add_exception_handler(StarletteHTTPException, _http_error)
    app.add_exception_handler(RequestValidationError, _validation_error)
    app.add_exception_handler(Exception, _unhandled)
