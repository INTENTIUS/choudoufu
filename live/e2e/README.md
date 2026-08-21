# The e2e harness

`live/e2e/run.sh` is both the end-to-end test for live resource
markers and the feature's live demo
(`site/content/start.md`, "See it prove itself"). This README
covers running it in one command, reading its output as a human or a
machine, and what the branch's claim means as a single exit code.

There are smaller harnesses beside it, each on its own port so they can all
run at once.

| | Issue | Needs |
|---|---|---|
| `live/e2e/record-store/` | #73's record-backed lifecycle, the only end-to-end exercise that class has | neither Docker nor AWS — null, time and random are cloud-free |
| `live/e2e/dataread-projection/` | #193's read side: a data source resolved from a managed resource's own configured argument | Docker, AWS CLI, :4599 |
| `live/e2e/tagging-sweep/` | #255's estate-wide tagging sweep, the production candidate path | Docker, AWS CLI, :4601 |
| `live/e2e/create-over/` | a pinned defect: a tag-losing needs-discovery type creating what the estate already owns | Docker, AWS CLI, :4602 |
| `live/e2e/per-element/` | `Component.PerElement`: a set-valued identity tail, rendered sorted | Docker, AWS CLI, :4604 |
| `live/e2e/record-located/` | #270's crossing: an object with no marker, found again by the estate's record store | Docker, AWS CLI, :4605 |
| `live/e2e/repeated-module/` | #280's crossing: one local module called seven times, and the seven markers read off the live objects | Docker, AWS CLI, :4606, `.corpus` |

All but `per-element` and `repeated-module` are documented at the bottom of
this file.

## Quickstart

You need four things.

- Docker, running (`docker info` must succeed). The harness stands up
  Floci (`floci/floci:latest`), a local AWS emulator, on a free port.
- The AWS CLI (`aws`) on `PATH`. It is used directly, alongside
  `choudoufu`, to make assertions no `choudoufu` command should be trusted
  to grade itself on, such as reading a tag straight off a live resource.
- Go, or a prebuilt `choudoufu` binary via `TOFU_BIN` (see below). Without
  `TOFU_BIN`, the harness runs `go build ./cmd/choudoufu` from this
  checkout itself, so it always exercises the code actually on the branch.

The one command, from the repository root.

```
bash live/e2e/run.sh
```

It takes about 2 minutes on a warm Go build cache (longer on a cold one,
since the harness builds `choudoufu` from this checkout unless `TOFU_BIN`
is set). It builds `choudoufu`, starts Floci, applies a real estate against
it with plain local state, deletes the state file in front of you, and then
proves each claim below live against the same running emulator. Nothing
here is mocked or simulated.

## What each step proves

Steps whose command surface isn't wired up yet in this build report `NOT
IMPLEMENTED (phase N)` instead of failing. See "Flags" below for the
`--expect` grouping. As of this branch every step is green, including
drift-exact, removal-exact, receipt-cycle, receipt-cycle-existence and
drift-reconverge (phase 5, P5.1's exactness work, gated additionally on
`LIVE_E2E_EXACTNESS`, see "Env knobs").

| Step | Proves |
|---|---|
| `standup` | Stock `choudoufu init`/`apply` builds a real estate on the emulator with plain local state, the ordinary baseline before any marker work happens. |
| `adopt` | Deleting `terraform.tfstate(.backup)` is the entire migration. Nothing else about the estate or its config changes. |
| `tofu-init` | The state-less work copy resolves providers via `choudoufu` (registry.opentofu.org), never `terraform`. |
| `slot-migration` | The pre-slot `aws_eip.pool` members take their stable `tofu-slot` markers with no create and no destroy, the one-time write an apply would perform. |
| `receipt-adoption` | Both SSM-parameter receipts (`demo_effect` the hash flavor, `demo_existence` the existence flavor, RA.6) take their ownership markers by hand with `aws ssm add-tags-to-resource`, adoption exactly as the docs describe it, a tag you write with your own cloud tools. The emulator drops the inline tags an SSM parameter is created with (floci-gaps #10), so without this both receipts are correctly unowned and every later step would be measuring against a standing diff instead of an owned resource. |
| `empty-plan-named` | `choudoufu live-plan -target=...` reports an empty plan for the client-named/attachment subset, rebuilt from markers alone, with no state file read or written. |
| `empty-plan-full` | The same, full-estate, no `-target`. Every declared instance materializes via discovery plus binding except the one the emulator will not serve tags for, which is reported as an `[UNOWNED]` omission with an adoption hint rather than silently adopted, and gathered in the plan's rendered `Unowned` section as `[ADOPTABLE]` with the exact tag values to write. |
| `drift-exact` | One out-of-band mutation per estate type (VPC, subnet, security group, EIP, bucket, log group tag, and log group retention as the non-tag case) surfaces as *exactly* that resource and that attribute with no broader neighbour noise, is corrected, and reconverges cleanly, all seven cases in one matrix. |
| `foreign-protected` | An unmarked security group created out of band is reported under a `Foreign resources` section and never proposed for deletion. |
| `removal-exact` | Deleting a whole resource block (`aws_security_group.main`) proposes *exactly* one destroy, named in an `Owned and undeclared` section alongside the live resource's ID. Applying it (via the AWS CLI, since no `live-apply` exists) removes exactly that resource and nothing else. |
| `count-scale-down` | Taking `count` from 3 to 2 on `aws_eip.pool` proposes exactly one destroy (the highest slot), zero churn on the survivors, and converges cleanly on re-plan. |
| `rename-no-churn` | `choudoufu live-mv <old> <new>` rewrites the live `tofu-address` tag in place. The following plan is empty. |
| `plain-plan-works` | A `live { estate = "..." }` block in `terraform{}` puts a plain `choudoufu plan`/`apply` on markers, with no `live-`-prefixed subcommand anywhere. `-out` and `refresh` are rejected by name. |
| `receipt-cycle` | The estate's receipt (`aws_ssm_parameter.demo_effect`, the HASH flavor, live/RECEIPTS.md) exists with a 64-char hash after standup. Breaking it out of band (a value overwrite) re-arms the plan's "effect will fire" signal on exactly that resource's value. A corrective write converges it back to clean. |
| `receipt-cycle-existence` | The estate's other receipt (`aws_ssm_parameter.demo_existence`, the EXISTENCE flavor, RA.6, live/RECEIPTS.md's default recommendation) exists with the constant value `"done"`. Breaking it out of band THE EXISTENCE WAY, a genuine `aws ssm delete-parameter` rather than an overwrite, re-arms the plan's "effect will fire" signal as exactly one create on that resource. Recreating it (playing the Op) converges it back to clean. |
| `drift-reconverge` | A phase-local copy of the standing estate, given its own `live { estate = "stateless-e2e" }` block, adopts the same live resources with no changes at all (no second standup). Three drifts of three different shapes injected out of band (a log group's retention, a plain tag beside the markers on the VPC, a whole CloudWatch alarm deleted) render on ONE plain `choudoufu plan` as exactly two in-place updates and one create, nothing else, and one untargeted apply reconverges all three in the same breath (1 added, 2 changed). drift-exact proves each shape alone under live-plan; this step proves them together under the plain-command path. (The observational-snapshot half this step used to carry — the `tofu-snapshots` git branch and its `git log`/`git diff` assertions — was removed with the subsystem, issue #109.) |
| `lint-rejects` | Every directory in `live/e2e/limits/` is refused by the shipped binary, naming exactly the lint rule that fixture exists for and no other. The one construct no lint rule catches yet (duplicate-identity, refused at identity resolution instead) is asserted as unenforced, so the gap is visible in the output rather than hidden by omission. |

Every step that mutates the shared live estate ($MAIN) restores it before
finishing. `count-scale-down` reallocates the EIP it released,
`removal-exact` recreates the security group it destroyed,
`rename-no-churn` rewrites the live tag back to the original address, and
`drift-reconverge` applies its own phase-local copy of the config to
reconverge the log group, VPC and alarm it drifted, so every later step
still sees the full estate its own config declares.

## Env knobs

| Variable | Default | Meaning |
|---|---|---|
| `FLOCI_PORT` | `4601` | Emulator port. Change if the default is taken on your machine. |
| `FLOCI_NAME` | `tofu-stateless-e2e-$$` | Container name. The default already includes the PID for uniqueness across parallel runs. |
| `TOFU_BIN` | (unset) | Path to a prebuilt `choudoufu` binary. Skips the `go build` step. Must be executable. |
| `LIVE_E2E_EXACTNESS` | `1` | Gates `drift-exact`/`removal-exact`/`receipt-cycle`/`receipt-cycle-existence` on top of their own `-estate`/`live-mv` probes. With the default `1` (since P5.2) all four run for real against P5.1's exactness work, which is merged and green. With `0` all four are forced back to `NOT IMPLEMENTED`, for a pre-exactness bisect or to reproduce what `--expect 4` verified before P5.1/P5.2 landed. |
| `LIVE_E2E_JSON` | `0` | Env-var spelling of `--json` (see below). `1` enables it. |

## Flags

- **`--json`** (or `LIVE_E2E_JSON=1`) emits exactly one JSON object
  as the final line written to stdout.

  ```
  {"steps":[{"name":"standup","status":"pass","phase":0}, ...],"overall":"pass"}
  ```

  `status` is one of `pass`, `not_implemented`, `fail`. `phase` is the
  `--expect` grouping below, not the roadmap task number embedded in a `NOT
  IMPLEMENTED (phase N)` human-readable line (those name which `P<N>.x`
  task unblocks a step, a different scale).

  `overall` is one of four values, independent of `--expect`.

  | `overall` | Meaning |
  |---|---|
  | `pass` | Every recorded step passed or legitimately reported `not_implemented`, and the harness reached its own end. |
  | `fail` | A claim was checked and found false. Some step's `status` is `fail`. |
  | `error` | The harness did not finish and no step reported a failure. A signal, a `set -e` abort, a crash. |
  | `skipped` | The run ended in `SKIP` (missing Docker, `aws` or `go`). Nothing was verified. |

  The last two exist because they used to be reported as `pass`. Every
  `RC=$?` guard after a `choudoufu` invocation was unreachable under `set
  -euo pipefail`, since the command substitution aborts the script first,
  so a genuinely broken run died with no diagnostic and this object still
  said the run had passed. `run_tf` in `run.sh` is what makes those guards
  live, and `error`/`skipped` are what the summary says when there is
  nothing to report a verdict on.

  A design decision worth knowing. With `--json` on, all the normal
  human-readable progress output (`=== N. step ===`, per-step detail
  lines, `FAIL`/`PASS`) stays on stdout exactly as it does without the
  flag. Nothing is redirected to stderr, and nothing about the run is
  quieter. The JSON object is appended as the truly last thing written to
  stdout, from an `EXIT` trap, so it comes out whether the run reaches its
  normal end or exits early via `fail()`. **Parse it by taking the last
  line of stdout.** Do not try to parse the whole stream as one JSON
  document.

- **`--expect <phase>`** exits `0` iff every recorded step at or below
  `<phase>` has status `pass` AND every step above `<phase>` reports
  `not_implemented`. A `fail` anywhere exits nonzero regardless of
  `--expect` (a step that already called `fail()` has already exited the
  script before `--expect` is ever evaluated). On a miss, `run.sh` names
  the blocking step(s), their phase, and their actual status on stderr.

  Phase assignment.

  | Phase | Steps |
  |---|---|
  | 0 | `standup`, `adopt`, `tofu-init`, `slot-migration`, `receipt-adoption` |
  | 1 | `empty-plan-named` |
  | 2 | `empty-plan-full`, `foreign-protected` |
  | 3 | `count-scale-down`, `rename-no-churn` |
  | 4 | `plain-plan-works` |
  | 5 | `drift-exact`, `removal-exact`, `receipt-cycle`, `receipt-cycle-existence`, `drift-reconverge`, `lint-rejects` |

## Exit-code meanings

| Exit code | Meaning |
|---|---|
| `0` | Every step ran and either passed or legitimately reported `NOT IMPLEMENTED`, or the run was cleanly `SKIP`ped with no `--expect` given, or `--expect <phase>` was given and its condition held. |
| `1` | A step's claim was checked and found false (`FAIL`), or the harness died before finishing, or `--expect <phase>` was given and did not hold. |
| `2` | An argument was invalid, or the run was `SKIP`ped while `--expect` was given. A skipped run verified nothing, so it must not report the expectation as met, distinct from both `0` (met) and `1` (checked and false). |

`SKIP` is reserved for missing tooling. A step that runs and finds its
claim false is always `FAIL`, never `SKIP`. The harness never lets a
broken claim through just because the environment happened to let it
start.

## The reproduction contract

`run.sh --expect <phase>` is how this branch states its own claim, in a
form any agent or human can check without reading the script. Run the
command, read the exit code. Today, that invocation is the following.

```
bash live/e2e/run.sh --expect 5
```

This passes. Every step through phase 5, including `drift-exact`,
`removal-exact`, `receipt-cycle`, `receipt-cycle-existence` and
`drift-reconverge`, is `pass`, and no step reports `not_implemented`.

```
bash live/e2e/run.sh --expect 4
```

This now FAILS, naming `drift-exact`, `removal-exact`, `receipt-cycle`,
`receipt-cycle-existence` and `drift-reconverge` as blockers. `--expect 4`
requires every step above phase 4 to be `not_implemented`, and all five are
`pass` now that P5.1's exactness work is merged and P5.2 wired the harness
steps and flipped `LIVE_E2E_EXACTNESS`'s default to `1`. It stays
meaningful as a pre-exactness bisect. Run it with `LIVE_E2E_EXACTNESS=0` to
reproduce the phase-4 claim this branch made before P5.1/P5.2 landed.

Nobody should need to read `run.sh` to know whether this branch backs up
what it says it does. The exit code of `--expect <current phase>` is the
verdict.

## The harness is also the demo

`live/e2e/run.sh` is the "Watch It Work" callout on
`site/content/start.md`'s "See it prove itself", the same command described
there in user-facing terms rather than test-harness terms. If you're
changing what a step proves, check that page's claims still match this
README's table above.

## The record-store harness

`live/e2e/record-store/run.sh` is the other end of the fork: issue #73's
record-backed class, the types whose identity is not in the cloud at all but
in the estate's own `record_store`. Everything above is about live markers on
cloud objects; nothing above declares a `record_store` at all, so without this
harness that whole class has no end-to-end exercise.

```
just demo-records
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
| destroy | Emptying the resource blocks and applying destroys all four and removes all four records. `choudoufu destroy` itself is refused under live markers, so removal-by-deletion is the tested path, the same as `live/e2e/run.sh`'s own removal steps. |

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
just demo-dataread
```

It needs Docker and the AWS CLI, and runs on port 4599 rather than 4566 so
it can run beside `just demo`. Under a minute after the image is pulled.

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
| phase 1 | A stateless `apply` under a `live` block creates the seed parameter. Floci drops an SSM parameter's tag set (floci-gaps #10), so the ownership markers go on by hand with `aws ssm add-tags-to-resource` — adoption exactly as the docs describe it — *after* the out-of-band overwrite, which drops them again. |
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
just demo-tagging-sweep
```

It needs Docker and the AWS CLI, and runs on port 4601 rather than 4566 or
4599 so all three harnesses can run at once. Well under a minute after the
image is pulled.

Why it did not exist before. `internal/command/live_plan.go` turned
`TaggingSweep` off for any loopback endpoint, on the strength of a real gap
in an older emulator pin — and loopback is what `live/e2e/run.sh` and
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
just demo-create-over
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
just demo-record-located
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

The fix is the pair of env vars below, exported together, near the top of
the script, before the first `init`:

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
problem; it is not what most scripts here need.

**Still true regardless:** this removes the *redundant* download, not the
*first* one. A directory that is the very first `init` anywhere against a
cold cache still pays the real download cost once — there is no way around
fetching a provider nobody's machine has yet.

## The corpus-crossing harness

`live/e2e/corpus-crossing/run.sh` runs somebody else's production
configuration. `.corpus/mastino/global/dns` is 63 instances of DataCite's
Route 53 estate, written for their own account and not for us; it has passed
`live-check` with zero refused sites for as long as the corpus has held it,
and until this harness existed it had never been run against anything.

```
just demo-corpus-crossing
```

Docker, the AWS CLI, a populated `.corpus` (`just corpus-fetch`), port 4605.
It runs six plans and three applies over 63 instances, so it is the slowest
of these harnesses by some way.

What the estate contributes that a generated fixture cannot is not scale.
It is three shapes nobody would think to write on purpose.

- `aws_route53_zone.production` and `aws_route53_zone.internal` are **both
  named `datacite.org`**. The hosted zone ID is server-assigned, so nothing
  in the configuration separates them and only the `tofu-address` marker
  does. `production-ns` and `internal-ns` then share a name *and* a type,
  differing in nothing but the zone ID each inherited, and step 6 asserts the
  run rendered two distinct apex NS identities rather than one twice.
- 59 of the 63 instances are `aws_route53_record`, which has no `tags`
  argument. They carry no marker, cannot carry one, and do not need one: the
  identity re-derives from the declaration plus the zone's ID on every run.
  Four marker carriers, 59 instances with none, in one estate.
- Names a fixture author would not invent: a wildcard label
  (`*.blog.datacite.org`, which Route 53 stores escaped as `\052`), a name
  written relative to its zone (`_lovable.strategy`), a label repeating the
  zone name inside itself (`datacite.org._domainkey.datacite.org`), and two
  names spelled with the trailing dot Route 53 itself uses.

The identity assertion takes its expectation from Route 53 rather than from
choudoufu: every string the run rendered must name a hosted zone that exists,
or a record set that exists *in the zone that identity names*. Both spellings
Route 53 accepts are allowed and nothing else is.

Steps 4 and 7 pin defects rather than features, so exit 0 means each is still
true and a red one means somebody fixed it.

| Step | Pins |
|---|---|
| 4 | `allow_overwrite` is a create-time argument Route 53 never returns, and under markers there is no state file to remember it. The ten `wp-prod-staging` records show an in-place update on every run, and applying that update does not settle it — the same ten come back. |
| 7 | A record whose `name` carries Route 53's own trailing dot renders an import identity carrying that dot. The import succeeds, so the instance binds a real record by a wrong name; the plan then proposes destroying and recreating it, once per run. Step 7 restores the dot deliberately and asserts the identity check fires **on the string**, not on the count. |

The deltas the estate needs before it can run at all are applied by the
script, each asserting that it matched, and each labelled with what kind it
is: an ordinary onboarding edit, an emulator flag, an emulator gap, or a
defect. `run.sh`'s header says what each one is and why.
