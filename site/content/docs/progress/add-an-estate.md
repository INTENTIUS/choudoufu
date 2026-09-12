---
title: "Add an estate"
weight: 40
---

# Add an estate

An estate is any real OpenTofu or Terraform configuration, pinned by tag or
commit. Adding one is a manifest entry and a script, and the site picks it up
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
go run ./tools/gauntlet render         # regenerates the artifact and the site's board data
```

Commit the entry, the script, `live/gauntlet.json`, `site/data/gauntlet.json`
and `site/data/gauntlet_board.json`. CI runs the same two commands nightly.
The site builds its progress pages from those two data files; there is no
rendered page to commit.

Lanes: {{< gauntlet-board "lanes" >}}. A `-core` estate needs `-reason`; the
rule for the core set is in
[`live/GAUNTLET.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/GAUNTLET.md).

A manifest entry looks like this:

{{< gauntlet-board "example" >}}
