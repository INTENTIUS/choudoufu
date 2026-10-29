# Evidence

What choudoufu has been checked against, and where the records are. This file
is updated with each release.

## Current numbers

- 13 claims, each proven on AWS and on Kubernetes. [live/CLAIMS.md](live/CLAIMS.md)
  lists them with the scenario or test behind each cell.
- GCP and Azure have no claim rows yet.
- 46 estates on the gauntlet board, which runs real OpenTofu and Terraform
  configurations beside stock OpenTofu. The Azure estates pass.
  [live/BOARD.md](live/BOARD.md) has every stage of every estate.

## How it is checked

A claim is a short promise. Each one has a scenario you run with
`just smoke <name>`, or a Go test, and `BREAK=1` breaks a fact on purpose so
the run must catch it. The claim rows come from `live/smoke/claims.json`.

An estate on the board is pinned by commit and run through the same stages
each time. The board is re-measured when the maintainer runs it, so a row is
as current as its date and commit.

[live/GAUNTLET.md](live/GAUNTLET.md) defines the stages.
