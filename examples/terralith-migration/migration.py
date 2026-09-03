"""The migration, told.

A marimo notebook that is the stage for the terralith-migration demo. The
story runs top to bottom, one phase per cell: each cell carries the beat's
narration, a button that runs that phase, the words the run itself says while
it happens, and the picture as the phase leaves it. A live picture near the
top redraws on a timer, so the map moves while the presenter is still talking.

    uv run --extra viz marimo run migration.py      # the stage, in a browser
    uv run --extra viz marimo edit migration.py     # the same, as a notebook

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
app = marimo.App(width="full", app_title="The migration, told")


@app.cell
def _():
    import os
    import pathlib

    import marimo as mo

    from tlmig import config, stage, viz

    return config, mo, os, pathlib, stage, viz


@app.cell
def _(mo):
    mo.md(
        """
    # The tag is the boundary

    This is the terralith-migration example: one monolith estate, split into
    per-team estates by retagging, on a real AWS account, with the account's
    own log as the receipt.

    An org's own terralith: one estate, everything in it, one all-or-nothing
    permission boundary. The team wants per-team ownership without the
    state-split engagement. Under choudoufu, ownership is two tags on the
    resource, and a plan reads only its own estate. So the decomposition
    stops being a project and becomes a metadata edit, and every step of it
    is an API call the account can refuse and does record.
    """
    )
    return


@app.cell
def _(config, mo):
    _live = f"live: run each phase for real, against AWS account ...{config.ACCOUNT_ID[-4:]} (writes, then tears down)"
    _replay = "replay: watch a recording of a past run; no account needed"
    mode = mo.ui.radio(options={_live: "live", _replay: "replay"}, value=_replay, label="")
    pin = mo.ui.dropdown({f"release {config.CHOUDOUFU_VERSION}": "", "local build of this checkout": "local"}, value=f"release {config.CHOUDOUFU_VERSION}", label="pin")
    tick = mo.ui.refresh(default_interval="2s", options=["1s", "2s", "5s", "10m"], label="redraw every")
    return mode, pin, tick


@app.cell
def _(mo, pathlib, stage):
    _existing = [p.name for p in sorted(pathlib.Path("runs").glob("*")) if (p / "events.jsonl").exists()]
    _fixtures = {p.name: str(p) for p in sorted(pathlib.Path("tests/fixtures").glob("*-run")) if (p / "events.jsonl").exists()}
    run_id = mo.ui.text(value=stage.new_run_id(), label="run id")
    _names = {"sample-run": "sample run: a synthetic walk of every phase", "emitter-run": "emitter run: written by the real emitters, cloud faked"}
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
    if live:
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
    return (live,)


@app.cell
def _(binary, live, pin, recording, run_id, stage):
    # One Stage per run id: the buttons below start phases through it, with
    # the chosen binary as CHOUDOUFU_BIN and the pin as CHOUDOUFU_VERSION.
    run_dir = f"runs/{run_id.value}" if live else (recording.value or "")
    # stage.for_run keeps one Stage per run id across cell re-runs, so the
    # phases it started are never forgotten and a click is served once.
    st = stage.for_run(run_id.value, binary=binary.value, env={"CHOUDOUFU_VERSION": pin.value} if pin.value else {})
    return run_dir, st


@app.cell
def _(live, mo, run_dir, tick, viz):
    tick.value  # redraw on every tick
    _state = viz.load_run(run_dir)
    boundaries = viz.phase_boundaries(run_dir)
    _done = [p.name for p in _state.phases if p.status == "done"]
    _active = _state.active_phase
    _order = list(viz.PHASES)
    if not live:
        # A finished recording ends empty (teardown), which is the wrong first
        # picture; open on the run as it stood before teardown and say so.
        _before = [n for n in _done if n != "teardown"]
        if "teardown" in _done and _before:
            _state = viz.load_run(run_dir, upto=boundaries[_before[-1]])
        _hint = f"Replaying `{run_dir}`: {len(_done)} phases recorded" + (", shown here as it stood after **" + _before[-1] + "**, before teardown emptied the account" if "teardown" in _done and _before else "") + ". Scroll down; each beat shows its picture."
    elif _active:
        _hint = f"Running **{_active.name}**. Keep talking; the picture follows."
    elif not _done:
        _hint = "Nothing has run yet. Start with **run preflight** below."
    else:
        _last = _done[-1]
        _next = _order[_order.index(_last) + 1] if _last in _order and _order.index(_last) + 1 < len(_order) else None
        _hint = f"**{_last}** finished. Next: **run {_next}**." if _next else f"**{_last}** finished. That was the last phase."
    mo.vstack([mo.hstack([mo.md(_hint), tick], justify="space-between"), mo.Html(viz.render_html(_state, ledger_rows=30, map_width=760))])
    return (boundaries,)


@app.cell
def _(live, mo):
    # The buttons. Globals, created here where nothing depends on the timer,
    # so a redraw never rebuilds them. Each counts its clicks.
    def _btn(name):
        return mo.ui.button(label=f"run {name}", value=0, on_click=lambda v: v + 1, disabled=not live)

    preflight_btn = _btn("preflight")
    setup_btn = _btn("setup")
    slow_btn = _btn("slow-plan")
    decompose_btn = _btn("decompose")
    fast_btn = _btn("fast-plan")
    carve_btn = _btn("carve")
    guard_btn = _btn("guard")
    receipt_btn = _btn("receipt")
    teardown_btn = _btn("teardown")
    return (carve_btn, decompose_btn, fast_btn, guard_btn, preflight_btn, receipt_btn, setup_btn, slow_btn, teardown_btn)


@app.cell
def _(boundaries, live, mo, run_dir, st, viz):
    STORY = {
        "preflight": ("Which account, which binary",
                      "Nothing has touched the cloud yet. The run names the one account it may use and the one release it was measured against, and refuses to go on if either is wrong."),
        "setup": ("The terralith",
                  "One config, one estate, three teams' worth of IAM and log groups. The account stands it up, and every taggable resource comes back carrying two tags: which estate owns it, and which address it answers to. Nobody wrote a tag by hand."),
        "slow-plan": ("The villain",
                      "A plan of the whole monolith. It re-reads everything the estate owns, every time, and the request count is the number the split is meant to bring down."),
        "decompose": ("The split, by retag",
                      "Three team configs, three estates. Each apply rewrites tofu-estate on the resources it declares. Nothing is re-created and no state file is split: where there was one boundary there are now three, and each cost a tag write."),
        "fast-plan": ("The payoff",
                      "One team's plan, served from its own cache, against the monolith's number from a minute ago. A steady-state plan costs what its estate costs, not what the account costs."),
        "carve": ("The boundary moves",
                  "Team-a dissolves into team-b: its resources move with one tag write each, and no state was split. The role's inline policy and attachment carry no tags of their own, so they follow the parent's live tag without a write, and the source estate stops seeing them the instant the parent leaves."),
        "guard": ("Four reads, one verdict",
                  "The role's live tag, its kept children, and both estates planning clean. A state mv has no such moment; nothing evaluates it and nothing records it. Here the account did both."),
        "receipt": ("The account's own record",
                    "The same carve, run on the real account: every governed write in CloudTrail within a minute, the refusals as Client.UnauthorizedOperation against the session that was refused. No state file could have produced that record."),
        "teardown": ("Nothing left behind",
                     "Each estate destroyed through its own configuration, then the account listed rather than trusted: nothing carrying this run's prefix remains."),
    }

    def phase(name, button):
        """One phase's cell: narration, the button, its standing, what the
        run said, the log tail while it runs, and the picture as the phase
        left it. Re-runs on every tick; the button itself is a global made
        elsewhere, so it survives the redraw, and the click is served once."""
        title, words = STORY[name]
        if live:
            st.click(name, button.value)
        status = st.status(name) if live else ("recorded" if name in boundaries else "not in this recording")
        if live and st.refused and st.refused[0] == name:
            status += f" · not started: {st.refused[1]} is still running, click again when it ends"
        parts = [mo.md(f"## {title}\n\n{words}"), mo.hstack([button, mo.md(f"`{name}` · {status}")], justify="start", gap=1)]
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
            picture = viz.render_html(viz.load_run(run_dir, upto=upto), map_width=760, compact=True)
            if picture:
                parts.append(mo.Html(picture))
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
def _(phase, setup_btn):
    phase("setup", setup_btn)
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
