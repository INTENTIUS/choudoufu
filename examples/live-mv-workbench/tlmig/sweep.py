"""Teardown sweep: after the manifest-driven destroy, read the account
back and refuse if anything of this run is still there.

Three lists, because each one misses something the others see. The tagging
index answers by estate tag across every service it indexes, but it lags
behind terminations and does not index IAM on a real account. IAM is read
directly by name prefix (roles, customer-managed policies, instance
profiles), which is exact and immediate. EC2 instances are read directly
too, because the tagging index keeps a terminated instance's ARN for a while
after it is gone, and a terminated instance is not a leftover. CloudWatch log
groups, the bulk of the fixture, are read directly by name prefix as well,
so a lagging tagging index cannot let one survive teardown unnoticed.

Nothing here writes. ``assert_torn_down`` raises ``Leftovers`` (a
``guard.GuardError``) naming every item found, so a teardown that missed
something fails loud rather than reporting an empty exit code.
"""

from __future__ import annotations

import dataclasses
import json

from . import config, events, guard, ui


@dataclasses.dataclass(frozen=True)
class Leftover:
    kind: str      # "iam role", "iam policy", "instance profile", "ec2 instance", "tagged resource"
    name: str      # name or ARN
    estate: str | None = None

    def __str__(self) -> str:
        est = f" (tofu-estate={self.estate})" if self.estate else ""
        return f"{self.kind} {self.name}{est}"


class Leftovers(guard.GuardError):
    """Raised by assert_torn_down with every item still present."""

    def __init__(self, items: list[Leftover]):
        self.items = items
        listing = "\n".join(f"  - {i}" for i in items)
        super().__init__(f"{len(items)} resource(s) of this run are still in the account:\n{listing}")


# --------------------------------------------------------------------------
# Pure filters over CLI JSON, so the logic is testable without an account
# --------------------------------------------------------------------------

def iam_leftovers(prefix: str, roles_json: str, policies_json: str, profiles_json: str) -> list[Leftover]:
    out: list[Leftover] = []
    for r in json.loads(roles_json).get("Roles", []):
        if r.get("RoleName", "").startswith(prefix):
            out.append(Leftover("iam role", r["RoleName"]))
    for p in json.loads(policies_json).get("Policies", []):
        if p.get("PolicyName", "").startswith(prefix):
            out.append(Leftover("iam policy", p["PolicyName"]))
    for ip in json.loads(profiles_json).get("InstanceProfiles", []):
        if ip.get("InstanceProfileName", "").startswith(prefix):
            out.append(Leftover("instance profile", ip["InstanceProfileName"]))
    return out


def ec2_leftovers(estate: str, describe_instances_json: str) -> list[Leftover]:
    """Instances carrying the estate tag in any state but terminated."""
    out: list[Leftover] = []
    for res in json.loads(describe_instances_json).get("Reservations", []):
        for inst in res.get("Instances", []):
            if inst.get("State", {}).get("Name") == "terminated":
                continue
            out.append(Leftover("ec2 instance", inst["InstanceId"], estate))
    return out


def log_group_leftovers(prefix: str, describe_log_groups_json: str) -> list[Leftover]:
    """Every log group under /<prefix>/, off `aws logs describe-log-groups
    --log-group-name-prefix`. The fixture names them /<prefix>/<team>/svc-N,
    so anything the read returns is this run's."""
    out: list[Leftover] = []
    for g in json.loads(describe_log_groups_json).get("logGroups", []):
        name = g.get("logGroupName", "")
        if name.startswith(f"/{prefix}/"):
            out.append(Leftover("log group", name))
    return out


def tagged_leftovers(estate: str, get_resources_json: str) -> list[Leftover]:
    """Everything the tagging index still lists under the estate, minus EC2
    instances, which ec2_leftovers judges by live state instead."""
    out: list[Leftover] = []
    for m in json.loads(get_resources_json).get("ResourceTagMappingList", []):
        arn = m.get("ResourceARN", "")
        if ":instance/" in arn and ":ec2:" in arn:
            continue
        out.append(Leftover("tagged resource", arn, estate))
    return out


# --------------------------------------------------------------------------
# The read
# --------------------------------------------------------------------------

def run_estates(cfg: config.Config) -> tuple[str, ...]:
    """Every estate name this run can have stamped: the monolith and one per
    team."""
    return (cfg.monolith_estate, *(cfg.estate(t) for t in config.TEAMS))


def find_leftovers(cfg: config.Config) -> list[Leftover]:
    """Read the account and return what is still there. Reads only."""
    found: list[Leftover] = []

    roles = guard.aws(cfg, "iam", "list-roles", "--output", "json").stdout
    policies = guard.aws(cfg, "iam", "list-policies", "--scope", "Local", "--output", "json").stdout
    profiles = guard.aws(cfg, "iam", "list-instance-profiles", "--output", "json").stdout
    found.extend(iam_leftovers(cfg.prefix, roles, policies, profiles))

    groups = guard.aws(
        cfg, "logs", "describe-log-groups", "--region", cfg.region,
        "--log-group-name-prefix", f"/{cfg.prefix}/", "--output", "json",
    ).stdout
    found.extend(log_group_leftovers(cfg.prefix, groups))

    for estate in run_estates(cfg):
        inst = guard.aws(
            cfg, "ec2", "describe-instances", "--region", cfg.region,
            "--filters", f"Name=tag:tofu-estate,Values={estate}", "--output", "json",
        ).stdout
        found.extend(ec2_leftovers(estate, inst))
        tagged = guard.aws(
            cfg, "resourcegroupstaggingapi", "get-resources", "--region", cfg.region,
            "--tag-filters", f"Key=tofu-estate,Values={estate}", "--output", "json",
        ).stdout
        found.extend(tagged_leftovers(estate, tagged))
    return found


def assert_torn_down(cfg: config.Config) -> None:
    """Raise Leftovers unless the account holds nothing of this run. Prints
    what was checked either way, so a clean result is a statement about
    named reads rather than an absence of errors."""
    ui.rule(f"teardown check: run {cfg.run_id} in account {guard.caller_account(cfg)}")
    found = find_leftovers(cfg)
    events.verdict(
        cfg, "teardown",
        {"clean": not found, "leftovers": [dataclasses.asdict(i) for i in found]},
        ok=not found, lines=[str(i) for i in found] or ["nothing of this run is left in the account"],
    )
    if found:
        for item in found:
            ui.err(str(item))
        raise Leftovers(found)
    ui.ok(f"nothing carrying prefix {cfg.prefix} in IAM or under /{cfg.prefix}/ in CloudWatch Logs, and nothing live under {', '.join(run_estates(cfg))}")
