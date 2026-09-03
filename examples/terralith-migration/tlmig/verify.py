"""The governance guard's reads, as pure functions over captured output.

The finale is a check that a carve left nothing behind. Every fact it needs
is something the cloud or a plan already prints, so this module never
decides anything from choudoufu's own report of itself: it parses plan text
and AWS CLI JSON into small verdicts a beat can print and assert on. No
subprocess here; guard.chdf and guard.aws capture the text, this reads it.

The shapes are pinned by the claim smokes on the same release, so a test
here fails the moment a line moves: carve-by-retag's BREAK prints
"Owned and undeclared: 2 live resources will be destroyed" and
"Plan: 0 to add, 0 to change, 5 to destroy." on the source, and
"aws_iam_role.team_0001_role [UNOWNED]" on the destination; its green path
prints "No changes." on both.
"""

from __future__ import annotations

import dataclasses
import json
import re

_PLAN_LINE = re.compile(r"^Plan: (\d+) to add, (\d+) to change, (\d+) to destroy\.", re.M)
_OWNED_UNDECLARED = re.compile(r"^Owned and undeclared: (\d+) live resources? will be destroyed", re.M)
_ACTION = re.compile(r"^  # (\S+) will be (created|updated in-place|destroyed|replaced)", re.M)
_UNOWNED = re.compile(r"^\s+(\S+) \[UNOWNED\]", re.M)
_ABSENT = re.compile(r"^\s+(\S+) \[ABSENT\]", re.M)


@dataclasses.dataclass(frozen=True)
class PlanVerdict:
    """What one plan proposes, reduced to the facts a carve guard asks."""

    clean: bool
    add: int
    change: int
    destroy: int
    destroys: tuple[str, ...]
    creates: tuple[str, ...]
    owned_undeclared: int
    unowned: tuple[str, ...]
    absent: tuple[str, ...]

    @property
    def leaves_nothing_behind(self) -> bool:
        """The source side of a carve after the retag: nothing to destroy and
        nothing this estate still claims without declaring."""
        return self.destroy == 0 and self.owned_undeclared == 0 and not self.destroys

    @property
    def owns_everything_it_declares(self) -> bool:
        """The destination side after the retag: nothing unowned, nothing
        absent, nothing to create."""
        return self.add == 0 and not self.unowned and not self.absent and not self.creates


def parse_plan(text: str) -> PlanVerdict:
    """Read a plan's text (the human output, -no-color). "No changes." is the
    clean case and short-circuits everything else; otherwise the Plan: line
    and the per-resource headers are both read, and they must agree."""
    clean = "No changes." in text
    m = _PLAN_LINE.search(text)
    add, change, destroy = (int(x) for x in m.groups()) if m else (0, 0, 0)
    ou = _OWNED_UNDECLARED.search(text)
    owned_undeclared = int(ou.group(1)) if ou else 0
    destroys = tuple(a for a, kind in _ACTION.findall(text) if kind == "destroyed")
    creates = tuple(a for a, kind in _ACTION.findall(text) if kind == "created")
    if m and len(destroys) != destroy:
        raise ValueError(
            f"the Plan: line says {destroy} to destroy but {len(destroys)} destroy headers were printed; refusing to grade a plan that disagrees with itself"
        )
    return PlanVerdict(
        clean=clean,
        add=add,
        change=change,
        destroy=destroy,
        destroys=destroys,
        creates=creates,
        owned_undeclared=owned_undeclared,
        unowned=tuple(_UNOWNED.findall(text)),
        absent=tuple(_ABSENT.findall(text)),
    )


def estate_of_role(list_role_tags_json: str) -> str | None:
    """The tofu-estate tag off `aws iam list-role-tags --role-name X`, read
    from the CLI's JSON, or None when the role carries none."""
    doc = json.loads(list_role_tags_json)
    for tag in doc.get("Tags", []):
        if tag.get("Key") == "tofu-estate":
            return tag.get("Value")
    return None


def address_of_role(list_role_tags_json: str) -> str | None:
    doc = json.loads(list_role_tags_json)
    for tag in doc.get("Tags", []):
        if tag.get("Key") == "tofu-address":
            return tag.get("Value")
    return None


def inline_policies(list_role_policies_json: str) -> tuple[str, ...]:
    """The names off `aws iam list-role-policies --role-name X`."""
    return tuple(json.loads(list_role_policies_json).get("PolicyNames", []))


def attached_policy_arns(list_attached_json: str) -> tuple[str, ...]:
    """The ARNs off `aws iam list-attached-role-policies --role-name X`."""
    return tuple(p["PolicyArn"] for p in json.loads(list_attached_json).get("AttachedPolicies", []))


@dataclasses.dataclass(frozen=True)
class CarveVerdict:
    """The whole finale in one object: each field is one read, and `ok` is
    true only when all four say the carve left nothing behind."""

    source: PlanVerdict
    destination: PlanVerdict
    moved_estates: dict[str, str | None]      # role name -> live tofu-estate
    children_kept: dict[str, tuple[str, ...]] # role name -> inline policies still attached
    expected_estate: str

    @property
    def ok(self) -> bool:
        return (
            self.source.leaves_nothing_behind
            and self.destination.owns_everything_it_declares
            and all(e == self.expected_estate for e in self.moved_estates.values())
        )

    def lines(self) -> list[str]:
        out = []
        out.append(f"source plan: {'clean, nothing left behind' if self.source.leaves_nothing_behind else f'{self.source.destroy} to destroy, owned-and-undeclared {self.source.owned_undeclared}'}")
        out.append(f"destination plan: {'clean, owns all it declares' if self.destination.owns_everything_it_declares else f'{self.destination.add} to add, unowned {list(self.destination.unowned)}'}")
        for role, est in self.moved_estates.items():
            out.append(f"{role}: tofu-estate={est} ({'ok' if est == self.expected_estate else 'WRONG, want ' + self.expected_estate})")
        for role, pols in self.children_kept.items():
            out.append(f"{role}: inline policies still attached: {', '.join(pols) or 'none'}")
        return out
