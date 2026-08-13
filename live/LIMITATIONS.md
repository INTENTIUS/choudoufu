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
| 1 | a default_* adopter: brings the AWS-created default VPC under management rather than creating one; CloudFormation's AWS::EC2::VPC always creates a new VPC and has no adopt-the-existing-default semantics, so there is no CFN resource of its own to alias to |
| 1 | a generic Cloud Control API passthrough: manages an arbitrary CFN-registry resource type by name via the CCAPI CRUDL operations, with no fixed cloud resource of its own |
| 1 | a one-shot KMS Encrypt API call whose ciphertext result is stored in Terraform state; not a cloud resource |
| 1 | a registration: enables AWS Audit Manager for the account/region (RegisterAccount), a single account-scoped toggle with no cloud resource of its own |
| 1 | a registration: registers an existing member account as CloudTrail's AWS Organizations delegated administrator, with no cloud resource of its own |
| 1 | a settings singleton: reads/writes account-wide AWS Backup settings (e.g. cross-account/cross-region opt-in) via API, with no cloud resource of its own |
| 1 | a settings singleton: reads/writes region-wide AWS Backup service-opt-in settings via API, with no cloud resource of its own |
| 1 | a settings singleton: reads/writes the account-wide Chime SDK Voice logging configuration via API, with no cloud resource of its own |
| 1 | a settings singleton: reads/writes the account/region-wide Bedrock model-invocation logging configuration via API, with no cloud resource of its own |
| 1 | a settings singleton: sets the customer-managed KMS key used to encrypt an existing AgentCore token vault, with no cloud resource of its own |
| 1 | a state-setter: starts/stops an existing aws_instance, no CFN resource of its own |
| 1 | a status toggle for an existing aws_config_configuration_recorder: starts or stops recording via the Config API's Stop/StartConfigurationRecorder actions directly; AWS::Config::ConfigurationRecorder starts recording automatically once created (its own CFN doc: 'AWS CloudFormation starts the recorder as soon as the delivery channel is available... To stop the recorder without deleting it, call the StopConfigurationRecorder action... directly') and exposes no property for this control |
| 1 | adds/removes an IoT thing from a thing group (AddThingToThingGroup); a dynamic relationship with no CFN AWS::IoT type of its own - ThingGroup and Thing are each modeled, but membership is neither a resource nor a property in either's CFN schema |
| 1 | adopts an existing Cognito User Pool Client (e.g. one AWS auto-creates for Managed Login branding or an OpenSearch domain's Cognito authentication) rather than creating one - the provider's own docs say it 'does not create or delete this resource, but instead assumes management of it', the same default_*-adopter shape as aws_default_vpc; no CFN resource of its own |
| 1 | an account-level ECR setting (PutAccountSetting, e.g. default basic scan type); a preference on the account, not a distinct resource, and no CFN AWS::ECR account-settings type exists |
| 1 | an account-level ECS default setting (PutAccountSettingDefault); a preference on the account, not a distinct resource |
| 1 | an account-wide IoT fleet-indexing configuration singleton (UpdateIndexingConfiguration); no CFN model |
| 1 | an account-wide IoT logging configuration singleton (SetV2LoggingOptions); no CFN model |
| 1 | an account/region-wide EMR security setting (PutBlockPublicAccessConfiguration), not a per-cluster resource; no distinct identity, no CFN model |
| 1 | an account/region-wide IoT event-type configuration singleton (UpdateEventConfigurations); no CFN model |
| 1 | an activation action: flips a pending registration to active, with no CFN resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 1 | an activation toggle: enables/disables Cost Explorer tracking for an existing cost allocation tag key, with no cloud resource of its own |
| 1 | an agreement action: accepts a Bedrock foundation model's EULA/offer for the account, with no cloud resource of its own |
| 1 | an agreement action: submits a Bedrock model-access use-case request for the account, with no cloud resource of its own |
| 1 | an association-manager: creates a delegation request for a control set within an existing Audit Manager assessment, with no cloud resource of its own |
| 1 | an association-manager: enables one of AWS's built-in managed Contributor Insights rule templates on an existing resource (import ID is resource_arn,template_name, not a CloudWatch::InsightRule ARN), with no cloud resource of its own |
| 1 | an association-manager: marks an existing App Runner AutoScalingConfiguration version as the account/region default, with no cloud resource of its own |
| 1 | an association-manager: shares an existing custom Audit Manager framework with another account, with no cloud resource of its own |
| 1 | an aws_ami_copy-style copy operation: starts a cross-region/cross-account EBS volume copy and tracks the resulting volume's id; the destination is an ordinary AWS::EC2::Volume but CFN's Volume resource has no cross-region copy-source semantics, so the copy operation itself models nothing CFN provides |
| 1 | an exclusive-set manager: overwrites a CloudFront KeyValueStore's entire key set to exactly what's configured (removing anything else), with no cloud resource of its own beyond the store |
| 1 | an invocation action: triggers a call and records its result, with no CFN resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 1 | an invocation: triggers an App Runner StartDeployment call and tracks its result, with no cloud resource of its own |
| 1 | an operation: reimports/overwrites a REST API's OpenAPI definition via PutRestApi, with no cloud resource of its own (the API itself is aws_api_gateway_rest_api) |
| 1 | an out-of-band tag manager: sets a single tag on an existing DynamoDB resource without owning it, no CFN resource of its own |
| 1 | an out-of-band tag manager: sets a single tag on an existing EC2 resource without owning it, no CFN resource of its own |
| 1 | associates/disassociates a Grafana Enterprise license via a dedicated AssociateLicense/DisassociateLicense API; not any property of AWS::Grafana::Workspace's CFN schema |
| 1 | attaches a single arbitrary tag to an existing ECS resource by ARN (TagResource); no cloud resource of its own |
| 1 | attaches one managed policy across an arbitrary combination of users/roles/groups in a single resource; spans multiple possible CFN parent types at once, so it has no single CFN parent and no distinct identity of its own |
| 1 | authoritatively reconciles the complete set of inline policies on an existing IAM group; a declarative enforcer over another resource's state, not a resource with its own identity |
| 1 | authoritatively reconciles the complete set of inline policies on an existing IAM role; a declarative enforcer over another resource's state, not a resource with its own identity |
| 1 | authoritatively reconciles the complete set of inline policies on an existing IAM user; a declarative enforcer over another resource's state, not a resource with its own identity |
| 1 | authoritatively reconciles the complete set of managed-policy attachments on an existing IAM group; a declarative enforcer over another resource's state, not a resource with its own identity |
| 1 | authoritatively reconciles the complete set of managed-policy attachments on an existing IAM role; a declarative enforcer over another resource's state, not a resource with its own identity |
| 1 | authoritatively reconciles the complete set of managed-policy attachments on an existing IAM user; a declarative enforcer over another resource's state, not a resource with its own identity |
| 1 | creates a Grafana-internal service account inside the workspace; an application-level construct, not a CFN-modeled AWS resource of its own |
| 1 | designates the GuardDuty delegated administrator account org-wide (EnableOrganizationAdminAccount); a singleton, no CFN model |
| 1 | designates the Inspector delegated administrator account org-wide (EnableDelegatedAdminAccount); a singleton, no CFN model |
| 1 | designates the account-wide Firewall Manager administrator account (PutAdminAccount); an account-level singleton, no distinct resource identity, no CFN model |
| 1 | enables IAM Organizations-wide features (centralized root credential management, etc.) via EnableAWSOrganizationsRootCredentialsManagement-style calls; a singleton, no CFN model |
| 1 | enables Inspector scanning for specific resource types across accounts (BatchEnableMember-style call); not a distinct addressable resource |
| 1 | issues a short-lived Grafana workspace API key/credential (CreateWorkspaceApiKey); not a persistent CFN-managed resource |
| 1 | issues a token for a workspace service account; an ephemeral credential revealed once, no persistent CFN resource |
| 1 | locks an existing Glacier vault's policy (InitiateVaultLock/CompleteVaultLock); the vault itself has no CFN model to fold under, and locking is a one-time state transition, not a separate resource |
| 1 | manages the complete, authoritative set of LF-tags on a Data Catalog resource in one call (the successor to the singular resource_lf_tag); not a 1:1 mapping to a single AWS::LakeFormation::TagAssociation resource |
| 1 | maps SSO users/groups to Grafana Admin/Editor roles via UpdatePermissions; not a Workspace property (distinct from the SAML-only RoleValues nested under SamlConfiguration) |
| 1 | org-wide GuardDuty auto-enable configuration (UpdateOrganizationConfiguration); a singleton, no CFN model |
| 1 | org-wide GuardDuty auto-enable configuration, per protection-plan feature; a singleton, no CFN model |
| 1 | org-wide Inspector auto-enable configuration (UpdateOrganizationConfiguration); a singleton, no CFN model |
| 1 | reconfigures the SAML sub-block of an existing domain's fine-grained access control via UpdateElasticsearchDomainConfig; AWS::Elasticsearch::Domain's AdvancedSecurityOptionsInput schema (AnonymousAuthEnabled/Enabled/InternalUserDatabaseEnabled/MasterUserOptions) exposes no SAMLOptions, and this is not a resource of its own |
| 1 | sets a function's runtime-version update mode (PutRuntimeManagementConfig - Auto/FunctionUpdate/Manual); not a property AWS::Lambda::Function's CFN schema exposes, no resource of its own |
| 1 | sets the account's IAM alias (CreateAccountAlias); an account-level singleton, no CFN model |
| 1 | sets the account's global STS endpoint token-version preference (SetSecurityTokenServicePreferences); a singleton, no CFN model |
| 1 | sets the reverse-DNS (PTR) domain name on an existing Elastic IP via ModifyAddressAttribute; AWS::EC2::EIP's CFN schema (Address/Domain/InstanceId/IpamPoolId/NetworkBorderGroup/PublicIpv4Pool/Tags/TransferAddress) has no such property, and no resource of its own |
| 1 | the Glue Data Catalog's single account-wide resource policy (PutResourcePolicy); a singleton with no distinct identity, no CFN model |
| 1 | the account-wide IAM password policy (UpdateAccountPasswordPolicy); a singleton, no CFN model |
| 1 | the account-wide Kinesis Data Streams shard-limit settings (UpdateStreamLimits); a singleton, no CFN model |
| 1 | the account-wide Lake Formation / IAM Identity Center integration configuration (CreateLakeFormationIdentityCenterConfiguration); a singleton, no CFN model |
| 1 | toggles GuardDuty protection-plan features on a member account's detector from the admin account (UpdateMemberDetectors); not a property of the admin's own Detector resource, and AWS::GuardDuty::Member has no Features property |
| 1 | toggles a Lambda function's recursive-invocation loop detection (PutFunctionRecursionConfig); not a property AWS::Lambda::Function's CFN schema exposes, no resource of its own |
| 1 | waiter: records only that DNS validation finished; waiting belongs to the lifecycle layer, not a CFN resource of its own |

**Total.** 101 Terraform AWS resource types that are provider-side constructs, not infrastructure - no CloudFormation counterpart is expected for any of them. Each row's own note is in `live/mapping.json`.
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
| 6 | real Device Farm resource; live/registry.json has zero AWS::DeviceFarm::* types |
| 4 | real DataExchange resource; live/registry.json has zero AWS::DataExchange::* types |
| 4 | searched the registry for an AWS::AppFabric service: no AWS::AppFabric::* type exists anywhere in live/registry.json |
| 3 | real CodeCatalyst resource; live/registry.json has zero AWS::CodeCatalyst::* types (service unmodeled by CFN entirely) |
| 3 | searched the registry for a Chime SDK Voice CFN service: none exists at all |
| 2 | real account-level Cost Optimization Hub setting; live/registry.json has zero AWS::CostOptimizationHub::* types |
| 2 | searched the registry for an AWS::CloudHSM service: none exists anywhere in live/registry.json |
| 1 | a FinSpace Managed kdb+ (Kx) cluster; CFN's FinSpace coverage is only AWS::FinSpace::Environment (the legacy FinSpace platform), with no Kx sub-resources modeled at all |
| 1 | a FinSpace Managed kdb+ (Kx) database; not modeled by CFN's sole AWS::FinSpace::Environment type |
| 1 | a FinSpace Managed kdb+ (Kx) dataview; not modeled by CFN's sole AWS::FinSpace::Environment type |
| 1 | a FinSpace Managed kdb+ (Kx) environment - a distinct resource from the modeled AWS::FinSpace::Environment (the legacy FinSpace platform), created via the separate CreateKxEnvironment API; not itself a CFN type |
| 1 | a FinSpace Managed kdb+ (Kx) scaling group; not modeled by CFN's sole AWS::FinSpace::Environment type |
| 1 | a FinSpace Managed kdb+ (Kx) user; not modeled by CFN's sole AWS::FinSpace::Environment type |
| 1 | a FinSpace Managed kdb+ (Kx) volume; not modeled by CFN's sole AWS::FinSpace::Environment type |
| 1 | a Glacier vault with its own VaultARN; the CFN registry has zero AWS::Glacier types |
| 1 | a Global Accelerator custom-routing accelerator; CFN's GlobalAccelerator types (Accelerator/CrossAccountAttachment/EndpointGroup/Listener) are all standard-routing only, with no CustomRouting* types |
| 1 | a Global Accelerator custom-routing endpoint group; not modeled by any CFN GlobalAccelerator type |
| 1 | a Global Accelerator custom-routing listener; not modeled by any CFN GlobalAccelerator type |
| 1 | a Glue partition index (CreatePartitionIndex), with its own IndexName; AWS::Glue::Partition's schema (CatalogId/DatabaseName/PartitionInput/TableName) has no index-list property, and no separate PartitionIndex CFN type exists |
| 1 | a KMS custom key store (CloudHSM- or XKS-backed) with its own CustomKeyStoreId; not modeled by any CFN KMS type |
| 1 | a KMS grant with its own GrantId; not modeled by any CFN KMS type |
| 1 | a Kendra Experience (search UI) with its own Id/Arn; CFN's Kendra types are DataSource/Faq/Index only |
| 1 | a Kendra query-suggestions block list, a real resource with its own Id; not modeled by CFN's Kendra types |
| 1 | a Kendra thesaurus, a real resource with its own Id; not modeled by CFN's Kendra types |
| 1 | a Kinesis Data Analytics v2 application snapshot with its own SnapshotName; CFN's KinesisAnalyticsV2 types (Application/ApplicationCloudWatchLoggingOption/ApplicationOutput/ApplicationReferenceDataSource) do not include Snapshot |
| 1 | a multi-Region replica of an externally-sourced KMS key; AWS::KMS::ReplicaKey's schema (Description/Enabled/KeyPolicy/PendingWindowInDays/PrimaryKeyArn/Tags) has no key-material-import support, so an EXTERNAL-origin replica is not representable |
| 1 | a purchased ElastiCache reservation (PurchaseReservedCacheNodesOffering); a real, billed resource with its own ReservedCacheNodeId, but no AWS::ElastiCache reservation type is in the CFN registry |
| 1 | a real DynamoDB item (data plane, not control plane) - the canonical cfn-unmodeled shape named in tools/mapping-gen/taxonomy.go's own doc comment alongside aws_s3_object; no AWS::DynamoDB::Item type exists |
| 1 | a real customer data record (not a control-plane resource); live/registry.json's CustomerProfiles types (Domain, DomainObjectType, ObjectType, Integration, ...) model schema/config, not individual profile records - no Profile type |
| 1 | a saved LF-tag expression (a named boolean combination of LF-tags) with its own name; not among CFN's LakeFormation types (DataCellsFilter/DataLakeSettings/Permissions/PrincipalPermissions/Resource/Tag/TagAssociation) |
| 1 | a service-specific credential (e.g. CodeCommit git credentials) with its own ServiceSpecificCredentialId; real and addressable, but CFN's IAM types include AccessKey, not this credential kind |
| 1 | an EMR-on-EKS job template, addressable via its own JobTemplateId/Arn; CFN's EMRContainers coverage (Endpoint/SecurityConfiguration/VirtualCluster) has no JobTemplate type |
| 1 | an Elastic Transcoder pipeline; a real, addressable resource (PipelineId/Arn), but the CFN registry has zero AWS::ElasticTranscoder types at all |
| 1 | an Elastic Transcoder preset; a real, addressable resource (PresetId/Arn), but the CFN registry has zero AWS::ElasticTranscoder types at all |
| 1 | an Elasticsearch/OpenSearch VPC endpoint (cross-account PrivateLink-style access) with its own VpcEndpointId; not modeled by any AWS::Elasticsearch or AWS::OpenSearchService type in the registry |
| 1 | an FSx File Cache with its own FileCacheId/Arn; not present among the CFN registry's FSx types |
| 1 | an FSx backup with its own BackupId/Arn; no AWS::FSx::Backup type is in the CFN registry (only FileSystem/Volume/Snapshot/StorageVirtualMachine/DataRepositoryAssociation/S3AccessPointAttachment are modeled) |
| 1 | an IAM Identity Center identity-store user with its own UserId; CFN's IdentityStore coverage is Group/GroupMembership only, no User type |
| 1 | an uploaded SSH public key for CodeCommit, with its own SSHPublicKeyId; no CFN IAM type models it |
| 1 | an uploaded X.509 signing certificate for a user, with its own CertificateId; no CFN IAM type models it |
| 1 | associates a member account with the Inspector delegated admin (mirroring AWS::GuardDuty::Member's concept); CFN's InspectorV2 types (Filter/CisScanConfiguration/CodeSecurityIntegration/CodeSecurityScanConfiguration) have no Member-equivalent type |
| 1 | opts a principal+resource pair into Lake Formation's hybrid access mode; addressable via ListLakeFormationOptIns with its own identity, not modeled by any CFN LakeFormation type |
| 1 | real CloudWatch log subscription for a directory; live/registry.json's only AWS::DirectoryService types are MicrosoftAD and SimpleAD - no log-subscription type |
| 1 | real Comprehend custom entity recognizer; live/registry.json's only AWS::Comprehend types are DocumentClassifier and Flywheel - no EntityRecognizer type |
| 1 | real DNS conditional-forwarder resource for a directory; live/registry.json's only AWS::DirectoryService types are MicrosoftAD and SimpleAD - neither models sub-features like conditional forwarders |
| 1 | real DataZone resource; live/registry.json's DataZone types (Connection, DataSource, Domain, DomainUnit, Environment, FormType, Project, ...) have no AssetType |
| 1 | real DataZone resource; live/registry.json's DataZone types have no Glossary type |
| 1 | real DataZone resource; live/registry.json's DataZone types have no GlossaryTerm type |
| 1 | real DocumentDB resource; live/registry.json has no AWS::DocDB::DBClusterSnapshot type (CFN cannot create ad-hoc DocDB snapshots) |
| 1 | real EBS resource; live/registry.json has no AWS::EC2::Snapshot type (only the unrelated AWS::EC2::SnapshotBlockPublicAccess account setting) - CFN cannot create ad-hoc EBS snapshots |
| 1 | real EC2 resource (RDMA secondary network, a newer high-performance-networking feature) with its own ARN and identity; live/registry.json's EC2 types have no secondary-network type |
| 1 | real EC2 resource (subnet within an RDMA secondary network) with its own ARN and identity; live/registry.json's EC2 types have no secondary-subnet type |
| 1 | real Elastic Disaster Recovery resource (an actively supported service, not deprecated); live/registry.json has zero AWS::DRS::* types |
| 1 | real MACsec CAK/CKN key association for a DX connection or LAG; live/registry.json's AWS::DirectConnect types have no MACsec key type |
| 1 | real ML capacity-block purchase, a distinct API from regular Capacity Reservations; live/registry.json's only capacity-reservation types are AWS::EC2::CapacityReservation and CapacityReservationFleet - no capacity-block type |
| 1 | real RADIUS MFA settings for a directory; live/registry.json's only AWS::DirectoryService types are MicrosoftAD and SimpleAD - no radius-settings type |
| 1 | real RDS resource; live/registry.json has no AWS::RDS::DBClusterSnapshot type (CFN cannot create ad-hoc RDS snapshots) |
| 1 | real RDS resource; live/registry.json has no AWS::RDS::DBSnapshot type (CFN cannot create ad-hoc RDS snapshots) |
| 1 | real account-level AMI block-public-access setting; live/registry.json's EC2 types have no image-block-public-access type (the registry's only block-public-access types are SnapshotBlockPublicAccess and VPCBlockPublicAccessOptions/Exclusion, neither of which covers AMIs) |
| 1 | real account-level AZ-group opt-in setting; live/registry.json's EC2 types have no availability-zone-group type |
| 1 | real account-level Allowed AMIs setting; live/registry.json's EC2 types have no allowed-images-settings type |
| 1 | real account-level Compute Optimizer setting; live/registry.json's only AWS::ComputeOptimizer type is AutomationRule - no enrollment/status type |
| 1 | real account-level Config setting (evaluation-result retention period); live/registry.json's AWS::Config types have no RetentionConfiguration type |
| 1 | real account-level DevOps Guru setting (cross-service integrations); live/registry.json's DevOpsGuru types have no service-integration type |
| 1 | real account-level DevOps Guru setting (e.g. CloudTrail event source); live/registry.json's DevOpsGuru types (LogAnomalyDetectionIntegration, NotificationChannel, ResourceCollection) have no event-sources-config type |
| 1 | real account-level EC2 Serial Console access setting; live/registry.json's EC2 types have no serial-console-access type |
| 1 | real account/region-level EBS default-encryption-key setting; live/registry.json's EC2 types have no default-KMS-key type (unlike e.g. AWS::EC2::VPCBlockPublicAccessOptions, CFN does not model this particular EBS account default) |
| 1 | real account/region-level EBS encryption-by-default setting; live/registry.json's EC2 types have no encryption-by-default type |
| 1 | real account/region-level IMDS defaults setting; live/registry.json's EC2 types have no instance-metadata-defaults type |
| 1 | real account/region-level default T-instance credit-specification setting; live/registry.json's EC2 types have no default-credit-specification type |
| 1 | real account/resource-level Compute Optimizer setting; live/registry.json's only AWS::ComputeOptimizer type is AutomationRule - no preferences type |
| 1 | real additional-replication-region resource for a MicrosoftAD directory; live/registry.json's AWS::DirectoryService::MicrosoftAD has no inline multi-region property and there is no dedicated Region type |
| 1 | real cross-account proposal resource; AWS::DirectConnect::DirectConnectGatewayAssociation is a single-step CFN resource with no separate propose/accept pair, and live/registry.json has no Proposal type |
| 1 | real cross-region automated-backup replication feature; live/registry.json's AWS::RDS::DBInstance has no property for this and there is no dedicated type |
| 1 | real custom vocabulary resource for Contact Lens; live/registry.json's Connect types have no Vocabulary type |
| 1 | real directory-sharing resource; live/registry.json's only AWS::DirectoryService types are MicrosoftAD and SimpleAD - no sharing type |
| 1 | real per-AZ fast-snapshot-restore enablement for a snapshot; live/registry.json's EC2 types have no fast-snapshot-restore type |
| 1 | real per-organization auto-enable setting for Detective; live/registry.json's Detective types (Graph, MemberInvitation, OrganizationAdmin) have no OrganizationConfiguration type |
| 1 | real per-subnet CIDR reservation resource; live/registry.json's EC2 types have no subnet-cidr-reservation type |
| 1 | real point-in-time export resource (ExportTableToPointInTime); live/registry.json's AWS::DynamoDB types (GlobalTable, Table) have no export type |
| 1 | real resource (legacy AssociateBot API, Lex V1); live/registry.json's Connect types include only the generic IntegrationAssociation, which doesn't clearly correspond 1:1 to this legacy per-purpose association API - no dedicated BotAssociation type |
| 1 | real resource (legacy AssociateLambdaFunction API); live/registry.json's Connect types include only the generic IntegrationAssociation, which doesn't clearly correspond 1:1 to this legacy per-purpose association API - no dedicated LambdaFunctionAssociation type |
| 1 | real resource (repository trigger/notification config); live/registry.json's only AWS::CodeCommit type is Repository - no Trigger type |
| 1 | real resource allocating a hosted connection to another account; live/registry.json's AWS::DirectConnect::Connection type is for the connection owner only - no dedicated hosted-connection allocation type |
| 1 | real resource associating a claimed phone number with a contact flow; live/registry.json's Connect types have no dedicated association type for this pairing |
| 1 | real resource associating a hosted connection with a LAG; live/registry.json's AWS::DirectConnect::Lag has no inline property for adding existing connections and there is no dedicated association type |
| 1 | real resource designating an existing transit gateway route table as the TGW's default association route table; AWS::EC2::TransitGateway's own DefaultRouteTableAssociation property is a create-time enable/disable flag, not a pointer to a specific route table, so this action has no CFN property or type to fold into |
| 1 | real resource designating an existing transit gateway route table as the TGW's default propagation route table; AWS::EC2::TransitGateway's own DefaultRouteTablePropagation property is a create-time enable/disable flag, not a pointer to a specific route table, so this action has no CFN property or type to fold into |
| 1 | real resource importing an external disk image as an EBS snapshot; live/registry.json's EC2 types have no snapshot-import type |
| 1 | real resource referencing a managed prefix list from a transit gateway route table; live/registry.json's EC2 types have no transit-gateway-prefix-list-reference type |
| 1 | real resource registering third-party repo infrastructure (e.g. on-premises GitHub Enterprise Server); live/registry.json's AWS::CodeConnections types are Connection only - no Host type |
| 1 | real resource registering third-party repo infrastructure (e.g. on-premises GitHub Enterprise Server); live/registry.json's AWS::CodeStarConnections types are Connection, RepositoryLink, SyncConfiguration - no Host type |
| 1 | real resource-based policy set via PutResourcePolicy on a CodeBuild project or report group; AWS::CodeBuild::Project's CFN schema has no resource-policy property and live/registry.json has no separate CodeBuild policy type (only Fleet, Project, ReportGroup, SourceCredential) |
| 1 | real resource; live/registry.json's only AWS::CodeCommit type is Repository - no ApprovalRuleTemplate type |
| 1 | real resource; live/registry.json's only AWS::CodeCommit type is Repository - no association type for approval rule templates |
| 1 | real trust-relationship resource between directories; live/registry.json's only AWS::DirectoryService types are MicrosoftAD and SimpleAD - no trust type |
| 1 | searched AWS::ACMPCA: only Certificate, CertificateAuthority, CertificateAuthorityActivation and Permission exist; no Policy type models the resource-based policy PutCertificateAuthorityPolicy attaches to a CA |
| 1 | searched AWS::Amplify: only App, Branch and Domain exist; no BackendEnvironment type models Amplify CLI backend environments |
| 1 | searched AWS::Amplify: only App, Branch and Domain exist; no Webhook type - note this is NOT AWS::CodePipeline::Webhook, a different service's unrelated same-named concept |
| 1 | searched AWS::AppRunner: no type models a custom domain association for a Service |
| 1 | searched AWS::AppRunner: only AutoScalingConfiguration, ObservabilityConfiguration, Service, VpcConnector and VpcIngressConnection exist; no Connection type models the (manually-authorized) source-repository connection |
| 1 | searched AWS::AppSync: schema is modeled only as one whole-document resource (GraphQLSchema's Definition/DefinitionS3Location); no type gives per-GraphQL-type granularity for the incremental CreateType/UpdateType API this TF resource wraps - not AWS::Cassandra::Type, an unrelated same-named type in a different service |
| 1 | searched AWS::Athena: only CapacityReservation, DataCatalog, NamedQuery, PreparedStatement and WorkGroup exist; no Database type (an Athena database is a Glue Data Catalog database under the hood, but that is a different CFN service, not an Athena-scoped match) |
| 1 | searched AWS::AuditManager: only Assessment and AssessmentFramework exist; no Control type models a custom Audit Manager control |
| 1 | searched AWS::AuditManager: only Assessment and AssessmentFramework exist; no type models a generated assessment report, which has its own id/lifecycle distinct from the assessment |
| 1 | searched AWS::Bedrock: no Evaluation type exists for model-evaluation jobs (AWS::BedrockAgentCore::Evaluator is a distinct agent-evaluation concept in a different sub-service) |
| 1 | searched AWS::Bedrock: no Model/CustomModel type exists; fine-tuned/custom models are not registry-modeled |
| 1 | searched AWS::Bedrock: no ProvisionedModelThroughput type exists, despite Provisioned Throughput having its own ARN and create/delete lifecycle |
| 1 | searched AWS::BedrockAgentCore: no Registry type exists among its ApiKeyCredentialProvider/BrowserCustom/.../WorkloadIdentity roster for this MCP-server/agent-skill catalog concept |
| 1 | searched AWS::Chime: only AppInstance, AppInstanceBot and AppInstanceUser exist (Chime SDK Identity); no VoiceConnector type - classic Chime Voice Connector has no CFN modeling at all |
| 1 | searched AWS::Chime: only AppInstance, AppInstanceBot and AppInstanceUser exist; no VoiceConnectorGroup type |
| 1 | searched AWS::Chime: only AppInstance, AppInstanceBot and AppInstanceUser exist; no type models Voice Connector SIP termination credentials |
| 1 | searched AWS::Chime: only AppInstance, AppInstanceBot and AppInstanceUser exist; no type models Voice Connector logging configuration |
| 1 | searched AWS::Chime: only AppInstance, AppInstanceBot and AppInstanceUser exist; no type models Voice Connector media streaming configuration |
| 1 | searched AWS::Chime: only AppInstance, AppInstanceBot and AppInstanceUser exist; no type models Voice Connector origination routing |
| 1 | searched AWS::Chime: only AppInstance, AppInstanceBot and AppInstanceUser exist; no type models Voice Connector termination routing |
| 1 | searched AWS::Cloud9: only EnvironmentEC2 exists; no type models adding a member/permission to an existing Cloud9 environment |
| 1 | searched AWS::CloudFront: KeyValueStore models only the store container; a single key-value pair is a data-plane item with its own import identity (store ARN + key name), the same aws_s3_object-style gap between CFN's control-plane container resource and per-item data |
| 1 | searched AWS::CloudFront: no FieldLevelEncryptionConfig/Profile type exists among its roster (AnycastIpList, CachePolicy, ..., VpcOrigin); field-level encryption has no CFN modeling |
| 1 | searched AWS::CloudFront: no FieldLevelEncryptionProfile type exists; field-level encryption has no CFN modeling |
| 1 | searched AWS::EC2 and AWS::ImageBuilder: no type models a plain custom AMI built from a snapshot/block-device mapping (AWS::ImageBuilder::Image is a distinct pipeline-built artifact, not this resource) |
| 1 | searched AWS::EC2 and AWS::ImageBuilder: no type models creating an AMI from a running instance |
| 1 | searched AWS::EC2: no type models AMI launch-permission grants (ModifyImageAttribute); the AMI itself (aws_ami) is also unmodeled by CFN in this registry |
| 1 | searched AWS::Logs: no type models exporting a log group to S3 Tables; this is a newer feature with no registry counterpart yet |
| 1 | searched AWS::Logs::AccountPolicy, the account-level policy resource: its PolicyType enum is DATA_PROTECTION_POLICY | SUBSCRIPTION_FILTER_POLICY | FIELD_INDEX_POLICY | TRANSFORMER_POLICY | METRIC_EXTRACTION_POLICY - no storage-tier option, and no other Logs type models the account-wide Infrequent-Access storage-tier policy this TF resource sets |
| 1 | searched the registry for a Chime SDK Media Pipelines CFN service: none exists (namesdata-generated.json's own mismatches roster confirms no CFN service normalizes to its AWS SDK id either) |
| 1 | searched the registry for an AWS::Account service (region opt-in/opt-out): no AWS::Account::* type exists anywhere in live/registry.json |
| 1 | searched the registry for an AWS::Account service (the AWS Account API's alternate-contact management): no AWS::Account::* type exists anywhere in live/registry.json |
| 1 | searched the registry for an AWS::Account service: no AWS::Account::* type exists anywhere in live/registry.json |
| 1 | searched the registry for an AWS::AppFabric service: no AWS::AppFabric::* type exists anywhere in live/registry.json (namesdata-generated.json's own mismatches roster confirms no CFN service normalizes to AppFabric's AWS SDK id either) |
| 1 | searched the registry for an AWS::CloudSearch service: none exists anywhere in live/registry.json |
| 1 | searched the registry for an AWS::CloudSearch service: none exists anywhere in live/registry.json (and its own parent domain is likewise unmodeled) |

**Total.** 149 Terraform AWS resource types that are real infrastructure with no CloudFormation Registry model at all. Each row's own note is in `live/mapping.json`.
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
| 361 | no CFN counterpart found by name or curated overlay |

**Total.** 361 Terraform AWS resource types with no CloudFormation Registry counterpart and no terminal classification yet - the count the family sweeps in issue #53's workplan burn down. Each row's own note is in `live/mapping.json`.
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
| `aws_codebuild_webhook` | `AWS::CodeBuild::Project` |
| `aws_codecommit_repository` | `AWS::CodeCommit::Repository` |
| `aws_config_configuration_recorder` | `AWS::Config::ConfigurationRecorder` |
| `aws_config_delivery_channel` | `AWS::Config::DeliveryChannel` |
| `aws_config_organization_custom_policy_rule` | `AWS::Config::OrganizationConfigRule` |
| `aws_config_organization_custom_rule` | `AWS::Config::OrganizationConfigRule` |
| `aws_config_organization_managed_rule` | `AWS::Config::OrganizationConfigRule` |
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
| `aws_ec2_client_vpn_network_association` | `AWS::EC2::ClientVpnTargetNetworkAssociation` |
| `aws_ec2_client_vpn_route` | `AWS::EC2::ClientVpnRoute` |
| `aws_elasticsearch_domain` | `AWS::Elasticsearch::Domain` |
| `aws_elasticsearch_domain_policy` | `AWS::Elasticsearch::Domain` |
| `aws_emr_cluster` | `AWS::EMR::Cluster` |
| `aws_emr_instance_fleet` | `AWS::EMR::InstanceFleetConfig` |
| `aws_emr_instance_group` | `AWS::EMR::InstanceGroupConfig` |
| `aws_emr_managed_scaling_policy` | `AWS::EMR::Cluster` |
| `aws_fsx_lustre_file_system` | `AWS::FSx::FileSystem` |
| `aws_fsx_ontap_file_system` | `AWS::FSx::FileSystem` |
| `aws_fsx_ontap_storage_virtual_machine` | `AWS::FSx::StorageVirtualMachine` |
| `aws_fsx_ontap_volume` | `AWS::FSx::Volume` |
| `aws_fsx_openzfs_file_system` | `AWS::FSx::FileSystem` |
| `aws_fsx_openzfs_snapshot` | `AWS::FSx::Snapshot` |
| `aws_fsx_openzfs_volume` | `AWS::FSx::Volume` |
| `aws_fsx_windows_file_system` | `AWS::FSx::FileSystem` |
| `aws_glue_catalog_table` | `AWS::Glue::Table` |
| `aws_glue_catalog_table_optimizer` | `AWS::Glue::TableOptimizer` |
| `aws_glue_classifier` | `AWS::Glue::Classifier` |
| `aws_glue_connection` | `AWS::Glue::Connection` |
| `aws_glue_data_quality_ruleset` | `AWS::Glue::DataQualityRuleset` |
| `aws_glue_dev_endpoint` | `AWS::Glue::DevEndpoint` |
| `aws_glue_ml_transform` | `AWS::Glue::MLTransform` |
| `aws_glue_partition` | `AWS::Glue::Partition` |
| `aws_glue_security_configuration` | `AWS::Glue::SecurityConfiguration` |
| `aws_glue_workflow` | `AWS::Glue::Workflow` |
| `aws_iam_access_key` | `AWS::IAM::AccessKey` |
| `aws_iam_group_membership` | `AWS::IAM::UserToGroupAddition` |
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

**Total.** 80 types, covered only where the provider's own identity schema reaches (the union `live/survey-full.json` measures). A successor CFN type sometimes exists with working handlers - `AWS::Elasticsearch::Domain` above has no handlers, but its successor `AWS::OpenSearchService::Domain` does; `live/mapping.json` does not yet link `aws_opensearch_domain` to it.
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
