"""The governance guard's cloud wrapper: the finale's four reads, run through
``guard`` so they land in the transcript, fed to ``verify``'s parsers, and
printed through ``ui``. ``verify`` stays pure; this is the only module that
talks to the cloud on the guard's behalf.

The four reads for one moved role. Its live tofu-estate through the IAM
API. Its inline policies and attachments, still there though nothing wrote
them. The source estate's plan, which must be "No changes." with no
"Owned and undeclared" line and no destroy header. The destination's plan,
"No changes." too. A correct carve is clean on both sides, and
``verify.CarveVerdict.ok`` says so only when all of that holds.
"""

from __future__ import annotations

from . import config, events, guard, ui, verify


def plan_verdict(cfg: config.Config, estate: str) -> verify.PlanVerdict:
    """Plan one estate in its run working directory and parse the output. A
    nonzero exit with no "No changes." is a failure of the read itself, not
    a verdict, and is raised as such."""
    res = guard.chdf(
        cfg, "plan", "-input=false", "-no-color",
        cwd=str(cfg.workdir(estate)), capture=True, check=False,
    )
    if not res.ok and "No changes." not in res.stdout:
        raise guard.GuardError(f"plan in {estate} failed (exit {res.returncode})\n{res.stderr.strip()}")
    return verify.parse_plan(res.stdout)


def role_reads(cfg: config.Config, role: str) -> tuple[dict[str, str], tuple[str, ...], tuple[str, ...]]:
    """The role's tags, inline policy names and attached policy ARNs, read
    through the IAM API as the account."""
    tags_json = guard.aws(cfg, "iam", "list-role-tags", "--role-name", role, "--output", "json").stdout
    inline_json = guard.aws(cfg, "iam", "list-role-policies", "--role-name", role, "--output", "json").stdout
    attached_json = guard.aws(cfg, "iam", "list-attached-role-policies", "--role-name", role, "--output", "json").stdout
    tags = {
        "tofu-estate": verify.estate_of_role(tags_json) or "",
        "tofu-address": verify.address_of_role(tags_json) or "",
    }
    return tags, verify.inline_policies(inline_json), verify.attached_policy_arns(attached_json)


def read_carve(
    cfg: config.Config,
    role: str,
    source_estate: str,
    dest_estate: str,
    expected_inline: tuple[str, ...] | None = None,
) -> verify.CarveVerdict:
    """Run the four reads for one moved role, print each with its own
    pass/fail, and return the verdict. ``expected_inline`` (the inline policy
    names read before the move) turns the children line into an assertion;
    without it the line is informational, and a dropped child is still
    caught by the destination plan's destroy header."""
    ui.rule(f"governance guard: {role} left {source_estate} for {dest_estate}")

    tags, inline, attached = role_reads(cfg, role)
    source = plan_verdict(cfg, source_estate)
    destination = plan_verdict(cfg, dest_estate)

    verdict = verify.CarveVerdict(
        source=source,
        destination=destination,
        moved_estates={role: tags["tofu-estate"] or None},
        children_kept={role: inline},
        expected_estate=dest_estate,
    )

    ui.kv(f"{role} tofu-estate", tags["tofu-estate"] or "(none)", tags["tofu-estate"] == dest_estate)
    children_ok = True if expected_inline is None else tuple(sorted(inline)) == tuple(sorted(expected_inline))
    ui.kv(f"{role} inline policies", ", ".join(inline) or "none", children_ok)
    ui.kv(f"{role} attachments", str(len(attached)), True)
    ui.kv(f"{source_estate} plan", source.describe(), source.clean and source.leaves_nothing_behind)
    ui.kv(f"{dest_estate} plan", destination.describe(), destination.clean)

    events.verdict(cfg, f"carve:{role}", verdict)
    if verdict.ok and children_ok:
        ui.ok("nothing left behind: the tag moved, the children followed, both estates plan clean")
    else:
        ui.err("the carve left something behind; see the reads above")
    return verdict


# --------------------------------------------------------------------------
# The inventory an estate map diffs
# --------------------------------------------------------------------------

def _arn_type(arn: str) -> str:
    """"aws_iam_role"-style type from an ARN's service and resource
    segments, close enough for a map legend: iam role -> iam:role."""
    parts = arn.split(":", 5)
    if len(parts) < 6:
        return "unknown"
    service, resource = parts[2], parts[5]
    return f"{service}:{resource.split('/')[0].split(':')[0]}"


def read_inventory(cfg: config.Config, estate: str) -> list[dict]:
    """Everything the account holds under one estate, as items the visual
    diffs: {id, type, address, tags}. Two reads, because the tagging index
    does not cover IAM on a real account: get-resources by estate for
    everything it does index, then the run's roles by prefix with each
    role's own tags, kept when the role's tofu-estate is this estate. The
    result is emitted as an inventory event and returned."""
    items: list[dict] = []
    seen: set[str] = set()

    tagged = guard.aws(
        cfg, "resourcegroupstaggingapi", "get-resources", "--region", cfg.region,
        "--tag-filters", f"Key=tofu-estate,Values={estate}", "--output", "json",
    ).stdout
    import json as _json
    for m in _json.loads(tagged).get("ResourceTagMappingList", []):
        arn = m.get("ResourceARN", "")
        tags = {t["Key"]: t.get("Value", "") for t in m.get("Tags", [])}
        items.append({"id": arn, "type": _arn_type(arn), "address": tags.get("tofu-address", ""), "tags": tags})
        seen.add(arn)

    roles = _json.loads(guard.aws(cfg, "iam", "list-roles", "--output", "json").stdout).get("Roles", [])
    for r in roles:
        name = r.get("RoleName", "")
        if not name.startswith(cfg.prefix) or r.get("Arn", "") in seen:
            continue
        tags_json = guard.aws(cfg, "iam", "list-role-tags", "--role-name", name, "--output", "json").stdout
        tags = {t["Key"]: t.get("Value", "") for t in _json.loads(tags_json).get("Tags", [])}
        if tags.get("tofu-estate") != estate:
            continue
        items.append({"id": r.get("Arn", name), "type": "iam:role", "address": tags.get("tofu-address", ""), "tags": tags})

    items.sort(key=lambda i: (i["type"], i["address"], i["id"]))
    events.inventory(cfg, estate, items)
    ui.kv(f"{estate} inventory", f"{len(items)} resource(s)", None)
    return items
