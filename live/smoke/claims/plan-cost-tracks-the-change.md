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

Unproven. No scenario has been written or run yet.

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
