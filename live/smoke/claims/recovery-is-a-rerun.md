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
    BREAK_EARLY_RECORD=1 just smoke a-killed-apply-hides-nothing

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
- An `aws_iam_group_policy` whose name the provider assigns has nowhere
  to carry a marker, so its record is the only place its identity is held.
  A `random_password`'s record is the password, and a `terraform_data`'s
  is the only trace that its provisioner ran. Since #1944 each record is
  written the moment its instance's apply step returns, so the kill, which
  lands after all three, leaves a record for each: the next plan proposes
  none of them, and the re-run leaves one inline policy, the same password
  and an effect that ran once.

Then every local file goes (the cache, the lock file, `.terraform`), and
the plan from a fresh init is still `No changes.`; this step was
`recovery-is-a-rerun.sh`'s, which #1817 folded in here. `BREAK=1` strips
the markers from everything the killed apply created, and the re-run must
then propose and build a second VPC, which is stock's behaviour.
`BREAK_EARLY_RECORD=1` runs a binary rebuilt to write no record until the
walk is over, which is how every apply behaved before #1944: the killed
run leaves no record, the plan proposes the password again, and the re-run
leaves two inline policies on the group and runs the effect twice.

The window is real, bounded to those ten types, and measured on the
emulator; its width on real AWS has not been measured. [Recover an
estate](https://intentius.io/choudoufu/docs/use/recover-an-estate/) is the
procedure this implies.

## On Kubernetes

### gauntlet stage reference-k8s-workloads:day2_crash

Cited, not run here: the gauntlet's `day2_crash` stage on the
`reference-k8s-workloads` estate, read from that row of
`live/gauntlet.json`, and this cell is proven only while it reads pass.
An apply creating two objects is interrupted by a real SIGTERM the instant
the first create commits. The next plan proposes exactly the remainder and
binds the created object by its `tofu-estate` label, namespace and name,
matching stock's own plan from the same position; one recovery apply adds
the second object and the replan is empty. The stage's control asserts
nothing is proposed and must fail. Every kind-lane estate runs the same
stage (`live/GAUNTLET.md`, stage 10); only the maintainer runs the
gauntlet.

`k8s-recovery-is-a-rerun.sh` proved this with a ConfigMap pair until the
stage replaced it. What it had that the stage does not is its control: it
stripped the label after the kill and required the plan to refuse the
object by name.
