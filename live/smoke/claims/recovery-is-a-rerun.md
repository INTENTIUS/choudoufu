---
title: "Claim 5: A crash is fixed by re-running"
claim: recovery-is-a-rerun
---

# Claim 5: A crash is fixed by re-running

Two disasters end an estate's day under stock. An apply that dies after a
create call leaves a resource no state file knows about, and re-applying
builds a duplicate while the original leaks. A lost state file is worse,
because the file was the record of everything you own. Here both end the
same way: run it again. What a dead apply marked is bound by the re-run,
and losing every local file loses no knowledge, because the markers are on
the resources and the records are in the record store.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### a-killed-apply-hides-nothing (claim 42 until #1817)

    just smoke a-killed-apply-hides-nothing
    BREAK=1 just smoke a-killed-apply-hides-nothing

A real apply is killed with SIGKILL at a point pinned by a resource count
read off the account, not a timer, and the next plan is measured for each
thing it left:

- A VPC is marked in its own create request, so there is no window: the
  next plan proposes nothing for it and the re-run binds it.
- A hosted zone is one of the ten `tag_on_create: false` types (#1084):
  `CreateHostedZone` takes no tags, so the markers land when the provider's
  whole create step returns, 15.0s later on the pinned emulator. A kill
  inside that window leaves a zone nothing can claim; the re-run builds a
  second one, and removing the first by hand is the one piece of surgery
  in the run.
- A `terraform_data` with a provisioner keeps its record, which is written
  after the whole walk, so a killed walk writes none. The next plan names
  it as a create and the effect runs twice: at-least-once, said by the
  run.

Then every local file goes (the cache, the lock file, `.terraform`), and
the plan from a fresh init is still `No changes.`; this step was
`recovery-is-a-rerun.sh`'s, which #1817 folded in here. `BREAK=1` strips
the markers from everything the killed apply created, and the re-run must
then propose and build a second VPC, which is stock's behaviour.

The window is real, bounded to those ten types, and measured on the
emulator; its width on real AWS has not been measured. [Recover an
estate](https://intentius.io/choudoufu/docs/use/recover-an-estate/) is the
procedure this implies.

## On Kubernetes

### k8s-recovery-is-a-rerun

    just smoke k8s-recovery-is-a-rerun
    BREAK=1 just smoke k8s-recovery-is-a-rerun

Needs `kind` and `kubectl`. An apply is SIGKILLed between one ConfigMap's
create and the next one's. The first object exists and carries the label;
the second was never sent; nothing was written that remembers either. The
next plan binds the first by its label, namespace and name and proposes
only what the killed run left undone; one re-run adds it, keeps the first
object's uid, and replans empty. `BREAK=1` strips the label after the
kill, and the plan must refuse the object by name. `live/GAUNTLET.md`
stage 10 (day2_crash) measures the same property on every kind-lane
gauntlet estate.
