# Is choudoufu, With No `live` Block, Stock?

Issues: https://github.com/INTENTIUS/choudoufu/issues/588,
https://github.com/INTENTIUS/choudoufu/issues/582

A measurement document plus a code reading. It answers one question: when
this fork's binary runs a configuration that has **no `live` block**, does it
cost what stock costs, and does it accept what stock accepts?

Everything measured about this fork so far has been measured with a live
block present, so "choudoufu plan takes 200s and terraform plan takes 3s"
(#588) has never been separable into *the fork costs this* and *statelessness
costs this*. Those are different claims with different consequences, and only
one of them is a reason not to install the binary.

## Summary of findings

1. **Stateful choudoufu is indistinguishable from stock, by API call count.**
   Over the same 79-instance and 301-instance terralith, against the same
   emulator, in the same session, `terraform plan`, `tofu plan` and
   `choudoufu plan` each issued **exactly the same number of AWS API calls** —
   150 at scale 1 and 558 at scale 4 — and each proposed no changes. The
   counts are identical run to run and binary to binary, with no variance at
   all. Reproduced across three independent runs.

2. **Nothing this fork adds to plan or apply executes without a live block.**
   Lint, the refusal set, marker stamping, discovery, the projection state
   manager and #388's plan-node seam are each behind a named guard, all of
   which reduce to `Meta.statelessSettings` returning `nil`. Seven things do
   run unconditionally; all seven are listed below, and none of them refuses
   anything.

3. **#580's `count-index` refusal does not fire in stateful mode, and neither
   does any other lint rule.** Proved by running the same configuration
   through the same binary twice, differing only in the `live` block: without
   it, `Plan: 6 to add`; with it, `Error: count.index is not available in
   resource arguments / Rule: count-index`.

4. **The mixed workflow is possible but not free, and the cost is a specific
   one nobody has written down.** After `live-import -approve`, a stateful
   plan on the same estate — same binary, no live block, the state file
   untouched — proposes **0 to add, 38 to change, 0 to destroy** at scale 1
   and **137 to change** at scale 4: it wants to strip `tofu-estate` and
   `tofu-address` off every stamped resource. Applying it would silently
   un-migrate the estate.

5. **A correction, and it is the largest number in this document.** The
   migrated plan on this estate costs **2.73–3.79s** against floci, not 273s.
   The 273s figure reproduces exactly — 273.42s to 274.43s across eight runs —
   but only with `tools/terralith-gen`'s own provider block, which sets
   `skip_requesting_account_id = true`. With the provider block
   `live/live-cert/terralith-scale.sh` uses for floci, the same plan on the
   same estate at the same commit takes 2.73s and is **empty**. The
   difference is 35 retried `ecs:DescribeServices` calls, which is issue #572.

6. **#580's fix holds end to end at scale 4, which the fixing unit said it had
   not verified.** The migrated terralith at `-scale 4` plans **empty**:
   301 instances, 137 stamped, `No changes`.

7. **In API calls, the two curves do not cross.** Fitting the two measured
   scales: stock `= 1.84N + 5`, migrated choudoufu `= 1.99N + 553`.
   choudoufu's *marginal* cost per instance is about 8% **higher** than
   stock's, not lower. What shrinks with N is the ratio (4.7x at N=79, 2.1x at
   N=301, a predicted 1.4x at N=1000), never the difference. Two points
   determine a line exactly, so this is a description of two measurements, not
   a tested model.

## Conditions

| | |
|---|---|
| Commit | `b1b1c6a13e` (rebased onto `origin/main` `5dbe452a1e`, which carries #580's fix `c02ea7aa88`) |
| Emulator | floci `ghcr.io/lex00/floci@sha256:c55d74e1`, the repository's pin |
| Real AWS | none. Nothing in this document cost money. |
| Estate | `tools/terralith-gen -scale 1` (79 instances) and `-scale 4` (301) |
| Stock | Terraform 1.15.8 and OpenTofu, both from PATH, darwin/arm64 |
| Provider | `hashicorp/aws` 6.59.0 |
| Repeats | 3 per column, every value reported, nothing averaged or discarded |
| Harness | `internal/live/statefulcost`, in this commit |

OpenTofu is measured as well as Terraform because choudoufu is an OpenTofu
fork and Terraform 1.15 is not OpenTofu. Without that column, any difference
between Terraform and choudoufu is unattributable.

Each column applies its own copy of the estate under its own name prefix, so
no column reads another's objects. The three stateful columns share one
emulator (a stateful plan reads each instance by the ID its state file holds
and issues no estate-wide list, so they cannot contaminate each other); the
migrated column gets an emulator to itself, because its whole cost is an
estate-wide sweep and a foreign object would inflate exactly the number under
test.

## The table

Scale 1 — 79 instances, every column an empty plan:

| column | seconds (3 runs) | API calls (3 runs) |
|---|---|---|
| stock `terraform plan`, state file, no live block | 1.70 1.59 1.60 | 150 150 150 |
| stock `tofu plan`, state file, no live block | 1.53 1.49 1.48 | 150 150 150 |
| **`choudoufu plan`, state file, no live block** | **1.47 1.47 1.49** | **150 150 150** |
| `choudoufu plan`, live block, migrated, no state | 3.79 2.87 2.73 | 710 710 710 |

Scale 4 — 301 instances, every column an empty plan:

| column | seconds (3 runs) | API calls (3 runs) |
|---|---|---|
| stock `terraform plan`, state file, no live block | 2.43 3.37 2.93 | 558 558 558 |
| stock `tofu plan`, state file, no live block | 1.70 1.81 1.74 | 558 558 558 |
| **`choudoufu plan`, state file, no live block** | **1.74 1.88 1.95** | **558 558 558** |
| `choudoufu plan`, live block, migrated, no state | 3.28 3.05 2.97 | 1152 1152 1152 |

Call counts, not seconds, carry this result. `live/plan-budget.json` treats
wall clock as never-gated because it grades the machine, and at these
magnitudes a plan's wall clock against floci is mostly provider process
startup: 1.5s of it is paid by every column including stock's.

`choudoufu live-plan` was measured too, at scale 1, and agrees with
`choudoufu plan` to within the noise: 273.73/273.57/273.44s versus
273.42/274.19/273.42s, 744 calls each, in the run that predates finding 5.
That agreement is not a coincidence and not evidence of much:
`LivePlanCommand.Run` delegates straight to `PlanCommand{Meta: c.Meta}` when
the configuration has a live block (`internal/command/live_plan.go:218`), so
the two forms are one code path.

## What the fork adds to a stateful run

Every guard below reduces to the same question, asked once per command:
`Meta.statelessSettings` (`internal/command/live_mode.go:97`) does the same
selective load that finds the `backend` block and returns `mod.Live`, which is
`nil` for a configuration with no live block and no `estate.chdf.hcl` sidecar.

| what the fork adds | where it is guarded |
|---|---|
| the whole stateless pipeline — projection state manager, discovery, marker sweep, record store, write-back | `if statelessCfg != nil` at `internal/command/plan.go:130` and `apply.go:139`, which is the only place `local.Stateless` is ever set (`live_mode.go:194`) |
| lint, including #580's `count-index` | `lint.CheckWith` at `internal/command/live_mode.go:602`, inside `statelessRunner.PriorState`, which `internal/backend/local/backend_local.go:239` calls only `if b.Stateless != nil` |
| the refusals (`-out`, `apply <planfile>`, `-json`, `-refresh-only`, `-state`/`-state-out`/`-backup`, non-default workspace, non-local backend) | `statelessRejections`, called only inside those same two `if statelessCfg != nil` blocks |
| #388's plan-node seam | `if resolver := evalCtx.ResourceIdentityResolver(); resolver != nil` (`internal/tofu/node_resource_plan_instance.go:301`) and `if adjuster := evalCtx.ConfigValueAdjuster(); adjuster != nil` (`node_resource_abstract_instance.go:1215`); both fields are set only in `statelessBegin`, under `nodeResolveEnabled()` |
| marker stamping | `internal/live/stamp` is imported by `internal/command/live_plan.go` and `live_policy.go` and by nothing else outside `internal/live` |
| discovery | `internal/live/discovery` is imported by `live_mode.go`, `live_mv.go`, `live_plan.go`, `live_policy.go` and by nothing else outside `internal/live` |
| `state`/`import`/`refresh`/`taint`/`untaint`/`workspace` refusals | each guard opens `settings, diags := m.statelessSettings(ctx, false); if diags.HasErrors() \|\| settings == nil { return diags }` — `live_state_guard.go:39`, `live_command_guard.go:38`, `live_workspace_guard.go:48` |
| the backend guard | `liveBackendGuard` returns `(false, nil)` when `settings == nil` (`meta_backend.go:805`) |
| the AWS provider version-skew check | `c.checkAWSProviderVersionSkew()`, called only inside `if statelessCfg != nil` |
| the static-evaluation subset (`WithRepetitionData`, `WithDataResults`, `WithUnknownForRefusedReferences`, `WithFunctionOverrides`, `EvalContextTolerant`) | additive builders returning copies; every caller is under `internal/live/**` or `internal/command/live_plan.go`. With none applied, `staticScopeData.GetCountAttr`/`GetForEachAttr`/`GetResource`/`GetModule` keep stock's `panic("Not Available in Static Context")` (`internal/configs/static_scope.go:580–670`) |

### The seven things that do run unconditionally

None of them refuses anything, and their combined cost is below the noise
floor of the table above — but "byte-identical" would be false, so here they
are.

1. **One extra selective config load per command.** `statelessSettings` calls
   `loadSingleModule(ctx, ".", configs.SelectiveLoadBackend)`, which is not
   cached. It is the same partial parse `loadBackendConfig` already does.
2. **Two new things are parsed everywhere.** A `live` block inside `terraform
   {}` (`internal/configs/parser_config.go`), and an `estate.chdf.hcl` sidecar
   appended at three call sites in `parser_config_dir.go`. A directory that
   happens to contain a file with that name is now read as configuration where
   stock ignored it — the one place where a stateful choudoufu run can behave
   differently because of a file on disk.
3. **`terraform.marker_module_prefix` resolves.** `internal/tofu/evaluate.go:1048`
   adds `markers.ModulePrefixAttr` to the `terraform`/`tofu` object. Additive:
   a name that used to be an unknown attribute now has an answer (and, in the
   root module, a fork-specific error).
4. **A malformed requires-replace path is a warning, not an error.** Where
   upstream fails the plan with "Provider produced invalid plan", the fork
   drops the signal with a warning when `plans.RequiresReplacePathIsDegenerate`
   holds — an attribute step with an empty name
   (`internal/tofu/node_resource_abstract_instance.go:1442`). **This is the
   only unconditional behaviour change that alters a verdict**, and it is in
   the accepting direction: a plan stock refuses can succeed here. Nothing in
   the terralith triggers it.
5. **Provider nodes record whether their config was wholly known.** One map
   write per provider instance in `internal/tofu/node_provider.go`, surfaced as
   `ResolvedProvider.ConfigKnown`. No behaviour change.
6. **`GetProviderSchema` decodes `ListResourceSchemas`.** `internal/plugin/grpc_provider.go`
   and `plugin6/grpc_provider.go` populate `resp.ListResourceTypes` on every
   schema fetch.
7. **HCL diagnostics keep their `Extra`.** `internal/tfdiags/hcl.go` carries
   `diag.ExtraInfo()` through the conversion.

`internal/engine/applying`'s two fork changes (`appliedObjectStatus`,
`markedAppliedValue`) are **not** in this list: that runtime is reachable only
with `TOFU_X_EXPERIMENTAL_RUNTIME` set in an experiments-enabled build
(`internal/tofu/context_temp_runtime.go`).

## Does #580's refusal fire in stateful mode?

No. Two directories, one binary, one difference — the `live` block:

```hcl
resource "aws_s3_bucket" "b" {
  count  = 6
  bucket = "q2b-bucket-${count.index % 3}"
}
```

```
no live block:   Plan: 6 to add, 0 to change, 0 to destroy.        exit 0
live block:      Error: count.index is not available in resource arguments
                 Rule: count-index. See live/LIMITATIONS.md.       exit 1
```

The same experiment with `lifecycle { ignore_changes = all }` gives
`Plan: 1 to add` without the block and `Error: Ownership markers would be
ignored / Rule: ignore-changes` with it. `RuleCountIndex` and
`RuleIgnoreChanges` are siblings in one enum
(`internal/live/lint/issue.go:59` and `:131`) emitted by one entry point
(`lint.CheckWith`), so this generalises to the whole rule set.

## Is the mixed workflow real?

Partly. Three things are true at once, and only the third is a surprise.

**Switching modes is an edit to one file.** Activation is a configuration
block and nothing persists the choice anywhere else. `liveBackendGuard`
deliberately leaves the old backend record in `.terraform` rather than
deleting it, and its own doc comment contemplates the return trip:
"Removing the live block later would have promoted it to truth."

**A read-only audit from a stateful directory works and costs nothing
durable.** `choudoufu live-plan -estate=NAME`, run in a directory with a state
file and no live block, exits 0, warns `State file present but not consulted`,
and leaves the state file byte-for-byte where it was. On a migrated estate it
reported `0 to add, 1 to change` — the one change being three `aws_ecs_service`
attributes floci does not echo back, which is what a record store exists to
hold and which the `-estate` form, having no live block, has none of.

**But once an estate is migrated, a stateful plan on it is no longer clean.**
Measured, one binary, no live block anywhere in the loop:

```
1. choudoufu apply           (no live block)   79 added
2. choudoufu live-plan -estate=E               Plan: 58 to add   [read-only audit, pre-migration]
3. choudoufu live-import -approve              38 stamped, 41 skipped
4. choudoufu plan            (no live block)   Plan: 0 to add, 38 to change, 0 to destroy
```

Step 4's 38 changes are 38 resources whose plan reads

```
      ~ tags = {
          - "tofu-address" = "aws_iam_role.team_0000_role" -> null
          - "tofu-estate"  = "sfcost-scd" -> null
        }
```

The state file has no record of the markers, so a stateful refresh reads them
as drift and proposes reverting them. Applying that plan un-migrates the
estate, silently. The same thing happens with stock `terraform` on a stock
state file: 38 to change at scale 1, 137 at scale 4.

Nothing warns about this. `liveStateFileNote` — the diagnostic written
precisely because "silently ignoring a file that every other OpenTofu command
treats as authoritative would be a nasty surprise" — is called from exactly
one place, `internal/command/live_plan.go:223`, which is reached only in the
`-estate` form, i.e. only when the configuration has **no** live block. Plain
`choudoufu plan` under a live block never emits it, and the reverse direction
(a stateful plan about to strip markers) has no diagnostic at all.

So "live is situational" is supportable in two of its three readings:

- *Install choudoufu and run it statefully day to day* — **yes**, on this
  evidence, with no measured cost and no new refusal.
- *Reach for `live-plan -estate` as a read-only audit or drift check* —
  **yes**, it writes nothing and leaves the state file alone.
- *Migrate an estate onto markers, then keep running it statefully between
  live sessions* — **not as it stands.** The two modes have no shared record
  of the markers and nothing tells the operator that.

## The 273-second correction

Finding 5 is the one most likely to be quoted, so here is its whole
derivation.

Same harness, same commit, same emulator, same estate, same session. The only
difference is the provider block: `tools/terralith-gen`'s own output, which
sets `skip_requesting_account_id = true`, versus the block
`live/live-cert/terralith-scale.sh`'s `provider_block` emits for
`TARGET=floci`, which does not.

| provider block | migrated plan, seconds | API calls | plan verdict |
|---|---|---|---|
| terralith-gen's own | 273.42 274.19 273.42 | 744 | 3 to add (the ECS layer) |
| the certification harness's floci block | 3.79 2.87 2.73 | 710 | empty |

A hundredfold difference in wall clock for a 4.6% difference in call count
means the wall clock was not work. The 34-call delta is exactly:

```
generator block: DescribeServices 36, DescribeClusters 3, DescribeTaskDefinition 3, no STS
cert block:      DescribeServices  1, DescribeClusters 1, DescribeTaskDefinition 2, GetCallerIdentity 2, GetUser 2
```

35 extra `ecs:DescribeServices` over 270 extra seconds is 7.7s apiece: SDK
retry backoff on a call that cannot succeed. That is issue #572, whose root
cause `live/live-cert/terralith-scale.sh`'s own doc comment already names —
"`skip_requesting_account_id` (root cause of #572, the ECS
identity-resolution defect)". An ECS ARN carries the account id.

Two consequences, stated with their limits:

- **`rfc/20260830-slicing-under-choudoufu.md`'s "744" for a scale-1 migrated
  projection is inflated by 34 calls**, and its "148" for stock by 2 (this
  measurement reads 710 and 150 with the corrected block). The 4.6% does not
  disturb that document's conclusions, all of which are ratios between
  configurations that share the fixture.
- **#582's "its post-migration plan ran *slower* than real AWS (273.6s vs
  209s at scale 1)" is numerically identical to what the defective provider
  block produces here.** I did not run #582's measurement and cannot say its
  273.6s has this cause; I can say that a run with the generator's own block
  reproduces it to three significant figures and that fixing the block moves
  it to 2.73s. That figure should be re-derived before it is quoted again.

**Direction of the correction.** This change moved the result strongly in
choudoufu's favour, which is the direction a measurement should be most
suspicious of. The reasons for making it are that it is the provider block the
repository's own certification harness already uses for this exact estate on
this exact emulator; that it is applied to all four columns, not only the one
it helps; and that without it the migrated column proposes work, so it is not
the same operation as the columns it is compared against. Both numbers are
above.

## What #588 should take from this

#588's working hypothesis is that choudoufu's plan is a high fixed cost plus a
low marginal cost, that stock's is near-zero fixed plus a marginal cost
proportional to resources, and that the curves therefore cross.

In API calls, on this evidence, **the first half is right and the second half
is not**:

```
stock                = 1.84N + 5        (150 at N=79, 558 at N=301)
choudoufu, migrated  = 1.99N + 553      (710 at N=79, 1152 at N=301)
```

The fixed cost is real and is 553 calls, which matches #582's independently
fitted sweep constant of 548.3 closely enough to be the same thing. But
choudoufu's marginal cost is not lower than stock's — it is about 8% higher,
because a migrated instance is read by the projection *and* pays its share of
the sweep. So the ratio falls with N and the difference does not: 4.7x at
N=79, 2.1x at N=301, and a predicted 1.4x at N=1000 that is an extrapolation
and should be measured before it is used.

Two points determine a line exactly. This fit has no residual and is therefore
a description of two measurements, not a test of a model. A third scale would
make it one.

Separately, #588's headline pair — stock 3/4/3s against choudoufu
203/211/200s on real AWS — is not contradicted by anything here. This document
measures an emulator, where 148 calls and 710 calls are 1.5s and 3s because
the round trip is a loopback. What it does establish is that the *stock* side
of that pair is available from this binary at no extra cost, and that the ~4.7x
call ratio (not 60x) is what the stateless path actually costs in work.

## What was not verified

- **Nothing was measured against real AWS.** Every number here is floci. The
  emulator returns a single page unconditionally (lex00/floci#185) and never
  throttles, so pagination and throttling are absent by construction, and wall
  clock is dominated by process startup rather than by network.
- **No third scale.** The two-point fits in findings 7 are exact by
  construction and untested.
- **Apply was not compared.** Only `plan`. The apply column would need a
  destroy-and-recreate loop this measurement did not build.
- **Only the AWS provider, and only one estate shape.** The terralith is
  IAM-dominated with an ECS layer and a Route 53 fan-out; a different mix
  moves the sweep's constant.
- **The stateful columns were measured on estates that were never stamped.**
  Finding 4's round trip covers the stamped case, but only as a single
  observation per scale, not as a timed column.
- **`#572`'s mechanism was inferred, not instrumented.** The evidence is a
  call-count delta of 35 `DescribeServices` and 270 seconds; no debug log was
  captured to show the retry loop directly.
- **`estate.chdf.hcl` in a stateful directory** (unconditional item 2) was
  read from the parser, not exercised.
