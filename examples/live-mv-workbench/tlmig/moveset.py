"""An arbitrary carve: the move set read from carve.json, the write-free
preview of each move parsed from ``live-mv -dry-run``, and the guard verdict
over the whole set.

This generalizes the single-role finale (:mod:`verify`'s ``CarveVerdict``) to
the workbench's carve plan: many resources, each with its own source and
destination estate. Everything here is pure. :mod:`govern` runs the plans and
the dry runs; this module reads their text and composes the answer, so the
grading is tested without a cloud.

Three shapes, one per thing the workbench asks:

* :class:`CarveSet` is what the planner wrote and the CLI executes: one
  :class:`CarveMove` per resource, its ``children`` the untaggable followers
  that ride the parent-read path (informational here, never moved separately).
* :class:`MovePreview` is one move as ``live-mv -dry-run`` reported it: the
  exact tag writes, the resource it found, and any refusal, so the page draws
  the map as it would stand after the move without parsing the terminal.
* :class:`CarveSetVerdict` is the guard over the set: every involved estate
  plans clean, and every moved address now reads under its destination.
"""

from __future__ import annotations

import dataclasses
import json
import re
from typing import Any

from . import verify


# --------------------------------------------------------------------------
# The move set (carve.json)
# --------------------------------------------------------------------------

@dataclasses.dataclass(frozen=True)
class CarveMove:
    """One resource leaving one estate for another. ``children`` are the
    untaggable followers (an inline role policy, an attachment) that the
    parent-read path carries automatically; they are named here so the guard
    can assert they stayed attached, never because the CLI moves them."""

    address: str
    from_estate: str
    to_estate: str
    new_address: str = ""          # the post-move address; == address for a pure retag
    children: tuple[str, ...] = ()

    @property
    def target(self) -> str:
        """The address the resource carries after the move: new_address when
        the block was also renamed, otherwise the address it moved under."""
        return self.new_address or self.address


@dataclasses.dataclass(frozen=True)
class CarveSet:
    moves: tuple[CarveMove, ...]

    @property
    def source_estates(self) -> tuple[str, ...]:
        return _uniq(m.from_estate for m in self.moves)

    @property
    def dest_estates(self) -> tuple[str, ...]:
        return _uniq(m.to_estate for m in self.moves)

    @property
    def estates(self) -> tuple[str, ...]:
        """Every estate the set touches, source or destination, each once.
        These are the estates the guard plans, one plan apiece however many
        resources cross between them."""
        return _uniq([*(m.from_estate for m in self.moves), *(m.to_estate for m in self.moves)])

    def moves_to(self, estate: str) -> tuple[CarveMove, ...]:
        return tuple(m for m in self.moves if m.to_estate == estate)


def _uniq(xs: Any) -> tuple[str, ...]:
    seen: dict[str, None] = {}
    for x in xs:
        seen.setdefault(x, None)
    return tuple(seen)


def load_carve(text: str) -> CarveSet:
    """Read carve.json. The schema is ``{"moves": [{"address", "from_estate",
    "to_estate", "children"?}, ...]}``; any other field a planner keeps
    (module, rule provenance) is ignored. Raises ``ValueError`` on anything
    it cannot read as a move rather than guessing."""
    try:
        doc = json.loads(text)
    except json.JSONDecodeError as e:
        raise ValueError(f"carve.json is not valid JSON: {e}") from e
    if not isinstance(doc, dict) or not isinstance(doc.get("moves"), list):
        raise ValueError('carve.json must be an object with a "moves" array')
    moves = []
    for i, m in enumerate(doc["moves"]):
        if not isinstance(m, dict):
            raise ValueError(f"move {i} is not an object")
        try:
            addr, frm, to = m["address"], m["from"], m["to"]
        except KeyError as e:
            raise ValueError(f"move {i} is missing {e}") from e
        if not (isinstance(addr, str) and isinstance(frm, str) and isinstance(to, str) and addr and frm and to):
            raise ValueError(f"move {i}: address, from and to must be non-empty strings")
        if frm == to:
            raise ValueError(f"move {i} ({addr}): from and to are the same estate {to!r}")
        new_addr = m.get("new_address", "") or ""
        if not isinstance(new_addr, str):
            raise ValueError(f"move {i} ({addr}): new_address must be a string")
        children = m.get("children", [])
        if not isinstance(children, list) or not all(isinstance(c, str) for c in children):
            raise ValueError(f"move {i} ({addr}): children must be a list of strings")
        moves.append(CarveMove(address=addr, from_estate=frm, to_estate=to, new_address=new_addr, children=tuple(children)))
    if not moves:
        raise ValueError("carve.json declares no moves")
    return CarveSet(moves=tuple(moves))


# --------------------------------------------------------------------------
# The dry-run preview (live-mv -dry-run)
# --------------------------------------------------------------------------

@dataclasses.dataclass(frozen=True)
class TagWrite:
    key: str
    frm: str
    to: str


@dataclasses.dataclass(frozen=True)
class Refusal:
    summary: str
    detail: str


@dataclasses.dataclass(frozen=True)
class MovePreview:
    """One move as ``live-mv -dry-run`` reported it, or the refusal it raised.
    ``written`` is always False for a dry run; it is a field so the same shape
    can carry a real write if a caller ever previews after the fact."""

    address: str
    old_address: str
    from_estate: str
    to_estate: str
    type: str
    live_id: str
    found_by: str
    tag_writes: tuple[TagWrite, ...]
    children: tuple[str, ...]
    written: bool
    refusal: Refusal | None

    @property
    def ok(self) -> bool:
        return self.refusal is None

    def as_event(self) -> dict[str, Any]:
        """The flat shape the visuals side accepts: ``from``/``to`` on each
        tag write, ``refusal`` null or ``{summary, detail}``. Built by hand so
        the reserved word ``from`` is a real key, which a dataclass field
        cannot be."""
        return {
            "address": self.address,
            "old_address": self.old_address,
            "from_estate": self.from_estate,
            "to_estate": self.to_estate,
            "type": self.type,
            "live_id": self.live_id,
            "found_by": self.found_by,
            "tag_writes": [{"key": t.key, "from": t.frm, "to": t.to} for t in self.tag_writes],
            "children": list(self.children),
            "written": self.written,
            "refusal": None if self.refusal is None else {"summary": self.refusal.summary, "detail": self.refusal.detail},
        }


# The report renders rows as "  %-14s %s" (internal/command/views/live_mv.go):
# two leading spaces, the label padded to 14, then the value. Labels never
# carry two spaces in a row, so the value is whatever follows the first run of
# two or more. A tag-write value is a quoted arrow: "<from>" -> "<to>".
_ROW = re.compile(r'^  (\S.*?)\s{2,}(.+?)\s*$', re.M)
_ARROW = re.compile(r'^"(.*)" -> "(.*)"$')
# tfdiags renders a refusal as: Error: <summary>, a blank line, then the
# indented detail. The summary alone is enough to key on; the detail is the
# block up to the next blank line.
_ERROR = re.compile(r'^Error:\s*(.+?)\s*$', re.M)


def parse_refusal(text: str) -> Refusal | None:
    """A refusal, if the output carries a tfdiags ``Error:`` diagnostic. The
    summary is the Error line; the detail is the indented paragraph that
    follows it, which tfdiags wraps and indents."""
    m = _ERROR.search(text)
    if not m:
        return None
    summary = m.group(1)
    rest = text[m.end():].splitlines()
    detail_lines = []
    started = False
    for line in rest:
        if not line.strip():
            if started:
                break
            continue
        started = True
        detail_lines.append(line.strip())
    return Refusal(summary=summary, detail=" ".join(detail_lines))


def parse_dry_run(text: str, move: CarveMove | None = None) -> MovePreview:
    """Read one ``live-mv -dry-run`` block into a :class:`MovePreview`. A
    refusal short-circuits: the diagnostic is captured and the write fields
    are left empty. ``move`` supplies the ``children`` (informational, from
    carve.json) and a fallback address when a refusal printed no block."""
    refusal = parse_refusal(text)
    rows = {label: value for label, value in _ROW.findall(text)}
    tag_writes = []
    for key in ("tofu-estate", "tofu-address"):
        v = rows.get(key)
        if v:
            a = _ARROW.match(v)
            if a:
                tag_writes.append(TagWrite(key=key, frm=a.group(1), to=a.group(2)))
    children = move.children if move is not None else ()
    old_addr = rows.get("old address", "") or (move.address if move else "")
    new_addr = rows.get("new address", "") or old_addr
    return MovePreview(
        address=new_addr,
        old_address=old_addr,
        from_estate=rows.get("from estate", "") or (move.from_estate if move else ""),
        to_estate=rows.get("to estate", "") or rows.get("estate", "") or (move.to_estate if move else ""),
        type=rows.get("resource type", ""),
        live_id=rows.get("live ID", ""),
        found_by=rows.get("found by", ""),
        tag_writes=tuple(tag_writes),
        children=children,
        written="Nothing was written" not in text and refusal is None and "cloud write" in text,
        refusal=refusal,
    )


# --------------------------------------------------------------------------
# The guard over the set
# --------------------------------------------------------------------------

@dataclasses.dataclass(frozen=True)
class MoveResult:
    """One moved resource's own verdict: its address now reads under the
    destination, and both the estate it left and the estate it joined plan
    clean. ``live_estate`` is the tofu-estate the tag index reports for the
    address, ``None`` when the address is not found there."""

    address: str
    from_estate: str
    to_estate: str
    landed: bool
    live_estate: str | None
    source_clean: bool
    dest_clean: bool

    @property
    def ok(self) -> bool:
        return self.landed and self.source_clean and self.dest_clean

    def line(self) -> str:
        where = f"tofu-estate={self.live_estate}" if self.live_estate else "not in the destination's tag index"
        return f"{self.address}: {where} ({'landed' if self.landed else 'NOT landed'}); {self.from_estate} {'clean' if self.source_clean else 'DIRTY'}, {self.to_estate} {'clean' if self.dest_clean else 'DIRTY'}"


@dataclasses.dataclass(frozen=True)
class CarveSetVerdict:
    """The guard over the whole move set. ``ok`` only when every estate the
    set touches plans clean and every moved address landed under its
    destination. A child dropped from both configs still surfaces here: it
    orphans under the estate that now owns its parent, whose plan then is not
    clean."""

    per_estate: dict[str, verify.PlanVerdict]
    per_move: tuple[MoveResult, ...]
    source_estates: tuple[str, ...]
    dest_estates: tuple[str, ...]

    @property
    def all_clean(self) -> bool:
        return all(v.clean for v in self.per_estate.values())

    @property
    def all_landed(self) -> bool:
        return all(m.landed for m in self.per_move)

    @property
    def ok(self) -> bool:
        return bool(self.per_move) and self.all_clean and self.all_landed

    def lines(self) -> list[str]:
        out = [f"{len(self.per_move)} move(s) across {len(self.per_estate)} estate(s)"]
        for e in self.source_estates:
            v = self.per_estate[e]
            out.append(f"source {e}: {'clean, nothing left behind' if v.clean and v.leaves_nothing_behind else v.describe()}")
        for e in self.dest_estates:
            if e in self.source_estates:
                continue
            v = self.per_estate[e]
            out.append(f"destination {e}: {'clean, owns all it declares' if v.clean and v.owns_everything_it_declares else v.describe()}")
        out.extend(m.line() for m in self.per_move)
        return out


def compose(cs: CarveSet, per_estate: dict[str, verify.PlanVerdict], landed: dict[str, tuple[bool, str | None]]) -> CarveSetVerdict:
    """Build the set verdict from the per-estate plans and the per-address
    landing (each address -> (present in its destination's tag index, the
    tofu-estate the index reports)). Pure: :mod:`govern` gathers the inputs."""
    results = []
    for m in cs.moves:
        present, live = landed.get(m.target, (False, None))
        results.append(MoveResult(
            address=m.target,
            from_estate=m.from_estate,
            to_estate=m.to_estate,
            landed=present,
            live_estate=live,
            source_clean=per_estate[m.from_estate].clean,
            dest_clean=per_estate[m.to_estate].clean,
        ))
    return CarveSetVerdict(
        per_estate=per_estate,
        per_move=tuple(results),
        source_estates=cs.source_estates,
        dest_estates=cs.dest_estates,
    )
