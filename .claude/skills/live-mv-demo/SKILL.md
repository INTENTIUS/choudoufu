---
name: live-mv-demo
description: Walk someone through the live-mv demo (examples/live-mv-workbench/demo.py), a live-against-floci notebook that splits a terralith by retagging. Use when someone wants to see choudoufu's tag-based ownership model in action, run the demo, or asks what the compose stack under examples/live-mv-workbench does.
---

# The live-mv demo

You are walking someone through `examples/live-mv-workbench`'s demo: a
single-screen marimo page that stands up a small terralith on floci (an AWS
emulator, no credentials, nothing billed) and splits it by retagging, live,
in front of them. Eight phases, one button. About five minutes end to end.

Confirm before starting anything that installs software or runs containers.
Ask the OS before naming an install command.

## 1. Say what this is, in three sentences

Choudoufu's ownership model is two tags on a resource. This demo proves it
live, on an emulator. A monolith splits into three team estates by
retagging alone, no state surgery, and a role folds from one team into
another the same way. Nothing here touches a real account or costs
anything.

## 2. Get the checkout

If the current directory is not a choudoufu checkout (no `go.mod` naming
`github.com/intentius/choudoufu`), **confirm**, then

```sh
git clone https://github.com/INTENTIUS/choudoufu && cd choudoufu/examples/live-mv-workbench
```

If it is one, `cd examples/live-mv-workbench` from the repo root.

## 3. Check for Docker and `just`

Ask which OS. The stack is two containers (floci and a marimo server), so
Docker is required. `just` is the front door but optional, since its
recipes are short enough to run by hand if it is missing.

```sh
docker version && just --version
```

| Missing | Install |
|---|---|
| Docker | macOS: `brew install --cask docker`, or the Docker Desktop `.dmg`. Windows: `winget install Docker.DockerDesktop`, WSL 2 backend. Linux: the distribution's `docker` packages. Open/start it once, wait for `docker version` to answer |
| `just` | `brew install just`, `winget install Casey.Just`, or `cargo install just` |

**confirm** before installing either.

## 4. Start it: one command

```sh
just up
```

This downloads the pinned choudoufu release into the demo container, no
Go toolchain needed, and starts floci beside it. It ends by printing the
URL, `http://localhost:2718`. Re-running `just up` is safe; it does not
double-start anything. `just up source` builds choudoufu from this
checkout's own code instead of the pinned release, for developing the
engine and the demo together. Skip it unless that is what you are doing.

If it fails, `docker compose -f compose/docker-compose.yml logs` says why.

Open `http://localhost:2718` (or open it yourself and describe what's on
screen: eight numbered steps across the top, a Run button, a big picture in
the middle, a ledger at the bottom).

## 5. Walk the eight phases

One button runs the next ready phase; a click while one is still running
does nothing until it finishes. For each phase below: click Run, read the
cue line out loud (it says what's about to happen and, for Move, warns
about its second act before it happens), wait for the breadcrumb to turn
green, then read the payoff line under the picture. It is always a real
number or fact from that phase's own log, never invented.

| # | phase | what to say happens |
|---|---|---|
| 1 | Seed | stands up one estate, three teams' worth of resources |
| 2 | Plan | free, completes the instant Seed writes the split plan |
| 3 | Survey | one plan of the whole monolith, the request count that's about to come down |
| 4 | Preview | dry-runs the three-way split; the map ghosts the plan, nothing is written |
| 5 | Move | two acts, the three-way split then a second retag folding one team into another; point out this is live proof ownership moves by tag alone, even across an unrelated boundary |
| 6 | Verify | both estates plan clean, at the same moment, proving nothing was left half-owned |
| 7 | Receipt | reads the account's own log of every tag write back; against floci this reports that CloudTrail isn't emulated there, which is expected, not a failure |
| 8 | Teardown | destroys everything this run made, then lists the account rather than trusting the tool's own report |

If a step's cue mentions a refusal or "findings," the tool is working as
designed. Say so and keep going.

## 6. Reset and finish

`↺ Reset & clean up` top-right tears down the current run and starts a
fresh one, any time. To stop the stack entirely:

```sh
just down
```

This keeps the provider plugin cache; `just reset` wipes everything and
runs `just up` again from clean.
