---
title: Start a new estate
description: Install choudoufu, add one file, and apply an estate where choudoufu creates every resource.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/start/ in this repository.
  Install choudoufu, create `estate.chdf.hcl` with an estate name I confirm,
  remove any `backend` or `cloud` block, add `.tofu-records/` to `.gitignore`,
  then run `choudoufu init` and `choudoufu plan` and show me the plan.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

This page is for an estate with nothing in it yet, where choudoufu creates
every resource.

:::caution
If AWS already holds resources this configuration should manage, read
[migrate an existing estate](../migrate/) first. A live resource is bound to
your configuration only once its markers are on it, so applying against
unmarked resources creates a second copy beside them.
:::

## Install

Every [release](https://github.com/INTENTIUS/choudoufu/releases) carries
binaries for macOS, Linux and Windows on amd64 and arm64, with a `SHA256SUMS`
file.

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz
```

Or build from a checkout with `go build ./cmd/choudoufu`. Until a
configuration opts in, the binary behaves as the OpenTofu commit it forked
from.

## Turn markers on

Create `estate.chdf.hcl` beside your configuration and remove any `backend` or
`cloud` block.

```hcl
estate = "my-estate"
```

The estate name is the unit of ownership. Every resource is tagged with it, and
the tag is how the next plan finds the resource again.

That file is the whole setup. No `.tf` changes, so `terraform validate`,
`tflint` and editors keep passing, and reverting means deleting the file.
Records go to a local `.tofu-records` directory beside the module. Add
`record_store "s3"` to put them in a bucket a team can share; you
[create that bucket first](../setup/).

Add `.tofu-records/` to `.gitignore` before the first run. The directory holds
what a state file would have held, generated secrets included.

The same content can go in a `live` block inside `terraform`. Declaring both is
an error.

:::caution
Stock Terraform and OpenTofu reject a configuration with a `live` block,
because `live` is this fork's addition to the schema. The sidecar file avoids
that, since nothing else reads its extension.
:::

## A first configuration

```hcl
# AWS assigns the ID, so ownership comes back only from a tag.
resource "aws_vpc" "main" {
  cidr_block = "10.99.0.0/16"
}

# The bucket name is already the identity.
resource "aws_s3_bucket" "data" {
  bucket = "my-estate-data"
}
```

A fuller example is checked in at `live/e2e/estate-block/`.

## Apply

```
choudoufu init
choudoufu apply
```

The diff shows the markers about to be written beside each resource's
arguments.

```
  # aws_vpc.main will be created
  + resource "aws_vpc" "main" {
      + cidr_block = "10.99.0.0/16"
      + tags       = {
          + "tofu-address" = "aws_vpc.main"
          + "tofu-estate"  = "my-estate"
        }
    }
```

Those two tags are the whole ownership contract;
[`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md)
is the spec.

## Check that the markers carry it

Run `choudoufu plan` again. It rebuilds what it needs by reading the tags off
the live resources and reports no changes. To watch the same thing against a
local emulator first, run the [tutorial](../../tutorial/).

## Next

- [What you set up by hand](../setup/) before the first plan.
- [Day-2 operations](../day2/) for renames, removals and working with others.
- [Compatibility](../../../aws/compatibility/) for what this mode refuses.
