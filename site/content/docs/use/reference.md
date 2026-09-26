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

These files in the repository are the authority, and this page summarises them.

| Document | What it settles |
|---|---|
| [`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md) | The marker spec: key names, escaping, ownership, the rename rule |
| [`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md) | Every refusal, with its lint rule and fixture |
| [`live/RECEIPTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md) | Receipts, and the guards on them |
| [`live/OUTPUTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md) | Sharing values between estates |
| [`live/COVERAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/COVERAGE.md), [`live/SURVEY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/SURVEY.md) | Which AWS types are covered, and how admission is decided |
| [`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md) | What the pinned emulator can and cannot show |
| [`live/e2e/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md) | The end-to-end harness: `bash live/e2e/run.sh --expect 5` |

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
| `choudoufu plan -filter=<category>` | The live report narrowed to `unowned`, `adoptable` or `foreign`. Read-only: the plan is unchanged. |
| `choudoufu force-unlock` | Refused, with the true reason: there is no lock to force open. Contention settles at the platform API, never in a lock this tool holds - the no-self-managed-locks claim demonstrates it. |

### `-adoption-only`

`choudoufu plan -adoption-only` answers one question during a migration:
which live resource does each declared instance bind to. It prints each
instance's class (already marked, adoptable now, waits on parent, no path, in
the way, nothing live) with the command that adopts it, and nothing else. It
takes the full sweep, so it costs more than an ordinary plan.
[Recover an estate]({{< relref "/docs/use/recover-an-estate" >}}) has the
classes, and
[`live/ADOPTION-ONLY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/ADOPTION-ONLY.md)
has the measurements.

### `-filter`

`choudoufu plan -filter=foreign` (or `live-plan -filter=foreign`) prints
only the named sections of the live report. The categories are the ones
the report and the `-json` document already carry:

| Category | What it selects |
|---|---|
| `unowned` | Live resources at an identity this configuration declares that carry no marker for this estate |
| `adoptable` | Live resources the estate-wide sweep matched to a declared instance by content |
| `foreign` | Live resources the sweep found that nothing in this estate claims |

Repeat the flag to show more than one; they union. The filter narrows the
report and nothing else: the planned changes, the resource diff, the
omissions and removals, and the meaning of `-detailed-exitcode` are the same
with or without it. A filter cannot make an apply partial.

A category that matches nothing prints a line saying so, for example
`No unowned resources.`, so an empty result never looks like a filter that
did nothing. Any other word, such as `-filter=drifted`, is a usage error
naming the three accepted ones. Under `-json` the document gains a `filter`
key listing the kept categories, and each category left out is `null`; a
kept one with nothing in it is `[]`. `-filter` is refused on a state-backed
plan and alongside `-adoption-only`.

## The live configuration

Two places to write it, one dialect. The leading form is the sidecar
`estate.chdf.hcl` at the configuration root. Its extension is not `.tf`, so
stock tooling never parses it: OpenTofu, Terraform, fmt and linters all skip
it.

```hcl
# estate.chdf.hcl
estate = "prod-networking"

record_store "s3" {
  bucket = "my-records-bucket"
}
```

The same content may live in a `live` block inside `terraform`. Both forms
are supported; both present at once is an error naming the file and the
block. A `backend` or `cloud` block alongside either is refused in the
decoder, before any command runs.

### Arguments

| Argument | Meaning |
|---|---|
| `estate` | The estate this configuration owns, the value the `tofu-estate` marker carries. A literal string, not an expression: a name assembled from variables could differ between plan and apply. Optional; omitted, it derives from the markers this configuration stamps. |
| `reads` | `"selective"` (the default) or `"full"`. Selective lets a `-refresh=false` run serve vouched, unchanged instances from the state cache; full makes every plan pay every read whatever the flags. `CHOUDOUFU_READS` overrides per run. Default plans read fully either way: drift detection never depends on this setting. |

`snapshots` and `snapshot_path` are tombstones: the subsystem they configured
was removed, and setting either errors with what replaced it. Guided
discovery's hint now rides the `record_store`.

### `record_store` block

One label picks the backend: `"local"`, `"s3"` or `"kubernetes"`. Every
estate has a store; a `live` block naming none gets the local one.
[Where things are stored]({{< relref "/docs/use/storage" >}}) has the rest.

| Argument | Applies to | Meaning |
|---|---|---|
| `path` | `local` | Directory for the records, relative to the module. |
| `bucket` | `s3` | The bucket holding the records. |
| `key_prefix` | `s3`, `kubernetes` | Namespace for this estate's records, in place of `tofu-records/<estate>/`. It may not begin with a reserved root (`tofu-receipts`, `tofu-hints`, `tofu-outputs`, `tofu-located`, `tofu-residue`, `tofu-provisioned`). |
| `region` | `s3` | Region of the bucket. Unset, the AWS SDK's default-configuration chain decides. |
| `bucket_owner` | `s3` | The twelve-digit AWS account that must own the bucket. Every S3 call carries it as `ExpectedBucketOwner`, so a same-named bucket in another account is refused. |
| `namespace` | `kubernetes` | Namespace holding this estate's record Secrets. Defaults to `tofu-records-<estate>`. The store does not create it. |
| connection | `kubernetes` | How to reach the cluster: `host`, `token`, `config_path`, `config_context` and the rest, plus an `exec` block, spelled as stock's `kubernetes` backend spells them. `insecure = true` fails `tls_verification`. |
| `allow_insecure` | `s3`, `kubernetes` | A list naming the assertions this estate proceeds without. On `s3`: `"versioning"`, `"lifecycle"`, `"public_access_block"`; on `kubernetes`: `"tls_verification"`, `"namespace_access"`, `"read_isolation"`, `"encryption_at_rest"`, `"estate_boundary"`. Never a boolean. Each waiver is announced on every run with what it costs. [The three settings]({{< relref "/docs/use/bucket" >}}) has the bucket's. |

### `policy` block

The ownership matrix. One verb per quadrant of declared-or-not against
tagged-or-not, plus marker key overrides and the delete guard.
[The ownership policy matrix]({{< relref "/docs/use/ownership-policy" >}}) has the verbs, defaults and reasoning.

| Argument | Meaning |
|---|---|
| `declared_tagged`, `declared_untagged`, `undeclared_tagged`, `undeclared_untagged` | The verb for each quadrant. "Untagged" means carrying no estate marker at all; an object marked for another estate is outside all four. |
| `tag_key`, `tag_value` | Override the marker tag names. |
| `threshold` | Guard for a delete quadrant: the run refuses when more resources than this would be deleted. The decoder accepts any non-negative whole number; lint refuses zero. |

### `strict` block

The principles this fork exists for, each as a toggle whose default is what
stock OpenTofu does. A block that sets nothing and no block at all mean the
same thing, so an estate that sets none behaves like stock plus markers.
Turning a toggle on is the setup step.

<!-- toggles-gen:begin strict-toggles -->
| Argument | Values | Default | Meaning |
|---|---|---|---|
| `marker_repair` | `"repair"`, `"never"` | `"repair"` | What a run does about an ownership marker on a live object that disagrees with the marker this configuration declares. "repair" writes the declared value over it, as the plan's ordinary in-place tags update. "never" leaves it silently, for an estate where something else owns the tags, and only once a markers "record" selection gives the resource an identity source that is not the marker. |
| `secrets` | `"store"`, `"refuse"` | `"store"` | What a run does with the secret material a configuration generates or sets. "store" keeps it the way stock OpenTofu keeps it. "refuse" is two refusals: a secret-generating type is refused outright, and a sensitive settable argument is left out of its record. It does not reach the cache file, or a terraform_data or null_resource the configuration hands a secret. |
| `no_source_create` | `"refuse"`, `"create"` | `"refuse"` | What a run does with an instance that has no record, no live marker and no identity anything can derive from configuration. "refuse" reports it, by name, and names both remedies: "choudoufu live-import" from a stock state that already holds it, or this toggle. "create" selects stock OpenTofu's own behavior for a resource with no prior state: plan a create. |
| `provider_change` | `"refuse"`, `"recreate"` | `"refuse"` | What a run does when a resource block names a different provider configuration than the one whose account or region still holds a live object carrying this estate's marker for that block's address - a region or account change. "refuse" reports the object, by name, with the provider configuration that found it and the one its address now belongs to, and names both remedies: destroying or disowning that object, or this toggle. "recreate" selects stock OpenTofu's own behavior - plan the create under the new configuration - and warns, by name, that the old one's object is abandoned and nothing will find it again. |
<!-- toggles-gen:end strict-toggles -->

`secrets` and `no_source_create` can be pinned to their strict setting by
`CHOUDOUFU_STRICT_PIN=1` in the environment that runs the plan or apply, so a
configuration cannot relax them.
[`live/STRICT.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STRICT.md)
has each toggle's reasoning and what it refuses, with the fixtures that prove
it.

## Permissions a run needs

choudoufu makes few calls of its own. Resource reads, writes and lists go
through the provider plugin, so those are the provider's permissions as in
any OpenTofu run. The fork's own surface follows.

| Stage | Calls | Where |
|---|---|---|
| Estate-wide tag sweep | `tag:GetResources` | `internal/live/cloudcontrol/tagging.go` |
| Cloud Control fallback | `cloudformation:ListResources`, `cloudformation:GetResource` | `internal/live/cloudcontrol/client.go` |
| Record store, `s3` | `s3:ListBucket`, `s3:GetObject`, `s3:PutObject`, `s3:PutObjectTagging`, `s3:DeleteObject`, and for the asserted settings `s3:GetBucketVersioning`, `s3:GetLifecycleConfiguration`, `s3:GetBucketPublicAccessBlock`. With a customer managed key, `kms:Decrypt` and `kms:GenerateDataKey`. Measured over an estate's whole life ([claim 37]({{< relref "/docs/claims/the-recommended-secure-configuration" >}})); [IAM]({{< relref "/docs/use/bucket" >}}) has the policy | `internal/live/staterecord/s3.go`, `bucketcontract.go` |
| Record store, `kubernetes` | On Secrets in the records namespace: `get` and `list` for a plan, plus `create`, `update` and `delete` for an apply. For the asserted settings, `create` on `selfsubjectaccessreviews`, which every authenticated identity holds by default | `internal/live/staterecord/kubernetes.go`, `kubernetescontract.go` |
| Record store, `local` | none | `internal/live/staterecord/local.go` |

Each row names the file making the calls. The tagging verbs below move with
botocore across <!-- tagverbs-gen:begin tag-verbs-total -->205<!-- tagverbs-gen:end tag-verbs-total --> services, so they are generated.

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
