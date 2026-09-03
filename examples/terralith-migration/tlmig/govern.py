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

from . import config, guard, ui, verify


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

    if verdict.ok and children_ok:
        ui.ok("nothing left behind: the tag moved, the children followed, both estates plan clean")
    else:
        ui.err("the carve left something behind; see the reads above")
    return verdict
