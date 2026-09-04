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
    if os.environ.get("CHOUDOUFU_VERSION") == "local":
        from . import localbuild
        built = localbuild.cached()
        return str(built) if built else ""   # empty: the CLI builds on first use
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
    if version:
        try:
            from . import localbuild
            return localbuild.fetch_release(version)
        except Exception:  # noqa: BLE001 - the CLI's preflight will say what is wrong
            pass
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

    @property
    def elapsed(self) -> float:
        """Seconds since this phase started: counts up while it runs, freezes
        once it ends. What a live 'Running X... Ns' cue counts."""
        end = self.ended or datetime.now(timezone.utc)
        return (end - self.started).total_seconds()

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
        self.env = dict(env or {})
        self.binary = binary if binary is not None else find_binary()
        if self.binary:
            self.env.setdefault("CHOUDOUFU_BIN", self.binary)
        # An empty binary with a local pin means the CLI builds it on first
        # use and caches it beside the example.
        self.phases: dict[str, PhaseRun] = {}
        self.handled: dict[str, int] = {}
        self.refused: tuple[str, str] | None = None   # (phase asked for, phase that was running)
        self._lock = threading.Lock()

    def running(self) -> str | None:
        """The name of the phase this stage is running right now, if any."""
        for name, rec in self.phases.items():
            if rec.returncode is None:
                return name
        return None

    def once(self, key: str, clicks: int) -> bool:
        """True exactly once per new click count for ``key``: the guard a
        cell uses around a write of its own (saving carve.json) so a redraw
        with the same count never repeats it."""
        if clicks <= self.handled.get(key, 0):
            return False
        self.handled[key] = clicks
        return True

    def click(self, phase: str, clicks: int, extra: list[str] | None = None, key: str | None = None) -> PhaseRun | None:
        """Start a phase once per button click. ``clicks`` is the button's
        running count; a redraw that re-runs the cell with the same count
        starts nothing, which is what keeps a timer from re-running a
        phase against the account. A click while another phase is still
        running is not served: two phases on one run directory would race
        each other's cache and record store. The click is forgotten, so
        the presenter clicks again once the running phase ends."""
        key = key or phase
        if clicks <= self.handled.get(key, 0):
            return self.phases.get(key)
        busy = self.running()
        if busy and busy != key:
            self.handled[key] = clicks
            self.refused = (key, busy)
            return None
        self.handled[key] = clicks
        return self.start(phase, extra, key)

    # -- starting ---------------------------------------------------------
    def argv(self, phase: str) -> list[str]:
        # Plain replacement, not str.format, so an argument that carries
        # braces of its own (a python -c body, a JMESPath query) survives.
        return [a.replace("{phase}", phase).replace("{run_id}", self.run_id) for a in self.cli]

    def start(self, phase: str, extra: list[str] | None = None, key: str | None = None) -> PhaseRun:
        """Start a phase unless it is already running. Returns its record.
        ``extra`` arguments follow the CLI's own (a seed's --config, --estate);
        ``key`` names the record when one verb is run in more than one way
        (seed:verify and seed:adopt both run the seed verb)."""
        key = key or phase
        with self._lock:
            current = self.phases.get(key)
            if current is not None and current.returncode is None:
                return current
            (self.run_dir / "stage").mkdir(parents=True, exist_ok=True)
            log = self.run_dir / "stage" / f"{key.replace(':', '-')}.log"
            env = dict(os.environ, TLMIG_AUTO="1", PYTHONUNBUFFERED="1", **(self.env or {}))
            fh = open(log, "w")
            proc = subprocess.Popen(self.argv(phase) + list(extra or []), stdout=fh, stderr=subprocess.STDOUT, env=env, cwd=os.getcwd())
            rec = PhaseRun(key, self.run_id, log, datetime.now(timezone.utc), proc)
            self.phases[key] = rec
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


_STAGES: dict[str, "Stage"] = {}


def for_run(run_id: str, **kw) -> "Stage":
    """The one Stage for a run id, however many times a notebook cell asks.
    A Stage holds the records of phases it started; a fresh object per cell
    re-run would forget them and, worse, serve a button's click again."""
    st = _STAGES.get(run_id)
    if st is None:
        st = _STAGES[run_id] = Stage(run_id, **kw)
    else:
        for k, v in kw.items():
            if k == "binary" and v is not None:
                st.binary = v
                st.env["CHOUDOUFU_BIN"] = v if v else st.env.get("CHOUDOUFU_BIN", "")
            elif k == "env" and v:
                st.env.update(v)
    return st


def new_run_id() -> str:
    """Six hex characters, the shape config.Config expects."""
    return os.urandom(3).hex()


def wait(rec: PhaseRun, timeout: float = 600) -> int | None:
    """Block until a phase ends; for tests and unattended rehearsals."""
    deadline = time.monotonic() + timeout
    while rec.returncode is None and time.monotonic() < deadline:
        time.sleep(0.2)
    return rec.returncode


def available_phases(cli: list[str] | None = None) -> set[str]:
    """The phase names the CLI knows, read from its own --help, so the page
    can offer a seed or a preview only once the CLI has them."""
    import re
    argv = [a for a in (cli or CLI) if a not in ("{phase}", "--run", "{run_id}", "--auto")] + ["--help"]
    try:
        out = subprocess.run(argv, capture_output=True, text=True, timeout=30).stdout
    except (OSError, subprocess.TimeoutExpired):
        return set()
    # argparse wraps a long choices list across lines, so whitespace inside
    # the braces is allowed and stripped from each name.
    m = re.search(r"\{([a-z0-9,\-\s]+)\}", out)
    return {n.strip() for n in m.group(1).split(",") if n.strip()} if m else set()


# The workflow the page presents, in order. Each phase runs one or more CLI
# verbs; which ones depends on the verbs the installed tlmig has, so the page
# reads the same before and after the CLI's rename from the demo's beat names.
WORKFLOW = ["seed", "survey", "plan", "preview", "move", "verify", "receipt", "teardown"]

# Verbs that write to the cloud, under either naming.
WRITES = {"setup", "seed", "decompose", "carve", "move", "teardown"}


def verbs_for(phase: str, available: set[str] | None = None) -> list[str]:
    """The CLI verbs a workflow phase runs, given the verbs the CLI has.
    Preflight opens the seed under either naming; plan is the page's own
    (it writes carve.json) and runs no verb; preview needs its verb."""
    have = available or set()
    if phase == "seed":
        return ["preflight", "seed" if "seed" in have else "setup"]
    if phase == "survey":
        return ["survey"] if "survey" in have else ["slow-plan"]
    if phase == "plan":
        return []
    if phase == "preview":
        return ["preview"] if "preview" in have else []
    if phase == "move":
        return ["move"] if "move" in have else ["decompose", "carve"]
    if phase == "verify":
        return ["verify"] if "verify" in have else ["fast-plan", "guard"]
    if phase in ("receipt", "teardown"):
        return [phase]
    return []


def phase_of(verb: str, available: set[str] | None = None) -> str | None:
    """The workflow phase a verb belongs to, under the current naming."""
    for phase in WORKFLOW:
        if verb in verbs_for(phase, available):
            return phase
    return None
