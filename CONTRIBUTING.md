# Contributing to choudoufu

choudoufu is a fork of OpenTofu that adds live resource markers. It is
experimental and AWS only.

**Contributing to upstream OpenTofu is a different thing entirely.** Its
process, Slack, issue tracker and community meetings are at
[github.com/opentofu/opentofu](https://github.com/opentofu/opentofu/blob/main/CONTRIBUTING.md).
Nothing sent here reaches them. A change to the language, the CLI, providers,
backends or the plugin protocol belongs upstream, not in this repository.

## Where to start

Read [`HANDOFF.md`](HANDOFF.md) first. It is the standing playbook and says
what the work is for, what makes a change acceptable, and how a task gets from
the tracker to a merge.

Then [`contributing/README.md`](contributing/README.md) indexes everything
else, including the specifications and the artifacts every published figure is
rendered from.

## Building and testing

```
just build      # the binary
just ci         # exactly what CI runs, in order
just site-serve # preview the docs site
```

`just --list` shows the rest. `just demo` stands up a real estate against a
local AWS emulator and checks each claim the project makes. Its exit code is
the verdict.

## What a good change looks like

**Claims come with their evidence.** This project publishes figures about how
far it goes, and every one is rendered from a committed artifact under `live/`
rather than typed. A change that alters what the project claims carries the
regenerated artifact with it. `HANDOFF.md` explains why, including the
documents that went stale within the hour of being written.

**Refusals say what to do.** A construct this mode cannot support gets a
diagnostic naming the fix, an entry in `live/LIMITATIONS.md`, and a fixture.

**Adding a resource type** follows
[`contributing/LIVE-TABLES.md`](contributing/LIVE-TABLES.md).

## Reporting things

- A bug in choudoufu, use [`BUG_REPORTS.md`](BUG_REPORTS.md) and this
  repository's tracker.
- A security issue, use [`SECURITY.md`](SECURITY.md). Do not file it upstream.
- Conduct, see [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## LLM-assisted contributions

Accepted here. The `Co-Authored-By` trailers in the history are the working
convention. [`AGENTS.md`](AGENTS.md) carries the detail, including which parts
of it are upstream's policy rather than this fork's.

## Governance

[`GOVERNANCE.md`](GOVERNANCE.md) says who decides what, and which decisions
are not this project's to make.
