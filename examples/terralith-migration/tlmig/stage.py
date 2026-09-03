"""Running one phase from the stage.

The notebook is the stage: a presenter clicks a phase, talks, and watches the
picture move. This module is the seam between that click and the beats. It
runs a phase as a subprocess in the background so the notebook's kernel stays
free to redraw, tees the phase's own narration (the rich panels the beats
print) into a log the notebook tails, and reports where the phase stands.

Nothing here imports the beats. The command it runs is a template the CLI's
owner can correct in one place (``CLI``), and a phase's status is read back
from files, so a phase started here and a phase started from a terminal look
the same to the picture.
"""
from __future__ import annotations

import dataclasses
import json
import os
import pathlib
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone

# The CLI grammar the buttons call. ``{phase}`` and ``{run_id}`` are filled in.
# ``--auto`` is tlmig's own switch that removes the keypress between beats,
# because the stage supplies the pacing instead.
CLI: list[str] = [sys.executable, "-m", "tlmig.cli", "{phase}", "--run", "{run_id}", "--auto"]

RUNS = pathlib.Path("runs")


def find_binary() -> str:
    """The choudoufu binary a phase should run: CHOUDOUFU_BIN if set, else
    one on PATH, else the pinned release the smoke harness caches under
    ~/.cache/choudoufu-smoke/<version>/choudoufu, else a bare name the CLI
    will refuse in preflight."""
    import shutil

    env = os.environ.get("CHOUDOUFU_BIN")
    if env:
        return env
    on_path = shutil.which("choudoufu")
    if on_path:
        return on_path
    try:
        from . import config
        version = config.CHOUDOUFU_VERSION
    except Exception:  # pragma: no cover - config is 37's; stage must not depend on it
        version = ""
    cached = pathlib.Path.home() / ".cache" / "choudoufu-smoke" / version / "choudoufu"
    if version and cached.exists():
        return str(cached)
    return "choudoufu"


@dataclasses.dataclass
class PhaseRun:
    phase: str
    run_id: str
    log: pathlib.Path
    started: datetime
    proc: subprocess.Popen | None = None
    returncode: int | None = None
    ended: datetime | None = None

    @property
    def status(self) -> str:
        if self.returncode is None:
            return "running"
        return "done" if self.returncode == 0 else f"failed (exit {self.returncode})"

    def tail(self, lines: int = 12) -> str:
        try:
            text = self.log.read_text(errors="replace")
        except OSError:
            return ""
        return "\n".join(text.splitlines()[-lines:])


class Stage:
    """One run's phases, started from buttons, tracked by name."""

    def __init__(self, run_id: str, runs: pathlib.Path = RUNS, cli: list[str] | None = None, env: dict | None = None, binary: str | None = None):
        self.run_id = run_id
        self.run_dir = runs / run_id
        self.cli = cli or CLI
        self.binary = binary or find_binary()
        self.env = dict(env or {})
        self.env.setdefault("CHOUDOUFU_BIN", self.binary)
        self.phases: dict[str, PhaseRun] = {}
        self.handled: dict[str, int] = {}
        self._lock = threading.Lock()

    def click(self, phase: str, clicks: int) -> PhaseRun | None:
        """Start a phase once per button click. ``clicks`` is the button's
        running count; a redraw that re-runs the cell with the same count
        starts nothing, which is what keeps a timer from re-running a
        phase against the account."""
        if clicks <= self.handled.get(phase, 0):
            return self.phases.get(phase)
        self.handled[phase] = clicks
        return self.start(phase)

    # -- starting ---------------------------------------------------------
    def argv(self, phase: str) -> list[str]:
        # Plain replacement, not str.format, so an argument that carries
        # braces of its own (a python -c body, a JMESPath query) survives.
        return [a.replace("{phase}", phase).replace("{run_id}", self.run_id) for a in self.cli]

    def start(self, phase: str) -> PhaseRun:
        """Start a phase unless it is already running. Returns its record."""
        with self._lock:
            current = self.phases.get(phase)
            if current is not None and current.returncode is None:
                return current
            (self.run_dir / "stage").mkdir(parents=True, exist_ok=True)
            log = self.run_dir / "stage" / f"{phase}.log"
            env = dict(os.environ, TLMIG_AUTO="1", PYTHONUNBUFFERED="1", **(self.env or {}))
            fh = open(log, "w")
            proc = subprocess.Popen(self.argv(phase), stdout=fh, stderr=subprocess.STDOUT, env=env, cwd=os.getcwd())
            rec = PhaseRun(phase, self.run_id, log, datetime.now(timezone.utc), proc)
            self.phases[phase] = rec
            threading.Thread(target=self._reap, args=(rec, fh), daemon=True).start()
            return rec

    def _reap(self, rec: PhaseRun, fh) -> None:
        rec.proc.wait()
        fh.close()
        rec.returncode = rec.proc.returncode
        rec.ended = datetime.now(timezone.utc)

    # -- reading back -----------------------------------------------------
    def status(self, phase: str) -> str:
        """running / done / failed from this stage, else what events.jsonl
        says (a phase run from a terminal), else 'not started'."""
        rec = self.phases.get(phase)
        if rec is not None:
            return rec.status
        ev = self.run_dir / "events.jsonl"
        if ev.exists():
            seen = ""
            for raw in ev.read_text().splitlines():
                try:
                    e = json.loads(raw)
                except json.JSONDecodeError:
                    continue
                if e.get("kind") in ("phase", "phase_start", "phase_end") and (e.get("name") or e.get("phase")) == phase:
                    seen = "done" if (e.get("status") == "end" or e.get("kind") == "phase_end") else "running"
            if seen:
                return seen
        return "not started"

    def notes(self, phase: str) -> list[str]:
        """The narration the beats themselves emitted during a phase."""
        ev = self.run_dir / "events.jsonl"
        out: list[str] = []
        if not ev.exists():
            return out
        for raw in ev.read_text().splitlines():
            try:
                e = json.loads(raw)
            except json.JSONDecodeError:
                continue
            if e.get("phase") == phase and e.get("kind") in ("note", "fact"):
                out.append(e.get("text") or f"{e.get('label')}: {e.get('value')}")
        return out

    def tail(self, phase: str, lines: int = 12) -> str:
        rec = self.phases.get(phase)
        return rec.tail(lines) if rec else ""


def new_run_id() -> str:
    """Six hex characters, the shape config.Config expects."""
    return os.urandom(3).hex()


def wait(rec: PhaseRun, timeout: float = 600) -> int | None:
    """Block until a phase ends; for tests and unattended rehearsals."""
    deadline = time.monotonic() + timeout
    while rec.returncode is None and time.monotonic() < deadline:
        time.sleep(0.2)
    return rec.returncode
