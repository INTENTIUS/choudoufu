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

import base64
import dataclasses
import datetime
import json
import os
import pathlib
import re
import shlex
import subprocess
import time

from . import config, events, ui, guard

# tlmig/receipt.py -> tlmig -> live-mv-workbench -> examples -> repo root
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
    event: str = ""      # the API call as CloudTrail names it: TagRole, TagResource, CreateTags

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


# The write calls a retag makes on this example's resource types, as
# CloudTrail names them. Ownership moves are tag writes and nothing else.
TAG_WRITE_EVENTS = ("TagRole", "UntagRole", "TagPolicy", "UntagPolicy", "TagResource", "UntagResource",
                    "TagLogGroup", "UntagLogGroup", "CreateTags", "DeleteTags")


def run_started(cfg: config.Config) -> datetime.datetime:
    """When this run began, from its own event feed; an hour ago if the feed
    is empty, so a lookup never misses the run by a narrow window."""
    try:
        lines = events.read(cfg)
        first = lines[0]["ts"]
        return datetime.datetime.fromisoformat(first.replace("Z", "+00:00"))
    except Exception:  # noqa: BLE001 - no feed yet
        return datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(hours=1)


def _principal(arn: str) -> str:
    if "assumed-role/" in arn:
        return arn.split("assumed-role/")[-1]
    if ":user/" in arn:
        return "user " + arn.split(":user/")[-1]
    return arn.rsplit(":", 1)[-1] or arn


def _parse_trail_event(raw: dict) -> CloudTrailEvent | None:
    try:
        ev = json.loads(raw["CloudTrailEvent"])
    except (KeyError, ValueError):
        return None
    rp = ev.get("requestParameters") or {}
    tags = {}
    for t in (rp.get("tags") or []):
        if isinstance(t, dict):
            k, v = t.get("key", t.get("Key")), t.get("value", t.get("Value"))
            if k is not None:
                tags[str(k)] = str(v)
    if isinstance(rp.get("tags"), dict):
        tags = {str(k): str(v) for k, v in rp["tags"].items()}
    resource = (rp.get("roleName") or rp.get("policyArn") or rp.get("resourceArn") or rp.get("logGroupName")
                or (raw.get("Resources") or [{}])[0].get("ResourceName") or "")
    return CloudTrailEvent(
        time=str(ev.get("eventTime") or raw.get("EventTime") or ""),
        role=_principal(str((ev.get("userIdentity") or {}).get("arn") or "")),
        resource=str(resource),
        tags=tags,
        error=ev.get("errorCode"),
        event=str(ev.get("eventName") or raw.get("EventName") or ""),
    )


def lookup_run_cloudtrail(cfg: config.Config, *, since: datetime.datetime | None = None, max_wait: int = 120,
                          poll: int = 20, min_events: int = 1) -> CloudTrailReceipt:
    """This run's own tag writes, read back from the account's CloudTrail
    event history: every TagRole, TagPolicy, TagResource and kin since the
    run began whose record names something carrying the run's prefix. Event
    history lags a write by a minute or so, so this polls, up to max_wait
    seconds, until at least min_events retags are visible. The retags this
    beat reads happened minutes earlier (decompose, then carve), so in
    practice the first lookup finds them; the cap is two minutes so a beat
    never holds a room, and a lookup that finds nothing in time returns an
    empty receipt rather than failing, so the beat can say so."""
    since = since or run_started(cfg)
    start = (since - datetime.timedelta(minutes=2)).strftime("%Y-%m-%dT%H:%M:%SZ")
    deadline = time.monotonic() + max_wait
    found: dict[str, CloudTrailEvent] = {}
    while True:
        for name in TAG_WRITE_EVENTS:
            token = None
            for _page in range(10):
                # Event history is regional and IAM's global events land in
                # us-east-1, which is this example's region; name it, because
                # the CLI's default region is whatever the profile says.
                argv = ["cloudtrail", "lookup-events", "--region", cfg.region,
                        "--lookup-attributes", f"AttributeKey=EventName,AttributeValue={name}",
                        "--start-time", start, "--max-results", "50", "--output", "json"]
                if token:
                    argv += ["--next-token", token]
                res = guard.aws(cfg, *argv, check=False)
                if not res.ok or not res.stdout.strip():
                    break
                try:
                    doc = json.loads(res.stdout)
                except ValueError:
                    break
                for raw in doc.get("Events") or []:
                    if cfg.prefix not in raw.get("CloudTrailEvent", ""):
                        continue
                    ev = _parse_trail_event(raw)
                    if ev is not None:
                        found[raw.get("EventId") or f"{ev.time}/{ev.resource}"] = ev
                token = doc.get("NextToken")
                if not token:
                    break
        retags = [e for e in found.values() if "tofu-estate" in e.tags]
        if len(retags) >= min_events or time.monotonic() >= deadline:
            break
        ui.kv("CloudTrail", f"{len(found)} of this run's writes visible so far; event history lags, waiting {poll}s", None)
        time.sleep(poll)
    ordered = sorted(found.values(), key=lambda e: e.time)
    return CloudTrailReceipt(account=cfg.account_id, region=cfg.region,
                            captured=datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                            events=tuple(ordered))


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


def read_record_store(cfg: config.Config) -> dict[str, list[str]]:
    """choudoufu's own record of what each estate owns, read straight from the
    on-disk record store (``.tofu-records``). Each recorded resource is a file
    whose name is the base64 of its address, so this returns
    ``{estate: [address, ...]}``.

    This is the tool's own record; CloudTrail is the account's independent one.
    The receipt shows both so a viewer can see two parties recording the same
    moves and agreeing - the store says which estate each resource now belongs
    to, the log says the tag writes that put it there."""
    out: dict[str, list[str]] = {}
    est_root = cfg.run_dir / "estates"
    if not est_root.is_dir():
        return out
    for workdir in sorted(est_root.iterdir()):
        store = workdir / ".tofu-records" / "tofu-records"
        if not store.is_dir():
            continue
        addrs: set[str] = set()
        for f in store.rglob("*"):
            if not f.is_file() or f.name.startswith("."):
                continue  # skip the .store-sentinel and any dotfile
            try:
                addrs.add(base64.b64decode(f.name + "=" * (-len(f.name) % 4)).decode())
            except Exception:  # noqa: BLE001 - an unreadable record name is skipped, never fatal
                continue
        if addrs:
            out[workdir.name] = sorted(addrs)
    return out
