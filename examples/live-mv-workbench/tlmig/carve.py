"""The carve plan: which addresses go to which estate.

The plan is the fence. On a user's estate the tool writes only the tags of
the addresses named here, into the estates named here, through live-mv and
its own refusals. The page authors it as a table filled by rules; the CLI
reads the same file. Rules are how rows were filled and are informational
to the executor; ``moves`` is what it acts on.

    {"from": "<source estate>", "estates": ["<dest>", ...],
     "moves": [{"address": "<tofu-address>", "from": "<its estate now>", "to": "<dest>", "new_address": "<optional>"}],
     "rules": [{"match": "module"|"prefix"|"type"|"name", "value": "...", "to": "<dest>"}]}

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


def plan(source: str, resources: list[tuple[str, str, str]], rules: list[Rule],
         overrides: dict[str, str] | None = None) -> dict:
    """The carve plan for ``resources`` as (address, type, current estate):
    only rows whose destination differs from where they are become moves.
    Untaggable children are never moves; they follow their parent."""
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
    """One line per destination: what moves there."""
    out = []
    for est in doc.get("estates", []):
        addrs = [m["address"] for m in doc.get("moves", []) if m["to"] == est]
        out.append(f"{est}: {len(addrs)} moves ({', '.join(addrs[:4])}{', ...' if len(addrs) > 4 else ''})")
    if not out:
        out.append("no moves: every row keeps its estate")
    return out
