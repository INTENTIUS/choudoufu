"""The carve plan: which addresses go to which estate.

The plan is the fence. On a user's estate the tool writes only the tags of
the addresses named here, into the estates named here, through live-mv and
its own refusals. The page authors it as a table filled by rules; the CLI
reads the same file. Rules are how rows were filled and are informational
to the executor; ``moves`` is what it acts on.

    {"from": "<source estate>", "estates": ["<dest>", ...],
     "moves": [{"address": "<tofu-address>", "from": "<its estate now>", "to": "<dest>", "new_address": "<optional>",
                "rewrites": <int>, "data_sources": ["data.<type>.<name>", ...], "read_by": ["<address>", ...]}],
     "rules": [{"match": "module"|"prefix"|"type"|"name", "value": "...", "to": "<dest>"}]}

A move is not free on the other side of an estate boundary. Where another
estate reads this one through the cross-estate data-source pattern
(live/OUTPUTS.md's replacement for the banned terraform_remote_state), moving
the producer means rewriting that data source's ``tag:tofu-estate`` filter.
``live-check -json`` reports those edges as ``references[]``, and the last
three fields above are what the planner makes of them - see
:func:`references_from_check` and :func:`price`.

Rules in text, one per line, the way the page takes them:

    module data -> team-data
    prefix aws_iam_ -> iam
    type aws_cloudwatch_log_group -> logs
    name team_a -> team-a

Later rules win over earlier ones; a per-row override wins over rules;
``keep`` as a destination means the row stays where it is.
"""
from __future__ import annotations

import dataclasses
import json
import pathlib
import re

KEEP = "keep"
KINDS = ("module", "prefix", "type", "name")


@dataclasses.dataclass(frozen=True)
class Rule:
    match: str      # module | prefix | type | name
    value: str
    to: str

    def applies(self, address: str, rtype: str) -> bool:
        if self.match == "module":
            return address.startswith(f"module.{self.value}.")
        if self.match == "prefix":
            return address.startswith(self.value)
        if self.match == "type":
            return rtype == self.value
        if self.match == "name":
            return self.value in address.split(".")[-1]
        return False


_RULE = re.compile(r"^\s*(module|prefix|type|name)\s+(\S+)\s*(?:->|→|=>)\s*(\S+)\s*$")


def parse_rules(text: str) -> tuple[list[Rule], list[str]]:
    """Rules from the page's text box; a line that is not a rule is returned
    as a problem rather than ignored, so a typo never silently keeps a row."""
    rules, problems = [], []
    for n, line in enumerate(text.splitlines(), start=1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        m = _RULE.match(line)
        if not m:
            problems.append(f"line {n}: not a rule ({line.strip()}); expected `module|prefix|type|name <value> -> <estate>`")
            continue
        rules.append(Rule(m.group(1), m.group(2), m.group(3)))
    return rules, problems


def destination(address: str, rtype: str, rules: list[Rule], override: str | None = None) -> str:
    """Where one resource goes: the override if given, else the last rule
    that applies, else keep."""
    if override:
        return override
    dest = KEEP
    for r in rules:
        if r.applies(address, rtype):
            dest = r.to
    return dest


@dataclasses.dataclass(frozen=True)
class Reference:
    """One cross-estate edge, as ``live-check -json``'s ``references[]``
    reports it: a data source in ``in_estate`` whose filters name the producer
    instance ``address`` in estate ``estate``, and the resources in that same
    configuration that read the data source.

    ``source`` is the document's ``from`` - the consuming data source's own
    address - renamed here because ``from`` is a reserved word and because
    this planner already uses "from" for the estate a move leaves.
    """

    source: str
    estate: str
    address: str
    read_by: tuple[str, ...] = ()
    in_estate: str = ""

    @property
    def cost(self) -> int:
        """The rewrite cost of moving :attr:`address` out of :attr:`estate`.

        internal/live/check/references.go states the rule this counts:
        "a move of Address costs one data-source filter rewrite per entry
        here" - one per reader. A data source nothing reads yet still appears
        in :attr:`Move.data_sources` and prices at zero, which is the engine's
        own arithmetic, not a rounding of it.
        """
        return len(self.read_by)


def references_from_check(doc: dict, in_estate: str = "") -> list[Reference]:
    """The references in one ``live-check -json`` document. ``in_estate``
    defaults to the document's own ``estate``, which is the estate whose
    configuration declares these data sources."""
    if not isinstance(doc, dict):
        raise ValueError("a live-check -json document is a JSON object")
    where = in_estate or doc.get("estate", "") or ""
    out = []
    for r in doc.get("references") or []:
        if not isinstance(r, dict):
            continue
        out.append(Reference(
            source=r.get("from", ""),
            estate=r.get("estate", ""),
            address=r.get("address", ""),
            read_by=tuple(r.get("read_by") or ()),
            in_estate=where,
        ))
    return out


def rewrites_for(address: str, current: str, references: list[Reference]) -> list[Reference]:
    """The references a move of ``address`` out of estate ``current``
    invalidates: every data source whose two filters name that estate and that
    address. A reference naming the estate and no address (the pattern permits
    ``tag:tofu-estate`` alone) matches nothing here on purpose - it does not
    say which instance it reads, so the planner will not guess that this move
    is the one that breaks it."""
    return [r for r in references if r.estate == current and r.address and r.address == address]


def price(moves: list[dict], references: list[Reference]) -> list[dict]:
    """Each move with its rewrite cost attached, in place. Returns the same
    list so a caller can chain it onto :func:`plan`'s ``moves``."""
    for m in moves:
        hit = rewrites_for(m["address"], m["from"], references)
        m["rewrites"] = sum(r.cost for r in hit)
        m["data_sources"] = [r.source for r in hit]
        m["read_by"] = sorted({addr for r in hit for addr in r.read_by})
    return moves


def total_rewrites(doc: dict) -> int:
    """The plan's whole rewrite cost: what a reader has to edit by hand after
    the tags are written."""
    return sum(int(m.get("rewrites", 0)) for m in doc.get("moves", []))


def plan(source: str, resources: list[tuple[str, str, str]], rules: list[Rule],
         overrides: dict[str, str] | None = None,
         references: list[Reference] | None = None) -> dict:
    """The carve plan for ``resources`` as (address, type, current estate):
    only rows whose destination differs from where they are become moves.
    Untaggable children are never moves; they follow their parent.

    ``references`` are the cross-estate edges ``live-check -json`` reported for
    the estates involved. Given them, every move also carries what it costs on
    the other side of the boundary: the data sources whose filters name it and
    the resources that read them."""
    overrides = overrides or {}
    moves, estates = [], []
    for address, rtype, current in resources:
        # An override may name the row by "<estate>:<address>", because the
        # same address can live in two estates, or by the address alone.
        dest = destination(address, rtype, rules, overrides.get(f"{current}:{address}", overrides.get(address)))
        if dest in (KEEP, current) or not dest:
            continue
        moves.append({"address": address, "from": current, "to": dest})
        if dest not in estates:
            estates.append(dest)
    if references:
        price(moves, references)
    return {"from": source, "estates": estates, "moves": moves,
            "rules": [dataclasses.asdict(r) for r in rules]}


def path(run_dir: str | pathlib.Path) -> pathlib.Path:
    return pathlib.Path(run_dir) / "carve.json"


def save(run_dir: str | pathlib.Path, doc: dict) -> pathlib.Path:
    p = path(run_dir)
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(json.dumps(doc, indent=2) + "\n")
    return p


def load(run_dir: str | pathlib.Path) -> dict | None:
    p = path(run_dir)
    return json.loads(p.read_text()) if p.exists() else None


def describe(doc: dict) -> list[str]:
    """One line per destination: what moves there, and what that costs on the
    other side of the boundary. A plan priced against no references reads the
    way it always did - "3 moves" - because a plan the planner could not price
    must not claim a cost of zero."""
    out = []
    for est in doc.get("estates", []):
        rows = [m for m in doc.get("moves", []) if m["to"] == est]
        addrs = [m["address"] for m in rows]
        cost = sum(int(m.get("rewrites", 0)) for m in rows)
        priced = any("rewrites" in m for m in rows)
        tail = f", {cost} filter rewrite{'' if cost == 1 else 's'}" if priced else ""
        out.append(f"{est}: {len(addrs)} moves{tail} ({', '.join(addrs[:4])}{', ...' if len(addrs) > 4 else ''})")
    if not out:
        out.append("no moves: every row keeps its estate")
    return out
