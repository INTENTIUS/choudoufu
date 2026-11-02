# The e2e harnesses

This directory holds the gauntlet's estates (`live/gauntlet/estates.json`),
the fixtures the Go tests read, and a set of small purpose-built harnesses,
each on its own port so they can all run at once. `just demo-run <name>` runs
`live/e2e/<name>/run.sh`.

The 15-step legacy harness `live/e2e/run.sh` (and `just demo`, `just smoke
full`) was retired under #2288. Its steps are gauntlet stages or smoke
claims; its lint-rejects step is `internal/live/lint/limits_test.go` and its
receipt steps are `internal/live/lifecycle/receipt_existence_test.go`. The
site's tutorial now runs `just smoke roundtrip`.

| | Issue | Needs |
|---|---|---|
| `live/e2e/record-store/` | #73's record-backed lifecycle, the only end-to-end exercise that class has | neither Docker nor AWS — null, time and random are cloud-free |
| `live/e2e/dataread-projection/` | #193's read side: a data source resolved from a managed resource's own configured argument | Docker, AWS CLI, :4599 |
| `live/e2e/tagging-sweep/` | #255's estate-wide tagging sweep, the production candidate path | Docker, AWS CLI, :4601 |
| `live/e2e/create-over/` | a pinned defect: a tag-losing needs-discovery type creating what the estate already owns | Docker, AWS CLI, :4602 |
| `live/e2e/per-element/` | `Component.PerElement`: a set-valued identity tail, rendered sorted | Docker, AWS CLI, :4604 |
| `live/e2e/record-located/` | #270's crossing: an object with no marker, found again by the estate's record store | Docker, AWS CLI, :4605 |
| `live/e2e/repeated-module/` | #280's crossing: one local module called seven times, and the seven markers read off the live objects | Docker, AWS CLI, :4606, `.corpus` |
| `live/e2e/provisioner-taint/` | #353's crossing: a create-time provisioner that fails on a marker-tracked cloud resource, and the one bit that survives it | Docker, AWS CLI, :4742 |

All but `per-element`, `repeated-module` and `provisioner-taint` are
documented at the bottom of this file.

Everything here that needs Docker runs against floci. Before writing a
fixture, read `live/FLOCI.md`: four classes of question the emulator cannot
answer at any scale, so that a fixture is not built to measure one of them.
Value semantics is the one that has actually shipped a defect to main, twice.

## The record-store harness

`live/e2e/record-store/run.sh` is the other end of the fork: issue #73's
record-backed class, the types whose identity is not in the cloud at all but
in the estate's own `record_store`. The other harnesses here are about live
markers on cloud objects and declare no `record_store`, so without this
harness that whole class has no end-to-end exercise.

```
just demo-run run record-store
```

Under a minute, and it needs neither Docker nor the AWS CLI: `null_resource`,
`terraform_data`, `time_static` and `random_pet` come from cloud-free
providers, so the whole thing runs against a local directory. It builds a real
`choudoufu` from the checkout unless `TOFU_BIN` names one.

| Step | Proves |
|---|---|
| `init` / `apply` | A `record_store "local"` block in `terraform { live { ... } }` admits all four RECORD_ADMITTED types, which are a hard refusal without one, and a stock `apply` creates them through the ordinary provider lifecycle. This is the admission gate, end to end. |
| record layout | Exactly four records land under `tofu-records/<estate>/<type>/<hash>`, and guided discovery's hint under `tofu-hints/<estate>/guided` is *not* one of them. The two namespaces are deliberately disjoint (`RecordKeyPrefix` and `HintKey`, `internal/live/projection`) so that orphan discovery listing the record namespace can never mistake a hint for a resource. |
| clean re-plan | With no state file anywhere, prior state rebuilt from the records alone gives `No changes`. A hydration bug looks like a proposal to re-create everything. |
| replace | Changing a `triggers` input forces a replacement of exactly that resource; the two untouched records stay byte-identical and the replaced one changes. |
| destroy | Emptying the resource blocks and applying destroys all four and removes all four records. `choudoufu destroy` itself is refused under live markers, so removal-by-deletion is the tested path, the same as the gauntlet's day2_remove stage. |

What it does **not** cover, and the next thing worth adding: a cloud-backed
resource whose identity is derived from a record-backed parent's attribute
(`resolver.parentPart`'s record-backed branch, and the ordering in
`builder.run` that makes it work). That crosses from the record store to a
real cloud object, so it needs the floci harness above rather than this one.
The unit-level proof of both halves is
`internal/live/identity/recordbackedattr_test.go` and
`internal/live/projection/recordparent_test.go`.

## The data-read projection harness

`live/e2e/dataread-projection/run.sh` is issue #193's read side: a data
source whose argument reads a managed resource attribute the resource's own
block **sets**, resolved before the plan and read against a real emulator.

```
just demo-run run dataread-projection
```

It needs Docker and the AWS CLI, and runs on port 4599 rather than 4566 so
it can run beside the other harnesses. Under a minute after the image is pulled.

The offline half of that mechanism — deciding *whether* an argument is
projectable — is tested offline in `internal/live/dataread`, and has to be:
`tools/corpus-gen` runs the same classification over 250 third-party
configurations with no AWS account behind them, and that is the instrument
the whole language-wall measurement rests on. The read half cannot be tested
that way. It exists to turn a projected argument into a real provider call,
and nothing offline shows that the call went out with the right argument and
came back with the live answer.

The fixture is arranged so a static shortcut cannot pass it. Phase 1 applies
an SSM parameter whose value the configuration states; the script then
overwrites that value **out of band** through the AWS CLI, so from that point
the configuration and the cloud disagree. Phase 2 names a CloudWatch log
group after the parameter's value *as read back from the cloud*. A run that
resolved the value from configuration would name the log group after the
phase-1 string, and step 4 fails on exactly that.

| Step | Proves |
|---|---|
| phase 1 | An `apply` under a `live` block creates the seed parameter. Floci drops an SSM parameter's tag set (floci-gaps #10), so the ownership markers go on by hand with `aws ssm add-tags-to-resource` — adoption exactly as the docs describe it — *after* the out-of-band overwrite, which drops them again. |
| overwrite | The parameter reads back as the live value through the AWS CLI, not through `choudoufu`. This is the setup that gives the next step its teeth. |
| phase 2 plan | The plan resolves `aws_ssm_parameter.seed.name` from the block that sets it, reads the data source with it, and names the log group after the value the cloud returned. Both directions are asserted: the live name must be present and the configured name must be absent. |
| apply + read back | The log group exists on the emulator under the live-derived name, read with the AWS CLI rather than from `choudoufu`'s own output. |
| clean re-plan | With the state file deleted, a second run redoes the whole projection and read — values are never cached — and proposes nothing for the derived log group. |
| unset attribute | `aws_ssm_parameter.seed.arn` is assigned by the provider and appears nowhere in the block, so it must still refuse, naming the managed resource, with no raw HCL "does not have an attribute named" error leaking out. |

The harness fails on a build without the read side, which is what makes it a
test rather than a demonstration: with the managed branch removed from
`reader.lookupFor`, step 4 refuses with *"Data source not readable before
resolution … Unable to use aws_ssm_parameter.seed in static context"*.

## The tagging-sweep harness

`live/e2e/tagging-sweep/run.sh` is issue #255: a full-estate `live-plan`
gathering its removal candidates from **one** Resource Groups Tagging API
call, through the command wiring rather than a hand-built
`discovery.Request`.

```
just demo-run tagging-sweep
```

It needs Docker and the AWS CLI, and runs on port 4601 rather than 4566 or
4599 so all three harnesses can run at once. Well under a minute after the
image is pulled.

Why it did not exist before. `internal/command/live_plan.go` turned
`TaggingSweep` off for any loopback endpoint, on the strength of a real gap
in an older emulator pin — and loopback is what the e2e harnesses and
`internal/live/flocitest.Endpoint` both use. So the only configuration that
could have covered the branch was the one configuration excluded from it. The
emulator gap was fixed (`lex00/floci#229`) and the pin moved; the gate
stayed, with a test asserting its source line. That gate is gone, and what
decides the question now is
`internal/command/tagging_sweep_premise_test.go`, which reads
`live/floci-capabilities.json`'s `tagging-sweep` rows for whatever digest
`live/floci-image` pins.

The fixture declares two IAM roles and then deletes one block. Nothing on
disk names the deleted role and there is no state file, so the estate-wide
sweep is the only thing that can find it.

| Step | Proves |
|---|---|
| apply | Both roles exist, read back with the AWS CLI rather than from `choudoufu`'s output, and `resourcegroupstaggingapi get-resources` filtered to the estate holds the demo role's ARN — visibility to the tagging API specifically, which is a different question from visibility to `iam:ListRoleTags`. |
| run A | With the block deleted, `live-plan` proposes exactly one destroy, names the live role, leaves the still-declared role alone, and its debug log shows the sweep going through the Tagging API. |
| run B | The same run under `TOFU_LIVE_CLOUDCONTROL=off` — the documented lever that skips the whole Cloud Control/tagging block — must **not** print that line, which is what keeps run A's assertion from being vacuous, and must still list `aws_iam_role` per type, so it is a control for the candidate source rather than for the sweep's existence. |
| cost | Both wall clocks are printed and neither is asserted. One `GetResources` is not measurably faster than the per-type sweep against a local emulator; a threshold here would be flaky and a quoted speedup would be a number nobody measured. |

Run B also records a finding the fixture was not built to look for, and it is
asserted rather than left as a note. The per-type sweep does not propose the
removal **at all**, and the reason is not the emulator: the AWS provider's
`aws_iam_role` list resource (6.58.0) builds its objects from `iam:ListRoles`
and issues no `GetRole` per member — zero `GetRole` requests reach the wire
during the `ListResource` call — and `iam:ListRoles` returns no tags, on
floci and on real AWS alike. A listed role therefore carries an empty tag
map, no ownership marker can be read off it, and the tagging sweep is the
only path that detects an undeclared `aws_iam_role`. The gate was not costing
the emulator tier its speed; it was costing it the removal. If a provider
release changes that, run B starts proposing the destroy and fails with a
message saying so.

## The create-over harness

`live/e2e/create-over/run.sh` pins a defect rather than a feature. Exit 0
means the defect is still present; when it goes red, the fix has landed and
the script names the assertions to invert.

```
just demo-run create-over
```

Docker and the AWS CLI, port 4602, about a minute after the image is pulled.

It is the other end of the tagging-sweep finding above. That one is about a
resource whose block was deleted and which no run proposes destroying — a
missing destroy. This one is about a resource whose block is still there: a
`ClassNeedsDiscovery` instance of a tag-losing type cannot be found by its
marker, so the plan proposes **creating** what the estate already owns.

The fixture declares two resources that differ in exactly one property.
Both are needs-discovery — `internal/live/check/testdata/identity-golden.txt`
records both as `NEEDS_DISCOVERY` with no import identity — so both depend
entirely on reading an ownership marker off a listed object.

| | List path | Outcome |
|---|---|---|
| `aws_vpc.control` | `ec2:DescribeVpcs` returns the object's `TagSet` | marker read, instance bound, nothing proposed |
| `aws_iam_role.subject` | `iam:ListRoles` returns no tags and the provider issues no `GetRole` per member | marker unreadable, instance unbound, creation proposed |

The control is what makes the run a measurement. Both mutations have been
checked: stripping the VPC's `tofu-estate` tag out of band fails step 4a, and
replacing the role's `name_prefix` with a static `name` fails step 4b with the
message that says the defect is fixed.

| Step | Proves |
|---|---|
| apply | One VPC and one role exist, both carrying this estate's markers, read back with the AWS CLI. `get-resources` filtered to the estate holds **both** ARNs with their tags — the answer the run needs is on the wire in a call it already makes. |
| live-plan | The control binds. The subject is proposed for creation, and the run prints the live role under *"Foreign resources … not owned by estate"* with `tags: (none)` — the object is on screen, named, and described as somebody else's. |
| apply | The proposal is not refused. A second role is created, and two live roles now carry `tofu-address = aws_iam_role.subject` — the collision condition `live/MARKERS.md` describes, written into the cloud by this tool. |
| re-plan | A third would be created. Neither existing role's marker is readable, so the defect is one new resource per run rather than a one-off double. |

For a type whose name is in the configuration the same defect surfaces as an
`AlreadyExists` error on apply. The fixture uses `name_prefix` deliberately,
because the silent form is the one worth pinning.

## The record-located harness

`live/e2e/record-located/run.sh` is issue #270's crossing:
`identity.ClassRecordLocated` against a real emulator, having never touched a
cloud before.

```
just demo-run record-located
```

Docker and the AWS CLI, port 4605, about two minutes after the image is
pulled.

A located resource has nowhere to carry an ownership marker and an identity
the provider minted at create time. Neither the configuration nor a tag can
say which live object it is, so the estate's record store says instead,
under its own namespace root `tofu-located`. The claim being crossed is that
this survives the state file being deleted — and that it names the *right*
object, which is a stronger claim than an empty plan can make.

The fixture is four resources and each has a job:

| | Identity | Why it is here |
|---|---|---|
| `aws_cloudfront_public_key.signers` (×2) | server-minted, opaque | The measurement. The ids appear nowhere in `main.tf`, so only the record can supply them, and there are two so a swap is possible. |
| `aws_ecr_registry_policy.registry` | the registry id, an account singleton | The located path must not depend on the identity being opaque. |
| `aws_vpc.control` | server-assigned, taggable | The only needs-discovery type present, so the *"Foreign resources"* line reports a sweep that ran. Without it the run sweeps nothing and every negative claim below is vacuous. |

The rendered identity is checked against **the emulator**, not against the
record. Asking whether the run agrees with the record it read is asking the
mechanism to grade itself; a record pointing at the wrong key would agree
with itself perfectly, both ids are well-formed, and either imports cleanly.
So step 3 asks CloudFront which id carries the name `rl-e2e-alpha` and step 7
requires the run to have rendered that id.

| Step | Proves |
|---|---|
| apply | Four resources exist. Three located records are written under `tofu-located/`, **zero** files under `tofu-records/` — the namespace orphan discovery enumerates, which for a cloud-backed object would turn a stale key into a deletion. |
| state deleted, `live-plan` | Empty plan, sweep completed, nothing foreign. |
| the rendered identities | Each located instance bound to the id the emulator names for it. |
| the deliberate break | One record pointed at the other key's object, `address` field left correct so `LocatedStore.Get`'s cross-check still passes. The run renders the wrong id, so step 7 would have failed. |
| apply again | 0 added, 0 destroyed, still two live keys, records unchanged. |
| record deleted | One create proposed, zero destroys, nothing classified *"Owned and undeclared"*, and the live key still there. Applying it makes a **third** key and leaves the first alone: an announced duplicate, never a silent deletion. |

Both halves of the value assertion were mutation-checked. Making step 7
expect the wrong id fails the run **with the plan still empty**, which is the
entire reason the value is asserted rather than the verdict; neutering step
8's mutation fails the run at step 8, so step 8 cannot pass by not having
broken anything.

Two things the run observes and does not endorse.

With one record pointed at the other key's object, **two instances resolve to
one live identity and nothing refuses**. The plan reads as an ordinary
replacement of the shared object, which would destroy the object the other
instance owns. Step 8 pins that as an observation; if it goes red a refusal
has been added, and the paragraph beside it says so.

And `materializeLocated`'s absent-record message ends *"it is reported as
unclaimed rather than destroyed"*. Destroyed is what must not happen and does
not. Unclaimed is not delivered: no sweep enumerates a markerless type, so
nothing can report one as unclaimed. The run says as much itself, naming
`aws_cloudfront_public_key` and `aws_ecr_registry_policy` under
`[NOT_SCANNED]`, and step 10 asserts that section rather than leaving the
negative to stand as silence.

### What floci gets wrong

CloudFront and ECR both serve this fixture natively — `CreatePublicKey`,
`GetPublicKey`, `DeletePublicKey`, `PutRegistryPolicy`, `GetRegistryPolicy`
and `DeleteRegistryPolicy` all work, and the key format is really validated
(a truncated PEM is rejected). Two divergences:

- `list-public-keys` returns every item with `Name: None`; the name is only
  visible through `get-public-key` on each id. The harness reads it the
  second way for that reason.
- floci mints a UUID for a public key id where real CloudFront mints a
  `K…`-form id. Server-assigned either way, which is all the located path
  depends on, but a fixture that pattern-matched the id would pass here and
  fail against AWS.

`live/floci-capabilities.json`'s `cloudcontrol-list` evidence does **not**
imply the native API works. It records that Cloud Control's `ListResources`
answered for the CFN type, which LocalStack answers generically.
`aws_cloudwatch_query_definition` and `aws_athena_named_query` are both
recorded `implemented` on that evidence and both return `UnsupportedOperation`
from the API the AWS provider actually calls.

## The shared plugin cache

Every `live/e2e/corpus-*/run.sh` and `reference-*/run.sh` script builds a
scratch estate in its own `mktemp` directory and runs `init` against it at
least twice — a cold-deploy copy and a migrated/adopted copy, sometimes more.
Left alone, each of those directories re-downloads the whole provider (the
AWS provider is several hundred megabytes) on every `init`, which on a
machine running more than one crossing at a time is most of the script's
wall time.

The fix is one call, right after the script sources the library (#1300):

```sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/lib/gauntlet.sh"
gauntlet_plugin_cache
```

`gauntlet_plugin_cache` exports the pair of env vars below and creates the
directory. It is the only place a crossing script chooses a cache, and
`tools/gauntlet`'s `TestEveryScriptTakesItsPluginCacheFromTheLibrary` fails
on a script that sets `TF_PLUGIN_CACHE_DIR` for itself — so the concurrency
policy in the next subsection cannot be opted out of by copy-paste. Before
#1300 there were two spellings across twelve scripts and neither was
reachable from the other: eight shared the user-global directory, four
Kubernetes scripts and `terralith-scale` used a private `$WORK/plugin-cache`
copy that git history shows was copied from `reference-k8s` rather than
chosen.

```sh
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
mkdir -p "$TF_PLUGIN_CACHE_DIR"
```

`TF_PLUGIN_CACHE_DIR` alone is not enough (#339). It makes `init` reuse an
already-downloaded provider binary, but the cache directory records no
checksums, and a directory with no `.terraform.lock.hcl` of its own
re-downloads and re-verifies the whole package purely to compute them —
even when the exact version is already sitting in the cache. The install log
admits it in one breath: `Installing hashicorp/aws v6.x.x to the shared
cache directory...` immediately followed by `Using hashicorp/aws v6.x.x from
the shared cache directory`. #339's own measurement, on the machine and
network it was filed from: 320s per redundant `init`. Re-measured at
`56481a4bbf` with this fix applied and pulled back out again (a throwaway
instrumented copy of `corpus-giantswarm-crossplane/run.sh`, not committed),
same estate, same warm cache, this machine and network: stage 1's `tofu
init` 6s, stage 2's `choudoufu init` 17s, full five-stage run 104.84s
wall-clock. The absolute seconds vary with network conditions between the
two measurements; the signature (`Installing ... Using ...` in the same
breath, on a cache that already had the version) is what's diagnostic, and
it reproduced.

`TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE` is the CLI's own
accommodation for exactly this
(`internal/command/cliconfig/cliconfig.go`'s
`PluginCacheMayBreakDependencyLockFile`, plumbed through to the provider
installer's `allowSkippingInstallWithoutHashes`). With it set, an `init`
that finds the package already in the global cache trusts it outright
instead of re-fetching and re-verifying it, and records only the current
platform's checksum in the lock file it writes. Same instrumented
`corpus-giantswarm-crossplane` run, same machine, this fix applied: stage 1
`tofu init` 1s, stage 2 `choudoufu init` 2s, full five-stage run 55.87s —
roughly half the unmitigated run's wall time, and both inits dropped, not
only the second one a hand-seeded lock file would have reached. A second,
isolated check against a plain scratch directory (not this estate) requiring
`hashicorp/aws` 6.61.0 against the same warm cache: `choudoufu` 11.30s →
1.45s. The `terraform`-binary equivalent is below.

That trade — the lock file this produces carries only one platform's
checksum, so it is not portable to a different OS/architecture — costs
nothing here: every directory in these scripts is a throwaway `mktemp` copy,
never committed, never read by anything running on a second platform. It is
a real trade for a project's real `.terraform.lock.hcl`, which is why this
is not the default and has to be opted into.

**It is not OpenTofu-only.** Real HashiCorp `terraform` honors the same env
var (confirmed against Terraform v1.15.8: 8.30s without it, 0.59s with it,
same warm cache, same provider version) — so it fixes *both* sides of a
script that cold-deploys with real `terraform` and migrates with
`choudoufu`/`tofu`, unlike a copied `.terraform.lock.hcl`, which cannot cross
the registry boundary (`corpus-hongbomiao-labelbox` runs `terraform init`
against `registry.terraform.io` and `tofu init` against
`registry.opentofu.org` — a lock file from one names the wrong provider
source for the other). The env var needs no such coordination: each `init`
consults the shared cache under its own registry-keyed path independently,
so it is safe to export unconditionally near the top of a script regardless
of which binaries that script goes on to run.

The one case this does not cover: `corpus-simpleinfra-dns` uses the shared
cache directory as a `-plugin-dir` filesystem *mirror* instead, for reasons
specific to that script (see its own header) — a mirror is already
authoritative, so `init` never re-verifies at all (measured there at
0.35s/0.48s). That is a different, also-valid technique for the same
problem; it is not what most scripts here need. It takes the directory from
`gauntlet_plugin_cache_dir`, which returns the path without exporting
`TF_PLUGIN_CACHE_DIR` — exporting it there would re-admit the writer that
script is deliberately avoiding — and it still wraps its inits in
`gauntlet_locked_init`, because a reader of a directory another process is
unpacking into sees the same torn file a second writer would.

**Still true regardless:** this removes the *redundant* download, not the
*first* one. A directory that is the very first `init` anywhere against a
cold cache still pays the real download cost once — there is no way around
fetching a provider nobody's machine has yet.

### Sharing it safely: `gauntlet_locked_init`

HashiCorp documents the cache as "not guaranteed to be concurrency safe…
the provider installer's behavior in environments with multiple `terraform
init` calls is undefined". Measured here, that applies to exactly one of the
two binaries these scripts run.

`choudoufu`/`tofu` already serialize themselves. `internal/providercache`'s
`Dir.InstallPackage` takes a per-`(provider, version, platform)` flock on
`<version>/<platform>.lock` before it unpacks anything, so two `tofu init`s
sharing a cache cannot interleave. It is visible from the outside: a cold
`tofu init` leaves that `.lock` file behind in the cache, a cold `terraform
init` leaves none.

Real `terraform` (checked against v1.15.8) takes no such lock, and unpacks
**straight into the final cache path**. Polling the cache directory during a
cold init shows the provider binary appear under its final name while it is
still being written — no temp directory, no atomic rename. A second `init`
that finds that path can link a half-written binary.

So every real-`terraform` init in a script that shares the cache goes
through `gauntlet_locked_init`, and `tofu`/`choudoufu` inits are left bare
because the installer in this tree already does the same job:

```sh
( cd "$EST" && gauntlet_locked_init terraform init -input=false -no-color )
```

The lockfile is `.choudoufu-init.lock` inside the cache directory, taken with
`O_EXCL` — the same name and protocol `internal/live/flocitest` uses, so an
estate script and a `go test ./internal/live/...` sharing a machine exclude
each other too. A holder older than ten minutes is treated as crashed and
broken; a waiter gives up after fifteen. `tools/gauntlet`'s
`TestSharedPluginCacheInitsAreLocked` fails on an unwrapped one.

Serializing costs almost nothing, because a warm init does not write at all:
measured, a second `init` for a version already in the cache leaves every
byte and inode unchanged, and takes 0.59s (terraform) or 1–2s (choudoufu).
Cold, serializing is the point — the first caller pays for the download and
everyone behind it gets a warm read.

**What was not shown.** Corruption was not reproduced: 72 concurrent cold
`terraform init` calls (12-way, six rounds) into one empty cache, with every
resulting file diffed against a serially-built sha256 reference, came back
identical every time. The reference oracle was proven red by truncating a
cached binary. An earlier attempt that checked binaries with `file | grep
Mach-O` was blind to a 1000-byte truncation and proved nothing. The lock
rests on the mechanism above — an in-place unpack with no rename — not on a
reproduction.

## Floci containers: ownership and cleanup

Every harness here starts its emulator through
`gauntlet_floci_start <name> <docker run args...>` in `lib/gauntlet.sh`,
never with its own `docker run -d` (`live/flocipostmortem_test.go` fails a
script that does). The helper adds `-d`, never `--rm` (#1299: a container
that dies mid-run must stay inspectable), and writes three labels:

| label | value |
|---|---|
| `choudoufu.estate` | the `<estate>` in `choudoufu-<estate>-<pid>` |
| `choudoufu.owner.pid` | `$$` of the script that started it |
| `choudoufu.owner.started` | that pid's start time, `TZ=UTC ps -o lstart=`, whitespace collapsed |

The labels exist because of #1312. A script SIGKILLed mid-run leaves its
container running: the EXIT trap cannot fire on SIGKILL, and `--rm` never
covered it either, since it removes a container when the *container* exits
rather than when the script does. The name does not collide, so the
containers accumulate, each holding its published port until an unrelated
run of the same estate fails its health check on it. The stopped half of
that residue was always safe to sweep by filter; the running half was not,
because two runs of the same estate can coexist on different ports and from
outside a leaked container and a concurrent run's are identical. The labels
are what tells them apart: a pid is the owner only if it is alive *and* its
start time is the one on the label. Pids are reused, and `kill -0` on a
reused pid says "alive" about a stranger, which is why the start time is
there.

`gauntlet_sweep_leaked_floci [estate]` decides one container at a time
(`gauntlet_floci_ownership` is the single decision, shared with
`scripts/pickup.sh`) and prints one `FLOCI-SWEEP` line per container saying
what it did and why. It removes a running container whose owner is dead or
whose pid has been reused, and a stopped container nobody alive owns, after
`gauntlet_floci_teardown` has printed that container's postmortem. It never
removes a running container with no ownership labels (a run from before the
labels, or something started by hand); it lists it with the `docker rm -f`
to run once you have looked.

Three things run it:

- `gauntlet_floci_start` itself, scoped to its own estate, before every
  `docker run`. A leak clears itself on the next run of the same estate, and
  a concurrent run of that estate on another port is kept because its owner
  is alive. Read the `FLOCI-SWEEP` lines at the top of an estate log to see
  what a run found waiting for it.
- `bash scripts/floci-sweep.sh [estate]`, the whole machine by default.
- `bash scripts/pickup.sh`, read-only: its "crossing floci containers"
  section prints the same verdicts, `LEAKED: owner pid N is gone` and so on,
  without acting on them.

`live/e2e/selftest-floci-sweep.sh` proves the sweeper against real
throwaway containers (a dead owner, a reused pid, a live owner, no labels,
an exited one, another estate's) and re-runs its first case against a copy
of the library with the liveness check removed, which must fail.
`live/flocisweep_test.go` runs it; `make test-floci` is where CI does.

If the sweeper itself cannot be used (a machine without this tree, say), the
recipe from before it is still correct for the stopped half and still unsafe
for the running half:

```
docker ps -aq --filter name=choudoufu- --filter status=exited | xargs docker rm
docker ps -q  --filter name=choudoufu-                        # inspect before killing these
```
