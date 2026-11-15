# Contribute to the gauntlet

The work is ordered, so it needs no coordinator:

```
go run ./tools/gauntlet next        # the next unit: an estate and the first active stage it does not pass
```

A unit is one estate and one stage. Classify every difference from stock with
the five-row table in HANDOFF.md, fix it generically, and open a pull request
on a branch named `gauntlet/<estate>-<stage>`. Check for an open pull request
on the same unit first and take the next one if it exists. A gauntlet run
asks for "run" at a terminal, so the person at the keyboard starts it
(`just gauntlet-run <estate>`). Every pull request waits for a maintainer to
merge it.

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

Commit the entry, the script, `live/gauntlet.json`, `live/gauntlet-board.json`
and `live/BOARD.md`. The Gauntlet workflow runs the same two
commands when the maintainer dispatches it.

The lanes and the shape of a manifest entry are in
[GAUNTLET.md](GAUNTLET.md#the-manifest-entry). A `-core` estate needs
`-reason`; the rule for the core set is in
[GAUNTLET.md](GAUNTLET.md#the-core-set), and what a new estate has to bring
is in [GAUNTLET.md](GAUNTLET.md#estate-admission). The board itself is
[BOARD.md](BOARD.md).
