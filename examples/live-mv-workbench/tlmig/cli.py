"""The command line: one phase per invocation, reusing a run directory.

    tlmig <phase> [--run <id>] [--auto]

A phase name runs that beat and returns. --run <id> re-opens an existing run
(its manifest, its event feed, its estate working dirs), which is how the
notebook runs one phase per cell against the same run, and how a phase that
failed can be retried. --auto drops the keypress between steps and answers the
destructive-op confirmations, for a rehearsal or CI; it never relaxes a fence.

The workflow verbs are seed, survey, preview, move, verify (plus preflight,
receipt, teardown); the pre-workbench names (setup, slow-plan, decompose,
carve, fast-plan, guard) remain as aliases for one release. `status` reads what
is live now, `all` runs the whole workflow in order for a rehearsal, and
`teardown` is always available and always safe to run.
"""

from __future__ import annotations

import argparse
import os
import sys

from . import beats, config, env, ui

# The full-rehearsal order. Individual phases are the demo; this is for a dry
# run under --auto.
_ALL = ["preflight", "seed", "survey", "preview", "move", "verify", "receipt", "teardown"]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="tlmig", description="choudoufu live-mv workbench (terralith migration)")
    parser.add_argument(
        "phase",
        choices=[*beats.PHASES, "status", "reset", "all"],
        help="the beat to run (or status / reset / all)",
    )
    parser.add_argument("--run", metavar="ID", help="reuse an existing run id instead of minting a new one")
    parser.add_argument("--auto", action="store_true", help="no keypress between beats; auto-confirm destructive ops")
    # seed accepts --demo (the built-in terralith seed). The general seed
    # flags (--config/--estate/--state/--approve) arrive with the seed verb.
    parser.add_argument("--demo", action="store_true", help="seed: use the built-in terralith demo")
    parser.add_argument("--config", metavar="DIR", help="seed: adopt the config in this directory")
    parser.add_argument("--estate", metavar="NAME", help="seed: the estate to adopt into")
    parser.add_argument("--state", metavar="FILE", help="seed: the tfstate to verify against (live-import)")
    parser.add_argument("--approve", action="store_true", help="seed: stamp the markers (default is verify-only)")
    args = parser.parse_args(argv)

    if args.auto:
        os.environ["TLMIG_AUTO"] = "1"
        ui.AUTO = True

    cfg = config.load(args.run)
    if args.run is None:
        ui.console.print(f"[dim]new run [bold]{cfg.run_id}[/]; reuse it with --run {cfg.run_id}[/]")
    else:
        ui.console.print(f"[dim]run [bold]{cfg.run_id}[/][/]")

    try:
        if args.phase == "all":
            for name in _ALL:
                beats.PHASES[name](cfg)
        elif args.phase == "status":
            env.status(cfg)
        elif args.phase == "reset":
            env.reset(cfg)
        elif args.phase == "seed":
            beats.seed(cfg, demo=args.demo, config_dir=args.config, estate=args.estate, state=args.state, approve=args.approve)
        else:
            beats.PHASES[args.phase](cfg)
    except KeyboardInterrupt:
        ui.warn("interrupted — the run is not torn down; `tlmig teardown --run "
                f"{cfg.run_id}` when ready")
        return 130
    except Exception as exc:  # a guard refusal or a failed command; show it and exit nonzero for the cell
        ui.fatal(str(exc))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
