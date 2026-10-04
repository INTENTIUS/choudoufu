---
title: "Claim 6: One command in, one file out"
claim: roundtrip
---

# Claim 6: One command in, one file out

## On AWS

Migrating to a new state tool is usually a trapdoor: once your estate
is in, the only way back is another migration project. Here the door in
is `live-import`, which reads the state file you already have and
stamps ownership markers on what verifies, and the door out is the
local cache, which is a stock-format state file ready to hand back.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke roundtrip

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke roundtrip and report the "caught" line: it skips
live-import, and the plan must propose a duplicate estate - the
documented quiet failure of every migration.
```

The steps as they print:

1. `stock stands the estate up, tagless` - pinned stock OpenTofu, in
   its own container, applies a seven-resource estate with no tags
   anywhere. Identity exists only in `terraform.tfstate`.
2. `the door in - one command` - `live-import` reads that file once
   and verifies every resource against the live system; what verifies
   gets the two markers. Nothing else is touched.
3. `bound - and the old record is now optional` - the choudoufu plan is
   clean, the state file is deleted, and an ordinary apply keeps the
   estate converged while refreshing the cache.
4. `the door out - one file` - the cache is copied to
   `terraform.tfstate` and the live block removed. Stock's first plan
   back proposes only the removal of the two marker tags; one apply
   later, nothing of the fork remains.
5. `teardown - by stock, from the handed-back file` - stock destroys
   all seven resources using the file choudoufu handed back.

The `BREAK=1` run skips the one command. The plan must then propose
building a duplicate estate beside the real one, because turning on the
live block never binds resources by itself - the markers do.

## On Kubernetes

### k8s-roundtrip

    just smoke k8s-roundtrip
    BREAK=1 just smoke k8s-roundtrip

Needs `kind`, `kubectl` and `terraform`. Stock Terraform stands four
objects up with a plain state file; one `live-import` labels them and
leaves the state file byte for byte; the state file is deleted and the
estate plans from labels. On the way out the cache is copied to
`terraform.tfstate`, its provider address rewritten once with `terraform
state replace-provider` (Terraform resolves `hashicorp/kubernetes`
against its own registry), and stock's first plan may only remove the
label and the annotation. Stock then converges and destroys all four.
`BREAK=1` skips the `live-import`, and the live plan must refuse the
unlabelled objects by name.
