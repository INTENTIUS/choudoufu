"""The demo, one function per phase.

Each phase is a self-contained beat: it reuses the run directory, does its
work through the guarded verbs, and wraps itself in an events.phase so every
command and fact in between carries the beat's name for the visualization.
Because each is standalone and reads its inputs from the run directory rather
than from the previous call's memory, the notebook can run them one cell at a
time, and a rehearsal can run them in sequence.

The story: a monolith one estate owns; the slow plan that refreshes all of
it; the decomposition that retags each team's resources into its own estate,
no state surgery; the fast plan of one estate served from cache; team-a
dissolving, its role carved into team-b; and the governance guard proving the
carve left nothing behind.
"""

from __future__ import annotations

from . import config, env, events, fixture, govern, guard, measure, receipt, ui


def preflight(cfg: config.Config) -> None:
    with events.phase(cfg, "preflight", title="check the account and the pinned binary"):
        ui.rule("preflight")
        guard.preflight(cfg)


def setup(cfg: config.Config) -> None:
    with events.phase(cfg, "setup", title="stand up the terralith monolith"):
        env.setup(cfg)
        govern.read_inventory(cfg, cfg.monolith_estate)


def slow_plan(cfg: config.Config) -> None:
    with events.phase(cfg, "slow-plan", title="plan the whole monolith — the villain"):
        ui.rule("the slow plan: the whole monolith, refreshed")
        ui.say(
            "Stock's plan refreshes every resource in the estate. On a real "
            "terralith that is the whole org. Here it is one estate holding "
            "every team — watch the request count."
        )
        measure.measure_plan(cfg, cfg.monolith_estate, refresh=True, label="whole monolith")


def decompose(cfg: config.Config) -> None:
    with events.phase(cfg, "decompose", title="split the monolith into per-team estates by retag"):
        ui.rule("decompose: adoption by tag, no state surgery")
        ui.say(
            "Each team gets its own estate. Its resources move by rewriting one "
            "ownership tag — no state file is edited, no moved block is authored, "
            "and the untaggable children follow their parent role."
        )
        for team in config.TEAMS:
            estate = cfg.estate(team)
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
        # The monolith no longer declares the moved teams, so its own plan stays
        # clean and teardown's monolith destroy proposes nothing.
        env.write_config(cfg, cfg.monolith_estate, fixture.monolith_hcl(cfg, teams=()))
        govern.read_inventory(cfg, cfg.monolith_estate)


def fast_plan(cfg: config.Config) -> None:
    with events.phase(cfg, "fast-plan", title="plan one estate at cache speed"):
        ui.rule("the fast plan: one estate, served from cache")
        estate = cfg.estate(config.SOURCE_TEAM)
        fast = measure.measure_plan(cfg, estate, "-refresh=false", refresh=False, label="one estate")
        slow = _last_measure(cfg, refresh=True)
        if slow is not None:
            measure.contrast(slow, fast)


def carve(cfg: config.Config) -> None:
    with events.phase(cfg, "carve", title="team-a dissolves; its role lives on under team-b"):
        ui.rule("the carve: retag one estate's resources into another")
        src, dst = config.SOURCE_TEAM, config.DEST_TEAM
        src_estate, dst_estate = cfg.estate(src), cfg.estate(dst)
        ui.say(
            f"{src} is dissolving. Its resources have to live on under {dst}, "
            f"with no downtime and no state surgery. Move the blocks into "
            f"{dst}'s configuration, then retag each one across the estate line."
        )
        # The destination's config now declares its own resources and the source's.
        env.write_config(cfg, dst_estate, fixture.team_hcl(cfg, dst, also=(src,)))
        env.init(cfg, dst_estate)
        for addr in fixture.taggable_addresses(src):
            guard.chdf(
                cfg, "live-mv", "-from-estate", src_estate, addr, addr,
                cwd=str(cfg.workdir(dst_estate)), destructive=True, capture=True, check=False,
                label=f"carve {addr} from {src} to {dst}",
            )
        env.apply(cfg, dst_estate)  # record the carried resources under the destination
        # The source dissolves: its config declares nothing now.
        env.write_config(cfg, src_estate, fixture.empty_hcl(cfg, src_estate))
        env.init(cfg, src_estate)
        govern.read_inventory(cfg, dst_estate)
        govern.read_inventory(cfg, src_estate)
        ui.ok(f"{src}'s resources now carry {dst}'s estate; {src} owns nothing")


def guard_phase(cfg: config.Config) -> None:
    with events.phase(cfg, "guard", title="prove the carve left nothing behind"):
        ui.rule("the governance guard: nothing left behind")
        role = fixture.role_name(cfg, config.SOURCE_TEAM)
        verdict = govern.read_carve(
            cfg, role, cfg.estate(config.SOURCE_TEAM), cfg.estate(config.DEST_TEAM),
        )
        for line in verdict.lines():
            (ui.ok if verdict.ok else ui.warn)(line)
        if verdict.ok:
            ui.ok("handover clean: the source leaves nothing behind, the destination owns it all")
        else:
            ui.err("the carve did NOT leave a clean handover — see the lines above")


def receipt_phase(cfg: config.Config) -> None:
    with events.phase(cfg, "receipt", title="the reproducible emulator receipt"):
        ui.rule("the reproducible receipt (emulator)")
        ui.say(
            "The live numbers above are real and vary run to run. These are the "
            "reproducible figures the write-up quotes, from the claim smokes on "
            "the emulator — anyone can rerun them. Shown as a receipt, never "
            "dressed up as the live measurement."
        )
        try:
            rec = receipt.read_receipt(cfg)
            for line in getattr(rec, "lines", lambda: [str(rec)])():
                ui.kv("receipt", line)
        except Exception as exc:  # a missing receipt should not stop the demo
            ui.warn(f"no receipt captured yet ({exc}); run `tlmig receipt` after capturing the smokes")


def teardown(cfg: config.Config) -> None:
    with events.phase(cfg, "teardown", title="destroy the run and verify nothing is left"):
        env.teardown(cfg)
        for estate in [cfg.monolith_estate, *(cfg.estate(t) for t in config.TEAMS)]:
            govern.read_inventory(cfg, estate)


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


# The phase registry the CLI dispatches on. Order is the demo's order.
PHASES = {
    "preflight": preflight,
    "setup": setup,
    "slow-plan": slow_plan,
    "decompose": decompose,
    "fast-plan": fast_plan,
    "carve": carve,
    "guard": guard_phase,
    "receipt": receipt_phase,
    "teardown": teardown,
}
