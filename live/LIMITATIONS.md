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

#### Logical resources: a three-way classification (GitHub issue #73)

`internal/live/lint`'s per-type table (`logical_type.go`,
`ClassifyLogicalType`) replaces the old family-prefix-only refusal with a
policy-grade classification, one of three:

- **RECORD_ADMITTED** - `null_resource`, `terraform_data`, `time_static`,
  `time_offset`, `time_rotating`, `time_sleep`, `random_id`, `random_pet`,
  `random_shuffle`, `random_integer`. None of these generates or holds
  secret material in any output, verified against each provider's own
  documentation (see `logical_type.go`'s `logicalTypes` table for the
  per-type citation). **Conditionally admitted as of #73's projection
  work:** refused exactly as before when the `live` block configures no
  `record_store`, and admitted the moment one is - the record store's key
  namespace is the "no persisted micro-state" limit closing, not a
  reinterpretation of what these types are. A `record_store` block backs
  the type's whole identity with a persisted record instead of a cloud
  observation (`internal/live/staterecord`, local/SSM/S3 backends); see
  `website/docs/language/live-markers.mdx` for the config surface. Without
  a store, the refusal Detail names this class and cites #73 exactly as it
  always has.
- **SECRET_REFUSED** - `random_password`, `random_bytes`, and the `tls_`
  family (`tls_private_key`, `tls_self_signed_cert`,
  `tls_locally_signed_cert`, `tls_cert_request`, and any future `tls_`
  addition by default). Each generates, or requires as an argument, secret
  material a live-markers run has nowhere safe to keep: no state file, and
  no persisted micro-state record either, since the no-secrets rule that
  already governs snapshots and receipts forbids a record from carrying it
  too. Refused permanently, with or without a `record_store` configured -
  the store never weakens this class.
- **OTHER_REFUSED** - `local_*` and any other logical-family member this
  table has no more specific opinion about. Refused for the original
  reason, in the original wording: nobody has done the per-type
  verification work for this group that the other two classes required, so
  the honest default is "still refused, nothing more to say yet" rather
  than a guess in either direction.

### null-resource

**Construct.** `null_resource` with a `triggers` map.

**Why banned (without a record store).** A `null_resource` has no existence
outside the record kept of it, and `triggers` is state used to decide when
to re-run something attached to it. That record is the store.
Logical-resource family, per "Banned, and why".

**Admitted with a record store.** Classified `RECORD_ADMITTED` (see above):
once the `live` block configures a `record_store`, `null_resource` runs
through the stock provider lifecycle against prior state hydrated from and
persisted to the store (`internal/live/projection`'s hydration and
write-back). No new syntax at the resource block itself - the same
`null_resource` block that used to be refused now plans and applies.

**Forwarding address (no record store).** The receipts pattern. A
declared, leaf resource whose value is a hash of inputs, read back to
decide whether an effect needs to re-run, without any of `null_resource`'s
implicit re-trigger machinery. Documented in `live/RECEIPTS.md`.

**Enforcement.** `RuleLogicalResource`, classified `RECORD_ADMITTED`
(`internal/live/lint/logical_type.go`, `ClassifyLogicalType`), gated on
`record_store` being absent. Fixture at `live/e2e/limits/null-resource/`
(no store, still refused); the admitted path is exercised by
`live/e2e/record-store/`.

### terraform-data

**Construct.** `terraform_data`.

**Why banned (without a record store).** The same logical-resource story as
`null_resource`: its `id` and `output` are minted once and remembered, not
observed from anything live. Logical-resource family. It shares no
type-name prefix with `null_resource` or any other logical type, so before
this table it was missing from the admission code's prefix list entirely
(GitHub issue #73's audit finding) and fell through to the generic "not in
the v0 admission table" refusal (`unadmitted-type`) instead of this one. It
is admitted to `internal/live/lint/logical_type.go`'s per-type table by
exact type name rather than by a shared prefix, which is what closes that
gap.

**Admitted with a record store.** Same as `null_resource`: classified
`RECORD_ADMITTED`, and once a `record_store` is configured it runs through
the stock provider lifecycle with prior state hydrated from and persisted
to the store. `triggers_replace` is graph-internal plumbing either way -
see `live/RECEIPTS.md`'s boundary section for why it stays out of the
receipts pattern even once `terraform_data` itself is admitted.

**Forwarding address (no record store).** Same as `null_resource`: the
receipts pattern.

**Enforcement.** `RuleLogicalResource`, classified `RECORD_ADMITTED`
(`internal/live/lint/logical_type.go`, `ClassifyLogicalType`), gated on
`record_store` being absent. Fixture at `live/e2e/limits/terraform-data/`
(no store, still refused); the admitted path is exercised by
`live/e2e/record-store/`.

### local-file

**Construct.** `local_file`.

**Why banned.** The file's content is generated once and stored in state,
and there is no live system to read it back from on the next run.
Logical-resource family.

**Forwarding address.** A build artifact. Render it as a build step (CI,
a Makefile, a chant task) that produces the file on disk before OpenTofu
runs, not as a resource OpenTofu tracks.

**Enforcement.** `RuleLogicalResource`, classified `OTHER_REFUSED` (see
above; `internal/live/lint/logical_type.go`, `ClassifyLogicalType`).
Fixture at `live/e2e/limits/local-file/`.

### random-password

**Construct.** `random_password`.

**Why banned.** The generated value only exists because state remembered
it, and regenerating it from the live system is impossible by construction.
A random value has no live twin. Logical-resource family.

**Forwarding address.** A secret-store Op. Generate and store the secret
in a secret manager (outside OpenTofu's model entirely), and have
configuration reference it by ARN/path, never by value. The same forwarding
applies to `tls_*`, banned for the same reason. Classified `SECRET_REFUSED`
(see above): refused permanently, with no record-store forwarding address,
unlike this family's `RECORD_ADMITTED` neighbors - configuring a
`record_store` does nothing for this type, by design.

**Enforcement.** `RuleLogicalResource`, classified `SECRET_REFUSED`
(`internal/live/lint/logical_type.go`, `ClassifyLogicalType`). Fixture at
`live/e2e/limits/random-password/`.

### time-sleep

**Construct.** `time_sleep`.

**Why banned (without a record store).** A `time_*` resource's entire value
is "did this already happen, and when", a question only a stored record
answers. Logical-resource family.

**Admitted with a record store.** Classified `RECORD_ADMITTED` (see above):
`time_sleep` and the rest of the `time_*` family (`time_static`,
`time_offset`, `time_rotating`) run through the stock provider lifecycle
once a `record_store` is configured, the timestamp persisted to and read
back from the store rather than a state file.

**Forwarding address (no record store).** Scheduling in the lifecycle
layer. Sequence the delay in Ops/CI (a wait step, a dependency on an
external readiness check), not as a resource in the graph.

**Enforcement.** `RuleLogicalResource`, classified `RECORD_ADMITTED`
(`internal/live/lint/logical_type.go`, `ClassifyLogicalType`), gated on
`record_store` being absent. Fixture at `live/e2e/limits/time-sleep/` (no
store, still refused); the admitted path is exercised by
`live/e2e/record-store/`.

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

**Construct.** A `module` block, at any depth, expanded with `count`, or
expanded with `for_each` whose keys cannot be enumerated from configuration
alone. A static module call, and a `for_each` module call whose keys *can*
be so enumerated, are not this limitation: see below.

**Why banned.** Module expansion by `count` renumbers every resource
address inside the module positionally, on every insertion or removal
above the changed index, and a `tofu-address` marker records an address,
not a position. A renumbering that moves addresses out from under their
markers is not a gap this mode intends to close, so `count`-expanded
modules are refused permanently. `for_each` on a module block does not
renumber the way `count` does - a key is stable under insertion and
removal, the same reason `RuleForEachKey`-disciplined resource keys are
admitted - which is what makes it worth admitting at all (issue #59, phase
3 / "59c"). What is still refused is a `for_each` whose keys this pass
cannot compute before anything is read from the cloud: an instance key
becomes part of every address inside the module, and an address that is
not knowable yet cannot become part of a marker yet either, the same reason
a resource's own non-static `for_each` is refused (by identity resolution,
not lint - see below).

**A static module call is admitted.** As of issue #59, phase 2 ("59b"), the
five packages downstream of lint - `identity`, `discovery`, `stamp`,
`projection`, `mv` - traverse `cfg.Children` recursively, and a resource
inside a static module binds by its module-qualified address
(`module.a.module.b.aws_x.y`) exactly as soundly as a root resource binds by
its own. `RuleChildModule` reports nothing for a module call that sets
neither `count` nor `for_each`. A `provider` block declared inside that
static module is a separate, still-open question (per-module provider
resolution, issue #70): it is neither supported nor refused today - the
module's resources are silently served by the root configuration's own
provider config instead - and `lint.CheckModuleProviders`
(`internal/live/lint/module_provider.go`) only warns about it by name, once
per run, rather than failing the run.

**A statically-keyed `for_each` module call is admitted.** As of issue #59,
phase 3 ("59c"), a module call's `for_each` is evaluated the same way a
resource's own `for_each` is: a literal collection, or one built from
variables, locals, `path` and `terraform` values. When every key is
knowable that way, `RuleChildModule` reports nothing, and the five packages
traverse each instance - `module.app["prod"].aws_x.y` binds exactly as
soundly as `module.app.aws_x.y` does. Two further, separate rules apply to
a module call this one admits, mirroring the rules a resource's own
`for_each` is already held to: `RuleForEachKey` rejects an individual key
that cannot survive the trip through a `tofu-address` marker (a `.` or a
`:`, or anything outside the AWS tag-value character set), and
`RuleOverlongAddress` rejects an expanded instance whose escaped address
does not fit in a 256-character tag value. A `for_each` this pass cannot
evaluate at all - a reference to a resource, a data source, or anything
else outside the static scope - is refused by `RuleChildModule` itself,
worded like a resource's own non-static `for_each` refusal.

**Forwarding address.** For a `count`-expanded module, or a `for_each`
module whose keys are not statically knowable: move the module's resources
into the root module, or give the module an estate of its own, with its own
directory, its own `live` block, and its own `estate` name. Two estates are
two independent runs, which is the separation an expanded child module is
standing in for. For `count` this is the only forwarding address - there is
no future traversal to wait for. For a non-static `for_each`, rewriting the
expression to a literal collection or a value derived from variables,
locals, `path` or `terraform` is the other way out, the same as it is for a
resource's own `for_each`.

**Enforcement.** `RuleChildModule`, `internal/live/lint/child_module.go`
(`checkChildModules`, detail text chosen by `childModuleDetail`, which
reports nothing for a static call or a statically-keyed `for_each` call).
The key evaluation itself is `identity.ChildModuleKeys`
(`internal/live/identity/modulepath.go`), shared with `resolve.go`'s own
module walk so that lint's admission verdict and identity resolution's
traversal never disagree about which keys a module call expands to.
Fixture at `live/e2e/limits/child-module/`, which is a tree rather than a
single file and needs `choudoufu get` before the rule can be reached, since
an uninstalled module block is refused while the configuration is still
being loaded, earlier than any marker code runs. The fixture carries four
module calls - a static call ("network", admitted), a statically-keyed
`for_each` call ("keyed-static", admitted), a `count` call ("counted",
refused permanently), and a `for_each` call whose keys reference another
resource ("keyed", refused as non-static) - so one load proves both
admitted shapes pass clean while the other two still fail, each for its own
named reason.

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

**Construct.** `aws_cloudwatch_event_rule`, a resource type outside the v0
admission table.

**Why bounded.** "The admission rule". A type participates only if its
identity is recoverable from the live system with no memory, by one of the
four admission paths. `aws_cloudwatch_event_rule` is in the AWS provider
survey (`live/SURVEY.md`, 65 of 68 top types admitted) but is not in the
hardcoded v0 table (`internal/live/lint/admission.go`, mirrored by
`internal/live/identity`'s `DefaultTable`, the copy the sweep and identity
resolution read).
`aws_nat_gateway` held this fixture's place until the EC2 networking
ratification batch (issue #65) admitted it; `aws_cloudwatch_event_rule`
takes over as a replacement stabler than "not yet wired" can offer: its
documented import id is `event_bus_name/rule_name`, where `event_bus_name`
silently defaults to the account's default bus when omitted from
configuration — a literal fallback for an omitted argument, not just a
separator, which is a [`Component`](internal/live/identity/table.go) this
table's vocabulary does not have yet. Four separate ratification batches
(messaging, DynamoDB periphery, RDS, ECS/EKS) have already reached this
exact type and cited the identical gap rather than wiring it, so it is a
proven-stable pick rather than a type the next batch is likely to close.
This is a scoping boundary, not a permanent ban — a future batch that adds
the missing `Component` kind could still admit it.

Two kinds of type hit this rule, and the error message does not distinguish
them. Most out-of-table types are simply not wired yet, which is the
scoping boundary described above — `aws_nat_gateway` was exactly this shape
until issue #65's EC2 networking batch reached it, and most of the survey's
remaining unadmitted rows still are. `aws_cloudwatch_event_rule` carries a
second, independent reason it stays out, one this table's own vocabulary
rather than any single batch's effort: see
`internal/live/identity/table.go`'s messaging-batch comment for the full
grammar citation. Three surveyed types are out by the admission rule
itself, with no wiring batch ever coming: `aws_iam_access_key` and
`aws_secretsmanager_secret_version` (credentials, whose identity is born
server-side alongside a secret that can never be read again; they become a
lifecycle-layer Op writing to the secret store, referenced by ARN or
pointer, never by value, the same forwarding `random_password` gets above)
and `aws_acm_certificate_validation` (a waiter pretending to be a resource;
it moves to lifecycle sequencing, the same forwarding as `time_sleep`).
`live/SURVEY.md`, "The three the rule excludes", has the full account.

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

**A resource inside a keyed module is stamped by hand, not automatically.**
Stamping cannot compute a per-instance marker for a resource declared
inside a module call that sets `for_each` (directly, or through an
ancestor module call, at any depth) - the module's several instances share
one HCL body for the resource's `tags` argument, and there is no single
literal `tofu-address` that is correct for all of them, nor a safe way to
evaluate an expression that depends on a variable threaded from the module
call's own `each.key` (`internal/configs`' static evaluator has no
repetition data to evaluate one against). Such a resource is left alone
with the `SkipModuleKeyed` reason (`MODULE_KEYED`): trusted as written when
it already declares a `tags` argument, and the ordinary must-stamp error
when it declares none and its type needs discovery to be found again. The
operator writes the marker by hand instead, threading the module's own
`each.key` through as a variable and interpolating it into the address -
see "The keyed-module marker idiom" on the concept page
(`website/docs/language/live-markers.mdx`) for the three-line pattern, and
`live/e2e/estate-module-keyed/` for the fixture it comes from. This is not
a lint refusal; a keyed module is admitted (see "child-module" above), and
this is a standing property of what the stamping pass can and cannot
inject into a shared configuration body. (`internal/live/stamp/stamp.go`,
`SkipModuleKeyed` and `moduleKeyedResource`.)

**Untaggable types carry no ownership marker of their own.** <!-- survey-gen:begin untaggable-admitted -->
`aws_acmpca_certificate_authority_certificate`, `aws_acmpca_policy`,
`aws_api_gateway_account`, `aws_api_gateway_base_path_mapping`,
`aws_api_gateway_documentation_version`, `aws_api_gateway_gateway_response`,
`aws_api_gateway_integration`, `aws_api_gateway_integration_response`,
`aws_api_gateway_method`, `aws_api_gateway_method_response`,
`aws_api_gateway_method_settings`, `aws_api_gateway_model`,
`aws_api_gateway_rest_api_policy`, `aws_api_gateway_usage_plan_key`,
`aws_apigatewayv2_routing_rule`, `aws_appflow_connector_profile`,
`aws_bedrockagentcore_resource_policy`,
`aws_cloudfront_monitoring_subscription`,
`aws_cloudfront_origin_access_control`,
`aws_cloudfront_realtime_log_config`, `aws_cloudwatch_dashboard`,
`aws_cloudwatch_event_api_destination`, `aws_cloudwatch_event_archive`,
`aws_cloudwatch_event_connection`, `aws_cloudwatch_event_endpoint`,
`aws_cloudwatch_event_permission`, `aws_cloudwatch_log_account_policy`,
`aws_cloudwatch_log_metric_filter`, `aws_cloudwatch_log_resource_policy`,
`aws_cloudwatch_log_stream`, `aws_cloudwatch_log_subscription_filter`,
`aws_cloudwatch_log_transformer`, `aws_cloudwatch_otel_enrichment`,
`aws_cloudwatch_query_definition`,
`aws_codeartifact_domain_permissions_policy`,
`aws_codeartifact_repository_permissions_policy`, `aws_codebuild_webhook`,
`aws_codedeploy_deployment_config`,
`aws_cognito_identity_pool_provider_principal_tag`,
`aws_cognito_identity_pool_roles_attachment`,
`aws_cognito_identity_provider`, `aws_cognito_resource_server`,
`aws_cognito_user`, `aws_cognito_user_group`, `aws_cognito_user_in_group`,
`aws_cognito_user_pool_domain`, `aws_config_conformance_pack`,
`aws_config_organization_conformance_pack`,
`aws_config_remediation_configuration`,
`aws_connect_user_hierarchy_structure`, `aws_controltower_control`,
`aws_db_instance_role_association`, `aws_db_proxy_default_target_group`,
`aws_dynamodb_global_table`, `aws_dynamodb_resource_policy`,
`aws_ebs_snapshot_block_public_access`, `aws_ec2_client_vpn_route`,
`aws_ec2_managed_prefix_list_entry`,
`aws_ec2_transit_gateway_metering_policy_entry`,
`aws_ec2_transit_gateway_policy_table_association`,
`aws_ec2_transit_gateway_route`,
`aws_ec2_transit_gateway_route_table_association`,
`aws_ec2_transit_gateway_route_table_propagation`,
`aws_ecr_lifecycle_policy`, `aws_ecr_pull_through_cache_rule`,
`aws_ecr_pull_time_update_exclusion`, `aws_ecr_registry_policy`,
`aws_ecr_registry_scanning_configuration`,
`aws_ecr_replication_configuration`, `aws_ecr_repository_creation_template`,
`aws_ecr_repository_policy`, `aws_ecrpublic_repository_policy`,
`aws_ecs_cluster_capacity_providers`, `aws_eip_association`,
`aws_eks_access_policy_association`, `aws_emr_security_configuration`,
`aws_fsx_s3_access_point_attachment`,
`aws_globalaccelerator_endpoint_group`, `aws_globalaccelerator_listener`,
`aws_glue_catalog_table`, `aws_glue_classifier`,
`aws_glue_data_catalog_encryption_settings`, `aws_guardduty_member`,
`aws_guardduty_organization_admin_account`,
`aws_guardduty_organization_configuration`, `aws_iam_group`,
`aws_iam_group_policy`, `aws_iam_group_policy_attachment`,
`aws_iam_role_policy`, `aws_iam_role_policy_attachment`,
`aws_iam_user_policy`, `aws_iam_user_policy_attachment`,
`aws_inspector2_delegated_admin_account`,
`aws_inspector2_member_association`, `aws_iot_thing`,
`aws_iot_topic_rule_destination`, `aws_kms_alias`,
`aws_lambda_layer_version`, `aws_lb_target_group_attachment`,
`aws_lexv2models_bot_locale`, `aws_lightsail_lb_certificate`,
`aws_lightsail_static_ip`, `aws_location_tracker_association`,
`aws_macie2_organization_admin_account`, `aws_medialive_multiplex_program`,
`aws_msk_configuration`, `aws_nat_gateway_eip_association`,
`aws_network_acl_rule`, `aws_network_interface_attachment`,
`aws_network_interface_permission`,
`aws_networkfirewall_logging_configuration`,
`aws_networkmanager_core_network_policy_attachment`,
`aws_networkmanager_customer_gateway_association`,
`aws_networkmanager_link_association`,
`aws_networkmanager_prefix_list_association`,
`aws_networkmanager_transit_gateway_registration`,
`aws_opensearchserverless_access_policy`,
`aws_opensearchserverless_lifecycle_policy`,
`aws_opensearchserverless_security_policy`,
`aws_prometheus_alert_manager_definition`,
`aws_prometheus_query_logging_configuration`,
`aws_prometheus_scraper_logging_configuration`,
`aws_rds_cluster_role_association`, `aws_route`,
`aws_route53_hosted_zone_dnssec`, `aws_route53_key_signing_key`,
`aws_route53_record`, `aws_route53_resolver_firewall_rule`,
`aws_route53_resolver_rule_association`, `aws_route53_zone_association`,
`aws_route_table_association`, `aws_s3_bucket_lifecycle_configuration`,
`aws_s3_bucket_policy`, `aws_s3_bucket_public_access_block`,
`aws_s3_bucket_server_side_encryption_configuration`,
`aws_s3_bucket_versioning`, `aws_sagemaker_model_package_group_policy`,
`aws_secretsmanager_secret_policy`, `aws_secretsmanager_secret_rotation`,
`aws_securityhub_configuration_policy_association`,
`aws_securityhub_member`, `aws_securityhub_organization_admin_account`,
`aws_securityhub_standards_control`,
`aws_securityhub_standards_control_association`,
`aws_servicecatalog_portfolio_share`,
`aws_servicecatalogappregistry_attribute_group_association`,
`aws_sns_topic_policy`, `aws_sqs_queue_policy`, `aws_ssm_patch_group`,
`aws_ssm_resource_data_sync`, `aws_ssm_service_setting`,
`aws_ssoadmin_account_assignment`, `aws_ssoadmin_application_assignment`,
`aws_ssoadmin_instance_access_control_attributes`,
`aws_transfer_web_app_customization`, `aws_volume_attachment`,
`aws_vpc_dhcp_options_association`, `aws_vpc_endpoint_policy`,
`aws_vpc_endpoint_private_dns`, `aws_vpc_endpoint_route_table_association`,
`aws_vpc_endpoint_security_group_association`,
`aws_vpc_endpoint_subnet_association`, `aws_vpc_ipam_pool_cidr`,
`aws_vpclattice_auth_policy`, `aws_vpclattice_resource_policy`,
`aws_wafv2_web_acl_rule`, `aws_workspacesweb_browser_settings_association`,
`aws_workspacesweb_data_protection_settings_association`,
`aws_workspacesweb_ip_access_settings_association`,
`aws_workspacesweb_network_settings_association`,
`aws_workspacesweb_session_logger_association`,
`aws_workspacesweb_trust_store_association`,
`aws_workspacesweb_user_access_logging_settings_association`,
`aws_workspacesweb_user_settings_association` and `aws_xray_resource_policy`<!-- survey-gen:end untaggable-admitted --> carry no tags, so a marker-based sweep
has nothing to search on for any of them. Their identity is built from
their own configuration, which is a problem the moment a resource block is
removed rather than destroyed: with no marker to search on and no
configuration left to build the identity from, deleting the resource block
looks indistinguishable from the resource never having existed. Issue #60
is the two ways this fork closes that gap, and the residue left once both
are applied.

**Some are swept via a parent read instead (issue #60).** An untaggable
type whose identity is composed from an admitted, taggable parent's own
identity - a bucket policy's `bucket` is the same string as the bucket's
own identity, and the same shape holds for a role, a topic, a queue, a
route table or a hosted zone - does not need a marker of its own: reading
the parent tells the sweep the child's identity too, so the child's live
existence is one read away with no memory required. This is derived, not a
second hand list: `internal/live/identity`'s `ParentOf` reads the same
`Components` every identity resolution already reads, matched against
which admitted types are themselves taggable
(`live/survey-full.json`'s signal here, the provider's own schema at run
time), and `SingleParentComponent` narrows that to the shape where nothing
besides the parent's value is needed - the "named-singleton child" the
identity table's own comments already name for `aws_s3_bucket_policy` and
`aws_sns_topic_policy`. <!-- survey-gen:begin untaggable-parent-read -->
| Type | Parent | Removed by this leg |
|---|---|---|
| `aws_acmpca_certificate_authority_certificate` | `aws_acmpca_certificate_authority` | no (report-only) |
| `aws_api_gateway_base_path_mapping` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_api_gateway_documentation_version` | `aws_api_gateway_rest_api` | no (report-only) |
| `aws_api_gateway_gateway_response` | `aws_api_gateway_rest_api` | no (report-only) |
| `aws_api_gateway_integration` | `aws_api_gateway_rest_api` | no (report-only) |
| `aws_api_gateway_integration_response` | `aws_api_gateway_rest_api` | no (report-only) |
| `aws_api_gateway_method` | `aws_api_gateway_rest_api` | no (report-only) |
| `aws_api_gateway_method_response` | `aws_api_gateway_rest_api` | no (report-only) |
| `aws_api_gateway_method_settings` | `aws_api_gateway_rest_api` | no (report-only) |
| `aws_api_gateway_model` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_api_gateway_rest_api_policy` | `aws_api_gateway_rest_api` | no (report-only) |
| `aws_api_gateway_usage_plan_key` | `aws_api_gateway_usage_plan` | no (report-only) |
| `aws_appflow_connector_profile` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cloudfront_monitoring_subscription` | `aws_lightsail_distribution` | no (report-only) |
| `aws_cloudfront_realtime_log_config` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cloudwatch_event_api_destination` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cloudwatch_event_archive` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cloudwatch_event_connection` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cloudwatch_event_endpoint` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cloudwatch_log_metric_filter` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cloudwatch_log_stream` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cloudwatch_log_subscription_filter` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cloudwatch_log_transformer` | `aws_cloudwatch_log_group` | no (report-only) |
| `aws_cognito_identity_pool_provider_principal_tag` | `aws_cognito_identity_pool` | no (report-only) |
| `aws_cognito_identity_pool_roles_attachment` | `aws_cognito_identity_pool` | no (report-only) |
| `aws_cognito_identity_provider` | `aws_cognito_user_pool` | no (report-only) |
| `aws_cognito_resource_server` | `aws_cognito_user_pool` | no (report-only) |
| `aws_cognito_user` | `aws_cognito_user_pool` | no (report-only) |
| `aws_cognito_user_group` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_cognito_user_in_group` | `aws_cognito_user_pool` | no (report-only) |
| `aws_config_conformance_pack` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_config_organization_conformance_pack` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_dynamodb_global_table` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_ec2_client_vpn_route` | `aws_ec2_client_vpn_endpoint` | no (report-only) |
| `aws_ec2_managed_prefix_list_entry` | `aws_ec2_managed_prefix_list` | no (report-only) |
| `aws_ec2_transit_gateway_metering_policy_entry` | `aws_ec2_transit_gateway_metering_policy` | no (report-only) |
| `aws_ec2_transit_gateway_policy_table_association` | `aws_ec2_transit_gateway_policy_table` | no (report-only) |
| `aws_ec2_transit_gateway_route_table_association` | `aws_ec2_transit_gateway_route_table` | no (report-only) |
| `aws_ec2_transit_gateway_route_table_propagation` | `aws_ec2_transit_gateway_route_table` | no (report-only) |
| `aws_ecr_lifecycle_policy` | `aws_ecr_repository` | no (report-only) |
| `aws_ecr_repository_policy` | `aws_ecr_repository` | no (report-only) |
| `aws_ecrpublic_repository_policy` | `aws_ecrpublic_repository` | no (report-only) |
| `aws_eks_access_policy_association` | `aws_iam_policy` | no (report-only) |
| `aws_emr_security_configuration` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_fsx_s3_access_point_attachment` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_glue_catalog_table` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_glue_classifier` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_guardduty_member` | `aws_guardduty_detector` | no (report-only) |
| `aws_guardduty_organization_configuration` | `aws_guardduty_detector` | no (report-only) |
| `aws_iam_group` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_iam_group_policy` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_iam_group_policy_attachment` | `aws_iam_policy` | no (report-only) |
| `aws_iam_role_policy` | `aws_iam_role` | no (report-only) |
| `aws_iam_role_policy_attachment` | `aws_iam_role` | no (report-only) |
| `aws_iam_user_policy` | `aws_iam_user` | no (report-only) |
| `aws_iam_user_policy_attachment` | `aws_iam_user` | no (report-only) |
| `aws_iot_thing` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_kms_alias` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_lb_target_group_attachment` | `aws_lb_target_group` | no (report-only) |
| `aws_lexv2models_bot_locale` | `aws_lexv2models_bot` | no (report-only) |
| `aws_lightsail_lb_certificate` | `aws_lightsail_lb` | no (report-only) |
| `aws_lightsail_static_ip` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_location_tracker_association` | `aws_location_tracker` | no (report-only) |
| `aws_medialive_multiplex_program` | `aws_medialive_multiplex` | no (report-only) |
| `aws_nat_gateway_eip_association` | `aws_nat_gateway` | no (report-only) |
| `aws_network_acl_rule` | `aws_network_acl` | no (report-only) |
| `aws_networkfirewall_logging_configuration` | `aws_networkfirewall_firewall` | no (report-only) |
| `aws_networkmanager_core_network_policy_attachment` | `aws_networkmanager_core_network` | no (report-only) |
| `aws_networkmanager_customer_gateway_association` | `aws_customer_gateway` | no (report-only) |
| `aws_networkmanager_link_association` | `aws_networkmanager_link` | no (report-only) |
| `aws_networkmanager_prefix_list_association` | `aws_ec2_managed_prefix_list` | no (report-only) |
| `aws_networkmanager_transit_gateway_registration` | `aws_ec2_transit_gateway` | no (report-only) |
| `aws_opensearchserverless_access_policy` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_opensearchserverless_lifecycle_policy` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_opensearchserverless_security_policy` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_prometheus_alert_manager_definition` | `aws_grafana_workspace` | no (report-only) |
| `aws_prometheus_query_logging_configuration` | `aws_grafana_workspace` | no (report-only) |
| `aws_prometheus_scraper_logging_configuration` | `aws_prometheus_scraper` | no (report-only) |
| `aws_route` | `aws_route_table` | no (report-only) |
| `aws_route53_key_signing_key` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_route53_record` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_route53_resolver_firewall_rule` | `aws_route53_resolver_firewall_domain_list` | no (report-only) |
| `aws_route53_zone_association` | `aws_route53_zone` | no (report-only) |
| `aws_route_table_association` | `aws_route_table` | no (report-only) |
| `aws_s3_bucket_lifecycle_configuration` | `aws_s3_bucket` | no (report-only) |
| `aws_s3_bucket_policy` | `aws_s3_bucket` | yes |
| `aws_s3_bucket_public_access_block` | `aws_s3_bucket` | no (report-only) |
| `aws_s3_bucket_server_side_encryption_configuration` | `aws_s3_bucket` | no (report-only) |
| `aws_s3_bucket_versioning` | `aws_s3_bucket` | no (report-only) |
| `aws_sagemaker_model_package_group_policy` | `aws_sagemaker_model_package_group` | no (report-only) |
| `aws_secretsmanager_secret_policy` | `aws_secretsmanager_secret` | no (report-only) |
| `aws_secretsmanager_secret_rotation` | `aws_secretsmanager_secret` | no (report-only) |
| `aws_servicecatalog_portfolio_share` | `aws_servicecatalog_portfolio` | no (report-only) |
| `aws_servicecatalogappregistry_attribute_group_association` | `aws_servicecatalogappregistry_attribute_group` | no (report-only) |
| `aws_sns_topic_policy` | `aws_sns_topic` | no (report-only) |
| `aws_sqs_queue_policy` | `aws_sqs_queue` | no (report-only) |
| `aws_ssm_patch_group` | `aws_ssm_patch_baseline` | no (report-only) |
| `aws_ssm_resource_data_sync` | `aws_api_gateway_domain_name` | no (report-only) |
| `aws_ssoadmin_account_assignment` | `aws_instance` | no (report-only) |
| `aws_ssoadmin_application_assignment` | `aws_ssoadmin_application` | no (report-only) |
| `aws_ssoadmin_instance_access_control_attributes` | `aws_instance` | no (report-only) |
| `aws_transfer_web_app_customization` | `aws_transfer_web_app` | no (report-only) |
| `aws_volume_attachment` | `aws_ebs_volume` | no (report-only) |
| `aws_vpc_endpoint_policy` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_endpoint_private_dns` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_endpoint_route_table_association` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_endpoint_security_group_association` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_endpoint_subnet_association` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_ipam_pool_cidr` | `aws_vpc_ipam_pool` | no (report-only) |
| `aws_wafv2_web_acl_rule` | `aws_wafv2_web_acl` | no (report-only) |
| `aws_workspacesweb_browser_settings_association` | `aws_workspacesweb_browser_settings` | no (report-only) |
| `aws_workspacesweb_data_protection_settings_association` | `aws_workspacesweb_data_protection_settings` | no (report-only) |
| `aws_workspacesweb_ip_access_settings_association` | `aws_workspacesweb_ip_access_settings` | no (report-only) |
| `aws_workspacesweb_network_settings_association` | `aws_workspacesweb_network_settings` | no (report-only) |
| `aws_workspacesweb_session_logger_association` | `aws_workspacesweb_session_logger` | no (report-only) |
| `aws_workspacesweb_trust_store_association` | `aws_workspacesweb_trust_store` | no (report-only) |
| `aws_workspacesweb_user_access_logging_settings_association` | `aws_workspacesweb_user_access_logging_settings` | no (report-only) |
| `aws_workspacesweb_user_settings_association` | `aws_workspacesweb_user_settings` | no (report-only) |

**Total.** 118 types swept via a parent read.
<!-- survey-gen:end untaggable-parent-read -->

Being parent-readable only says the sweep can *see* the child; whether it
can also *remove* it is a narrower, per-type question the parent read
alone does not settle, and the "Removed by this leg" column above is that
answer today rather than a promise about the rest of the row. Wired for
removal this pass: `aws_s3_bucket_policy`, this fork's first read-based
removal - S3's `GetBucketPolicy` returns a clean "not found" when a bucket
carries none, so a parent read gives the sweep the same yes/no answer a
marker would have, and the bucket name is the whole of the policy's
identity end to end (`internal/live/discovery`'s gated e2e exercises this
against floci). Everything else in the table stays report-only: a plan
still names it, under "Not swept for removal", but does not propose
destroying it. `aws_iam_role_policy` and `aws_iam_role_policy_attachment`
each carry a second, free-standing argument the parent alone does not
supply (the inline policy's own name, the attached policy's ARN);
`aws_route`, `aws_route53_record`, `aws_route_table_association` and
`aws_lb_target_group_attachment` are the same shape, one component short of
what the parent alone determines; and the S3 siblings besides the policy,
and the SNS/SQS policy pair, are structurally the named-singleton shape
that would let a future pass wire them the same way `aws_s3_bucket_policy`
was wired here, once each one's own "found vs. not found" provider
behavior is checked the way the bucket policy's was - see
`internal/live/identity/parent.go`'s `parentReadRemovable` for the
per-type reasoning as it stands.

**The residue.** <!-- survey-gen:begin untaggable-residue -->
`aws_acmpca_policy`, `aws_api_gateway_account`,
`aws_apigatewayv2_routing_rule`, `aws_bedrockagentcore_resource_policy`,
`aws_cloudfront_origin_access_control`, `aws_cloudwatch_dashboard`,
`aws_cloudwatch_event_permission`, `aws_cloudwatch_log_account_policy`,
`aws_cloudwatch_log_resource_policy`, `aws_cloudwatch_otel_enrichment`,
`aws_cloudwatch_query_definition`,
`aws_codeartifact_domain_permissions_policy`,
`aws_codeartifact_repository_permissions_policy`, `aws_codebuild_webhook`,
`aws_codedeploy_deployment_config`, `aws_cognito_user_pool_domain`,
`aws_config_remediation_configuration`,
`aws_connect_user_hierarchy_structure`, `aws_controltower_control`,
`aws_db_instance_role_association`, `aws_db_proxy_default_target_group`,
`aws_dynamodb_resource_policy`, `aws_ebs_snapshot_block_public_access`,
`aws_ec2_transit_gateway_route`, `aws_ecr_pull_through_cache_rule`,
`aws_ecr_pull_time_update_exclusion`, `aws_ecr_registry_policy`,
`aws_ecr_registry_scanning_configuration`,
`aws_ecr_replication_configuration`, `aws_ecr_repository_creation_template`,
`aws_ecs_cluster_capacity_providers`, `aws_eip_association`,
`aws_globalaccelerator_endpoint_group`, `aws_globalaccelerator_listener`,
`aws_glue_data_catalog_encryption_settings`,
`aws_guardduty_organization_admin_account`,
`aws_inspector2_delegated_admin_account`,
`aws_inspector2_member_association`, `aws_iot_topic_rule_destination`,
`aws_lambda_layer_version`, `aws_macie2_organization_admin_account`,
`aws_msk_configuration`, `aws_network_interface_attachment`,
`aws_network_interface_permission`, `aws_rds_cluster_role_association`,
`aws_route53_hosted_zone_dnssec`, `aws_route53_resolver_rule_association`,
`aws_securityhub_configuration_policy_association`,
`aws_securityhub_member`, `aws_securityhub_organization_admin_account`,
`aws_securityhub_standards_control`,
`aws_securityhub_standards_control_association`, `aws_ssm_service_setting`,
`aws_vpc_dhcp_options_association`, `aws_vpclattice_auth_policy`,
`aws_vpclattice_resource_policy` and `aws_xray_resource_policy`<!-- survey-gen:end untaggable-residue --> are neither taggable nor
parent-readable: the three ECR registry types are account-level singletons
with no admitted parent resource to read at all, and the dashboard, the
KMS alias and the Lambda layer version are each client-named on their own
terms, with no dependency on any other admitted type's identity. For these,
issue #60 changes nothing: destroy the resource before removing its block,
or delete it out of band. Every plan still names this narrower list under
"Not swept for removal" - the parent-readable set above is reported there
too when it is report-only, and left out of it entirely on the one row this
pass also removes.

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
| MediaStore | `aws_media_store_` | 1 | a service AWS discontinued effective November 13, 2025 (already past), which the pinned provider's own docs also flag as deprecated |

**Total.** 77 CloudFormation Registry types across 8 services.

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
| 1 | a default_* adopter: brings the AWS-created default VPC under management rather than creating one; CloudFormation's AWS::EC2::VPC always creates a new VPC and has no adopt-the-existing-default semantics, so there is no CFN resource of its own to alias to |
| 1 | a generic Cloud Control API passthrough: manages an arbitrary CFN-registry resource type by name via the CCAPI CRUDL operations, with no fixed cloud resource of its own |
| 1 | a generic tag escape-hatch: sets one tag key/value on an existing Transfer Family resource's ARN (for resources tagged outside Terraform, or tag keys requiring the aws: prefix), with no CFN resource of its own |
| 1 | a one-shot KMS Encrypt API call whose ciphertext result is stored in Terraform state; not a cloud resource |
| 1 | a preview/dry-run action: shows the next CIDR an IPAM pool would allocate without allocating it, with no CFN resource of its own |
| 1 | a region-level default-certificate override: overrides the system-default SSL/TLS CA certificate new RDS instances/clusters in the region will use going forward. Keyed by region, not an individual DB resource; deleting it reverts to the region's system default. No AWS::RDS::Certificate (or equivalent) type exists in the registry (RDS's modeled types are CustomDBEngineVersion, DBCluster, DBClusterParameterGroup, DBInstance, DBParameterGroup, DBProxy*, DBSecurityGroup*, DBShardGroup, DBSubnetGroup, EventSubscription, GlobalCluster, Integration, OptionGroup). |
| 1 | a registration: enables AWS Audit Manager for the account/region (RegisterAccount), a single account-scoped toggle with no cloud resource of its own |
| 1 | a registration: registers an existing member account as CloudTrail's AWS Organizations delegated administrator, with no cloud resource of its own |
| 1 | a running/stopped state toggle on an existing RDS instance (start/stop only); the provider's own docs state destroying this resource is a no-op and does not modify the instance's AWS-side state. No cloud resource of its own - it acts on an instance mapped elsewhere. |
| 1 | a settings singleton: reads/writes account-wide AWS Backup settings (e.g. cross-account/cross-region opt-in) via API, with no cloud resource of its own |
| 1 | a settings singleton: reads/writes region-wide AWS Backup service-opt-in settings via API, with no cloud resource of its own |
| 1 | a settings singleton: reads/writes the account-wide Chime SDK Voice logging configuration via API, with no cloud resource of its own |
| 1 | a settings singleton: reads/writes the account/region-wide Bedrock model-invocation logging configuration via API, with no cloud resource of its own |
| 1 | a settings singleton: sets the customer-managed KMS key used to encrypt an existing AgentCore token vault, with no cloud resource of its own |
| 1 | a state-setter: starts/stops an existing aws_instance, no CFN resource of its own |
| 1 | a status toggle for an existing aws_config_configuration_recorder: starts or stops recording via the Config API's Stop/StartConfigurationRecorder actions directly; AWS::Config::ConfigurationRecorder starts recording automatically once created (its own CFN doc: 'AWS CloudFormation starts the recorder as soon as the delivery channel is available... To stop the recorder without deleting it, call the StopConfigurationRecorder action... directly') and exposes no property for this control |
| 1 | a tagging-only wrapper for an AWS Organizations resource (account, OU, or root) created outside Terraform's own management - e.g. an account implicitly created by AWS Control Tower - with no cloud resource of its own (registry.terraform.io: "Manages an individual Organizations resource tag ... in cases where Organizations resources are created outside Terraform") |
| 1 | a verification waiter: polls until a domain identity's DNS verification record is detected, the same shape as the base overlay's aws_acm_certificate_validation - the provider's own docs say it 'doesn't represent an actual AWS entity' |
| 1 | a verification waiter: starts and tracks AWS::EC2::VPCEndpointService's private-DNS verification (StartVpcEndpointServicePrivateDnsVerification), with no CFN resource or property of its own - VPCEndpointService's property list carries no verification-status field |
| 1 | adds/removes an IoT thing from a thing group (AddThingToThingGroup); a dynamic relationship with no CFN AWS::IoT type of its own - ThingGroup and Thing are each modeled, but membership is neither a resource nor a property in either's CFN schema |
| 1 | adopts an existing Cognito User Pool Client (e.g. one AWS auto-creates for Managed Login branding or an OpenSearch domain's Cognito authentication) rather than creating one - the provider's own docs say it 'does not create or delete this resource, but instead assumes management of it', the same default_*-adopter shape as aws_default_vpc; no CFN resource of its own |
| 1 | an account-level ECR setting (PutAccountSetting, e.g. default basic scan type); a preference on the account, not a distinct resource, and no CFN AWS::ECR account-settings type exists |
| 1 | an account-level ECS default setting (PutAccountSettingDefault); a preference on the account, not a distinct resource |
| 1 | an account-level feature toggle enabling AWS Organizations-wide Service Catalog portfolio sharing; the entire resource is one required boolean (enabled), and the provider's own docs carry no Import section at all. No cloud resource of its own. |
| 1 | an account-level feature-enablement action (EnableSharingWithAwsOrganization): toggles RAM's AWS Organizations sharing integration for the whole account. No arguments beyond the toggle, keyed by account ID, no cloud resource of its own. |
| 1 | an account-wide IoT fleet-indexing configuration singleton (UpdateIndexingConfiguration); no CFN model |
| 1 | an account-wide IoT logging configuration singleton (SetV2LoggingOptions); no CFN model |
| 1 | an account/region-level feature toggle (Enabled/Disabled) controlling whether SageMaker AI Projects may use Service Catalog; its only arguments are region and status, and its import identity is the region itself, not a resource ID. No cloud resource of its own. |
| 1 | an account/region-wide EMR security setting (PutBlockPublicAccessConfiguration), not a per-cluster resource; no distinct identity, no CFN model |
| 1 | an account/region-wide IoT event-type configuration singleton (UpdateEventConfigurations); no CFN model |
| 1 | an activation action: flips a pending registration to active, with no CFN resource of its own (no identity schema in the provider's own schema, so no importable identity either) |
| 1 | an activation setting: designates which existing AWS::SES::ReceiptRuleSet is the account's active rule set (SetActiveReceiptRuleSet), with no CFN property of its own - AWS::SES::ReceiptRuleSet has no Active property and ships no update handler |
| 1 | an activation toggle: enables/disables Cost Explorer tracking for an existing cost allocation tag key, with no cloud resource of its own |
| 1 | an agreement action: accepts a Bedrock foundation model's EULA/offer for the account, with no cloud resource of its own |
| 1 | an agreement action: submits a Bedrock model-access use-case request for the account, with no cloud resource of its own |
| 1 | an association-manager: creates a delegation request for a control set within an existing Audit Manager assessment, with no cloud resource of its own |
| 1 | an association-manager: enables one of AWS's built-in managed Contributor Insights rule templates on an existing resource (import ID is resource_arn,template_name, not a CloudWatch::InsightRule ARN), with no cloud resource of its own |
| 1 | an association-manager: marks an existing App Runner AutoScalingConfiguration version as the account/region default, with no cloud resource of its own |
| 1 | an association-manager: shares an existing custom Audit Manager framework with another account, with no cloud resource of its own |
| 1 | an aws_ami_copy-style copy operation: starts a cross-region/cross-account EBS volume copy and tracks the resulting volume's id; the destination is an ordinary AWS::EC2::Volume but CFN's Volume resource has no cross-region copy-source semantics, so the copy operation itself models nothing CFN provides |
| 1 | an aws_ami_copy-style one-shot operation: starts a code-signing job against an S3 object and records its completed, immutable result, with no CFN resource of its own (registry has no AWS::Signer::SigningJob type; only SigningProfile and ProfilePermission) |
| 1 | an exclusive-set manager: overwrites a CloudFront KeyValueStore's entire key set to exactly what's configured (removing anything else), with no cloud resource of its own beyond the store |
| 1 | an invocation action: starts a QuickSight dataset ingestion (SPICE refresh) job and tracks its result, with no CFN resource of its own (registry search: AWS::QuickSight has no Ingestion type) |
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

**Total.** 116 Terraform AWS resource types that are provider-side constructs, not infrastructure - no CloudFormation counterpart is expected for any of them. Each row's own note is in `live/mapping.json`.
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
| 8 | same registry gap as aws_storagegateway_cache - AWS::StorageGateway's only registered CFN type is TapePool |
| 6 | real Device Farm resource; live/registry.json has zero AWS::DeviceFarm::* types |
| 4 | AWS::WorkMail has zero CFN Registry resource types at all - same evidence as aws_workmail_default_domain |
| 4 | real DataExchange resource; live/registry.json has zero AWS::DataExchange::* types |
| 4 | searched the registry for an AWS::AppFabric service: no AWS::AppFabric::* type exists anywhere in live/registry.json |
| 3 | no AWS::ServiceQuotas::* type exists in the registry at all - same registry-search evidence as aws_servicequotas_auto_management |
| 3 | real CodeCatalyst resource; live/registry.json has zero AWS::CodeCatalyst::* types (service unmodeled by CFN entirely) |
| 3 | searched the registry for a Chime SDK Voice CFN service: none exists at all |
| 2 | real account-level Cost Optimization Hub setting; live/registry.json has zero AWS::CostOptimizationHub::* types |
| 2 | searched the registry for an AWS::CloudHSM service: none exists anywhere in live/registry.json |
| 1 | AWS::EC2::Snapshot itself has no CFN Registry resource type at all (registry search for 'snapshot' under EC2 finds only SnapshotBlockPublicAccess), so the CreateVolumePermission attribute TF exposes as a separate resource cannot be modeled either |
| 1 | AWS::Lightsail::Disk exposes AttachedTo/AttachmentState/IsAttached only as Fn::GetAtt read-only attributes, not as settable Properties - the attach action itself is not something a CFN template can declare |
| 1 | AWS::NetworkFirewall::* carries no ContainerAssociation type (confirmed by search of every AWS::NetworkFirewall::* type in live/registry.json: Firewall, FirewallPolicy, LoggingConfiguration, RuleGroup, TLSInspectionConfiguration, VpcEndpointAssociation - none models linking an ECS/EKS cluster's containers to a Network Firewall for dynamic IP-set resolution, which is what this TF resource's own docs describe) |
| 1 | AWS::SWF has zero CFN Registry resource types at all (registry search for 'SWF' returns no matches) |
| 1 | AWS::StorageGateway's current CFN Registry footprint (live/registry.json) is a single type, AWS::StorageGateway::TapePool; no Cache/Gateway/Volume/FileShare type is registered even though these are real, actively used Storage Gateway resources |
| 1 | AWS::WorkMail has zero CFN Registry resource types at all (registry search for 'WorkMail' returns no matches) |
| 1 | Lex V1 has no registry model; AWS::Lex::Bot is V2-only per its own docs, corresponding to aws_lexv2models_bot |
| 1 | Lex V1 has no registry model; AWS::Lex::BotAlias is V2-only per its own docs (same note as AWS::Lex::Bot), and the provider has no V2 bot-alias resource to claim it instead |
| 1 | Lex V1 intent. AWS::Lex::Bot's own CFN doc states plainly: "Amazon Lex V2 is the only supported version in CloudFormation" - the registry's four Lex types (Bot, BotAlias, BotVersion, ResourcePolicy) are all V2-shaped (BotLocales/Intents/Slots are properties of AWS::Lex::Bot, not importable types); V1's standalone intent has no CFN counterpart at any version |
| 1 | Lex V1 slot type - same V2-only CFN gap as aws_lex_intent above |
| 1 | Macie member-account association. registry search: AWS::Macie has no Member type |
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
| 1 | a License Configuration (the license-counting-rules object). registry search: AWS::LicenseManager has no LicenseConfiguration type (only Grant, License, LicenseAssetRuleSet) |
| 1 | a PrivateLink-style VPC endpoint into a domain. registry search: AWS::OpenSearchService has only Application and Domain |
| 1 | a QuickSight identity-store group. registry search: no Group type (QuickSight groups are an identity construct managed by API, not CFN) |
| 1 | a QuickSight multi-tenancy namespace (identity construct). registry search: no Namespace type |
| 1 | a QuickSight user (identity construct, typically federated via IAM/Identity Center). registry search: no User type |
| 1 | a Redshift Partner Integration record tying a cluster/database to an AWS-authorized partner name, with partner-driven status fields Terraform doesn't control. No AWS::Redshift Partner type exists in the registry (see aws_redshift_authentication_profile's note for the full Redshift type list). |
| 1 | a VPC endpoint connection into an OpenSearch Ingestion pipeline. registry search: AWS::OSIS has only a Pipeline type, no endpoint type |
| 1 | a custom plugin/dictionary package associated with a domain. registry search: AWS::OpenSearchService has only Application and Domain |
| 1 | a custom/reader endpoint on a Neptune cluster. registry search: AWS::Neptune has DBCluster, DBClusterParameterGroup, DBInstance, DBParameterGroup, DBSubnetGroup, EventSubscription, GlobalCluster - no ClusterEndpoint type |
| 1 | a multi-Region replica of an externally-sourced KMS key; AWS::KMS::ReplicaKey's schema (Description/Enabled/KeyPolicy/PendingWindowInDays/PrimaryKeyArn/Tags) has no key-material-import support, so an EXTERNAL-origin replica is not representable |
| 1 | a named JSON authentication-profile config object with its own identifier. No AWS::Redshift::AuthenticationProfile (or equivalent) type exists in the registry - Redshift's modeled types are Cluster, ClusterParameterGroup, ClusterSecurityGroup, ClusterSecurityGroupIngress, ClusterSubnetGroup, EndpointAccess, EndpointAuthorization, EventSubscription, Integration, ScheduledAction, SnapshotSchedule only. |
| 1 | a named pointer to one version of an existing template - confirmed via provider docs (template_id + template_version_number, no template content of its own). registry search: AWS::QuickSight::Template exists but there is no TemplateAlias type |
| 1 | a one-time RDS Reserved Instance purchase (a capacity/pricing commitment); the provider's own docs state it cannot be deleted through the API - destroy only removes it from Terraform state while the real reservation runs its term. No RDS reservation type exists in the registry (see aws_rds_cluster_endpoint's note for the full RDS type list). |
| 1 | a purchased ElastiCache reservation (PurchaseReservedCacheNodesOffering); a real, billed resource with its own ReservedCacheNodeId, but no AWS::ElastiCache reservation type is in the CFN registry |
| 1 | a real DynamoDB item (data plane, not control plane) - the canonical cfn-unmodeled shape named in tools/mapping-gen/taxonomy.go's own doc comment alongside aws_s3_object; no AWS::DynamoDB::Item type exists |
| 1 | a real HSM client certificate object (with its own public key, to be registered in the customer's HSM out of band). No AWS::Redshift HSM type exists in the registry (see aws_redshift_authentication_profile's note for the full Redshift type list). |
| 1 | a real HSM configuration object (server IP, partition name, certs). Same gap as aws_redshift_hsm_client_certificate - no AWS::Redshift HSM type exists in the registry. |
| 1 | a real IAM Identity Center managed application bound to Redshift, with its own AWS-generated ARN. No AWS::Redshift IdcApplication (or equivalent) type exists in the registry (see aws_redshift_authentication_profile's note for the full Redshift type list). |
| 1 | a real Savings Plan purchase; the provider's own docs state an active plan cannot be cancelled or deleted - destroying it only removes it from Terraform state while the real commitment runs its term (only a still-queued, not-yet-active plan can genuinely be deleted). No AWS::SavingsPlans service exists in live/registry.json's roster at all (verified against the registry's full service list) - Savings Plans aren't a CFN-modeled resource kind. |
| 1 | a real custom log source with its own provisioned infrastructure (a Glue crawler IAM role, cross-account trust via provider_identity, OCSF event-class config). No AWS::SecurityLake::CustomLogSource type exists in the registry - only AwsLogSource (for AWS-native sources), DataLake, Subscriber, and SubscriberNotification are modeled. |
| 1 | a real customer data record (not a control-plane resource); live/registry.json's CustomerProfiles types (Domain, DomainObjectType, ObjectType, Integration, ...) model schema/config, not individual profile records - no Profile type |
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
| 1 | a saved LF-tag expression (a named boolean combination of LF-tags) with its own name; not among CFN's LakeFormation types (DataCellsFilter/DataLakeSettings/Permissions/PrincipalPermissions/Resource/Tag/TagAssociation) |
| 1 | a service-specific credential (e.g. CodeCommit git credentials) with its own ServiceSpecificCredentialId; real and addressable, but CFN's IAM types include AccessKey, not this credential kind |
| 1 | account-level IP allow-list singleton. registry search: no IpRestriction type |
| 1 | adds a real AWS account as a Security Hub member of the calling admin account, optionally sending an invitation. No AWS::SecurityHub::Member type exists in the registry (see aws_securityhub_action_target's note for the full SecurityHub type list). |
| 1 | an AWS Management Console personalization setting (account color, visible regions/services); no CFN resource models console UI preferences |
| 1 | an EMR-on-EKS job template, addressable via its own JobTemplateId/Arn; CFN's EMRContainers coverage (Endpoint/SecurityConfiguration/VirtualCluster) has no JobTemplate type |
| 1 | an Elastic Transcoder pipeline; a real, addressable resource (PipelineId/Arn), but the CFN registry has zero AWS::ElasticTranscoder types at all |
| 1 | an Elastic Transcoder preset; a real, addressable resource (PresetId/Arn), but the CFN registry has zero AWS::ElasticTranscoder types at all |
| 1 | an Elasticsearch/OpenSearch VPC endpoint (cross-account PrivateLink-style access) with its own VpcEndpointId; not modeled by any AWS::Elasticsearch or AWS::OpenSearchService type in the registry |
| 1 | an FSx File Cache with its own FileCacheId/Arn; not present among the CFN registry's FSx types |
| 1 | an FSx backup with its own BackupId/Arn; no AWS::FSx::Backup type is in the CFN registry (only FileSystem/Volume/Snapshot/StorageVirtualMachine/DataRepositoryAssociation/S3AccessPointAttachment are modeled) |
| 1 | an IAM Identity Center identity-store user with its own UserId; CFN's IdentityStore coverage is Group/GroupMembership only, no User type |
| 1 | an account-level telemetry-configuration evaluation run. registry search: AWS::ObservabilityAdmin's 6 types (OrganizationCentralizationRule, OrganizationTelemetryRule, S3TableIntegration, TelemetryEnrichment, TelemetryPipelines, TelemetryRule) have no Evaluation type |
| 1 | an on-demand capacity-evaluation task run against an Outpost. registry search: AWS::Outposts has only a Site type (plus the unrelated AWS::S3Outposts::* family) - no CapacityTask type |
| 1 | an uploaded SSH public key for CodeCommit, with its own SSHPublicKeyId; no CFN IAM type models it |
| 1 | an uploaded X.509 signing certificate for a user, with its own CertificateId; no CFN IAM type models it |
| 1 | assigns a CustomPermissions profile to one specific user, the per-user counterpart of aws_quicksight_role_custom_permission. registry search: no per-user assignment type |
| 1 | assigns a user to a QuickSight role. registry search: no RoleMembership type |
| 1 | assigns an IAM policy to QuickSight users/groups. registry search: no IamPolicyAssignment type |
| 1 | assigns an existing CustomPermissions profile to an entire QuickSight role (ADMIN/AUTHOR/READER/...) within a namespace - confirmed via provider docs (role + namespace arguments, no permissions content of its own). registry search: no per-role assignment type; AWS::QuickSight::CustomPermissions models the permissions profile itself, not this assignment |
| 1 | associates a License Manager configuration with an existing resource (AMI, launch template, EC2 host) via UpdateLicenseSpecificationsForResource; registry search: AWS::LicenseManager only has Grant, License and LicenseAssetRuleSet, no association/specification type |
| 1 | associates a Redshift datashare with a consumer (account, ARN, or region); same gap as aws_redshift_data_share_authorization - no AWS::Redshift DataShare type exists in the registry. |
| 1 | associates a custom domain name and ACM certificate with a Redshift Serverless workgroup. AWS::RedshiftServerless::Workgroup's full property list (verified against the CFN template reference: BaseCapacity, ConfigParameters, EnhancedVpcRouting, MaxCapacity, NamespaceName, Port, PricePerformanceTarget, PubliclyAccessible, RecoveryPointId, SecurityGroupIds, SnapshotArn/Name/OwnerAccount, SubnetIds, Tags, TrackName, Workgroup, WorkgroupName) has no custom-domain property at all. |
| 1 | associates a member account with the Inspector delegated admin (mirroring AWS::GuardDuty::Member's concept); CFN's InspectorV2 types (Filter/CisScanConfiguration/CodeSecurityIntegration/CodeSecurityScanConfiguration) have no Member-equivalent type |
| 1 | associates an AWS Budget with a Service Catalog product or portfolio (a compound budget_name:resource_id link). No AWS::ServiceCatalog::BudgetResourceAssociation type exists in the registry (ServiceCatalog's modeled types are AcceptedPortfolioShare, CloudFormationProduct, CloudFormationProvisionedProduct, LaunchNotificationConstraint, LaunchRoleConstraint, LaunchTemplateConstraint, Portfolio, PortfolioPrincipalAssociation, PortfolioProductAssociation, PortfolioShare, ResourceUpdateConstraint, ServiceAction, ServiceActionAssociation, StackSetConstraint, TagOption, TagOptionAssociation only). |
| 1 | associates an existing cluster with an existing AWS::Redshift::SnapshotSchedule. AWS::Redshift::Cluster's full property list (verified against the CFN template reference) has no SnapshotSchedule/ScheduleIdentifier-linking property, and SnapshotSchedule itself is scoped independently - the cluster-to-schedule association isn't modeled by either type. |
| 1 | attaches a cross-account resource policy to the singleton per-account S3 Access Grants instance. No AWS::S3::AccessGrantsInstanceResourcePolicy type exists in the registry, and AccessGrantsInstance's own schema (primary identifier AccessGrantsInstanceArn; read-only AccessGrantsInstanceArn/Id; create-only Tags) carries no resource-policy property. |
| 1 | attaches a resource policy document to a Redshift Serverless resource ARN, generically. No AWS::RedshiftServerless ResourcePolicy type exists in the registry, and neither Namespace nor Workgroup's schema exposes a resource-policy property. |
| 1 | attaches a resource policy document to an arbitrary Redshift resource ARN (cluster, namespace, or snapshot, per the provider's own docs - not scoped to one resource kind). AWS::Redshift::Cluster does carry its own NamespaceResourcePolicy property, but since this TF resource is generic across resource kinds Redshift doesn't even give CFN types to (there is no Redshift snapshot type at all), pinning it to Cluster alone would misrepresent every non-cluster use - not modeled as a single fold target. |
| 1 | attaches a resource policy to an S3 Access Point. No AWS::S3::AccessPointPolicy type exists in the registry (verified against the full AWS::S3::* type list: AccessGrant, AccessGrantsInstance, AccessGrantsLocation, AccessPoint, Bucket, BucketPolicy, MultiRegionAccessPoint, MultiRegionAccessPointPolicy, StorageLens, StorageLensGroup - only AWS::S3ObjectLambda::AccessPointPolicy is modeled, for a different access-point kind). |
| 1 | configures where an existing CloudWatch RUM app monitor sends extended metrics (CloudWatch, Evidently, ...) via PutRumMetricsDestination. AWS::RUM::AppMonitor's full property list (verified against the CFN template reference: AppMonitorConfiguration, CustomEvents, CwLogEnabled, DeobfuscationConfiguration, Domain, DomainList, Name, Platform, ResourcePolicy, Tags) has no metric-destination property, and RUM has no other registry type. |
| 1 | creates a distinct VPC endpoint object (its own vpc_endpoint_id, DNS address, network interfaces) for a Redshift Serverless workgroup, not merely a workgroup setting. No AWS::RedshiftServerless EndpointAccess type exists in the registry - RedshiftServerless's modeled types are Namespace, Snapshot, Workgroup only. |
| 1 | cross-account resource policy for pipeline log ingestion. registry search: AWS::OSIS has only a Pipeline type |
| 1 | cross-account resource-based policy attached to a Firewall/FirewallPolicy/RuleGroup ARN via PutResourcePolicy. registry search: AWS::NetworkFirewall has Firewall, FirewallPolicy, LoggingConfiguration, RuleGroup, TLSInspectionConfiguration, VpcEndpointAssociation - no ResourcePolicy type |
| 1 | cross-cluster search connection. registry search: AWS::OpenSearchService has only Application and Domain |
| 1 | deploys a CloudFormation stack from a Serverless Application Repository application (application_id + semantic_version as the template source), with SAR-specific capabilities (CAPABILITY_RESOURCE_POLICY, CAPABILITY_AUTO_EXPAND). No AWS::ServerlessApplicationRepository service exists in live/registry.json's roster at all (verified against the registry's full service list) - the deployed artifact is CloudFormation's own Stack primitive, not a Registry type representing SAR itself. |
| 1 | designates an existing route table as a VPC's main route table (EC2's ReplaceRouteTableAssociation on the implicit main association). AWS::EC2::VPC carries no MainRouteTableId-style property, and the registry has no resource type for this association (only AWS::EC2::SubnetRouteTableAssociation exists, for subnet-level associations) - a well-known CloudFormation gap |
| 1 | enables the User Notifications service's organizational access (trusted-access-style toggle). registry search: AWS::Notifications' 9 types (ChannelAssociation, EventRule, ManagedNotification*, NotificationConfiguration, NotificationHub, OrganizationalUnitAssociation, NotificationsContacts::EmailContact) have no OrganizationsAccess type |
| 1 | enables trusted access for an AWS service at the organization level (EnableAWSServiceAccess). registry search: AWS::Organizations has Account, Organization, OrganizationalUnit, Policy, ResourcePolicy - no service-access type |
| 1 | executes a Redshift Data API SQL statement (ExecuteStatement) and tracks its own AWS-issued statement ID through a real status state machine (SUBMITTED/STARTED/FINISHED/FAILED/ABORTED). AWS::RedshiftServerless carries no Data-API type, and there is no separate AWS::RedshiftData CFN service at all - the executed statement is real tracked activity, not a CFN-modeled resource. |
| 1 | generated S3-compatible access-key credential for a Lightsail bucket. AWS::Lightsail::Bucket's full property list (AccessRules, BucketName, BundleId, ObjectVersioning, ReadOnlyAccessAccounts, ResourcesReceivingAccess, Tags) has no AccessKeys property - unlike AWS::IAM::AccessKey, Lightsail bucket keys are not CFN-declarable |
| 1 | grants a Redshift datashare authorization to a consumer account (or ADX); no AWS::Redshift DataShare/DataShareAuthorization type exists in the registry at all (see aws_redshift_authentication_profile's note) - Redshift data sharing predates/lacks CFN Registry support. |
| 1 | grants another account permission to create a VPC endpoint into a domain (AuthorizeVpcEndpointAccess). registry search: AWS::OpenSearchService has only Application and Domain - no authorization/access type |
| 1 | manages a custom Aurora cluster endpoint (a curated subset of readers, exposed under its own DNS name). No AWS::RDS::DBClusterEndpoint (or equivalent) type exists in live/registry.json - RDS's modeled types are CustomDBEngineVersion, DBCluster, DBClusterParameterGroup, DBInstance, DBParameterGroup, DBProxy, DBProxyEndpoint, DBProxyTargetGroup, DBSecurityGroup, DBSecurityGroupIngress, DBShardGroup, DBSubnetGroup, EventSubscription, GlobalCluster, Integration, OptionGroup only. |
| 1 | manages a real DS (delegation signer) record and its AWS-generated signing key for a Route53-registered domain's parent zone. No AWS::Route53Domains service exists in live/registry.json's roster at all (verified against the registry's full service list). |
| 1 | manages a specific security control's enablement status within a specific standard - a compound identity (security_control_id, standards_arn). AWS::SecurityHub::SecurityControl's primary identifier is SecurityControlId alone with no standards_arn dimension, so the per-standard scope this resource manages isn't what that type's identity models. |
| 1 | manages one provisioning artifact (product version) with its own AWS-issued ID and independent create/update/delete lifecycle. AWS::ServiceCatalog::CloudFormationProduct only accepts ProvisioningArtifactParameters at creation plus a write-only whole-list ReplaceProvisioningArtifacts operation on update (per live/registry.json) - it has no per-artifact identity or lifecycle of its own, so an individual artifact's independent management isn't modeled. |
| 1 | manages settings (contacts, name servers, auto-renew, transfer lock) on a domain already registered with Route53 Domains; the provider's own docs note destroy does not deregister the domain. Same gap as aws_route53domains_domain - no AWS::Route53Domains service exists in the registry. |
| 1 | materializes a traffic-policy version into actual DNS records at a name, with its own UUID identifier. No AWS::Route53 TrafficPolicyInstance type exists in the registry (see aws_route53_delegation_set's note for the full Route53 type list). |
| 1 | no AWS::ServiceQuotas::* type exists in the registry at all (confirmed by search; the only 'Quota' match anywhere in live/registry.json is the unrelated AWS::Batch::QuotaShare) |
| 1 | opts a principal+resource pair into Lake Formation's hybrid access mode; addressable via ListLakeFormationOptIns with its own identity, not modeled by any CFN LakeFormation type |
| 1 | per-account setting for where Macie writes classification results. registry search: AWS::Macie only has AllowList, CustomDataIdentifier, FindingsFilter, Session - no export-configuration type |
| 1 | places an existing asset (dashboard, analysis, dataset...) into a folder. AWS::QuickSight::Folder's full property list (AwsAccountId, FolderId, FolderType, Name, ParentFolderArn, Permissions, SharingModel, Tags) has no members/membership property - registry search confirms no separate FolderMembership type either |
| 1 | real AWS Organizations-wide IPAM delegated-admin enrollment with no CFN model - an org-level setting, not a property of any AWS::EC2::IPAM* type |
| 1 | real CloudWatch log subscription for a directory; live/registry.json's only AWS::DirectoryService types are MicrosoftAD and SimpleAD - no log-subscription type |
| 1 | real Comprehend custom entity recognizer; live/registry.json's only AWS::Comprehend types are DocumentClassifier and Flywheel - no EntityRecognizer type |
| 1 | real DNS conditional-forwarder resource for a directory; live/registry.json's only AWS::DirectoryService types are MicrosoftAD and SimpleAD - neither models sub-features like conditional forwarders |
| 1 | real DataZone resource; live/registry.json's DataZone types (Connection, DataSource, Domain, DomainUnit, Environment, FormType, Project, ...) have no AssetType |
| 1 | real DataZone resource; live/registry.json's DataZone types have no Glossary type |
| 1 | real DataZone resource; live/registry.json's DataZone types have no GlossaryTerm type |
| 1 | real DocumentDB resource; live/registry.json has no AWS::DocDB::DBClusterSnapshot type (CFN cannot create ad-hoc DocDB snapshots) |
| 1 | real EBS resource; live/registry.json has no AWS::EC2::Snapshot type (only the unrelated AWS::EC2::SnapshotBlockPublicAccess account setting) - CFN cannot create ad-hoc EBS snapshots |
| 1 | real EC2 account-wide spot datafeed S3 subscription with no CFN model - no matching AWS::EC2::* type in the registry |
| 1 | real EC2 resource (RDMA secondary network, a newer high-performance-networking feature) with its own ARN and identity; live/registry.json's EC2 types have no secondary-network type |
| 1 | real EC2 resource (subnet within an RDMA secondary network) with its own ARN and identity; live/registry.json's EC2 types have no secondary-subnet type |
| 1 | real EC2 single-instance spot request (RequestSpotInstances, the discouraged legacy API) with no CFN model - the registry's only spot-related EC2 type is AWS::EC2::SpotFleet, which models fleets, not a single instance request |
| 1 | real Elastic Disaster Recovery resource (an actively supported service, not deprecated); live/registry.json has zero AWS::DRS::* types |
| 1 | real IAM Identity Center multi-region provisioning (adds/removes a Region from an instance) with no CFN model - no AWS::SSO::Region type exists in the registry's 6 AWS::SSO::* types |
| 1 | real IAM Identity Center trusted OIDC token issuer with no CFN model - no AWS::SSO::TrustedTokenIssuer type exists in the registry's 6 AWS::SSO::* types |
| 1 | real MACsec CAK/CKN key association for a DX connection or LAG; live/registry.json's AWS::DirectConnect types have no MACsec key type |
| 1 | real ML capacity-block purchase, a distinct API from regular Capacity Reservations; live/registry.json's only capacity-reservation types are AWS::EC2::CapacityReservation and CapacityReservationFleet - no capacity-block type |
| 1 | real RADIUS MFA settings for a directory; live/registry.json's only AWS::DirectoryService types are MicrosoftAD and SimpleAD - no radius-settings type |
| 1 | real RDS resource; live/registry.json has no AWS::RDS::DBClusterSnapshot type (CFN cannot create ad-hoc RDS snapshots) |
| 1 | real RDS resource; live/registry.json has no AWS::RDS::DBSnapshot type (CFN cannot create ad-hoc RDS snapshots) |
| 1 | real SES account/identity config (SetIdentityNotificationTopic) with no CFN model - AWS::SES::EmailIdentity's property list (ConfigurationSetAttributes, DkimAttributes, DkimSigningAttributes, FeedbackAttributes, MailFromAttributes, Tags per its CFN docs) carries no notification-topic property, and no other AWS::SES::* type covers it |
| 1 | real SES sending-authorization policy (PutIdentityPolicy) with no CFN model - not a property of AWS::SES::EmailIdentity, and no AWS::SES::*Policy* type exists in the registry's 21 SES types |
| 1 | real SESv2 account-wide suppression-list config with no CFN model - no AWS::SES::Account* type exists in the registry |
| 1 | real SESv2 action (PutDedicatedIpInPool, assigning one IP to a pool) with no CFN model - AWS::SES::DedicatedIpPool's CFN properties are only PoolName, ScalingMode and Tags; it carries no member-IP list to attach to |
| 1 | real SNS account-wide SMS preferences (default SMS type, spend limit, etc.) with no CFN model - not a property of AWS::SNS::Topic, and no dedicated CFN type exists |
| 1 | real SNS mobile-push PlatformApplication resource with no CFN model - the registry's 4 AWS::SNS::* types (Subscription, Topic, TopicInlinePolicy, TopicPolicy) carry no PlatformApplication |
| 1 | real SSO application access-scope config (PutApplicationAccessScope) with no CFN model - AWS::SSO::Application's CFN properties (ApplicationProviderArn, Description, InstanceArn, Name, PortalOptions, Status, Tags per its CFN docs) carry no access-scope field |
| 1 | real SSO application assignment-required toggle (PutApplicationAssignmentConfiguration) with no CFN model - not among AWS::SSO::Application's CFN properties (see aws_ssoadmin_application_access_scope's evidence) |
| 1 | real Shield Advanced account subscription/enrollment (1-year commitment, auto-renew) with no CFN model - the registry's 4 AWS::Shield::* types (DRTAccess, ProactiveEngagement, Protection, ProtectionGroup) have none for the subscription itself |
| 1 | real Transcribe resource with no CFN model - same registry gap as aws_transcribe_language_model (only VocabularyFilter is registered) |
| 1 | real Transcribe resource with no CFN model - same registry gap as aws_transcribe_language_model (only VocabularyFilter is registered, not the base Vocabulary type) |
| 1 | real Transcribe resource with no CFN model - the registry's only AWS::Transcribe::* type is VocabularyFilter; no LanguageModel type exists |
| 1 | real Transfer Family custom SFTP host key (ImportHostKey) with no CFN model - not among AWS::Transfer::Server's CFN properties (Certificate, Domain, EndpointDetails, EndpointType, IdentityProviderDetails, IdentityProviderType, IpAddressType, LoggingRole, PostAuthenticationLoginBanner, PreAuthenticationLoginBanner, ProtocolDetails, Protocols, S3StorageOptions, SecurityPolicyName, StructuredLogDestinations, Tags, WorkflowDetails per its CFN docs) |
| 1 | real Transfer Family resource (an external-identity-group landing directory, CreateAccess) with no CFN model - not among AWS::Transfer::Server's CFN properties and no dedicated AWS::Transfer::Access type is registered |
| 1 | real VPC Lattice target registration (RegisterTargets) with no CFN model - AWS::VpcLattice::TargetGroup's CFN properties are Name, Type, Config/* and Tags only; no Targets list to attach to |
| 1 | real WAFv2 API key (for CAPTCHA/Challenge JS integration) with no CFN model - the registry's 6 AWS::WAFv2::* types (IPSet, LoggingConfiguration, RegexPatternSet, RuleGroup, WebACL, WebACLAssociation) carry no ApiKey type |
| 1 | real WorkSpaces directory registration with no CFN model - the registry's 4 AWS::WorkSpaces::* types (ConnectionAlias, Workspace, WorkspaceIpGroup, WorkspacesPool) carry no Directory type |
| 1 | real X-Ray account/region KMS encryption setting with no CFN model - not among the registry's 4 AWS::XRay::* types (Group, ResourcePolicy, SamplingRule, TransactionSearchConfig) |
| 1 | real X-Ray account/region trace-segment destination setting (XRay vs CloudWatchLogs) with no CFN model - not among the registry's 4 AWS::XRay::* types |
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
| 1 | real per-connection DNS-resolution/options config with no CFN model - AWS::EC2::VPCPeeringConnection's CFN properties (AssumeRoleRegion, PeerOwnerId, PeerRegion, PeerRoleArn, PeerVpcId, Tags, VpcId per its CFN docs) carry no peering-options field |
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
| 1 | real, per-rule X-Ray sampling/indexing rule with no CFN model - distinct from the account-wide singleton AWS::XRay::TransactionSearchConfig (whose only property is IndexingPercentage, with AccountId as primary identifier); no per-rule IndexingRule type is registered |
| 1 | references a SageMaker JumpStart public-hub model into a private Hub. AWS::SageMaker::Hub is modeled, but no AWS::SageMaker::HubContent/HubContentReference type exists in the registry - referencing content into a hub isn't modeled, only the hub container itself. |
| 1 | registers a Redshift (serverless or provisioned) namespace with the AWS Glue Data Catalog; the provider's own docs note AWS provides no reliable API to verify registration status. Neither AWS::Redshift nor AWS::RedshiftServerless carries a Glue Data Catalog registration type. |
| 1 | registers a member account as delegated administrator for a service. registry search: AWS::Organizations has no DelegatedAdministrator type |
| 1 | registers an already-existing Transit Gateway Connect peer with a device/link in a global network (confirmed via provider docs: "Associates a transit gateway Connect peer with a device"). registry search: AWS::NetworkManager has a ConnectPeer type (for creating SD-WAN Connect peers) but no separate association type for registering a TGW Connect peer |
| 1 | registers, renews, and deregisters a real domain name via Route53 Domains - an actual purchase/registration lifecycle. No AWS::Route53Domains service exists in live/registry.json's roster at all (verified against the registry's full service list). |
| 1 | registry search: AWS::Macie has no ClassificationJob type |
| 1 | registry search: AWS::Macie has no OrganizationAdminAccount type |
| 1 | registry search: AWS::Macie has no OrganizationConfiguration type |
| 1 | registry search: AWS::MemoryDB has ACL, Cluster, MultiRegionCluster, ParameterGroup, SubnetGroup, User - no Snapshot type |
| 1 | registry search: AWS::Neptune has no DBClusterSnapshot type |
| 1 | registry search: AWS::NetworkFlowMonitor has only a Monitor type, no Scope type |
| 1 | registry search: AWS::QuickSight's 18 types have no AccountSettings type (account-level singleton config, not CFN-modeled) |
| 1 | registry search: no AWS::Lightsail::KeyPair type exists at all (the 15 Lightsail registry types are Alarm, Bucket, Certificate, Container, Database, DatabaseSnapshot, Disk, DiskSnapshot, Distribution, Domain, Instance, InstanceSnapshot, LoadBalancer, LoadBalancerTlsCertificate, StaticIp) |
| 1 | registry search: no AWS::NetworkMonitor::* type exists at all (distinct from AWS::NetworkFlowMonitor, which is a different service) |
| 1 | registry search: no GroupMembership type |
| 1 | replicates a single S3 Tables table (by table_arn) to destination bucket(s). AWS::S3Tables::Table's full property list (verified against the CFN template reference: Compaction, IcebergMetadata, Namespace, OpenTableFormat, SnapshotManagement, StorageClassConfiguration, TableBucketARN, TableName, Tags, WithoutMetadata) has no Replication property - only the table-bucket-level AWS::S3Tables::TableBucket carries ReplicationConfiguration (see aws_s3tables_table_bucket_replication's fold). |
| 1 | same as aws_networkmonitor_monitor - no AWS::NetworkMonitor::* type in the registry |
| 1 | same as aws_ses_identity_policy: a sending-authorization policy on an identity, not a property of AWS::SES::EmailIdentity and not modeled by any other AWS::SES::* type |
| 1 | same gap as aws_opensearch_package - no CFN type models package associations |
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
| 1 | sets the S3 account-level Public Access Block configuration (a singleton per account). No AWS::S3::AccountPublicAccessBlock (or equivalent account-scoped) type exists in the registry - only the per-bucket PublicAccessBlockConfiguration property on AWS::S3::Bucket is modeled, a different scope entirely. |
| 1 | sets traffic-dial percentages on an existing S3 Multi-Region Access Point's routes; the provider's own docs note destroying this resource only removes it from state and does not reset the real routing configuration. AWS::S3::MultiRegionAccessPoint's registry entry has no update handler at all (handlers: create/read/delete/list true, update false) - CFN cannot modify an MRAP's routes after creation, so ongoing route management isn't modeled. |
| 1 | subscribes an AWS account to QuickSight (the one-time account-creation action). registry search: no AccountSubscription type |
| 1 | the classic (non-Cloud-WAN) device-to-device Connection object within a global network. registry search: AWS::NetworkManager's 16 types (ConnectAttachment, ConnectPeer, CoreNetwork*, CustomerGatewayAssociation, Device, DirectConnectGatewayAttachment, GlobalNetwork, Link, LinkAssociation, Site, SiteToSiteVpnAttachment, TransitGateway*, VpcAttachment) include no Connection type |
| 1 | the organization-wide counterpart of aws_observabilityadmin_telemetry_evaluation - same registry gap, no Evaluation type at any scope |
| 1 | toggles RDS/Aurora Database Activity Streams on an existing cluster (StartActivityStream/StopActivityStream), keyed by the parent cluster's own ARN with no independent stream identity. AWS::RDS::DBCluster's full property list (verified against the CFN template reference: AllowEngineModeChange, AssociatedRoles, ... EnableCloudwatchLogsExports, ... - no ActivityStream* property anywhere) has no activity-stream property; the feature isn't modeled at all. |
| 1 | toggles an individual legacy standards control, identified by its own per-standard-scoped ARN (standards_control_arn). AWS::SecurityHub::SecurityControl's primary identifier is SecurityControlId alone (the newer, standard-independent unified control view, per the registry) - a per-standard control ARN isn't the identity CFN models here. |
| 1 | uploads/manages an object's content in an S3 bucket - a data-plane operation (PutObject/DeleteObject), not account/control-plane infrastructure the CloudFormation Registry models. Deprecated by the provider in favor of aws_s3_object (identical functionality); see that row for the canonical evidence. |
| 1 | uploads/manages an object's content in an S3 bucket - a real, live piece of data-plane activity, but not a control-plane resource the CloudFormation Registry models (there is no AWS::S3::Object type; S3's modeled types are AccessGrant, AccessGrantsInstance, AccessGrantsLocation, AccessPoint, Bucket, BucketPolicy, MultiRegionAccessPoint, MultiRegionAccessPointPolicy, StorageLens, StorageLensGroup only). |

**Total.** 303 Terraform AWS resource types that are real infrastructure with no CloudFormation Registry model at all. Each row's own note is in `live/mapping.json`.
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
| 1 | AWS::SES::EmailIdentity is already claimed by aws_ses_email_identity/aws_sesv2_email_identity; asserting a third synonym needs a decision, not a sweep guess |
| 1 | CFN models RoutingPolicyLabel as a property on several NetworkManager attachment types; the TF resource attaches generically across all of them |
| 1 | an exclusive-set manager spanning both SecurityGroupIngress and SecurityGroupEgress - no single CFN counterpart |
| 1 | attaches to private, public, or transit VIFs via a bare virtual_interface_id - no single fold parent among the three DirectConnect VIF types |
| 1 | creates SimpleAD, MicrosoftAD, or ADConnector depending on its type argument - three CFN targets, no single alias |
| 1 | creates an ingress or egress rule by its type argument, and both AWS::EC2::SecurityGroup{Ingress,Egress} are already claimed by the split per-direction TF types |
| 1 | legacy per-table replica API; AWS::DynamoDB::GlobalTable's Replicas is plausible but the correspondence to a plain Table's replica is unconfirmed |
| 1 | no registry candidate identified with confidence at sweep time |
| 1 | one TF resource registers any extension kind; the registry splits it across AWS::CloudFormation::{Resource,Module}{Version,DefaultVersion} by kind |
| 1 | one TF resource spans five distinct AWS::ServiceCatalog::*Constraint types depending on its type field |
| 1 | semantics too obscure to classify with confidence; no registry candidate found |
| 1 | targets either a Lambda alias or a raw version - two possible fold parents, no single correct one |
| 1 | the reverse-direction shape of AWS::IAM::UserToGroupAddition (one user to many groups vs one group to many users) - the correspondence is not clean |

**Total.** 13 Terraform AWS resource types with no CloudFormation Registry counterpart and no terminal classification yet - the count the family sweeps in issue #53's workplan burn down. Each row's own note is in `live/mapping.json`.
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
| `aws_glue_connection` | `AWS::Glue::Connection` |
| `aws_glue_dev_endpoint` | `AWS::Glue::DevEndpoint` |
| `aws_glue_ml_transform` | `AWS::Glue::MLTransform` |
| `aws_glue_partition` | `AWS::Glue::Partition` |
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
| `aws_medialive_channel` | `AWS::MediaLive::Channel` |
| `aws_medialive_input` | `AWS::MediaLive::Input` |
| `aws_medialive_input_security_group` | `AWS::MediaLive::InputSecurityGroup` |
| `aws_network_interface_permission` | `AWS::EC2::NetworkInterfacePermission` |
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
| `aws_ses_receipt_filter` | `AWS::SES::ReceiptFilter` |
| `aws_ses_receipt_rule` | `AWS::SES::ReceiptRule` |
| `aws_ses_receipt_rule_set` | `AWS::SES::ReceiptRuleSet` |

**Total.** 74 types, covered only where the provider's own identity schema reaches (the union `live/survey-full.json` measures). A successor CFN type sometimes exists with working handlers - `AWS::Elasticsearch::Domain` above has no handlers, but its successor `AWS::OpenSearchService::Domain` does; `live/mapping.json` does not yet link `aws_opensearch_domain` to it.
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
| `aws_db_instance` | yes (standing e2e residue) | RDS only works fully against floci when the docker socket is mounted into the emulator container, which this harness does not do (lex00/floci#28) |
| `aws_iam_role` | yes (standing e2e residue) | floci's iam:GetRole omits Tags, so the role's own marker never reads back and every plan reports it unowned |
| `aws_s3_bucket_policy` | yes (standing e2e residue) | downstream of aws_iam_role's residue: its policy document embeds the unowned role's ARN, so its own plan never settles |
| `aws_ssm_document` | no | floci answers ssm:CreateDocument with UnsupportedOperation, so no SSM document can be created against the emulator at all (choudoufu#26) |

**Total.** 4 types.
<!-- survey-gen:end residue-emulator -->
