"""The structured event feed: one JSON object per line, appended to
``runs/<id>/events.jsonl``, so a visual reads one file instead of scraping
the transcript. Append-only; nothing here rewrites an earlier line.

Every line carries ``ts`` (ISO 8601, UTC), ``run_id``, ``phase`` (the beat
the CLI is in, set by :func:`phase`) and ``kind``. The kinds, agreed with
the visuals side:

- ``phase``: ``status`` start or end, ``title``.
- ``cmd``: from the guarded executor; ``argv``, ``cwd``, ``returncode``,
  ``seconds``, and ``stdout_path`` when the output was captured (the text
  lands in a file under ``runs/<id>/cmd/``, never inline).
- ``inventory``: ``estate`` and ``items``, each ``{id, type, address, tags}``,
  from the reads in :mod:`govern`; emitted after setup, after each carve and
  after teardown, since those three diffs are the whole picture.
- ``verdict``: ``name`` and the verdict dataclass as a dict.
- ``measure``: ``estate``, ``requests``, ``refresh`` and any reference numbers.
- ``receipt``: the reproducible receipt as :mod:`receipt` writes it.
- ``note``: ``text`` the visual should echo.

Only writes live here. Readers (the visual, the tests) use :func:`read`.
"""

from __future__ import annotations

import dataclasses
import datetime
import json
import pathlib
from typing import Any

from . import config

_current_phase: str | None = None


def path(cfg: config.Config) -> pathlib.Path:
    return cfg.run_dir / "events.jsonl"


def _now() -> str:
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds")


def _emit(cfg: config.Config, kind: str, **fields: Any) -> dict[str, Any]:
    cfg.run_dir.mkdir(parents=True, exist_ok=True)
    line = {"ts": _now(), "run_id": cfg.run_id, "phase": _current_phase, "kind": kind, **fields}
    with path(cfg).open("a") as fh:
        fh.write(json.dumps(line, default=_jsonable) + "\n")
    return line


def _jsonable(obj: Any) -> Any:
    if dataclasses.is_dataclass(obj) and not isinstance(obj, type):
        return dataclasses.asdict(obj)
    if isinstance(obj, pathlib.Path):
        return str(obj)
    if isinstance(obj, (set, tuple)):
        return list(obj)
    return str(obj)


# --------------------------------------------------------------------------
# The kinds
# --------------------------------------------------------------------------

def phase(cfg: config.Config, name: str, status: str, title: str = "") -> None:
    """Mark a beat's start or end. Every line emitted between a start and
    its end carries the beat's name in ``phase``."""
    global _current_phase
    if status not in ("start", "end"):
        raise ValueError(f"phase status must be start or end, not {status!r}")
    if status == "start":
        _current_phase = name
    _emit(cfg, "phase", name=name, status=status, title=title)
    if status == "end":
        _current_phase = None


def cmd(
    cfg: config.Config,
    argv: list[str],
    cwd: str | None,
    returncode: int,
    seconds: float,
    stdout: str | None = None,
) -> None:
    """One executed command. Captured stdout is written to
    ``runs/<id>/cmd/<n>.out`` and referenced by path, so a plan's text is
    available to the visual without bloating the feed."""
    stdout_path: str | None = None
    if stdout is not None:
        d = cfg.run_dir / "cmd"
        d.mkdir(parents=True, exist_ok=True)
        n = sum(1 for _ in d.glob("*.out")) + 1
        p = d / f"{n:04d}.out"
        p.write_text(stdout)
        stdout_path = str(p)
    _emit(cfg, "cmd", argv=list(argv), cwd=cwd, returncode=returncode, seconds=round(seconds, 3), stdout_path=stdout_path)


def inventory(cfg: config.Config, estate: str, items: list[dict[str, Any]]) -> None:
    _emit(cfg, "inventory", estate=estate, items=items)


def verdict(cfg: config.Config, name: str, obj: Any) -> None:
    _emit(cfg, "verdict", name=name, verdict=obj)


def measure(cfg: config.Config, estate: str, requests: int, refresh: bool, reference: dict[str, Any] | None = None) -> None:
    _emit(cfg, "measure", estate=estate, requests=requests, refresh=refresh, reference=reference or {})


def receipt(cfg: config.Config, obj: Any) -> None:
    _emit(cfg, "receipt", receipt=obj)


def note(cfg: config.Config, text: str) -> None:
    _emit(cfg, "note", text=text)


# --------------------------------------------------------------------------
# Reading back
# --------------------------------------------------------------------------

def read(cfg: config.Config) -> list[dict[str, Any]]:
    p = path(cfg)
    if not p.exists():
        return []
    return [json.loads(line) for line in p.read_text().splitlines() if line.strip()]
