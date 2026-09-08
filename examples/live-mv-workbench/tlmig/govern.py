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

import json
import pathlib

from . import approval, carve, config, env, events, guard, measure, moveset, ui, verify


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

    shown = [f"{label}: {value}" for label, value, _ in (
        (f"{role} tofu-estate", tags["tofu-estate"] or "(none)", None),
        (f"{role} inline policies", ", ".join(inline) or "none", None),
        (f"{role} attachments", str(len(attached)), None),
        (f"{source_estate} plan", source.describe(), None),
        (f"{dest_estate} plan", destination.describe(), None),
    )]
    events.fact(cfg, f"role:{role}", tags["tofu-estate"] or None)
    events.verdict(cfg, f"carve:{role}", verdict, ok=verdict.ok and children_ok, lines=shown)
    if verdict.ok and children_ok:
        ui.ok("nothing left behind: the tag moved, the children followed, both estates plan clean")
    else:
        ui.err("the carve left something behind; see the reads above")
    return verdict


def read_carve_set(cfg: config.Config, carve_path: str | pathlib.Path) -> moveset.CarveSetVerdict:
    """The guard over an arbitrary carve. Reads carve.json, plans every estate
    the set touches once, reads each destination's tag index to see which
    moved addresses landed, and returns the set verdict. Emits one verdict
    per moved resource and one for the set, so the page grades a move at a
    time and the run has a single headline. The per-estate plan cost is
    measure.py's job (one measure per destination); this is the guard."""
    cs = moveset.load_carve(pathlib.Path(carve_path).read_text())
    ui.rule(f"governance guard: {len(cs.moves)} move(s) across {len(cs.estates)} estate(s)")

    per_estate = {e: plan_verdict(cfg, e) for e in cs.estates}
    landed: dict[str, tuple[bool, str | None]] = {}
    for e in cs.dest_estates:
        index = {i["address"]: i["tags"].get("tofu-estate") for i in read_inventory(cfg, e)}
        for m in cs.moves_to(e):
            landed[m.target] = (m.target in index, index.get(m.target))

    verdict = moveset.compose(cs, per_estate, landed)

    for e, v in per_estate.items():
        ui.kv(f"{e} plan", v.describe(), v.clean)
    for mr in verdict.per_move:
        ui.kv(f"move {mr.address}", mr.line(), mr.ok)
        events.verdict(cfg, f"carve:{mr.address}", mr, ok=mr.ok, lines=[mr.line()])
    events.verdict(cfg, "carve-set", verdict, ok=verdict.ok, lines=verdict.lines())
    if verdict.ok:
        ui.ok("every estate plans clean and every moved address landed under its destination")
    else:
        ui.err("the carve left something behind; see the reads above")
    return verdict


def approval_gate(cfg: config.Config, estate: str, name: str, log_group: str,
                  *, approved: int = 3, drift: int = 5, restore: int = 1) -> approval.GateVerdict:
    """Walk live/GAUNTLET.md stage 12 on this run's own resources, and grade it.

    One log group the seed already made is the subject, because it is free,
    reversible and readable back out of the account. The walk:

    1. edit the estate's config so ``retention_in_days`` becomes ``approved``,
       and ``plan -out`` that change into a plan file;
    2. move the world out of band - ``aws logs put-retention-policy`` to
       ``drift``, through the AWS CLI, never through choudoufu, so nothing
       choudoufu wrote explains the mismatch;
    3. ``apply <planfile>``, which must refuse: exit 3, the engine's own
       summary, and the moved address named;
    4. put the world back and apply the IDENTICAL file, which must succeed -
       the inverted control, without which a gate that refused everything
       would grade green;
    5. read the retention back out of the account, because "Apply complete!"
       is the tool's own report and this verdict does not take it.

    The applies here are not fenced through :func:`guard.chdf`'s destructive
    path with a confirmation each: this is one composite act the caller has
    already confirmed, and every command still runs inside the estate's own
    working directory under this run's tree, and every AWS write names a log
    group carrying this run's prefix.
    """
    workdir = cfg.workdir(estate)
    main = workdir / "main.tf"
    ui.rule(f"approval gate: plan -out, then apply the file, on {estate}")

    original = main.read_text()
    main.write_text(approval.set_retention(original, name, approved))
    address = f"aws_cloudwatch_log_group.{name}"

    plan_res = guard.chdf(cfg, "plan", "-input=false", "-no-color", f"-out={env.PLANFILE}",
                          cwd=str(workdir), capture=True, check=False, label=f"{estate} plan -out")
    planfile = approval.saved_planfile(plan_res.stdout)
    planfile_bytes = (workdir / planfile).stat().st_size if planfile and (workdir / planfile).exists() else 0

    # The world moves, through the AWS CLI. put-retention-policy is a write on
    # a log group this run created and named, so it goes through the prefix
    # fence like every other write this example makes.
    _set_retention_live(cfg, log_group, drift)
    drifted = guard.chdf(cfg, "apply", "-input=false", "-no-color", planfile or env.PLANFILE,
                         cwd=str(workdir), capture=True, check=False, label=f"{estate} apply (world moved)")
    drifted_text = drifted.stdout + "\n" + drifted.stderr

    # ... and back, so the same file can be applied unchanged.
    _set_retention_live(cfg, log_group, restore)
    restored = guard.chdf(cfg, "apply", "-input=false", "-no-color", planfile or env.PLANFILE,
                          cwd=str(workdir), capture=True, check=False, label=f"{estate} apply (world put back)")

    live = guard.aws(cfg, "logs", "describe-log-groups", "--region", cfg.region,
                     "--log-group-name-prefix", log_group,
                     "--query", "logGroups[0].retentionInDays", "--output", "text", check=False)

    verdict = approval.GateVerdict(
        estate=estate, address=address, planfile=planfile, planfile_bytes=planfile_bytes,
        drifted_exit=drifted.returncode,
        drifted_refused=approval.refused(drifted_text, drifted.returncode),
        drifted_named=approval.refused(drifted_text, drifted.returncode, address=address),
        restored_exit=restored.returncode,
        restored_counts=approval.applied_counts(restored.stdout),
        approved_value=str(approved),
        live_value=live.stdout.strip() if live.ok else "",
    )
    for line in verdict.lines():
        ui.say(line)
    events.verdict(cfg, "plan-approval", verdict, ok=verdict.ok, lines=verdict.lines())
    if verdict.ok:
        ui.ok("the approved plan file refused a world that had moved, and applied one that had not")
    else:
        ui.err("the approval gate did not hold; see the lines above")
    return verdict


def _set_retention_live(cfg: config.Config, log_group: str, days: int) -> None:
    """Move the world out of band. A write, so it names the resource for the
    run-prefix fence; the fence matches on the name, and this run's log groups
    are named /<prefix>/<team>/svc-N."""
    guard.assert_owned_name(cfg, log_group.lstrip("/"))
    guard.aws(cfg, "logs", "put-retention-policy", "--region", cfg.region,
              "--log-group-name", log_group, "--retention-in-days", str(days),
              label="move the world out of band (AWS CLI, not choudoufu)")


def read_references(cfg: config.Config, estates: list[str] | tuple[str, ...]) -> list[carve.Reference]:
    """Every cross-estate edge the given estates declare, read once per estate
    through ``live-check -json``.

    This is the planner's cost input. live-check makes no cloud call - it
    parses the configuration - so it is a read in the fence's sense and needs
    no confirmation; check=False because an estate whose check reports
    findings still reports its references, and a planner that refused to price
    a move because some unrelated instance was refused would be worse than one
    that priced it.
    """
    out: list[carve.Reference] = []
    for estate in estates:
        workdir = cfg.workdir(estate)
        if not workdir.exists():
            continue
        res = guard.chdf(cfg, "live-check", "-json", "-no-color",
                         cwd=str(workdir), capture=True, check=False,
                         label=f"{estate} references")
        try:
            doc = json.loads(res.stdout)
        except json.JSONDecodeError:
            ui.warn(f"{estate}: live-check -json printed no document; its references are not priced")
            continue
        refs = carve.references_from_check(doc, in_estate=estate)
        for r in refs:
            events.reference(cfg, r)
        out.extend(refs)
    return out


def preview_carve(cfg: config.Config, carve_path: str | pathlib.Path) -> list[moveset.MovePreview]:
    """The write-free preview: run ``live-mv -json -dry-run`` for every move in
    the set, in its destination working directory, and emit one preview event
    per move carrying the tag writes it would make and any refusal it raised.
    Nothing is written; -dry-run makes every check and stops. check=False so a
    refusal's nonzero exit is read as the diagnostic it is, not raised.

    -json, not the human report: the document is printed on a refusal as well
    as a success, so a refused preview and a passed one come back through one
    parser, and its ``found_by`` is the engine's own "LIST"/"IDENTITY" rather
    than a sentence this module would have to read back into a value.
    Warnings land on stderr under -json so they cannot corrupt the document;
    both streams are still concatenated here, because a failure with no
    document at all has to stay legible."""
    cs = moveset.load_carve(pathlib.Path(carve_path).read_text())
    ui.rule(f"preview: {len(cs.moves)} move(s), nothing written")
    previews = []
    for m in cs.moves:
        res = guard.chdf(
            cfg, "live-mv", "-json", "-dry-run", "-no-color",
            "-from-estate", m.from_estate, m.address, m.target,
            cwd=str(cfg.workdir(m.to_estate)), capture=True, check=False,
        )
        pv = moveset.parse_preview(res.stdout + ("\n" + res.stderr if res.stderr else ""), move=m)
        if pv.refusal is not None:
            ui.kv(f"preview {m.address}", f"REFUSED: {pv.refusal.summary}", False)
        else:
            writes = "; ".join(f"{t.key} {t.frm}->{t.to}" for t in pv.tag_writes)
            ui.kv(f"preview {m.address}", writes or "no tag write reported", True)
        events.preview(cfg, pv)
        previews.append(pv)
    return previews


def read_carve_cost(cfg: config.Config, carve_path: str | pathlib.Path, *, refresh: bool = False) -> list[measure.PlanMeasurement]:
    """Plan cost per destination estate, one measure event each, so the page
    draws one cost bar per new estate. The default is the workbench's
    headline, the fast plan a carved estate serves from cache (-refresh=false);
    pass refresh=True for the full-refresh number the monolith pays. Reads the
    destination estates straight from the carve set, so the bars match the
    moves the guard graded."""
    cs = moveset.load_carve(pathlib.Path(carve_path).read_text())
    out = []
    for estate in cs.dest_estates:
        extra = () if refresh else ("-refresh=false",)
        out.append(measure.measure_plan(cfg, estate, *extra, refresh=refresh, label=f"{estate} plan"))
    return out


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
    ui.inventory(estate, items)
    return items
