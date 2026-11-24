# Agent skills

Portable skills extracted from this repository, for AI agents such as Claude Code, Codex and Cursor. A skill is a folder (shipped as a zip) with a `SKILL.md` at its root that tells the agent what the skill does and how to run it.

| Skill | What it does | Side effects |
|---|---|---|
| [`signal-analysis`](signal-analysis/SKILL.md) | RSI and EMA-crossover signals from price bars the agent provides | None: offline, read-only, no credentials, never trades |

Skills are deliberately independent of the running services (`signal-service`, `bot-engine`). They run entirely on the user's machine with the Python standard library. Order execution is **out of scope** for any published skill until the safety controls in `ADR-005` (M0 audit) are implemented.

## Develop

```bash
cd skills/signal-analysis
python -m unittest discover -s tests -v    # pandas parity tests run if pandas is installed
python scripts/analyze.py -i examples/sample_bars.csv -s DEMO
```

When strategy logic changes in `signal-service/src/application/strategies/`, update `scripts/analyze.py` too and keep the parity tests green.

## Package

```bash
python skills/package_skill.py signal-analysis          # validate, test, build
python skills/package_skill.py signal-analysis --check  # validate only
```

The script:
- validates the frontmatter (`name` matches the folder, `description` is present);
- scans for credential-looking content;
- compiles scripts and runs the tests;
- writes `skills/dist/signal-analysis-<version>.zip` (git-ignored) with `SKILL.md` at the root. Only `SKILL.md`, `scripts/`, `references/`, `examples/` and an optional `LICENSE` are shipped.

Bump `VERSION` in `scripts/analyze.py` for each release.
