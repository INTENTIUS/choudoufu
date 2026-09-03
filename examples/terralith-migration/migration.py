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
    import pathlib

    import marimo as mo

    from tlmig import stage, viz

    return mo, pathlib, stage, viz


@app.cell
def _(mo):
    mo.md(
        """
    # The tag is the boundary

    An org's own terralith: one estate, everything in it, one all-or-nothing
    permission boundary. The team wants per-team ownership without the
    state-split engagement. Under choudoufu, ownership is two tags on the
    resource, and a plan reads only its own estate. So the decomposition
    stops being a project and becomes a metadata edit, and every step of it
    is an API call the account can refuse and does record.

    Below, the story in phases. Each button runs one phase for real; the
    picture follows the run's own event log.
    """
    )
    return


@app.cell
def _(mo, pathlib, stage):
    _existing = [p.name for p in sorted(pathlib.Path("runs").glob("*")) if (p / "events.jsonl").exists()]
    _fixtures = {p.name: str(p) for p in sorted(pathlib.Path("tests/fixtures").glob("*-run")) if (p / "events.jsonl").exists()}
    run_id = mo.ui.text(value=stage.new_run_id(), label="run id")
    binary = mo.ui.text(value=stage.find_binary(), label="choudoufu binary", full_width=True)
    replay = mo.ui.dropdown({"(live run above)": "", **{f"replay {k}": v for k, v in _fixtures.items()}, **{f"replay runs/{k}": f"runs/{k}" for k in reversed(_existing)}}, value="(live run above)", label="or replay")
    # Durations only: marimo's refresh has no "off"; 10m is the quiet setting.
    tick = mo.ui.refresh(default_interval="2s", options=["1s", "2s", "5s", "10m"], label="redraw")
    mo.vstack([mo.hstack([run_id, replay, tick], justify="start", gap=2), binary])
    return binary, replay, run_id, tick


@app.cell
def _(binary, replay, run_id, stage):
    # One Stage per run id: the buttons below start phases through it, with
    # the chosen binary as CHOUDOUFU_BIN. A replay picks a recorded directory
    # and disables the buttons.
    run_dir = replay.value or f"runs/{run_id.value}"
    st = stage.Stage(run_id.value, binary=binary.value)
    return run_dir, st


@app.cell
def _(mo, run_dir, tick, viz):
    tick.value  # redraw on every tick
    _state = viz.load_run(run_dir)
    boundaries = viz.phase_boundaries(run_dir)
    mo.Html(viz.render_html(_state, ledger_rows=30, map_width=760))
    return (boundaries,)


@app.cell
def _(mo, replay):
    # The buttons. Globals, created here where nothing depends on the timer,
    # so a redraw never rebuilds them. Each counts its clicks.
    def _btn(name):
        return mo.ui.button(label=f"run {name}", value=0, on_click=lambda v: v + 1, disabled=bool(replay.value))

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
def _(boundaries, mo, replay, run_dir, st, viz):
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
        if not replay.value:
            st.click(name, button.value)
        status = st.status(name) if not replay.value else ("done" if name in boundaries else "not in this replay")
        parts = [mo.md(f"## {title}\n\n{words}"), mo.hstack([button, mo.md(f"`{name}` · {status}")], justify="start", gap=1)]
        said = st.notes(name)
        if said:
            parts.append(mo.md("\n".join(f"> {s}" for s in said)))
        tail = st.tail(name)
        if tail and status == "running":
            parts.append(mo.ui.code_editor(tail, language="text", disabled=True, max_height=180))
        upto = boundaries.get(name)
        if upto is not None:
            picture = viz.render_html(viz.load_run(run_dir, upto=upto), map_width=760, compact=True)
            if picture:
                parts.append(mo.Html(picture))
        return mo.vstack(parts)

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
