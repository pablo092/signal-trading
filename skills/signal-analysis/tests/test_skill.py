"""Tests for the signal-analysis skill (stdlib unittest; pandas parity tests run only if pandas is installed).

Run from skills/signal-analysis:  python -m unittest discover -s tests -v
"""

from __future__ import annotations

import json
import math
import os
import random
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.join(os.path.dirname(HERE), "scripts")
EXAMPLE = os.path.join(os.path.dirname(HERE), "examples", "sample_bars.csv")
sys.path.insert(0, SCRIPTS)

import analyze  # noqa: E402
from indicators import ema, rsi_wilder  # noqa: E402

try:
    import pandas as pd  # type: ignore
except ImportError:  # pragma: no cover
    pd = None


def random_walk(n: int, seed: int, drift: float = 0.0) -> list:
    rng = random.Random(seed)
    p, out = 100.0, []
    for _ in range(n):
        p *= 1 + drift + rng.gauss(0, 0.01)
        out.append(p)
    return out


def trend(n: int, step: float, bounce_every: int = 5, bounce: float = 0.001) -> list:
    out = [100.0]
    for i in range(1, n):
        out.append(out[-1] * ((1 + bounce) if i % bounce_every == 0 else (1 + step)))
    return out


@unittest.skipIf(pd is None, "pandas not installed")
class PandasParityTest(unittest.TestCase):
    """Indicators must match signal-service's pandas formulas."""

    def test_rsi_matches_pandas(self) -> None:
        for seed in range(20):
            closes = random_walk(120, seed)
            s = pd.Series(closes)
            d = s.diff()
            g = d.clip(lower=0).ewm(com=13, min_periods=14).mean()
            l = (-d.clip(upper=0)).ewm(com=13, min_periods=14).mean()
            expected = (100 - 100 / (1 + g / l)).tolist()
            got = rsi_wilder(closes, 14)
            for e, v in zip(expected, got):
                if isinstance(e, float) and math.isnan(e):
                    self.assertIsNone(v)
                else:
                    self.assertAlmostEqual(e, v, places=9)

    def test_ema_matches_pandas(self) -> None:
        closes = random_walk(200, 3)
        for span in (3, 9, 21, 50):
            expected = pd.Series(closes).ewm(span=span, adjust=False).mean().tolist()
            for e, v in zip(expected, ema(closes, span)):
                self.assertAlmostEqual(e, v, places=9)


class StrategyTest(unittest.TestCase):
    def test_downtrend_is_buy(self) -> None:
        r = analyze.rsi_signal(trend(50, -0.015))
        self.assertEqual(r["direction"], "BUY")
        self.assertTrue(r["is_actionable"])
        self.assertIn("RSI=", r["reason"])

    def test_uptrend_is_sell(self) -> None:
        self.assertEqual(analyze.rsi_signal(trend(50, 0.015, bounce=-0.001))["direction"], "SELL")

    def test_confidence_bounds(self) -> None:
        for closes in (trend(50, -0.015), trend(50, 0.015, bounce=-0.001), random_walk(80, 1)):
            for r in (analyze.rsi_signal(closes), analyze.ma_cross_signal(closes)):
                self.assertGreaterEqual(r["confidence"], 0.0)
                self.assertLessEqual(r["confidence"], 1.0)

    def test_flat_prices_rsi_is_neutral(self) -> None:
        r = analyze.rsi_signal([100.0] * 30)
        self.assertEqual(r["direction"], "HOLD")
        self.assertEqual(r["indicators"]["rsi"], 50.0)

    def test_golden_cross(self) -> None:
        closes = [100.0] * 10 + [90.0] * 5 + [110.0]
        self.assertEqual(analyze.ma_cross_signal(closes, fast=3, slow=5)["direction"], "BUY")

    def test_death_cross(self) -> None:
        closes = [100.0] * 10 + [110.0] * 5 + [90.0]
        self.assertEqual(analyze.ma_cross_signal(closes, fast=3, slow=5)["direction"], "SELL")

    def test_no_cross_is_hold(self) -> None:
        r = analyze.ma_cross_signal([100.0 + i for i in range(40)], fast=3, slow=5)
        self.assertEqual(r["direction"], "HOLD")
        self.assertEqual(r["strength"], "WEAK")

    def test_insufficient_bars(self) -> None:
        with self.assertRaises(analyze.InsufficientDataError):
            analyze.rsi_signal([100.0] * 10, period=14)
        with self.assertRaises(analyze.InsufficientDataError):
            analyze.ma_cross_signal([100.0] * 21, fast=9, slow=21)

    def test_invalid_params(self) -> None:
        with self.assertRaises(analyze.InputError):
            analyze.rsi_signal([100.0] * 30, period=1)
        with self.assertRaises(analyze.InputError):
            analyze.rsi_signal([100.0] * 30, oversold=70, overbought=30)
        with self.assertRaises(analyze.InputError):
            analyze.ma_cross_signal([100.0] * 30, fast=21, slow=9)


class InputParsingTest(unittest.TestCase):
    def _write(self, text: str, suffix: str) -> str:
        fd, path = tempfile.mkstemp(suffix=suffix)
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(text)
        self.addCleanup(os.remove, path)
        return path

    def test_csv_example_file(self) -> None:
        closes, last_ts, warnings = analyze.load_closes(EXAMPLE)
        self.assertEqual(len(closes), 60)
        self.assertTrue(last_ts.startswith("2024-01-05T01:30"))
        self.assertEqual(warnings, [])

    def test_json_list_object_and_numbers(self) -> None:
        bars = [{"t": f"2024-01-0{i + 1}T00:00:00Z", "c": 100 + i} for i in range(5)]
        closes, _, _ = analyze.load_closes(self._write(json.dumps({"bars": bars}), ".json"))
        self.assertEqual(closes, [100.0, 101.0, 102.0, 103.0, 104.0])
        closes, _, w = analyze.load_closes(self._write("[1, 2, 3]", ".json"))
        self.assertEqual(closes, [1.0, 2.0, 3.0])
        self.assertTrue(any("no timestamps" in x for x in w))

    def test_unsorted_input_is_sorted(self) -> None:
        bars = [{"timestamp": "2024-01-03", "close": 3}, {"timestamp": "2024-01-01", "close": 1},
                {"timestamp": "2024-01-02", "close": 2}]
        closes, _, w = analyze.load_closes(self._write(json.dumps(bars), ".json"))
        self.assertEqual(closes, [1.0, 2.0, 3.0])
        self.assertTrue(any("sorted" in x for x in w))

    def test_epoch_ms_timestamps(self) -> None:
        bars = [{"t": 1704067200000 + i * 3600000, "close": 10 + i} for i in range(3)]
        _, last_ts, _ = analyze.load_closes(self._write(json.dumps(bars), ".json"))
        self.assertTrue(last_ts.startswith("2024-01-01T02:00"))

    def test_bad_values_rejected(self) -> None:
        for text in ("close\n100\nabc\n", "close\n100\n-5\n", "close\n100\nnan\n", "open\n1\n", ""):
            with self.assertRaises(analyze.InputError):
                analyze.load_closes(self._write(text, ".csv"))


class CliTest(unittest.TestCase):
    def _cli(self, *args: str, stdin: str = None):
        proc = subprocess.run([sys.executable, os.path.join(SCRIPTS, "analyze.py"), *args],
                              input=stdin, capture_output=True, text=True)
        return proc.returncode, proc.stdout

    def test_end_to_end_all(self) -> None:
        code, out = self._cli("--input", EXAMPLE, "--symbol", "demo", "--strategy", "all")
        self.assertEqual(code, 0, out)
        data = json.loads(out)
        self.assertEqual(data["symbol"], "DEMO")
        self.assertEqual([s["strategy"].split("(")[0] for s in data["signals"]], ["RSIStrategy", "MACrossStrategy"])
        self.assertIn("Not financial advice", data["disclaimer"])

    def test_stdin_and_exit_codes(self) -> None:
        code, out = self._cli("--input", "-", "--strategy", "rsi", "--compact", stdin="close\n1\n2\n3\n")
        self.assertEqual(code, 3)
        self.assertEqual(json.loads(out)["error"]["code"], "insufficient_data")
        code, out = self._cli("--input", "-", stdin="not,a\nvalid,file\n")
        self.assertEqual(code, 2)

    def test_no_network_imports(self) -> None:
        """The skill must stay offline: no networking modules in the scripts."""
        banned = ("socket", "urllib", "http.client", "requests", "httpx", "aiohttp", "subprocess")
        for name in os.listdir(SCRIPTS):
            if name.endswith(".py"):
                with open(os.path.join(SCRIPTS, name), encoding="utf-8") as fh:
                    src = fh.read()
                for mod in banned:
                    self.assertNotIn(f"import {mod}", src, f"{name} imports {mod}")
                    self.assertNotIn(f"from {mod}", src, f"{name} imports from {mod}")


if __name__ == "__main__":
    unittest.main()
