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

Unproven. The AWS scenario is not written yet, and the Kubernetes one is
written and has not run.

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

## On Kubernetes

### k8s-plan-cost-tracks-the-change

    just smoke k8s-plan-cost-tracks-the-change
    BREAK=1 just smoke k8s-plan-cost-tracks-the-change

Written for GitHub issue #2077 and not run yet. On a kind cluster, an
estate of 20 ConfigMaps, each its own block, plus one that reads the
first one's value, is applied and committed. The first ConfigMap's value
changes in the working tree, and the change is planned in full and with
`-cone=HEAD`, both through `live/smoke/k8sproxy.py`, which counts this
client's requests alone. The estate then grows to 80 ConfigMaps and the
same change is planned again. The cone plan must read the two ConfigMaps
in the cone by name and no other, propose the full plan's two updates,
and send the same number of requests at 20 and at 80, while the full
plan's count grows by at least one request per added ConfigMap. The
marker sweep stays estate-wide on a cone plan; on a cluster it is
discovery plus one label-selected list per kind served, so it does not
grow with the estate.

`BREAK=1` points `-cone` at a base that is not a commit. The guard then
plans the whole estate and warns that the cone plan fell back, and the
control passes only when the read check refuses that plan as a cone plan:
every ConfigMap read by name and the full plan's request count.
