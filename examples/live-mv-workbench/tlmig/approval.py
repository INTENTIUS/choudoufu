"""The approval gate, graded: `plan -out`, then `apply <planfile>` against a
world that moved, and the same file again against one that did not.

This is live/GAUNTLET.md stage 12 ("Plan, review, apply"), which v0.14.0 turned
from planned to active. The stage's own words are the oracle this grades
against: "`plan -out` followed by `apply <planfile>` applies when the world has
not moved and refuses, naming the mismatch, when it has." Its Break line is
"apply the planfile after a mutation and expect success; the run must refuse",
so a grader that cannot fail on a missing refusal is no grader at all -
:class:`GateVerdict` is false unless every half held.

Everything here is pure: :mod:`govern` runs the commands and reads the cloud,
this reads their text and composes the answer. The two halves it grades:

* the refusal. Exit 3, the engine's own summary, and the moved object named -
  all three, because an exit code alone is not a verdict and a message alone
  is not an exit code a pipeline can branch on.
* the inverted control. The IDENTICAL file, applied once the world is put
  back, applies - so the refusal was earned by the drift and not handed out to
  every plan file. Without this half a gate that refused everything would
  grade green.

And one fact neither half reports about itself: the value read back from the
cloud afterwards, which is how the verdict knows the approved change actually
landed rather than trusting an "Apply complete!" line.
"""

from __future__ import annotations

import dataclasses
import re

# The engine's own summary for this refusal (internal/live/approval's
# diagnostic, rendered by tfdiags). Matched as a substring, not a whole line:
# the terminal wraps it, and the words are the contract, not the wrapping.
REFUSAL_SUMMARY = "The approved plan no longer matches the live system"

# Exit 3 is the stage's own number: "Exit status 3 says exactly this: send it
# back to review."
REFUSAL_EXIT = 3

_SAVED = re.compile(r"^Saved the plan to:\s*(\S+)\s*$", re.M)
_CHANGED = re.compile(r"Apply complete!\s+Resources:\s+(\d+) added,\s+(\d+) changed,\s+(\d+) destroyed", re.M)
_RETENTION = re.compile(
    r'(resource\s+"aws_cloudwatch_log_group"\s+"%s"\s*\{[^}]*?retention_in_days\s*=\s*)(\d+)')


def saved_planfile(text: str) -> str:
    """The path `plan -out` says it wrote, or "" if it said it wrote none.
    Read from the output rather than from the flag, so a plan that silently
    wrote nothing is caught here and not two steps later."""
    m = _SAVED.search(text)
    return m.group(1).strip('"') if m else ""


def applied_counts(text: str) -> tuple[int, int, int] | None:
    """(added, changed, destroyed) from an "Apply complete!" line, or None
    when the apply printed none."""
    m = _CHANGED.search(text)
    return (int(m.group(1)), int(m.group(2)), int(m.group(3))) if m else None


def refused(text: str, returncode: int, address: str = "") -> bool:
    """True when this apply is the refusal stage 12 names: exit 3, the
    engine's summary, and - when an address is given - that address named in
    the mismatch it printed. All of them: a run that exits 3 for some other
    reason, or prints the summary and exits 0, is not this."""
    if returncode != REFUSAL_EXIT or REFUSAL_SUMMARY not in text:
        return False
    return address in text if address else True


def set_retention(hcl: str, name: str, days: int) -> str:
    """Rewrite one log group's ``retention_in_days``, leaving every other
    block alone. The demo's approved change: small, reversible, on a resource
    the seed already made, and visible in the account afterwards so the
    verdict can read it back rather than believe the apply."""
    pattern = re.compile(_RETENTION.pattern % re.escape(name))
    out, n = pattern.subn(lambda m: f"{m.group(1)}{days}", hcl, count=1)
    if n != 1:
        raise ValueError(f"no aws_cloudwatch_log_group {name!r} with a retention_in_days in this config")
    return out


@dataclasses.dataclass(frozen=True)
class GateVerdict:
    """One walk of the gate. ``ok`` only when the plan file was written, the
    drifted apply refused by all three signs, the identical file then applied,
    and the approved value is what the cloud reports now."""

    estate: str
    address: str
    planfile: str
    planfile_bytes: int
    drifted_exit: int
    drifted_refused: bool
    drifted_named: bool
    restored_exit: int
    restored_counts: tuple[int, int, int] | None
    approved_value: str
    live_value: str

    @property
    def wrote_planfile(self) -> bool:
        return bool(self.planfile) and self.planfile_bytes > 0

    @property
    def reapplied(self) -> bool:
        counts = self.restored_counts
        return self.restored_exit == 0 and counts is not None and counts[1] >= 1

    @property
    def landed(self) -> bool:
        return bool(self.live_value) and self.live_value == self.approved_value

    @property
    def ok(self) -> bool:
        return self.wrote_planfile and self.drifted_refused and self.drifted_named \
            and self.reapplied and self.landed

    def lines(self) -> list[str]:
        out = [
            f"{self.planfile} written, {self.planfile_bytes} bytes"
            if self.wrote_planfile else "plan -out wrote no plan file",
            f"world moved out of band, `apply {self.planfile}` exited {self.drifted_exit}"
            + (f" and refused, naming {self.address}" if self.drifted_refused and self.drifted_named
               else " and did NOT refuse as stage 12 requires"),
            f"world put back, the identical file applied ({self.restored_counts[1]} changed)"
            if self.reapplied else f"the identical file did not apply (exit {self.restored_exit})",
            f"{self.address} reads {self.live_value or '(unread)'} in the account; approved {self.approved_value}"
            if self.landed else
            f"{self.address} reads {self.live_value or '(unread)'} in the account, not the approved {self.approved_value}",
        ]
        return out
