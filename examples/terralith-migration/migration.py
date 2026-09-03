"""The migration, watched.

A marimo notebook that renders a tlmig run directory as one picture, live and
one phase per cell. It is the visual half of the side-by-side stage: the
presenter drives ``tlmig`` in a terminal, this page follows the run's
``events.jsonl`` and redraws on a timer.

    uv run --extra viz marimo run migration.py      # the browser page for the room
    uv run --extra viz marimo edit migration.py     # the notebook, cell by cell

The picture is tlmig.viz's: an estate-ownership map, every resource coloured
by the estate its live tag names, beside a ledger of every write and the
platform's answer. Pick a run at the top; the synthetic run under
tests/fixtures/sample-run is always there for a rehearsal with no account.

marimo's checker notes that this file imports ``tlmig`` from beside the
package directory; with the project installed editable that is the same
module, so the hint is expected here.
"""

import marimo

__generated_with = "0.16.0"
app = marimo.App(width="full", app_title="The migration, watched")


@app.cell
def _():
    import pathlib

    import marimo as mo

    from tlmig import viz

    return mo, pathlib, viz


@app.cell
def _(mo, pathlib):
    _runs = [p for p in sorted(pathlib.Path("runs").glob("*")) if (p / "events.jsonl").exists()]
    _sample = pathlib.Path("tests/fixtures/sample-run")
    _choices = {p.name: str(p) for p in reversed(_runs)}
    _choices["sample (synthetic)"] = str(_sample)
    run = mo.ui.dropdown(_choices, value=next(iter(_choices)), label="run")
    tick = mo.ui.refresh(default_interval="2s", options=["1s", "2s", "5s", "off"], label="redraw")
    mo.hstack([run, tick], justify="start", gap=2)
    return run, tick


@app.cell
def _(mo, run, tick, viz):
    tick.value  # a dependency, so this cell redraws on every tick
    state = viz.load_run(run.value)
    boundaries = viz.phase_boundaries(run.value)
    live = mo.Html(viz.render_html(state, stack=True, ledger_rows=18, map_width=640))
    live
    return boundaries, live, state


@app.cell
def _(mo):
    mo.md(
        """
    ## One phase per cell

    Each cell below replays the run as it stood when that phase ended, so the
    same page works after the fact as a leave-behind. A phase the run has not
    reached yet says so.
    """
    )
    return


@app.cell
def _(boundaries, mo, run, viz):
    def at_phase(name: str, blurb: str):
        """The picture at the end of one phase, or a placeholder."""
        upto = boundaries.get(name)
        if upto is None:
            return mo.md(f"**{name}** · not reached yet")
        _s = viz.load_run(run.value, upto=upto)
        return mo.vstack([mo.md(f"### {name}\n{blurb}"), mo.Html(viz.render_html(_s, stack=False, ledger_rows=10, map_width=560))])

    return (at_phase,)


@app.cell
def _(at_phase):
    at_phase("setup", "One config, one estate, three teams' worth of IAM and log groups: the monolith before anyone splits it.")
    return


@app.cell
def _(at_phase):
    at_phase("slow-plan", "The villain. A full-refresh plan of the whole monolith re-reads everything the estate owns; the request count is the cost the split removes.")
    return


@app.cell
def _(at_phase):
    at_phase("decompose", "Each team applies its own config. The apply rewrites tofu-estate on the resources it declares: nothing is re-created, and no state file was split.")
    return


@app.cell
def _(at_phase):
    at_phase("fast-plan", "The payoff. One team's plan, served from its cache, against the monolith's number from the slow plan and the emulator's reference.")
    return


@app.cell
def _(at_phase):
    at_phase("carve", "A role moves from team-a to team-b with one tag write. Its inline policy and attachment follow the parent's live tag without a write of their own.")
    return


@app.cell
def _(at_phase):
    at_phase("guard", "Four reads, one verdict: the role's live tag, its kept children, and both estates planning clean. Then the receipt: what the account's own log recorded.")
    return


if __name__ == "__main__":
    app.run()
