"""BL-26: order-intent tools. The gateway proposes intents; it can never approve them."""

from __future__ import annotations

import io
import json
import re
from typing import Any

import pytest
from mcp import Client
from tests.conftest import INTENT_EXAMPLE, INTENT_LIST_EXAMPLE, FakeServices, audit_lines

pytestmark = pytest.mark.anyio

ENGINE = "127.0.0.1:8080"
INTENTS = "/api/v1/order-intents"
INTENT_ID = INTENT_EXAMPLE["id"]
KEY_RE = re.compile(r"^[A-Za-z0-9._:-]{8,128}$")


def text(result: Any) -> str:
    return " ".join(getattr(c, "text", "") for c in result.content)


async def test_no_tool_can_approve_reject_or_cancel(client: Client) -> None:
    tools = (await client.list_tools()).tools
    for t in tools:
        for word in ("approve", "reject", "cancel", "kill"):
            assert word not in t.name
    by_name = {t.name: t for t in tools}
    create = by_name["create_order_intent"]
    assert create.annotations is not None
    assert (
        create.annotations.read_only_hint is False and create.annotations.destructive_hint is False
    )
    desc = (create.description or "").lower()
    for phrase in ("propose", "executes nothing", "human", "approve", "paper trading only"):
        assert phrase in desc
    assert set(create.input_schema["properties"]) == {"symbol", "strategy", "idempotency_key"}
    assert "side" not in json.dumps(create.input_schema) and "qty" not in json.dumps(
        create.input_schema
    )


async def test_create_generates_key_and_returns_intent(
    client: Client, services: FakeServices, audit_stream: io.StringIO
) -> None:
    r = await client.call_tool("create_order_intent", {"symbol": " aapl ", "strategy": "RSI"})
    assert not r.is_error, text(r)
    [req] = services.requests
    assert req.method == "POST" and req.url.path == INTENTS
    assert json.loads(req.read()) == {"symbol": "AAPL", "strategy": "rsi"}
    key = req.headers["idempotency-key"]
    assert KEY_RE.match(key)
    out = r.structured_content
    assert out is not None
    assert out["intent"] == INTENT_EXAMPLE
    assert out["idempotency_key"] == key
    assert out["idempotent_replayed"] is False
    assert out["requires_human_approval"] is True and out["executed"] is False
    assert "human must approve" in out["note"]
    [line] = audit_lines(audit_stream)
    assert line["tool"] == "create_order_intent" and line["outcome"] == "ok"
    assert (
        line["args"]["idempotency_key"] == key and line["args"]["idempotency_key_generated"] is True
    )
    assert req.headers["x-request-id"] == line["request_id"]


async def test_create_passes_supplied_key_and_reports_replay(
    client: Client, services: FakeServices
) -> None:
    services.set(
        ENGINE, "POST", INTENTS, 201, INTENT_EXAMPLE, headers={"Idempotent-Replayed": "true"}
    )
    key = "agent-run:42_retry.1"
    r = await client.call_tool(
        "create_order_intent", {"symbol": "AAPL", "strategy": "rsi", "idempotency_key": key}
    )
    assert not r.is_error, text(r)
    assert services.requests[0].headers["idempotency-key"] == key
    out = r.structured_content
    assert out is not None
    assert out["idempotency_key"] == key and out["idempotent_replayed"] is True
    assert out["intent"] == INTENT_EXAMPLE


async def test_generated_keys_are_unique(client: Client, services: FakeServices) -> None:
    for _ in range(2):
        await client.call_tool("create_order_intent", {"symbol": "AAPL", "strategy": "rsi"})
    keys = {r.headers["idempotency-key"] for r in services.requests}
    assert len(keys) == 2


async def test_create_drops_unexpected_fields(client: Client, services: FakeServices) -> None:
    services.set(ENGINE, "POST", INTENTS, 201, {**INTENT_EXAMPLE, "account_id": "SHOULD-NOT-LEAK"})
    r = await client.call_tool("create_order_intent", {"symbol": "AAPL", "strategy": "rsi"})
    assert not r.is_error and "SHOULD-NOT-LEAK" not in text(r)


@pytest.mark.parametrize(
    ("tool", "args"),
    [
        ("create_order_intent", {"symbol": "AAPL", "strategy": "rsi", "idempotency_key": "short"}),
        (
            "create_order_intent",
            {"symbol": "AAPL", "strategy": "rsi", "idempotency_key": "has space in it"},
        ),
        (
            "create_order_intent",
            {"symbol": "AAPL", "strategy": "rsi", "idempotency_key": "bad/key/../x"},
        ),
        (
            "create_order_intent",
            {"symbol": "AAPL", "strategy": "rsi", "idempotency_key": "k" * 129},
        ),
        ("create_order_intent", {"symbol": "AAPL1", "strategy": "rsi"}),
        ("create_order_intent", {"symbol": "AAPL", "strategy": "../approve"}),
        ("create_order_intent", {"symbol": "AAPL"}),
        ("get_order_intent", {"intent_id": "not-a-uuid"}),
        ("get_order_intent", {"intent_id": "7d0c1b2a-3e4f-4a5b-8c6d-7e8f9a0b1c2d/approve"}),
        ("get_order_intent", {"intent_id": "../kill-switch"}),
        ("get_order_intent", {}),
        ("list_order_intents", {"status": "pending"}),
        ("list_order_intents", {"status": "PENDING_APPROVAL"}),
        ("list_order_intents", {"limit": 0}),
        ("list_order_intents", {"limit": 201}),
    ],
)
async def test_invalid_input_is_rejected_without_a_request(
    client: Client, services: FakeServices, tool: str, args: dict[str, Any]
) -> None:
    r = await client.call_tool(tool, args)
    assert r.is_error
    assert services.requests == []


async def test_invalid_key_is_audited_as_invalid_input(
    client: Client, services: FakeServices, audit_stream: io.StringIO
) -> None:
    r = await client.call_tool(
        "create_order_intent", {"symbol": "AAPL", "strategy": "rsi", "idempotency_key": "short"}
    )
    assert r.is_error and "invalid_input" in text(r) and "idempotency_key" in text(r)
    assert services.requests == []
    assert audit_lines(audit_stream)[-1]["error_code"] == "invalid_input"


@pytest.mark.parametrize(
    ("status", "body", "expected"),
    [
        (
            409,
            {"error": "kill_switch_engaged", "message": "kill switch on", "request_id": "eng-1"},
            [
                "conflict:",
                "kill switch is engaged",
                "error=kill_switch_engaged",
                "request_id=eng-1",
            ],
        ),
        (
            409,
            {
                "error": "intent_exists",
                "message": "intent 8e1d2c3b-4f5a-4b6c-9d7e-8f9a0b1c2d3e already exists PKSECRETLEAK",
                "request_id": "eng-2",
            },
            [
                "conflict:",
                "error=intent_exists",
                "8e1d2c3b-4f5a-4b6c-9d7e-8f9a0b1c2d3e",
                "request_id=eng-2",
            ],
        ),
        (
            422,
            {"error": "idempotency_key_reused", "message": "PKSECRETLEAK", "request_id": "eng-3"},
            ["invalid_input:", "error=idempotency_key_reused", "new key", "request_id=eng-3"],
        ),
        (
            400,
            {"error": "idempotency_key_required", "message": "x", "request_id": "eng-4"},
            ["invalid_input:", "error=idempotency_key_required"],
        ),
        (
            429,
            {"error": "rate_limited", "message": "PKSECRETLEAK 10/h", "request_id": "eng-5"},
            ["upstream_rate_limited:", "error=rate_limited", "request_id=eng-5"],
        ),
        (
            403,
            {
                "error": "insufficient_scope",
                "message": "no",
                "required_scope": "intent:create",
                "request_id": "eng-6",
            },
            [
                "forbidden:",
                "requires intent:create",
                "error=insufficient_scope",
                "request_id=eng-6",
            ],
        ),
        (
            502,
            {"error": "signal_unavailable", "message": "PKSECRETLEAK", "request_id": "eng-7"},
            ["signal_unavailable:", "error=signal_unavailable", "retry with idempotency_key="],
        ),
        (
            409,
            {
                "error": "Weird Code PKSECRETLEAK",
                "message": "PKSECRETLEAK",
                "request_id": "bad id with spaces",
            },
            ["conflict:", "request_id=test-agent-"],
        ),
    ],
)
async def test_create_errors_are_mapped_and_sanitized(
    client: Client,
    services: FakeServices,
    audit_stream: io.StringIO,
    status: int,
    body: Any,
    expected: list[str],
) -> None:
    services.set(ENGINE, "POST", INTENTS, status, body)
    r = await client.call_tool("create_order_intent", {"symbol": "AAPL", "strategy": "rsi"})
    assert r.is_error
    msg = text(r)
    for part in expected:
        assert part in msg
    assert "PKSECRETLEAK" not in msg and "Weird" not in msg
    assert "PKSECRETLEAK" not in audit_stream.getvalue()


async def test_create_timeout_suggests_retry_with_same_key(
    client: Client, services: FakeServices
) -> None:
    import httpx

    services.raise_exc = httpx.ReadTimeout("slow")
    r = await client.call_tool(
        "create_order_intent",
        {"symbol": "AAPL", "strategy": "rsi", "idempotency_key": "retry-key-0001"},
    )
    assert r.is_error
    msg = text(r)
    assert "upstream_timeout" in msg and "retry with idempotency_key=retry-key-0001" in msg
    assert "request_id=test-agent-" in msg


async def test_get_order_intent(client: Client, services: FakeServices) -> None:
    r = await client.call_tool("get_order_intent", {"intent_id": INTENT_ID.upper()})
    assert not r.is_error, text(r)
    assert services.requests[0].url.path == f"{INTENTS}/{INTENT_ID}"
    assert services.requests[0].method == "GET"
    assert r.structured_content == INTENT_EXAMPLE


async def test_get_order_intent_not_found(client: Client, services: FakeServices) -> None:
    missing = "00000000-0000-4000-8000-000000000000"
    services.set(
        ENGINE,
        "GET",
        f"{INTENTS}/{missing}",
        404,
        {"error": "not_found", "message": "m", "request_id": "e"},
    )
    r = await client.call_tool("get_order_intent", {"intent_id": missing})
    assert r.is_error
    assert "not_found: no order intent with this id" in text(r) and "request_id=e" in text(r)


async def test_list_order_intents(client: Client, services: FakeServices) -> None:
    r = await client.call_tool("list_order_intents", {"status": "pending_approval", "limit": 10})
    assert not r.is_error, text(r)
    req = services.requests[0]
    assert req.method == "GET" and req.url.path == INTENTS
    assert dict(req.url.params) == {"status": "pending_approval", "limit": "10"}
    out = r.structured_content
    assert out is not None
    assert out["intents"] == INTENT_LIST_EXAMPLE["intents"] and out["count"] == 2


async def test_list_order_intents_defaults(client: Client, services: FakeServices) -> None:
    r = await client.call_tool("list_order_intents", {})
    assert not r.is_error, text(r)
    assert dict(services.requests[0].url.params) == {"limit": "50"}


async def test_list_rejects_malformed_body(client: Client, services: FakeServices) -> None:
    services.set(ENGINE, "GET", INTENTS, 200, [{"id": "x"}])
    r = await client.call_tool("list_order_intents", {})
    assert r.is_error and "upstream_bad_response" in text(r)
