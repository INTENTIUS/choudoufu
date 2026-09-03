"""The reproducible receipt: the numbers the artifact and the docs quote,
read off the claim smokes' own evidence lines rather than retyped.

Two sources. ``capture`` runs a claim smoke on the emulator with the pinned
release and saves its output under the run directory; ``parse_carve`` reads
carve-by-retag's evidence lines into a ``CarveReceipt``. The real-account
CloudTrail receipt for claim 13 is a file the repository already carries,
``live/smoke/evidence/the-tag-is-the-boundary.cloudtrail.json``;
``load_cloudtrail`` reads it. ``read_receipt`` combines them.

The emulator's numbers are the emulator's: the tagging index there covers
IAM, a real account's does not, so the request counts here are the
reproducible receipt, labelled as such, never presented as a live run's
figure. measure.py owns the live column.

The line shapes are this release's, and tests/test_receipt.py pins them
with the exact lines the smoke printed, so a moved line fails there first.
"""

from __future__ import annotations

import dataclasses
import datetime
import json
import os
import pathlib
import re
import shlex
import subprocess

from . import config, events, ui

# tlmig/receipt.py -> tlmig -> terralith-migration -> examples -> repo root
REPO_ROOT = pathlib.Path(__file__).resolve().parents[3]
CLOUDTRAIL_EVIDENCE = REPO_ROOT / "live" / "smoke" / "evidence" / "the-tag-is-the-boundary.cloudtrail.json"

_STAMPED = re.compile(r"^\s*(\d+) of (\d+) resource instance\(s\) are eligible for stamping", re.M)
_MONO_PLAN = re.compile(r"the monolith plans clean with the state file gone: (\d+) requests", re.M)
_COST = re.compile(r"monolith plan: (\d+) requests\s+·\s+team estate plan: (\d+) requests", re.M)
_DESTROYED = re.compile(r"^\s*(\w+): (\d+) destroyed$", re.M)
_RETAG = re.compile(r'tofu-estate\s+"([^"]+)" -> "([^"]+)"', re.M)
_ROLE_TAG = re.compile(r"aws iam list-role-tags (\S+): tofu-estate=(\S+?);?(?: inline policies still attached: (\S+))?$", re.M)
_PASS = re.compile(r"^PASS: smoke scenario '([^']+)'", re.M)


@dataclasses.dataclass(frozen=True)
class CarveReceipt:
    """carve-by-retag's numbers, as printed."""

    scenario: str
    passed: bool
    stamped: int                    # 38
    declared: int                   # 79
    monolith_plan_requests: int     # 166
    carved_plan_requests: int       # 39
    destroyed: dict[str, int]       # {"monolith": 71, "iam": 2, "team1": 6}
    retags: tuple[tuple[str, str], ...]        # (("tl-terralith", "tl-team-1"), ...)
    role_estates: dict[str, str]    # {"tl-team-0001-role": "tl-team-1", "tl-svc-0000-exec-role": "tl-iam"}

    @property
    def followed(self) -> tuple[int, ...]:
        """Destroyed per estate in the order printed: (71, 2, 6)."""
        return tuple(self.destroyed.values())

    @property
    def total_destroyed(self) -> int:
        return sum(self.destroyed.values())


def parse_carve(text: str) -> CarveReceipt:
    """Read a saved carve-by-retag run. Every number is required; a missing
    line raises, because a receipt with a hole is not a receipt."""
    st = _STAMPED.search(text)
    mono = _MONO_PLAN.search(text)
    cost = _COST.search(text)
    if not (st and mono and cost):
        missing = [n for n, m in (("stamping", st), ("monolith plan", mono), ("plan cost", cost)) if not m]
        raise ValueError(f"carve-by-retag output is missing its {', '.join(missing)} line(s)")
    destroyed = {name: int(n) for name, n in _DESTROYED.findall(text)}
    if not destroyed:
        raise ValueError("carve-by-retag output has no '<estate>: N destroyed' lines")
    passed = _PASS.search(text)
    return CarveReceipt(
        scenario="carve-by-retag",
        passed=bool(passed),
        stamped=int(st.group(1)),
        declared=int(st.group(2)),
        monolith_plan_requests=int(mono.group(1)),
        carved_plan_requests=int(cost.group(2)),
        destroyed=destroyed,
        retags=tuple(_RETAG.findall(text)),
        role_estates={role: est for role, est, _ in _ROLE_TAG.findall(text)},
    )


@dataclasses.dataclass(frozen=True)
class CloudTrailEvent:
    time: str
    role: str
    resource: str
    tags: dict[str, str]
    error: str | None

    @property
    def denied(self) -> bool:
        return self.error is not None


@dataclasses.dataclass(frozen=True)
class CloudTrailReceipt:
    """Claim 13's real-account receipt as the repository carries it."""

    account: str
    region: str
    captured: str
    events: tuple[CloudTrailEvent, ...]

    @property
    def denied(self) -> tuple[CloudTrailEvent, ...]:
        return tuple(e for e in self.events if e.denied)


def load_cloudtrail(path: pathlib.Path = CLOUDTRAIL_EVIDENCE) -> CloudTrailReceipt:
    doc = json.loads(path.read_text())
    events = []
    for e in doc.get("events", []):
        arn = e.get("userIdentity.arn", "")
        role = arn.split("/")[-2] if "assumed-role/" in arn else arn
        events.append(CloudTrailEvent(
            time=e.get("eventTime", ""),
            role=role,
            resource=", ".join(e.get("resources", [])),
            tags=dict(e.get("tags", {})),
            error=e.get("errorCode"),
        ))
    return CloudTrailReceipt(
        account=str(doc.get("account", "")),
        region=str(doc.get("region", "")),
        captured=str(doc.get("captured", "")),
        events=tuple(events),
    )


@dataclasses.dataclass(frozen=True)
class Receipt:
    carve: CarveReceipt
    cloudtrail: CloudTrailReceipt | None
    source: pathlib.Path

    def lines(self) -> list[tuple[str, str, bool | None]]:
        c = self.carve
        out: list[tuple[str, str, bool | None]] = [
            ("smoke", f"{c.scenario} {'PASS' if c.passed else 'no PASS line'}", c.passed),
            ("stamped by live-import", f"{c.stamped} of {c.declared}, 0 by hand", None),
            ("monolith plan", f"{c.monolith_plan_requests} requests", False),
            ("carved estate plan", f"{c.carved_plan_requests} requests", True),
            ("destroyed", " + ".join(str(n) for n in c.followed) + f" = {c.total_destroyed}", None),
        ]
        if self.cloudtrail:
            d = self.cloudtrail.denied
            out.append(("CloudTrail receipt", f"{len(self.cloudtrail.events)} CreateTags, {len(d)} denied, account {self.cloudtrail.account} {self.cloudtrail.region}", None))
        return out


def _transcript(cfg: config.Config, argv: list[str], cwd: pathlib.Path) -> None:
    cfg.run_dir.mkdir(parents=True, exist_ok=True)
    stamp = datetime.datetime.now().isoformat(timespec="seconds")
    with cfg.transcript_path.open("a") as fh:
        fh.write(f"{stamp}  {shlex.join(argv)} (in {cwd})\n")


def capture(cfg: config.Config, scenario: str = "carve-by-retag") -> pathlib.Path:
    """Run one claim smoke on the emulator with the pinned release and save
    its output under runs/<id>/receipts/. Needs Docker, the AWS CLI and Go;
    about six minutes for carve-by-retag. The emulator, never the fenced
    account: the smoke's own stack, its own credentials, nothing of this
    run's prefix."""
    out = cfg.run_dir / "receipts" / f"{scenario}.log"
    out.parent.mkdir(parents=True, exist_ok=True)
    argv = ["bash", "live/smoke/smoke.sh", scenario]
    env = dict(os.environ, CHOUDOUFU_VERSION=cfg.version)
    ui.cmd(f"CHOUDOUFU_VERSION={cfg.version} {' '.join(argv)}   # in {REPO_ROOT}")
    _transcript(cfg, argv, REPO_ROOT)
    with out.open("w") as fh:
        proc = subprocess.run(argv, cwd=REPO_ROOT, env=env, stdout=fh, stderr=subprocess.STDOUT, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"smoke scenario {scenario} exited {proc.returncode}; output in {out}")
    ui.ok(f"{scenario} passed on the emulator; output saved to {out}")
    return out


def read_receipt(cfg: config.Config, log: pathlib.Path | None = None) -> Receipt:
    """The reproducible receipt. Reads a saved carve-by-retag log, or the
    latest one under this run, and the repository's CloudTrail evidence."""
    log = log or (cfg.run_dir / "receipts" / "carve-by-retag.log")
    if not log.exists():
        raise FileNotFoundError(f"no saved carve-by-retag run at {log}; run receipt.capture(cfg) first")
    carve = parse_carve(log.read_text())
    ct = load_cloudtrail() if CLOUDTRAIL_EVIDENCE.exists() else None
    receipt = Receipt(carve=carve, cloudtrail=ct, source=log)
    cfg.run_dir.mkdir(parents=True, exist_ok=True)
    (cfg.run_dir / "receipt.json").write_text(json.dumps({
        "carve": dataclasses.asdict(carve),
        "cloudtrail": dataclasses.asdict(ct) if ct else None,
        "source": str(log),
    }, indent=2, default=str))
    events.receipt(cfg, {"carve": carve, "cloudtrail": ct, "source": str(log)})
    for label, value, good in receipt.lines():
        ui.kv(label, value, good)
    return receipt
