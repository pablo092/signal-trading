"""Token store and scope rules (ADR-003). Mirrors bot-engine's auth tests."""

from __future__ import annotations

import json

import pytest
from src.infrastructure.auth import (
    HUMAN_ONLY_SCOPES,
    Principal,
    TokenStore,
    TokenTableError,
    hash_token,
)


def entry(pid: str, ptype: str, token: str, *scopes: str) -> dict[str, object]:
    return {"id": pid, "type": ptype, "sha256": hash_token(token), "scopes": list(scopes)}


def test_authenticate_known_and_unknown_tokens() -> None:
    store = TokenStore.from_entries(
        [entry("gw", "agent", "agent-t", "market:read"), entry("me", "human", "human-t", "portfolio:read")]
    )
    gw = store.authenticate("agent-t")
    assert gw is not None and gw.id == "gw" and gw.has_scope("market:read")
    me = store.authenticate("human-t")
    assert me is not None and me.id == "me"
    for bad in ["", "agent-t ", "AGENT-T", hash_token("agent-t")]:
        assert store.authenticate(bad) is None


def test_empty_store_denies() -> None:
    store = TokenStore.load("", "")
    assert len(store) == 0
    assert store.authenticate("anything") is None


@pytest.mark.parametrize("scope", sorted(HUMAN_ONLY_SCOPES))
def test_agent_cannot_hold_human_only_scope(scope: str) -> None:
    with pytest.raises(TokenTableError, match="agent tokens cannot hold"):
        TokenStore.from_entries([entry("gw", "agent", "t", scope)])


def test_defense_in_depth_at_verification() -> None:
    import hashlib

    p = Principal(id="gw", type="agent", scopes=frozenset({"admin:killswitch"}), digest=hashlib.sha256(b"t").digest())
    assert TokenStore([p]).authenticate("t") is None


@pytest.mark.parametrize(
    "raw",
    [
        "not json",
        '{"an": "object"}',
        json.dumps([{"id": "", "type": "human", "sha256": hash_token("a"), "scopes": ["market:read"]}]),
        json.dumps([{"id": "a", "type": "robot", "sha256": hash_token("a"), "scopes": ["market:read"]}]),
        json.dumps([{"id": "a", "type": "human", "sha256": "plaintext-token", "scopes": ["market:read"]}]),
        json.dumps([{"id": "a", "type": "human", "sha256": hash_token("a"), "scopes": []}]),
        json.dumps([{"id": "a", "type": "human", "sha256": hash_token("a"), "scopes": ["admin:*"]}]),
        json.dumps([entry("a", "human", "a", "market:read"), entry("a", "human", "b", "market:read")]),
        json.dumps([entry("a", "human", "a", "market:read"), entry("b", "human", "a", "market:read")]),
    ],
)
def test_invalid_tables_rejected(raw: str) -> None:
    with pytest.raises(TokenTableError):
        TokenStore.load(raw)


def test_error_never_echoes_content() -> None:
    with pytest.raises(TokenTableError) as exc_info:
        TokenStore.load('{"secret-looking-value": 1')
    assert "secret-looking-value" not in str(exc_info.value)


def test_load_from_file_and_inline_wins(tmp_path) -> None:  # type: ignore[no-untyped-def]
    f = tmp_path / "tokens.json"
    f.write_text(json.dumps([entry("file", "service", "file-t", "market:read")]), encoding="utf-8")
    assert TokenStore.load("", str(f)).authenticate("file-t") is not None
    inline = json.dumps([entry("inline", "service", "inline-t", "market:read")])
    store = TokenStore.load(inline, str(f))
    assert store.authenticate("inline-t") is not None
    assert store.authenticate("file-t") is None


def test_missing_file_is_an_error() -> None:
    with pytest.raises(TokenTableError, match="AUTH_TOKENS_FILE"):
        TokenStore.load("", "/nonexistent/tokens.json")


def test_uppercase_digest_accepted() -> None:
    e = entry("a", "human", "t", "market:read")
    e["sha256"] = str(e["sha256"]).upper()
    assert TokenStore.from_entries([e]).authenticate("t") is not None


def test_same_table_format_as_bot_engine() -> None:
    # The Go test vector: sha256("x") must match bot-engine's auth.HashToken("x").
    assert hash_token("x") == "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"
