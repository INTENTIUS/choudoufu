# Limitations

This file is the limits wing's index. Every construct listed here has a
fixture directory under `live/e2e/limits/<name>/`, a minimal, self
contained configuration that loads and triggers exactly the behavior
described, and an assertion in `internal/live/lint/limits_test.go` that
pins that behavior today. Doc, fixture, and test are required to agree.
`TestLimitationsDocCoversDirs` fails if this file names a directory that does
not exist or omits one that does, `TestLimitsDirsMatchTable` fails if the test
table drifts from the directory tree, and `TestLimitsEnforced` /
`TestLimitsNotYetEnforced` fail if the lint rule that actually fires stops
matching what is written below. Nothing here is asserted from memory.

One enforced family is indexed elsewhere: the receipt-shape rules
(`RuleReceiptLeaf`, `RuleReceiptValue`, `RuleReceiptSecret`) are specified
alongside the pattern they guard in `live/RECEIPTS.md`, and have no
fixture directory here.

Two kinds of entry appear below.

- **Enforced today.** Lint (`internal/live/lint`) rejects the
  construct now, with the named rule constant.
- **Documented, not yet enforced.** The mode bans or bounds the construct,
  but no lint check exists yet (or the check lives in a different package,
  at a later phase of the run, than lint). The fixture directory loads
  clean today, and the test asserts zero issues on purpose, so that the day
  enforcement lands, the test fails loudly instead of the gap staying quiet.
  Only new enforcement is allowed to move an entry from this section to the
  one above.

The recurring justification, stated once here rather than in every entry
below, is that every banned feature exists to maintain or repair the store.
That is the test for edge cases.

## Enforced today

### local-exec

**Construct.** `provisioner "local-exec"` on a resource.

**Why banned.** A provisioner runs an effect, not a resource. Whether it
already ran is knowable only from a stored record of the run, which is
exactly the authority live markers give up. The store test applies directly.

**Forwarding address.** The lifecycle layer. Run it in Ops/CI, outside the
plan/apply cycle, where a real execution log can say whether it happened.

**Enforcement.** `RuleProvisioner`, `internal/live/lint/lint.go`
(`checkProvisioners`). Fixture at `live/e2e/limits/local-exec/`.

### remote-exec

**Construct.** `provisioner "remote-exec"` plus the `connection` block that
configures it.

**Why banned.** Same as local-exec, an effect with no stored record of
whether it ran. The connection block only exists to reach a provisioner, so
it is rejected in its own right rather than tolerated once the provisioner
using it is gone.

**Forwarding address.** The lifecycle layer, Ops/CI, same as local-exec.

**Enforcement.** `RuleProvisioner` (fires twice, once for the provisioner
and once for the connection block). Fixture at
`live/e2e/limits/remote-exec/`.

### null-resource

**Construct.** `null_resource` with a `triggers` map.

**Why banned.** A `null_resource` has no existence outside the record kept
of it, and `triggers` is state used to decide when to re-run something
attached to it. That record is the store. Logical-resource family, per
"Banned, and why".

**Forwarding address.** The receipts pattern. A declared, leaf resource
whose value is a hash of inputs, read back to decide whether an effect needs
to re-run, without any of `null_resource`'s implicit re-trigger machinery.
Documented in `live/RECEIPTS.md`.

**Enforcement.** `RuleLogicalResource` (prefix `null_`),
`internal/live/lint/admission.go` (`logicalType`). Fixture at
`live/e2e/limits/null-resource/`.

### local-file

**Construct.** `local_file`.

**Why banned.** The file's content is generated once and stored in state,
and there is no live system to read it back from on the next run.
Logical-resource family.

**Forwarding address.** A build artifact. Render it as a build step (CI,
a Makefile, a chant task) that produces the file on disk before OpenTofu
runs, not as a resource OpenTofu tracks.

**Enforcement.** `RuleLogicalResource` (prefix `local_`). Fixture at
`live/e2e/limits/local-file/`.

### random-password

**Construct.** `random_password`.

**Why banned.** The generated value only exists because state remembered
it, and regenerating it from the live system is impossible by construction.
A random value has no live twin. Logical-resource family.

**Forwarding address.** A secret-store Op. Generate and store the secret
in a secret manager (outside OpenTofu's model entirely), and have
configuration reference it by ARN/path, never by value. The same forwarding
applies to `tls_*`, banned for the same reason.

**Enforcement.** `RuleLogicalResource` (prefix `random_`). Fixture at
`live/e2e/limits/random-password/`.

### time-sleep

**Construct.** `time_sleep`.

**Why banned.** A `time_*` resource's entire value is "did this already
happen, and when", a question only a stored record answers.
Logical-resource family.

**Forwarding address.** Scheduling in the lifecycle layer. Sequence the
delay in Ops/CI (a wait step, a dependency on an external readiness check),
not as a resource in the graph.

**Enforcement.** `RuleLogicalResource` (prefix `time_`). Fixture at
`live/e2e/limits/time-sleep/`.

### remote-state

**Construct.** `data "terraform_remote_state"`.

**Why banned.** It reads a state file, and a marker run has no state file
to read. Named explicitly in "Banned, and why".

**Forwarding address.** Live data sources. Read the same live objects the
other estate reads with a data source of their own type, or pass values
across explicitly as variables or module outputs.

**Enforcement.** `RuleRemoteState`, `internal/live/lint/lint.go`
(`checkDataResources`). Fixture at `live/e2e/limits/remote-state/`.

### moved-block

**Construct.** A `moved` block.

**Why banned.** It rewrites which state entry belongs to which address, and
there is no state entry to rewrite. Named explicitly in "Banned, and why".

**Forwarding address.** `choudoufu live-mv <old-address> <new-address>`,
the marker rewrite that plays the same role by editing the live
resource's `tofu-address` tag directly (`live/MARKERS.md`, "The rename
rule").

**Enforcement.** `RuleMovedBlock`, `internal/live/lint/lint.go`
(`checkMovedBlocks`). Fixture at `live/e2e/limits/moved-block/`.

### child-module

**Construct.** A `module` block, at any depth.

**Why banned.** Live markers v0 are a root-module mode. Identity
resolution, discovery, marker stamping and the projection all stop at the
root, and module expansion (`count` or `for_each` on a module block)
changes every resource address inside the module, which is exactly what a
`tofu-address` marker records. Binding markers under an expansion that can
renumber them is the ambiguity the marker exists to remove.

**Forwarding address.** Move the module's resources into the root module,
or give the module an estate of its own, with its own directory, its own
`live` block, and its own `estate` name. Two estates are two independent
runs, which is the separation a child module was standing in for.

**Enforcement.** `RuleChildModule`, `internal/live/lint/child_module.go`
(`checkChildModules`). Fixture at `live/e2e/limits/child-module/`, which is
a tree rather than a single file and needs `choudoufu get` before the rule can
be reached, since an uninstalled module block is refused while the
configuration is still being loaded, earlier than any marker code runs.
The five packages downstream of lint (`identity`, `discovery`, `stamp`,
`projection`, `mv`) each still refuse a configuration with children, but as a
one-line internal invariant. Lint runs first in both commands, so reaching one
of them with a child module means the pipeline ran out of order.

### backend-block

**Construct.** `terraform { backend "..." { } }`.

**Why banned.** A backend configures where authoritative state is stored and
locked. A marker run has no state file to store and nothing for a lock to
protect. Named explicitly in "Banned, and why".

**Forwarding address.** None. Remove the block. The projection (rebuilt
from the live system every run, discarded after) replaces what a backend
would have stored, and there is nothing to point it at instead.

**Enforcement.** `RuleStateBackend`, `internal/live/lint/lint.go`
(`checkStateBackends`). Fixture at `live/e2e/limits/backend-block/`.

### cloud-block

**Construct.** `terraform { cloud { } }`.

**Why banned.** A remote state backend under another name, with remote
locking attached. The same problem as `backend-block` by a different
syntax.

**Forwarding address.** None. Remove the block, same as `backend-block`.

**Enforcement.** `RuleStateBackend`, the same rule as `backend-block`. The two
fixtures exist separately because they are two distinct HCL forms of one
rule, and each should be provably caught on its own. Fixture at
`live/e2e/limits/cloud-block/`.

### unadmitted-type

**Construct.** `aws_instance`, a resource type outside the v0 admission
table.

**Why bounded.** "The admission rule". A type participates only if its
identity is recoverable from the live system with no memory, by one of the
four admission paths. `aws_instance` is in the AWS provider survey
(`live/SURVEY.md`, 65 of 68 top types admitted) but is not yet in the
hardcoded v0 table (`internal/live/lint/admission.go`, mirrored by
`internal/live/identity`'s `DefaultTable`, the copy the sweep and
identity resolution read). This is a scoping boundary, not a permanent ban.

Two kinds of type hit this rule, and the error message does not distinguish
them. Most out-of-table types are like `aws_instance`: the survey admits
them in principle and they are simply not wired yet, which is the scoping
boundary described above. Three surveyed types are out by the admission
rule itself, with no wiring batch ever coming: `aws_iam_access_key` and
`aws_secretsmanager_secret_version` (credentials, whose identity is born
server-side alongside a secret that can never be read again; they become a
lifecycle-layer Op writing to the secret store, referenced by ARN or
pointer, never by value, the same forwarding `random_password` gets above)
and `aws_acm_certificate_validation` (a waiter pretending to be a
resource; it moves to lifecycle sequencing, the same forwarding as
`time_sleep`). `live/SURVEY.md`, "The three the rule excludes", has
the full account.

**Forwarding address.** For types awaiting wiring: the provider survey
(`live/SURVEY.md`) / v0 admission table, which grows as later phases
add types and, eventually, as provider identity schemas (opentofu#2854)
make most of the table derivable instead of hardcoded. For the three types
the rule excludes: the lifecycle layer, per their entries in
`live/SURVEY.md`.

**Enforcement.** `RuleUnadmittedType`, `internal/live/lint/lint.go`
(`checkManagedResources`). Fixture at `live/e2e/limits/unadmitted-type/`.

### count-index-in-tag

**Construct.** `count.index` interpolated into a tag value
(`tags = { Name = "vpc-${count.index}" }`).

**Why banned.** Per "Banned, and why", `count.index` in an identity-bearing
property is banned, because a marker written from an index has no
correspondence once instances are added, removed, or reordered. The
replacement is a `for_each` key, which is stable by construction.

**Forwarding address.** `for_each`. Key the resource by a stable string
instead of a positional index.

**Deliberate carve-out.** The `tofu-address` marker tag value (see
`live/MARKERS.md`) is exempt from this rule.
`markerKeysExemptFromCountIndex` in `count_index.go` allows `count.index`
there and there alone, because a count instance's canonical address
permanently includes its instance index. That is the marker doing its
specified job rather than leaking into an identity-bearing property.
`tofu-slot` is deliberately not among the exempted keys.

**Enforcement.** `RuleCountIndex`, `internal/live/lint/count_index.go`
(`checkCountIndex`). It rejects `count.index` anywhere it is reachable from a
managed resource's own configuration body (arguments, tag values, nested
blocks, and conditional/template expressions that reference it indirectly).
The count expression itself and the other meta-argument positions are out of
scope by construction (see `internal/live/lint/doc.go`, "Scope of the
count.index rule"). Fixture at `live/e2e/limits/count-index-in-tag/`.

### foreach-dotted-key

**Construct.** A `for_each` key containing `.` (e.g. `"a.b"`), or any other
character outside the safe set named below.

**Why banned.** `live/MARKERS.md`'s escaping rule for `tofu-address`
cannot unambiguously round-trip a key containing `.` or `:`. The escaped
address format uses `.` as the segment separator and `:` to introduce an
instance key, so either character inside a key collides with the escaping
rule itself. Both are AWS-legal in a tag value, which is exactly what made
this class of key dangerous before this rule existed. It passed lint,
stamped a marker, and applied cleanly, and only the *next* run found the
wedge (a `:` key reads back as a malformed marker, and a `.` key makes
`discovery.UnescapeAddress` refuse on deletion), with no in-band way out.

**Forwarding address.** Pick a `for_each` key drawn from the intersection of
the AWS-allowed tag character set and the two escaping separators removed.
That means letters, digits, space, and `+ - = _ / @`, the AWS tag-value set
from `live/MARKERS.md` **minus** `.` and `:`. An empty key is also
rejected, since an escaped address cannot end in a bare separator either.

**Enforcement.** `RuleForEachKey`, `internal/live/lint/foreach_key.go`
(`checkForEachKeys`). For every `for_each` expression it can evaluate
statically, it rejects any key outside Unicode letters, Unicode digits,
space, and `+ - = _ / @`, including the empty string. The same bound is
enforced a second time in `internal/live/identity`
(`checkedForEachKeys` in `foreach_key.go`, which delegates the rune check
back to lint), so a configuration that reaches identity resolution without
passing lint still cannot mint a marker nothing can read back. Fixture at
`live/e2e/limits/foreach-dotted-key/`.

### overlong-address

**Construct.** A resource whose escaped `tofu-address` would exceed 256
characters (an absurdly long resource label).

**Why bounded.** AWS caps a tag value at 256 Unicode characters, a hard
limit stated directly in `live/MARKERS.md`. "An address that does not fit
is a lint-time error, not a truncation. Silently truncating an ownership
key is worse than refusing to admit the resource."

**Forwarding address.** Shorten the resource address, with a shorter label
or a shorter instance key.

**Enforcement.** `RuleOverlongAddress`, `internal/live/lint/overlong_address.go`
(`checkOverlongAddresses`). It escapes each instance address exactly as the
stamped marker would be escaped (per `live/MARKERS.md`, `[` becomes `:`
and `]` and `"` are dropped) and rejects anything past 256 Unicode
characters. A plain resource is measured directly, a `for_each` resource is
measured once per statically evaluable key under the same boundary as
`foreach-dotted-key`, and a `count` resource is measured at its highest
index when the count is statically evaluable. Fixture at
`live/e2e/limits/overlong-address/`.

## Documented, not yet enforced

### duplicate-identity

**Construct.** Two resource blocks (of an admitted, client-named type) that
resolve to the same identity, such as two `aws_s3_bucket` blocks both
naming bucket `estate-shared`.

**Why bounded.** `live/MARKERS.md`, "Ownership semantics". Two live
resources claiming one address is a named error, never a guess. The
identity package enforces the analogous rule on the config side. Two config
blocks that would both bind to the same live object is an ambiguity to
name, not resolve.

**Forwarding address.** Give each resource a distinct client-assigned
identity (a distinct bucket name, role name, etc.). There is no automatic
resolution, by design. The whole point is that a human has to choose.

**Enforcement.** Resolve-time, not lint.
`internal/live/identity/resolve.go` (`checkCollisions`), not
`internal/live/lint`. This split is intentional and documented rather
than papered over. Lint has no notion of identity, only construct and type
shape, so it cannot see two `bucket` attributes colliding. Identity
resolution runs later in the pipeline and is where that check belongs.
Fixture at `live/e2e/limits/duplicate-identity/`, asserted to produce
zero *lint* issues by `TestLimitsNotYetEnforced` (a parallel fixture at
`internal/live/identity/testdata/duplicate-identity/` already exercises
the resolve-time error itself).

## Behavioral limits (runtime, not lint)

The entries above are lint matters. Each has a fixture directory and an
asserted rule. The limits below are runtime behaviors of the implemented
mode, documented here and asserted by the integration tests named in each.

**Removal coverage is the admission table.** A resource block deleted from
the configuration is planned as a destroy because the estate-wide sweep
lists every type in the admission table for this estate's markers. A
resource carrying this estate's markers at a type *outside* the admission
table is not swept and its deletion is not planned. Adoption is a tag
write, so this is reachable by hand-stamping markers onto an unadmitted
type. The markers are the contract, and the admission table is the list of
types the contract is defined over. (The sweep half, a deleted block planned
as a destroy, is asserted in `internal/live/lifecycle/exactness_test.go`.
The unadmitted half holds by construction: `internal/live/discovery`
builds the sweep universe from `identity.AdmittedTypes()`.)

**Untaggable types cannot be removed by the sweep.** <!-- survey-gen:begin untaggable-admitted -->
`aws_cloudwatch_dashboard`, `aws_ecr_registry_policy`,
`aws_ecr_registry_scanning_configuration`,
`aws_ecr_replication_configuration`, `aws_iam_role_policy`,
`aws_iam_role_policy_attachment`, `aws_kms_alias`,
`aws_lambda_layer_version`, `aws_lb_target_group_attachment`, `aws_route`,
`aws_route53_record`, `aws_route_table_association`,
`aws_s3_bucket_lifecycle_configuration`, `aws_s3_bucket_policy`,
`aws_s3_bucket_public_access_block`,
`aws_s3_bucket_server_side_encryption_configuration`,
`aws_s3_bucket_versioning`, `aws_sns_topic_policy` and
`aws_sqs_queue_policy`<!-- survey-gen:end untaggable-admitted --> carry no tags, so they can carry no
ownership marker and the sweep has nothing to search on. Their identity is
built from their own configuration, which means deleting the resource
block deletes the only record of which resource it was. Destroy the
resource before removing its block, or delete it out of band. Every plan
names these types under "Not swept for removal".

**An import-derived prior state cannot hold config-only attributes.** A
provider attribute that the cloud does not store and the configuration
does not set has no value in a projection, since no read can return it.
When the resource changes for any other reason, the provider's default
arrives in the diff as a null-to-default line beside the real change.
`aws_security_group`'s `revoke_rules_on_delete` is the case in the v0
subset. This is not specific to marker runs. A stock `choudoufu import` of
the same resource followed by the same drift prints the identical line. It
is cosmetic (the attribute is only consulted on delete) and not
recoverable at OpenTofu's layer, because provider defaults live in the SDK
and not in the schema OpenTofu is served.

The same gap stops being cosmetic when the configuration *does* set such
an argument. Then the projection holds the null the read returned, the
configuration holds the written value, and every plan proposes the same
in-place update forever — a standing non-empty plan rather than a stray
line beside a real change. `aws_kms_key`'s `deletion_window_in_days` and
`aws_route53_zone`'s `force_destroy` are the two in the v0 subset: KMS and
Route 53 never return either one, and both are consulted only on destroy.
The estate fixture leaves both at their defaults for exactly this reason
(`live/e2e/estate/keys.tf`, `dns.tf`). If you need a non-default
value for one of these, a marker run will re-propose it on every plan;
that is the cost, and it is visible rather than silent.

## Exclusion cohorts

The residue roster (issue #49): the entries above are what *is* implemented
and enforced. This section names what is not, as a set instead of an
implication — every cohort a resource type can be excluded by, with a
count and a one-sentence reason each, so that "not covered" answers a
per-type question without reading code. `live/residue.go` is the source:
every count below is either computed straight from `live/mapping.json`
(issue #43) and `live/registry.json` (issue #42), or, where a table cannot
carry the judgment, curated data with its evidence in that file's own
comments, the same way `internal/live/lint/admission.go`'s `opsExcluded`
carries the credential and waiter judgments above. A type in none of these
cohorts and also outside the v0 admission table is simply not wired yet —
the scoping boundary the unadmitted-type entry above already describes,
not an exclusion.

When a refused type falls in one of these cohorts, `internal/live/lint`'s
admission refusal names it: one more sentence, in the schema clause's own
voice, appended to the base "not in the table" message. A type in no
cohort gets nothing appended. `internal/live/lint/residue_test.go` pins one
such refusal per cohort below that a TF configuration can actually name;
`CFN-only constructs` cannot be, since no Terraform type maps to a
CloudFormation-only construct, so that cohort is doc-only.

#### Deprecated or EOL services

Seven AWS services this fork holds out of scope by policy: retired,
end-of-life, or being wound down. The service list and its judgment are
curated (`live/residue.go`'s `DeprecatedServices`); each service's
registry-side footprint is computed against `live/registry.json`.

<!-- survey-gen:begin residue-deprecated -->
| Service | TF prefix | CFN registry types | Reason |
|---|---|---|---|
| Pinpoint | `aws_pinpoint_` | 19 | a service AWS is retiring (end-of-support October 30, 2026) |
| Greengrass V1 | `aws_greengrass_` | 16 | a service AWS has superseded with Greengrass V2, shipping no new features for V1 |
| WAF Classic | `aws_waf_` | 7 | a service AWS has superseded with the unified WAFv2 API |
| WAF Classic Regional | `aws_wafregional_` | 11 | a service AWS has superseded with the unified WAFv2 API, same as WAF Classic |
| App Mesh | `aws_appmesh_` | 7 | a service AWS has closed to new customers and is winding down |
| AppStream 2.0 | `aws_appstream_` | 13 | a service this fork holds out of scope by policy |
| DAX | `aws_dax_` | 3 | a service (DynamoDB Accelerator) this fork holds out of scope by policy |

**Total.** 76 CloudFormation Registry types across 7 services.

7 Terraform types carry `live/mapping.json`'s own `via: "deprecated-service"` (issue #53): a TF prefix under one of the services above whose entire CFN Registry footprint ships no working handler at all, so a family sweep can never recover a real mapping for it either.
<!-- survey-gen:end residue-deprecated -->

#### CloudFormation-only constructs

CloudFormation Registry types that are CloudFormation's own template and
stack mechanics, not AWS infrastructure — the CFN-side mirror of this
fork's logical-resource family above (`null_resource`, `local_file`,
`random_password`, `time_sleep`): a construct whose whole value is
something a template-processing engine tracks, with no live twin any
provider could read back. The list is curated
(`live/residue.go`'s `CFNOnlyConstructs`); the "no TF counterpart" claim
that goes with it is computed, checked against `live/mapping.json` at
package load and again by `tools/survey-gen`'s drift test.

<!-- survey-gen:begin residue-cfn-only -->
| CFN type | Reason |
|---|---|
| `AWS::CloudFormation::WaitCondition` | a template-internal signal that a stack step finished, not an AWS resource |
| `AWS::CloudFormation::WaitConditionHandle` | the pre-signed URL a WaitCondition polls, existing only to be written to |
| `AWS::CloudFormation::Macro` | registers a template preprocessor for CloudFormation's own macro transform, not infrastructure |
| `AWS::CloudFormation::CustomResource` | a template escape hatch to an arbitrary Lambda-backed handler, with no AWS resource of its own |

**Total.** 4 constructs, none counted against coverage: no Terraform configuration can name a CloudFormation-only type, so none can ever be refused either.
<!-- survey-gen:end residue-cfn-only -->

#### The terminal taxonomy (issue #53)

`live/mapping.json`'s `via: "none"` used to be a single catch-all: 754 of
the roster's 1,691 Terraform types, "no CFN counterpart," no further word on
why. Issue #53 replaces the shrug with a taxonomy: every `none` row is now
either evidenced into one of three terminal classes below, or left
`none` — an explicitly counted **unclassified** remainder, never a silent
default. `tools/mapping-gen` assigns `tf-only` and `deprecated-service`
mechanically, each requiring corroboration beyond a name match before it
classifies anything (a name pattern plus the provider's own schema showing
no importable identity, for `tf-only`; a TF prefix's entire CloudFormation
Registry footprint shipping no working handler, for `deprecated-service`);
`cfn-unmodeled` is curated only, in `tools/mapping-gen/overlay.json`'s
`cfn_unmodeled` table, since proving a real resource has no CFN model at all
is a per-family judgment call this pass does not make on its own. The three
sections below and "Unclassified Terraform types" further down are what the
754 split into after this pass; a drift test (`TestNoBareNoneOnceEnforced`
in `tools/mapping-gen/mapping_gen_test.go`) forbids the unclassified count
from ever regrowing past where a later family sweep leaves it, and, once
the last sweep lands, forbids `none` outright.

#### TF-only constructs

Terraform provider constructs with no cloud resource of their own: waiters,
`aws_ami_copy`-style one-shot operations, `default_*` adopters that bring an
already-existing AWS default resource under management rather than
creating one. `via: "tf-only"` in `live/mapping.json`.

<!-- survey-gen:begin residue-tf-only -->
| Count | Note |
|---|---|
| 17 | an acceptance-side waiter: flips a pending cross-account request (an invitation, a peering or attachment offer) to accepted, with no cloud resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 6 | an aws_ami_copy-style copy operation: starts a copy and tracks its result, with no CFN resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 5 | a default_* adopter: brings an AWS-created default resource under management rather than creating one, with no CFN resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 2 | a registration action: enrolls an account or resource into a feature, with no CFN resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 1 | a confirmation waiter: flips a pending request to confirmed, with no CFN resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 1 | a declarative exclusive-set manager: owns a hosted zone's entire recordset list on apply (any record not listed is removed), rather than the per-record aws_route53_record resource. The provider's own docs state destroying it halts management but does not delete the configured records - it never owned an AWS-side object of its own. |
| 1 | a declarative exclusive-set manager: replaces a RAM resource share's entire principal and resource-association lists on apply, rather than adding one item the way aws_ram_principal_association/aws_ram_resource_association do; the provider's own docs call it out as incompatible with mixing those per-item resources on the same share. No CFN resource of its own - it only ever sets AWS::RAM::ResourceShare's existing Principals/ResourceArns properties wholesale. |
| 1 | a region-level default-certificate override: overrides the system-default SSL/TLS CA certificate new RDS instances/clusters in the region will use going forward. Keyed by region, not an individual DB resource; deleting it reverts to the region's system default. No AWS::RDS::Certificate (or equivalent) type exists in the registry (RDS's modeled types are CustomDBEngineVersion, DBCluster, DBClusterParameterGroup, DBInstance, DBParameterGroup, DBProxy*, DBSecurityGroup*, DBShardGroup, DBSubnetGroup, EventSubscription, GlobalCluster, Integration, OptionGroup). |
| 1 | a running/stopped state toggle on an existing RDS instance (start/stop only); the provider's own docs state destroying this resource is a no-op and does not modify the instance's AWS-side state. No cloud resource of its own - it acts on an instance mapped elsewhere. |
| 1 | an account-level feature toggle enabling AWS Organizations-wide Service Catalog portfolio sharing; the entire resource is one required boolean (enabled), and the provider's own docs carry no Import section at all. No cloud resource of its own. |
| 1 | an account-level feature-enablement action (EnableSharingWithAwsOrganization): toggles RAM's AWS Organizations sharing integration for the whole account. No arguments beyond the toggle, keyed by account ID, no cloud resource of its own. |
| 1 | an account/region-level feature toggle (Enabled/Disabled) controlling whether SageMaker AI Projects may use Service Catalog; its only arguments are region and status, and its import identity is the region itself, not a resource ID. No cloud resource of its own. |
| 1 | an activation action: flips a pending registration to active, with no CFN resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 1 | an invocation action: triggers a call and records its result, with no CFN resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 1 | waiter: records only that DNS validation finished; waiting belongs to the lifecycle layer, not a CFN resource of its own |

**Total.** 41 Terraform AWS resource types that are provider-side constructs, not infrastructure - no CloudFormation counterpart is expected for any of them. Each row's own note is in `live/mapping.json`.
<!-- survey-gen:end residue-tf-only -->

#### CFN-unmodeled resources

Real, live AWS resources the CloudFormation Registry simply does not model
(the canonical example is `aws_s3_object`, though it is not yet classified
here — proving the negative is a family sweep's job, not this pass's).
`via: "cfn-unmodeled"` in `live/mapping.json`; curated only, so this table is
empty until a sweep adds its first entry.

<!-- survey-gen:begin residue-cfn-unmodeled -->
| Count | Note |
|---|---|
| 1 | a Redshift Partner Integration record tying a cluster/database to an AWS-authorized partner name, with partner-driven status fields Terraform doesn't control. No AWS::Redshift Partner type exists in the registry (see aws_redshift_authentication_profile's note for the full Redshift type list). |
| 1 | a named JSON authentication-profile config object with its own identifier. No AWS::Redshift::AuthenticationProfile (or equivalent) type exists in the registry - Redshift's modeled types are Cluster, ClusterParameterGroup, ClusterSecurityGroup, ClusterSecurityGroupIngress, ClusterSubnetGroup, EndpointAccess, EndpointAuthorization, EventSubscription, Integration, ScheduledAction, SnapshotSchedule only. |
| 1 | a one-time RDS Reserved Instance purchase (a capacity/pricing commitment); the provider's own docs state it cannot be deleted through the API - destroy only removes it from Terraform state while the real reservation runs its term. No RDS reservation type exists in the registry (see aws_rds_cluster_endpoint's note for the full RDS type list). |
| 1 | a real HSM client certificate object (with its own public key, to be registered in the customer's HSM out of band). No AWS::Redshift HSM type exists in the registry (see aws_redshift_authentication_profile's note for the full Redshift type list). |
| 1 | a real HSM configuration object (server IP, partition name, certs). Same gap as aws_redshift_hsm_client_certificate - no AWS::Redshift HSM type exists in the registry. |
| 1 | a real IAM Identity Center managed application bound to Redshift, with its own AWS-generated ARN. No AWS::Redshift IdcApplication (or equivalent) type exists in the registry (see aws_redshift_authentication_profile's note for the full Redshift type list). |
| 1 | a real Savings Plan purchase; the provider's own docs state an active plan cannot be cancelled or deleted - destroying it only removes it from Terraform state while the real commitment runs its term (only a still-queued, not-yet-active plan can genuinely be deleted). No AWS::SavingsPlans service exists in live/registry.json's roster at all (verified against the registry's full service list) - Savings Plans aren't a CFN-modeled resource kind. |
| 1 | a real custom log source with its own provisioned infrastructure (a Glue crawler IAM role, cross-account trust via provider_identity, OCSF event-class config). No AWS::SecurityLake::CustomLogSource type exists in the registry - only AwsLogSource (for AWS-native sources), DataLake, Subscriber, and SubscriberNotification are modeled. |
| 1 | a real, AWS-issued usage-limit object (RPU-hours or cross-region datasharing) with its own identifier. No AWS::RedshiftServerless UsageLimit type exists in the registry - RedshiftServerless's modeled types are Namespace, Snapshot, Workgroup only. |
| 1 | a real, AWS-issued usage-limit object (Spectrum data scanned, concurrency-scaling time, or cross-region datasharing caps) with its own identifier, distinct from the cluster it's scoped to. No AWS::Redshift UsageLimit type exists in the registry (see aws_redshift_authentication_profile's note for the full Redshift type list). |
| 1 | a real, named KMS-key grant object for cross-region encrypted snapshot copies, created in the destination region. No AWS::Redshift SnapshotCopyGrant type exists in the registry (see aws_redshift_authentication_profile's note for the full Redshift type list). |
| 1 | a real, per-VPC DNS Firewall fail-open setting (GetFirewallConfig/UpdateFirewallConfig), distinct from AWS::Route53Resolver::ResolverConfig (which only covers the unrelated AutodefinedReverseFlag setting). No AWS::Route53Resolver::FirewallConfig type exists in the registry. |
| 1 | a real, reusable Route 53 delegation set object with its own AWS-assigned ID and ARN, usable across multiple hosted zones. No AWS::Route53 DelegationSet type exists in the registry - Route53's modeled types are CidrCollection, DNSSEC, HealthCheck, HostedZone, KeySigningKey, RecordSet, RecordSetGroup only. |
| 1 | a real, standalone Augmented AI human-review workflow with its own name/ARN. No AWS::SageMaker::FlowDefinition type exists in the registry (SageMaker's ~39 modeled types include Domain, Endpoint, Model, ModelPackageGroup, Pipeline, ... but no FlowDefinition). |
| 1 | a real, standalone SageMaker training job with its own identifier and an ongoing lifecycle (the provider's own docs document an update timeout and cleanup-on-destroy flags, not a fire-and-forget run). No AWS::SageMaker::TrainingJob type exists in the registry (see aws_sagemaker_flow_definition's note). |
| 1 | a real, standalone Security Hub custom action target with its own ARN, wireable to EventBridge automations. No AWS::SecurityHub::ActionTarget type exists in the registry (SecurityHub's modeled types are AggregatorV2, AutomationRule, AutomationRuleV2, ConfigurationPolicy, Connector, ConnectorV2, DelegatedAdmin, FindingAggregator, Hub, HubV2, Insight, OrganizationConfiguration, PolicyAssociation, ProductSubscription, SecurityControl, Standard only). |
| 1 | a real, standalone private labeling workforce (Cognito- or OIDC-backed) with its own name/ARN. No AWS::SageMaker::Workforce type exists in the registry - only the related but distinct AWS::SageMaker::Workteam is modeled. |
| 1 | a real, standalone worker-facing UI template with its own name/ARN, used by human-review workflows. No AWS::SageMaker::HumanTaskUi type exists in the registry (see aws_sagemaker_flow_definition's note). |
| 1 | a real, standing cross-account authorization grant (CreateVPCAssociationAuthorization/DeleteVPCAssociationAuthorization) permitting another account's VPC to associate with a local private hosted zone. No AWS::Route53 authorization type exists in the registry (see aws_route53_delegation_set's note for the full Route53 type list) - cross-account authorization grants for Route53 aren't modeled. |
| 1 | a real, tracked RDS/Aurora snapshot-to-S3 export task (StartExportTask) with its own identifier and status/progress attributes. No snapshot or export-task type exists among RDS's registry types (see aws_rds_cluster_endpoint's note for the full list) - CFN does not model RDS snapshots or their exports at all. |
| 1 | a real, tracked SageMaker Ground Truth labeling job with completion telemetry (status, label counters, failure reason). No AWS::SageMaker::LabelingJob type exists in the registry (see aws_sagemaker_flow_definition's note). |
| 1 | a real, tracked SageMaker hyperparameter tuning job; the provider's own docs note Terraform does not wait for the job to reach a terminal state before returning. No AWS::SageMaker::HyperParameterTuningJob type exists in the registry (see aws_sagemaker_flow_definition's note). |
| 1 | a real, tracked manual Redshift cluster snapshot object with its own identifier and retention settings. No AWS::Redshift snapshot type exists in the registry (see aws_redshift_authentication_profile's note for the full Redshift type list). |
| 1 | a real, tracked one-shot export of a SageMaker model card to S3, with its own ARN-shaped identifier. No AWS::SageMaker::ModelCardExportJob type exists in the registry - only AWS::SageMaker::ModelCard (the card itself) is modeled, not its export jobs. |
| 1 | a real, versioned Route 53 traffic-policy document with its own ID and AWS-managed version number. No AWS::Route53 TrafficPolicy type exists in the registry (see aws_route53_delegation_set's note for the full Route53 type list). |
| 1 | adds a real AWS account as a Security Hub member of the calling admin account, optionally sending an invitation. No AWS::SecurityHub::Member type exists in the registry (see aws_securityhub_action_target's note for the full SecurityHub type list). |
| 1 | associates a Redshift datashare with a consumer (account, ARN, or region); same gap as aws_redshift_data_share_authorization - no AWS::Redshift DataShare type exists in the registry. |
| 1 | associates a custom domain name and ACM certificate with a Redshift Serverless workgroup. AWS::RedshiftServerless::Workgroup's full property list (verified against the CFN template reference: BaseCapacity, ConfigParameters, EnhancedVpcRouting, MaxCapacity, NamespaceName, Port, PricePerformanceTarget, PubliclyAccessible, RecoveryPointId, SecurityGroupIds, SnapshotArn/Name/OwnerAccount, SubnetIds, Tags, TrackName, Workgroup, WorkgroupName) has no custom-domain property at all. |
| 1 | associates an AWS Budget with a Service Catalog product or portfolio (a compound budget_name:resource_id link). No AWS::ServiceCatalog::BudgetResourceAssociation type exists in the registry (ServiceCatalog's modeled types are AcceptedPortfolioShare, CloudFormationProduct, CloudFormationProvisionedProduct, LaunchNotificationConstraint, LaunchRoleConstraint, LaunchTemplateConstraint, Portfolio, PortfolioPrincipalAssociation, PortfolioProductAssociation, PortfolioShare, ResourceUpdateConstraint, ServiceAction, ServiceActionAssociation, StackSetConstraint, TagOption, TagOptionAssociation only). |
| 1 | associates an existing cluster with an existing AWS::Redshift::SnapshotSchedule. AWS::Redshift::Cluster's full property list (verified against the CFN template reference) has no SnapshotSchedule/ScheduleIdentifier-linking property, and SnapshotSchedule itself is scoped independently - the cluster-to-schedule association isn't modeled by either type. |
| 1 | attaches a cross-account resource policy to the singleton per-account S3 Access Grants instance. No AWS::S3::AccessGrantsInstanceResourcePolicy type exists in the registry, and AccessGrantsInstance's own schema (primary identifier AccessGrantsInstanceArn; read-only AccessGrantsInstanceArn/Id; create-only Tags) carries no resource-policy property. |
| 1 | attaches a resource policy document to a Redshift Serverless resource ARN, generically. No AWS::RedshiftServerless ResourcePolicy type exists in the registry, and neither Namespace nor Workgroup's schema exposes a resource-policy property. |
| 1 | attaches a resource policy document to an arbitrary Redshift resource ARN (cluster, namespace, or snapshot, per the provider's own docs - not scoped to one resource kind). AWS::Redshift::Cluster does carry its own NamespaceResourcePolicy property, but since this TF resource is generic across resource kinds Redshift doesn't even give CFN types to (there is no Redshift snapshot type at all), pinning it to Cluster alone would misrepresent every non-cluster use - not modeled as a single fold target. |
| 1 | attaches a resource policy to an S3 Access Point. No AWS::S3::AccessPointPolicy type exists in the registry (verified against the full AWS::S3::* type list: AccessGrant, AccessGrantsInstance, AccessGrantsLocation, AccessPoint, Bucket, BucketPolicy, MultiRegionAccessPoint, MultiRegionAccessPointPolicy, StorageLens, StorageLensGroup - only AWS::S3ObjectLambda::AccessPointPolicy is modeled, for a different access-point kind). |
| 1 | configures where an existing CloudWatch RUM app monitor sends extended metrics (CloudWatch, Evidently, ...) via PutRumMetricsDestination. AWS::RUM::AppMonitor's full property list (verified against the CFN template reference: AppMonitorConfiguration, CustomEvents, CwLogEnabled, DeobfuscationConfiguration, Domain, DomainList, Name, Platform, ResourcePolicy, Tags) has no metric-destination property, and RUM has no other registry type. |
| 1 | creates a distinct VPC endpoint object (its own vpc_endpoint_id, DNS address, network interfaces) for a Redshift Serverless workgroup, not merely a workgroup setting. No AWS::RedshiftServerless EndpointAccess type exists in the registry - RedshiftServerless's modeled types are Namespace, Snapshot, Workgroup only. |
| 1 | deploys a CloudFormation stack from a Serverless Application Repository application (application_id + semantic_version as the template source), with SAR-specific capabilities (CAPABILITY_RESOURCE_POLICY, CAPABILITY_AUTO_EXPAND). No AWS::ServerlessApplicationRepository service exists in live/registry.json's roster at all (verified against the registry's full service list) - the deployed artifact is CloudFormation's own Stack primitive, not a Registry type representing SAR itself. |
| 1 | executes a Redshift Data API SQL statement (ExecuteStatement) and tracks its own AWS-issued statement ID through a real status state machine (SUBMITTED/STARTED/FINISHED/FAILED/ABORTED). AWS::RedshiftServerless carries no Data-API type, and there is no separate AWS::RedshiftData CFN service at all - the executed statement is real tracked activity, not a CFN-modeled resource. |
| 1 | grants a Redshift datashare authorization to a consumer account (or ADX); no AWS::Redshift DataShare/DataShareAuthorization type exists in the registry at all (see aws_redshift_authentication_profile's note) - Redshift data sharing predates/lacks CFN Registry support. |
| 1 | manages a custom Aurora cluster endpoint (a curated subset of readers, exposed under its own DNS name). No AWS::RDS::DBClusterEndpoint (or equivalent) type exists in live/registry.json - RDS's modeled types are CustomDBEngineVersion, DBCluster, DBClusterParameterGroup, DBInstance, DBParameterGroup, DBProxy, DBProxyEndpoint, DBProxyTargetGroup, DBSecurityGroup, DBSecurityGroupIngress, DBShardGroup, DBSubnetGroup, EventSubscription, GlobalCluster, Integration, OptionGroup only. |
| 1 | manages a real DS (delegation signer) record and its AWS-generated signing key for a Route53-registered domain's parent zone. No AWS::Route53Domains service exists in live/registry.json's roster at all (verified against the registry's full service list). |
| 1 | manages a specific security control's enablement status within a specific standard - a compound identity (security_control_id, standards_arn). AWS::SecurityHub::SecurityControl's primary identifier is SecurityControlId alone with no standards_arn dimension, so the per-standard scope this resource manages isn't what that type's identity models. |
| 1 | manages one provisioning artifact (product version) with its own AWS-issued ID and independent create/update/delete lifecycle. AWS::ServiceCatalog::CloudFormationProduct only accepts ProvisioningArtifactParameters at creation plus a write-only whole-list ReplaceProvisioningArtifacts operation on update (per live/registry.json) - it has no per-artifact identity or lifecycle of its own, so an individual artifact's independent management isn't modeled. |
| 1 | manages settings (contacts, name servers, auto-renew, transfer lock) on a domain already registered with Route53 Domains; the provider's own docs note destroy does not deregister the domain. Same gap as aws_route53domains_domain - no AWS::Route53Domains service exists in the registry. |
| 1 | materializes a traffic-policy version into actual DNS records at a name, with its own UUID identifier. No AWS::Route53 TrafficPolicyInstance type exists in the registry (see aws_route53_delegation_set's note for the full Route53 type list). |
| 1 | references a SageMaker JumpStart public-hub model into a private Hub. AWS::SageMaker::Hub is modeled, but no AWS::SageMaker::HubContent/HubContentReference type exists in the registry - referencing content into a hub isn't modeled, only the hub container itself. |
| 1 | registers a Redshift (serverless or provisioned) namespace with the AWS Glue Data Catalog; the provider's own docs note AWS provides no reliable API to verify registration status. Neither AWS::Redshift nor AWS::RedshiftServerless carries a Glue Data Catalog registration type. |
| 1 | registers, renews, and deregisters a real domain name via Route53 Domains - an actual purchase/registration lifecycle. No AWS::Route53Domains service exists in live/registry.json's roster at all (verified against the registry's full service list). |
| 1 | replicates a single S3 Tables table (by table_arn) to destination bucket(s). AWS::S3Tables::Table's full property list (verified against the CFN template reference: Compaction, IcebergMetadata, Namespace, OpenTableFormat, SnapshotManagement, StorageClassConfiguration, TableBucketARN, TableName, Tags, WithoutMetadata) has no Replication property - only the table-bucket-level AWS::S3Tables::TableBucket carries ReplicationConfiguration (see aws_s3tables_table_bucket_replication's fold). |
| 1 | sets the S3 account-level Public Access Block configuration (a singleton per account). No AWS::S3::AccountPublicAccessBlock (or equivalent account-scoped) type exists in the registry - only the per-bucket PublicAccessBlockConfiguration property on AWS::S3::Bucket is modeled, a different scope entirely. |
| 1 | sets traffic-dial percentages on an existing S3 Multi-Region Access Point's routes; the provider's own docs note destroying this resource only removes it from state and does not reset the real routing configuration. AWS::S3::MultiRegionAccessPoint's registry entry has no update handler at all (handlers: create/read/delete/list true, update false) - CFN cannot modify an MRAP's routes after creation, so ongoing route management isn't modeled. |
| 1 | toggles RDS/Aurora Database Activity Streams on an existing cluster (StartActivityStream/StopActivityStream), keyed by the parent cluster's own ARN with no independent stream identity. AWS::RDS::DBCluster's full property list (verified against the CFN template reference: AllowEngineModeChange, AssociatedRoles, ... EnableCloudwatchLogsExports, ... - no ActivityStream* property anywhere) has no activity-stream property; the feature isn't modeled at all. |
| 1 | toggles an individual legacy standards control, identified by its own per-standard-scoped ARN (standards_control_arn). AWS::SecurityHub::SecurityControl's primary identifier is SecurityControlId alone (the newer, standard-independent unified control view, per the registry) - a per-standard control ARN isn't the identity CFN models here. |
| 1 | uploads/manages an object's content in an S3 bucket - a data-plane operation (PutObject/DeleteObject), not account/control-plane infrastructure the CloudFormation Registry models. Deprecated by the provider in favor of aws_s3_object (identical functionality); see that row for the canonical evidence. |
| 1 | uploads/manages an object's content in an S3 bucket - a real, live piece of data-plane activity, but not a control-plane resource the CloudFormation Registry models (there is no AWS::S3::Object type; S3's modeled types are AccessGrant, AccessGrantsInstance, AccessGrantsLocation, AccessPoint, Bucket, BucketPolicy, MultiRegionAccessPoint, MultiRegionAccessPointPolicy, StorageLens, StorageLensGroup only). |

**Total.** 55 Terraform AWS resource types that are real infrastructure with no CloudFormation Registry model at all. Each row's own note is in `live/mapping.json`.
<!-- survey-gen:end residue-cfn-unmodeled -->

#### Unclassified Terraform types

Every `via: "none"` row of `live/mapping.json` still standing after the
terminal taxonomy above: a Terraform AWS resource type the join found no
CloudFormation Registry counterpart for, by name, curated overlay, or
mechanical classifier. Entirely computed — grouped by the row's own note.
The registry-backed admission path (issue #40) cannot reach any type in
this cohort by definition; the survey's other admission paths (client-named,
parent-derived) are unaffected and may still reach one.

<!-- survey-gen:begin residue-unmapped -->
| Count | Note |
|---|---|
| 596 | no CFN counterpart found by name or curated overlay |

**Total.** 596 Terraform AWS resource types with no CloudFormation Registry counterpart and no terminal classification yet - the count the family sweeps in issue #53's workplan burn down. Each row's own note is in `live/mapping.json`.
<!-- survey-gen:end residue-unmapped -->

#### Registry-laggard live services

Terraform types the join *did* map to a CloudFormation type, where that
CloudFormation Registry entry ships no working handler at all (`create`,
`read`, `update`, `delete` and `list` all false) — the registry-backed
admission path resolves the identity question but has nothing to read or
list against. Covered only where the provider's own identity schema
reaches, which is what the union `live/survey-full.json` measures.
Entirely computed against `live/mapping.json` and `live/registry.json`,
excluding types already counted under "Deprecated or EOL services" above.

<!-- survey-gen:begin residue-laggard -->
| TF type | CFN type |
|---|---|
| `aws_alb_listener_certificate` | `AWS::ElasticLoadBalancingV2::ListenerCertificate` |
| `aws_appsync_api_cache` | `AWS::AppSync::ApiCache` |
| `aws_appsync_api_key` | `AWS::AppSync::ApiKey` |
| `aws_autoscalingplans_scaling_plan` | `AWS::AutoScalingPlans::ScalingPlan` |
| `aws_budgets_budget` | `AWS::Budgets::Budget` |
| `aws_cloud9_environment_ec2` | `AWS::Cloud9::EnvironmentEC2` |
| `aws_codebuild_project` | `AWS::CodeBuild::Project` |
| `aws_codebuild_report_group` | `AWS::CodeBuild::ReportGroup` |
| `aws_codebuild_source_credential` | `AWS::CodeBuild::SourceCredential` |
| `aws_codecommit_repository` | `AWS::CodeCommit::Repository` |
| `aws_config_configuration_recorder` | `AWS::Config::ConfigurationRecorder` |
| `aws_config_delivery_channel` | `AWS::Config::DeliveryChannel` |
| `aws_dlm_lifecycle_policy` | `AWS::DLM::LifecyclePolicy` |
| `aws_dms_replication_instance` | `AWS::DMS::ReplicationInstance` |
| `aws_dms_replication_subnet_group` | `AWS::DMS::ReplicationSubnetGroup` |
| `aws_dms_replication_task` | `AWS::DMS::ReplicationTask` |
| `aws_docdb_cluster` | `AWS::DocDB::DBCluster` |
| `aws_docdb_cluster_instance` | `AWS::DocDB::DBInstance` |
| `aws_docdb_cluster_parameter_group` | `AWS::DocDB::DBClusterParameterGroup` |
| `aws_docdb_subnet_group` | `AWS::DocDB::DBSubnetGroup` |
| `aws_ec2_client_vpn_authorization_rule` | `AWS::EC2::ClientVpnAuthorizationRule` |
| `aws_ec2_client_vpn_endpoint` | `AWS::EC2::ClientVpnEndpoint` |
| `aws_ec2_client_vpn_route` | `AWS::EC2::ClientVpnRoute` |
| `aws_elasticsearch_domain` | `AWS::Elasticsearch::Domain` |
| `aws_emr_cluster` | `AWS::EMR::Cluster` |
| `aws_glue_classifier` | `AWS::Glue::Classifier` |
| `aws_glue_connection` | `AWS::Glue::Connection` |
| `aws_glue_data_quality_ruleset` | `AWS::Glue::DataQualityRuleset` |
| `aws_glue_dev_endpoint` | `AWS::Glue::DevEndpoint` |
| `aws_glue_ml_transform` | `AWS::Glue::MLTransform` |
| `aws_glue_partition` | `AWS::Glue::Partition` |
| `aws_glue_security_configuration` | `AWS::Glue::SecurityConfiguration` |
| `aws_glue_workflow` | `AWS::Glue::Workflow` |
| `aws_iam_access_key` | `AWS::IAM::AccessKey` |
| `aws_iot_policy_attachment` | `AWS::IoT::PolicyPrincipalAttachment` |
| `aws_iot_thing_principal_attachment` | `AWS::IoT::ThingPrincipalAttachment` |
| `aws_kinesis_analytics_application` | `AWS::KinesisAnalytics::Application` |
| `aws_lakeformation_data_lake_settings` | `AWS::LakeFormation::DataLakeSettings` |
| `aws_lakeformation_permissions` | `AWS::LakeFormation::Permissions` |
| `aws_lakeformation_resource` | `AWS::LakeFormation::Resource` |
| `aws_lb_listener_certificate` | `AWS::ElasticLoadBalancingV2::ListenerCertificate` |
| `aws_media_convert_queue` | `AWS::MediaConvert::Queue` |
| `aws_media_store_container` | `AWS::MediaStore::Container` |
| `aws_medialive_channel` | `AWS::MediaLive::Channel` |
| `aws_medialive_input` | `AWS::MediaLive::Input` |
| `aws_medialive_input_security_group` | `AWS::MediaLive::InputSecurityGroup` |
| `aws_qldb_ledger` | `AWS::QLDB::Ledger` |
| `aws_route53_record` | `AWS::Route53::RecordSet` |
| `aws_sagemaker_code_repository` | `AWS::SageMaker::CodeRepository` |
| `aws_sagemaker_endpoint_configuration` | `AWS::SageMaker::EndpointConfig` |
| `aws_sagemaker_notebook_instance` | `AWS::SageMaker::NotebookInstance` |
| `aws_sagemaker_notebook_instance_lifecycle_configuration` | `AWS::SageMaker::NotebookInstanceLifecycleConfig` |
| `aws_sagemaker_workteam` | `AWS::SageMaker::Workteam` |
| `aws_service_discovery_http_namespace` | `AWS::ServiceDiscovery::HttpNamespace` |
| `aws_service_discovery_instance` | `AWS::ServiceDiscovery::Instance` |
| `aws_service_discovery_private_dns_namespace` | `AWS::ServiceDiscovery::PrivateDnsNamespace` |
| `aws_service_discovery_public_dns_namespace` | `AWS::ServiceDiscovery::PublicDnsNamespace` |
| `aws_ses_receipt_filter` | `AWS::SES::ReceiptFilter` |
| `aws_ses_receipt_rule` | `AWS::SES::ReceiptRule` |
| `aws_ses_receipt_rule_set` | `AWS::SES::ReceiptRuleSet` |

**Total.** 60 types, covered only where the provider's own identity schema reaches (the union `live/survey-full.json` measures). A successor CFN type sometimes exists with working handlers - `AWS::Elasticsearch::Domain` above has no handlers, but its successor `AWS::OpenSearchService::Domain` does; `live/mapping.json` does not yet link `aws_opensearch_domain` to it.
<!-- survey-gen:end residue-laggard -->

#### Emulator-blocked

Types wireable against real AWS but not provable against the floci
emulator today (issue #26). Two are already admitted and carry standing
e2e residue (`live/e2e/run.sh`'s `RESIDUE_UNOWNED` and `RESIDUE_CHANGED`);
three were kept out of a wiring slice entirely because the emulator gap
leaves nothing to prove admission against. Which floci behavior blocks
which type is read off the harness and the admission table, not derivable
from any artifact, so this roster is curated
(`live/residue.go`'s `EmulatorBlocked`).

<!-- survey-gen:begin residue-emulator -->
| Type | Admitted today | Reason |
|---|---|---|
| `aws_db_instance` | no | RDS only works fully against floci when the docker socket is mounted into the emulator container, which this harness does not do (lex00/floci#28) |
| `aws_iam_role` | yes (standing e2e residue) | floci's iam:GetRole omits Tags, so the role's own marker never reads back and every plan reports it unowned |
| `aws_s3_bucket_policy` | yes (standing e2e residue) | downstream of aws_iam_role's residue: its policy document embeds the unowned role's ARN, so its own plan never settles |

**Total.** 3 types.
<!-- survey-gen:end residue-emulator -->
