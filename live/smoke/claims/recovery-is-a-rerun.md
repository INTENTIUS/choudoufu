---
title: "Claim 5: A crash is fixed by re-running"
claim: recovery-is-a-rerun
---

# Claim 5: A crash is fixed by re-running

What a killed apply created is bound by the next run, and losing every
local file loses nothing, because the markers are on the resources and
the records are in the record store.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### a-killed-apply-hides-nothing

    just smoke a-killed-apply-hides-nothing
    BREAK=1 just smoke a-killed-apply-hides-nothing
    BREAK_EARLY_RECORD=1 just smoke a-killed-apply-hides-nothing

Needs Go. A real apply is killed with SIGKILL at a point pinned by a
resource count read off the account. A VPC is marked in its own create
request, so the next plan proposes nothing for it and the re-run binds it.
An `aws_iam_group_policy` with a provider-assigned name, a
`random_password` and a `terraform_data` each have their record written
the moment their apply step returns, so the next plan proposes none of
them and the re-run leaves one inline policy, the same password and an
effect that ran once. Then every local file goes (the cache, the lock
file, `.terraform`), and the plan from a fresh init is `No changes.`
`BREAK=1` strips the markers from everything the killed apply created,
and the re-run must propose a second VPC. `BREAK_EARLY_RECORD=1` runs a
binary that writes no record until the walk is over, and the re-run must
leave two inline policies and run the effect twice. [Recover an
estate](https://intentius.io/choudoufu/docs/use/recover-an-estate/) is the
procedure.

## On Kubernetes

### gauntlet stage reference-k8s:day2_crash

The gauntlet's `day2_crash` stage on the `reference-k8s` estate, read
from that row of `live/gauntlet.json`. An apply creating two objects is
interrupted by a real SIGTERM the instant the first create commits. The
next plan proposes exactly the remainder and binds the created object by
its `tofu-estate` label, namespace and name, matching stock's own plan
from the same position; one recovery apply adds the second object and the
replan is empty. The stage's control asserts nothing is proposed and must
fail.
