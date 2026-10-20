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

Use this page for an empty estate where choudoufu creates every resource.

:::caution
If AWS already holds these resources, [migrate the estate](../migrate/)
first. Applying against unmarked resources creates duplicates.
:::

## Install

Every [release](https://github.com/INTENTIUS/choudoufu/releases) carries
binaries for macOS, Linux and Windows on amd64 and arm64, with `SHA256SUMS`.

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz
```

Or build from a checkout with `go build ./cmd/choudoufu`. Until a
configuration opts in, the binary behaves as stock OpenTofu.

## Turn markers on

Create `estate.chdf.hcl` beside your configuration and remove any `backend` or
`cloud` block.

```hcl
estate = "my-estate"
```

Every resource is tagged with the estate name. Your `.tf` files stay
unchanged. Delete the file to revert.

Records go to a local `.tofu-records` directory. Add it to `.gitignore` before
the first run, because it holds generated secrets. A team can share records in
a bucket with `record_store "s3"` once [the bucket exists](../setup/).

A `live` block inside `terraform` takes the same content. Stock Terraform
rejects that block, so prefer the sidecar file.

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

## Apply

```
choudoufu init
choudoufu apply
```

The diff shows the markers on each resource.

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

[`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md)
specifies the two tags.

## Check that the markers carry it

Run `choudoufu plan` again. It reads the tags and reports no changes.

## Next

- [What you set up by hand](../setup/)
- [Day-2 operations](../day2/)
- [Compatibility](../../../aws/compatibility/)
