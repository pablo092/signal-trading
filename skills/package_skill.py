#!/usr/bin/env python3
"""Validate and package an agent skill as a zip with SKILL.md at the root.

Usage (from the repo root or skills/):
    python skills/package_skill.py signal-analysis          # validate + run tests + build zip
    python skills/package_skill.py signal-analysis --check  # validate only

Output: skills/dist/<name>-<version>.zip  (+ printed SHA-256)
Only SKILL.md, scripts/, references/, examples/ and an optional LICENSE are shipped;
tests/ and caches are excluded.
"""

from __future__ import annotations

import argparse
import hashlib
import py_compile
import re
import subprocess
import sys
import zipfile
from pathlib import Path

SKILLS_DIR = Path(__file__).resolve().parent
DIST_DIR = SKILLS_DIR / "dist"
INCLUDE_DIRS = ("scripts", "references", "examples")
INCLUDE_FILES = ("SKILL.md", "LICENSE", "LICENSE.md", "LICENSE.txt")
MAX_BYTES = 50 * 1024 * 1024  # Agensi upload limit
BANNED_NAME = re.compile(r"(^\.env|\.pem$|\.key$|id_rsa|credentials|secret)", re.I)
SECRET_CONTENT = re.compile(
    r"(PK[A-Z0-9]{16,}|AK[A-Z0-9]{16,}|ghp_[A-Za-z0-9]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY-----"
    r"|(api[_-]?key|secret[_-]?key)\s*[=:]\s*['\"][^'\"]{12,})"
)
NAME_RE = re.compile(r"^[a-z0-9]+(-[a-z0-9]+)*$")


def fail(msg: str) -> None:
    print(f"ERROR: {msg}", file=sys.stderr)
    sys.exit(1)


def parse_frontmatter(skill_md: Path) -> dict:
    text = skill_md.read_text(encoding="utf-8")
    m = re.match(r"^---\r?\n(.*?)\r?\n---\r?\n", text, re.S)
    if not m:
        fail("SKILL.md must start with a '---' YAML frontmatter block")
    meta = {}
    for line in m.group(1).splitlines():
        if ":" in line and not line.startswith(" "):
            key, _, value = line.partition(":")
            meta[key.strip()] = value.strip()
    return meta


def collect(skill_dir: Path) -> list:
    files = [skill_dir / f for f in INCLUDE_FILES if (skill_dir / f).is_file()]
    for d in INCLUDE_DIRS:
        if (skill_dir / d).is_dir():
            files += [p for p in sorted((skill_dir / d).rglob("*"))
                      if p.is_file() and "__pycache__" not in p.parts and p.suffix != ".pyc"]
    return files


def read_version(skill_dir: Path) -> str:
    src = (skill_dir / "scripts" / "analyze.py")
    if src.is_file():
        m = re.search(r'^VERSION\s*=\s*"([^"]+)"', src.read_text(encoding="utf-8"), re.M)
        if m:
            return m.group(1)
    return "0.0.0"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("skill", help="skill directory name under skills/")
    ap.add_argument("--check", action="store_true", help="validate only, do not build")
    ap.add_argument("--skip-tests", action="store_true")
    args = ap.parse_args()

    skill_dir = SKILLS_DIR / args.skill
    skill_md = skill_dir / "SKILL.md"
    if not skill_md.is_file():
        fail(f"{skill_md} not found")

    meta = parse_frontmatter(skill_md)
    name, desc = meta.get("name", ""), meta.get("description", "")
    if not NAME_RE.match(name):
        fail(f"frontmatter 'name' must be lowercase-hyphenated, got {name!r}")
    if name != args.skill:
        fail(f"frontmatter name {name!r} must match directory {args.skill!r}")
    if not 20 <= len(desc) <= 1024:
        fail(f"frontmatter 'description' must be 20-1024 chars, got {len(desc)}")

    files = collect(skill_dir)
    total = 0
    for f in files:
        rel = f.relative_to(skill_dir).as_posix()
        if BANNED_NAME.search(f.name):
            fail(f"refusing to package sensitive-looking file: {rel}")
        data = f.read_bytes()
        total += len(data)
        if f.suffix in (".py", ".md", ".csv", ".json", ".txt", ".sh") and SECRET_CONTENT.search(data.decode("utf-8", "ignore")):
            fail(f"possible credential in {rel}; remove it before packaging")
        if f.suffix == ".py":
            py_compile.compile(str(f), doraise=True, cfile=None)
    for p in skill_dir.rglob("__pycache__"):
        for c in p.glob("*.pyc"):
            c.unlink()
    if total > MAX_BYTES:
        fail(f"package content is {total} bytes, over the 50 MB limit")

    if not args.skip_tests and (skill_dir / "tests").is_dir():
        r = subprocess.run([sys.executable, "-m", "unittest", "discover", "-s", "tests"], cwd=skill_dir)
        if r.returncode != 0:
            fail("tests failed; not packaging")

    version = read_version(skill_dir)
    print(f"OK  {name} v{version}: {len(files)} files, {total} bytes")
    for f in files:
        print(f"    {f.relative_to(skill_dir).as_posix()}")
    if args.check:
        return

    DIST_DIR.mkdir(exist_ok=True)
    out = DIST_DIR / f"{name}-{version}.zip"
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zf:
        for f in files:
            info = zipfile.ZipInfo(f.relative_to(skill_dir).as_posix(), date_time=(2024, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (0o755 if f.suffix == ".py" else 0o644) << 16
            zf.writestr(info, f.read_bytes())
    digest = hashlib.sha256(out.read_bytes()).hexdigest()
    print(f"\nBuilt {out.relative_to(SKILLS_DIR.parent).as_posix()} ({out.stat().st_size} bytes)")
    print(f"SHA-256 {digest}")


if __name__ == "__main__":
    main()
