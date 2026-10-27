# choudoufu

[![Go Reference](https://pkg.go.dev/badge/github.com/intentius/choudoufu.svg)](https://pkg.go.dev/github.com/intentius/choudoufu)

**OpenTofu that respects the owner tag on each resource.** <img src="site/public/brand/cube-160.png" width="32" height="32" alt="">

Each resource carries its identity as a marker the platform can select on.
On AWS that is two resource tags. The apply writes the marker and the next
plan reads it back live. The state file becomes a cache you may lose. The
policy engine you already run decides who may read or change what.

It runs on four platforms:

- [AWS](https://intentius.io/choudoufu/aws/): two tags, scoped by IAM tag conditions.
- [Kubernetes](https://intentius.io/choudoufu/kubernetes/): one label, fenced by one admission policy.
- [GCP](https://intentius.io/choudoufu/gcp/): two labels, one project per estate.
- [Azure](https://intentius.io/choudoufu/azure/): two tags, resource groups and Azure Policy.

Three things persist between runs. A tag on the resource says which real
resource an address refers to. Values with no home in AWS go in a
`record_store`: an S3 bucket you own, or a local directory. Effects with
nothing to read back get a receipt that tracks their staleness.

AWS already scopes IAM by tag. choudoufu derives each marker from the
configuration address and writes it in the create call, so every resource
that exists carries one. Your IAM is the whole permission model. Concurrent
runs settle at the API, with no lock table. Whoever inherits an estate can
list it with any cloud tool before running the binary.

Handover is granting a role. Adoption and renames are tag writes.

The name means stinky tofu, an acquired taste that suits a fork whose state
may go stale.

Built on OpenTofu v1.13.0 (fork point
[`2b6193043d`](https://github.com/opentofu/opentofu/commit/2b6193043d)). The
exact upstream version lives in [`version/VERSION`](version/VERSION), and
each [release](https://github.com/INTENTIUS/choudoufu/releases)'s notes name
both. Live markers are the only change from stock OpenTofu.

## Where this stands

Most commonly used AWS resource types are admitted. `aws_ecs_service` is
proven end to end (deploy, migrate, drift, rename, remove) on a real
ECS/Fargate estate. `aws_lambda_permission` takes no tags, so it is
[declaration-carried](https://intentius.io/choudoufu/docs/use/resource-tiers/):
its identity is recomputed from the configuration on every run.
[The gauntlet](https://intentius.io/choudoufu/docs/progress/) records which
estates clear which stages against a real emulator.

Constructs such as a `backend "s3"` block stop a configuration more often
than types do. A saved plan file (`-out` plus `apply <planfile>`) works:
the apply re-reads the live system and refuses by name if it differs from
the approved file ([#878](https://github.com/INTENTIUS/choudoufu/issues/878)).

For a verdict on your own code, run this in your configuration directory.
It runs offline.

```
choudoufu live-check
```

[Will my config work?](https://intentius.io/choudoufu/docs/use/compatibility/)
covers the same ground. [`live/LIMITATIONS.md`](live/LIMITATIONS.md) lists
every limit with its reasoning.

## See it prove itself

The smoke stack is the demo and the test suite. One `docker compose` stack
runs the pinned emulator and a stock OpenTofu oracle. Each step prints a
verdict line, and the run exits 0 when every claim held. A scenario needs
Docker and takes about two minutes.

```
just smoke roundtrip    # stock estate -> live-import -> no state file -> back to stock
just smoke             # lists every scenario and the claim it proves
```

Or paste this to a coding agent:

```
Clone https://github.com/INTENTIUS/choudoufu, then do the following.

1. Confirm Docker is running (`docker info` must succeed) and the AWS CLI
   is installed (`aws --version`).
2. If Go is installed, skip this step. Otherwise pick the latest release
   tag from https://github.com/INTENTIUS/choudoufu/releases and
   export CHOUDOUFU_VERSION=<that tag> so the smoke runs a prebuilt binary.
3. From the repo root, run: just smoke roundtrip
4. Report each step's verdict line as it prints, and the final PASS or
   FAIL line.
```

`BREAK=1` corrupts one expected fact mid-scenario, and the scenario passes
only by catching it. [`live/smoke/README.md`](live/smoke/README.md) has
every knob.

## Install

Every tagged release publishes binaries for macOS, Linux and Windows (amd64
and arm64) with a `SHA256SUMS` file on the
[releases page](https://github.com/INTENTIUS/choudoufu/releases). This
fetches the latest on macOS or Linux.

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz   # unpacks ./choudoufu
```

On Windows (use `*_windows_arm64.zip` on ARM64):

```powershell
gh release download -R INTENTIUS/choudoufu --pattern "*_windows_amd64.zip"
Expand-Archive choudoufu_*_windows_amd64.zip .   # unpacks .\choudoufu.exe
```

## Which path is yours

If AWS already holds resources your configuration manages, read
[Migrate an existing estate](https://intentius.io/choudoufu/docs/use/migrate/).
Write the markers before the first apply; an early apply creates a duplicate
beside the real resource.

For a new estate, read
[Start a new estate](https://intentius.io/choudoufu/docs/use/start/). It is a
`live` block and an apply.

## Building and testing

```
go build ./cmd/choudoufu
go test ./...
```

The integration tier needs Docker and `TF_FLOCI_TEST=1`.

## Docs

The user guides live at https://intentius.io/choudoufu/. The repository
holds the specs: [`live/MARKERS.md`](live/MARKERS.md) for marker tags,
[`live/LIMITATIONS.md`](live/LIMITATIONS.md) for every bounded construct,
[`live/RECEIPTS.md`](live/RECEIPTS.md) for receipts, and
[`live/e2e/README.md`](live/e2e/README.md) for the demo harness.

Stock OpenTofu documentation lives at [opentofu.org](https://opentofu.org/docs/).

<p align="center">
  <img src="site/public/brand/cube-160.png" width="160" alt="a pixel-art block of dark fried tofu">
</p>

## License

choudoufu is licensed MPL-2.0, forked from
[opentofu/opentofu](https://github.com/opentofu/opentofu) at `2b6193043d`.
LICENSE and all copyright headers match upstream.

choudoufu is not affiliated with or endorsed by OpenTofu or the Linux
Foundation. OpenTofu is a registered trademark of the Linux Foundation.
