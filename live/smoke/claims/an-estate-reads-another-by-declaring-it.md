---
title: "Claim 44: An estate reads another estate's outputs only by declaring the read"
claim: an-estate-reads-another-by-declaring-it
---

# Claim 44: An estate reads another estate's outputs only by declaring the read

Every estate records its root output values under `tofu-outputs/<estate>/`
in the record store, for its own next plan. Issue #1371 lets another estate
in the same bucket read them, narrowly: for a value no live resource holds,
such as a name the producer chose. A value that mirrors a live attribute is
still read with a data source on the live resource (`live/OUTPUTS.md`).

Both sides say so. The consumer's configuration declares the read:

```hcl
data "terraform_estate_outputs" "network" {
  estate = "network"
  names  = ["cluster_services_namespace"]
}
```

and its role is rendered with `render-policy.sh app <bucket>
--reads-outputs-of network`, which adds one statement,
`ReadDeclaredDependenciesOutputs`, a GetObject on `tofu-outputs/network/*`.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info), and the AWS CLI and jq are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke an-estate-reads-another-by-declaring-it

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke an-estate-reads-another-by-declaring-it and report the
"caught" line: the consumer's role loses the grant, and the plan must
refuse naming estate network.
```

1. `one bucket, two roles`. A bucket that meets the bucket contract, and
   two emulator roles from the published renderer: `network`, and `app`
   with `--reads-outputs-of network`. The emulator's IAM filter is on.
2. `the producer applies and records its output`. `network` applies as its
   own role. Its one root output is one object under
   `tofu-outputs/network/`.
3. `the consumer plans with the value`. `app` plans as its own role, in its
   own directory. `terraform_data.service` is planned with
   `input = "cluster-services"`, read from network's record, and the plan
   carries the warning `Values from another estate are as of its last
   apply` with the time the record was written.
4. `the producer is destroyed, and its values go with it`. `network` runs
   `apply -destroy`. Nothing is left under `tofu-outputs/network/`, and the
   consumer's same plan now stops with `Another estate has not recorded
   this output`, naming estate network and the output. Before #1371's
   second change the object stayed and the consumer went on planning with a
   destroyed estate's last value; the scenario run against that binary
   fails at this step. A `-target` or `-exclude` destroy keeps the records,
   since the estate is still there.

Under `BREAK=1` the consumer's role is rendered without
`--reads-outputs-of network` and its configuration still declares the read.
The plan must fail with `This estate may not read another estate's
outputs`, naming estate network and the `--reads-outputs-of network` flag,
and must not show network's value. That the refusal is a named one and not
an evaluation against nothing is the point: the reader treats every failure
as a diagnostic, where the estate's reader of its own outputs logs and
skips.

What this claim does not say. The emulator does not evaluate `s3:prefix`,
`s3:RequestObjectTag` or `s3:ExistingObjectTag`, so the scenario removes the
conditions from the Allow statements, grants the list on `*` (the emulator
does not match a virtual-hosted ListObjectsV2 against the bucket's ARN), and
the tag Denies never fire. The refusal here comes from the resource scope of
the one statement the flag adds. The tag-conditioned half, that the grant
opens network's outputs and nothing else of network, is claim 35's, on real
AWS.
