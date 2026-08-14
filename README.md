# choudoufu

[![Go Reference](https://pkg.go.dev/badge/github.com/intentius/choudoufu.svg)](https://pkg.go.dev/github.com/intentius/choudoufu)

**Ownership on the resource.** <img src="docs/images/choudoufu-inline-64.png" width="32" height="32" alt="">

choudoufu is OpenTofu with live resource markers: no state file, no backend,
no lock. Each resource carries its own ownership record as ordinary cloud
tags, and every plan rebuilds prior state by reading those markers back off
the live system. Experimental, AWS only.

The name is stinky tofu: fermented, famously an acquired taste, and a fit
for an OpenTofu counterpart whose state is allowed to be stale. (The
[FAQ](https://intentius.io/choudoufu/faq.html) has the longer answer.)

The state model splits into three jobs, each serviced by ordinary IAM
governance: ownership and estate markers, tagged on the resource; a micro
state backend (`record_store`) holding the values of logical resources; and
receipts, which track the staleness of effects. Together they make estates
easy to carve into smaller domains and shrink the blast radius, with no
locks to manage.

If you already use OpenTofu, the short version is that `terraform.tfstate`
stops existing. Adoption is a tag you write. A rename is a tag you rewrite.

Built on OpenTofu (fork point
[`03743ce6e8`](https://github.com/opentofu/opentofu/commit/03743ce6e8)). The
exact upstream version this tree is built on lives in
[`version/VERSION`](version/VERSION), and each
[release](https://github.com/INTENTIUS/choudoufu/releases)'s notes name both
versions. The binary is `choudoufu`; everything outside live markers is stock
OpenTofu.

## Where this stands

Experimental, and AWS only. Most of the commonly used AWS resource types are
admitted; the gap that hurts is the connective tissue between them, such as
`aws_ecs_service` and `aws_lambda_permission`.

Type coverage is rarely what stops a configuration, though. A
`backend "s3"` block, a CI pipeline that saves a plan file (`-out` plus
`apply <planfile>`), a non-default workspace, a `count.index` in a resource
name, a `for_each` keyed by CIDRs, or an identity argument read from a data
source will each stop one first. Run

```
choudoufu live-check
```

in your configuration directory for a verdict on your own code with no cloud
credentials, or read
[Will my config work?](https://intentius.io/choudoufu/compatibility.html)
The full boundary, each limit with its reasoning, is
[`live/LIMITATIONS.md`](live/LIMITATIONS.md).

## See it prove itself

The demo is also the test suite. It stands up a real estate of resources
against a local AWS emulator, deletes the state file partway through, and
shows the plans stay exact anyway. It needs Docker and takes about two
minutes. The exit code is the verdict.

```
bash live/e2e/run.sh --expect 5
```

Or paste this to a coding agent (Claude Code or similar) and let it run the
demo end to end.

```
Clone https://github.com/INTENTIUS/choudoufu, then do the following.

1. Confirm Docker is running (`docker info` must succeed).
2. If Go is installed, skip this step. Otherwise download the latest
   release tarball for this platform from
   https://github.com/INTENTIUS/choudoufu/releases, extract it, and
   export TOFU_BIN=<absolute path to the extracted choudoufu binary>.
3. From the repo root, run: bash live/e2e/run.sh --expect 5
4. Report each step's result as the script prints it, and the final exit code.

Exit code 0 means every claim the script makes about live resource markers
held. Non-zero means one of them did not. Report which step failed.
```

## Install

Every tagged release publishes prebuilt binaries for macOS, Linux and
Windows (amd64 and arm64), with a `SHA256SUMS` file, on the
[releases page](https://github.com/INTENTIUS/choudoufu/releases). macOS and
Linux ship as `.tar.gz`; Windows ships as `.zip`, since that is what
Windows' built-in Explorer opens without extra tooling. To fetch the latest
for macOS or Linux:

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz   # unpacks ./choudoufu
```

On Windows, in PowerShell:

```powershell
gh release download -R INTENTIUS/choudoufu --pattern "*_windows_amd64.zip"
Expand-Archive choudoufu_*_windows_amd64.zip .   # unpacks .\choudoufu.exe
```

(use `*_windows_arm64.zip` on ARM64 Windows). Building from source is one
command (below).

## Which path is yours

**You have an estate already.** An estate with live resources needs care
before anything binds to it: adoption is a deliberate tag write, and until a
resource's markers are on it, it is not yours — applying too early is how
you get a duplicate created beside the real thing. The steps, which types
adopt automatically, and which need a hand-written tag are on
[Migrate an existing estate](https://intentius.io/choudoufu/migrate.html).

**You are starting fresh.** A greenfield estate is a `live` block and an
apply. [Start a new estate](https://intentius.io/choudoufu/start.html) walks
it end to end.

## Building and testing

```
go build ./cmd/choudoufu
go test ./...
```

The integration tier needs Docker and `TF_FLOCI_TEST=1`.

## Docs

The two user paths and the compatibility answer live on the docs site at
https://intentius.io/choudoufu/. The repository carries the normative specs
and the contributor material.

- [`live/MARKERS.md`](live/MARKERS.md) is the marker tag spec, the
  one integration surface external tooling relies on.
- [`live/LIMITATIONS.md`](live/LIMITATIONS.md) lists every
  construct the mode bounds or rejects, each with its lint rule and fixture.
- [`live/RECEIPTS.md`](live/RECEIPTS.md) covers receipts: how an
  effect that leaves nothing in the live system to read back gets a
  record that tracks its staleness.
- [`live/e2e/README.md`](live/e2e/README.md) documents the
  demo/test harness and how to read its output.

All stock OpenTofu documentation lives at
[opentofu.org](https://opentofu.org/docs/).

<p align="center">
  <img src="docs/images/choudoufu-hero.png" width="400" alt="a plate of choudoufu">
</p>

## License

MPL-2.0. Forked from [opentofu/opentofu](https://github.com/opentofu/opentofu)
at `03743ce6e8`. LICENSE and all copyright headers are unchanged from
upstream.

choudoufu is not affiliated with or endorsed by OpenTofu or the Linux
Foundation. OpenTofu is a registered trademark of the Linux Foundation.
