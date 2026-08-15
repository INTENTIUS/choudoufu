# Start a new estate

This page is for an estate with nothing in it yet, where you are writing the
configuration and choudoufu will create every resource in it.

If AWS already holds resources this configuration should manage, stop here and
read [Migrate an existing estate](migrate.html) first. Nothing binds a live
resource to your configuration until its markers are on it, so applying against
unmarked resources creates a second copy beside them. Get that right before
anything else.

## Install

Prebuilt binaries for macOS, Linux and Windows on amd64 and arm64 are attached
to every [tagged release](https://github.com/INTENTIUS/choudoufu/releases),
alongside a `SHA256SUMS` file. macOS and Linux ship as `.tar.gz`, Windows as
`.zip`.

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz   # unpacks ./choudoufu
```

Building from a checkout is one command.

```
go build ./cmd/choudoufu
```

The binary is called `choudoufu`. Until a configuration opts in, with the
sidecar file below or a `live` block, it behaves as the OpenTofu commit it
was forked from, so you can point it at existing work without changing
anything.

## Turn markers on

Create a file named `estate.chdf.hcl` beside your configuration, and remove
any `backend` or `cloud` block.

```hcl
# estate.chdf.hcl
estate = "my-estate"
```

The estate name is the unit of ownership. Every resource this configuration
manages gets tagged with it, and that tag is how the next plan finds the
resource again.

That one file is the whole setup. No `.tf` file changes, so stock
`terraform validate`, `tflint` and editors keep passing on the rest of the
repository, and reverting is deleting the file. If your configuration later
needs an effect the cloud cannot report back on, you add a `record_store` to
this same file. Until then the markers on your resources hold everything.

If you prefer the configuration in one place, the same content can live as a
`live` block inside `terraform`.

```hcl
terraform {
  live {
    estate = "my-estate"
  }
}
```

The two forms are equivalent. Declaring both at once is an error, so that
there is one source of truth.

:::warning
This applies to the in-block form only. Stock Terraform and stock OpenTofu
reject a configuration containing a `live` block, because `live` is this
fork's addition to the `terraform` block's schema and nothing signals it to a
tool that never heard of it. Any tool that validates the `terraform` block
against upstream's schema will do the same. The sidecar file has none of this
cost, because its extension is one those tools never read. See
[Will my config work](compatibility.html#editors-and-linters) for the
details.
:::

## A first configuration

Two resources, one from each of the paths identity is resolved through.

```hcl
# AWS assigns the ID here, so ownership can only be recovered from a tag.
resource "aws_vpc" "main" {
  cidr_block = "10.99.0.0/16"
}

# The bucket name in the configuration is already the identity, so this one
# needs no discovery pass to be found again.
resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-e2e-block-data"
}
```

A fuller example, adding a subnet, a security group, a CloudWatch log group and
a `count`-expanded pair of EIPs, is checked in at `live/e2e/estate-block/`. Its
`README.md` explains why each resource is there. It runs as it stands.

## Apply

```
$ choudoufu init
$ choudoufu apply
```

The plan carries something a plain OpenTofu plan does not. Alongside each
resource's own arguments, the diff shows the ownership markers about to be
stamped on it.

```
  # aws_vpc.main will be created
  + resource "aws_vpc" "main" {
      + cidr_block = "10.99.0.0/16"
      + tags       = {
          + "tofu-address" = "aws_vpc.main"
          + "tofu-estate"  = "my-estate"
        }
      ...
    }
```

Those two tags are the entire ownership contract. `live/MARKERS.md` in the
repository is the normative spec, and it is the surface external tooling can
rely on.

## Check that the markers carry it

Run `choudoufu plan` again in this directory. It rebuilds prior state by
reading the `tofu-estate` and `tofu-address` tags back off the live
resources, and reports no changes. The tags alone were enough.

Repeat that on your own estate. It is the check you can run without trusting
this page, because after a live apply the plan rebuilt from markers alone is
empty.

## See it prove itself

The demo is also the test suite. It stands up a real estate against a local AWS
emulator, hands it over to its markers partway through, and shows the plans
stay exact across the handover. It needs Docker, and takes about two minutes on
a warm Go build cache.

```
bash live/e2e/run.sh --expect 5
```

The exit code is the verdict. `live/e2e/README.md` lists what each step proves
and what the other exit codes mean.

## Next

- [Day-2 operations](day2.html) for renames, removals and running this with
  other people.
- [Will my config work](compatibility.html) for the constructs the mode
  refuses. Read it before the configuration grows.
