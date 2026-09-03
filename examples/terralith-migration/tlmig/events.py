"""The structured event feed: one JSON object per line, appended to
``runs/<id>/events.jsonl``, so a visual reads one file instead of scraping
the transcript. Append-only; nothing here rewrites an earlier line.

Every line carries ``ts`` (ISO 8601, UTC), ``run_id``, ``phase`` (the beat
the CLI is in, set by :func:`phase`) and ``kind``. The kinds, agreed with
the visuals side:

- ``phase``: ``status`` start or end, ``name``, ``title``, and ``seconds``
  on the end line; written by the :func:`phase` context manager.
- ``cmd``: from the guarded executor; ``argv``, ``cwd``, ``returncode``,
  ``seconds``, and ``stdout_path`` when the output was captured (the text
  lands in a file under ``runs/<id>/cmd/``, never inline).
- ``inventory``: ``estate`` and ``items``, each ``{id, type, address, tags}``,
  from the reads in :mod:`govern`; emitted after setup, after each carve and
  after teardown, since those three diffs are the whole picture.
- ``fact``: ``label`` and ``value``, one thing the map can place, such as a
  role's estate after a move.
- ``verdict``: ``name``, ``ok``, ``lines`` (what the terminal showed) and
  ``verdict``, the dataclass as a dict.
- ``measure``: ``label``, ``estate``, ``requests``, ``cache_hits``,
  ``refresh``, ``seconds`` and ``reference``, the emulator's numbers beside.
- ``receipt``: the reproducible receipt as :mod:`receipt` writes it.
- ``note``: ``text`` the visual should echo.

Only writes live here; :func:`emit` is the one writer and every kind above
is a thin wrapper on it. Readers (the visual, the tests) use :func:`read`.
"""

from __future__ import annotations

import contextlib
import dataclasses
import datetime
import json
import pathlib
import time
from typing import Any, Iterator

from . import config

_current_phase: str | None = None


def path(cfg: config.Config) -> pathlib.Path:
    return cfg.run_dir / "events.jsonl"


def _now() -> str:
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds")


def emit(cfg: config.Config, kind: str, **fields: Any) -> dict[str, Any]:
    """Append one line. Every wrapper below is this with a fixed kind."""
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

@contextlib.contextmanager
def phase(cfg: config.Config, name: str, title: str = "") -> Iterator[None]:
    """A beat. ``with events.phase(cfg, "carve"):`` writes the start line,
    tags every line emitted inside with the beat's name, and writes the end
    line with the elapsed seconds, on exceptions too, so a beat that dies
    still closes in the feed."""
    global _current_phase
    previous = _current_phase
    _current_phase = name
    emit(cfg, "phase", name=name, status="start", title=title)
    started = time.monotonic()
    try:
        yield
    finally:
        emit(cfg, "phase", name=name, status="end", title=title, seconds=round(time.monotonic() - started, 3))
        _current_phase = previous


def fact(cfg: config.Config, label: str, value: Any) -> None:
    """One placeable fact, such as ``fact(cfg, "role:tlmig-1-team-a-role",
    "tlmig-1-team-b")`` after a move."""
    emit(cfg, "fact", label=label, value=value)


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
    emit(cfg, "cmd", argv=list(argv), cwd=cwd, returncode=returncode, seconds=round(seconds, 3), stdout_path=stdout_path)


def inventory(cfg: config.Config, estate: str, items: list[dict[str, Any]]) -> None:
    emit(cfg, "inventory", estate=estate, items=items)


def verdict(cfg: config.Config, name: str, obj: Any, ok: bool | None = None, lines: list[str] | None = None) -> None:
    """A verdict: ``ok`` and ``lines`` are what the terminal showed, and
    ``verdict`` is the dataclass (or dict) behind them for anything the
    visual wants to drill into."""
    if ok is None and hasattr(obj, "ok"):
        ok = bool(obj.ok)
    emit(cfg, "verdict", name=name, ok=ok, lines=list(lines or []), verdict=obj)


def measure(
    cfg: config.Config,
    *,
    requests: int,
    label: str = "",
    estate: str = "",
    cache_hits: int | None = None,
    refresh: bool | None = None,
    seconds: float | None = None,
    reference: dict[str, Any] | None = None,
) -> None:
    """One measured plan: what it cost, and the emulator's reference numbers
    beside it so the two are never conflated."""
    emit(
        cfg, "measure", label=label, estate=estate, requests=requests,
        cache_hits=cache_hits, refresh=refresh,
        seconds=None if seconds is None else round(seconds, 3),
        reference=reference or {},
    )


def receipt(cfg: config.Config, obj: Any) -> None:
    emit(cfg, "receipt", receipt=obj)


def note(cfg: config.Config, text: str) -> None:
    emit(cfg, "note", text=text)


# --------------------------------------------------------------------------
# Reading back
# --------------------------------------------------------------------------

def read(cfg_or_path: config.Config | pathlib.Path | str) -> list[dict[str, Any]]:
    """The feed as a list of dicts, from a Config or a path to events.jsonl."""
    p = path(cfg_or_path) if isinstance(cfg_or_path, config.Config) else pathlib.Path(cfg_or_path)
    if not p.exists():
        return []
    return [json.loads(line) for line in p.read_text().splitlines() if line.strip()]
