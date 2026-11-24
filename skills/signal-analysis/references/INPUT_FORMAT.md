# Input format

`scripts/analyze.py --input <path|->` auto-detects JSON (input starting with `[` or `{`) or CSV.

## CSV

A header row is required. Only a close column is mandatory; other columns are ignored.

```csv
timestamp,open,high,low,close,volume
2024-01-02T14:30:00Z,100.00,100.20,99.80,100.00,782554
2024-01-02T15:30:00Z,100.00,100.50,99.80,100.30,150631
```

Minimal:

```csv
close
187.15
188.02
```

## JSON

Any of these shapes:

```json
[{"timestamp": "2024-01-02T14:30:00Z", "close": 100.0}, ...]
```
```json
{"bars": [{"t": 1704205800000, "c": 100.0}, ...]}
```
```json
[100.0, 100.3, 99.8]
```

A top-level object may hold the list under `bars`, `data`, `results` or `candles`.

## Recognized field names (case-insensitive)

| Meaning | Accepted keys |
|---|---|
| Close price | `close`, `c`, `adj_close`, `close_price` |
| Time | `timestamp`, `time`, `date`, `datetime`, `t` |

Time values may be ISO-8601 strings (`2024-01-02`, `2024-01-02T14:30:00Z`, with or without an offset; naive values are treated as UTC) or Unix epoch numbers in seconds or milliseconds.

## Ordering and validation

- If **every** bar has a parseable time, bars are sorted chronologically, with a warning if the input wasn't already sorted. Duplicate timestamps produce a warning.
- Otherwise input order is used and is assumed to be **oldest first**. A warning says so.
- Every close must be a positive, finite number. Otherwise the script exits with code `2` and names the offending bar.
