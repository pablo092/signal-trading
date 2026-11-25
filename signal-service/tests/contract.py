"""Minimal JSON Schema validator for the shared contracts/ (BL-16).

Implements the subset the schemas use (type, enum, const, required, properties,
additionalProperties: false, items, minimum, maximum, exclusiveMinimum,
minLength, pattern, format: date-time, oneOf, $ref to a sibling file), the same
subset as bot-engine's internal/contracttest. agent-gateway checks the schemas
with the reference jsonschema library.
"""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any

CONTRACTS = Path(__file__).resolve().parents[2] / "contracts"
_DATE_TIME = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$")
_TYPES: dict[str, tuple[type, ...]] = {
    "object": (dict,),
    "array": (list,),
    "string": (str,),
    "boolean": (bool,),
    "null": (type(None),),
    "number": (int, float),
    "integer": (int,),
}


def schema(name: str) -> dict[str, Any]:
    result: dict[str, Any] = json.loads((CONTRACTS / "schemas" / name).read_text(encoding="utf-8"))
    return result


def example(name: str) -> Any:
    return json.loads((CONTRACTS / "examples" / name).read_text(encoding="utf-8"))


def _is_type(value: Any, name: str) -> bool:
    if isinstance(value, bool) and name in ("number", "integer"):
        return False
    return isinstance(value, _TYPES[name])


def check(s: dict[str, Any], value: Any, path: str = "$") -> list[str]:
    """Return the violations of value against schema s (empty when valid)."""
    if "$ref" in s:
        return check(schema(s["$ref"]), value, path)
    errs: list[str] = []
    if "type" in s:
        types = s["type"] if isinstance(s["type"], list) else [s["type"]]
        if not any(_is_type(value, t) for t in types):
            return [f"{path}: type {type(value).__name__}, want {s['type']}"]
    if "enum" in s and value not in s["enum"]:
        errs.append(f"{path}: {value!r} not in {s['enum']}")
    if "const" in s and value != s["const"]:
        errs.append(f"{path}: {value!r} != const {s['const']!r}")
    if isinstance(value, dict):
        props = s.get("properties", {})
        errs += [f"{path}: missing required {r!r}" for r in s.get("required", []) if r not in value]
        for key, item in sorted(value.items()):
            if key in props:
                errs += check(props[key], item, f"{path}.{key}")
            elif s.get("additionalProperties") is False:
                errs.append(f"{path}: unexpected property {key!r}")
    elif isinstance(value, list) and "items" in s:
        for i, item in enumerate(value):
            errs += check(s["items"], item, f"{path}[{i}]")
    elif isinstance(value, (int, float)) and not isinstance(value, bool):
        if "minimum" in s and value < s["minimum"]:
            errs.append(f"{path}: {value} < minimum {s['minimum']}")
        if "maximum" in s and value > s["maximum"]:
            errs.append(f"{path}: {value} > maximum {s['maximum']}")
        if "exclusiveMinimum" in s and value <= s["exclusiveMinimum"]:
            errs.append(f"{path}: {value} <= exclusiveMinimum {s['exclusiveMinimum']}")
    elif isinstance(value, str):
        if "minLength" in s and len(value) < s["minLength"]:
            errs.append(f"{path}: shorter than {s['minLength']}")
        if "pattern" in s and not re.search(s["pattern"], value):
            errs.append(f"{path}: {value!r} does not match {s['pattern']}")
        if s.get("format") == "date-time" and not _DATE_TIME.match(value):
            errs.append(f"{path}: {value!r} is not an RFC 3339 date-time")
    if "oneOf" in s:
        matched = sum(1 for branch in s["oneOf"] if not check(branch, value, path))
        if matched != 1:
            errs.append(f"{path}: matches {matched} oneOf branches, want exactly 1")
    return errs


def assert_conforms(schema_name: str, value: Any) -> None:
    errs = check(schema(schema_name), value)
    assert not errs, f"{schema_name} violations:\n  " + "\n  ".join(errs)
