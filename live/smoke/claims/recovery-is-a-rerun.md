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

### terragucci rows: smoke, choudoufu/rows-takeover and choudoufu/killed

terragucci's smoke claims under scenario `smoke` kill a wave in its
pipeline. In `choudoufu/killed` the killed wave leaves a record for the
resource whose apply returned and none for the slow one, and the wave run
again applies only the second under the same approval. In
`choudoufu/rows-takeover` a wave killed after its change landed leaves its
row held, and the next push's wave takes it over with nothing unlocked.

A terragucci row counts toward the claim when it ran choudoufu 0.31.0,
the newest release in `live/history`, and `live/CLAIMS.md` gives each
row's state. The rows stand beside the scenarios above, whose `BREAK=1`
arms rebuild choudoufu from source, which a pipeline running a release
cannot do.

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
