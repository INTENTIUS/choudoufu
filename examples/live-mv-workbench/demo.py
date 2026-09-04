"""live-mv demo — a single-screen stage for the terralith split.

Phase breadcrumbs across the top show what is done and what is ready next.
One action panel where the estate renders. A collapsible ledger at the
bottom. It runs live against floci (no credentials, nothing to clean up),
the compose stack points it there; with real AWS credentials it runs there
instead. No prose on the screen: the picture and the cues carry it.
"""
import marimo

__generated_with = "0.16.0"
app = marimo.App(width="full", app_title="live-mv demo")


@app.cell
def _():
    import os

    import marimo as mo

    from tlmig import stage, viz

    # The run state lives on the imported module, minted once. marimo can
    # re-execute a cell body, which would re-mint a local; a module attribute
    # survives because the module is cached in sys.modules. Reset mutates it.
    if not hasattr(stage, "_demo_run"):
        stage._demo_run = {"id": os.environ.get("DEMO_RUN") or stage.new_run_id(), "reset_seen": 0}

    return mo, os, stage, viz


@app.cell
def _(mo):
    # Component styles. The fixed, full-viewport frame is applied by .style()
    # on the shell's own container (below), lifted out of marimo's cell wrappers.
    mo.Html("""
    <style>
      :root { --bg: Canvas; --edge: color-mix(in srgb, currentColor 14%, transparent);
              --edge2: color-mix(in srgb, currentColor 22%, transparent);
              --panel: color-mix(in srgb, currentColor 4%, transparent);
              --accent: #2563eb; --good: #3f9e57; --bad: #c0392b;
              --mut: color-mix(in srgb, currentColor 55%, transparent); }
      html, body { height: 100%; margin: 0; overflow: hidden; }
      .lmd-bar { display: flex; gap: 6px; align-items: center; padding: 10px 14px;
                 border-bottom: 1px solid var(--edge2); }
      .lmd-step { display: flex; flex-direction: column; gap: 2px; align-items: flex-start;
                  padding: 7px 13px; border: 1px solid var(--edge); border-radius: 9px;
                  background: var(--panel); white-space: nowrap; min-width: 84px; }
      .lmd-step .n { font-size: 10px; letter-spacing: .09em; text-transform: uppercase; color: var(--mut); }
      .lmd-step .t { font-size: 13px; font-weight: 600; }
      .lmd-step.done { border-color: color-mix(in srgb, var(--good) 45%, transparent); }
      .lmd-step.done .n { color: var(--good); }
      .lmd-step.active { border-color: var(--accent); box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 25%, transparent);
                         background: color-mix(in srgb, var(--accent) 8%, transparent);
                         animation: lmd-pulse 1.6s ease-in-out infinite; }
      .lmd-step.active .n { color: var(--accent); }
      @keyframes lmd-pulse {
        0%, 100% { box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 25%, transparent); }
        50% { box-shadow: 0 0 0 5px color-mix(in srgb, var(--accent) 10%, transparent); }
      }
      .lmd-step.ready { border-color: var(--accent); border-style: dashed; }
      .lmd-step.ready .n { color: var(--accent); }
      .lmd-step.failed { border-color: color-mix(in srgb, var(--bad) 55%, transparent); }
      .lmd-step.failed .n { color: var(--bad); }
      .lmd-step.locked { opacity: .42; }
      .lmd-dot { width: 7px; height: 7px; border-radius: 50%; display: inline-block; margin-right: 5px; background: var(--mut); }
      .lmd-step.done .lmd-dot { background: var(--good); }
      .lmd-step.active .lmd-dot, .lmd-step.ready .lmd-dot { background: var(--accent); }
      .lmd-step.failed .lmd-dot { background: var(--bad); }
      .lmd-spacer { flex: 1 1 auto; }
      .lmd-say { font-size: 18px; font-weight: 500; color: currentColor; }
      .lmd-stage { flex: 1 1 auto; min-height: 0; display: flex; align-items: center; justify-content: center; overflow: auto; }
      .lmd-stage svg { max-height: 100%; }
      .lmd-note { font-size: 13px; color: var(--mut); }
      .lmd-pay { font-size: 14px; }
      .lmd-elapsed { font-variant-numeric: tabular-nums; opacity: .75; }
      .lmd-busy { display: inline-flex; align-items: center; gap: 6px; font-size: 12px;
                  letter-spacing: .05em; text-transform: uppercase; color: var(--accent); }
      .lmd-busy .lmd-spin { width: 9px; height: 9px; border-radius: 50%;
                             border: 2px solid color-mix(in srgb, var(--accent) 30%, transparent);
                             border-top-color: var(--accent); animation: lmd-spin .8s linear infinite; }
      @keyframes lmd-spin { to { transform: rotate(360deg); } }
      .lmd-planned { display: inline-block; font-size: 11px; letter-spacing: .06em; text-transform: uppercase;
                     color: var(--accent); border: 1px dashed var(--accent); border-radius: 6px; padding: 2px 8px;
                     margin-bottom: 6px; }
    </style>
    """)
    return


@app.cell
def _():
    # The eight phases, in the order they actually complete. Plan is not a
    # CLI verb -- it's done the instant carve.json exists, which the demo
    # seed writes as part of standing up, before Survey ever runs. Listing
    # Plan after Survey (narrative order) let the breadcrumb show a later
    # step done while an earlier one was still unstarted, which read as the
    # page skipping a stage; this order is the true completion order instead.
    PHASES = ["seed", "plan", "survey", "preview", "move", "verify", "receipt", "teardown"]
    TITLE = {"seed": "Seed", "survey": "Survey", "plan": "Plan", "preview": "Preview",
             "move": "Move", "verify": "Verify", "receipt": "Receipt", "teardown": "Teardown"}
    # (verb, extra args). None verb = no run, resolved from the run's files.
    VERB = {"seed": ("seed", ["--demo"]), "survey": ("survey", []), "plan": (None, []),
            "preview": ("preview", []), "move": ("move", []), "verify": ("verify", []),
            "receipt": ("receipt", []), "teardown": ("teardown", [])}
    CUE = {
        "seed": "The terralith: one estate, three teams, on floci. The map appears once every resource is up.",
        "survey": "Measure the monolith. One plan of everything, and the number of requests it costs.",
        "plan": "The split, planned: each team takes its own estate. Preview it next.",
        "preview": "Dry-run every move. Nothing is written; see the tag writes each would make.",
        "move": "Split it. Each team's resources take its own tag. No state file is touched.",
        "verify": "Prove it. Each estate plans clean, on its own, at the same moment.",
        "receipt": "The proof. The account's own log of every tag write, beside the tool's own record.",
        "teardown": "Clean up. Destroy what this run made; the account is listed to confirm it is empty.",
    }
    return CUE, PHASES, TITLE, VERB


@app.cell
def _(mo):
    # The redraw timer. Its value changes on the browser's clock, which runs
    # only while the element is displayed (the top bar mounts it).
    tick = mo.ui.refresh(default_interval="1s", options=["1s", "2s", "5s"], label="")
    return (tick,)


@app.cell
def _(mo):
    # Buttons are globals in a cell with no timer dependency, so a redraw never
    # rebuilds them and a click is served once.
    run_btn = mo.ui.button(label="Run ▸", value=0, on_click=lambda v: v + 1)
    reset_btn = mo.ui.button(label="↺ Reset & clean up", value=0, on_click=lambda v: v + 1)
    return reset_btn, run_btn


@app.cell
def _(CUE, PHASES, TITLE, VERB, mo, reset_btn, run_btn, stage, tick, viz):
    tick.value  # redraw on every tick while a phase runs

    _RUN = stage._demo_run
    # A button's count restarts at 0 when the page reloads while the kernel,
    # and this dict, live on. A count below the one last seen is a reload,
    # not a click: fall back to it, and serve only a count above it.
    for _btn, _key in ((run_btn, "run_seen"), (reset_btn, "reset_seen")):
        if _btn.value < _RUN.get(_key, 0):
            _RUN[_key] = _btn.value

    # -- Reset: tear down the current run, start a fresh one -----------------
    if reset_btn.value > _RUN.get("reset_seen", 0):
        _RUN["reset_seen"] = reset_btn.value
        stage.for_run(_RUN["id"]).start("teardown")   # clean the old run on floci, async
        _RUN["id"] = stage.new_run_id()

    st = stage.for_run(_RUN["id"])
    _run_dir = f"runs/{_RUN['id']}"
    _state = viz.load_run(_run_dir)

    def _verb_done(ph):
        verb = VERB[ph][0]
        if verb is None:                       # plan: done once the carve plan exists
            import pathlib
            return (pathlib.Path(_run_dir) / "carve.json").exists()
        return st.status(verb) in ("done", "recorded")

    def _verb_failed(ph):
        verb = VERB[ph][0]
        return verb is not None and st.status(verb).startswith("failed")

    _running = st.running()                    # the verb running right now, or None
    _done = [ph for ph in PHASES if _verb_done(ph)]
    _next = next((ph for ph in PHASES if ph not in _done), None)

    # -- Run: one click, one phase. The count is consumed here, once, not per
    # phase: a per-phase check would let the count that started seed start
    # survey the moment seed ended, and every phase after it, unclicked.
    if run_btn.value > _RUN.get("run_seen", 0):
        _RUN["run_seen"] = run_btn.value
        if _next is not None and VERB[_next][0] is not None and not _running:
            _verb, _extra = VERB[_next]
            if _verb == "move":
                # A snapshot of the map the instant before Move writes a
                # single tag, so the running map can show which cells have
                # flipped owner so far, not just the settled end state.
                _RUN["move_before"] = _state
            st.start(_verb, _extra or None)
            _running = _verb                   # this render already shows it active

    # -- Breadcrumb states ---------------------------------------------------
    def _step_state(ph):
        if _verb_failed(ph):
            return "failed"
        if _running and _running == VERB[ph][0]:
            return "active"
        if ph in _done:
            return "done"
        if ph == _next:
            return "ready"
        return "locked"

    _steps = "".join(
        f'<div class="lmd-step {_step_state(ph)}"><span class="n"><span class="lmd-dot"></span>{i + 1}</span>'
        f'<span class="t">{TITLE[ph]}</span></div>'
        for i, ph in enumerate(PHASES)
    )
    # -- The cue: what the ready (or running) phase is about -----------------
    _focus = next((ph for ph in PHASES if VERB[ph][0] == _running), None) if _running else _next
    if _focus is None:
        _cue_text = "Every phase is done. Reset to run it again."
        _busy = mo.md("")
    elif _running:
        # A live, ticking count of seconds, not a static "Running..." label
        # that looks identical whether the phase is working or stuck.
        _rec = st.phases.get(_running)
        _secs = f" · {_rec.elapsed:.0f}s" if _rec is not None else ""
        _cue_text = f"Running {TITLE[_focus]}…{_secs} {CUE.get(_focus, '')}"
        _busy = mo.md('<span class="lmd-busy"><span class="lmd-spin"></span>working</span>')
    else:
        _cue_text = CUE.get(_focus, "")
        _busy = mo.md("")
    _button = run_btn if (_next is not None and VERB[_next][0] is not None and not _running) else mo.md("").style({"width": "0"})
    _cue = mo.hstack([_button, mo.md(f'<span class="lmd-say">{_cue_text}</span>'), _busy], justify="start", align="center", gap=1)

    # -- The action stage: the live map --------------------------------------
    # Preview never writes a live tag (it stages, dry-runs, then restores),
    # so the live map has nothing to show yet; project() computes the map as
    # it would stand after the previewed moves, and ghost=True draws it
    # dashed and dimmed so a plan can never read as a fact. Once Move starts,
    # switch to the real map with just-changed cells ringed, so the split
    # visibly happens instead of jumping from grey to settled between polls.
    _plan_view = bool(_state.previews) and (_focus == "preview" or (_next == "move" and not _running))
    if _plan_view:
        _map_label = mo.Html('<span class="lmd-planned">planned, not yet written</span>')
        _map = mo.vstack([_map_label, mo.Html(f'<div class="lmd-stage">{viz.render_map_svg(viz.project(_state), width=900, ghost=True)}</div>')], gap=0.3)
    elif _running == "move" and _RUN.get("move_before") is not None:
        _map = mo.Html(f'<div class="lmd-stage">{viz.render_map_svg(_state, width=900, before=_RUN["move_before"])}</div>')
    else:
        _map = mo.Html(f'<div class="lmd-stage">{viz.render_map_svg(_state, width=900)}</div>')

    # payoff of the last done phase, if it left a number
    _pay = ""
    if _done:
        _last = _done[-1]
        _pv = VERB[_last][0]
        _pay = viz.payoff(_pv, _state) if _pv else ""
    _payline = mo.md(f'<span class="lmd-pay"><b>{_pay}</b></span>') if _pay else mo.md("")

    # -- Ledger, collapsible, at the bottom ----------------------------------
    _ledger = viz.render_ledger(_state, limit=20) if _state.ledger else "<div class='lmd-note' style='padding:10px 16px'>nothing written yet</div>"
    _tail = (_running and st.tail(_running)) or ""
    _ledger_body = _ledger + (f"<details style='padding:6px 16px'><summary class='lmd-note'>shell output</summary><pre style='font-size:12px;white-space:pre-wrap'>{mo.Html(_tail).text if _tail else ''}</pre></details>" if _tail else "")
    _drawer = mo.Html(
        "<details class='lmd-ledger' open style='border-top:1px solid var(--edge2); background:var(--panel)'>"
        "<summary style='padding:9px 16px; font-size:12px; letter-spacing:.05em; text-transform:uppercase; color:var(--mut); cursor:pointer'>ledger</summary>"
        f"<div style='max-height:32vh; overflow:auto'>{_ledger_body}</div></details>"
    )

    # The redraw timer must be on the page: a refresh element that is never
    # displayed has no browser-side timer, so nothing would ever re-render
    # while a phase runs. It sits beside Reset, small.
    _top = mo.hstack([
        mo.Html(f'<div class="lmd-bar" style="flex:1;border:none;padding:0">{_steps}</div>'),
        mo.hstack([tick, reset_btn], gap=0.5, align="center"),
    ], justify="space-between", align="center").style({"padding": "8px 14px", "border-bottom": "1px solid var(--edge2)"})

    mo.vstack([
        _top,
        mo.vstack([_cue, _map, _payline], gap=0.6).style({"flex": "1 1 auto", "min-height": "0", "padding": "16px 22px", "display": "flex", "flex-direction": "column"}),
        _drawer,
    ], gap=0).style({
        "position": "fixed", "inset": "0", "background": "var(--bg)", "z-index": "5",
        "display": "flex", "flex-direction": "column",
    })
    return


if __name__ == "__main__":
    app.run()
