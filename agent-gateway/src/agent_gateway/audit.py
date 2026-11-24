"""One structured audit line per tool call (ADR-003 until audit_events lands in M2).

Lines go to stderr — in STDIO transport stdout carries the MCP protocol — and,
optionally, to an append-only file. Only known, validated arguments are recorded.
"""

from __future__ import annotations

import json
import sys
import threading
from datetime import UTC, datetime
from typing import Any, TextIO


class AuditLog:
    def __init__(self, agent_id: str, file_path: str = "", stream: TextIO | None = None) -> None:
        self._agent_id = agent_id
        self._stream = stream if stream is not None else sys.stderr
        self._file_path = file_path
        self._lock = threading.Lock()

    def record(
        self,
        tool: str,
        args: dict[str, Any],
        outcome: str,
        duration_ms: float,
        error_code: str = "",
    ) -> dict[str, Any]:
        entry = {
            "ts": datetime.now(UTC).isoformat(timespec="milliseconds"),
            "event": "audit.tool_call",
            "actor_id": self._agent_id,
            "actor_type": "agent",
            "tool": tool,
            "args": args,
            "outcome": outcome,
            "error_code": error_code,
            "duration_ms": round(duration_ms, 1),
        }
        line = json.dumps(entry, ensure_ascii=True, sort_keys=True)
        with self._lock:
            print(line, file=self._stream, flush=True)
            if self._file_path:
                with open(self._file_path, "a", encoding="utf-8") as f:
                    f.write(line + "\n")
        return entry
