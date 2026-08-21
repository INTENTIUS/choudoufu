# Start a new estate

For an estate with nothing in it yet, where choudoufu creates every resource.

If AWS already holds resources this configuration should manage, read
[Migrate an existing estate](migrate.html) first. Nothing binds a live resource
to your configuration until its markers are on it, so applying against unmarked
resources creates a second copy beside them.

## Install

Every [tagged release](https://github.com/INTENTIUS/choudoufu/releases) carries
prebuilt binaries for macOS, Linux and Windows on amd64 and arm64, plus a
`SHA256SUMS` file.

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz   # unpacks ./choudoufu
```

Or build from a checkout.

```
go build ./cmd/choudoufu
```

Until a configuration opts in, the binary behaves as the OpenTofu commit it
forked from. Point it at existing work safely.

## Turn markers on

Create `estate.chdf.hcl` beside your configuration and remove any `backend` or
`cloud` block.

```hcl
# estate.chdf.hcl
estate = "my-estate"
```

The estate name is the unit of ownership. Every resource gets tagged with it,
and that tag is how the next plan finds the resource again.

That one file is the whole setup. No `.tf` changes, so stock `terraform
validate`, `tflint` and editors keep passing. Reverting means deleting the
file. Add a `record_store` here later if you need an effect the cloud cannot
report back.

The same content can live as a `live` block inside `terraform`.

```hcl
terraform {
  live {
    estate = "my-estate"
  }
}
```

Both forms are equivalent. Declaring both at once is an error.

:::warning
Stock Terraform and stock OpenTofu reject a configuration containing a `live`
block, because `live` is this fork's addition to the `terraform` block schema.
Any tool validating against upstream's schema does the same. The sidecar file
avoids this, because nothing reads its extension. See
[Will my config work](compatibility.html#editors-and-linters).
:::

## A first configuration

Two resources, one from each path identity resolves through.

```hcl
# AWS assigns the ID here, so ownership recovers only from a tag.
resource "aws_vpc" "main" {
  cidr_block = "10.99.0.0/16"
}

# The bucket name is already the identity, so this needs no discovery pass.
resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-e2e-block-data"
}
```

A fuller example adding a subnet, a security group, a log group and a
`count`-expanded pair of EIPs is checked in at `live/e2e/estate-block/`. It runs
as it stands.

## Apply

```
$ choudoufu init
$ choudoufu apply
```

The plan carries something a plain OpenTofu plan does not. Beside each
resource's arguments, the diff shows the ownership markers about to be stamped.

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

Those two tags are the entire ownership contract. `live/MARKERS.md` is the
normative spec and the surface external tooling can rely on.

## Check that the markers carry it

Run `choudoufu plan` again. It rebuilds prior state by reading the
`tofu-estate` and `tofu-address` tags back off the live resources, and reports
no changes. The tags alone were enough.

Repeat that on your own estate. It is the check you can run without trusting
this page.

## See it prove itself

The demo is also the test suite. It stands up a real estate against a local AWS
emulator, hands it to its markers partway through, and shows the plans stay
exact across the handover. Needs Docker, about two minutes.

```
bash live/e2e/run.sh --expect 5
```

The exit code is the verdict. `live/e2e/README.md` lists what each step proves.

## Next

- [Day-2 operations](day2.html) for renames, removals and working with other
  people.
- [Will my config work](compatibility.html) for the constructs this mode
  refuses. Read it before the configuration grows.
