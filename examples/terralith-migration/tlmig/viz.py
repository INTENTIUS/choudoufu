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

PHASES = ("setup", "slow-plan", "decompose", "fast-plan", "carve", "guard", "teardown")

# Types that carry no tags of their own; drawn attached to the parent whose
# live tag names their estate.
UNTAGGABLE = {"aws_iam_role_policy", "aws_iam_role_policy_attachment"}

# Column order inside a team row, by resource type. Unknown types append.
COLUMNS = (
    "aws_iam_role",
    "aws_iam_role_policy",
    "aws_iam_policy",
    "aws_iam_role_policy_attachment",
    "aws_cloudwatch_log_group",
)
SHORT = {
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

    @property
    def taggable(self) -> bool:
        return self.type not in UNTAGGABLE


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

    @property
    def active_phase(self) -> Phase | None:
        for p in reversed(self.phases):
            if p.status == "active":
                return p
        return None

    def estate_of(self, address: str) -> str | None:
        r = self.resources.get(address)
        if r is None:
            return None
        if r.taggable:
            return r.estate or r.declared_in
        if r.parent and r.parent in self.resources:
            return self.estate_of(r.parent)
        return r.estate or r.declared_in


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


def _team_of(name: str) -> str:
    """team_a, team_a_inline, team_a_0 -> team-a. Falls back to the name."""
    m = re.match(r"^(team_[a-z0-9]+?)(?:_inline|_[0-9]+)?$", name)
    return m.group(1).replace("_", "-") if m else name


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
    run_id = manifest.get("run_id", run_dir.name)
    prefix = manifest.get("prefix", f"tlmig-{run_id}")
    region = manifest.get("region", "")

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

    phases: dict[str, Phase] = {n: Phase(name=n) for n in PHASES}
    ledger: list[LedgerRow] = []
    measures: list[Measure] = []
    verdicts: list[dict] = []
    notes: list[tuple[str, str]] = []
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

        if kind in ("phase", "phase_start", "phase_end"):
            name = ev.get("name") or ev.get("phase") or phase_name
            status = ev.get("status") or ("start" if kind == "phase_start" else "end")
            p = phases.setdefault(name, Phase(name=name))
            p.title = ev.get("title") or p.title
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
            for item in ev.get("items") or []:
                addr = item.get("address")
                tags = item.get("tags") or {}
                live_estate = tags.get("tofu-estate") or estate
                if not addr:
                    continue
                r = resources.get(addr)
                if r is None:
                    rtype, _, rname = addr.partition(".")
                    r = Resource(address=addr, type=rtype, name=rname, team=_team_of(rname),
                                 declared_in=live_estate)
                    resources[addr] = r
                r.estate = live_estate
                r.id = item.get("id") or r.id
                if live_estate not in estates:
                    estates.append(live_estate)
        elif kind == "fact":
            label, value = str(ev.get("label", "")), str(ev.get("value", ""))
            # "role <name> -> estate" facts move a role on the map.
            m = re.match(r"^(?:role\s+)?(\S+?)\s*(?:->|:)\s*(\S+)$", f"{label} -> {value}")
            for r in resources.values():
                if r.type == "aws_iam_role" and (label.endswith(r.name) or r.id == label or label.endswith(r.address)):
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
            verdicts.append({"name": name, "phase": phase_name, "ts": ts, **(body if isinstance(body, dict) else {"value": body})})
            ok = body.get("ok") if isinstance(body, dict) else None
            if ok is None and isinstance(body, dict):
                ok = body.get("clean")
            ledger.append(LedgerRow(ts, phase_name, "guard", f"verdict {name}", "", "holds" if ok else "FAILS" if ok is False else "", ok, False))
            # A carve verdict names where each role now lives.
            moved = body.get("moved_estates") if isinstance(body, dict) else None
            if isinstance(moved, dict):
                for role, est in moved.items():
                    for r in resources.values():
                        if r.type == "aws_iam_role" and (role.endswith(r.name) or role == r.id or role.endswith(r.address.split(".")[-1])):
                            r.estate = est
        elif kind == "receipt":
            rec = ev.get("receipt") or {}
            ct = rec.get("cloudtrail") or {}
            for e in ct.get("events") or []:
                who = (e.get("role") or e.get("userIdentity.arn") or "").split("assumed-role/")[-1].split("/")[0] or "?"
                tags = e.get("tags") or {}
                tagtxt = ", ".join(f"{k}={v}" for k, v in tags.items()) if isinstance(tags, dict) else str(tags)
                err = e.get("error") or e.get("errorCode")
                target = e.get("resource") or ", ".join(e.get("resources") or [])
                ledger.append(LedgerRow(_parse_ts(e.get("time") or e.get("eventTime")), phase_name, who,
                                        f"CloudTrail {e.get('eventName', 'CreateTags')} {tagtxt}", target,
                                        err or "recorded", not err, True))
        elif kind == "note":
            notes.append((phase_name, str(ev.get("text", ""))))

    ordered = [phases[n] for n in PHASES if n in phases] + [p for n, p in phases.items() if n not in PHASES]
    return RunState(run_id, prefix, region, ordered, resources, estates, ledger, measures, verdicts, notes, seen, last_ts)


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
        return f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="60"><text x="12" y="36" fill="#6b7280" font-family="ui-monospace, monospace" font-size="13">no estate configs yet</text></svg>'
    cell, gap, pad, rowh, left = 64, 8, 14, 74, 96
    ncols = max(len([r for r in state.resources.values() if r.team == t]) for t in teams)
    height = pad * 2 + rowh * len(teams) + 26
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {max(width, left + ncols * (cell + gap) + pad)} {height}" font-family="ui-monospace, Menlo, monospace">']
    y = pad
    for team in teams:
        rs = [r for r in state.resources.values() if r.team == team]
        rs.sort(key=lambda r: (COLUMNS.index(r.type) if r.type in COLUMNS else len(COLUMNS), r.name))
        out.append(f'<text x="{pad}" y="{y + 40}" font-size="13" fill="#374151">{_esc(team)}</text>')
        # outlines around runs of one estate
        x = left
        run_start, run_estate = x, None
        boxes: list[tuple[int, int, str]] = []
        for i, r in enumerate(rs):
            e = state.estate_of(r.address)
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
            e = state.estate_of(r.address)
            c = colours.get(e or "", "#9ca3af")
            positions[r.address] = x
            dash = ' stroke-dasharray="4 3"' if not r.taggable else ""
            out.append(f'<rect x="{x}" y="{y + 12}" width="{cell - 8}" height="{cell - 14}" rx="6" fill="{c}" fill-opacity="{0.35 if r.taggable else 0.18}" stroke="{c}" stroke-width="1.5"{dash}><title>{_esc(r.address)} · {_esc(e or "unseen")}</title></rect>')
            out.append(f'<text x="{x + (cell - 8) / 2}" y="{y + 12 + (cell - 14) / 2 + 4}" text-anchor="middle" font-size="11" fill="#111827">{_esc(SHORT.get(r.type, r.type.split("_")[-1]))}</text>')
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
        n = sum(1 for r in state.resources.values() if state.estate_of(r.address) == e)
        out.append(f'<rect x="{lx}" y="{y + 6}" width="12" height="12" rx="3" fill="{c}"/>')
        label = f"{_short_estate(state, e)} · {n}"
        out.append(f'<text x="{lx + 18}" y="{y + 16}" font-size="12" fill="#374151">{_esc(label)}</text>')
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


def render_ledger(state: RunState, limit: int = 40) -> str:
    rows = state.ledger[-limit:]
    if not rows:
        return "<div class='empty'>no commands yet</div>"
    trs = []
    for r in rows:
        t = r.ts.strftime("%H:%M:%S") if r.ts else ""
        cls = "ok" if r.ok else "bad" if r.ok is False else ""
        w = "write" if r.write else "read"
        trs.append(f"<tr class='{w}'><td class='t'>{_esc(t)}</td><td class='p'>{_esc(r.phase)}</td><td>{_esc(r.actor)}</td><td>{_esc(r.action)}</td><td class='tg'>{_esc(r.target)}</td><td class='{cls}'>{_esc(r.answer)}</td></tr>")
    return "<table class='ledger'><thead><tr><th>time</th><th>phase</th><th>who</th><th>action</th><th>target</th><th>answer</th></tr></thead><tbody>" + "".join(trs) + "</tbody></table>"


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
        out.append(f'<text x="0" y="{y + 15}" fill="#374151">{_esc(label[:38])}</text>')
        out.append(f'<rect x="{lw}" y="{y + 4}" width="{bw}" height="14" rx="3" fill="{fill}"/>')
        txt = f"{v} requests" + (f" · {hits} cache hits" if hits else "")
        out.append(f'<text x="{lw + bw + 8}" y="{y + 15}" fill="#111827">{_esc(txt)}</text>')
        y += rowh
    out.append("</svg>")
    return "".join(out)


def render_verdicts(state: RunState) -> str:
    if not state.verdicts:
        return ""
    items = []
    for v in state.verdicts:
        name = v.get("name", "")
        ok = v.get("ok", v.get("clean"))
        light = "on" if ok else "off" if ok is False else "dim"
        items.append(f"<div class='verdict'><span class='light {light}'></span><b>{_esc(name)}</b></div>")
        src, dst = v.get("source"), v.get("destination")
        for side, pv in (("source", src), ("destination", dst)):
            if isinstance(pv, dict):
                summary = f"{side}: add {pv.get('add', '?')} change {pv.get('change', '?')} destroy {pv.get('destroy', '?')}"
                extra = []
                for k in ("owned_undeclared", "unowned", "absent"):
                    if k in pv:
                        val = pv[k]
                        extra.append(f"{k.replace('_', ' ')} {len(val) if isinstance(val, list) else val}")
                items.append(f"<div class='vline'>{_esc(summary)}{(' · ' + _esc(' · '.join(extra))) if extra else ''}</div>")
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
:root { --ink:#111827; --soft:#374151; --faint:#6b7280; --rule:#e5e7eb; --paper:#ffffff; --ok:#059669; --bad:#dc2626; --accent:#2563eb; }
.tlmig { font-family: ui-sans-serif, -apple-system, system-ui, sans-serif; color: var(--ink); background: var(--paper); }
.tlmig h2 { font-size: 13px; letter-spacing: .08em; text-transform: uppercase; color: var(--faint); margin: 14px 0 6px; font-weight: 600; }
.tlmig .head { display:flex; justify-content:space-between; align-items:baseline; border-bottom:1px solid var(--rule); padding-bottom:6px; }
.tlmig .head b { font-size: 16px; }
.tlmig .head span { font-family: ui-monospace, Menlo, monospace; font-size: 12px; color: var(--faint); }
.tlmig .strip { display:flex; gap:6px; flex-wrap:wrap; margin: 10px 0; }
.tlmig .ph { font-family: ui-monospace, Menlo, monospace; font-size: 12px; padding: 3px 9px; border-radius: 999px; border: 1px solid var(--rule); color: var(--faint); }
.tlmig .ph.done { background:#ecfdf5; border-color:#a7f3d0; color:#065f46; }
.tlmig .ph.active { background:#eff6ff; border-color:var(--accent); color:#1e3a8a; font-weight:600; }
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
.tlmig .light { width:12px; height:12px; border-radius:50%; background:#d1d5db; display:inline-block; }
.tlmig .light.on { background: var(--ok); box-shadow: 0 0 0 3px #d1fae5; }
.tlmig .light.off { background: var(--bad); box-shadow: 0 0 0 3px #fee2e2; }
.tlmig .vline { font-family: ui-monospace, Menlo, monospace; font-size: 12px; color: var(--soft); margin-left: 20px; }
.tlmig .wrap { overflow-x: auto; }
"""


def render_html(state: RunState, *, stack: bool = False, ledger_rows: int = 40, map_width: int = 620) -> str:
    """The whole picture: header, phase strip, map beside ledger, then
    measures and verdicts. ``stack`` puts the ledger under the map for a
    narrow (half-screen) stage."""
    active = state.active_phase
    note = next((t for p, t in reversed(state.notes) if not active or p == active.name), None)
    parts = [f"<style>{CSS}</style><div class='tlmig'>",
             f"<div class='head'><b>{_esc(state.prefix)}</b><span>{_esc(state.region)} · {state.events_seen} events" + (f" · {state.last_ts.strftime('%H:%M:%S')}Z" if state.last_ts else "") + "</span></div>",
             render_phase_strip(state)]
    if note:
        parts.append(f"<div class='note'>{_esc(note)}</div>")
    parts.append(f"<div class='cols{' stack' if stack else ''}'>")
    parts.append(f"<div><h2>estates · who owns what</h2><div class='wrap'>{render_map_svg(state, map_width)}</div></div>")
    parts.append(f"<div><h2>ledger · every write and the platform's answer</h2><div class='wrap'>{render_ledger(state, ledger_rows)}</div></div>")
    parts.append("</div>")
    m = render_measures(state, map_width)
    if m:
        parts.append(f"<h2>plan cost · requests per plan</h2><div class='wrap'>{m}</div>")
    v = render_verdicts(state)
    if v:
        parts.append(f"<h2>guard</h2>{v}")
    parts.append("</div>")
    return "".join(parts)


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
