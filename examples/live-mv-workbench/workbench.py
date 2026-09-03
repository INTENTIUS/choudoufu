"""The live-mv workbench.

A marimo notebook that is the stage for a terralith migration: today the demo
seed, the terralith fixture; next, any estate through the seed panel. The
story runs top to bottom, one phase per cell: each cell carries the beat's
narration, a button that runs that phase, the words the run itself says while
it happens, and the picture as the phase leaves it. A live picture near the
top redraws on a timer, so the map moves while the presenter is still talking.

    uv run --extra viz marimo run workbench.py      # the stage, in a browser
    uv run --extra viz marimo edit workbench.py     # the same, as a notebook

Phases run as subprocesses of the tlmig CLI (tlmig.stage.CLI is the grammar),
so a phase clicked here and a phase typed in a terminal leave the same trail
in runs/<id>/events.jsonl, and the picture cannot tell them apart. The
recorded runs under tests/fixtures replay the story with no account, for a
rehearsal of the words alone.

Two marimo rules shape the cells. A button must be a global created in a
cell that does not depend on the redraw timer, or the timer rebuilds it and a
click is lost. And a phase must start once per click, never on a redraw, so
the buttons are counters and the stage remembers which count it has served.

marimo's checker notes that this file imports ``tlmig`` from beside the
package directory; with the project installed editable that is the same
module, so the hint is expected here.
"""

import marimo

__generated_with = "0.16.0"
app = marimo.App(width="full", app_title="The live-mv workbench")


@app.cell
def _():
    import os
    import pathlib
    import shlex

    import marimo as mo

    from tlmig import carve, config, stage, tips, viz

    # The verbs this build's tlmig knows, from its own --help. The page offers
    # a seed or a preview only when the CLI has it, and says so when it does not.
    PHASES = stage.available_phases()
    return PHASES, carve, config, mo, os, pathlib, shlex, stage, tips, viz


@app.cell
def _(mo):
    mo.md(
        """
    # The live-mv workbench

    A phased workflow for splitting a terralith by retagging, on a real AWS
    account, with the account's own log as the receipt. Today it runs the
    demo seed, a three-team monolith; the seed panel for your own estate is
    next.

    An org's own terralith: one estate, everything in it, one all-or-nothing
    permission boundary. The team wants per-team ownership without the
    state-split engagement. Under choudoufu, ownership is two tags on the
    resource, and a plan reads only its own estate. So the decomposition
    stops being a project and becomes a metadata edit, and every step of it
    is an API call the account can refuse and does record.

    **Where this starts.** The stage assumes a terralith: one configuration
    and one state own everything, applied by one principal whose permission
    covers it all, the way a monolith is run before anyone splits it. The
    IAM you will see on the map is the estate's own resources (a role, an
    inline policy and a managed policy per team); the operator's permission
    is the account's, all or nothing. If your org is not there, the first
    step differs. Many states already: each one is adopted as its own estate
    with `live-import` (the roundtrip claim), and you begin at the carve
    beat. Per-team operator permissions already: the governance you have is
    by state file, and the carve and guard beats show the tag-scoped grant
    that replaces it. Centralizing first is not required; the boundary is a
    tag, so it goes wherever the resources are today.

    This page is an example and a tutorial at once. Read it top to bottom:
    the index below tracks the nine beats, and every beat carries what it
    does, a button that runs it, and the payoff it proved, computed from the
    run's own log.
    """
    )
    return


@app.cell
def _(config, mo):
    mo.accordion({"Paste-and-go prompt: let an assistant walk you through this demo": mo.md(
        f"""
    ```text
    Clone https://github.com/INTENTIUS/choudoufu and cd examples/live-mv-workbench.
    Confirm uv is installed (uv --version). Run:

      uv run --extra viz marimo run workbench.py

    A browser page opens on a recording in replay mode. Walk me through it
    beat by beat: for each of the nine beats, read me its action line and
    its payoff line, and explain what the map or the cost bars changed.
    If I have AWS credentials for account {config.ACCOUNT_ID}, switch the
    page to live and run the beats in order with the buttons, one at a
    time, waiting for each to finish; explain each payoff as it appears,
    and finish with teardown. If preflight refuses, tell me why from the
    reason under its button and stay in replay.
    ```
    """
    )})
    return


@app.cell
def _(config, mo):
    _live = f"live: run each phase for real, against AWS account ...{config.ACCOUNT_ID[-4:]} (writes, then tears down)"
    _bid = "bid: preview only, nothing written; every planned move as a dry run, projected on the map"
    _replay = "replay: watch a recording of a past run; no account needed"
    mode = mo.ui.radio(options={_live: "live", _bid: "bid", _replay: "replay"}, value=_replay, label="")
    pin = mo.ui.dropdown({f"release {config.CHOUDOUFU_VERSION}": "", "local build of this checkout": "local"}, value=f"release {config.CHOUDOUFU_VERSION}", label="pin")
    tick = mo.ui.refresh(default_interval="2s", options=["1s", "2s", "5s", "10m"], label="redraw every")
    return mode, pin, tick


@app.cell
def _(mo, pathlib, stage):
    _existing = [p.name for p in sorted(pathlib.Path("runs").glob("*")) if (p / "events.jsonl").exists()]
    _fixtures = {p.name: str(p) for p in sorted(pathlib.Path("tests/fixtures").glob("*-run")) if (p / "events.jsonl").exists()}
    run_id = mo.ui.text(value=stage.new_run_id(), label="run id")
    _names = {"sample-run": "sample run: a synthetic walk of every phase", "emitter-run": "emitter run: written by the real emitters, cloud faked",
              "preview-run": "preview run: a planned carve as dry runs judged it, two passed and one refused"}
    _choices = {**{_names.get(k, k): v for k, v in _fixtures.items()}, **{f"your run {k}": f"runs/{k}" for k in reversed(_existing)}}
    recording = mo.ui.dropdown(_choices, value=next(iter(_choices), None), label="recording")
    return recording, run_id


@app.cell
def _(mo, os, pin, stage):
    # The binary follows the pin: found for a release, built on demand for a
    # local pin (empty here until the first phase builds it). Editable.
    os.environ["CHOUDOUFU_VERSION"] = pin.value or ""
    if not pin.value:
        os.environ.pop("CHOUDOUFU_VERSION", None)
    binary = mo.ui.text(value=stage.find_binary(), label="choudoufu binary" + (" (built from this checkout on the first phase when empty)" if pin.value else ""), full_width=True)
    return (binary,)


@app.cell
def _(binary, mo, mode, pin, recording, run_id):
    # Each mode shows only its own controls; the knobs a presenter rarely
    # touches are folded away. This cell must not reference the redraw timer:
    # a cell that does re-runs every tick, and everything downstream with it.
    live = mode.value == "live"
    bid = mode.value == "bid"
    if bid:
        _controls = mo.vstack([
            mo.md("Bid mode writes nothing. Preflight and the surveys run as reads; every planned move runs as `live-mv -dry-run` and the page draws the map as it would stand afterwards, beside the map as it stands now. The buttons that write are off. The two numbers a dry run cannot give, the plan cost after the split and the guard's clean plans, are shown from the demo seed and labelled as the demo's."),
            mo.accordion({"run settings (run id, pin, binary)": mo.vstack([mo.hstack([run_id, pin], justify="start", gap=2), binary])}),
        ])
    elif live:
        _controls = mo.vstack([
            mo.md("Press each phase's button below, in story order, and talk over it; one phase at a time. The picture follows the run as it writes its own event log. Live needs credentials for the pinned account; without them, preflight refuses and nothing else runs. To continue an earlier run after a restart, put its id in the run settings: finished phases are read back from its log."),
            mo.accordion({"run settings (run id, pin, binary)": mo.vstack([mo.hstack([run_id, pin], justify="start", gap=2), binary])}),
        ])
    else:
        _controls = mo.vstack([
            recording,
            mo.md("A recording plays back: the buttons are off, and each beat below shows the picture as that phase left it."),
        ])
    mo.vstack([mode, _controls])
    return bid, live


@app.cell
def _(bid, binary, live, pin, recording, run_id, stage):
    # One Stage per run id: the buttons below start phases through it, with
    # the chosen binary as CHOUDOUFU_BIN and the pin as CHOUDOUFU_VERSION.
    run_dir = f"runs/{run_id.value}" if (live or bid) else (recording.value or "")
    # stage.for_run keeps one Stage per run id across cell re-runs, so the
    # phases it started are never forgotten and a click is served once.
    st = stage.for_run(run_id.value, binary=binary.value, env={"CHOUDOUFU_VERSION": pin.value} if pin.value else {})
    return run_dir, st


@app.cell
def _(mo):
    # The adopt form's fields. Globals, made where nothing depends on the
    # timer, so what a user typed survives every redraw.
    seed_config = mo.ui.text(placeholder="/path/to/the/config", label="config directory", full_width=True)
    seed_state = mo.ui.text(placeholder="/path/to/terraform.tfstate, or empty for the config's own backend", label="state file", full_width=True)
    seed_estate = mo.ui.text(placeholder="prod-network", label="estate name")
    return seed_config, seed_estate, seed_state


@app.cell
def _(PHASES, bid, live, mo, seed_adopt_btn, seed_config, seed_demo_btn, seed_estate, seed_state, seed_verify_btn, shlex, st, tick, tips):
    # Seeding: how resources come to carry the two identity tags. The demo
    # applies a config written for choudoufu; adopting runs live-import on a
    # config and state that already exist, verify first, then approve.
    tick.value
    _has_seed = "seed" in PHASES
    _demo_phase = "seed" if _has_seed else "setup"
    if live:
        st.click(_demo_phase, seed_demo_btn.value, extra=["--demo"] if _has_seed else None)
    _adopt_args = ["--config", seed_config.value, "--estate", seed_estate.value] + (["--state", seed_state.value] if seed_state.value else [])
    _adopt_ready = _has_seed and bool(seed_config.value) and bool(seed_estate.value)
    if _adopt_ready and (live or bid):
        st.click("seed", seed_verify_btn.value, extra=_adopt_args, key="seed:verify")
    if _adopt_ready and live:
        st.click("seed", seed_adopt_btn.value, extra=_adopt_args + ["--approve"], key="seed:adopt")
    _demo = mo.vstack([
        mo.md("**The demo terralith.** One config, one estate, 21 IAM and log-group resources for three teams, applied into the pinned account under this run's prefix. It is the estate the beats below were written against, and the only seed teardown removes."),
        mo.hstack([seed_demo_btn, mo.md(f"`{_demo_phase}` · {st.status(_demo_phase)}")], justify="start", gap=1),
    ])
    _cmd = "tlmig seed --run " + st.run_id + " " + (shlex.join(_adopt_args) if _adopt_ready else "--config <dir> --estate <name> [--state <file>]")
    _adopt_parts = [
        mo.md("**Your own estate.** Point at a config and its state. Verify reads every resource the state names and refuses anything it cannot match in the account; it writes nothing. Adopt writes the two tags, `tofu-estate` and `tofu-address`, on each taggable resource and nothing else. The state file is read, never rewritten."),
        seed_config, seed_state,
        mo.hstack([seed_estate, seed_verify_btn, seed_adopt_btn], justify="start", gap=1, align="end"),
    ]
    if not _has_seed:
        _adopt_parts.append(mo.md("*This build's `tlmig` has no `seed` verb yet, so the adopt buttons do nothing. The verb is the next PR; the command it will accept is below, and the demo button runs today's `setup`.*"))
    elif not _adopt_ready:
        _adopt_parts.append(mo.md("*Fill in the config directory and an estate name to enable verify and adopt.*"))
    _adopt_parts.append(mo.md(f"runs `{_cmd}`" + ("" if not _adopt_ready else f"; adopt adds `--approve`") + f"\n\nverify · {st.status('seed:verify')} · adopt · {st.status('seed:adopt')}"))
    for _k in ("seed:verify", "seed:adopt"):
        _t = st.tail(_k)
        if _t:
            _adopt_parts.append(mo.accordion({f"{_k} log": mo.ui.code_editor(_t, language="text", disabled=True, max_height=220)}, lazy=False))
    mo.vstack([
        mo.md("## Seed\n\n<span style='font-family: ui-monospace, Menlo, monospace; font-size: 12px; opacity: .75'>seed · the demo applies; adopt verifies, then writes two tags per resource</span>"),
        mo.accordion({"for a beginner": mo.md(tips.tip("seed", "beginner")), "for an OpenTofu hand": mo.md(tips.tip("seed", "expert"))}),
        mo.hstack([_demo, mo.vstack(_adopt_parts)], widths=[1, 2], gap=2, align="start"),
    ]).style({"border": "1px solid color-mix(in srgb, currentColor 18%, transparent)", "border-left": "4px solid color-mix(in srgb, currentColor 35%, transparent)", "border-radius": "10px", "padding": "18px 22px 20px", "margin": "10px 0 18px", "background": "color-mix(in srgb, currentColor 5%, transparent)"})
    return


@app.cell
def _(bid, live, mo, run_dir, tick, viz):
    tick.value  # redraw on every tick
    _state = viz.load_run(run_dir)
    boundaries = viz.phase_boundaries(run_dir)
    _done = [p.name for p in _state.phases if p.status == "done"]
    _active = _state.active_phase
    _order = list(viz.PHASES)
    if not (live or bid):
        # A finished recording ends empty (teardown), which is the wrong first
        # picture; open on the run as it stood before teardown and say so.
        _before = [n for n in _done if n != "teardown"]
        if "teardown" in _done and _before:
            _state = viz.load_run(run_dir, upto=boundaries[_before[-1]])
        _hint = f"Replaying `{run_dir}`: {len(_done)} phases recorded" + (", shown here as it stood after **" + _before[-1] + "**, before teardown emptied the account" if "teardown" in _done and _before else "") + ". Scroll down; each beat shows its picture."
    elif _active:
        _hint = f"Running **{_active.name}**. Keep talking; the picture follows."
    elif not _done:
        _hint = "Nothing has run yet. Start with **run preflight** below." + (" In bid mode the reads run and the writes stay off." if bid else "")
    else:
        _last = _done[-1]
        _next = _order[_order.index(_last) + 1] if _last in _order and _order.index(_last) + 1 < len(_order) else None
        _hint = f"**{_last}** finished. Next: **run {_next}**." if _next else f"**{_last}** finished. That was the last phase."
    mo.vstack([mo.hstack([mo.md(_hint), tick], justify="space-between"), mo.Html(viz.render_html(_state, ledger_rows=30, map_width=760))])
    return (boundaries,)


@app.cell
def _(boundaries, run_dir, viz):
    ORDER = ["preflight", "setup", "slow-plan", "decompose", "fast-plan", "carve", "guard", "receipt", "teardown"]

    def before_state(name):
        """The run as it stood when the nearest recorded beat before this one
        ended; the empty run when none did. A recording may skip beats."""
        for prev in reversed(ORDER[:ORDER.index(name)]):
            if prev in boundaries:
                return viz.load_run(run_dir, upto=boundaries[prev])
        return viz.load_run(run_dir, upto=0)

    return ORDER, before_state


@app.cell
def _(ORDER, before_state, boundaries, live, mo, run_dir, st, viz):
    # The index: every beat with its standing and, once it ran, its payoff.
    _order = ORDER
    _titles = {"preflight": "Which account, which binary", "setup": "Build the terralith", "slow-plan": "Measure the villain",
               "decompose": "Split it, by retag", "fast-plan": "Measure the payoff", "carve": "Move the boundary",
               "guard": "Four reads, one verdict", "receipt": "The account's own record", "teardown": "Nothing left behind"}
    _rows = []
    for _i, _n in enumerate(_order, start=1):
        _status = st.status(_n) if live else ("recorded" if _n in boundaries else "not in this recording")
        _mark = {"done": "✓", "recorded": "✓", "running": "▶"}.get(_status, "✗" if _status.startswith("failed") else "·")
        _pay = viz.payoff(_n, viz.load_run(run_dir, upto=boundaries[_n]), before_state(_n)) if _n in boundaries else ""
        _rows.append(f"<tr><td>{_mark}</td><td>{_i}</td><td><b>{_titles[_n]}</b> <code>{_n}</code></td><td>{_status}</td><td>{_pay}</td></tr>")
    mo.vstack([mo.md("### The beats"), mo.Html(
        "<style>.beats{border-collapse:collapse;width:100%;font-size:14px}.beats th{text-align:left;font-weight:600;opacity:.6;font-size:12px;letter-spacing:.06em;text-transform:uppercase;padding:6px 10px 6px 0;border-bottom:1px solid color-mix(in srgb, currentColor 20%, transparent)}.beats td{text-align:left;vertical-align:top;padding:7px 10px 7px 0;border-bottom:1px solid color-mix(in srgb, currentColor 12%, transparent)}.beats td:first-child{width:1.4em}.beats td:nth-child(2){opacity:.6}.beats code{font-size:12px;opacity:.75}</style>"
        "<table class='beats'><thead><tr><th></th><th>#</th><th>beat</th><th>status</th><th>payoff</th></tr></thead><tbody>" + "".join(_rows) + "</tbody></table>")])
    return


@app.cell
def _(PHASES, bid, live, mo):
    # The buttons. Globals, created here where nothing depends on the timer,
    # so a redraw never rebuilds them. Each counts its clicks. A button for a
    # verb this build's CLI lacks stays off, and its panel says why.
    WRITES = {"setup", "decompose", "carve", "teardown"}

    def _btn(name):
        off = (not live and not bid) or (bid and name in WRITES)
        return mo.ui.button(label=f"run {name}", value=0, on_click=lambda v: v + 1, disabled=off)

    preflight_btn = _btn("preflight")
    slow_btn = _btn("slow-plan")
    decompose_btn = _btn("decompose")
    fast_btn = _btn("fast-plan")
    carve_btn = _btn("carve")
    guard_btn = _btn("guard")
    receipt_btn = _btn("receipt")
    teardown_btn = _btn("teardown")
    # The seed panel's buttons and the planner's. Verify reads, adopt and the
    # demo seed write, save writes a file beside the run, preview dry-runs.
    seed_demo_btn = mo.ui.button(label="seed the demo terralith", value=0, on_click=lambda v: v + 1, disabled=not live)
    seed_verify_btn = mo.ui.button(label="verify adoption (reads)", value=0, on_click=lambda v: v + 1, disabled=not (live or bid) or "seed" not in PHASES)
    seed_adopt_btn = mo.ui.button(label="adopt: write the two tags", value=0, on_click=lambda v: v + 1, disabled=not live or "seed" not in PHASES)
    rows_btn = mo.ui.button(label="reload rows from the run", value=0, on_click=lambda v: v + 1)
    save_btn = mo.ui.button(label="save carve.json", value=0, on_click=lambda v: v + 1, disabled=not (live or bid))
    preview_btn = mo.ui.button(label="run preview (dry runs)", value=0, on_click=lambda v: v + 1, disabled=not (live or bid) or "preview" not in PHASES)
    return (WRITES, carve_btn, decompose_btn, fast_btn, guard_btn, preflight_btn, preview_btn, receipt_btn, rows_btn, save_btn, seed_adopt_btn, seed_demo_btn, seed_verify_btn, slow_btn, teardown_btn)


@app.cell
def _(WRITES, before_state, bid, boundaries, live, mo, run_dir, st, tips, viz):
    STORY = {
        "preflight": ("Which account, which binary", "checks, writes nothing",
                      "Nothing has touched the cloud yet. The run names the one account it may use and the one release it was measured against, and refuses to go on if either is wrong."),
        "setup": ("Build the terralith", "applies: creates 21 resources in one estate",
                  "One config, one estate, three teams' worth of IAM and log groups. The account applies it and the map fills in: every taggable resource comes back carrying two tags, which estate owns it and which address it answers to. Nobody wrote a tag by hand."),
        "slow-plan": ("Measure the villain", "plans the whole monolith, changes nothing, counts requests",
                      "Nothing is built here. A plan of the monolith re-reads everything the estate owns, every time, and the number to watch is how many requests that costs. It is the number the split is meant to bring down."),
        "decompose": ("Split it, by retag", "applies three team configs: retags, creates nothing",
                      "Three team configs, three estates. Each apply rewrites tofu-estate on the resources it declares, and the map recolours by team. Nothing is re-created and no state file is split: where there was one boundary there are now three, and each cost a tag write."),
        "fast-plan": ("Measure the payoff", "plans one team's estate from its cache, counts requests",
                      "Nothing is built here either. One team's plan, served from its own cache, against the monolith's number from a minute ago. A steady-state plan costs what its estate costs, not what the account costs."),
        "carve": ("Move the boundary", "retags: team-a's resources move to team-b, one tag write each",
                  "Team-a dissolves into team-b: its resources move with one tag write each, and no state was split. The role's inline policy and attachment carry no tags of their own, so they follow the parent's live tag without a write, and the source estate stops seeing them the instant the parent leaves."),
        "guard": ("Four reads, one verdict", "reads only: the role's tag, its children, then two plans, the source estate's and the destination's",
                  "Four reads and no writes. First the role's live tag, which must name its new estate, and its inline policy and attachment, which must still be with it. Then two plans, one per estate, each targeted at its own resources, because a carve is only clean when both sides agree at the same moment: the source estate must not want to destroy or rebuild what left, and the destination must not want to create what arrived. Terraform's carve has a window where one side wants to destroy and the other to create; here both plan clean at once, because each estate reads only what carries its tag."),
        "receipt": ("The account's own record", "reads this run's own tag writes back from CloudTrail; writes nothing",
                    "Every ownership move this run made was a tag write, and a tag write is an API call the account logs. This beat reads them back from CloudTrail: who wrote which tag on what, and when, for this run's own resources. Event history lags a minute or so, so the beat waits for it. A state edit has no such record; no account logs a file changing. The governed refusals, an IAM condition on the tag saying no, are claim 13's smoke on the emulator and its own CloudTrail receipt."),
        "teardown": ("Nothing left behind", "destroys every estate this run made, then lists the account",
                     "Each estate destroyed through its own configuration, then the account listed rather than trusted: nothing carrying this run's prefix remains."),
    }

    def phase(name, button):
        """One phase's cell: narration, the button, its standing, what the
        run said, the log tail while it runs, and the picture as the phase
        left it. Re-runs on every tick; the button itself is a global made
        elsewhere, so it survives the redraw, and the click is served once."""
        title, does, words = STORY[name]
        if button is not None and (live or (bid and name not in WRITES)):
            st.click(name, button.value)
        if live or bid:
            status = st.status(name)
            if bid and name in WRITES:
                status = "off in bid mode: this beat writes"
        else:
            status = "recorded" if name in boundaries else "not in this recording"
        if live and st.refused and st.refused[0] == name:
            status += f" · not started: {st.refused[1]} is still running, click again when it ends"
        parts = [mo.md(f"## {title}\n\n<span style='font-family: ui-monospace, Menlo, monospace; font-size: 12px; opacity: .75'>{name} · {does}</span>\n\n{words}"),
                 mo.accordion({"for a beginner": mo.md(tips.tip(name, "beginner")), "for an OpenTofu hand": mo.md(tips.tip(name, "expert"))}),
                 mo.hstack([button if button is not None else mo.md("*started from the seed panel above*"), mo.md(f"`{name}` · {status}")], justify="start", gap=1)]
        said = st.notes(name)
        if said:
            parts.append(mo.md("\n".join(f"> {s}" for s in said)))
        # The beat's own ledger rows, live while it runs and kept after: what
        # ran, who ran it, what the platform answered. The raw log stays
        # folded away, and is opened for the reader only when the phase failed.
        _now = viz.load_run(run_dir)
        rows = viz.render_phase_ledger(_now, name)
        if rows:
            parts.append(mo.Html(rows))
        tail = st.tail(name)
        if tail:
            parts.append(mo.accordion({"raw log": mo.ui.code_editor(tail, language="text", disabled=True, max_height=260)}, lazy=False, multiple=False) if not status.startswith("failed")
                         else mo.vstack([mo.md("**Why it failed**"), mo.ui.code_editor(tail, language="text", disabled=True, max_height=260)]))
        upto = boundaries.get(name)
        if upto is not None:
            # The picture of what THIS beat changed: the map when ownership
            # moved, the cost bars when a plan was measured, the verdict when
            # the guard ran. The previous beat's end is the "before".
            _before = before_state(name)
            _after = viz.load_run(run_dir, upto=upto)
            picture = viz.render_delta(_after, _before, map_width=760)
            parts.append(mo.Html(picture) if picture else mo.md("*Nothing on the map changed in this beat.*"))
            _pay = viz.payoff(name, _after, _before)
            if _pay:
                parts.append(mo.md(f"**Payoff.** {_pay}"))
        # Each beat in its own box, tints alternating, so the sections read as
        # sections instead of running together. Colours mix from the page's
        # own text colour, so the boxes hold on the light and dark themes.
        index = list(STORY).index(name)
        tint = "color-mix(in srgb, currentColor 5%, transparent)" if index % 2 == 0 else "transparent"
        return mo.vstack(parts).style({
            "border": "1px solid color-mix(in srgb, currentColor 18%, transparent)",
            "border-left": "4px solid color-mix(in srgb, currentColor 35%, transparent)",
            "border-radius": "10px",
            "padding": "18px 22px 20px",
            "margin": "10px 0 18px",
            "background": tint,
        })

    return STORY, phase


@app.cell
def _(phase, preflight_btn):
    phase("preflight", preflight_btn)
    return


@app.cell
def _(phase):
    phase("setup", None)
    return


@app.cell
def _(phase, slow_btn):
    phase("slow-plan", slow_btn)
    return


@app.cell
def _(decompose_btn, phase):
    phase("decompose", decompose_btn)
    return


@app.cell
def _(fast_btn, phase):
    phase("fast-plan", fast_btn)
    return


@app.cell
def _(mo):
    rules_ta = mo.ui.text_area(value="", rows=4, full_width=True, label="rules, one per line: `module|prefix|type|name <value> -> <estate>`; later rules win",
                               placeholder="module data -> team-data\nprefix aws_iam_ -> iam\ntype aws_cloudwatch_log_group -> logs\nname team_a -> team-b")
    return (rules_ta,)


@app.cell
def _(bid, carve, live, mo, rows_btn, rules_ta, run_dir, viz):
    # The table rows: every taggable resource the run has seen, with the
    # estate its live tag names and the destination the rules give it. Rows
    # reload on the button, not the timer, so an edit in the table survives
    # the redraw. Untaggable children are a count: they follow their parent.
    rows_btn.value
    _upto = None
    if not (live or bid):
        # A finished recording ends empty; plan over the run as it stood
        # before teardown, the way the picture above shows it.
        _b = viz.phase_boundaries(run_dir)
        _before = [n for n in _b if n != "teardown"]
        if "teardown" in _b and _before:
            _upto = _b[_before[-1]]
    _state = viz.load_run(run_dir, upto=_upto)
    rules, rule_problems = carve.parse_rules(rules_ta.value)
    _children = {}
    for _r in _state.resources.values():
        if _r.parent:
            _children[_r.parent] = _children.get(_r.parent, 0) + 1
    plan_rows = [{"address": _r.address, "type": _r.type, "estate": _r.estate, "to": carve.destination(_r.address, _r.type, rules), "children": _children.get(_r.address, 0)}
                 for _r in sorted((x for x in _state.resources.values() if x.parent is None and not x.gone and x.estate), key=lambda x: (x.estate, x.address))]
    editor = mo.ui.data_editor(plan_rows, editable_columns=["to"], pagination=False) if plan_rows else None
    return editor, plan_rows, rule_problems, rules


@app.cell
def _(bid, carve, editor, live, mo, plan_rows, rows_btn, rule_problems, rules, rules_ta, run_dir, save_btn, st, tips):
    # The plan as the table stands: rows whose destination differs from
    # their estate are the moves; "keep" or the same estate is not a move.
    _edited = editor.value if editor is not None else []
    _overrides = {f"{plan_rows[_i]['estate']}:{plan_rows[_i]['address']}": str(_row.get("to") or carve.KEEP) for _i, _row in enumerate(_edited) if _i < len(plan_rows)}
    _sources = sorted({_r["estate"] for _r in plan_rows if _overrides.get(f"{_r['estate']}:{_r['address']}", _r["to"]) not in (carve.KEEP, _r["estate"], "")})
    _from = _sources[0] if len(_sources) == 1 else ",".join(_sources)
    plan_doc = carve.plan(_from, [(_r["address"], _r["type"], _r["estate"]) for _r in plan_rows], rules, _overrides)
    _saved = ""
    if (live or bid) and st.once("carve.json", save_btn.value):
        _p = carve.save(run_dir, plan_doc)
        _saved = f"saved `{_p}` with {len(plan_doc['moves'])} moves"
    elif carve.load(run_dir) is not None:
        _on_disk = carve.load(run_dir)
        _saved = f"on disk: `{carve.path(run_dir)}` with {len(_on_disk.get('moves', []))} moves" + ("" if _on_disk.get("moves") == plan_doc["moves"] else " (the table has changed since; save again)")
    elif not (live or bid):
        _saved = "replay: the plan is shown, not saved"
    _parts = [
        mo.md("## Plan the carve\n\n<span style='font-family: ui-monospace, Menlo, monospace; font-size: 12px; opacity: .75'>plan · writes carve.json beside the run; nothing in the account changes</span>\n\nOne row per taggable resource. Rules fill the `to` column; edit any row by hand. A row whose `to` is `keep`, or its own estate, is not a move."),
        mo.accordion({"for a beginner": mo.md(tips.tip("plan", "beginner")), "for an OpenTofu hand": mo.md(tips.tip("plan", "expert"))}),
        rules_ta,
    ]
    if rule_problems:
        _parts.append(mo.md("\n".join(f"- ⚠ {_x}" for _x in rule_problems)))
    if editor is None:
        _parts.append(mo.md("*No resources yet: seed first, or pick a recording. Then reload the rows.*"))
    else:
        _parts.append(editor)
    _parts.append(mo.md("\n".join(f"- {_line}" for _line in carve.describe(plan_doc))))
    _parts.append(mo.hstack([save_btn, rows_btn, mo.md(_saved)], justify="start", gap=1))
    _parts.append(mo.accordion({"carve.json as it would be saved": mo.ui.code_editor(__import__("json").dumps(plan_doc, indent=2), language="json", disabled=True, max_height=300)}))
    mo.vstack(_parts).style({"border": "1px solid color-mix(in srgb, currentColor 18%, transparent)", "border-left": "4px solid color-mix(in srgb, currentColor 35%, transparent)", "border-radius": "10px", "padding": "18px 22px 20px", "margin": "10px 0 18px", "background": "transparent"})
    return (plan_doc,)


@app.cell
def _(PHASES, bid, live, mo, preview_btn, run_dir, st, tick, tips, viz):
    # Preview: every planned move as a dry run, and the map as it would stand.
    tick.value
    _has_preview = "preview" in PHASES
    if _has_preview and (live or bid):
        st.click("preview", preview_btn.value)
    _status = st.status("preview") if (live or bid) else ("recorded" if viz.load_run(run_dir).previews else "not in this recording")
    if not _has_preview and (live or bid):
        _status = "this build's tlmig has no preview verb yet; the next PR adds it"
    _state = viz.load_run(run_dir)
    _table = viz.render_previews(_state)
    _parts = [
        mo.md("## What is about to happen\n\n<span style='font-family: ui-monospace, Menlo, monospace; font-size: 12px; opacity: .75'>preview · every planned move as a dry run; nothing written</span>\n\nEach row is one planned move as `live-mv -dry-run` judged it: the tag writes it would make, the untaggable children that follow the parent without a write, and the refusal if a check failed. Below, the map as it stands and the map as it would stand once the passed moves are written."),
        mo.accordion({"for a beginner": mo.md(tips.tip("preview", "beginner")), "for an OpenTofu hand": mo.md(tips.tip("preview", "expert"))}),
        mo.hstack([preview_btn, mo.md(f"`preview` · {_status}")], justify="start", gap=1),
    ]
    _tail = st.tail("preview")
    if _tail:
        _parts.append(mo.accordion({"raw log": mo.ui.code_editor(_tail, language="text", disabled=True, max_height=220)}, lazy=False))
    if _table:
        _parts += [mo.Html(_table), mo.Html(viz.render_projection(_state, map_width=560))]
    else:
        _parts.append(mo.md("*No previews yet. Save a plan, then run preview; in a recording, this shows what the recording holds.*"))
    mo.vstack(_parts).style({"border": "1px solid color-mix(in srgb, currentColor 18%, transparent)", "border-left": "4px solid color-mix(in srgb, currentColor 35%, transparent)", "border-radius": "10px", "padding": "18px 22px 20px", "margin": "10px 0 18px", "background": "color-mix(in srgb, currentColor 5%, transparent)"})
    return


@app.cell
def _(carve_btn, phase):
    phase("carve", carve_btn)
    return


@app.cell
def _(guard_btn, phase):
    phase("guard", guard_btn)
    return


@app.cell
def _(phase, receipt_btn):
    phase("receipt", receipt_btn)
    return


@app.cell
def _(phase, teardown_btn):
    phase("teardown", teardown_btn)
    return


if __name__ == "__main__":
    app.run()
