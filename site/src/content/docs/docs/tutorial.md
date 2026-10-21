---
title: "Tutorial: see markers work"
description: Stand up a small estate on a local AWS emulator or a kind cluster, delete its state cache, and watch the next plan find everything by its markers.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/tutorial/ in a checkout of
  INTENTIUS/choudoufu. Check that `docker info` succeeds, run
  `bash live/e2e/run.sh --expect 5` for AWS or `just smoke k8s-greenfield`
  for Kubernetes, and tell me which steps passed and, if it failed, which
  step it named.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

This two-minute run builds a small estate on your machine, deletes the state,
and plans again from the markers alone.

## Before you start

Start Docker so that `docker info` succeeds. For Kubernetes, also install
`kind` and `kubectl`.

## On AWS

From the root of a checkout of this repository:

```
bash live/e2e/run.sh --expect 5
```

The output ends like this:

```
EXPECT 5: OK -- every step phase<=5 is pass, every step phase>5 is not_implemented
PASS: live-mode E2E harness reached the end.
```

Step 2 applies and tags each resource with `tofu-estate` and `tofu-address`.
Step 3 deletes `terraform.tfstate`. Steps 4 and 5 plan again, and both plans
come back empty.

## On Kubernetes

```
just smoke k8s-greenfield
```

This applies four objects to a kind cluster and deletes the cache. The replan
finds them by the `tofu-estate` label and comes back empty.

## What just happened

The apply wrote a marker on each resource, so the state file is now a cache.
In your own estate, an `estate.chdf.hcl` file turns that on.

## Next

- [Start a new estate](../use/start/)
- [Migrate an existing estate](../use/migrate/)
- [What it is](../../how-it-works/)
