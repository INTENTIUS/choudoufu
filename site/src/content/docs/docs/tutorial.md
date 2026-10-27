---
title: "Tutorial: see markers work"
description: Stand up a small estate on a local AWS emulator or a kind cluster, delete its state cache, and watch the next plan find everything by its markers.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/tutorial/ in a checkout of
  INTENTIUS/choudoufu. Check that `docker info` succeeds, run
  `just smoke roundtrip` for AWS or `just smoke k8s-greenfield` for
  Kubernetes, and report each verdict line.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

This two-minute run builds a small estate on your machine, deletes the state,
and plans again from the markers alone.

## Before you start

Install Docker (started, so `docker info` succeeds), the `aws` CLI, Go and
`just`, then clone the repository:

```
git clone https://github.com/INTENTIUS/choudoufu.git
cd choudoufu
```

For Kubernetes, also install `kind` and `kubectl`.

## On AWS

From the root of the clone:

```
just smoke roundtrip
```

The output ends like this:

```
PASS: smoke scenario 'roundtrip' - every claim held (smoke v...)
```

Stock OpenTofu stands up an untagged estate. `live-import` tags each
resource with `tofu-estate` and `tofu-address`. With `terraform.tfstate`
deleted, the plan comes back empty. Stock then takes the estate back from
one file and destroys it.

## On Kubernetes

```
just smoke k8s-greenfield
```

This applies four objects to a kind cluster and deletes the cache. The replan
finds them by the `tofu-estate` label and comes back empty.

## What just happened

The run wrote a marker on each taggable resource, so the state file is now a cache.
In your own estate, an `estate.chdf.hcl` file turns that on.

## Next

- [Start a new estate](../use/start/)
- [Migrate an existing estate](../use/migrate/)
- [What it is](../../how-it-works/)
