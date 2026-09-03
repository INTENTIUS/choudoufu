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

    A phased workflow for moving ownership of cloud resources between
    estates by retagging, on a real AWS account, with the account's own log
    as the receipt. Eight phases, top to bottom: seed, survey, plan,
    preview, move, verify, receipt, teardown. Each has a button, the words
    the run says while it happens, and the picture as it leaves it. The demo
    seed is a three-team terralith; the adopt form takes your own config.

    Under choudoufu, ownership is two tags on the resource, and a plan reads
    only its own estate. So splitting a terralith stops being a state-file
    project and becomes a metadata edit, and every step of it is an API call
    the account can refuse and does record.

    **Where this starts.** The workbench assumes a terralith: one
    configuration and one state own everything, applied by one principal
    whose permission covers it all. The IAM on the map is the estate's own
    resources (a role, an inline policy and a managed policy per team); the
    operator's permission is the account's, all or nothing. If your org is
    not there, the first step differs. Many states already: adopt each as its
    own estate in the seed phase and begin at plan. Per-team operator
    permissions already: the governance you have is by state file, and the
    move and verify phases show the tag-scoped grant that replaces it.
    Centralizing first is not required; the boundary is a tag, so it goes
    wherever the resources are today.

    This page is a tool and a tutorial at once. The index below tracks the
    eight phases; each phase says what it does, in two registers, and shows
    the payoff it proved, computed from the run's own log.
    """
    )
    return


@app.cell
def _(config, mo):
    mo.accordion({"Paste-and-go prompt: let an assistant walk you through this workbench": mo.md(
        f"""
    ```text
    Clone https://github.com/INTENTIUS/choudoufu and cd examples/live-mv-workbench.
    Confirm uv is installed (uv --version). Run:

      uv run --extra viz marimo run workbench.py

    A browser page opens on a recording in replay mode. Walk me through it
    phase by phase, seed to teardown: for each phase read me what it does
    and its payoff line, and explain what the map or the cost bars changed.
    If I have AWS credentials for account {config.ACCOUNT_ID}, switch the
    page to live and run the phases in order with the buttons, one at a
    time, waiting for each to finish; explain each payoff as it appears,
    and finish with teardown. If the account check refuses, tell me why
    from the reason under its button and stay in replay.
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
            mo.md("A recording plays back: the buttons are off, and each phase below shows the picture as it left it."),
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
    # The run's inventory signature as marimo state. The picture cell sets it
    # only when the resources or phases change, so the planner's rows follow
    # a live seed without the redraw timer rebuilding the table every tick.
    get_inventory, set_inventory = mo.state(None)
    INVENTORY_SEEN = {"sig": None}
    return INVENTORY_SEEN, get_inventory, set_inventory


@app.cell
def _(PHASES, stage):
    # The workflow and the verbs each phase runs on this build's CLI.
    WORKFLOW = stage.WORKFLOW
    VERBS = {p: stage.verbs_for(p, PHASES) for p in WORKFLOW}
    ORDER = [v for p in WORKFLOW for v in VERBS[p]]
    WRITES = stage.WRITES
    return ORDER, VERBS, WORKFLOW, WRITES


@app.cell
def _(INVENTORY_SEEN, ORDER, VERBS, WORKFLOW, bid, live, mo, run_dir, set_inventory, stage, tick, viz):
    tick.value  # redraw on every tick
    _state = viz.load_run(run_dir)
    boundaries = viz.phase_boundaries(run_dir)
    _sig = (run_dir, len(_state.resources), len(_state.phases))
    if _sig != INVENTORY_SEEN["sig"]:
        INVENTORY_SEEN["sig"] = _sig
        set_inventory(_sig)
    _done = [p.name for p in _state.phases if p.status == "done"]
    _active = _state.active_phase
    if not (live or bid):
        # A finished recording ends empty (teardown), which is the wrong first
        # picture; open on the run as it stood before teardown and say so.
        _before = [n for n in _done if n != "teardown"]
        if "teardown" in _done and _before:
            _state = viz.load_run(run_dir, upto=boundaries[_before[-1]])
        _hint = f"Replaying `{run_dir}`: {len(_done)} phases recorded" + (", shown here as it stood after **" + _before[-1] + "**, before teardown emptied the account" if "teardown" in _done and _before else "") + ". Scroll down; each phase shows its picture."
    elif _active:
        _hint = f"Running **{_active.name}** ({stage.phase_of(_active.name, set(ORDER)) or 'a phase'}). Keep talking; the picture follows."
    elif not _done:
        _hint = "Nothing has run yet. Start in **Seed** below with the account check." + (" In bid mode the reads run and the writes stay off." if bid else "")
    else:
        _last = _done[-1]
        _next = ORDER[ORDER.index(_last) + 1] if _last in ORDER and ORDER.index(_last) + 1 < len(ORDER) else None
        _where = stage.phase_of(_next, set(ORDER)) if _next else None
        if _next and _where and VERBS.get("plan") == [] and _last in VERBS["survey"]:
            _hint = f"**{_last}** finished. Next: **Plan** the carve, then preview it, then **run {_next}** under Move."
        else:
            _hint = f"**{_last}** finished. Next: **run {_next}** under {_where.capitalize()}." if _next else f"**{_last}** finished. That was the last phase."
    mo.vstack([mo.hstack([mo.md(_hint), tick], justify="space-between"), mo.Html(viz.render_html(_state, ledger_rows=30, map_width=760))])
    return (boundaries,)


@app.cell
def _(ORDER, boundaries, run_dir, viz):
    def before_state(name):
        """The run as it stood when the nearest recorded verb before this one
        ended; the empty run when none did. A recording may skip verbs."""
        for prev in reversed(ORDER[:ORDER.index(name)]):
            if prev in boundaries:
                return viz.load_run(run_dir, upto=boundaries[prev])
        return viz.load_run(run_dir, upto=0)

    return (before_state,)


@app.cell
def _(VERBS, WORKFLOW, before_state, bid, boundaries, carve, live, mo, run_dir, st, viz):
    # The index: every phase with its standing and, once it ran, its payoff.
    PHASE_TITLES = {"seed": "Seed", "survey": "Survey", "plan": "Plan", "preview": "Preview", "move": "Move", "verify": "Verify", "receipt": "Receipt", "teardown": "Teardown"}
    PHASE_DOES = {"seed": "the account check, then the demo apply or your own config adopted; two tags per resource",
                  "survey": "a plan of the whole estate, counted: the number the split brings down",
                  "plan": "which address goes to which estate, as a table; saved as carve.json",
                  "preview": "every planned move as a dry run, and the map as it would stand",
                  "move": "the boundary moves: one tag write per resource, no state split",
                  "verify": "plan cost after the split, and both sides plan clean at once",
                  "receipt": "this run's tag writes read back from CloudTrail",
                  "teardown": "demo seeds only: every estate destroyed, then the account listed"}

    def verb_status(v):
        if live or bid:
            s = st.status(v)
            return "off in bid mode: this verb writes" if (bid and s == "not started" and v in {"setup", "seed", "decompose", "carve", "move", "teardown"}) else s
        return "recorded" if v in boundaries else "not in this recording"

    def phase_status(p):
        verbs = VERBS[p]
        if p == "plan":
            if not (live or bid):
                return "shown from the recording"
            return "saved" if carve.load(run_dir) is not None else "not saved"
        if p == "preview" and not verbs:
            return "recorded" if viz.load_run(run_dir).previews else "needs the preview verb"
        ss = [verb_status(v) for v in verbs]
        if any(s == "running" for s in ss):
            return "running " + verbs[ss.index("running")]
        if any(s.startswith("failed") for s in ss):
            return "failed: " + verbs[[s.startswith("failed") for s in ss].index(True)]
        if all(s in ("done", "recorded") for s in ss):
            return "done" if (live or bid) else "recorded"
        if any(s in ("done", "recorded") for s in ss):
            return "partly: " + ", ".join(f"{v} {s}" for v, s in zip(verbs, ss))
        return ss[0] if len(set(ss)) == 1 else ", ".join(f"{v} {s}" for v, s in zip(verbs, ss))

    _rows = []
    for _i, _p in enumerate(WORKFLOW, start=1):
        _status = phase_status(_p)
        _mark = "▶" if _status.startswith("running") else ("✗" if _status.startswith("failed") else ("✓" if _status in ("done", "recorded", "saved") else "·"))
        _pay = " ".join(viz.payoff(_v, viz.load_run(run_dir, upto=boundaries[_v]), before_state(_v)) for _v in VERBS[_p] if _v in boundaries).strip()
        _verbs = " ".join(f"<code>{_v}</code>" for _v in VERBS[_p]) or ("<code>carve.json</code>" if _p == "plan" else "")
        _rows.append(f"<tr><td>{_mark}</td><td>{_i}</td><td><b>{PHASE_TITLES[_p]}</b> {_verbs}</td><td>{PHASE_DOES[_p]}</td><td>{_status}</td><td>{_pay}</td></tr>")
    mo.vstack([mo.md("### The workflow"), mo.Html(
        "<style>.beats{border-collapse:collapse;width:100%;font-size:14px}.beats th{text-align:left;font-weight:600;opacity:.6;font-size:12px;letter-spacing:.06em;text-transform:uppercase;padding:6px 10px 6px 0;border-bottom:1px solid color-mix(in srgb, currentColor 20%, transparent)}.beats td{text-align:left;vertical-align:top;padding:7px 10px 7px 0;border-bottom:1px solid color-mix(in srgb, currentColor 12%, transparent)}.beats td:first-child{width:1.4em}.beats td:nth-child(2){opacity:.6}.beats td:nth-child(4){opacity:.8}.beats code{font-size:12px;opacity:.75}</style>"
        "<table class='beats'><thead><tr><th></th><th>#</th><th>phase</th><th>what it does</th><th>status</th><th>payoff</th></tr></thead><tbody>" + "".join(_rows) + "</tbody></table>")])
    return PHASE_DOES, PHASE_TITLES, verb_status


@app.cell
def _(ORDER, PHASES, WRITES, bid, live, mo):
    # The buttons. Globals, created here where nothing depends on the timer,
    # so a redraw never rebuilds them. Each counts its clicks. A button for a
    # verb this build's CLI lacks stays off, and its panel says why.
    def _btn(name, label=None):
        off = (not live and not bid) or (bid and name in WRITES)
        return mo.ui.button(label=label or f"run {name}", value=0, on_click=lambda v: v + 1, disabled=off)

    BTN = {v: _btn(v) for v in ORDER if v not in ("preflight", "setup", "seed")}
    BTN["preflight"] = _btn("preflight", "check the account and binary")
    # The seed panel's buttons and the planner's. Verify reads, adopt and the
    # demo seed write, save writes a file beside the run, preview dry-runs.
    seed_demo_btn = mo.ui.button(label="seed the demo terralith", value=0, on_click=lambda v: v + 1, disabled=not live)
    seed_verify_btn = mo.ui.button(label="verify adoption (reads)", value=0, on_click=lambda v: v + 1, disabled=not (live or bid) or "seed" not in PHASES)
    seed_adopt_btn = mo.ui.button(label="adopt: write the two tags", value=0, on_click=lambda v: v + 1, disabled=not live or "seed" not in PHASES)
    rows_btn = mo.ui.button(label="reload rows from the run", value=0, on_click=lambda v: v + 1)
    save_btn = mo.ui.button(label="save carve.json", value=0, on_click=lambda v: v + 1, disabled=not (live or bid))
    preview_btn = mo.ui.button(label="run preview (dry runs)", value=0, on_click=lambda v: v + 1, disabled=not (live or bid) or "preview" not in PHASES)
    return BTN, preview_btn, rows_btn, save_btn, seed_adopt_btn, seed_demo_btn, seed_verify_btn


@app.cell
def _(BTN, PHASE_DOES, PHASE_TITLES, VERBS, WORKFLOW, WRITES, before_state, bid, boundaries, live, mo, run_dir, st, tips, verb_status, viz):
    # What each verb does in the demo, under its phase. The title is the
    # demo's beat name, kept as the label of the verb's block.
    STORY = {
        "preflight": ("Which account, which binary", "checks, writes nothing",
                      "Nothing has touched the cloud yet. The run names the one account it may use and the one release it was measured against, and refuses to go on if either is wrong."),
        "setup": ("Build the terralith", "applies: creates 21 resources in one estate",
                  "One config, one estate, three teams' worth of IAM and log groups. The account applies it and the map fills in: every taggable resource comes back carrying two tags, which estate owns it and which address it answers to. Nobody wrote a tag by hand."),
        "seed": ("Seed", "applies the demo, or adopts your config",
                 "The resources come to carry the two tags: with the apply for the demo, with live-import for a config that already exists."),
        "slow-plan": ("Measure the villain", "plans the whole monolith, changes nothing, counts requests",
                      "Nothing is built here. A plan of the monolith re-reads everything the estate owns, every time, and the number to watch is how many requests that costs. It is the number the split is meant to bring down."),
        "survey": ("Survey", "plans the whole estate, changes nothing, counts requests",
                   "A plan of the estate as it stands, and the number of requests it costs. It is the number the split is meant to bring down."),
        "decompose": ("Split it, by retag", "applies three team configs: retags, creates nothing",
                      "Three team configs, three estates. Each apply rewrites tofu-estate on the resources it declares, and the map recolours by team. Nothing is re-created and no state file is split: where there was one boundary there are now three, and each cost a tag write."),
        "carve": ("Move the boundary", "retags: team-a's resources move to team-b, one tag write each",
                  "Team-a dissolves into team-b: its resources move with one tag write each, and no state was split. The role's inline policy and attachment carry no tags of their own, so they follow the parent's live tag without a write, and the source estate stops seeing them the instant the parent leaves."),
        "move": ("Move", "runs carve.json: one live-mv per move, one tag write each",
                 "Every move the plan names, made with live-mv: one tag write per resource, children following their parent without a write, and the engine's own refusal on anything the preview would have refused."),
        "fast-plan": ("Measure the payoff", "plans one team's estate from its cache, counts requests",
                      "Nothing is built here either. One team's plan, served from its own cache, against the monolith's number from a minute ago. A steady-state plan costs what its estate costs, not what the account costs."),
        "guard": ("Four reads, one verdict", "reads only: the role's tag, its children, then two plans, the source estate's and the destination's",
                  "Four reads and no writes. First the role's live tag, which must name its new estate, and its inline policy and attachment, which must still be with it. Then two plans, one per estate, each targeted at its own resources, because a carve is only clean when both sides agree at the same moment: the source estate must not want to destroy or rebuild what left, and the destination must not want to create what arrived. Terraform's carve has a window where one side wants to destroy and the other to create; here both plan clean at once, because each estate reads only what carries its tag."),
        "verify": ("Verify", "reads only: each moved resource's tag and children, then a plan per estate",
                   "Every moved resource's live tag must name its new estate and its children must still be with it; then one plan per estate, each targeted at its own resources, and both must be clean at once."),
        "receipt": ("The account's own record", "reads this run's own tag writes back from CloudTrail; writes nothing",
                    "Every ownership move this run made was a tag write, and a tag write is an API call the account logs. This phase reads them back from CloudTrail: who wrote which tag on what, and when, for this run's own resources. Event history lags a minute or so, so it waits. A state edit has no such record; no account logs a file changing."),
        "teardown": ("Nothing left behind", "destroys every estate this run made, then lists the account",
                     "Each estate destroyed through its own configuration, then the account listed rather than trusted: nothing carrying this run's prefix remains. Only the demo seed is torn down; an adopted estate is never destroyed from here."),
    }

    def verb_block(name, button="own", label=True):
        """One verb inside a phase: its demo label and words, the button,
        its standing, what the run said, the ledger rows it made, the log
        (folded, opened on failure), the picture it changed and its payoff.
        ``button=None`` shows the standing without a button, for a verb
        another panel starts (the demo seed)."""
        title, does, words = STORY.get(name, (name, "", ""))
        button = BTN.get(name) if button == "own" else button
        if button is not None and (live or (bid and name not in WRITES)):
            st.click(name, button.value)
        status = verb_status(name)
        if live and st.refused and st.refused[0] == name:
            status += f" · not started: {st.refused[1]} is still running, click again when it ends"
        parts = []
        if label:
            parts.append(mo.md(f"**{title}** <span style='font-family: ui-monospace, Menlo, monospace; font-size: 12px; opacity: .75'>{name} · {does}</span>\n\n{words}"))
        parts.append(mo.hstack(([button] if button is not None else []) + [mo.md(f"`{name}` · {status}")], justify="start", gap=1))
        said = st.notes(name)
        if said:
            parts.append(mo.md("\n".join(f"> {s}" for s in said)))
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
            _before = before_state(name)
            _after = viz.load_run(run_dir, upto=upto)
            picture = viz.render_delta(_after, _before, map_width=760)
            parts.append(mo.Html(picture) if picture else mo.md("*Nothing on the map changed in this step.*"))
            _pay = viz.payoff(name, _after, _before)
            if _pay:
                parts.append(mo.md(f"**Payoff.** {_pay}"))
        return mo.vstack(parts)

    def tip_for(phase, register):
        own = tips.tip(phase, register)
        if own:
            return own
        return "\n\n".join(t for t in (tips.tip(v, register) for v in VERBS[phase]) if t)

    def section(phase, body):
        """One phase's box: number and title, what it does, the tips in two
        registers, then the body (verb blocks, or the phase's own panel).
        Tints alternate so the phases read as sections."""
        index = WORKFLOW.index(phase)
        tint = "color-mix(in srgb, currentColor 5%, transparent)" if index % 2 == 0 else "transparent"
        parts = [mo.md(f"## {index + 1}. {PHASE_TITLES[phase]}\n\n<span style='font-family: ui-monospace, Menlo, monospace; font-size: 12px; opacity: .75'>{phase} · {PHASE_DOES[phase]}</span>")]
        b, e = tip_for(phase, "beginner"), tip_for(phase, "expert")
        if b or e:
            parts.append(mo.accordion({"for a beginner": mo.md(b), "for an OpenTofu hand": mo.md(e)}))
        parts += body if isinstance(body, list) else [body]
        return mo.vstack(parts).style({"border": "1px solid color-mix(in srgb, currentColor 18%, transparent)", "border-left": "4px solid color-mix(in srgb, currentColor 35%, transparent)", "border-radius": "10px", "padding": "18px 22px 20px", "margin": "10px 0 18px", "background": tint})

    return section, verb_block


@app.cell
def _(mo):
    # The adopt form's fields. Globals, made where nothing depends on the
    # timer, so what a user typed survives every redraw.
    seed_config = mo.ui.text(placeholder="/path/to/the/config", label="config directory", full_width=True)
    seed_state = mo.ui.text(placeholder="/path/to/terraform.tfstate, or empty for the config's own backend", label="state file", full_width=True)
    seed_estate = mo.ui.text(placeholder="prod-network", label="estate name")
    return seed_config, seed_estate, seed_state


@app.cell
def _(BTN, PHASES, VERBS, bid, live, mo, section, seed_adopt_btn, seed_config, seed_demo_btn, seed_estate, seed_state, seed_verify_btn, shlex, st, tick, verb_block):
    # Seed: the account check first, then how resources come to carry the two
    # identity tags. The demo applies a config written for choudoufu;
    # adopting runs live-import on a config and state that already exist,
    # verify first, then approve.
    tick.value
    _has_seed = "seed" in PHASES
    _demo_verb = VERBS["seed"][-1]     # seed once the CLI has it, setup until then
    if live:
        st.click(_demo_verb, seed_demo_btn.value, extra=["--demo"] if _has_seed else None)
    _adopt_args = ["--config", seed_config.value, "--estate", seed_estate.value] + (["--state", seed_state.value] if seed_state.value else [])
    _adopt_ready = _has_seed and bool(seed_config.value) and bool(seed_estate.value)
    if _adopt_ready and (live or bid):
        st.click("seed", seed_verify_btn.value, extra=_adopt_args, key="seed:verify")
    if _adopt_ready and live:
        st.click("seed", seed_adopt_btn.value, extra=_adopt_args + ["--approve"], key="seed:adopt")
    _demo = mo.vstack([
        mo.md("**The demo terralith.** One config, one estate, 21 IAM and log-group resources for three teams, applied into the pinned account under this run's prefix. It is the estate the demo words below were written against, and the only seed teardown removes."),
        seed_demo_btn,
    ])
    _cmd = "tlmig seed --run " + st.run_id + " " + (shlex.join(_adopt_args) if _adopt_ready else "--config <dir> --estate <name> [--state <file>]")
    _adopt_parts = [
        mo.md("**Your own estate.** Point at a config and its state. Verify reads every resource the state names and refuses anything it cannot match in the account; it writes nothing. Adopt writes the two tags, `tofu-estate` and `tofu-address`, on each taggable resource and nothing else. The state file is read, never rewritten."),
        seed_config, seed_state,
        mo.hstack([seed_estate, seed_verify_btn, seed_adopt_btn], justify="start", gap=1, align="end"),
    ]
    if not _has_seed:
        _adopt_parts.append(mo.md("*This build's `tlmig` has no `seed` verb yet, so the adopt buttons are off. The verb is the next CLI change; the command it will accept is below, and the demo button runs today's `setup`.*"))
    elif not _adopt_ready:
        _adopt_parts.append(mo.md("*Fill in the config directory and an estate name to enable verify and adopt.*"))
    _adopt_parts.append(mo.md(f"runs `{_cmd}`" + ("" if not _adopt_ready else "; adopt adds `--approve`") + f"\n\nverify · {st.status('seed:verify')} · adopt · {st.status('seed:adopt')}"))
    for _k in ("seed:verify", "seed:adopt"):
        _t = st.tail(_k)
        if _t:
            _adopt_parts.append(mo.accordion({f"{_k} log": mo.ui.code_editor(_t, language="text", disabled=True, max_height=220)}, lazy=False))
    section("seed", [
        verb_block("preflight", BTN["preflight"]),
        mo.md("---"),
        mo.hstack([_demo, mo.vstack(_adopt_parts)], widths=[1, 2], gap=2, align="start"),
        verb_block(_demo_verb, None, label=False),
    ])
    return


@app.cell
def _(VERBS, section, tick, verb_block):
    tick.value
    section("survey", [verb_block(v) for v in VERBS["survey"]])
    return


@app.cell
def _(mo):
    rules_ta = mo.ui.text_area(value="", rows=4, full_width=True, label="rules, one per line: `module|prefix|type|name <value> -> <estate>`; later rules win",
                               placeholder="module data -> team-data\nprefix aws_iam_ -> iam\ntype aws_cloudwatch_log_group -> logs\nname team_a -> team-b")
    demo_move = mo.ui.checkbox(value=True, label="plan the demo's move: team-a into team-b")
    return demo_move, rules_ta


@app.cell
def _(bid, carve, demo_move, get_inventory, live, mo, rows_btn, rules_ta, run_dir, viz):
    # The table rows: every taggable resource the run has seen, with the
    # estate its live tag names and the destination the rules give it. Rows
    # reload when the run's inventory changes (a seed landing, a move made)
    # or on the button, never on the timer, so an edit in the table survives
    # the redraw. Untaggable children are a count: they follow their parent.
    rows_btn.value
    get_inventory()
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
    run_prefix = _state.prefix or ""
    # The demo's own move, offered as a checkbox so the panel is never empty
    # for a demo user: team-a's resources into team-b. Only added when the run
    # is the demo terralith (a monolith estate holding a team_a resource), so
    # on an adopted estate it matches nothing and changes nothing.
    is_demo = any(r.estate and r.estate.endswith("-monolith") and ".team_a" in r.address for r in _state.resources.values())
    if demo_move.value and is_demo and run_prefix:
        rules = rules + [carve.Rule("name", "team_a", f"{run_prefix}-team-b")]
    _children = {}
    for _r in _state.resources.values():
        if _r.parent:
            _children[_r.parent] = _children.get(_r.parent, 0) + 1
    plan_rows = [{"address": _r.address, "type": _r.type, "estate": _r.estate, "to": carve.destination(_r.address, _r.type, rules), "children": _children.get(_r.address, 0)}
                 for _r in sorted((x for x in _state.resources.values() if x.parent is None and not x.gone and x.estate), key=lambda x: (x.estate, x.address))]
    editor = mo.ui.data_editor(plan_rows, editable_columns=["to"], pagination=False) if plan_rows else None
    return editor, is_demo, plan_rows, rule_problems, rules, run_prefix


@app.cell
def _(bid, carve, demo_move, editor, is_demo, live, mo, plan_rows, rows_btn, rule_problems, rules, rules_ta, run_dir, run_prefix, save_btn, section, st):
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
    _parts = [mo.md("The plan decides which resource goes to which estate. It is saved as `carve.json`, and the Move phase reads it. **In the demo you can leave this alone:** the box below is ticked, so the plan already holds the demo's move, team-a into team-b, and the Move phase makes that move whether or not you touch this panel. Untick it, or add rules, to plan your own.")]
    if is_demo:
        _parts.append(demo_move)
    _parts += [
        mo.md("One row per taggable resource. A rule fills the `to` column in bulk; edit any single row by hand. A row whose `to` is `keep`, or its own estate, is not a move, so an all-`keep` plan is a valid, empty plan, not a broken one."),
        rules_ta,
    ]
    if rule_problems:
        _parts.append(mo.md("\n".join(f"- ⚠ {_x}" for _x in rule_problems)))
    if editor is None:
        _parts.append(mo.hstack([mo.md("*No resources in this run yet. The rows fill in when the seed's inventory lands; if it has and they did not, reload.*"), rows_btn], justify="start", gap=1))
    else:
        _parts.append(editor)
    _parts.append(mo.md("\n".join(f"- {_line}" for _line in carve.describe(plan_doc))))
    if plan_rows and not plan_doc["moves"]:
        _hint = "*Every row keeps its estate, so `carve.json` has no moves. That is a fine place to stand: nothing will move. "
        if is_demo:
            _hint += f"To plan the demo's move, tick the box above, or add the rule `name team_a -> {run_prefix}-team-b`. "
        _hint += "You can still go to Move: for the demo, the Move phase makes its own retag; the executor that reads this file is the next CLI change.*"
        _parts.append(mo.md(_hint))
    elif plan_rows:
        _parts.append(mo.md(f"**{len(plan_doc['moves'])} move(s) planned.** Save writes `carve.json`; Preview dry-runs each move; Move makes them. You can go forward."))
    _parts.append(mo.hstack([save_btn, rows_btn, mo.md(_saved)], justify="start", gap=1))
    _parts.append(mo.accordion({"carve.json as it would be saved": mo.ui.code_editor(__import__("json").dumps(plan_doc, indent=2), language="json", disabled=True, max_height=300)}))
    section("plan", _parts)
    return (plan_doc,)


@app.cell
def _(PHASES, bid, live, mo, preview_btn, run_dir, section, st, tick, viz):
    # Preview: every planned move as a dry run, and the map as it would stand.
    tick.value
    _has_preview = "preview" in PHASES
    if _has_preview and (live or bid):
        st.click("preview", preview_btn.value)
    _status = st.status("preview") if (live or bid) else ("recorded" if viz.load_run(run_dir).previews else "not in this recording")
    if not _has_preview and (live or bid):
        _status = "this build's tlmig has no preview verb yet; the next CLI change adds it"
    _state = viz.load_run(run_dir)
    _table = viz.render_previews(_state)
    _parts = [
        mo.md("Each row is one planned move as `live-mv -dry-run` judged it: the tag writes it would make, the untaggable children that follow the parent without a write, and the refusal if a check failed. Below, the map as it stands and the map as it would stand once the passed moves are written."),
        mo.hstack([preview_btn, mo.md(f"`preview` · {_status}")], justify="start", gap=1),
    ]
    _tail = st.tail("preview")
    if _tail:
        _parts.append(mo.accordion({"raw log": mo.ui.code_editor(_tail, language="text", disabled=True, max_height=220)}, lazy=False))
    if _table:
        _parts += [mo.Html(_table), mo.Html(viz.render_projection(_state, map_width=560))]
    else:
        _parts.append(mo.md("*No previews yet. Save a plan, then run preview; in a recording, this shows what the recording holds.*"))
    section("preview", _parts)
    return


@app.cell
def _(VERBS, mo, section, tick, verb_block):
    tick.value
    _note = mo.md("*In the demo the move is two verbs: `decompose` applies three team configs, which retags each team's resources into its own estate; `carve` then moves team-a into team-b with `live-mv`. Once the CLI has the `move` verb, one button runs carve.json.*") if VERBS["move"] != ["move"] else mo.md("")
    section("move", [_note] + [verb_block(v) for v in VERBS["move"]])
    return


@app.cell
def _(VERBS, section, tick, verb_block):
    tick.value
    section("verify", [verb_block(v) for v in VERBS["verify"]])
    return


@app.cell
def _(VERBS, section, tick, verb_block):
    tick.value
    section("receipt", [verb_block(v) for v in VERBS["receipt"]])
    return


@app.cell
def _(VERBS, section, tick, verb_block):
    tick.value
    section("teardown", [verb_block(v) for v in VERBS["teardown"]])
    return


if __name__ == "__main__":
    app.run()
