"""The measurement harness — the demo's payload, captured live.

The claim is that planning one team's estate is far cheaper than planning the
whole monolith, because the monolith refreshes every resource and one estate
served from cache reads almost nothing. This measures both on the real
account by counting the provider HTTP requests a plan actually makes, under
TF_LOG, plus the cache hits and the wall-clock.

These are the LIVE numbers and they vary run to run — that is the point, they
are real. The reproducible figures the artifact quotes come from the floci
smokes via :mod:`receipt`, and are shown beside the live ones as a labelled
receipt, never dressed up as the same measurement.
"""

from __future__ import annotations

import dataclasses

from . import config, events, guard, ui

_HTTP = "HTTP Request Sent"
_CACHE_HIT = "state cache hit"


@dataclasses.dataclass(frozen=True)
class PlanMeasurement:
    estate: str
    refresh: bool
    requests: int
    cache_hits: int
    seconds: float

    @property
    def label(self) -> str:
        return f"{self.estate} ({'refresh' if self.refresh else 'refresh=false'})"


def measure_plan(cfg: config.Config, estate: str, *extra: str, refresh: bool, label: str = "") -> PlanMeasurement:
    """Plan one estate under TF_LOG=debug and count what crossed the wire.

    The provider's debug log is written to a file rather than parsed off
    stdout, so the plan's own -no-color output stays clean for the governance
    guard to read. Counting is a line count of two stable markers the fork
    prints: one per provider HTTP request, one per instance served from the
    state cache instead of a read.
    """
    workdir = cfg.workdir(estate)
    logpath = cfg.run_dir / "measure" / f"{estate}-{'refresh' if refresh else 'norefresh'}.log"
    logpath.parent.mkdir(parents=True, exist_ok=True)

    ui.say(
        f"planning {estate} "
        f"{'with a full refresh' if refresh else 'with -refresh=false (served from cache)'} "
        f"— counting provider requests"
    )
    res = guard.chdf(
        cfg, "plan", "-input=false", "-no-color", *extra,
        cwd=str(workdir), capture=True, check=False,
        env={"TF_LOG": "debug", "TF_LOG_PATH": str(logpath)},
    )
    if res.returncode not in (0, 2):  # 0 = no changes, 2 = changes present; both are valid plans
        raise guard.GuardError(f"plan of {estate} failed (exit {res.returncode})\n{res.stderr.strip()}")

    text = logpath.read_text(errors="replace") if logpath.exists() else ""
    requests = text.count(_HTTP)
    cache_hits = text.count(_CACHE_HIT)

    m = PlanMeasurement(estate=estate, refresh=refresh, requests=requests, cache_hits=cache_hits, seconds=res.seconds)
    events.measure(
        cfg, requests=requests, cache_hits=cache_hits, seconds=res.seconds,
        estate=estate, refresh=refresh, label=label or m.label,
    )
    ui.kv(f"{m.label}: requests", str(requests), good=not refresh)
    ui.kv(f"{m.label}: cache hits", str(cache_hits), good=cache_hits > 0)
    ui.kv(f"{m.label}: wall-clock", f"{res.seconds:.1f}s", good=not refresh)
    return m


def contrast(slow: PlanMeasurement, fast: PlanMeasurement) -> None:
    """Say the headline out loud: the whole monolith's plan against one
    estate's, as a ratio the room can hear."""
    if fast.requests == 0:
        ui.ok(f"one estate planned with zero provider reads; the monolith made {slow.requests}")
        return
    ratio = slow.requests / max(fast.requests, 1)
    ui.rule("the contrast")
    ui.kv("whole monolith, full refresh", f"{slow.requests} requests, {slow.seconds:.1f}s")
    ui.kv("one estate, -refresh=false", f"{fast.requests} requests, {fast.seconds:.1f}s", good=True)
    ui.kv("cheaper by", f"{ratio:.1f}x fewer requests", good=True)
