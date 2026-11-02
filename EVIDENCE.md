# Evidence

What choudoufu has been checked against, and where the records are. This file
is updated with each release.

## Current numbers

- 13 claims, each proven on AWS and on Kubernetes. [live/CLAIMS.md](live/CLAIMS.md)
  lists them with the scenario or test behind each cell.
- GCP and Azure have no claim rows yet.
- Claims 2, 5, 6, 7, 13, 28 and 45 each cite terragucci scenario rows on AWS.
  Those rows are stale: they last ran choudoufu 0.31.0, and the claims are now
  about 0.32.0, so they are listed in [live/CLAIMS.md](live/CLAIMS.md) as last
  proven at 0.31.0 and do not count toward the 0.32.0 cells. The other proofs
  in those cells are unchanged.
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
