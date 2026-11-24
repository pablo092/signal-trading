"""Per-agent token-bucket rate limiter (ADR-002: read tools still consume data quota)."""

from __future__ import annotations

import time
from collections.abc import Callable


class RateLimiter:
    def __init__(self, per_minute: float, clock: Callable[[], float] = time.monotonic) -> None:
        self._capacity = per_minute
        self._rate = per_minute / 60.0
        self._clock = clock
        self._buckets: dict[str, tuple[float, float]] = {}

    def allow(self, agent_id: str) -> bool:
        now = self._clock()
        tokens, last = self._buckets.get(agent_id, (self._capacity, now))
        tokens = min(self._capacity, tokens + (now - last) * self._rate)
        if tokens < 1.0:
            self._buckets[agent_id] = (tokens, now)
            return False
        self._buckets[agent_id] = (tokens - 1.0, now)
        return True

    def retry_after(self, agent_id: str) -> float:
        tokens, _ = self._buckets.get(agent_id, (self._capacity, 0.0))
        return max(0.0, (1.0 - tokens) / self._rate)
