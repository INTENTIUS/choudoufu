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

This two-minute run builds a small estate on your machine. Then it deletes the
state and plans again from the markers alone. Pick the platform you run.

## Before you start

Start Docker so that `docker info` succeeds. For Kubernetes, also install
`kind` and `kubectl`. Nothing reaches a cloud account.

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

Exit code 0 means every step passed. Otherwise the script names the failed step.

Step 2 runs `init` and `apply` with a local state file. The apply writes the
tags `tofu-estate` and `tofu-address` on each resource. Step 3 deletes
`terraform.tfstate`, and steps 4 and 5 plan with `-target` and then in full.
Both plans come back empty.

Step 6 changes attributes with the AWS CLI, and the plan shows exactly those.
Step 7 creates an undeclared security group, which shows as foreign. Step 8
removes a block and destroys that resource. Step 14 checks that each fixture
under `live/e2e/limits/` is refused by its named rule.

## On Kubernetes

```
just smoke k8s-greenfield
```

This starts a kind cluster and applies a namespace, a ConfigMap, a
ServiceAccount and a Service, with no AWS provider anywhere. Each object gets
the `tofu-estate` label and an address annotation. `kubectl` reads the label
back, `choudoufu live-ls` lists the estate by it, and the replan is empty with
the cache deleted. Each step prints its proof, and the run ends on one verdict
line.

## What just happened

The apply wrote a marker on each resource. In your own estate, an
`estate.chdf.hcl` file turns that on. Deleting the state was safe because
every resource already carried its marker. For unmarked resources, follow
[migrate an existing estate](../use/migrate/). From here the state file is a
cache, and choudoufu finds what it owns by its markers.

## Next

- [Start a new estate](../use/start/)
- [Migrate an existing estate](../use/migrate/)
- [What it is](../../how-it-works/)
- [`live/e2e/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md)
  lists every AWS harness step, and
  [claim 7](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/claims/identity-is-a-tag.md#on-kubernetes)
  the Kubernetes run.
