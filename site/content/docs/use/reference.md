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

## The demo, which is also the test suite

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

## The live configuration

Two places to write it, one dialect. The leading form is the sidecar
`estate.chdf.hcl` at the configuration root. Its body is the live configuration
itself, and since the extension is not `.tf`, stock OpenTofu, Terraform, fmt
and linters never parse it.

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

`snapshots` and `snapshot_path` are tombstones. The observational-snapshot
subsystem they configured was removed, and setting either errors with what
replaced it. Guided discovery's hint now rides the `record_store`.

### `record_store` block

One label picks the backend, `"local"`, `"ssm"`, or `"s3"`. It stores the
values of logical resources such as `null_resource`, `terraform_data`, `time_*`
and non-secret `random_*`. Declaring it is what admits those types. Writes are
conditional rather than locked. [Storage]({{< relref "/docs/use/storage" >}}) has the per-backend
trade-offs.

| Argument | Applies to | Meaning |
|---|---|---|
| `path` | `local` | Directory for the records, relative to the module. |
| `bucket` | `s3` | The bucket holding the records. |
| `key_prefix` | `ssm`, `s3` | Namespace for this estate's records. A prefix whose first segment is `tofu-receipts` or `tofu-hints` is a decode error, because those namespaces belong to receipts (ordinary declared resources) and the guided-discovery hint respectively. |
| `region` | `ssm`, `s3` | Region of the store. Unset, the AWS SDK's own default-configuration chain decides. |

### `policy` block

The ownership matrix. One verb per quadrant of declared-or-not against
tagged-or-not, plus marker key overrides and the delete guard.
[Day 2 operations]({{< relref "/docs/use/day2" >}}) has the verbs, defaults and reasoning. The
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

| Argument | Values | Default | Meaning |
|---|---|---|---|
| `marker_repair` | `"repair"`, `"report"`, `"never"` | `"repair"` | What a run does about an ownership marker on a live object that disagrees with the marker this configuration declares. `"repair"` writes the declared value over it, as the plan's ordinary in-place tags update. `"report"` leaves it and names what it would have written. `"never"` leaves it silently, for an estate where something else owns the tags. |

None of the three affects a resource being created. A create is stamped
whatever the setting says: the safety rule has no converse permitting an
unmarked create, and a create writes a marker that is new rather than one
that disagrees with anything.

`"report"` and `"never"` are refused by lint in this build. The grammar
landed ahead of the mechanism, and a setting about marker safety that
decoded and then did nothing would be worse than one that says so. The
`strict-marker-repair` entry in
[`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#strict-marker-repair)
has the reason, which is that marker repair is not a switch anywhere:
markers are repaired by the plan's ordinary tags diff, and suppressing that
per key is the same problem `lifecycle { ignore_changes }` poses.

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
tagging verbs below move with botocore across 205 services, so they are
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

The language, CLI, providers and backends are unmodified. Use
[opentofu.org/docs](https://opentofu.org/docs/).
