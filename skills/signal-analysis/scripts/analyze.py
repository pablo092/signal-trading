#!/usr/bin/env python3
"""signal-analysis - compute RSI / EMA-crossover trading signals from OHLCV bars.

Read-only and offline: reads bars from a file or stdin, prints a JSON result.
It never contacts a broker, never places orders, and needs no credentials.

Usage:
    python analyze.py --input bars.csv --symbol AAPL --strategy all
    cat bars.json | python analyze.py --input - --symbol BTCUSD --strategy rsi

Exit codes: 0 ok · 2 invalid input/arguments · 3 not enough bars.
"""

from __future__ import annotations

import argparse
import csv
import io
import json
import math
import sys
from datetime import datetime, timezone
from decimal import ROUND_HALF_UP, Decimal
from typing import Any, Dict, List, Optional, Sequence, Tuple

from indicators import ema, rsi_wilder

VERSION = "1.0.0"
DISCLAIMER = (
    "Educational technical-analysis output only. Not financial advice. "
    "No order was placed. Past indicator behavior does not predict future prices."
)
CLOSE_KEYS = ("close", "c", "Close", "adj_close", "close_price")
TIME_KEYS = ("timestamp", "time", "date", "datetime", "t")


class InputError(Exception):
    """Invalid input data or arguments (exit code 2)."""


class InsufficientDataError(Exception):
    """Fewer bars than the strategy requires (exit code 3)."""


# ── Input parsing ────────────────────────────────────────────────────────────

def _read_text(path: str) -> str:
    if path == "-":
        return sys.stdin.read()
    try:
        with open(path, encoding="utf-8-sig") as fh:
            return fh.read()
    except OSError as exc:
        raise InputError(f"cannot read input file: {exc}") from exc


def _rows_from_text(text: str) -> List[Dict[str, Any]]:
    stripped = text.lstrip()
    if not stripped:
        raise InputError("input is empty")
    if stripped[0] in "[{":
        try:
            data = json.loads(stripped)
        except json.JSONDecodeError as exc:
            raise InputError(f"invalid JSON: {exc}") from exc
        if isinstance(data, dict):
            for key in ("bars", "data", "results", "candles"):
                if isinstance(data.get(key), list):
                    data = data[key]
                    break
            else:
                raise InputError("JSON object must contain a 'bars' (or 'data'/'results'/'candles') list")
        if not isinstance(data, list):
            raise InputError("JSON input must be a list of bars")
        rows = []
        for item in data:
            if isinstance(item, (int, float)):
                rows.append({"close": item})
            elif isinstance(item, dict):
                rows.append(item)
            else:
                raise InputError("each JSON bar must be an object or a number (close)")
        return rows
    reader = csv.DictReader(io.StringIO(text))
    if not reader.fieldnames:
        raise InputError("CSV input has no header row")
    return [dict(r) for r in reader]


def _pick(row: Dict[str, Any], keys: Sequence[str]) -> Any:
    lowered = {str(k).strip().lower(): v for k, v in row.items()}
    for key in keys:
        if key.lower() in lowered:
            return lowered[key.lower()]
    return None


def _parse_time(value: Any) -> Optional[datetime]:
    if value is None or value == "":
        return None
    if isinstance(value, (int, float)):
        seconds = value / 1000.0 if value > 1e11 else value  # ms vs s epoch
        return datetime.fromtimestamp(seconds, tz=timezone.utc)
    text = str(value).strip().replace("Z", "+00:00")
    try:
        dt = datetime.fromisoformat(text)
    except ValueError:
        return None
    return dt if dt.tzinfo else dt.replace(tzinfo=timezone.utc)


def load_closes(path: str) -> Tuple[List[float], Optional[str], List[str]]:
    """Return (closes oldest-first, last bar timestamp ISO or None, warnings)."""
    rows = _rows_from_text(_read_text(path))
    warnings: List[str] = []
    parsed: List[Tuple[Optional[datetime], float]] = []
    for i, row in enumerate(rows, start=1):
        raw = _pick(row, CLOSE_KEYS)
        if raw is None or raw == "":
            raise InputError(f"bar {i}: missing 'close' value")
        try:
            close = float(raw)
        except (TypeError, ValueError) as exc:
            raise InputError(f"bar {i}: close {raw!r} is not a number") from exc
        if not math.isfinite(close) or close <= 0:
            raise InputError(f"bar {i}: close must be a positive finite number, got {raw!r}")
        parsed.append((_parse_time(_pick(row, TIME_KEYS)), close))

    times = [t for t, _ in parsed]
    last_ts: Optional[str] = None
    if all(t is not None for t in times) and parsed:
        if times != sorted(times):  # type: ignore[type-var]
            parsed.sort(key=lambda p: p[0])  # type: ignore[arg-type, return-value]
            warnings.append("bars were not in chronological order; sorted by timestamp")
        if len(set(times)) != len(times):
            warnings.append("duplicate timestamps detected")
        last_ts = parsed[-1][0].isoformat()  # type: ignore[union-attr]
    elif any(t is not None for t in times):
        warnings.append("some timestamps missing or unparseable; using input order (assumed oldest-first)")
    else:
        warnings.append("no timestamps; using input order (assumed oldest-first)")
    return [c for _, c in parsed], last_ts, warnings


# ── Strategies (mirror signal-service/src/application/strategies) ────────────

def _price(x: float) -> float:
    return float(Decimal(str(x)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP))


def _result(direction: str, strength: str, confidence: float, reason: str,
            strategy: str, price: float, indicators: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "strategy": strategy,
        "direction": direction,
        "strength": strength,
        "confidence": round(confidence, 4),
        "is_actionable": direction != "HOLD",
        "price": _price(price),
        "reason": reason,
        "indicators": indicators,
    }


def rsi_signal(closes: Sequence[float], period: int = 14, oversold: float = 30.0,
               overbought: float = 70.0, strong_margin: float = 5.0) -> Dict[str, Any]:
    if period < 2:
        raise InputError(f"RSI period must be >= 2, got {period}")
    if not (0 < oversold < overbought < 100):
        raise InputError("oversold must be < overbought and both in (0, 100)")
    required = period + 1
    if len(closes) < required:
        raise InsufficientDataError(f"RSI({period}) requires at least {required} bars, got {len(closes)}")

    rsi = rsi_wilder(closes, period)[-1]
    assert rsi is not None
    name = f"RSIStrategy(period={period})"
    ind = {"rsi": round(rsi, 4), "period": period, "oversold": oversold, "overbought": overbought}

    def conf(direction: str) -> float:
        if direction == "buy":
            norm = max(0.0, (oversold - rsi) / oversold)
        else:
            norm = max(0.0, (rsi - overbought) / (100 - overbought))
        return round(min(0.99, 0.5 + norm * 0.49), 4)

    strong_os, strong_ob = oversold - strong_margin, overbought + strong_margin
    price = closes[-1]
    if rsi <= strong_os:
        return _result("BUY", "STRONG", conf("buy"),
                       f"RSI={rsi:.1f} - strongly oversold (threshold={strong_os})", name, price, ind)
    if rsi <= oversold:
        return _result("BUY", "MODERATE", conf("buy"),
                       f"RSI={rsi:.1f} - oversold (threshold={oversold})", name, price, ind)
    if rsi >= strong_ob:
        return _result("SELL", "STRONG", conf("sell"),
                       f"RSI={rsi:.1f} - strongly overbought (threshold={strong_ob})", name, price, ind)
    if rsi >= overbought:
        return _result("SELL", "MODERATE", conf("sell"),
                       f"RSI={rsi:.1f} - overbought (threshold={overbought})", name, price, ind)
    return _result("HOLD", "WEAK", max(0.1, abs(rsi - 50) / 50 * 0.3),
                   f"RSI={rsi:.1f} - neutral zone", name, price, ind)


def ma_cross_signal(closes: Sequence[float], fast: int = 9, slow: int = 21) -> Dict[str, Any]:
    if fast < 1 or fast >= slow:
        raise InputError(f"fast period ({fast}) must be >= 1 and < slow period ({slow})")
    required = slow + 1
    if len(closes) < required:
        raise InsufficientDataError(f"MA cross({fast},{slow}) requires at least {required} bars, got {len(closes)}")

    f, s = ema(closes, fast), ema(closes, slow)
    pf, cf, ps, cs = f[-2], f[-1], s[-2], s[-1]
    name = f"MACrossStrategy(fast={fast},slow={slow})"
    ind = {"fast_ema": round(cf, 4), "slow_ema": round(cs, 4), "fast_period": fast, "slow_period": slow}
    price = closes[-1]

    if pf <= ps and cf > cs:
        c = round(min(0.95, 0.65 + (cf - cs) / cs * 10), 4)
        return _result("BUY", "STRONG" if c > 0.80 else "MODERATE", c,
                       f"Golden cross: fast EMA({fast}) crossed above slow EMA({slow})", name, price, ind)
    if pf >= ps and cf < cs:
        c = round(min(0.95, 0.65 + (cs - cf) / cs * 10), 4)
        return _result("SELL", "STRONG" if c > 0.80 else "MODERATE", c,
                       f"Death cross: fast EMA({fast}) crossed below slow EMA({slow})", name, price, ind)
    return _result("HOLD", "WEAK", 0.1,
                   f"No crossover detected (fast={cf:.2f}, slow={cs:.2f})", name, price, ind)


# ── CLI ──────────────────────────────────────────────────────────────────────

def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Compute RSI / EMA-crossover signals from OHLCV bars (read-only).")
    p.add_argument("--input", "-i", required=True, help="CSV or JSON file with bars, or '-' for stdin")
    p.add_argument("--symbol", "-s", default="UNKNOWN", help="Ticker label for the output (not validated against any market)")
    p.add_argument("--strategy", choices=["rsi", "ma_cross", "all"], default="all")
    p.add_argument("--timeframe", default=None, help="Label only, e.g. 1Hour or 1Day")
    p.add_argument("--period", type=int, default=14, help="RSI period (default 14)")
    p.add_argument("--oversold", type=float, default=30.0)
    p.add_argument("--overbought", type=float, default=70.0)
    p.add_argument("--strong-margin", type=float, default=5.0)
    p.add_argument("--fast", type=int, default=9, help="Fast EMA span (default 9)")
    p.add_argument("--slow", type=int, default=21, help="Slow EMA span (default 21)")
    p.add_argument("--compact", action="store_true", help="Single-line JSON output")
    p.add_argument("--version", action="version", version=f"signal-analysis {VERSION}")
    return p


def run(argv: Optional[Sequence[str]] = None) -> Tuple[int, Dict[str, Any]]:
    args = build_parser().parse_args(argv)
    base: Dict[str, Any] = {"tool": "signal-analysis", "version": VERSION, "symbol": args.symbol.upper(),
                            "timeframe": args.timeframe, "disclaimer": DISCLAIMER}
    try:
        closes, last_ts, warnings = load_closes(args.input)
        results = []
        if args.strategy in ("rsi", "all"):
            results.append(rsi_signal(closes, args.period, args.oversold, args.overbought, args.strong_margin))
        if args.strategy in ("ma_cross", "all"):
            results.append(ma_cross_signal(closes, args.fast, args.slow))
    except InputError as exc:
        return 2, {**base, "error": {"code": "invalid_input", "message": str(exc)}}
    except InsufficientDataError as exc:
        return 3, {**base, "error": {"code": "insufficient_data", "message": str(exc)}}

    directions = {r["direction"] for r in results}
    consensus = directions.pop() if len(directions) == 1 else "MIXED"
    return 0, {**base, "bars_used": len(closes), "last_bar_time": last_ts,
               "generated_at": datetime.now(tz=timezone.utc).isoformat(),
               "signals": results, "consensus": consensus, "warnings": warnings}


def main() -> None:
    compact = "--compact" in sys.argv
    code, payload = run()
    print(json.dumps(payload, ensure_ascii=True, separators=(",", ":") if compact else None,
                     indent=None if compact else 2))
    sys.exit(code)


if __name__ == "__main__":
    main()
