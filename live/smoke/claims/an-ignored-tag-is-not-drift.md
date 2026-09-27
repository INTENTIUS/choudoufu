---
title: "Claim 45: A tag added out of band does not churn the plan once the estate ignores it"
claim: an-ignored-tag-is-not-drift
---

# Claim 45: A tag added out of band does not churn the plan once the estate ignores it

Kubernetes' claim 27 gives its own version of this for free: a key the
configuration never declared - the API server's own
`kubernetes.io/metadata.name`, a controller's annotation - stays the
server's and churns nothing, because `kubernetes_manifest`'s
`computed_fields` reads the prior manifest to decide what to keep.

AWS has no such branch, and this is not a state-file question. A taggable
resource's `tags` argument is Optional but not Computed on nearly every
type, so Terraform's own core plan mechanics - common to every provider -
propose the configured value verbatim as the new state on every plan. A
stateless run's rebuilt prior carries the live object's real tags, so a tag
present live and absent from configuration reads as a difference and plans
an update removing it - the same answer a stock, state-backed run gives a
real account for the identical drift. This is a well-known AWS provider
behaviour, and it is why the provider ships an `ignore_tags` block: the
ordinary, stock-compatible way an estate tells its provider "a key another
system owns is not my business."

```hcl
provider "aws" {
  ignore_tags {
    keys = ["CostCenter"]
  }
}
```

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info), and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke an-ignored-tag-is-not-drift

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke an-ignored-tag-is-not-drift and report the "caught"
line: with no ignore_tags declared, the identical out-of-band tag plans
an update.
```

1. `an estate applies`. One `aws_cloudwatch_log_group` with a declared
   `team` tag, and a provider block that already names `CostCenter` as a
   key it does not manage.
2. `the same key, added out of band, does not churn the plan`. The plain
   AWS CLI - not choudoufu - tags the log group with `CostCenter`. The next
   plan reads "No changes.": the tag is on the object, and the plan never
   treated it as this estate's business.
3. `a tag the estate DOES declare still plans when it changes`.
   `ignore_tags` names `CostCenter` alone; editing the declared `team` tag
   still plans and applies an update, and that update never touches
   `CostCenter`. The silence in step 2 is about one key, not about the
   estate going blind to its own tags.

Under `BREAK=1` the estate's `ignore_tags` declaration is removed - the one
line of this estate's own configuration the claim is about, not a
choudoufu code path. choudoufu has no tag-diffing of its own to break with
a build overlay: a resource's tags are planned entirely by the real
`hashicorp/aws` provider process, the same one a stock run talks to. With
`ignore_tags` gone, the identical out-of-band write must plan an update
naming `CostCenter`, proving step 2's clean plan was the declaration at
work.

What this claim does not measure: a tag added out of band to an object
choudoufu itself wrote for a record-backed resource. That is not a
plan-time question - `S3Store.PutIfVersion`
(`internal/live/staterecord/s3.go`) replaces an object's whole tag set on
every write, and no code path reads a record object's own tags to compute
a diff; they are read only to confirm the object is this estate's, never
compared against a configured value. Established by reading the store's
write path, not by a scenario step.
