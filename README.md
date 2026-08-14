# choudoufu

[![Go Reference](https://pkg.go.dev/badge/github.com/intentius/choudoufu.svg)](https://pkg.go.dev/github.com/intentius/choudoufu)

**Ownership on the resource.** <img src="docs/images/choudoufu-inline-64.png" width="32" height="32" alt="">

Built on OpenTofu (fork point
[`03743ce6e8`](https://github.com/opentofu/opentofu/commit/03743ce6e8)). The
exact upstream version this tree is built on lives in
[`version/VERSION`](version/VERSION), and each
[release](https://github.com/INTENTIUS/choudoufu/releases)'s notes name both
versions.

Choudoufu is OpenTofu with live resource markers. There is no state file, no
backend and no lock. Each resource carries its own ownership record as plain
tags, and every plan rebuilds prior state by reading those markers off the
live system. It is experimental and AWS only at the moment. Everything else
is stock OpenTofu. The binary is `choudoufu`.

If you already use OpenTofu, the short version is that `terraform.tfstate`
stops existing. Adoption is a tag you write. A rename is a tag you rewrite.

New here? The docs site at
[intentius.io/choudoufu](https://intentius.io/choudoufu/) has the two paths:
migrating an estate you already have, or starting a new one. Read
[Will my config work?](https://intentius.io/choudoufu/compatibility.html)
first if you are evaluating.

## Where this stands

Live markers are experimental, and the scope is deliberately narrow. The mode
covers AWS only and a fixed subset of resource types. Static module trees and
statically-keyed `for_each` modules are covered too; a `count`-expanded
module block is refused permanently. Configs outside that subset are refused
up front by a lint pass rather than half supported. The full boundary, with
the reasoning for each limit, is in
[`live/LIMITATIONS.md`](live/LIMITATIONS.md).

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

(use `*_windows_arm64.zip` on ARM64 Windows). Building from source stays one
command (below).

## Moving an existing estate over

A greenfield estate is two steps: declare a `live` block and apply. An
estate that already has live resources is not, and the difference matters
before you try this on anything you care about. Adoption is a deliberate
tag write, and nothing binds a live resource to your configuration until
its markers are on it.

1. Add `live { estate = "..." }` to your `terraform` block, remove any
   `backend` or `cloud` block, and delete the state file.
2. Run `choudoufu plan` and read it. Live resources carrying no markers
   appear in the plan's `Adoptable` and `Unowned` sections, each naming the
   exact tags that claim it.
3. Write those tags, using the command the plan prints or your own tooling.
   There is no `adopt` command; two tags is the whole contract
   ([`live/MARKERS.md`](live/MARKERS.md)).
4. Plan again. Adopted resources read back their own markers and report no
   changes.
5. Apply once the plan is what you expect.

Applying at step 2 rather than reading is how you get duplicates: an
unmarked resource is not yours yet, so the plan proposes creating a second
one beside it. Which types can be offered automatically, which need a
hand-written tag, and which have no adoption path at all are covered in
["Migrate an existing estate"](https://intentius.io/choudoufu/migrate.html).

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

## Building and testing

```
go build ./cmd/choudoufu
go test ./...
```

The integration tier needs Docker and `TF_FLOCI_TEST=1`.

## Docs

The docs unique to this fork, in reading order.

The two user paths, plus the compatibility answer, live on the docs site at
https://intentius.io/choudoufu/. The repository carries the normative specs
and the contributor material.

- [`live/MARKERS.md`](live/MARKERS.md) is the marker tag spec, the
  one integration surface external tooling relies on.
- [`live/LIMITATIONS.md`](live/LIMITATIONS.md) lists every
  construct the mode bounds or rejects, each with its lint rule and fixture.
- [`live/RECEIPTS.md`](live/RECEIPTS.md) shows how to record an
  effect that leaves nothing in the live system to read back.
- [`live/e2e/README.md`](live/e2e/README.md) documents the
  demo/test harness and how to read its output.

These also render as a small docs site at
https://intentius.io/choudoufu/. All stock OpenTofu documentation lives at
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
