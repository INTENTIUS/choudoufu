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
# The dry-run preview (live-mv -json -dry-run)
# --------------------------------------------------------------------------
#
# The preview reads the document -json prints, not the labelled rows the
# human report renders. internal/command/views/live_mv.go names this
# workbench's old reconstruction as the reason -json exists (GitHub issue
# #791), and the document is the better source on three counts: it is printed
# on a refusal as well as a success, so a refused preview needs no second
# parser; it carries the refusal's stable code beside the prose; and its
# ``found_by`` is mv.Path's own value, "LIST" or "IDENTITY", rather than the
# sentence the human report wraps it in.
#
# The text parser below stays as the fallback for the one path that prints no
# document: a command line -json never reached, which live-mv answers with the
# usage text before the report is rendered at all.


@dataclasses.dataclass(frozen=True)
class TagWrite:
    key: str
    frm: str
    to: str


@dataclasses.dataclass(frozen=True)
class Refusal:
    """A refusal as the document reports one. ``code`` is
    mv.RefusalCode's own stable value where the refusal is one of the five
    named shapes, and empty otherwise - which is the document's own
    convention, not a loss of information: ``summary`` and ``detail`` are
    always set."""

    summary: str
    detail: str
    code: str = ""


@dataclasses.dataclass(frozen=True)
class MovePreview:
    """One move as ``live-mv -json -dry-run`` reported it, or the refusal it
    raised. ``written`` is always False for a dry run; it is a field so the
    same shape can carry a real write if a caller ever previews after the
    fact, and ``verified`` beside it is the document's own second fact: the
    write returned no error, and the marker was read back."""

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
    display_name: str = ""
    verified: bool = False
    dry_run: bool = True

    @property
    def ok(self) -> bool:
        return self.refusal is None

    def as_event(self) -> dict[str, Any]:
        """The flat shape the visuals side accepts: ``from``/``to`` on each
        tag write, ``refusal`` null or ``{summary, detail, code}``. Built by
        hand so the reserved word ``from`` is a real key, which a dataclass
        field cannot be."""
        return {
            "address": self.address,
            "old_address": self.old_address,
            "from_estate": self.from_estate,
            "to_estate": self.to_estate,
            "type": self.type,
            "live_id": self.live_id,
            "display_name": self.display_name,
            "found_by": self.found_by,
            "tag_writes": [{"key": t.key, "from": t.frm, "to": t.to} for t in self.tag_writes],
            "children": list(self.children),
            "written": self.written,
            "verified": self.verified,
            "dry_run": self.dry_run,
            "refusal": None if self.refusal is None else {
                "summary": self.refusal.summary, "detail": self.refusal.detail, "code": self.refusal.code},
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


# The document -json prints is one JSON value on stdout. Warnings go to
# stderr under -json precisely so they cannot corrupt it
# (internal/command/live_mv.go's own comment), but govern hands us both
# streams concatenated so a hard failure is still legible, so the document
# has to be found in the text rather than assumed to be all of it.
#
# Keys only this document has, used to tell it from a JSON object that
# happened to appear inside a diagnostic's prose.
_DOC_KEYS = frozenset({"from", "to", "resource", "refusal", "dry_run", "written", "found_by"})


def find_document(text: str) -> dict | None:
    """The JSON document in ``text``, or None when there is not one.

    Scans for a decodable object rather than matching a layout, because the
    engine's own rendering (MarshalIndent) and a test's compact one are the
    same document. Returns None rather than raising on text that carries no
    document: the caller's fallback is the labelled-row parser, and that is
    exactly the case that wants it."""
    dec = json.JSONDecoder()
    at = text.find("{")
    while at != -1:
        try:
            doc, _ = dec.raw_decode(text, at)
        except json.JSONDecodeError:
            doc = None
        if isinstance(doc, dict) and _DOC_KEYS & doc.keys():
            return doc
        at = text.find("{", at + 1)
    return None


def parse_json_report(doc: dict, move: CarveMove | None = None) -> MovePreview:
    """Read one ``live-mv -json`` document into a :class:`MovePreview`.

    The mapping is the document's, field for field
    (views.StatelessMvJSONReport): ``resource`` is the live object,
    ``from``/``to`` are the two endpoints, and ``found_by`` is "LIST" or
    "IDENTITY". The two tag writes are derived rather than transcribed,
    because the document reports endpoints and not writes: the tofu-estate
    write exists exactly when the two endpoints name different estates, and
    the tofu-address write is the two ``marker`` values, which are the escaped
    tag values and not the addresses beside them.

    ``move`` supplies fallbacks for the one case the document leaves empty: a
    refusal raised before the live resource was ever found, where the engine
    has nothing but the two addresses it was given.
    """
    resource = doc.get("resource") or {}
    frm = doc.get("from") or {}
    to = doc.get("to") or {}

    ref = doc.get("refusal")
    refusal = None
    if isinstance(ref, dict):
        refusal = Refusal(summary=ref.get("summary", ""), detail=ref.get("detail", ""), code=ref.get("code", ""))

    from_estate = frm.get("estate") or (move.from_estate if move else "")
    to_estate = to.get("estate") or (move.to_estate if move else "")

    # Derived from the document's own endpoints, never from the carve.json
    # fallback above: a preview may report a tag write only where the engine
    # reported the endpoints it would write between. A refusal raised before
    # the live resource was found leaves both empty, and the preview then
    # says no writes - which is the truth - while still naming the estates
    # the plan asked for.
    tag_writes = []
    if frm.get("estate") and to.get("estate") and frm["estate"] != to["estate"]:
        tag_writes.append(TagWrite(key="tofu-estate", frm=frm["estate"], to=to["estate"]))
    old_marker, new_marker = frm.get("marker", ""), to.get("marker", "")
    if old_marker or new_marker:
        tag_writes.append(TagWrite(key="tofu-address", frm=old_marker, to=new_marker))

    # followers[] is the document's own answer to "what moves with it and is
    # never written". It is omitted, not empty, when there are none, so an
    # absent key on a run that found the resource means none - carve.json's
    # informational children are the fallback only when the document never
    # got as far as a resource.
    followers = doc.get("followers")
    if isinstance(followers, list):
        children = tuple(f.get("address", "") for f in followers if isinstance(f, dict))
    elif resource.get("type") or doc.get("found_by"):
        children = ()
    else:
        children = move.children if move is not None else ()

    return MovePreview(
        address=to.get("address") or (move.target if move else ""),
        old_address=frm.get("address") or (move.address if move else ""),
        from_estate=from_estate,
        to_estate=to_estate,
        type=resource.get("type", ""),
        live_id=resource.get("live_id", ""),
        display_name=resource.get("display_name", ""),
        found_by=doc.get("found_by", ""),
        tag_writes=tuple(tag_writes),
        children=children,
        written=bool(doc.get("written")),
        verified=bool(doc.get("verified")),
        dry_run=bool(doc.get("dry_run")),
        refusal=refusal,
    )


def parse_preview(text: str, move: CarveMove | None = None) -> MovePreview:
    """One ``live-mv`` run's output as a preview. The document when there is
    one, the labelled rows when there is not - the second only reachable when
    -json never rendered at all (a command line the parser rejected, which
    live-mv answers with usage text)."""
    doc = find_document(text)
    if doc is not None:
        return parse_json_report(doc, move=move)
    return parse_dry_run(text, move=move)


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
