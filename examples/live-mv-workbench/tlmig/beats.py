"""The workflow, one function per phase.

The verbs are the workbench's: seed, survey, preview, move, verify, plus
preflight, receipt and teardown. The terralith demo is these verbs run over a
seeded monolith - seed stands it up, survey plans the whole thing, preview
dry-runs the carve plan write-free, move retags the resources into their
estates, verify plans one estate fast and proves the handover clean.

Each phase is a self-contained beat: it reuses the run directory, works through
the guarded verbs, and wraps itself in one events.phase so every command and
fact in between carries the beat's name for the visualization. The phase
bodies are factored into `_`-prefixed helpers so a compound verb (move,
verify) wraps them once, and the pre-workbench names (setup, slow-plan,
decompose, carve, fast-plan, guard) stay callable as aliases for one release
so recorded runs and the smoke keep working.
"""

from __future__ import annotations

from . import carve, config, env, events, fixture, govern, guard, measure, receipt, ui


# --------------------------------------------------------------------------
# preflight / receipt / teardown - unchanged verbs
# --------------------------------------------------------------------------

def preflight(cfg: config.Config) -> None:
    with events.phase(cfg, "preflight", title="check the account and the pinned binary"):
        ui.rule("preflight")
        guard.preflight(cfg)


# --------------------------------------------------------------------------
# seed - stand up (or adopt) the estates the plan will move between
# --------------------------------------------------------------------------

def _demo_carve_doc(cfg: config.Config) -> dict:
    """The demo's carve plan: every team's taggable resource leaves the
    monolith for that team's estate. It is the decompose move set, and it is
    previewable straight after seed because the resources are all still in the
    monolith and each team's destination config is written by seed."""
    moves, estates = [], []
    for team in config.TEAMS:
        dest = cfg.estate(team)
        for addr in fixture.taggable_addresses(team):
            moves.append({"address": addr, "from": cfg.monolith_estate, "to": dest})
        if dest not in estates:
            estates.append(dest)
    return {"from": cfg.monolith_estate, "estates": estates, "moves": moves, "rules": []}


def seed(cfg: config.Config, *, demo: bool = False, config_dir: str | None = None,
         estate: str | None = None, state: str | None = None, approve: bool = False) -> None:
    """Seed the run. With --demo (or no --config), stand up the built-in
    terralith. With --config/--estate, adopt an existing estate: verify-only,
    or with --approve stamp its markers (see :mod:`adopt`). An adopted estate
    is the user's own; teardown never touches it."""
    if config_dir:
        with events.phase(cfg, "seed", title=f"adopt {estate} onto live markers"):
            if not estate:
                raise guard.GuardError("seed --config requires --estate")
            if state:
                from . import adopt
                adopt.seed_adopt(cfg, config_dir, estate, state, approve)
            else:
                # No state given: write the config as a live estate and plan it,
                # so the report is the config's own view against the live system.
                import pathlib
                workdir = cfg.workdir(estate)
                workdir.mkdir(parents=True, exist_ok=True)
                from . import adopt
                for f in sorted(pathlib.Path(config_dir).glob("*.tf")):
                    text = f.read_text()
                    (workdir / f.name).write_text(adopt.to_live_hcl(text, estate) if "terraform" in text else text)
                env.init(cfg, estate)
                env.plan(cfg, estate, capture=False)
            govern.read_inventory(cfg, estate)
        return
    with events.phase(cfg, "seed", title="stand up the terralith monolith (demo seed)"):
        env.setup(cfg)
        for team in config.TEAMS:
            est = cfg.estate(team)
            env.write_config(cfg, est, fixture.team_hcl(cfg, team))
            env.init(cfg, est)
        carve.save(cfg.run_dir, _demo_carve_doc(cfg))
        govern.read_inventory(cfg, cfg.monolith_estate)
        ui.ok(f"demo seed up; carve plan at {carve.path(cfg.run_dir)}")


# --------------------------------------------------------------------------
# survey - plan the whole estate as it is
# --------------------------------------------------------------------------

def _survey(cfg: config.Config) -> None:
    ui.rule("survey: the whole monolith, refreshed")
    ui.say(
        "Stock's plan refreshes every resource in the estate. On a real "
        "terralith that is the whole org. Here it is one estate holding every "
        "team - watch the request count."
    )
    measure.measure_plan(cfg, cfg.monolith_estate, refresh=True, label="whole monolith")


def survey(cfg: config.Config) -> None:
    with events.phase(cfg, "survey", title="plan the whole monolith - the villain"):
        _survey(cfg)


# --------------------------------------------------------------------------
# preview - dry-run the carve plan, write-free
# --------------------------------------------------------------------------

def preview(cfg: config.Config) -> None:
    """The write-free preview: dry-run every move in the carve plan, emitting
    one preview event per move with the tag writes it would make or the refusal
    it raised. Nothing is written."""
    with events.phase(cfg, "preview", title="dry-run the carve plan - nothing written"):
        ui.say(
            "Every move is checked and reported without a single write: "
            "live-mv -dry-run finds the resource, makes every check, and stops "
            "before touching a tag. This is what a bid can show a client."
        )
        govern.preview_carve(cfg, carve.path(cfg.run_dir))


# --------------------------------------------------------------------------
# move - retag the resources into their estates
# --------------------------------------------------------------------------

def _decompose(cfg: config.Config) -> None:
    ui.rule("decompose: adoption by tag, no state surgery")
    ui.say(
        "Each team gets its own estate. Its resources move by rewriting one "
        "ownership tag - no state file is edited, no moved block is authored, "
        "and the untaggable children follow their parent role."
    )
    for team in config.TEAMS:
        estate = cfg.estate(team)
        # Idempotent: seed writes and inits these, but decompose stays
        # self-sufficient so its alias runs standalone too.
        env.write_config(cfg, estate, fixture.team_hcl(cfg, team))
        env.init(cfg, estate)
        for addr in fixture.taggable_addresses(team):
            guard.chdf(
                cfg, "live-mv", "-from-estate", cfg.monolith_estate, addr, addr,
                cwd=str(cfg.workdir(estate)), destructive=True, capture=True, check=False,
                label=f"retag {addr} into {team}",
            )
        env.apply(cfg, estate)  # the recording apply: binds the adopted resources into this estate's store
        govern.read_inventory(cfg, estate)
        ui.ok(f"{team} decomposed into {estate}")
    env.write_config(cfg, cfg.monolith_estate, fixture.monolith_hcl(cfg, teams=()))
    govern.read_inventory(cfg, cfg.monolith_estate)


def _carve(cfg: config.Config) -> None:
    ui.rule("carve: retag one estate's resources into another")
    src, dst = config.SOURCE_TEAM, config.DEST_TEAM
    src_estate, dst_estate = cfg.estate(src), cfg.estate(dst)
    ui.say(
        f"{src} is dissolving. Its resources have to live on under {dst}, with "
        f"no downtime and no state surgery. Move the blocks into {dst}'s "
        f"configuration, then retag each one across the estate line."
    )
    env.write_config(cfg, dst_estate, fixture.team_hcl(cfg, dst, also=(src,)))
    env.init(cfg, dst_estate)
    for addr in fixture.taggable_addresses(src):
        guard.chdf(
            cfg, "live-mv", "-from-estate", src_estate, addr, addr,
            cwd=str(cfg.workdir(dst_estate)), destructive=True, capture=True, check=False,
            label=f"carve {addr} from {src} to {dst}",
        )
    env.apply(cfg, dst_estate)
    env.write_config(cfg, src_estate, fixture.empty_hcl(cfg, src_estate))
    env.init(cfg, src_estate)
    govern.read_inventory(cfg, dst_estate)
    govern.read_inventory(cfg, src_estate)
    ui.ok(f"{src}'s resources now carry {dst}'s estate; {src} owns nothing")


def move(cfg: config.Config) -> None:
    """Execute the migration: decompose the monolith into per-team estates by
    retag, then carve team-a into team-b. The general carve.json executor lands
    in a later change; this runs the demo's move set."""
    with events.phase(cfg, "move", title="retag the resources into their estates"):
        _decompose(cfg)
        _carve(cfg)


# --------------------------------------------------------------------------
# verify - plan one estate fast, prove the handover clean
# --------------------------------------------------------------------------

def _fast_plan(cfg: config.Config) -> None:
    ui.rule("the fast plan: one estate, served from cache")
    estate = cfg.estate(config.SOURCE_TEAM)
    # Let the tagging index catch up with the retags first, so the sweep
    # vouches from the index instead of reading live and the number reflects
    # the cache, not eventual consistency.
    env.settle(cfg, estate)
    fast = measure.measure_plan(cfg, estate, "-refresh=false", refresh=False, label="one estate")
    slow = _last_measure(cfg, refresh=True)
    if slow is not None:
        measure.contrast(slow, fast)


def _guard(cfg: config.Config) -> None:
    ui.rule("the governance guard: nothing left behind")
    role = fixture.role_name(cfg, config.SOURCE_TEAM)
    verdict = govern.read_carve(cfg, role, cfg.estate(config.SOURCE_TEAM), cfg.estate(config.DEST_TEAM))
    for line in verdict.lines():
        (ui.ok if verdict.ok else ui.warn)(line)
    if verdict.ok:
        ui.ok("handover clean: the source leaves nothing behind, the destination owns it all")
    else:
        ui.err("the carve did NOT leave a clean handover - see the lines above")


def verify(cfg: config.Config) -> None:
    """Prove the moves: one estate plans at cache speed, and the carve left
    nothing behind."""
    with events.phase(cfg, "verify", title="plan one estate fast, prove the handover clean"):
        _fast_plan(cfg)
        _guard(cfg)


# --------------------------------------------------------------------------
# receipt - the account's own record of this run's writes (unchanged)
# --------------------------------------------------------------------------

def receipt_phase(cfg: config.Config) -> None:
    with events.phase(cfg, "receipt", title="the account's own record of this run's writes"):
        ui.rule("the account's own record")
        text = ("Every ownership move this run made was a tag write, and a tag write is an API call the "
                "account logs. This reads them back from CloudTrail: who wrote which tag on what, and when. "
                "A state edit has no such record; no account logs a file changing.")
        ui.say(text)
        events.note(cfg, text)
        ui.cmd("aws cloudtrail lookup-events --lookup-attributes AttributeKey=EventName,AttributeValue=TagRole ...   # since this run began, filtered to its prefix")
        ct = receipt.lookup_run_cloudtrail(cfg)
        retags = [e for e in ct.events if "tofu-estate" in e.tags]
        if ct.events:
            for e in ct.events:
                ui.kv(e.time, f"{e.role}  {e.resource}  {', '.join(f'{k}={v}' for k, v in e.tags.items())}" + (f"  {e.error}" if e.error else ""), not e.error)
            ui.ok(f"{len(ct.events)} of this run's writes are in the account's log, {len(retags)} of them ownership moves")
        else:
            warn = ("CloudTrail event history has not surfaced this run's writes yet (it can lag a few minutes); "
                    "run `tlmig receipt` again in a minute and the record appears")
            ui.warn(warn)
            events.note(cfg, warn)
        carve_receipt = None
        saved = cfg.run_dir / "receipts" / "carve-by-retag.log"
        if saved.exists():
            try:
                carve_receipt = receipt.parse_carve(saved.read_text())
                ui.kv("emulator receipt", f"carve-by-retag {'PASS' if carve_receipt.passed else 'no PASS line'}: monolith {carve_receipt.monolith_plan_requests} requests, carved estate {carve_receipt.carved_plan_requests}", carve_receipt.passed)
            except Exception as exc:  # noqa: BLE001 - a bad capture must not stop the demo
                ui.warn(f"saved emulator receipt unreadable: {exc}")
        events.receipt(cfg, {"carve": carve_receipt, "cloudtrail": ct, "source": f"cloudtrail lookup-events since {receipt.run_started(cfg).isoformat()}"})


def teardown(cfg: config.Config) -> None:
    with events.phase(cfg, "teardown", title="destroy the run and verify nothing is left"):
        env.teardown(cfg)
        for estate in [cfg.monolith_estate, *(cfg.estate(t) for t in config.TEAMS)]:
            govern.read_inventory(cfg, estate)


# --------------------------------------------------------------------------
# Aliases: the pre-workbench verb names, kept working for one release
# --------------------------------------------------------------------------

def setup(cfg: config.Config) -> None:
    with events.phase(cfg, "setup", title="stand up the terralith monolith"):
        env.setup(cfg)
        govern.read_inventory(cfg, cfg.monolith_estate)


def slow_plan(cfg: config.Config) -> None:
    with events.phase(cfg, "slow-plan", title="plan the whole monolith - the villain"):
        _survey(cfg)


def decompose(cfg: config.Config) -> None:
    with events.phase(cfg, "decompose", title="split the monolith into per-team estates by retag"):
        _decompose(cfg)


def carve_phase(cfg: config.Config) -> None:
    with events.phase(cfg, "carve", title="team-a dissolves; its resources live on under team-b"):
        _carve(cfg)


def fast_plan(cfg: config.Config) -> None:
    with events.phase(cfg, "fast-plan", title="plan one estate at cache speed"):
        _fast_plan(cfg)


def guard_phase(cfg: config.Config) -> None:
    with events.phase(cfg, "guard", title="prove the carve left nothing behind"):
        _guard(cfg)


def _last_measure(cfg: config.Config, *, refresh: bool):
    """The most recent measurement of a given kind, read back from the event
    feed so a later phase in a separate process can pair the fast plan with the
    slow one."""
    found = None
    for e in events.read(cfg):
        if e.get("kind") == "measure" and e.get("refresh") is refresh:
            found = e
    if found is None:
        return None
    return measure.PlanMeasurement(
        estate=found["estate"], refresh=found["refresh"], requests=found["requests"],
        cache_hits=found.get("cache_hits") or 0, seconds=found.get("seconds") or 0.0,
    )


# The phase registry the CLI dispatches on. Workflow verbs first, in order;
# the pre-workbench names follow as aliases.
PHASES = {
    "preflight": preflight,
    "seed": seed,
    "survey": survey,
    "preview": preview,
    "move": move,
    "verify": verify,
    "receipt": receipt_phase,
    "teardown": teardown,
    # aliases (one release): recorded runs and the smoke keep working
    "setup": setup,
    "slow-plan": slow_plan,
    "decompose": decompose,
    "carve": carve_phase,
    "fast-plan": fast_plan,
    "guard": guard_phase,
}
