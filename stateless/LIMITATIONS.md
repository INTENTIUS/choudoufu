# Limitations

This file is the limits wing's index. Every construct listed here has a
fixture directory under `stateless/e2e/limits/<name>/`, a minimal, self
contained configuration that loads and triggers exactly the behavior
described, and an assertion in `internal/stateless/lint/limits_test.go` that
pins that behavior today. Doc, fixture, and test are required to agree.
`TestLimitationsDocCoversDirs` fails if this file names a directory that does
not exist or omits one that does, `TestLimitsDirsMatchTable` fails if the test
table drifts from the directory tree, and `TestLimitsEnforced` /
`TestLimitsNotYetEnforced` fail if the lint rule that actually fires stops
matching what is written below. Nothing here is asserted from memory.

One enforced family is indexed elsewhere: the receipt-shape rules
(`RuleReceiptLeaf`, `RuleReceiptValue`, `RuleReceiptSecret`) are specified
alongside the pattern they guard in `stateless/RECEIPTS.md`, and have no
fixture directory here.

Two kinds of entry appear below.

- **Enforced today.** Lint (`internal/stateless/lint`) rejects the
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

**Enforcement.** `RuleProvisioner`, `internal/stateless/lint/lint.go`
(`checkProvisioners`). Fixture at `stateless/e2e/limits/local-exec/`.

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
`stateless/e2e/limits/remote-exec/`.

### null-resource

**Construct.** `null_resource` with a `triggers` map.

**Why banned.** A `null_resource` has no existence outside the record kept
of it, and `triggers` is state used to decide when to re-run something
attached to it. That record is the store. Logical-resource family, per
"Banned, and why".

**Forwarding address.** The receipts pattern. A declared, leaf resource
whose value is a hash of inputs, read back to decide whether an effect needs
to re-run, without any of `null_resource`'s implicit re-trigger machinery.
Documented in `stateless/RECEIPTS.md`.

**Enforcement.** `RuleLogicalResource` (prefix `null_`),
`internal/stateless/lint/admission.go` (`logicalType`). Fixture at
`stateless/e2e/limits/null-resource/`.

### local-file

**Construct.** `local_file`.

**Why banned.** The file's content is generated once and stored in state,
and there is no live system to read it back from on the next run.
Logical-resource family.

**Forwarding address.** A build artifact. Render it as a build step (CI,
a Makefile, a chant task) that produces the file on disk before OpenTofu
runs, not as a resource OpenTofu tracks.

**Enforcement.** `RuleLogicalResource` (prefix `local_`). Fixture at
`stateless/e2e/limits/local-file/`.

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
`stateless/e2e/limits/random-password/`.

### time-sleep

**Construct.** `time_sleep`.

**Why banned.** A `time_*` resource's entire value is "did this already
happen, and when", a question only a stored record answers.
Logical-resource family.

**Forwarding address.** Scheduling in the lifecycle layer. Sequence the
delay in Ops/CI (a wait step, a dependency on an external readiness check),
not as a resource in the graph.

**Enforcement.** `RuleLogicalResource` (prefix `time_`). Fixture at
`stateless/e2e/limits/time-sleep/`.

### remote-state

**Construct.** `data "terraform_remote_state"`.

**Why banned.** It reads a state file, and a marker run has no state file
to read. Named explicitly in "Banned, and why".

**Forwarding address.** Live data sources. Read the same live objects the
other estate reads with a data source of their own type, or pass values
across explicitly as variables or module outputs.

**Enforcement.** `RuleRemoteState`, `internal/stateless/lint/lint.go`
(`checkDataResources`). Fixture at `stateless/e2e/limits/remote-state/`.

### moved-block

**Construct.** A `moved` block.

**Why banned.** It rewrites which state entry belongs to which address, and
there is no state entry to rewrite. Named explicitly in "Banned, and why".

**Forwarding address.** `choudoufu live-mv <old-address> <new-address>`,
the marker rewrite that plays the same role by editing the live
resource's `tofu-address` tag directly (`stateless/MARKERS.md`, "The rename
rule").

**Enforcement.** `RuleMovedBlock`, `internal/stateless/lint/lint.go`
(`checkMovedBlocks`). Fixture at `stateless/e2e/limits/moved-block/`.

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

**Enforcement.** `RuleChildModule`, `internal/stateless/lint/child_module.go`
(`checkChildModules`). Fixture at `stateless/e2e/limits/child-module/`, which is
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

**Enforcement.** `RuleStateBackend`, `internal/stateless/lint/lint.go`
(`checkStateBackends`). Fixture at `stateless/e2e/limits/backend-block/`.

### cloud-block

**Construct.** `terraform { cloud { } }`.

**Why banned.** A remote state backend under another name, with remote
locking attached. The same problem as `backend-block` by a different
syntax.

**Forwarding address.** None. Remove the block, same as `backend-block`.

**Enforcement.** `RuleStateBackend`, the same rule as `backend-block`. The two
fixtures exist separately because they are two distinct HCL forms of one
rule, and each should be provably caught on its own. Fixture at
`stateless/e2e/limits/cloud-block/`.

### unadmitted-type

**Construct.** `aws_instance`, a resource type outside the v0 admission
table.

**Why bounded.** "The admission rule". A type participates only if its
identity is recoverable from the live system with no memory, by one of the
four admission paths. `aws_instance` is in the AWS provider survey
(`stateless/SURVEY.md`, 65 of 68 top types admitted) but is not yet in the
hardcoded v0 table (`internal/stateless/lint/admission.go`, mirrored by
`internal/stateless/identity`'s `DefaultTable`, the copy the sweep and
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
`time_sleep`). `stateless/SURVEY.md`, "The three the rule excludes", has
the full account.

**Forwarding address.** For types awaiting wiring: the provider survey
(`stateless/SURVEY.md`) / v0 admission table, which grows as later phases
add types and, eventually, as provider identity schemas (opentofu#2854)
make most of the table derivable instead of hardcoded. For the three types
the rule excludes: the lifecycle layer, per their entries in
`stateless/SURVEY.md`.

**Enforcement.** `RuleUnadmittedType`, `internal/stateless/lint/lint.go`
(`checkManagedResources`). Fixture at `stateless/e2e/limits/unadmitted-type/`.

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
`stateless/MARKERS.md`) is exempt from this rule.
`markerKeysExemptFromCountIndex` in `count_index.go` allows `count.index`
there and there alone, because a count instance's canonical address
permanently includes its instance index. That is the marker doing its
specified job rather than leaking into an identity-bearing property.
`tofu-slot` is deliberately not among the exempted keys.

**Enforcement.** `RuleCountIndex`, `internal/stateless/lint/count_index.go`
(`checkCountIndex`). It rejects `count.index` anywhere it is reachable from a
managed resource's own configuration body (arguments, tag values, nested
blocks, and conditional/template expressions that reference it indirectly).
The count expression itself and the other meta-argument positions are out of
scope by construction (see `internal/stateless/lint/doc.go`, "Scope of the
count.index rule"). Fixture at `stateless/e2e/limits/count-index-in-tag/`.

### foreach-dotted-key

**Construct.** A `for_each` key containing `.` (e.g. `"a.b"`), or any other
character outside the safe set named below.

**Why banned.** `stateless/MARKERS.md`'s escaping rule for `tofu-address`
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
from `stateless/MARKERS.md` **minus** `.` and `:`. An empty key is also
rejected, since an escaped address cannot end in a bare separator either.

**Enforcement.** `RuleForEachKey`, `internal/stateless/lint/foreach_key.go`
(`checkForEachKeys`). For every `for_each` expression it can evaluate
statically, it rejects any key outside Unicode letters, Unicode digits,
space, and `+ - = _ / @`, including the empty string. The same bound is
enforced a second time in `internal/stateless/identity`
(`checkedForEachKeys` in `foreach_key.go`, which delegates the rune check
back to lint), so a configuration that reaches identity resolution without
passing lint still cannot mint a marker nothing can read back. Fixture at
`stateless/e2e/limits/foreach-dotted-key/`.

### overlong-address

**Construct.** A resource whose escaped `tofu-address` would exceed 256
characters (an absurdly long resource label).

**Why bounded.** AWS caps a tag value at 256 Unicode characters, a hard
limit stated directly in `stateless/MARKERS.md`. "An address that does not fit
is a lint-time error, not a truncation. Silently truncating an ownership
key is worse than refusing to admit the resource."

**Forwarding address.** Shorten the resource address, with a shorter label
or a shorter instance key.

**Enforcement.** `RuleOverlongAddress`, `internal/stateless/lint/overlong_address.go`
(`checkOverlongAddresses`). It escapes each instance address exactly as the
stamped marker would be escaped (per `stateless/MARKERS.md`, `[` becomes `:`
and `]` and `"` are dropped) and rejects anything past 256 Unicode
characters. A plain resource is measured directly, a `for_each` resource is
measured once per statically evaluable key under the same boundary as
`foreach-dotted-key`, and a `count` resource is measured at its highest
index when the count is statically evaluable. Fixture at
`stateless/e2e/limits/overlong-address/`.

## Documented, not yet enforced

### duplicate-identity

**Construct.** Two resource blocks (of an admitted, client-named type) that
resolve to the same identity, such as two `aws_s3_bucket` blocks both
naming bucket `estate-shared`.

**Why bounded.** `stateless/MARKERS.md`, "Ownership semantics". Two live
resources claiming one address is a named error, never a guess. The
identity package enforces the analogous rule on the config side. Two config
blocks that would both bind to the same live object is an ambiguity to
name, not resolve.

**Forwarding address.** Give each resource a distinct client-assigned
identity (a distinct bucket name, role name, etc.). There is no automatic
resolution, by design. The whole point is that a human has to choose.

**Enforcement.** Resolve-time, not lint.
`internal/stateless/identity/resolve.go` (`checkCollisions`), not
`internal/stateless/lint`. This split is intentional and documented rather
than papered over. Lint has no notion of identity, only construct and type
shape, so it cannot see two `bucket` attributes colliding. Identity
resolution runs later in the pipeline and is where that check belongs.
Fixture at `stateless/e2e/limits/duplicate-identity/`, asserted to produce
zero *lint* issues by `TestLimitsNotYetEnforced` (a parallel fixture at
`internal/stateless/identity/testdata/duplicate-identity/` already exercises
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
as a destroy, is asserted in `internal/stateless/lifecycle/exactness_test.go`.
The unadmitted half holds by construction: `internal/stateless/discovery`
builds the sweep universe from `identity.AdmittedTypes()`.)

**Untaggable types cannot be removed by the sweep.** `aws_route`,
`aws_route_table_association`, `aws_s3_bucket_policy`,
`aws_s3_bucket_versioning`, `aws_s3_bucket_public_access_block`,
`aws_s3_bucket_server_side_encryption_configuration`,
`aws_s3_bucket_lifecycle_configuration` and
`aws_iam_role_policy_attachment` carry no tags, so they can carry no
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
(`stateless/e2e/estate/keys.tf`, `dns.tf`). If you need a non-default
value for one of these, a marker run will re-propose it on every plan;
that is the cost, and it is visible rather than silent.
