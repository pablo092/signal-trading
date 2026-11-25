"""BL-16: the shared contracts are valid JSON Schema 2020-12 and every canonical
example conforms to its schema, checked with the reference `jsonschema` library.

The Go, Python (signal-service) and Dart contract tests use a small validator
for the same subset; this test anchors them to the standard implementation.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from referencing import Registry, Resource

CONTRACTS = Path(__file__).resolve().parents[2] / "contracts"
SCHEMAS = {
    p.name: json.loads(p.read_text(encoding="utf-8"))
    for p in (CONTRACTS / "schemas").glob("*.schema.json")
}
MANIFEST: dict[str, str] = json.loads(
    (CONTRACTS / "examples" / "manifest.json").read_text(encoding="utf-8")
)
REGISTRY = Registry().with_resources(
    (name, Resource.from_contents(s)) for name, s in SCHEMAS.items()
)


def validator(schema_name: str) -> Draft202012Validator:
    return Draft202012Validator(
        SCHEMAS[schema_name], registry=REGISTRY, format_checker=Draft202012Validator.FORMAT_CHECKER
    )


@pytest.mark.parametrize("name", sorted(SCHEMAS))
def test_schema_is_valid_2020_12(name: str) -> None:
    Draft202012Validator.check_schema(SCHEMAS[name])


@pytest.mark.parametrize(("example", "schema"), sorted(MANIFEST.items()))
def test_example_conforms(example: str, schema: str) -> None:
    validator(schema).validate(
        json.loads((CONTRACTS / "examples" / example).read_text(encoding="utf-8"))
    )


def test_every_example_is_listed() -> None:
    files = {p.name for p in (CONTRACTS / "examples").glob("*.json")} - {"manifest.json"}
    assert files == set(MANIFEST)


@pytest.mark.parametrize(
    ("schema", "bad"),
    [
        ("signal.schema.json", {"direction": "MAYBE"}),
        ("signal.schema.json", {"confidence": 1.5}),
        ("signal.schema.json", {"extra": 1}),
        ("error.schema.json", {"error": "Not A Code"}),
    ],
)
def test_schemas_reject_bad_payloads(schema: str, bad: dict[str, object]) -> None:
    base = json.loads(
        (
            CONTRACTS
            / "examples"
            / ("signal.json" if schema.startswith("signal") else "error.json")
        ).read_text(encoding="utf-8")
    )
    assert not validator(schema).is_valid({**base, **bad})


def test_ws_event_type_must_match_data() -> None:
    event = json.loads(
        (CONTRACTS / "examples" / "ws-order-placed.json").read_text(encoding="utf-8")
    )
    event["type"] = "signal.received"  # order data under a signal type
    assert not validator("ws-event.schema.json").is_valid(event)
