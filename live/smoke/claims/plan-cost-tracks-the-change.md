---
title: "Claim 47: A plan costs its change"
claim: plan-cost-tracks-the-change
---

# Claim 47: A plan costs its change

[Claim 14](plan-cost-tracks-the-estate.md) says a plan costs its estate,
not the account. This one goes further for a pull request: `choudoufu plan
-cone=BASE` plans the cone of the change since `BASE` (the changed,
deleted and refactored blocks and everything that reads them), so its
read pass costs the cone, and its changes are a full plan's changes
restricted to the cone. A guard plans the whole estate instead, and says
why, when the cone cannot be trusted (GitHub issue #1956).

Unproven on AWS, where the scenario is not written yet. Open on
Kubernetes, where the proof is a draft that has not run:
`live/smoke/drafts/k8s-plan-cost-tracks-the-change.sh` (GitHub issue
#2077), run with `just smoke k8s-plan-cost-tracks-the-change` and
`BREAK=1 just smoke k8s-plan-cost-tracks-the-change`. On a kind cluster
it changes one of 20 ConfigMaps, which a second block reads, and plans
the change in full and with `-cone=HEAD` through `live/smoke/k8sproxy.py`,
then again once the estate has grown to 80. The cone plan must read the
two ConfigMaps in the cone by name and no other, propose the full plan's
two updates, and send the same number of requests at 20 and at 80, while
the full plan's count grows by at least one request per added ConfigMap.
`BREAK=1` points `-cone` at a base that is not a commit, so the guard
plans the whole estate, and passes only when the read check refuses that
plan. The draft gets its `## On Kubernetes` section here when it is
promoted after a green run of both arms (live/smoke/drafts/README.md).

## On AWS

### plan-cost-tracks-the-change

    just smoke plan-cost-tracks-the-change
    BREAK=1 just smoke plan-cost-tracks-the-change

The scenario is a stub that fails before it touches anything. The one to
write runs a 100-instance change on terralith-scale at
745 and 10,069 resources, counts the cone plan's calls against the full
plan's, and checks that the cone plan's changes equal the full plan's
restricted to the cone. `BREAK=1` drops the cone's dependents, and the
missed update must be caught. The unit half of the claim is in
`internal/live/cone` and `internal/command/live_cone_plan_test.go`.

## On GCP

No GCP scenario is written yet, so the cell is open. The marker sweep
stays estate-wide on a cone plan, as on the other substrates.
