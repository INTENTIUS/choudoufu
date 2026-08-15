# choudoufu

[![Go Reference](https://pkg.go.dev/badge/github.com/intentius/choudoufu.svg)](https://pkg.go.dev/github.com/intentius/choudoufu)

**IAM-governed state for OpenTofu.** <img src="docs/images/choudoufu-inline-64.png" width="32" height="32" alt="">

Each resource carries its own ownership record as ordinary cloud tags. AWS
can tell you what an estate contains. Your IAM already decides who may read
or change it. Experimental, AWS only.

**An estate is inherited by being granted access to it.** Handover is
granting a role. Splitting an estate in two is rewriting tags. Whoever
inherits one can list what they got with any cloud tool before running
anything.

The three jobs a state file does are each done here by a feature the platform
already has. Which real resource an address refers to is a tag on the
resource. Values AWS has nowhere to put go in a `record_store`, backed by
Parameter Store, S3, or a local directory. Who may read or change either is
your IAM, per resource, with no second permission model to keep in sync.
Effects that leave nothing behind to read back get a receipt, which tracks
their staleness.

Adoption is a tag you write. A rename is a tag you rewrite.

The name is stinky tofu, fermented and famously an acquired taste, a fit for
an OpenTofu counterpart whose state is allowed to be stale. The
[FAQ](https://intentius.io/choudoufu/faq.html) has the longer answer.

Built on OpenTofu (fork point
[`03743ce6e8`](https://github.com/opentofu/opentofu/commit/03743ce6e8)). The
exact upstream version lives in [`version/VERSION`](version/VERSION), and
each [release](https://github.com/INTENTIUS/choudoufu/releases)'s notes name
both. Everything outside live markers is stock OpenTofu.

## Where this stands

Most commonly used AWS resource types are admitted. The gap that hurts is the
connective tissue between them, such as `aws_ecs_service` and
`aws_lambda_permission`.

Type coverage is rarely what stops a configuration. A `backend "s3"` block, a
CI pipeline that saves a plan file (`-out` plus `apply <planfile>`), a
non-default workspace, a `count.index` in a resource name, a `for_each` keyed
by CIDRs, or an identity argument read from a data source will each stop one
first. Run

```
choudoufu live-check
```

in your configuration directory for a verdict on your own code, with no cloud
credentials. [Will my config work?](https://intentius.io/choudoufu/compatibility.html)
covers the same ground, and [`live/LIMITATIONS.md`](live/LIMITATIONS.md) has
every limit with its reasoning.

## See it prove itself

The demo is also the test suite. It stands up a real estate against a local
AWS emulator, hands it over to its markers partway through, and shows the
plans stay exact across the handover. Docker, about two minutes, and the exit
code is the verdict.

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

Every tagged release publishes prebuilt binaries for macOS, Linux and Windows
(amd64 and arm64) with a `SHA256SUMS` file, on the
[releases page](https://github.com/INTENTIUS/choudoufu/releases). To fetch the
latest for macOS or Linux, run

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz   # unpacks ./choudoufu
```

Windows ships as `.zip`, which Explorer opens without extra tooling.

```powershell
gh release download -R INTENTIUS/choudoufu --pattern "*_windows_amd64.zip"
Expand-Archive choudoufu_*_windows_amd64.zip .   # unpacks .\choudoufu.exe
```

(use `*_windows_arm64.zip` on ARM64 Windows).

## Which path is yours

**You have an estate already.** Adoption is a deliberate tag write. Until a
resource's markers are on it, it is not yours, and applying too early creates
a duplicate beside the real thing.
[Migrate an existing estate](https://intentius.io/choudoufu/migrate.html) has
the steps, which types adopt automatically, and which need a hand-written tag.

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

- [`live/MARKERS.md`](live/MARKERS.md) is the marker tag spec, the one
  integration surface external tooling relies on.
- [`live/LIMITATIONS.md`](live/LIMITATIONS.md) lists every construct the mode
  bounds or rejects, each with its lint rule and fixture.
- [`live/RECEIPTS.md`](live/RECEIPTS.md) covers receipts, the record that
  tracks staleness for an effect with nothing in the live system to read back.
- [`live/e2e/README.md`](live/e2e/README.md) documents the demo harness and
  how to read its output.

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
