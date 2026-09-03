"""The visual aid: a run directory rendered as one picture.

Reads what a run leaves on disk (``manifest.json``, ``events.jsonl``,
``receipt.json``, each estate's ``main.tf``) and renders it as self-contained
HTML with inline SVG, so the same string shows in a marimo cell, in a browser
tab, or in a test. Nothing here talks to AWS and nothing imports the beats:
the picture is a function of files, which is what lets a recorded run be
replayed one phase per cell after the fact.

The persistent picture is the estate-ownership map: every resource the run
declares as a cell coloured by the estate whose ``tofu-estate`` tag it
carries, untaggable children (inline policies, attachments) coloured by their
parent role's live tag, because that is the rule the engine follows. Beside it
sits the ledger, every command the run made and the platform's answer, which
grows into the audit story when the receipt phase adds CloudTrail rows.

Stdlib only. Tolerant of the two event spellings the orchestration discussed
(``phase``/``cmd``/``measure``/``note`` and ``phase_start``/``command``/
``measurement``/``fact``), so a renderer never blocks a beat.
"""
from __future__ import annotations

import dataclasses
import html
import json
import pathlib
import re
from datetime import datetime, timezone

# ----------------------------------------------------------------------------
# Model
# ----------------------------------------------------------------------------

PHASES = ("preflight", "setup", "slow-plan", "decompose", "fast-plan", "carve", "guard", "receipt", "teardown")

# Types that carry no tags of their own; drawn attached to the parent whose
# live tag names their estate.
UNTAGGABLE = {"aws_iam_role_policy", "aws_iam_role_policy_attachment"}

# Column order inside a team row, by resource type. Unknown types append.
COLUMNS = (
    "aws_iam_role",
    "iam:role",
    "aws_iam_role_policy",
    "aws_iam_policy",
    "iam:policy",
    "aws_iam_role_policy_attachment",
    "aws_cloudwatch_log_group",
    "logs:log-group",
)
SHORT = {
    "iam:role": "role",
    "iam:policy": "policy",
    "logs:log-group": "log",
    "ec2:instance": "instance",
    "aws_iam_role": "role",
    "aws_iam_role_policy": "inline",
    "aws_iam_policy": "policy",
    "aws_iam_role_policy_attachment": "attach",
    "aws_cloudwatch_log_group": "log",
    "aws_instance": "instance",
}


@dataclasses.dataclass
class Resource:
    address: str            # tofu-address, e.g. aws_iam_role.team_a
    type: str               # aws_iam_role
    name: str               # team_a
    team: str               # team-a (from the address's name)
    declared_in: str        # estate whose config declares it
    estate: str | None = None   # estate the live tag names (None: unseen)
    parent: str | None = None   # address of the parent for untaggable types
    id: str | None = None       # ARN or name from the inventory
    gone: bool = False          # listed once, absent from the latest listing

    @property
    def taggable(self) -> bool:
        return self.type not in UNTAGGABLE

    @property
    def key(self) -> str:
        return self.id or self.address


@dataclasses.dataclass
class Phase:
    name: str
    title: str = ""
    started: datetime | None = None
    ended: datetime | None = None
    seconds: float | None = None

    @property
    def status(self) -> str:
        if self.ended:
            return "done"
        if self.started:
            return "active"
        return "pending"


@dataclasses.dataclass
class LedgerRow:
    ts: datetime | None
    phase: str
    actor: str
    action: str
    target: str
    answer: str
    ok: bool | None          # True good, False refused/failed, None neutral
    write: bool              # a mutation, as opposed to a read


@dataclasses.dataclass
class Measure:
    estate: str
    label: str
    requests: int
    refresh: bool | None
    cache_hits: int | None
    seconds: float | None
    reference: dict


@dataclasses.dataclass
class RunState:
    run_id: str
    prefix: str
    region: str
    phases: list[Phase]
    resources: dict[str, Resource]         # by address
    estates: list[str]                     # in first-seen order
    ledger: list[LedgerRow]
    measures: list[Measure]
    verdicts: list[dict]
    notes: list[tuple[str, str]]           # (phase, text)
    events_seen: int
    last_ts: datetime | None
    previews: list[dict] = dataclasses.field(default_factory=list)   # events.preview, one per planned move
    record_store: dict = dataclasses.field(default_factory=dict)     # {estate: [address,...]} from .tofu-records, from the receipt event

    @property
    def active_phase(self) -> Phase | None:
        for p in reversed(self.phases):
            if p.status == "active":
                return p
        return None

    def estate_of(self, key: str) -> str | None:
        """The estate a resource sits in right now, by key (id, else
        address). Untaggable children take their parent's answer."""
        r = self.resources.get(key) or self.by_address.get(key)
        if r is None or r.gone:
            return None
        if r.taggable:
            return r.estate or r.declared_in
        parent = self.by_address.get(r.parent) if r.parent else None
        if parent is not None:
            return None if parent.gone else self.estate_of(parent.key)
        return r.estate or r.declared_in

    @property
    def by_address(self) -> dict[str, Resource]:
        """Address -> resource; the first one wins when configs share an
        address, which only the synthetic fixtures do."""
        out: dict[str, Resource] = {}
        for r in self.resources.values():
            out.setdefault(r.address, r)
        return out


# ----------------------------------------------------------------------------
# Loading
# ----------------------------------------------------------------------------

_RES_BLOCK = re.compile(r'^resource\s+"([^"]+)"\s+"([^"]+)"\s*\{', re.M)
_ROLE_REF = re.compile(r'\brole\s*=\s*aws_iam_role\.([A-Za-z0-9_-]+)\.')
_ESTATE = re.compile(r'estate\s*=\s*"([^"]+)"')


def _parse_ts(s: str | None) -> datetime | None:
    if not s:
        return None
    try:
        dt = datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt


def _team_of(name: str, ident: str | None = None) -> str:
    """team_a, team_a_inline, team_a_0 -> team-a; else the team named in the
    id (a role name or log-group path); else the name itself."""
    m = re.match(r"^(team_[a-z0-9]+?)(?:_inline|_[0-9]+)?$", name)
    if m:
        return m.group(1).replace("_", "-")
    if ident:
        m = re.search(r"(team-[a-z0-9]+)", ident)
        if m:
            return m.group(1)
    return name


def _declared(estate_dir: pathlib.Path, estate: str, into: dict[str, Resource]) -> None:
    """Every resource block in an estate's config, with parent links for the
    untaggable children."""
    main = estate_dir / "main.tf"
    if not main.exists():
        return
    text = main.read_text()
    blocks = [(m.group(1), m.group(2), m.end()) for m in _RES_BLOCK.finditer(text)]
    for i, (rtype, rname, start) in enumerate(blocks):
        end = blocks[i + 1][2] if i + 1 < len(blocks) else len(text)
        body = text[start:end]
        addr = f"{rtype}.{rname}"
        parent = None
        if rtype in UNTAGGABLE:
            m = _ROLE_REF.search(body)
            if m:
                parent = f"aws_iam_role.{m.group(1)}"
        r = into.get(addr)
        if r is None:
            into[addr] = Resource(address=addr, type=rtype, name=rname, team=_team_of(rname),
                                  declared_in=estate, parent=parent)
        else:
            # A block that moved between configs: the newest config wins as
            # the declarer; the live tag decides the colour regardless.
            r.declared_in = estate
            r.parent = r.parent or parent


def _summarise_argv(argv: list[str], cwd: str | None) -> tuple[str, str, bool]:
    """(action, target, is_write) from an argv the guard ran."""
    if not argv:
        return ("", "", False)
    prog = pathlib.Path(argv[0]).name
    rest = [a for a in argv[1:] if not a.startswith("-")]
    estate = pathlib.Path(cwd).name if cwd else ""
    if prog == "choudoufu" or prog == "tofu":
        verb = rest[0] if rest else ""
        if verb == "live-mv":
            frm = next((a.split("=", 1)[1] for a in argv if a.startswith("-from-estate=")), "")
            addr = rest[1] if len(rest) > 1 else ""
            return (f"live-mv from {frm}", f"{addr} -> {estate}", True)
        if verb in ("apply", "destroy"):
            return (f"choudoufu {verb}", estate, True)
        if verb == "plan":
            mode = "-refresh=false" if "-refresh=false" in argv else "full refresh"
            return (f"choudoufu plan ({mode})", estate, False)
        return (f"choudoufu {verb}", estate, verb not in ("init", "version", "show"))
    if prog == "aws":
        svc = rest[0] if rest else ""
        op = rest[1] if len(rest) > 1 else ""
        write = not re.match(r"^(get|list|describe|lookup)", op)
        target = next((argv[i + 1] for i, a in enumerate(argv) if a in ("--role-name", "--resources", "--resource-arn") and i + 1 < len(argv)), "")
        return (f"aws {svc} {op}", target, write)
    return (" ".join(argv[:2]), "", False)


def load_run(run_dir: str | pathlib.Path, upto: int | None = None) -> RunState:
    """Build the state of a run from its directory. ``upto`` replays only the
    first N events, which is how a phase-per-cell notebook shows the picture
    as it stood when that phase ended."""
    run_dir = pathlib.Path(run_dir)
    manifest = {}
    if (run_dir / "manifest.json").exists():
        manifest = json.loads((run_dir / "manifest.json").read_text())
    run_id = manifest.get("run_id")
    prefix = manifest.get("prefix")
    region = manifest.get("region", "")
    expected_phases = tuple(manifest.get("phases") or PHASES)

    resources: dict[str, Resource] = {}
    estates: list[str] = []
    estates_dir = run_dir / "estates"
    if estates_dir.is_dir():
        for d in sorted(estates_dir.iterdir()):
            if (d / "main.tf").exists():
                m = _ESTATE.search((d / "main.tf").read_text())
                estate = m.group(1) if m else d.name
                if estate not in estates:
                    estates.append(estate)
                _declared(d, estate, resources)

    phases: dict[str, Phase] = {n: Phase(name=n) for n in expected_phases}
    seen_order: list[str] = []
    ledger: list[LedgerRow] = []
    measures: list[Measure] = []
    verdicts: list[dict] = []
    notes: list[tuple[str, str]] = []
    previews: list[dict] = []
    record_store: dict = {}
    seen = 0
    last_ts = None
    current_phase = ""

    events_path = run_dir / "events.jsonl"
    lines = events_path.read_text().splitlines() if events_path.exists() else []
    for raw in lines:
        if upto is not None and seen >= upto:
            break
        raw = raw.strip()
        if not raw:
            continue
        try:
            ev = json.loads(raw)
        except json.JSONDecodeError:
            continue
        seen += 1
        ts = _parse_ts(ev.get("ts"))
        last_ts = ts or last_ts
        kind = ev.get("kind", "")
        phase_name = ev.get("phase") or current_phase or ""
        if run_id is None and ev.get("run_id"):
            run_id = str(ev["run_id"])

        if kind in ("phase", "phase_start", "phase_end"):
            name = ev.get("name") or ev.get("phase") or phase_name
            status = ev.get("status") or ("start" if kind == "phase_start" else "end")
            p = phases.setdefault(name, Phase(name=name))
            p.title = ev.get("title") or p.title
            if name not in seen_order:
                seen_order.append(name)
            if status == "start":
                p.started = ts
                current_phase = name
            else:
                p.ended = ts
                p.seconds = ev.get("seconds", p.seconds)
                if p.started and p.ended and p.seconds is None:
                    p.seconds = (p.ended - p.started).total_seconds()
                current_phase = ""
        elif kind in ("cmd", "command"):
            argv = ev.get("argv") or []
            action, target, write = _summarise_argv(argv, ev.get("cwd"))
            label = ev.get("label")
            rc = ev.get("returncode", ev.get("rc"))
            ok = None if rc is None else rc == 0
            answer = "ok" if ok else ("" if rc is None else f"exit {rc}")
            secs = ev.get("seconds")
            if secs is not None:
                answer = f"{answer} · {secs:.1f}s".strip(" ·")
            ledger.append(LedgerRow(ts, phase_name, ev.get("actor") or "account",
                                    label or action, target, answer, ok, write))
        elif kind == "inventory":
            estate = ev.get("estate", "")
            if estate and estate not in estates:
                estates.append(estate)
            if prefix is None and estate:
                prefix = re.sub(r"-(monolith|team-[a-z0-9]+)$", "", estate)
            listed: set[str] = set()
            for item in ev.get("items") or []:
                addr = item.get("address") or ""
                ident = item.get("id")
                tags = item.get("tags") or {}
                live_estate = tags.get("tofu-estate") or estate
                if not addr and not ident:
                    continue
                key = ident or addr
                r = resources.get(key)
                if r is None and not ident:
                    r = resources.get(addr)
                if r is None:
                    # A declared resource sighted for the first time keeps its
                    # config-derived entry, re-keyed by id.
                    declared = next((d for d in resources.values() if d.id is None and d.address == addr), None)
                    if declared is not None:
                        resources.pop(declared.key, None)
                        r = declared
                    else:
                        rtype, _, rname = addr.partition(".") if addr else ("?", "", ident or "?")
                        r = Resource(address=addr or (ident or "?"), type=rtype, name=rname,
                                     team=_team_of(rname, ident), declared_in=live_estate)
                    r.id = ident or r.id
                    resources[r.key] = r
                r.estate = live_estate
                r.gone = False
                if r.team == r.name and ident:
                    r.team = _team_of(r.name, ident)
                listed.add(r.key)
                if live_estate not in estates:
                    estates.append(live_estate)
            # Anything this estate held that the listing no longer shows is
            # gone from it: destroyed, or moved and about to be listed
            # elsewhere in the same batch (which un-marks it).
            for r in resources.values():
                if r.taggable and r.id and r.estate == estate and r.key not in listed:
                    r.gone = True
        elif kind == "fact":
            label, value = str(ev.get("label", "")), str(ev.get("value", ""))
            # "role <name> -> estate" facts move a role on the map.
            m = re.match(r"^(?:role\s+)?(\S+?)\s*(?:->|:)\s*(\S+)$", f"{label} -> {value}")
            role = re.sub(r"^role[:\s]+", "", label)
            for r in resources.values():
                if r.type in ("aws_iam_role", "iam:role") and (role == r.id or (r.id or "").endswith("/" + role) or role.endswith(r.name) or role.endswith(r.address)):
                    r.estate = value
            notes.append((phase_name, f"{label}: {value}"))
        elif kind in ("measure", "measurement"):
            estate = ev.get("estate", "")
            refresh = ev.get("refresh")
            label = ev.get("label") or (f"{estate} plan" + (" (-refresh=false)" if refresh is False else " (full refresh)" if refresh else ""))
            measures.append(Measure(estate, label, int(ev.get("requests", 0)), refresh,
                                    ev.get("cache_hits"), ev.get("seconds"), ev.get("reference") or {}))
        elif kind == "verdict":
            name = ev.get("name", "")
            body = ev.get("verdict")
            if body is None:
                body = {"ok": ev.get("ok"), "lines": ev.get("lines", [])}
            elif isinstance(body, dict):
                # ok and lines may ride beside the dataclass rather than in it.
                body = {**body}
                body.setdefault("ok", ev.get("ok"))
                if "lines" in ev and "lines" not in body:
                    body["lines"] = ev["lines"]
            entry = {"name": name, "phase": phase_name, "ts": ts, **(body if isinstance(body, dict) else {"value": body})}
            if str(name).startswith("carve") and isinstance(body, dict):
                # The two plans the guard ran, source first, then destination:
                # their targets name the estates the verdict does not carry.
                planned = [r.target for r in ledger if r.phase == phase_name and "plan" in r.action and r.target]
                if not planned:
                    # A recording without the plan commands: the verdict's own
                    # lines name the estates ("<estate> plan: No changes.").
                    planned = [m.group(1) for ln in (entry.get("lines") or []) for m in [re.match(r"^(\S+) plan:", str(ln))] if m]
                dest = body.get("expected_estate") or (list(body.get("moved_estates", {}).values()) or [None])[0]
                src = next((t for t in planned if t != dest), planned[0] if planned else None)
                entry.setdefault("source_estate", src)
                entry.setdefault("destination_estate", dest or (planned[1] if len(planned) > 1 else None))
            verdicts.append(entry)
            ok = body.get("ok") if isinstance(body, dict) else None
            if ok is None and isinstance(body, dict):
                ok = body.get("clean")
            ledger.append(LedgerRow(ts, phase_name, "guard", f"verdict {name}", "", "holds" if ok else "FAILS" if ok is False else "", ok, False))
            # A carve verdict names where each role now lives.
            moved = body.get("moved_estates") if isinstance(body, dict) else None
            if isinstance(moved, dict):
                for role, est in moved.items():
                    for r in resources.values():
                        if r.type in ("aws_iam_role", "iam:role") and (role == r.id or (r.id or "").endswith("/" + role) or role.endswith(r.name) or role.endswith(r.address.split(".")[-1])):
                            r.estate = est
        elif kind == "receipt":
            rec = ev.get("receipt") or {}
            _store = rec.get("record_store")
            if isinstance(_store, dict):
                record_store = _store
            ct = rec.get("cloudtrail") or {}
            for e in ct.get("events") or []:
                _arn = e.get("role") or e.get("userIdentity.arn") or ""
                who = (_arn.split("assumed-role/")[-1].split("/")[0] if "assumed-role/" in _arn
                       else "user " + _arn.split(":user/")[-1] if ":user/" in _arn else _arn.split("/")[-1] or "?")
                tags = e.get("tags") or {}
                tagtxt = ", ".join(f"{k}={v}" for k, v in tags.items()) if isinstance(tags, dict) else str(tags)
                err = e.get("error") or e.get("errorCode")
                target = e.get("resource") or ", ".join(e.get("resources") or [])
                call = e.get("event") or e.get("eventName") or "tag write"
                ledger.append(LedgerRow(_parse_ts(e.get("time") or e.get("eventTime")), phase_name, who,
                                        f"CloudTrail {call} {tagtxt}", target,
                                        err or "recorded", not err, True))
        elif kind == "note":
            notes.append((phase_name, str(ev.get("text", ""))))
        elif kind == "preview":
            # A planned move as live-mv -dry-run judged it: the tag writes it
            # would make, the children that follow, and any refusal. A later
            # preview of the same address replaces an earlier one.
            body = {k: v for k, v in ev.items() if k not in ("ts", "run_id", "kind")}
            body["phase"] = phase_name
            # e9's shape: address is the destination's, old_address the
            # source's; equal for a plain change of owner.
            key = body.get("old_address") or body.get("address") or ""
            previews[:] = [p for p in previews if (p.get("old_address") or p.get("address")) != key]
            previews.append(body)

    # The strip follows the run: phases in the order they started, then the
    # expected ones not yet seen. When the run uses names outside the
    # expected list, the unseen expected ones are dropped rather than shown
    # as pending forever.
    unexpected = any(n not in expected_phases for n in seen_order)
    ordered = [phases[n] for n in seen_order]
    if not unexpected:
        ordered += [phases[n] for n in expected_phases if n not in seen_order]
    run_id = run_id or run_dir.name
    prefix = prefix or f"tlmig-{run_id}"
    for pv in previews:
        for e in (pv.get("from_estate"), pv.get("to_estate")):
            if e and e not in estates:
                estates.append(e)
    return RunState(run_id, prefix, region, ordered, resources, estates, ledger, measures, verdicts, notes, seen, last_ts, previews, record_store)


def phase_boundaries(run_dir: str | pathlib.Path) -> dict[str, int]:
    """Event index just after each phase's end, for one-cell-per-phase replay."""
    path = pathlib.Path(run_dir) / "events.jsonl"
    out: dict[str, int] = {}
    if not path.exists():
        return out
    for i, raw in enumerate(path.read_text().splitlines(), start=1):
        try:
            ev = json.loads(raw)
        except json.JSONDecodeError:
            continue
        k = ev.get("kind")
        if (k == "phase" and ev.get("status") == "end") or k == "phase_end":
            out[ev.get("name") or ev.get("phase") or ""] = i
    return out


# ----------------------------------------------------------------------------
# Rendering
# ----------------------------------------------------------------------------

PALETTE = ["#6b7280", "#2563eb", "#d97706", "#059669", "#7c3aed", "#db2777", "#0891b2"]


def _colours(state: RunState) -> dict[str, str]:
    """Monolith gets the grey; each other estate a hue, stable by order."""
    out: dict[str, str] = {}
    hue = 1
    for e in state.estates:
        if e.endswith("-monolith") or e == state.prefix:
            out[e] = PALETTE[0]
        else:
            out[e] = PALETTE[hue % len(PALETTE)]
            hue += 1
    return out


def _short_estate(state: RunState, estate: str | None) -> str:
    if not estate:
        return "unseen"
    return estate[len(state.prefix) + 1:] if estate.startswith(state.prefix + "-") else estate


def _esc(s: object) -> str:
    return html.escape(str(s), quote=True)


def render_map_svg(state: RunState, width: int = 640) -> str:
    """The estate-ownership map: one row per team, one cell per resource,
    colour by live estate, an outline around each run of cells one estate
    holds, children drawn with a tie to their parent."""
    colours = _colours(state)
    teams = sorted({r.team for r in state.resources.values()})
    if not teams:
        return f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="60"><text x="12" y="36" fill="currentColor" fill-opacity="0.5" font-family="ui-monospace, monospace" font-size="13">the map fills in when setup runs</text></svg>'
    cell, gap, pad, rowh, left = 64, 8, 14, 74, 96
    ncols = max(len([r for r in state.resources.values() if r.team == t]) for t in teams)
    height = pad * 2 + rowh * len(teams) + 26
    vb_w = max(width, left + ncols * (cell + gap) + pad)
    # width=100% with a viewBox: the map fits its container, never scrolls sideways.
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="100%" viewBox="0 0 {vb_w} {height}" style="max-width:{vb_w}px;display:block" font-family="ui-monospace, Menlo, monospace">']
    y = pad
    for team in teams:
        rs = [r for r in state.resources.values() if r.team == team]
        rs.sort(key=lambda r: (COLUMNS.index(r.type) if r.type in COLUMNS else len(COLUMNS), r.name))
        out.append(f'<text x="{pad}" y="{y + 40}" font-size="13" fill="currentColor" fill-opacity="0.75">{_esc(team)}</text>')
        # outlines around runs of one estate
        x = left
        run_start, run_estate = x, None
        boxes: list[tuple[int, int, str]] = []
        for i, r in enumerate(rs):
            e = state.estate_of(r.key)
            if e != run_estate:
                if run_estate is not None:
                    boxes.append((run_start, x - gap, run_estate))
                run_start, run_estate = x, e
            x += cell + gap
        if run_estate is not None:
            boxes.append((run_start, x - gap, run_estate))
        for x0, x1, e in boxes:
            c = colours.get(e or "", "#9ca3af")
            out.append(f'<rect x="{x0 - 4}" y="{y + 8}" width="{x1 - x0 + 8}" height="{cell - 6}" rx="8" fill="{c}" fill-opacity="0.10" stroke="{c}" stroke-width="1.5"/>')
        x = left
        positions: dict[str, int] = {}
        for r in rs:
            e = state.estate_of(r.key)
            gone = r.gone or e is None
            c = "#9ca3af" if gone else colours.get(e or "", "#9ca3af")
            positions[r.address] = x
            dash = ' stroke-dasharray="4 3"' if (not r.taggable or gone) else ""
            title = f"{r.address} · {'gone' if gone else e}"
            out.append(f'<rect x="{x}" y="{y + 12}" width="{cell - 8}" height="{cell - 14}" rx="6" fill="{c}" fill-opacity="{0.06 if gone else 0.35 if r.taggable else 0.18}" stroke="{c}" stroke-width="1.5"{dash}><title>{_esc(title)}</title></rect>')
            out.append(f'<text x="{x + (cell - 8) / 2}" y="{y + 12 + (cell - 14) / 2 + 4}" text-anchor="middle" font-size="11" fill="currentColor" fill-opacity="{0.4 if gone else 1}">{_esc(SHORT.get(r.type, r.type.split("_")[-1].split(":")[-1]))}</text>')
            x += cell + gap
        for r in rs:
            if r.parent and r.parent in positions and r.address in positions:
                px, cx = positions[r.parent], positions[r.address]
                out.append(f'<path d="M{px + (cell - 8) / 2} {y + 12} C {px + (cell - 8) / 2} {y}, {cx + (cell - 8) / 2} {y}, {cx + (cell - 8) / 2} {y + 12}" fill="none" stroke="#9ca3af" stroke-width="1"/>')
        y += rowh
    # legend
    lx = pad
    for e in state.estates:
        c = colours.get(e, "#9ca3af")
        n = sum(1 for r in state.resources.values() if state.estate_of(r.key) == e)
        out.append(f'<rect x="{lx}" y="{y + 6}" width="12" height="12" rx="3" fill="{c}"/>')
        label = f"{_short_estate(state, e)} · {n}"
        out.append(f'<text x="{lx + 18}" y="{y + 16}" font-size="12" fill="currentColor" fill-opacity="0.75">{_esc(label)}</text>')
        lx += 18 + 8 * len(label) + 18
    out.append("</svg>")
    return "".join(out)


def render_phase_strip(state: RunState) -> str:
    now = state.last_ts
    parts = []
    for p in state.phases:
        cls = p.status
        extra = ""
        if p.status == "done" and p.seconds is not None:
            extra = f" <span class='t'>{p.seconds:.0f}s</span>"
        elif p.status == "active" and p.started and now:
            extra = f" <span class='t'>{(now - p.started).total_seconds():.0f}s</span>"
        parts.append(f"<span class='ph {cls}' title='{_esc(p.title)}'>{_esc(p.name)}{extra}</span>")
    return "<div class='strip'>" + "".join(parts) + "</div>"


def render_ledger(state: RunState, limit: int = 40, newest_first: bool = True) -> str:
    rows = state.ledger[-limit:]
    if not rows:
        return "<div class='empty'>nothing has run yet</div>"
    if newest_first:
        rows = list(reversed(rows))
    trs = []
    for r in rows:
        t = r.ts.strftime("%H:%M:%S") if r.ts else ""
        cls = "ok" if r.ok else "bad" if r.ok is False else ""
        w = "write" if r.write else "read"
        trs.append(f"<tr class='{w}'><td class='t'>{_esc(t)}</td><td class='p'>{_esc(r.phase)}</td><td>{_esc(r.actor)}</td><td>{_esc(r.action)}</td><td class='tg'>{_esc(r.target)}</td><td class='{cls}'>{_esc(r.answer)}</td></tr>")
    return "<table class='ledger'><thead><tr><th>time</th><th>phase</th><th>who</th><th>action</th><th>target</th><th>answer</th></tr></thead><tbody>" + "".join(trs) + "</tbody></table>"


def render_phase_ledger(state: RunState, phase: str, limit: int = 30) -> str:
    """One phase's rows of the ledger, in order, the way a presenter reads a
    beat: what ran, who ran it, what the platform answered."""
    rows = [r for r in state.ledger if r.phase == phase][-limit:]
    if not rows:
        return ""
    trs = []
    for r in rows:
        t = r.ts.strftime("%H:%M:%S") if r.ts else ""
        cls = "ok" if r.ok else "bad" if r.ok is False else ""
        trs.append(f"<tr class='{'write' if r.write else 'read'}'><td class='t'>{_esc(t)}</td><td>{_esc(r.actor)}</td><td>{_esc(r.action)}</td><td class='tg'>{_esc(r.target)}</td><td class='{cls}'>{_esc(r.answer)}</td></tr>")
    return f"<style>{CSS}</style><div class='tlmig'><table class='ledger'><thead><tr><th>time</th><th>who</th><th>action</th><th>target</th><th>answer</th></tr></thead><tbody>" + "".join(trs) + "</tbody></table></div>"


def render_measures(state: RunState, width: int = 640) -> str:
    if not state.measures:
        return ""
    bars = []
    for m in state.measures:
        bars.append((m.label, m.requests, False, m.cache_hits))
        for k, v in (m.reference or {}).items():
            if isinstance(v, (int, float)):
                bars.append((f"{k} (reference)", int(v), True, None))
            elif isinstance(v, dict):
                for k2, v2 in v.items():
                    if isinstance(v2, (int, float)):
                        bars.append((f"{k} {k2} (reference)", int(v2), True, None))
    mx = max(v for _, v, _, _ in bars) or 1
    rowh, lw, h = 22, 250, 22 * len(bars) + 12
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{h}" font-family="ui-monospace, Menlo, monospace" font-size="12">']
    y = 6
    for label, v, ref, hits in bars:
        bw = max(2, int((width - lw - 70) * v / mx))
        fill = "#d1d5db" if ref else "#2563eb"
        out.append(f'<text x="0" y="{y + 15}" fill="currentColor" fill-opacity="0.75">{_esc(label[:38])}</text>')
        out.append(f'<rect x="{lw}" y="{y + 4}" width="{bw}" height="14" rx="3" fill="{fill}"/>')
        txt = f"{v} requests" + (f" · {hits} cache hits" if hits else "")
        out.append(f'<text x="{lw + bw + 8}" y="{y + 15}" fill="currentColor">{_esc(txt)}</text>')
        y += rowh
    out.append("</svg>")
    return "".join(out)


def _plan_panel(side: str, estate: str | None, pv: dict, short) -> str:
    """One of the guard's two plans as a panel: which estate, what this plan
    had to prove, the counts, and the checks."""
    if side == "source":
        must = "What left must not be destroyed or rebuilt: the estate no longer sees the role, and its plan proposes nothing."
    else:
        must = "What arrived must already be its own: the estate sees the role under its tag, and its plan proposes nothing."
    clean = pv.get("clean")
    light = "on" if clean else "off" if clean is False else "dim"
    counts = f"add {pv.get('add', '?')} · change {pv.get('change', '?')} · destroy {pv.get('destroy', '?')}"
    checks = []
    for k, label in (("owned_undeclared", "owned but undeclared"), ("unowned", "unowned"), ("absent", "absent")):
        if k in pv:
            val = pv[k]
            checks.append(f"{label} {len(val) if isinstance(val, list) else val}")
    verdict_word = "clean" if clean else "NOT clean" if clean is False else "unread"
    return (f"<div class='plan'><div class='plan-h'><span class='light {light}'></span>"
            f"<span class='plan-side'>plan of the {side} estate</span> <code>{_esc(short(estate) if estate else '?')}</code></div>"
            f"<div class='plan-must'>{_esc(must)}</div>"
            f"<div class='vline'>{_esc(counts)}</div>"
            + (f"<div class='vline'>{_esc(' · '.join(checks))}</div>" if checks else "")
            + f"<div class='vline'><b>{verdict_word}</b></div></div>")


def render_verdicts(state: RunState) -> str:
    if not state.verdicts:
        return ""
    items = []
    short = lambda e: _short_estate(state, e)
    for v in state.verdicts:
        name = v.get("name", "")
        ok = v.get("ok", v.get("clean"))
        light = "on" if ok else "off" if ok is False else "dim"
        src, dst = v.get("source"), v.get("destination")
        if isinstance(src, dict) and isinstance(dst, dict):
            role = str(name).split(":", 1)[-1]
            items.append(f"<div class='verdict'><span class='light {light}'></span><b>{_esc(role)}</b> moved: two plans, one per estate, each targeted at its own resources</div>")
            items.append("<div class='vline'>A carve is clean only when both sides agree at the same moment. Terraform's carve leaves a window where the source wants to destroy what left and the destination wants to create what arrived; here both plan clean at once, because the tag is the boundary and each estate reads only its own.</div>")
            items.append("<div class='plans'>" + _plan_panel("source", v.get("source_estate"), src, short) + _plan_panel("destination", v.get("destination_estate"), dst, short) + "</div>")
        else:
            items.append(f"<div class='verdict'><span class='light {light}'></span><b>{_esc(name)}</b></div>")
        kept = v.get("children_kept")
        if isinstance(kept, dict):
            for role, names in kept.items():
                items.append(f"<div class='vline'>children kept with {_esc(role.split('/')[-1])}: {_esc(', '.join(names) if isinstance(names, list) else names)}</div>")
        lines = v.get("lines")
        if isinstance(lines, list):
            for ln in lines:
                items.append(f"<div class='vline'>{_esc(ln)}</div>")
        if "leftovers" in v:
            lo = v["leftovers"] or []
            items.append(f"<div class='vline'>{'nothing left' if not lo else str(len(lo)) + ' leftovers'}</div>")
    return "<div class='verdicts'>" + "".join(items) + "</div>"


CSS = """
.tlmig { --ink: currentColor; --soft: color-mix(in srgb, currentColor 72%, transparent); --faint: color-mix(in srgb, currentColor 50%, transparent); --rule: color-mix(in srgb, currentColor 18%, transparent); --ok:#16a34a; --bad:#dc2626; --accent:#2563eb; --wash: color-mix(in srgb, currentColor 6%, transparent);
  font-family: ui-sans-serif, -apple-system, system-ui, sans-serif; color: inherit; background: transparent; }
.tlmig h2 { font-size: 12px; letter-spacing: .08em; text-transform: uppercase; color: var(--faint); margin: 14px 0 6px; font-weight: 600; }
.tlmig .head { display:flex; justify-content:space-between; align-items:baseline; border-bottom:1px solid var(--rule); padding-bottom:6px; }
.tlmig .head b { font-size: 16px; }
.tlmig .head span { font-family: ui-monospace, Menlo, monospace; font-size: 12px; color: var(--faint); }
.tlmig .strip { display:flex; gap:6px; flex-wrap:wrap; margin: 10px 0; }
.tlmig .ph { font-family: ui-monospace, Menlo, monospace; font-size: 12px; padding: 3px 9px; border-radius: 999px; border: 1px solid var(--rule); color: var(--faint); }
.tlmig .ph.done { border-color: var(--ok); color: var(--ok); }
.tlmig .ph.active { border-color: var(--accent); color: var(--accent); font-weight:600; background: var(--wash); }
.tlmig .ph .t { opacity:.7; margin-left:4px; }
.tlmig .cols { display:grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 18px; }
.tlmig .cols.stack { grid-template-columns: minmax(0, 1fr); }
.tlmig table.ledger { border-collapse: collapse; width:100%; font-size: 12px; font-family: ui-monospace, Menlo, monospace; }
.tlmig table.ledger th { text-align:left; font-weight:600; color:var(--faint); border-bottom:1px solid var(--rule); padding:4px 6px 4px 0; }
.tlmig table.ledger td { border-bottom:1px solid var(--rule); padding:4px 6px 4px 0; vertical-align: top; }
.tlmig table.ledger tr.read td { color: var(--faint); }
.tlmig table.ledger td.ok { color: var(--ok); }
.tlmig table.ledger td.bad { color: var(--bad); font-weight: 600; }
.tlmig table.ledger td.tg { max-width: 260px; overflow-wrap: anywhere; }
.tlmig .empty { color: var(--faint); font-size: 13px; }
.tlmig .note { font-size: 14px; color: var(--soft); border-left: 3px solid var(--accent); padding: 4px 10px; margin: 8px 0; }
.tlmig .verdict { display:flex; align-items:center; gap:8px; margin-top:6px; font-size:13px; }
.tlmig .light { width:12px; height:12px; border-radius:50%; background: var(--rule); display:inline-block; }
.tlmig .light.on { background: var(--ok); box-shadow: 0 0 0 3px color-mix(in srgb, var(--ok) 25%, transparent); }
.tlmig .light.off { background: var(--bad); box-shadow: 0 0 0 3px color-mix(in srgb, var(--bad) 25%, transparent); }
.tlmig .vline { font-family: ui-monospace, Menlo, monospace; font-size: 12px; color: var(--soft); margin-left: 20px; }
.tlmig .plans { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 12px; margin: 10px 0 6px 20px; }
.tlmig .plan { border: 1px solid var(--rule); border-radius: 8px; padding: 10px 12px; }
.tlmig .plan-h { display: flex; align-items: center; gap: 8px; font-size: 13px; }
.tlmig .plan-side { text-transform: uppercase; letter-spacing: .06em; font-size: 11px; color: var(--faint); }
.tlmig .plan-must { font-size: 13px; color: var(--soft); margin: 6px 0; }
.tlmig .plan .vline { margin-left: 0; }
.tlmig .wrap { overflow-x: auto; }
.tlmig .ledgerwrap { max-height: 300px; overflow-y: auto; border: 1px solid var(--rule); border-radius: 6px; padding: 0 8px; }
.tlmig .ledgerwrap thead th { position: sticky; top: 0; background: inherit; }
"""


def render_html(state: RunState, *, stack: bool = True, ledger_rows: int = 40, map_width: int = 620, compact: bool = False) -> str:
    """The whole picture: header, phase strip, the map at full width, the
    ledger under it in a bounded scrolling box (newest first), then measures
    and verdicts. ``stack=False`` puts the ledger beside the map instead.
    ``compact`` is the picture as one phase left it: the map, measures and
    verdict only, no header, strip or ledger (the live view above has them),
    and nothing at all when the phase left no resources on the map."""
    if compact:
        parts = [f"<style>{CSS}</style><div class='tlmig'>"]
        if any(not r.gone for r in state.resources.values()):
            parts.append(f"<div class='wrap'>{render_map_svg(state, map_width)}</div>")
        m = render_measures(state, map_width)
        if m:
            parts.append(f"<h2>plan cost · requests per plan</h2><div class='wrap'>{m}</div>")
        v = render_verdicts(state)
        if v:
            parts.append(f"<h2>guard</h2>{v}")
        parts.append("</div>")
        return "".join(parts) if len(parts) > 2 else ""
    active = state.active_phase
    note = next((t for p, t in reversed(state.notes) if not active or p == active.name), None)
    parts = [f"<style>{CSS}</style><div class='tlmig'>",
             f"<div class='head'><b>{_esc(state.prefix)}</b><span>{_esc(state.region)} · {state.events_seen} events" + (f" · {state.last_ts.strftime('%H:%M:%S')}Z" if state.last_ts else "") + "</span></div>",
             render_phase_strip(state)]
    if note:
        parts.append(f"<div class='note'>{_esc(note)}</div>")
    parts.append(f"<div class='cols{' stack' if stack else ''}'>")
    parts.append(f"<div><h2>estates · who owns what</h2><div class='wrap'>{render_map_svg(state, map_width)}</div></div>")
    parts.append(f"<div><h2>ledger · every write and the platform's answer, newest first</h2><div class='ledgerwrap'>{render_ledger(state, ledger_rows)}</div></div>")
    parts.append("</div>")
    m = render_measures(state, map_width)
    if m:
        parts.append(f"<h2>plan cost · requests per plan</h2><div class='wrap'>{m}</div>")
    v = render_verdicts(state)
    if v:
        parts.append(f"<h2>guard</h2>{v}")
    parts.append("</div>")
    return "".join(parts)


def map_signature(state: RunState) -> tuple:
    """What the map shows: each resource's key and the estate it sits in."""
    return tuple(sorted((r.key, state.estate_of(r.key)) for r in state.resources.values()))


def render_delta(after: RunState, before: RunState | None, *, map_width: int = 620) -> str:
    """The picture of what one phase changed: the map only when a resource's
    estate moved or appeared, the measures and verdicts only those the phase
    added. A phase that changed nothing visible renders as empty, so a cell
    can say so in words instead."""
    parts = [f"<style>{CSS}</style><div class='tlmig'>"]
    if before is None or map_signature(after) != map_signature(before):
        if any(not r.gone for r in after.resources.values()):
            parts.append(f"<h2>who owns what, after this step</h2><div class='wrap'>{render_map_svg(after, map_width)}</div>")
        elif before is not None and any(not r.gone for r in before.resources.values()):
            parts.append("<h2>who owns what, after this step</h2><div class='empty'>nothing: every resource this run made is gone</div>")
    n_m = len(before.measures) if before else 0
    if len(after.measures) > n_m:
        sliced = dataclasses.replace(after, measures=after.measures[n_m:])
        m = render_measures(sliced, map_width)
        if m:
            parts.append(f"<h2>plan cost · requests per plan</h2><div class='wrap'>{m}</div>")
    n_v = len(before.verdicts) if before else 0
    if len(after.verdicts) > n_v:
        sliced = dataclasses.replace(after, verdicts=after.verdicts[n_v:])
        v = render_verdicts(sliced)
        if v:
            parts.append(f"<h2>guard</h2>{v}")
    parts.append("</div>")
    return "".join(parts) if len(parts) > 2 else ""


def project(state: RunState) -> RunState:
    """The run as it would stand after every previewed move that passed its
    checks: a copy of the state with each moved resource's estate set to the
    preview's destination. Refused moves change nothing. Untaggable children
    follow through estate_of as they do live."""
    import copy
    after = copy.deepcopy(state)
    for pv in state.previews:
        if pv.get("refusal"):
            continue
        old_addr = pv.get("old_address") or pv.get("address")
        new_addr = pv.get("address") or old_addr
        dest = pv.get("to_estate")
        r = after.by_address.get(old_addr) if old_addr else None
        if r is None:
            lid = pv.get("live_id") or pv.get("id")
            r = after.resources.get(lid) if lid else None
        if r is not None and dest:
            r.estate = dest
            r.gone = False
            if new_addr:
                r.address = new_addr
    return after


def render_previews(state: RunState) -> str:
    """What is about to happen: one row per planned move with the tag writes
    it makes, the children that follow, and the refusal if the dry run said
    no. Empty when nothing has been previewed."""
    if not state.previews:
        return ""
    short = lambda e: _short_estate(state, e) if e else "?"
    rows = []
    for pv in state.previews:
        addr = pv.get("old_address") or pv.get("address") or "?"
        if pv.get("address") and pv.get("old_address") and pv["address"] != pv["old_address"]:
            addr = f"{pv['old_address']} → {pv['address']}"
        writes = "; ".join(f"{w.get('key')}: {w.get('from')} → {w.get('to')}" for w in (pv.get("tag_writes") or []))
        kids = ", ".join(str(c) for c in (pv.get("children") or []))
        ref = pv.get("refusal")
        if ref:
            answer, cls = (ref.get("summary") if isinstance(ref, dict) else str(ref)), "bad"
        else:
            answer, cls = ("written" if pv.get("written") else "would write"), ("ok" if pv.get("written") else "")
        rows.append(f"<tr class='write'><td>{_esc(addr)}</td><td>{_esc(short(pv.get('from_estate')))} → {_esc(short(pv.get('to_estate')))}</td><td class='tg'>{_esc(writes)}</td><td class='tg'>{_esc(kids)}</td><td class='{cls}'>{_esc(answer)}</td></tr>")
    refused = sum(1 for pv in state.previews if pv.get("refusal"))
    head = f"{len(state.previews)} planned moves, {refused} refused" if refused else f"{len(state.previews)} planned moves, every check passed"
    return (f"<style>{CSS}</style><div class='tlmig'><h2>what is about to happen · {_esc(head)}</h2>"
            "<table class='ledger'><thead><tr><th>resource</th><th>owner</th><th>tag writes</th><th>children that follow</th><th>dry run</th></tr></thead><tbody>"
            + "".join(rows) + "</tbody></table></div>")


def render_record_store(state: RunState) -> str:
    """choudoufu's own record, per estate: the addresses ``.tofu-records``
    lists for each estate. Shown per estate, not summed, because a source
    estate can still list what it handed away until it is applied again."""
    if not state.record_store:
        return ""
    rows = []
    for est in sorted(state.record_store):
        addrs = state.record_store[est] or []
        items = ", ".join(_esc(a) for a in addrs) if addrs else "<span class='muted'>none</span>"
        rows.append(f"<tr><td>{_esc(_short_estate(state, est))}</td><td>{len(addrs)}</td><td class='tg'>{items}</td></tr>")
    return (f"<style>{CSS}</style><div class='tlmig'>"
            "<h2>the tool&rsquo;s own record · <span class='muted'>.tofu-records, per estate</span></h2>"
            "<table class='ledger'><thead><tr><th>estate</th><th>records</th><th>addresses</th></tr></thead><tbody>"
            + "".join(rows) + "</tbody></table></div>")


def render_projection(state: RunState, map_width: int = 620) -> str:
    """The map now beside the map as it would stand after the planned moves."""
    if not state.previews:
        return ""
    after = project(state)
    return (f"<style>{CSS}</style><div class='tlmig'><div class='cols'>"
            f"<div><h2>who owns what · now</h2><div class='wrap'>{render_map_svg(state, map_width)}</div></div>"
            f"<div><h2>who owns what · after the planned moves</h2><div class='wrap'>{render_map_svg(after, map_width)}</div></div>"
            "</div></div>")


def _counts(state: RunState) -> dict[str, int]:
    out: dict[str, int] = {}
    for r in state.resources.values():
        e = state.estate_of(r.key)
        if e:
            out[e] = out.get(e, 0) + 1
    return out


def payoff(name: str, after: RunState, before: RunState | None = None) -> str:
    """What one beat proved, in a sentence built from the run's own numbers,
    so the presenter's payoff line is never a claim the log cannot back.
    Empty when the beat left nothing to say yet."""
    counts = _counts(after)
    mono = next((e for e in after.estates if e.endswith("-monolith")), None)
    teams = [e for e in after.estates if e != mono]
    short = lambda e: _short_estate(after, e)
    n_m = len(before.measures) if before else 0
    new_measures = after.measures[n_m:]
    if name == "preflight":
        rows = [r for r in after.ledger if r.phase == "preflight"]
        if rows and all(r.ok for r in rows):
            build = next((t for p, t in after.notes if p == "preflight" and t.startswith("build")), None)
            return "Account and binary verified; nothing touched." + (f" Build: {build.split(':', 1)[1].strip()}." if build else "")
        return ""
    if name == "setup":
        if mono and counts.get(mono):
            tagged = sum(1 for r in after.resources.values() if r.taggable and after.estate_of(r.key) == mono)
            kids = counts[mono] - tagged
            return (f"{tagged} taggable resources now carry tofu-estate={short(mono)}, stamped by the apply"
                    + (f"; {kids} untaggable children follow their parents" if kids else "") + ". One estate, one boundary.")
        return ""
    if name in ("slow-plan", "fast-plan"):
        if not new_measures:
            return ""
        m = new_measures[-1]
        if name == "slow-plan":
            return f"One plan of the monolith cost {m.requests} requests" + (f" in {m.seconds:.1f}s" if m.seconds else "") + ". Every plan of a terralith pays this."
        slow = next((x for x in after.measures if x.refresh and x.estate == mono), None)
        if slow and slow.requests:
            ratio = slow.requests / max(m.requests, 1)
            return f"{m.requests} requests against the monolith's {slow.requests}: {ratio:.1f}x fewer, with {m.cache_hits or 0} served from cache. Cost tracks the estate."
        return f"{m.requests} requests with {m.cache_hits or 0} served from cache."
    if name == "decompose":
        held = [e for e in teams if counts.get(e)]
        if held:
            left = counts.get(mono, 0) if mono else 0
            return f"{len(held)} team estates hold {sum(counts[e] for e in held)} resources; the monolith holds {left}. Nothing was re-created and no state file was split."
        return ""
    if name == "carve":
        if before is None:
            return ""
        moved = [(r, before.estate_of(r.key), after.estate_of(r.key)) for r in after.resources.values()
                 if before.estate_of(r.key) and after.estate_of(r.key) and before.estate_of(r.key) != after.estate_of(r.key)]
        if moved:
            dests = sorted({short(d) for _, _, d in moved})
            return f"{len(moved)} resources changed owner into {', '.join(dests)} by tag write alone; their untaggable children followed the parent's tag without a write."
        return ""
    if name == "guard":
        v = next((v for v in reversed(after.verdicts) if str(v.get("name", "")).startswith("carve")), None)
        if v is None:
            return ""
        return "Verdict holds: the role's live tag names its new estate, its children stayed with it, and both estates plan clean." if v.get("ok") else "Verdict FAILS: read the lines above; the carve left something behind."
    if name == "receipt":
        rows = [r for r in after.ledger if r.action.startswith("CloudTrail")]
        if rows:
            refused = sum(1 for r in rows if r.ok is False)
            return (f"{len(rows)} writes in the account's own log, each naming who made it"
                    + (f", {refused} refused with the session named" if refused else "") + ". No state file could produce that record.")
        return ""
    if name == "teardown":
        v = next((v for v in reversed(after.verdicts) if v.get("name") == "teardown"), None)
        gone = sum(1 for r in after.resources.values() if r.gone)
        if v is not None:
            return f"Nothing carrying this run's prefix remains; {gone} resources destroyed, the account listed rather than trusted." if v.get("ok", v.get("clean")) else "Leftovers found: the account was listed and something remains. Read the verdict."
        return ""
    return ""


def render_page(run_dir: str | pathlib.Path, *, refresh_seconds: int | None = 2, **kw) -> str:
    """A standalone HTML document for a browser tab, re-fetching itself on a
    timer so a projected page follows the run."""
    state = load_run(run_dir)
    meta = f"<meta http-equiv='refresh' content='{refresh_seconds}'>" if refresh_seconds else ""
    return f"<!doctype html><html><head><meta charset='utf-8'>{meta}<title>{_esc(state.prefix)}</title></head><body style='margin:16px'>{render_html(state, **kw)}</body></html>"


def serve(run_dir: str | pathlib.Path, port: int = 8765, **kw) -> None:
    """Serve render_page on localhost; ctrl-c to stop. The browser half of
    the side-by-side stage when marimo is not the one on screen."""
    import http.server

    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):  # noqa: N802
            body = render_page(run_dir, **kw).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):  # quiet
            pass

    print(f"serving {run_dir} at http://127.0.0.1:{port}/")
    http.server.HTTPServer(("127.0.0.1", port), H).serve_forever()


if __name__ == "__main__":
    import sys

    serve(sys.argv[1] if len(sys.argv) > 1 else "runs/sample")
