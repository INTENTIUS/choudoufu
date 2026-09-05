---
title: "Reference"
weight: 10
---

# Reference

The normative specifications live in the repository beside the code and the
tests holding them to it. This page indexes them.

They are for people integrating with choudoufu or working on it. To get an
estate running, use the path pages.

## Specifications

| Document | What it settles |
|---|---|
| [`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md) | The marker tag spec. Key names, the escaping rule, continuation tags, ownership semantics, the rename rule, and what protects the tags. The one surface external tooling can rely on. |
| [`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md) | Every construct the mode bounds or rejects, per rule, each with its lint rule and fixture. |
| [`live/RECEIPTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md) | Recording an effect that leaves nothing in the live system to read back, and the guards on the pattern. |
| [`live/OUTPUTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md) | Sharing values between estates with no remote state. |

## Coverage and evidence

| Document | What it settles |
|---|---|
| [`live/COVERAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/COVERAGE.md) | Which AWS resource types are covered, in layers, and what each layer means. |
| [`live/SURVEY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/SURVEY.md) | How admission is decided per type, the method, and the raw signals behind it. |
| [`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md) | What the pinned AWS emulator can and cannot show. Four questions no emulator-backed run answers at any scale, and where each one's real answer comes from instead. |

## The demo that is also the test suite

[`live/e2e/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md)
documents the harness, what each step proves, the environment knobs, and each
exit code.

```
bash live/e2e/run.sh --expect 5
```

## Commands

`choudoufu <command> -help` is authoritative for flags. The live-specific
commands follow.

| Command | What it does |
|---|---|
| `choudoufu plan` / `apply` | Ordinary plan and apply. With a `live` block present, these run against markers. |
| `choudoufu live-mv <old> <new>` | Rewrites the `tofu-address` tag. The replacement for `moved` blocks. |
| `choudoufu live-import` | Bulk migration. Reads an existing state file once, verifies each entry, stamps markers on what verifies. |
| `choudoufu live-plan` | The live plan, invoked directly. |
| `choudoufu plan -adoption-only` | The adoption ledger alone: what this estate can adopt, what it cannot, and why. |
| `choudoufu force-unlock` | Refused, with the true reason: there is no lock to force open. Contention settles at the platform API, never in a lock this tool holds - the no-self-managed-locks claim demonstrates it. |

### `-adoption-only`

During a migration the question is which live resources this estate can
claim. A plan answers it, but in pieces, spread across three sections that
are each about something else and surrounded by a report whose size is set by
the provider's type count rather than by the estate. Measured on a generated
55-resource terralith at commit `e1dec69cef` (2026-08-30, #587), the sections
carrying an adoption path were 5.6% of 2,885 lines; at 205 resources they were
5.5% of 7,649. The admission table has grown since (see the [readiness
tiers]({{< relref "/docs/use/compatibility#readiness-tiers" >}}) table for
the current type count), so a fresh plan's line count will not match these
two exactly; the ratio is the point, not the byte count.

`choudoufu plan -adoption-only` (or `choudoufu live-plan -adoption-only`)
prints that question and nothing else. Every declared instance lands in one
of two halves:

- **Identity by declaration.** The provider's schema for the type has no tags
  argument, so the resource carries no ownership marker and never will: its
  identity is composed from its own declaration and from parents that do
  carry markers. Nothing is adopted here, and nothing is written here. On a
  real estate this is routinely the larger half - on the generated terralith
  it is 41 of 79 instances at scale 1, all of them
  `aws_iam_role_policy_attachment`, `aws_route53_record` and
  `aws_iam_role_policy`.
- **Identity by marker.** Split into what this estate already owns, what a
  tag write would claim (with the values, and a command where the type has
  one), what needs a marker but has no live resource to offer, and what
  another estate holds.

Warnings are compacted rather than dropped: each is printed as one line, its
summary with a count when the same summary recurs. A heading says how many
there were and that the same command without `-adoption-only` shows them in
full. Errors are never touched. This is most of what the mode removes -
against `live/e2e/estate-block` plus an IAM role and its inline policy on the
pinned emulator at commit `e1dec69cef` (2026-08-30, #587), a plain plan was
926 lines, of which 470 were the bodies of 36 "Incomplete sweep for
undeclared resources" warnings, one per provider type the emulator could not
list. The adoption-only run of the same estate was 53 lines. **Stale on the
warning count specifically**: `09d180f921` landed one day later and stopped
an ordinary plan from enumerating the whole admission table, so a plan run
today against an estate with its own evidence to narrow by prints far fewer
of these warnings than it did when this was captured; `-adoption-only` still
forces the full sweep regardless (see [what a plan
costs]({{< relref "/docs/model/plan-cost#when-the-native-leg-is-narrowed-and-when-it-is-not" >}})),
so the *shape* this paragraph describes still holds, but the exact line
counts have not been re-measured since.

The mode changes what is printed, and since `09d180f921` it also changes what
is done. The live reads and the plan are the same, and every verdict in the
ledger is the one an ordinary run would have printed. The sweep is not the
same: `-adoption-only` is what turns the estate-wide sweep's account-inventory
question **on**, so it enumerates every admitted type this estate has no
evidence of ever having used, and an ordinary plan of an adopted estate does
not. On the 79-instance terralith that is 710 API calls against 157, about
4.5x. It is also the flag a migrating operator is told to reach for, which is
correct, because during a migration the account-wide question is the point.

**An earlier version of this page said the mode "costs the same time as an
ordinary plan".** That was true when written and stopped being true at
`09d180f921`. Budget for the wider run.
[What a plan costs]({{< relref "/docs/model/plan-cost" >}}) has the split, the
conditions under which an ordinary plan narrows, and
`TOFU_LIVE_COLLECT_UNCLAIMED` for asking or declining the question
independently of this flag.

It needs a `live` block; a state-backed plan refuses it.

Identity resolution and marker stamping run through the plan-node seam
(GitHub issue #388) by default: the record, then the marker index, then the
provider's identity schema over the plan's own evaluated configuration,
resolved at the same graph node stock plans a resource at. `CHOUDOUFU_NODE_RESOLVE=0`
in the environment that runs a plan or apply opts back out to the older
pre-walk static evaluator and HCL-rewriting stamp, which choudoufu still
ships and still runs the full estate suite against; that path is scheduled
for retirement, not removed, so the variable exists for an estate the node
path does not yet handle, not as a supported long-term choice.
This is a build-migration switch, not a per-estate setting, so it belongs in
the environment that invokes the binary, never in a `live` block.

## The live configuration

Two places to write it, one dialect. The leading form is the sidecar
`estate.chdf.hcl` at the configuration root. Its body is the live configuration
itself, and since the extension is not `.tf`, stock tooling never parses it:
OpenTofu and Terraform skip it, and so do fmt and linters.

```hcl
# estate.chdf.hcl
estate = "prod-networking"

record_store "ssm" {
  key_prefix = "tofu-records/prod-networking"
}
```

The same content may live in a `live` block inside `terraform`. Both forms are
supported. Both present at once is an error naming the file and the block. A
`backend` or `cloud` block alongside either is refused in the decoder, before
any command runs.

### Arguments

| Argument | Meaning |
|---|---|
| `estate` | The estate this configuration owns, the value the `tofu-estate` marker carries. Deliberately a literal string, because a name assembled from variables could differ between plan and apply, and the estate name is an identity rather than a computed value. Optional. Omitted, the name derives from the markers this configuration stamps. |
| `reads` | `"selective"` (the default) or `"full"`. Selective lets a `-refresh=false` run serve vouched, unchanged instances from the state cache, skipping their wire reads outright; full makes every plan pay every read regardless of flags - the estate-level off switch. `CHOUDOUFU_READS` overrides per run. Default plans read fully either way: drift detection never depends on this setting. |

`snapshots` and `snapshot_path` are tombstones. The observational-snapshot
subsystem they configured was removed, and setting either errors with what
replaced it. Guided discovery's hint now rides the `record_store`.

### `record_store` block

One label picks the backend, `"local"`, `"ssm"`, or `"s3"`. It stores the
values of logical resources such as `null_resource`, `terraform_data`, `time_*`
and `random_*`. Declaring the block is not what admits those types: every
estate has a store, and one that names no `record_store` gets an implied local
one, so a logical resource is admitted with no `record_store` block present.
Declare it to choose where the records go. Writes are conditional rather than
locked. [Storage]({{< relref "/docs/use/storage" >}}) has the per-backend
trade-offs, and
[What you set up by hand]({{< relref "/docs/use/setup" >}}) has what each
backend needs to exist first.

| Argument | Applies to | Meaning |
|---|---|---|
| `path` | `local` | Directory for the records, relative to the module. |
| `bucket` | `s3` | The bucket holding the records. |
| `key_prefix` | `ssm`, `s3` | Namespace for this estate's records. A prefix whose first segment is `tofu-receipts` or `tofu-hints` is a decode error, because those namespaces belong to receipts (ordinary declared resources) and the guided-discovery hint respectively. |
| `region` | `ssm`, `s3` | Region of the store. Unset, the AWS SDK's own default-configuration chain decides. |

### `policy` block

The ownership matrix. One verb per quadrant of declared-or-not against
tagged-or-not, plus marker key overrides and the delete guard.
[The ownership policy matrix]({{< relref "/docs/use/ownership-policy" >}}) has the verbs, defaults and reasoning. The
arguments follow.

| Argument | Meaning |
|---|---|
| `declared_tagged`, `declared_untagged`, `undeclared_tagged`, `undeclared_untagged` | The verb for each quadrant. |
| `tag_key`, `tag_value` | Override the marker tag names. |
| `threshold` | Guard for a delete quadrant. The run refuses when more resources than this would be deleted. The decoder accepts any non-negative whole number, and lint refuses zero. |

The `undeclared_untagged = "delete"` quadrant reconciles a whole account and
requires a nested `scope` block bounding what a sweep may touch, through
`services`, `types` and `regions`, each a list. Other delete verbs need none,
including `undeclared_tagged`'s default estate-scoped sweep.

### `strict` block

The principles this fork exists for, each as a toggle whose default is
today's behavior. A configuration with no `strict` block, and one whose
`strict` block sets nothing, behave identically: that is what makes
"compatible out of the box" true by construction rather than by review.
Turning a toggle on is the setup step.

<!-- toggles-gen:begin strict-toggles -->
| Argument | Values | Default | Meaning |
|---|---|---|---|
| `marker_repair` | `"repair"`, `"never"` | `"repair"` | What a run does about an ownership marker on a live object that disagrees with the marker this configuration declares. "repair" writes the declared value over it, as the plan's ordinary in-place tags update. "never" leaves it silently, for an estate where something else owns the tags, and only once a markers "record" selection gives the resource an identity source that is not the marker. |
| `secrets` | `"store"`, `"refuse"` | `"store"` | What a run does with the secret material a configuration generates or sets. "store" keeps it the way stock OpenTofu keeps it. "refuse" keeps none of it: a secret-generating type is refused outright, and a sensitive settable argument is never recorded. |
| `no_source_create` | `"refuse"`, `"create"` | `"refuse"` | What a run does with an instance that has no record, no live marker and no identity anything can derive from configuration. "refuse" reports it, by name, and names both remedies: "choudoufu live-import" from a stock state that already holds it, or this toggle. "create" selects stock OpenTofu's own behavior for a resource with no prior state: plan a create. |
<!-- toggles-gen:end strict-toggles -->

None of the settings above affects a resource being created. A create is stamped
whatever the setting says: the safety rule has no converse permitting an
unmarked create, and a create writes a marker that is new rather than one
that disagrees with anything.

The table's `marker_repair` values leave out `"report"` on purpose: it is
still valid `strict { marker_repair = ... }` grammar (this fork's decoder
parses it and refuses it with a "not implemented yet" detail, rather than
a generic typo message), but no build gives it a mechanism, and unlike
`"never"` it has no path to one - not even the conditional one a `markers
"record"` selection gives `"never"`. Declaring it as a usable setting
would be the same false "you are fine" HANDOFF.md warns against, so this
page does not. `"never"` on its own (no selection) is refused for the
reason in the `strict-marker-repair` entry in
[`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#strict-marker-repair):
marker repair is not a switch anywhere. Markers are repaired by the plan's
ordinary tags diff, and suppressing that per key is what
`lifecycle { ignore_changes }` does - which is refused: a resource whose
identity is only its marker and whose marker write is discarded can never be
found again. `"never"` therefore needs a resource to have somewhere
else to hold its identity, which is the next block.

#### Pinning `secrets` and `no_source_create` from the environment

`secrets` and `no_source_create` can be pinned to their strict setting
(`"refuse"` for both) from OUTSIDE the configuration: set
`CHOUDOUFU_STRICT_PIN=1` in the environment that runs a plan or apply, and
a `strict` block that sets either of them to anything else is refused, at
the offending argument's own line, naming the environment variable and the
value it forces. An omitted argument resolves to the pinned setting
silently, with no refusal - pinning changes what "nothing here" means, it
does not require every configuration to say so out loud.

This is the mechanism a platform team uses to require a behavior a
configuration author cannot switch off in the same commit that would relax
it: the pin lives in the process that runs the plan rather than in anything a
pull request touches. Relaxing a toggle and approving that relaxation can
never be the same change. `marker_repair` is not pinnable this way - its
three settings are not a single safety axis the way the other two are (see
the table above), so there is no one setting "pinning the profile" could
force it to.

#### `secrets`

The default is `"store"`, and that is the compatibility half: a stock
OpenTofu state file holds `random_password.result` in clear, so a
configuration that generates a password runs here with a `live` block added
and nothing else. What a state file would hold, the estate's record store
holds - namespaced per estate, under IAM, written with compare-and-swap,
with the sensitivity marks travelling beside the value. A secret-generating
type still needs a `record_store` declared, exactly as every other logical
type does.

`"refuse"` is the principle, and it is two refusals rather than one:

- a **secret-generating logical type** (`random_password`, `tls_private_key`,
  `local_sensitive_file` and their measured siblings) is refused at lint,
  naming the setting. It is refused again at the two other layers that could
  write such a record without lint having run: identity resolution, and
  `choudoufu live-import`, which seeds records straight from a stock state
  file;
- a **sensitive settable argument** on an ordinary cloud resource is never
  recorded as residue - the argument values this fork remembers because the
  provider's own read never gives them back.

```hcl
terraform {
  live {
    estate = "prod"
    record_store "ssm" {}

    strict {
      secrets = "refuse"
    }
  }
}
```

Three things neither setting reaches, and they are not the same kind of
thing:

- **Write-only attributes**, ever. The plugin protocol forbids a provider
  returning one, so a recorded value could never be checked against the
  object it describes - and stock does not keep one either, nulling them out
  before the state is written. This is not a stricter or laxer choice.
- **Effect receipt values.** A receipt is a published breadcrumb whose whole
  purpose is that other tools can read it, which is the opposite of a record
  store's IAM boundary, and stock has no equivalent of it to be compatible
  with. See `receipt-secret` in
  [`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#receipt-secret).
- **A sensitivity mark the provider's schema did not put there.** A residue
  record stores an unmarked value and the sensitivity is reconstructed from
  the schema when the record is read, which is exact for a schema mark and
  for nothing else. A value that picked up sensitivity from a
  `sensitive = true` *variable* stays out under either setting, and the
  argument is proposed for update on every plan.

A **markerless type whose schema carries credential material** is also
outside this setting's reach today, and that is a deliberate bound rather
than an omission - see
[`strict-secrets`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#strict-secrets)
for the two measurements behind it.

#### `markers "record"` block

A nested block inside `strict`, naming the resources that hold their
identity in the estate's record store instead of in a `tofu-address` tag. No
ownership marker is written for them at all. It is the tag-budget and
tag-policy toggle: you buy a tag back and pay for it in governability, since
an `aws:ResourceTag` condition or a cost report can no longer see the
resource as this estate's, and neither can any other tool that lists by tag.

```hcl
terraform {
  live {
    estate = "prod"
    record_store "ssm" {}

    strict {
      marker_repair = "never"

      markers "record" {
        types     = ["aws_ebs_volume"]
        addresses = ["aws_instance.worker", "module.server.aws_instance.instance"]
      }
    }
  }
}
```

| Argument | Meaning |
|---|---|
| `types` | Resource types whose every instance is selected. A literal list of strings. |
| `addresses` | Individual resources, in the `-target` grammar: module-qualified or not, no wildcards. A literal list of strings. |

Both are optional and either may be given alone, but a block naming neither
is refused: it narrows nothing, and reading it as "everything" would
withhold a marker from resources nobody named.

Three things it requires, each a lint refusal when missing:

- **The identity goes to a `record_store`.** A selection with nowhere to
  put one leaves the resource with neither a marker nor a record.
- **Whole resources go in `addresses`, not instances.** `aws_instance.web[0]`
  is refused. One configuration body serves every instance a `count` or
  `for_each` expands to and the marker written into it is a template over
  the instance key, so a marker cannot be withheld from one instance and
  written for its siblings. Split the instance you mean into its own
  resource block.
- **The type's identity must be recordable.** The provider has to import
  the type back, its exported `id` has to be provably the whole of its
  import string, and the attribute the record would hold must not be one the
  provider marks sensitive. See
  [`strict-markers-unrecordable`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#strict-markers-unrecordable);
  those three are not skippable by choosing, because each is a way to record
  a *wrong* identity, which no later run can detect.

Pairing the selection with `marker_repair = "never"` is what makes
`lifecycle { ignore_changes }` over the marker tags stop being refused - for
the selected resources only. A resource the selection does not cover still
gets its marker and still refuses `ignore_changes = [tags]`, so an
estate-wide `"never"` meets its limit loudly rather than silently.

The label is `"record"` because it names one of a family. `markers "tag"`,
the inverse selection, is grammar this leaves room for.

## Permissions a run needs

choudoufu makes few AWS calls of its own. Resource reads, writes and lists go
through the provider plugin, so those are the AWS provider's permissions,
exactly as any OpenTofu run. The fork's own surface follows.

| Stage | Calls | Where |
|---|---|---|
| Estate-wide tag sweep | `tag:GetResources` | `internal/live/cloudcontrol/tagging.go` |
| Cloud Control fallback | `cloudformation:ListResources`, `cloudformation:GetResource` | `internal/live/cloudcontrol/client.go` |
| Record store, `ssm` | `ssm:GetParameter`, `ssm:PutParameter`, `ssm:DeleteParameter`, `ssm:GetParametersByPath` | `internal/live/staterecord/ssm.go` |
| Record store, `s3` | `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`, `s3:ListBucket` | `internal/live/staterecord/s3.go` |
| Record store, `local` | none | `internal/live/staterecord/local.go` |

Each row names the file making the calls. That list is short and fixed, so a
generated span for ten names would cost more machinery than it saves. The
tagging verbs below move with botocore across <!-- tagverbs-gen:begin tag-verbs-total -->205<!-- tagverbs-gen:end tag-verbs-total --> services, so they are
generated.

## Marker stamping

Writing an ownership marker calls the tagging action for the resource's own
service. The provider makes that call during the ordinary apply, so a role that
can create the resource can usually already tag it. The actions matter when a
policy is scoped tightly.

<!-- tagverbs-gen:begin tag-verbs -->
| Action | Services |
|---|---|
| `TagResource` | 136. ARCRegionSwitch, AccessAnalyzer, Amplify, AppConfig, AppFlow, AppIntegrations and 130 more |
| `AddTagsToResource` | 7. DMS, DocDB, ElastiCache, Neptune, RDS, SSM and 1 more |
| `AddTags` | 5. DataPipeline, EMR, ElasticLoadBalancing, ElasticLoadBalancingV2, SageMaker |
| `CreateTags` | 4. EC2, MediaLive, Redshift, WorkSpaces |
| `AddLFTagsToResource` | 1. LakeFormation |
| `ChangeTagsForResource` | 1. Route53 |
| `SetTagsForResource` | 1. Inspector |
| `Tag` | 1. ResourceGroups |
| `TagCertificateAuthority` | 1. ACMPCA |
| `TagQueue` | 1. SQS |

158 services carry an unambiguous tagging verb. 47 do not, and a run cannot stamp a marker on those.
<!-- tagverbs-gen:end tag-verbs -->

Whether a policy condition on those actions is evaluated is a separate
question. `live/iam-reference.json` answers it from AWS's own Service
Authorization Reference. That artifact is authoritative about the condition
keys it names and silent about the ones it omits. A listed key is evidence the
condition applies. An unlisted one is an absent statement rather than a
statement of absence.

## Everything else is OpenTofu

The language and CLI are unmodified, and so are providers and backends. Use
[opentofu.org/docs](https://opentofu.org/docs/).
