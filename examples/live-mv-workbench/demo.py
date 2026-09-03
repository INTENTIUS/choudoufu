"""live-mv demo — a single-screen, no-scroll stage for the terralith split.

The workbench is a tool; this is the demo. One action panel where the estate
renders, phase breadcrumbs across the top that show what is done and what is
ready next, and a collapsible ledger at the bottom. It runs live against
floci by default (no credentials, nothing to clean up) or real AWS when
credentials are present. No prose on the screen: the picture and the cues
carry it.

This file is the LAYOUT SHELL only — static content, real map, no phase
logic yet, so the look can be judged before the wiring goes in.
"""
import marimo

__generated_with = "0.16.0"
app = marimo.App(width="full", app_title="live-mv demo")


@app.cell
def _():
    import pathlib

    import marimo as mo

    from tlmig import viz

    return mo, pathlib, viz


@app.cell
def _(mo):
    # Kill the page scroll and make the app a fixed, full-viewport column:
    # breadcrumbs on top, the action panel filling the middle, the ledger
    # drawer at the bottom. Only the action panel scrolls, and only if it
    # must. Theme-aware: colours come from tokens defined for both grounds.
    mo.Html("""
    <style>
      :root { --bg: Canvas; --edge: color-mix(in srgb, currentColor 14%, transparent);
              --edge2: color-mix(in srgb, currentColor 22%, transparent);
              --panel: color-mix(in srgb, currentColor 4%, transparent);
              --accent: #2563eb; --good: #3f9e57; --mut: color-mix(in srgb, currentColor 55%, transparent); }
      html, body { height: 100%; margin: 0; overflow: hidden; }
      /* The shell owns the whole viewport, lifted out of marimo's centered,
         width-capped, bottom-padded cell wrappers so nothing collapses it. */
      .lmd { position: fixed; inset: 0; background: var(--bg, Canvas); z-index: 5; }
      .lmd { font-family: ui-sans-serif, system-ui, -apple-system, sans-serif; }
      .lmd-bar { display: flex; gap: 6px; align-items: center; padding: 10px 14px;
                 border-bottom: 1px solid var(--edge2); }
      .lmd-spacer { flex: 1 1 auto; }
      .lmd-reset { border: 1px solid var(--edge2); background: transparent; color: var(--mut);
                   border-radius: 8px; padding: 8px 13px; font-size: 12.5px; white-space: nowrap; cursor: pointer; }
      .lmd-reset:hover { color: currentColor; border-color: currentColor; }
      .lmd-step { display: flex; flex-direction: column; gap: 2px; align-items: flex-start;
                  padding: 7px 13px; border: 1px solid var(--edge); border-radius: 9px;
                  background: var(--panel); white-space: nowrap; cursor: default; min-width: 84px; }
      .lmd-step .n { font-size: 10px; letter-spacing: .09em; text-transform: uppercase; color: var(--mut); }
      .lmd-step .t { font-size: 13px; font-weight: 600; }
      .lmd-step.done { border-color: color-mix(in srgb, var(--good) 45%, transparent); }
      .lmd-step.done .n { color: var(--good); }
      .lmd-step.active { border-color: var(--accent); box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 25%, transparent); background: color-mix(in srgb, var(--accent) 8%, transparent); }
      .lmd-step.active .n { color: var(--accent); }
      .lmd-step.ready { border-color: var(--accent); border-style: dashed; }
      .lmd-step.locked { opacity: .45; }
      .lmd-dot { width: 7px; height: 7px; border-radius: 50%; display: inline-block; margin-right: 5px; background: var(--mut); }
      .lmd-step.done .lmd-dot { background: var(--good); }
      .lmd-step.active .lmd-dot { background: var(--accent); }
      .lmd-step.ready .lmd-dot { background: var(--accent); }
      .lmd-action { flex: 1 1 auto; min-height: 0; overflow: auto; padding: 22px 26px;
                    display: flex; flex-direction: column; gap: 14px; }
      .lmd-cue { display: flex; align-items: center; gap: 12px; }
      .lmd-cue .go { background: var(--accent); color: #fff; border: none; border-radius: 8px;
                     padding: 10px 18px; font-size: 14px; font-weight: 600; }
      .lmd-cue .say { font-size: 15px; color: var(--mut); }
      .lmd-stage { flex: 1 1 auto; min-height: 0; display: flex; align-items: center; justify-content: center; }
      .lmd-stage svg { max-height: 100%; }
      .lmd-ledger { border-top: 1px solid var(--edge2); background: var(--panel); }
      .lmd-ledger > summary { padding: 9px 16px; font-size: 12px; letter-spacing: .05em;
                              text-transform: uppercase; color: var(--mut); cursor: pointer; }
      .lmd-ledger[open] { max-height: 34vh; overflow: auto; }
      .lmd-ledger .body { padding: 4px 16px 14px; }
      .lmd-ledger table { border-collapse: collapse; width: 100%; font-size: 12.5px;
                          font-family: ui-monospace, Menlo, monospace; }
      .lmd-ledger td { padding: 4px 10px 4px 0; border-bottom: 1px solid var(--edge); vertical-align: top; }
    </style>
    """)
    return


@app.cell
def _(mo, pathlib, viz):
    # A recorded run stands in for the live one so the shell shows real shapes.
    RUN = "tests/fixtures/emitter-run"
    _st = viz.load_run(RUN)

    # A fresh run: Seed is the one ready move, everything after it is locked
    # until its turn. (Live, these states come from the run's own events.)
    STEPS = [
        ("seed", "Seed", "ready"), ("survey", "Survey", "locked"),
        ("plan", "Plan", "locked"), ("preview", "Preview", "locked"),
        ("move", "Move", "locked"), ("verify", "Verify", "locked"),
        ("receipt", "Receipt", "locked"), ("teardown", "Teardown", "locked"),
    ]
    _bar = "".join(
        f'<div class="lmd-step {state}"><span class="n"><span class="lmd-dot"></span>{i+1}</span>'
        f'<span class="t">{title}</span></div>'
        for i, (key, title, state) in enumerate(STEPS)
    )

    _fresh = viz.load_run(RUN, upto=0)   # a fresh run: nothing stood up yet
    _map = viz.render_map_svg(_fresh, width=900)
    _ledger = viz.render_ledger(_st, limit=12)

    _shell = f"""
    <div class="lmd" style="display:flex;flex-direction:column">
      <div class="lmd-bar">{_bar}<div class="lmd-spacer"></div><button class="lmd-reset" title="tear down this run and start clean">↺ Reset &amp; clean up</button></div>
      <div class="lmd-action">
        <div class="lmd-cue">
          <button class="go">Run Seed ▸</button>
          <span class="say">Build the terralith: one estate, three teams, on floci. Click to stand it up and watch the map fill.</span>
        </div>
        <div class="lmd-stage">{_map}</div>
      </div>
      <details class="lmd-ledger">
        <summary>ledger</summary>
        <div class="body">{_ledger}</div>
      </details>
    </div>
    """
    mo.Html(_shell)
    return


if __name__ == "__main__":
    app.run()
