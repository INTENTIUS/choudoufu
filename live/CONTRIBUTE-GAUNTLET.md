# Contribute compute

Anyone can move the bars by spending tokens rather than time. The loop is
deterministic, so it needs no coordinator:

```
go run ./tools/gauntlet next        # the next unit: an estate and the first active stage it does not pass
just contribute [max-usd]           # one worker run on that unit, in a fresh worktree, with your own key
```

`just contribute` runs Claude Code headless under the brief in
`.claude/agents/gauntlet-worker.md`, which does exactly one unit: run the estate, classify every
difference from stock with the five-row table in HANDOFF.md, fix it generically,
re-run, render, and open a pull request whose title carries the unit ID. It
never merges. There is no hosted path; every worker runs under the key of
whoever started it.

Two contributors picking the same unit is expected. The worker checks for an
open pull request carrying the unit ID before starting and takes the next unit
if one exists. Every pull request waits for a maintainer to merge it.

Providers are lanes. A Kubernetes, GCP or Azure estate enters the same manifest with its
own lane, runs the same stages against its own substrate, and `next` picks it up
like any other; an emulator gap is a unit in the emulator's repository.

## Add an estate

An estate is any real OpenTofu or Terraform configuration, pinned by tag or
commit. Adding one is a manifest entry and a script, and the board picks it up
on the next run.

```
go run ./tools/gauntlet add <name> <repo-url> <tag-or-commit> -lane <lane> [-core -reason "..."] -source "<one line>"
```

That writes the entry and a script stub at `live/e2e/<name>/run.sh` with every
stage wired to the protocol and marked `not_run`. Fill the stub in, using the
script of a similar estate as the template
(`live/e2e/corpus-vpc-complete/run.sh` is the fullest), then:

```
go run ./tools/gauntlet run <name>     # runs it against the emulator, records verdicts
go run ./tools/gauntlet render         # regenerates the artifact and the board data
```

Commit the entry, the script, `live/gauntlet.json`, `site/data/gauntlet.json`
and `site/data/gauntlet_board.json`. The Gauntlet workflow runs the same two
commands when the maintainer dispatches it.

The lanes and the shape of a manifest entry are in
[GAUNTLET.md](GAUNTLET.md#the-manifest-entry). A `-core` estate needs
`-reason`; the rule for the core set is in
[GAUNTLET.md](GAUNTLET.md#the-core-set), and what a new estate has to bring
is in [GAUNTLET.md](GAUNTLET.md#estate-admission). The board itself is
[BOARD.md](BOARD.md).
