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

One enforced family is indexed elsewhere: the three receipt rules
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

A third kind of entry lives further down, under "Every refusal, enumerated".
Those are generated from the registries that define the refusals rather than
written here, and they cover the refusals that never had a hand-written entry
at all. Read that section first if you are trying to find out why a
particular run was refused. Read this one if you are trying to find out
whether a construct is usable.

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

`internal/live/lint`'s per-type table (`ClassifyLogicalType`, over the
generated `logical_type_generated.go`) replaces the old family-prefix-only
refusal with a policy-grade classification, one of three.

The table is derived, not written. `tools/row-gen -logical-schemas` reads
every managed resource type of the five logical providers plus the built-in
one straight from their `GetProviderSchema` responses into
`live/logical-schemas.json`, and `-emit` classifies from it: a live
(non-deprecated) sensitive attribute anywhere in a store-only provider's type
means SECRET_REFUSED, none means RECORD_ADMITTED. The same rule over the same
artifact derives `internal/live/identity`'s `RecordBacked` rows, so lint's
RECORD_ADMITTED set and identity's `RecordBacked` set are the same set by
construction. They were once maintained separately and diverged - identity
held records for four `random_*` types lint refused outright, so a
configuration with a `record_store` was told its type could never work.

- **RECORD_ADMITTED** - `null_resource`, `terraform_data`, `time_static`,
  `time_offset`, `time_rotating`, `time_sleep`, `random_id`, `random_pet`,
  `random_shuffle`, `random_integer`, `random_string`, `random_uuid`,
  `random_uuid4` and `random_uuid7`. No attribute of any of them is marked
  sensitive by its provider, measured over every attribute of every block
  including nested ones (see `logical_type_generated.go` for the per-type
  evidence). **Conditionally admitted as of #73's projection
  work:** refused exactly as before when the `live` block configures no
  `record_store`, and admitted the moment one is. The record store's key
  namespace is the "no persisted micro-state" limit closing, not a
  reinterpretation of what these types are. A `record_store` block backs
  the type's whole identity with a persisted record instead of a cloud
  observation (`internal/live/staterecord`, local/SSM/S3 backends). See
  `site/content/reference.md`'s `record_store` block for the config
  surface. Without
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
- **OTHER_REFUSED** - `local_file` and `local_sensitive_file`, plus any
  logical-family member released since the last `-logical-schemas` run.
  `hashicorp/local` is the one measured provider that is not store-only, and
  it is excluded deliberately rather than left unreviewed: a `local_file`'s
  identity is its `filename`, not a record, so two instances at distinct
  addresses still collide on one path - measured under stock OpenTofu,
  `count = 4` with a filename built from `count.index % 2` never converges.
  Promoting it would also silence lint's `count.index` walk over that
  filename, which `TestLocalFileKeepsItsCountIndexCheck` pins. For a type
  released after the last measurement, this class is the safe default rather
  than a verdict, and re-running `-logical-schemas` is what resolves it.

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
write-back). No new syntax at the resource block itself, just the same
`null_resource` block that used to be refused now plans and applies.

**Forwarding address (no record store).** The receipts pattern. A
declared, leaf resource whose value is a hash of inputs, read back to
decide whether an effect needs to re-run, without any of `null_resource`'s
implicit re-trigger machinery. Documented in `live/RECEIPTS.md`.

**Enforcement.** `RuleLogicalResource`, classified `RECORD_ADMITTED`
(`internal/live/lint/logical_type.go`, `ClassifyLogicalType`), gated on
`record_store` being absent. Fixture at `live/e2e/limits/null-resource/`
(no store, still refused). The admitted path is exercised by
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
(no store, still refused). The admitted path is exercised by
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
above. See `internal/live/lint/logical_type.go`, `ClassifyLogicalType`.)
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
unlike this family's `RECORD_ADMITTED` neighbors, because configuring a
`record_store` does nothing for this type, by design.

**Enforcement.** `RuleLogicalResource`, classified `SECRET_REFUSED`
(`internal/live/lint/logical_type.go`, `ClassifyLogicalType`). Fixture at
`live/e2e/limits/random-password/`.

**The same problem returns, permanently, wherever ANY `random_*` resource's
generated attribute is built into a sibling's identity.** This is not the
`random_password` case above by another name; it is the same architectural
fact reached through a second door. `random_pet`, `random_id`,
`random_shuffle`, `random_integer` and the rest of the `random_*` family are
`RECORD_ADMITTED` (see "Logical resources: a three-way classification"
above) because none of them generates secret material - configured with a
`record_store`, they run through the stock provider lifecycle just fine on
their own. But a value one of them generates still only exists because it
was generated once and remembered; nothing in the live cloud can be read
back to reproduce it. Ordinarily that is fine, because nothing downstream
needs to reproduce it. It stops being fine the moment another resource's
identity-bearing argument is BUILT from that value - `bucket =
"${random_pet.suffix.id}-image-layers"` is a real example, not a
constructed one (`k8s-io/infra/aws/terraform/registry-sandbox-k8s-io-image-layers`,
`s3-origin.tf`) - because then the sibling's own cloud identity depends on a
value a fresh run cannot regenerate, exactly `random_password`'s problem,
now on the consuming resource rather than the generating one.

**Where it surfaces.** Not a new refusal: the identity pass already refuses
this, because a `random_*` type carries no identity-bearing attribute for a
reference to resolve against. The sibling resource's identity argument (here,
`aws_s3_bucket.bucket`'s `bucket` attribute) is refused as "Not an identity
attribute" at its own site, and every resource whose identity is built from
*that* resource's then cascades to "Unresolvable identity" - the same
one-raise-site cascade #178's "unresolvable identity" scoping traced for
every other root cause on this rule. registry-sandbox-k8s-io-image-layers is
the worked example: 1 site raises "Not an identity attribute" directly on
`random_pet.bucket.id`'s use in the bucket name, and 11 more cascade from it
as "Unresolvable identity" - the estate's whole language-blocked count, net
of its unrelated `logical-resource` and `state-backend` findings.

**Forwarding address.** None, and none is coming: the fix is in the
configuration, not in this fork. Generate the value once, outside OpenTofu's
model (a secret-store Op, a fixed literal chosen by a human, or a value
compiled into the pipeline that provisions the estate), and have the
sibling's identity argument reference that fixed value instead of a
`random_*` resource's attribute. `random_*` resources remain fully usable
for what they were before this ever mattered - a suffix on a scratch
resource nothing else's identity depends on - which is why the type stays
`RECORD_ADMITTED` rather than moving to `SECRET_REFUSED`: the limitation is
in how the VALUE is used, not in the type itself.

**Enforcement.** No dedicated rule - the identity pass's ordinary "Not an
identity attribute" / "Unresolvable identity" refusals
(`internal/live/identity`), permanent for the same reason `random_password`
above is permanent. See "Not an identity attribute" and "Unresolvable
identity" in "Every refusal, enumerated" for the catalog entries.

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
store, still refused). The admitted path is exercised by
`live/e2e/record-store/`.

### moved-block

**Construct.** A `moved` block whose endpoints describe a move an ownership
marker cannot follow. Most `moved` blocks are not this: they are carried, and
reported by nobody.

**What is carried.** A `moved` block relocates a state entry so that the object
recorded at the old address is planned as the resource declared at the new one.
Here that record is the resource's own `tofu-address` tag, so the same
statement reads as "a live resource carrying the old address is the object the
new address names". `internal/live/discovery` indexes the live resource's
marker under both addresses, the instance binds to the address that declares it
now, and the ordinary tags diff rewrites the tag to the new address in place.
Nothing needs deleting and no separate command is involved. That is what makes
the `moved` blocks published modules ship permanently work: `terraform-aws-modules`
writes them under a `Migrations: vX -> vY` header, and a consumer of a pinned
module cannot delete upstream source. Once the tag has been rewritten the block
is simply a no-op on every later run, because the old address matches nothing.

Resource renames, root-to-module refactors, cross-module moves, module renames,
chains of two or more statements, and destinations expanded with `count` all
carry. The last matters more than it sounds: `count = var.create ? 1 : 0` is how
every `terraform-aws-modules` resource is written, so it is what most shipped
`moved` blocks land on.

**Why the rest are banned.** Three shapes cannot be aliased safely, and the
danger is one-directional - a block admitted but not aliased leaves the live
resource reading as an orphan at the old address (the plan proposes destroying
it) while the new address reads as absent (the plan proposes creating it), which
is one cloud object and two wrong beliefs:

- The address it moves *from* is still declared. Nothing is vacated, so the live
  resource stays bound to the old address and the destination is created fresh -
  the opposite assignment to stock's, over two objects a later change could tell
  apart. Stock refuses this too, as "Moved object still exists".
- The two endpoints name different resource types. A marker names the type of
  the resource it is written on, so an alias across types could never match.
- An endpoint passes through a `count`-expanded module instance. `count`
  renumbers every address beneath it, so an alias into one would name addresses
  that move under their own markers - the same step `choudoufu live-mv` refuses,
  and the reason "child-module" refuses `count` modules outright.

**Forwarding address.** `choudoufu live-mv <old-address> <new-address>`, the
marker rewrite that plays the same role by editing the live resource's
`tofu-address` tag directly (`live/MARKERS.md`, "The rename rule").

**Enforcement.** `RuleMovedBlock`, `internal/live/lint/lint.go`
(`checkMovedBlocks`), over the predicate in `internal/live/moved`
(`Honourable`) that `internal/live/discovery` builds its alias index from - one
predicate, so lint cannot admit a shape discovery does not alias. Fixture at
`live/e2e/limits/moved-block/`.

### child-module

**Construct.** A `module` block, at any depth, expanded with a `count` that
is not statically evaluable or whose own arguments read `count.index`, or
expanded with `for_each` whose keys cannot be enumerated from configuration
alone. A static module call, a statically-evaluable `count` module call
whose own arguments do not read `count.index`, and a `for_each` module call
whose keys *can* be enumerated, are not this limitation: see below.

**Why banned.** `for_each` on a module block is refused only when this pass
cannot compute its keys before anything is read from the cloud: an instance
key becomes part of every address inside the module, and an address that is
not knowable yet cannot become part of a marker yet either, the same reason
a resource's own non-static `for_each` is refused (by identity resolution,
not lint; see below). `count` on a module block is refused in two narrower
cases (issue #195, which reversed the earlier unconditional ban): a count
expression this pass cannot statically evaluate, and a statically-evaluable
count whose own arguments still read `count.index` - the same hazard
`RuleCountIndex` guards a resource's own body against (issue #192, see
`count-index-in-tag` below), applied to a module call's arguments instead.
A plain, static integer count that never leaks
`count.index` into the call's own arguments is not positionally fragile:
`module.name[i]` is exactly as stable an address as `resource.name[i]`, and
shrinking count only ever retires the highest index, never renumbers a
survivor, which is why that shape is admitted rather than refused
permanently.

**A static module call is admitted.** As of issue #59, phase 2 ("59b"), the
five packages downstream of lint (`identity`, `discovery`, `stamp`,
`projection`, `mv` - traverse `cfg.Children` recursively, and a resource
inside a static module binds by its module-qualified address
(`module.a.module.b.aws_x.y`) exactly as soundly as a root resource binds by
its own. `RuleChildModule` reports nothing for a module call that sets
neither `count` nor `for_each`. A `provider` block declared inside that
module is a separate question, policed by `RuleModuleProviderBlock` (see
the `module-provider-block` entry under "Documented, not yet enforced"):
since issue #201 it is admitted and honoured, not refused, when (as here)
no call in the chain reaching it uses `count`, `for_each`, `enabled` or
`depends_on`.

Calling one module *source* more than once - `./impl` once per domain, the
ordinary way a configuration is factored - is admitted too, and until issue
#280 that admission was wrong in a way nothing here reported. Seven calls of
one source are parsed once, because the HCL parser caches a file by its
name, so all seven reached one syntax tree; the stamping pass wrote a
literal `tofu-address` into it once per call and the last call's address was
the one all seven live objects carried. The apply reported success and the
next run was a hard "Two live resources claiming one address". Each call now
gets its own copy of the nodes stamping mutates
(`internal/live/stamp/sharedbody.go`), and a body two resources somehow
still share is refused rather than stamped. Crossed against an emulator at
`live/e2e/repeated-module/`.

**A statically-evaluable `count` module call with no `count.index` leak is
admitted.** As of issue #195, a module call's `count` is evaluated the same
way a resource's own `count` is: a literal, or an expression built from
variables, locals, `path` and `terraform` values. When the expression is
statically evaluable and none of the call's own arguments read
`count.index` (directly, or by indexing a sibling resource's own
count-expanded collection), `RuleChildModule` reports nothing, and the five
packages traverse each instance - `module.app[0].aws_x.y` binds exactly as
soundly as `module.app.aws_x.y` does. A `count` this pass cannot evaluate
at all is refused as non-static; a statically-evaluable `count` whose own
arguments do read `count.index` is refused for the leak, worded like a
resource's own `count.index`-into-identity refusal.

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
evaluate at all, meaning a reference to a resource, a data source, or
anything else outside the static scope, is refused by `RuleChildModule` itself,
worded like a resource's own non-static `for_each` refusal.

**Forwarding address.** For a non-statically-evaluable `count`, or a
`for_each` module whose keys are not statically knowable: move the
module's resources into the root module, or give the module an estate of
its own, with its own directory, its own `live` block, and its own
`estate` name. Two estates are two independent runs, which is the
separation an expanded child module is standing in for. Rewriting the
`count` or `for_each` expression to a literal or a value derived from
variables, locals, `path` or `terraform` is the other way out, the same as
it is for a resource's own `count` or `for_each`. For a statically-
evaluable `count` whose own arguments leak `count.index`: replace
`count.index` with a value that does not depend on the instance's
position - a `for_each` key, or an argument that is the same for every
instance.

**Enforcement.** `RuleChildModule`, `internal/live/lint/child_module.go`
(`checkChildModules`, detail text chosen by `childModuleDetail`, which
reports nothing for a static call, a statically-evaluable non-leaking
`count` call, or a statically-keyed `for_each` call). The `count.index`
leak check is `moduleCallHasCountIndex`, the module-call analogue of
`checkCountIndex`'s own body walk. The `for_each` key evaluation itself is
`identity.ChildModuleKeys` (`internal/live/identity/modulepath.go`), shared
with `resolve.go`'s own module walk so that lint's admission verdict and
identity resolution's traversal never disagree about which keys a module
call expands to. Fixture at `live/e2e/limits/child-module/`, which is a
tree rather than a single file and needs `choudoufu get` before the rule
can be reached, since an uninstalled module block is refused while the
configuration is still being loaded, earlier than any marker code runs.
The fixture carries five module calls: a static call ("network",
admitted), a statically-keyed `for_each` call ("keyed-static", admitted), a
statically-evaluable `count` call with no `count.index` leak ("counted",
admitted), a statically-evaluable `count` call whose own arguments do read
`count.index` ("counted-leaking", refused for the leak), and a `for_each`
call whose keys reference another resource ("keyed", refused as
non-static), so one load proves all three admitted forms pass clean while
the two refused forms each fail for their own named reason.

### backend-block

**Construct.** `terraform { backend "..." { } }`.

**Why a warning, not a refusal.** A backend configures where authoritative
state is stored and locked. Under markers the live system is that store, and
concurrent writes to a record are settled by conditional write - so the
block is simply not read: no file under `internal/live` consults
`mod.Backend`, and `internal/command/live_plan.go`'s design note explains why
avoiding the backend, rather than stubbing it, is what makes "no state was
read or written" structural. GitHub issue #214 demoted this from a fatal
finding once the corpus showed it was the sole thing blocking every estate on
the onboarding ladder's upper rungs, and leaving the block in place carries
no risk: it configures nothing this run touches.

**Forwarding address.** None required. Deleting the block is still the
recommended edit, so the configuration says what actually happens - the
projection, rebuilt from the live system every run and discarded after, is
what a backend would otherwise have stored - but the plan proceeds either
way.

**Enforcement.** `RuleStateBackend`, `internal/live/lint/lint.go`
(`checkStateBackends`), warning severity (`Rule.Severity`,
`internal/live/lint/issue.go`). Fixture at `live/e2e/limits/backend-block/`.

### cloud-block

**Construct.** `terraform { cloud { } }`.

**Why a warning, not a refusal.** A remote state backend under another name,
with remote locking attached. The same story as `backend-block` by a
different syntax, including the demotion: GitHub issue #214.

**Forwarding address.** None required. Deleting the block is still the
recommended edit, same as `backend-block`.

**Enforcement.** `RuleStateBackend`, the same rule as `backend-block`, same
warning severity. The two fixtures exist separately because they are two
distinct HCL forms of one rule, and each should be provably caught on its
own. Fixture at `live/e2e/limits/cloud-block/`.

### unadmitted-type

**Construct.** `aws_acm_certificate_validation`, a resource type outside the
v0 admission table.

**Why bounded.** "The admission rule". A type participates only if its
identity is recoverable from the live system with no memory, by one of the
four admission paths (`live/SURVEY.md`, 67 of 68 top types admitted; the
one out is the excluded-by-rule set below), and the tables that record
admission
(`internal/live/lint/admission_generated.go`, mirrored by
`internal/live/identity`'s `table_generated.go`, the copy the sweep and
identity resolution read) are generated by `tools/row-gen -emit` and carry
a `DO NOT EDIT` header. A type is not added to them by hand.
`aws_nat_gateway` held this fixture's place until the EC2 networking
ratification batch (issue #65) admitted it, and `aws_cloudwatch_event_rule`
until the omitted-bus fallback vocabulary (`Component.Default`, the #175
reversal) let its batch land. `aws_iam_access_key` held it after those and
moved to the `markerless-type` entry below when that rule landed: it is on
the derived markerless roster, so the refusal an operator now sees for it
names the mechanism rather than the table. `aws_acm_certificate_validation`
is what is left, and as of 2026-08-17 it is here for the ordinary reason
rather than a special one. It used to be described as stable - out by
ruling, so no ratification batch would ever retire it. The maintainer
withdrew that ruling: the resource gates whether the certificate is usable,
so an estate does care about it, and "waiter" was a statement about what
the resource means rather than about what can name it. Classified from its
own schema it is parent-derived (`live/survey.json`), because the
provider's identity schema for it requires exactly `certificate_arn`, a
required argument pointing at the taggable, admitted `aws_acm_certificate`.
So this example is admission debt like the rest of the entry, and a future
ratification batch is expected to retire it.

Two kinds of type hit this rule, and the error message used to make no
distinction. Most out-of-table types are simply not wired yet - a scoping
boundary, not a ban. `aws_nat_gateway` was exactly this case until issue
#65's EC2 networking batch reached it, `aws_cloudwatch_event_rule` until
the #175 batch built the `Component` vocabulary its omitted-bus identity
needed, and most of the survey's remaining unadmitted rows still are. The
other kind is out by rule with no wiring batch ever coming, and that half
now has a refusal of its own wherever the reason is derivable: see
`markerless-type` below, which claims every type the markerless roster
vetoes, `aws_iam_access_key` among them. What is left under this heading is
one surveyed top type nobody has ratified a row for yet -
`aws_acm_certificate_validation`, whose hand exclusion was withdrawn on
2026-08-17 and which the survey now classes parent-derived.
`aws_iam_access_key`'s own forwarding is unchanged
by the move: it becomes a lifecycle-layer Op writing to the secret store,
referenced by ARN or pointer, never by value, the same forwarding
`random_password` gets. What cannot be read back there is the resource's
own contents, never the marker, and that is the distinction which took
`aws_secretsmanager_secret_version` off this list on 2026-08-16.
`aws_secretsmanager_secret_version` was a third until 2026-08-16, when the
maintainer withdrew the exclusion: the ownership marker goes into a tag,
never into the secret, so the credential rationale never applied to it. It
is ordinary admission debt now, refused like every other untaggable type
whose identity carries a server-minted component (#233).
`live/SURVEY.md`, "The one the rule excludes", has the full account.

**Forwarding address.** For types not yet covered: the provider survey
(`live/SURVEY.md`) and the generated admission table, which grows as
ratified identity rows are added. Note that provider resource identity
schemas are already plumbed and load-bearing (issue #22): `admitted()`
consults the provider's own schema, and the configuration's naming signal,
*before* this rule refuses anything. Both are conditional, so a
configuration change **can** admit a type that reached this refusal. The
schema fallback runs only when the caller supplied provider schemas
(`admitted()` returns false immediately with none, which is what
`CheckContext` passes). And the naming signal flips a refusal to an
admission when every block of the type sets its identity argument
explicitly. A `*_prefix` argument used in place of the name itself is the
usual reason a type lands here. For the one
type the rule excludes: the lifecycle layer, per its entry in
`live/SURVEY.md`.

**Enforcement.** `RuleUnadmittedType`, `internal/live/lint/lint.go`
(`checkManagedResources`). Fixture at `live/e2e/limits/unadmitted-type/`.

### markerless-type

**Construct.** `aws_emr_instance_group`, or any other resource type on
`internal/live/identity`'s `MarkerlessTypes` roster.

**Why bounded.** Ownership under this fork is two tags on the live object
(`live/MARKERS.md`), and every way a run finds a live object again reads
them off it. A type on this roster fails that twice over: the provider
mints its identity at create time, so no run can compute what the object
will be called, and the type carries no `tags` argument, so the marker that
is the only handle left has nowhere to be written. Applying one would
create a resource the configuration can never see again, and every later
plan would propose creating another.

The roster is derived, not maintained. `tools/row-gen` computes it on every
run from `live/survey-full.json`'s taggability signal and the same
server-assignment verdict that decides admission, and emits it into
`internal/live/identity/markerless_generated.go` alongside
`MarkerlessReason`, the one sentence covering the whole set. The refusal
carries that sentence verbatim rather than a copy, so a type joining or
leaving the roster cannot leave the message describing it wrongly.

This is the distinction the `unadmitted-type` entry above could not make.
That rule closes by asking a reader with a documented import ID to open an
issue naming it - a reasonable request for a type no ratification batch has
reached, and a round trip to be told no for a type the derivation has
already refused on evidence. This rule offers no next step, because there
is none: no configuration edit changes it, and no batch reaches it.

Two of the four credential types the project excludes by standing ruling
(`aws_iam_access_key`, `aws_iot_certificate`) are also on this roster and
report through this rule. Both reasons are true of them; the roster's is
the one the code can derive, and the credential ruling stands behind it
unchanged.

**Forwarding address.** None for the type as written. Where the same cloud
object can be expressed by a taggable parent resource - a policy or
attachment folded into the thing it attaches to - that parent is admitted
in the ordinary way and carries the marker for both. `live/SURVEY.md`'s
untaggable sections are where to check whether a given type has such a
parent.

**Enforcement.** `RuleMarkerlessType`, `internal/live/lint/lint.go`
(`checkManagedResources`), consulted ahead of `RuleUnadmittedType` and
ahead of `admitted()`'s provider-identity-schema fallback - a retracted
type would otherwise be re-admitted from its identity schema with
plan-and-create-only support, which is the one outcome worse than refusing
it. Fixture at `live/e2e/limits/markerless-type/`.

### count-index-in-tag

**Construct.** `count.index` used to *select* something, in an argument that
helps build a resource's live identity. The original motivating case was a
tag (`tags = { Name = "vpc-${count.index}" }`), and that shape is no longer
refused: see "What is safe" below.

**Why banned.** `count.index` in an identity-bearing property is refused
when two instances would render the same value for it, because a live
marker is then written twice onto one cloud object and the two config
addresses become indistinguishable - `live-mv` and every other operation
can act on the wrong resource. It is *not* banned for being an index: a
value built injectively from `count.index` names the same instance on every
run, and is admitted. See "What is safe" below, which is most of this
section.

This anchor kept its name after the rule was narrowed to only the arguments
that could plausibly carry identity (GitHub issue #187): a tag is the
easiest case to picture, but for the great majority of AWS resource types no
tag ever feeds import identity at all, so `count.index` in an ordinary tag,
description, or other non-identity property is no longer refused. What is
still refused is `count.index` in one of the specific arguments a type's own
identity is built from - see "Scope" below.

**What is safe, and why.** GitHub issue #217 reopened this class after a
narrowing landed that enumerated *unsafe* shapes - an index expression's key
position, four named accessor functions, a conditional's own condition -
and treated everything else as safe by default, `*hclsyntax.BinaryOpExpr`
included. `100 + (count.index % 3)` fell straight through: at `count = 5`,
indices 0 and 3 both render `rule_number = 100`, so two distinct config
addresses resolved to the identical live identity - a wrong marker written
onto a real cloud resource, not merely an unrefined refusal. The rule was
inverted in response: it now enumerates the shapes it can *prove* injective
and refuses everything else, a node type it has never seen included.

A shape is safe only if the value it renders is guaranteed distinct for
every index at every `count`, and unchanged for any index that survives a
scale-down. Because OpenTofu always retires the highest `count` index
first, the second half follows for free from the first: a scalar function
of the index that is injective at every `count` cannot change for a
surviving lower index just because a higher one disappeared. The shapes
proved safe:

- The bare index, `count.index` itself.
- A string template with the index interpolated among literal text
  (`"name-${count.index}"`). Decimal rendering of a non-negative integer is
  injective, and concatenating that around a fixed prefix and suffix stays
  injective.
- Addition or subtraction where exactly one operand reads the index (itself
  safely) and the other does not reference it at all - `100 + count.index`,
  `var.base - count.index`. For any value fixed across the resource's
  instances, `x -> x+c` and `x -> c-x` are both injective, regardless of
  what `c` is, including zero. `count.index - count.index` - both operands
  reading the index - is refused: nothing proves that combination stays
  injective, and the degenerate case is identically zero for every
  instance.
- Multiplication where exactly one operand reads the index (itself safely)
  and the other statically evaluates, with no variables at all, to a known
  *nonzero* number - `2 * count.index`. `x -> c*x` is injective only for
  `c != 0`; `count.index * var.n` is refused because this is a syntax-only
  check with no evaluation context for `var.n`, so a zero value can never be
  ruled out, and `count.index * 0` is refused because the constant is known
  and known to be zero.
- A conditional whose own condition does not read the index, with both
  results independently safe or index-free. Every instance takes the same
  branch, so the whole expression reduces to that branch's own proof.

Nesting is walked recursively: an injective operation wrapping a
non-injective one is still refused, because safety is decided over the
whole expression, not at the outermost node.

**The second question, which is the one that usually decides.** The shape
analysis above proves things about an expression over *all* the integers.
That is far more than is needed: what matters is only the indices this
resource's `count` actually produces. So a shape it cannot prove is asked a
second and strictly stronger question, in
`internal/live/lint/count_index_domain.go`:

> An expression that reads `count.index` is admitted exactly when the
> values it actually renders - one per index in the resource's real index
> range - are all known, pure, of one type, and pairwise distinct.

This is not a wider list of blessed shapes. It is the absence of a list:
nothing in that file names `format`, `element`, `modulo`, or any other
operation, and the shapes it admits are whatever the operators and
functions in the expression turn out to do when the values are rendered and
compared. Every entry in the "Refused" list above is refused only when it
actually collides:

- `var.availability_zones[count.index]` over a list of three different
  zones is **admitted**; the same expression over a list that repeats an
  entry within the index range is refused. Indexing a collection was never
  wrong in itself - it is wrong when the collection repeats.
- `format("web-%d", count.index)` is **admitted**, and so is
  `element(local.subnets, count.index)` over distinct subnets.
- `count.index % 3` is **admitted at `count = 3`**, where the modulus is
  the identity map and no two instances can collide, and refused at
  `count = 5`, which is exactly the case that reopened this class.
  Injectivity is a property of a function *on a domain*, and the domain is
  knowable here.
- `min(count.index, 5)` is admitted up to `count = 6` and refused at
  `count = 7`, which is precisely where it stops being injective.

The check falls back to the shape analysis above - which refuses by default
- in every case where it cannot see the real answer, and each of these is a
refusal rather than an assumption:

- The `count` expression is not statically evaluable, so the index range is
  unknown. An unset input variable puts a configuration here.
- The expression reaches outside `var`, `local`, `path`, `terraform` and
  `tofu`. This is what keeps `aws_subnet.this[count.index].id` and
  `element(data.aws_x.y.*.id, count.index)` refused: the collection is a
  managed resource or a data source, so nothing about it is knowable before
  the plan, and the index being static is neither sufficient nor what is
  checked.
- Any rendered value comes back unknown, sensitive, or null. Evaluation
  goes through the same purity gate `internal/live/identity` uses, so an
  impure function such as `uuid()` or `timestamp()` renders unknown and is
  refused rather than looking distinct by accident.
- The rendered values do not all share one type. A heterogeneous tuple
  (`["100", 100][count.index]`) yields a string at one index and a number
  at another; the two are not structurally equal, but both render to the
  marker `100`, so inequality would be the wrong evidence.
- The `count` exceeds 256 (`countIndexDomainMax`). A cost bound, not a
  correctness one.

Scale-down stability comes free here for the same reason it does above:
OpenTofu retires the highest index first, and every value is rendered from
the index alone against a configuration that is otherwise fixed, so a
surviving index renders after a scale-down what it rendered before. What
this does not promise - and the shape analysis never promised either - is
stability across an edit to the configuration itself. Deleting a middle
element of `var.availability_zones` renumbers every index above it, so
`aws_x.r[1]`'s identity moves onto the object `aws_x.r[2]` used to name.

The ownership check catches that, and what it does about it is refuse. A
live object carrying this estate's `tofu-estate` marker and a `tofu-address`
naming a different instance is left out of the prior state, with the
resource's own refusal (`Live resource marked for another address`) naming
both addresses. It is not adopted, and no plan rewrites one instance's
address marker onto another instance's object. From there `live-mv`, or a
`moved` block, is how a human says which object is which - and both are
honoured: a `moved` block's old address is one of the markers the check
accepts, precisely so a pending move is not mistaken for a renumbering.

The displaced object is reported too, which is the other half of the same
question and a different pass's job. The object that used to be `aws_x.r[1]`
still carries `aws_x.r[1]` in its own marker, and the ownership check above
never sees it: that check reads the one object the configuration's identity
fetched, which after the renumbering is a different object entirely.
Discovery is the pass that lists what the cloud holds, so it is the only one
holding both halves - the marker on the object, and the identity the
configuration computes for the address that marker names. When those two
disagree it says so, as `Live resource displaced from the address it is
marked for`.

That finding is a warning and it proposes nothing. The resource is not read,
not changed and not destroyed; it stays in the account until you say which
resource is which, with `live-mv` or a `moved` block. The comparison behind
it is inexact - the identity a configuration computes and the identity a
provider attaches to a listed object are two different things that usually,
not always, spell the same string - so it is built to report only on
positive evidence and to stay silent wherever it cannot compare like with
like. Costing a line of output rather than a destroy is what pays for that.

A module call's arguments are deliberately excluded from this second
question and keep the shape analysis alone. Proving a call's own arguments
distinct per instance would not prove what matters there: the identities of
every resource inside the module, built from those arguments in ways the
call site cannot see.

**Forwarding address.** `for_each`. Key the resource by a stable string
instead of a positional index.

**Scope.** Whether an argument is in scope is read from the same table
`internal/live/identity` uses to resolve identity from configuration, not
asserted independently:

- A logical, record-backed type (`null_resource`, `terraform_data`, and the
  `random_*`/`time_*` families this fork admits) has no argument-derived
  identity at all - its whole existence is a persisted record addressed by
  the resource's own instance address - so nothing in its body is in scope.
- A server-assigned type (most AWS resources whose ID the provider mints at
  create time - `aws_instance`, `aws_vpc`, `aws_security_group`, and
  hundreds more) is matched to the live object by its `tofu-address` marker
  alone; identity resolution never reads a single configuration argument for
  such a type, so nothing in its body is in scope either.
- Otherwise, the type's own import-identity components name exactly which
  top-level arguments are in scope (for `aws_network_acl_rule`:
  `network_acl_id`, `rule_number`, `protocol`, `egress`). A nested block can
  never be in scope this way, because import identity is never built from
  one.
- A type this fork has no identity data for gets no narrowing: every
  argument stays in scope, the conservative default for a type nobody has
  reviewed.

**Deliberate carve-out.** The `tofu-address` marker tag value (see
`live/MARKERS.md`) is exempt from this rule.
`markerKeysExemptFromCountIndex` in `count_index.go` allows `count.index`
there and there alone, because a count instance's canonical address
permanently includes its instance index. That is the marker doing its
specified job rather than leaking into an identity-bearing property.
`tofu-slot` is deliberately not among the exempted keys.

**Enforcement.** `RuleCountIndex`, `internal/live/lint/count_index.go`
(`checkCountIndex`, `countIndexScopeForType`, `analyzeCountIndexSafety`) and
`internal/live/lint/count_index_domain.go`
(`countIndexDomainFor`, `countIndexDomain.verdict`). Within an
argument in scope for the resource's type, `analyzeCountIndexSafety`
recurses over the whole expression and refuses anything it cannot prove
injective, a node type with no case in that function included; what it
refuses is then rendered once per index and admitted if the values are
distinct. The count
expression itself and the other meta-argument positions are out of scope by
construction, for every type (see `internal/live/lint/doc.go`, "Scope of
the count.index rule"). Fixture at `live/e2e/limits/count-index-in-tag/`;
`internal/live/lint/testdata/count-index-nonlinear` pins the modulo,
integer-division, min/max, unprovable-multiplier, both-operands,
truncating-function and bare-comparison shapes, and
`internal/live/lint/testdata/count-index-pure-scalar` pins their admitted
mirrors. `TestCountIndexAdmittedShapesRenderDistinctIdentities` is the test
that makes this safe to widen: it resolves every fixture through
`internal/live/identity` and asserts that everything the rule admits has
pairwise-distinct import IDs and everything it refuses actually collides -
asking the resolver, never the analyzer, which is the check that was
missing when `100 + (count.index % 3)` shipped as safe.

### foreach-invalid-key

**Construct.** A `for_each` key containing one of six characters
(`markerkey.Excluded`: `"`, `\`, `$`, `%`, `[`, `]`) that collide with a
rule outside this fork's own control, e.g. `"a%b"`.

**Why banned.** A `for_each` instance key becomes part of the resource's
`tofu-address` marker, which is written as an AWS resource tag. Before
issue #210, the marker's own escaping could only carry characters AWS
itself allows in a tag value, so anything else - `"a (b)"`, say - was
refused outright, which issue #210 identified as a defect in this fork's
charset rather than a real limitation: stock OpenTofu accepts any string as
a `for_each` key. `markerkey.Encode` now carries almost every printable
character into a marker reversibly, as an `Introducer`-led hex escape, the
same move issue #178 made for `.` and `:` (below) applied to the rest of
the printable range.

What #210 leaves refused is a narrower set of six characters that collide
with a *different*, unrelated escaping rule this package does not own:
`"`, `\` and every non-printable rune are backslash- or `\u`-escaped by
`addrs`' `toHCLQuotedString` when OpenTofu itself renders the "declared"
side of an address comparison, before this package's own escaping ever
runs; `[` and `]` are the delimiters `internal/live/markers`' scanning uses
to find an instance key's boundaries inside a full address string; and `$`
and `%` are doubled by that same `toHCLQuotedString` function when
immediately followed by `{`, a transformation with no per-rune inverse -
#210 refuses both unconditionally rather than only in that one shape, since
the escaping has no way to tell the two cases apart at the point it runs.
None of the six was ever admitted before #210 either, so this is where the
boundary now sits, not a new restriction.

Before issue #178, `.` and `:` were banned here too, alongside everything
truly outside the AWS set: `live/MARKERS.md`'s escaping rule used `.` to
separate an escaped address's segments and `:` to introduce an instance key,
and a key carrying either character raw collided with that rule. Both are
AWS-legal in a tag value, which is what made this class of key dangerous
rather than merely invalid: it passed lint, stamped a marker, and applied
cleanly, and only the *next* run found the wedge (a `:` key read back as a
malformed marker, and a `.` key made `discovery.UnescapeAddress` refuse on
deletion), with no in-band way out. Issue #178 closed the gap instead of
leaving it excluded: `internal/live/markers` now escapes a key's own `.`,
`:` and `@` before it ever reaches an address - doubling `@`, then
substituting `.` and `:` for two-character sequences that cannot collide
with the address's own separators - and reverses the substitution on read,
so both characters are admitted.

**Forwarding address.** Drop the six excluded characters from the key, or
substitute an equivalent that does not collide with the address-rendering
or address-scanning rules they conflict with. An empty key is also
rejected, since an escaped address cannot end in a bare separator.

**Enforcement.** `RuleForEachKey`, `internal/live/lint/foreach_key.go`
(`checkForEachKeys`), which delegates the rune check to
`markerkey.InvalidRune`/`markerkey.Excluded`. For every `for_each`
expression it can evaluate statically, it rejects any key containing one of
the six excluded characters, or the empty string. The same bound is
enforced a second time in `internal/live/identity` (`checkedForEachKeys` in
`foreach_key.go`, which delegates the rune check back to lint), so a
configuration that reaches identity resolution without passing lint still
cannot mint a marker nothing can read back. Fixture at
`live/e2e/limits/foreach-invalid-key/`, which demonstrates the still-refused
`"%"` case; `internal/live/lint/testdata/foreach-key-clean/main.tf` and
`internal/live/lint/testdata/foreach-key/main.tf` pin the much wider
admission issue #210 opened and the exact six-character residue,
respectively.

### overlong-address

**Construct.** A resource whose escaped `tofu-address` would exceed 1024
characters (an absurdly long resource label, `for_each` key, or nesting of
both).

**Why bounded.** AWS caps a single tag value at 256 Unicode characters, a
hard limit stated directly in `live/MARKERS.md`. Since issue #71, an
address that does not fit one tag is split across up to four ordered tags -
`tofu-address`, `tofu-address-2`, `tofu-address-3`, `tofu-address-4` - and
concatenated back into one value on read, so the enforced budget is
`MaxContinuations x MaxTagValue` = 4 x 256 = 1024 characters, not 256. Past
that wider budget, the same rule as before still applies: "An address that
does not fit is a lint-time error, not a truncation. Silently truncating an
ownership key is worse than refusing to admit the resource."

**Forwarding address.** Shorten the resource address, with a shorter label
or a shorter instance key.

**Enforcement.** `RuleOverlongAddress`, `internal/live/lint/overlong_address.go`
(`checkOverlongAddresses`). It escapes each instance address exactly as the
stamped marker would be escaped (per `live/MARKERS.md`, `[` becomes `:`
and `]` and `"` are dropped) and rejects anything past the 1024-character
budget. A plain resource is measured directly, a `for_each` resource is
measured once per statically evaluable key under the same boundary as
`foreach-invalid-key`, and a `count` resource is measured at its highest
index when the count is statically evaluable. Fixture at
`live/e2e/limits/overlong-address/`.

### ignore-changes

**Construct.** `lifecycle { ignore_changes = all }`, or an `ignore_changes`
entry covering the whole `tags` argument or one of the ownership markers
inside it.

**Why banned.** This is the quietest failure the live path had, and it is
worse than a refusal. The stamp pass writes `tofu-estate` and `tofu-address`
into the resource's `tags`, the plan renders that as an in-place update, and
the core then throws the change away, because the configuration asked for
tags to be ignored. Nothing warns. The resource is applied unmarked, the next
run's discovery cannot find it, and every run after that proposes creating a
duplicate of something that already exists.

`ignore_changes = [tags]` is a common idiom, and it is usually added for
exactly the reason that makes it dangerous here: something outside Terraform
writes tags on this resource. Under live markers, this tool is that something.

**Forwarding address.** Ignore the individual keys rather than the argument:
`ignore_changes = [tags["Owner"]]`. A non-marker key is not refused, because
ignoring a tag this tool does not write changes nothing about ownership.

**What is not refused.** `tags_all` is the provider's computed union of `tags`
and the provider-level `default_tags`. Ignoring it does not stop the markers
being written into `tags`, so the update still happens and the rule leaves it
alone.

**Enforcement.** `RuleIgnoreChanges`, `internal/live/lint/ignore_changes.go`
(`checkIgnoreChanges`). Fixture at `live/e2e/limits/ignore-changes/`, whose
fourth resource is the admitted single-key form, pinned by
`TestIgnoreChangesAdmitsAForeignTagKey`, since `TestLimitsEnforced` alone
would pass just as happily if all four were refused.

### module-providers

**Construct.** A module call whose `providers` mapping names an aliased
provider configuration:

```hcl
module "useast1" {
  source    = "./vpc"
  providers = { aws = aws.useast1 }
}
```

**Why banned.** Live mode does not read a module call's `providers` mapping.
The provider cache in `internal/command/live_plan.go` keys on the provider
and its alias alone, omitting the module, and the provider configuration is
read from the root module unconditionally. So that module's resources are
planned and applied against the root's default `aws` configuration instead -
a different account, or a different region.

That is not a difference a plan shows you. The resources are read, written
and swept somewhere other than where the configuration asked, discovery
lists in the wrong place so the estate reads as missing rather than
unreachable, and under the `undeclared_untagged = "delete"` quadrant the
blast radius is considerably worse than a bad plan. Refusing is the honest
answer until the mapping is honoured. Silence is not one of the options.

**Forwarding address.** Configure the whole estate against one provider
configuration, or split it into one configuration per account or region and
run them separately. Aliases themselves work. A resource's own `provider =`
argument is honoured, and `live-plan` carries the alias correctly. It is the
module-call mapping that is not read.

**The other form, and the worse one.** `providers = { aws.primary = aws }`
is the standard `configuration_aliases` form: the alias is on the child side,
and the module's resources write `provider = aws.primary`. Live mode resolves
that address against the *root* module, which declares no `aws.primary`, so
the provider is configured from the environment alone and none of the root
`aws` block's settings reach it. That is refused too, unless the root does
declare a matching aliased block for it to resolve against.

**What is not refused.** `providers = { aws = aws }`, mapping to the default
configuration, describes exactly what live mode already does, so it is
admitted. So is `{ myaws = aws }`, where only the child's local name differs.

**Enforcement.** `RuleModuleProviders`,
`internal/live/lint/module_providers_mapping.go`
(`checkModuleProviderMapping`). Fixture at
`live/e2e/limits/module-providers/`, carrying all three forms. The admitted
default mapping is pinned by `TestModuleProvidersAdmitsTheDefaultMapping`,
since `TestLimitsEnforced` would pass just as happily if every call were
refused.
This is distinct from `module-provider-block` (see "Documented, not yet
enforced" below), which polices provider *blocks* declared inside a child
module (GitHub issue #70): a module can declare no provider block of its
own and still be called with a mapping.

### undeclared-provider-alias

**Construct.** A root resource whose `provider` argument names an alias no
root provider block declares:

```hcl
resource "aws_s3_bucket" "stray" {
  provider = aws.nope
  bucket   = "example"
}
```

**Why banned.** Stock OpenTofu refuses this configuration in the graph
("Provider configuration not present"). Live mode resolves the address much
earlier, during marker discovery, and the lookup miss used to fall through
to an empty provider configuration: the provider was configured from the
environment alone, with nothing from the configuration reaching it and no
diagnostic saying so. The real AWS provider accepts an empty configuration
and reads the environment, so the run simply proceeded, reading, writing
and sweeping against whatever account and region the environment happened to
name. Established by running it (GitHub issue #123): discovery had already
scanned types through other providers before the stray address was even
looked up.

**Forwarding address.** Declare the provider block the alias names -
`provider "aws" { alias = "nope" ... }` - or drop the resource's `provider`
argument to use the default configuration. Aliases that resolve to a
declared root provider block work, and `live-plan` carries them correctly.

**What is not refused.** A resource with no `provider` argument, or one
naming an unaliased provider. An absent root provider block for the
*default* configuration stays legal: that is the documented way a provider
takes everything from the environment, and refusing it would refuse
configurations that work today.

**Enforcement.** `RuleUndeclaredProviderAlias`,
`internal/live/lint/undeclared_provider_alias.go`
(`checkUndeclaredProviderAlias`). Fixture at
`live/e2e/limits/undeclared-provider-alias/`. The admitted twin, an alias a
root provider block does declare, is pinned by `TestCheck`'s
`undeclared-provider-alias-declared` case. `providerConfigValue` in
`internal/command/live_plan.go` backstops the same miss with a hard error
rather than an empty body, for any provider address lint did not see. The
child-module routes into that fallback are `module-providers`' subject.

### child-live-config

**Construct.** A live configuration (a `live` block or an
`estate.chdf.hcl` sidecar file) declared inside a child module:

```
module "vendored" { source = "./mod" }   # ./mod carries estate.chdf.hcl
```

**Why banned.** Live mode reads the root module's live configuration only.
A child module's own was decoded and then read by nobody, so its resources
were silently absorbed into the calling estate, and the module's declared
estate boundary reinterpreted with nothing said, the same misattribution
class `module-providers` and `undeclared-provider-alias` refuse one level
down. Found by the wave-3 adversarial audit of the sidecar work (#72): the
sidecar exists precisely so a module repository can check the file in,
which made the silent case likely rather than exotic.

**Forwarding address.** Declare the live configuration at the root, and remove
it from the module. A module that should be its own estate is a root
module of its own run.

**What is not refused.** A child module with no live configuration of its
own, the ordinary case, and the root's own block or sidecar. Both forms
present in one module is a separate, earlier error from the decoder.

**Enforcement.** `RuleChildLiveConfig`,
`internal/live/lint/child_live_config.go` (`checkChildLiveConfig`).
Fixture at `live/e2e/limits/child-live-config/`.

### policy-verb

**Construct.** A `policy` block inside a `live` block assigning a verb to an
ownership quadrant that verb is not allowed in, such as `declared_tagged =
"delete"`.

**Why bounded.** The ownership matrix (GitHub issue #67) crosses two
questions, does the configuration declare this resource and does it carry
this estate's marker, and each of the four answers admits a different set
of safe verbs. `internal/live/policy`'s `ValidVerbs` is that matrix.
Declared and tagged is the ordinary converge path. A delete there would turn
an edit to a resource block into a destroy of the live object it names.
Assigning a verb the matrix does not allow is refused rather than clamped,
because clamping would run a policy the author did not write while reporting
success.

**Forwarding address.** Pick a verb the quadrant allows. The refusal lists
them. If the intent was to remove resources the configuration still declares,
delete the blocks instead, which is the ordinary destroy path, and it leaves
a plan to review.

**Enforcement.** `RulePolicyVerb`, `internal/live/lint/policy.go`
(`checkLivePolicy`). Fixture at `live/e2e/limits/policy-verb/`. An omitted
quadrant is not checked: it resolves to `internal/live/policy.DefaultVerb`,
which is valid for its quadrant by construction, so existing estates that
write no policy block change nothing.

### policy-scope

**Construct.** `undeclared_untagged = "delete"` in a `policy` block with no
`scope` block, or with a `scope` block naming no service, type or region.

**Why bounded.** That one quadrant is the only one whose delete reaches
resources this configuration has never named *and* which carry no marker of
this estate's. It is account reconciliation with aws-nuke semantics, and
unscoped it is an account-wide purge. An empty `scope` block does not
satisfy the requirement either, because it narrows nothing and would
otherwise be a way to acknowledge the rail without accepting it. The other
delete quadrant, `undeclared_tagged`, needs no scope and is not checked:
those resources already carry this estate's ownership marker, so the marker
is the scope, and it is the ordinary orphan sweep `DefaultVerb` assigns
there anyway.

**Forwarding address.** Add a `scope` block naming at least one service,
type or region the purge may reach.

**Enforcement.** `RulePolicyScope`, `internal/live/lint/policy.go`
(`checkLivePolicy`, `scopeIsSet`). Fixture at
`live/e2e/limits/policy-scope/`.

### policy-threshold

**Construct.** A `policy` block whose `threshold` argument is zero.

**Why bounded.** The threshold is the delete quadrant's first-run guard: a
run proposing more deletions than it allows stops and asks. It exists to be
raised deliberately, once the roster it would remove has been reviewed. Zero
expresses neither a reviewed roster nor a guard.

Zero is the only value that reaches this rule. A negative or fractional
threshold is refused a stage earlier, by the decoder
(`internal/configs/live.go`, "Invalid threshold"), which leaves `ThresholdSet`
false so lint never sees it. Zero decodes cleanly because it is an ordinary
non-negative whole number, which is why the refusal has to be here.

**Forwarding address.** Set a positive whole number, or omit the argument
and take the default.

**Enforcement.** `RulePolicyThreshold`, `internal/live/lint/policy.go`
(`checkLivePolicy`). Fixture at `live/e2e/limits/policy-threshold/`.

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
type, so it cannot see two `bucket` attributes colliding. Identity
resolution runs later in the pipeline and is where that check belongs.
Fixture at `live/e2e/limits/duplicate-identity/`, asserted to produce
zero *lint* issues by `TestLimitsNotYetEnforced` (a parallel fixture at
`internal/live/identity/testdata/duplicate-identity/` already exercises
the resolve-time error itself).

### module-provider-block

**Construct.** A `provider` block declared inside a child module:

```hcl
# inside modules/vpc/main.tf
provider "aws" {
  region = "us-east-1"
}
```

**History.** GitHub issue #70 originally refused every in-module provider
block unconditionally: live mode read provider configurations from the root
module only, so the block was never consulted and the module's resources
were silently served by the root's own provider config instead - possibly a
different account or region than the block asked for, with nothing said
about it. That ruling was measured before it was made (0 of 740
module-source `.tf` files across the ten most-installed
terraform-aws-modules repositories declare an in-module provider block, and
upstream documents the shape as legacy), but the measurement missed a real
site the corpus later found using exactly this shape with none of `count`,
`for_each`, `enabled` or `depends_on` on the call reaching it -
`simpleinfra/terraform/shared/modules/gha-iam-user/main.tf:10`. Stock
OpenTofu accepts that shape (`internal/configs/provider_validation.go`'s
`validateProviderConfigs`, forked verbatim), so refusing it was a parity
gap, not a correct narrower rule. GitHub issue #201 narrowed
`RuleModuleProviderBlock` to match: a module-local provider block is refused
only when the call chain from root down to it passes through a call using
one of those four meta-arguments, mirroring upstream's own condition
exactly, and `internal/live/providerscope.Resolve` was taught to walk
straight to a module's own content-bearing provider block when the chain
does not block it - honouring the block instead of silently falling back to
root, which is what closes the original "nothing said about it" risk for
every shape this fork can still admit.

**Why this is not enforced today.** The one case the rule still refuses -
a blocked call chain reaching a content-bearing local provider block -
cannot be produced by any buildable OpenTofu configuration, in this fork or
in stock OpenTofu: `internal/configs.BuildConfig`'s own
`validateProviderConfigs` hard-errors on that exact combination before
`internal/live/lint` ever runs, at every entry point this fork has
(`internal/live/check.Load`, `internal/configs/configload`, and
`internal/live/lint`'s own `loadConfigDir`). `internal/configs/testdata/
config-diagnostics/nested-provider` is upstream's own fixture proving the
hard error. So `RuleModuleProviderBlock`'s refusal branch is unreachable
through the ordinary live path today and is kept only as defense in depth
for a future caller that builds a `*configs.Config` without going through
`BuildConfig`. This is why the fixture below is classified under
"documented, not yet enforced" rather than "enforced today": there is
nothing left for the rule to enforce that upstream itself does not already
forbid.

**What is admitted, and honoured.** A module-local provider block reached
by a call chain with none of `count`, `for_each`, `enabled` or
`depends_on` - the shape #70 originally refused - is legal, and live mode
now resolves a resource inside that module straight to the block, not to
the root configuration. Provider blocks in the root module, aliased or
not, and a child module declaring no provider block of its own (every
module in the original measured ecosystem), are unaffected either way.

**Forwarding address.** None needed for the admitted shape. For the
unreachable refused shape: move the provider configuration to the root
module and let the module receive it implicitly, or drop the meta-argument
from the call chain. A `providers` mapping on the module call remains
subject to the `module-providers` rule's admitted forms: an unaliased
mapping such as `providers = { aws = aws }` is admitted, an aliased one is
refused.

**Enforcement.** `RuleModuleProviderBlock`,
`internal/live/lint/module_provider_block.go`
(`checkModuleProviderBlocks`), gated by `moduleCallBlocksLocalProviders`
and `configuredProviderBlock`. Fixture at
`live/e2e/limits/module-provider-block/`, which today demonstrates the
admitted-and-honoured shape (`CheckContext()` reports nothing for it,
pinned by `TestLimitsNotYetEnforced`) rather than a refusal. The rule's own
logic - the meta-argument gate and the empty/configured-block split - is
pinned directly against synthetic input by
`internal/live/lint/module_provider_block_test.go`, since no loadable
configuration can drive the refusal branch through `CheckContext()` at
all. The admitted root-level twin, the same provider block declared at
root, is pinned by `TestCheck`'s `module-provider-root` case. This rule
replaced an interim `CheckModuleProviders` warning, since retired.

## Attribute-level residue (warned, never refused)

This section is a policy entry, not a refusal: nothing here stops a run.
It documents an accepted, permanent behavior and the warning that names it.
(No `### <dir>` heading, deliberately, because there is no lint rule and no limits
fixture, because there is nothing enforced. The tests live in
`internal/live/lint/residue_attribute_test.go` instead.)

**The behavior.** Some arguments of admitted, cloud-backed types can never
round-trip a stateless replan. Two schema-visible classes, measured against
hashicorp/aws 6.59.0 in GitHub issue #126 (the `tools/wo-sweep` probe): 10
types / 21 attributes are write-only (`aws_ssm_parameter.value_wo`,
`aws_db_instance.password_wo`, and the other `_wo` twins, the plugin
protocol forbids the provider ever returning their values), and 53 types /
132 attributes are sensitive and settable (`aws_db_instance.password`,
`aws_glue_connection`'s credentials, and their kin, whatever the cloud
would echo, the no-secrets rule keeps out of every ownership marker and
record). Either way, no memory of the configured value survives a run, so
every stateless plan proposes sending it again, forever. That is the same
perpetual diff stock `terraform import` produces for these arguments
(measured for `content` in #105's closing comment): the plan is correct,
the apply converges, and the diff never goes away. Accepted behavior, per
attribute, by #126's ruling.

**The warning.** `lint.CheckResidueAttributes`
(`internal/live/lint/residue_attribute.go`) warns once per resource block
and attribute path when a configuration sets one of these arguments,
deriving the verdict from the live provider schema's own WriteOnly and
Sensitive flags at runtime, with no generated table, so a new provider
release's new `_wo` twin is covered the day it ships. It is a `tfdiags`
warning riding beside the subset check in every live entry point, not a
lint `Issue`: lint issues are fatal by design, and a refusal was ruled out
at both ends. It cannot see the schema-invisible members at all, and 7 of
the sensitive attributes are unconditionally required, so refusing the
argument would refuse the type and undo its admission
(`aws_lightsail_database` without `master_password` is not a valid
configuration).

**What stays uncaught, by name.** `aws_s3_object.content` - the founding
example, produces NO warning. Its schema reads
optional/not-sensitive/not-write-only, indistinguishable from any ordinary
argument. That the provider's Read never fetches an object body is
provider behavior the schema carries no trace of. The same holds for the
`_wo_version` bookkeeping companions (plain optional numbers whose value
exists only in memory of the run) and for any other argument a provider
simply never reads back without marking. A schema-derived check has
nothing to hang these on, and no fuller extraction of the schema would
change that: the signal is absent at the source, not missed by the sweep.
`TestResidueAttributesCannotSeeS3ObjectContent` asserts the silence on
purpose, so the day the schema starts carrying a signal, the claim gets
rewritten rather than silently going stale.

**Forwarding address.** Set these arguments knowingly and let the diff
stand, since it re-sends the same value and converges. For secrets
specifically, the better answer is the one `live/RECEIPTS.md` documents: keep the secret
in a secret manager and reference it by pointer, so the perpetually
re-proposed value is at least not a literal in configuration.

## Every refusal, enumerated

Generated by `tools/limits-gen` from the registries that define what this
fork can refuse. Do not edit the two spans below by hand. Run `just limits`
instead.

The sections above are hand-written and answer a design question: is this
construct usable at all, and what replaces it. This section answers an
operational one: a run was refused, what does the refusal mean. The two are
kept apart because they drift apart for different reasons. A construct's
treatment changes when a decision changes, and this list changes whenever
anybody adds a refusal anywhere.

Six registries feed it, one per stage of the live path plus the class this
fork does not author.

- `internal/live/lint` decides whether a construct is inside the subset at
  all. Most of its rules have a hand-written entry above and the table links
  to it. The three receipt rules are specified in `live/RECEIPTS.md`
  instead. Two of its rules do evaluate expressions, since the `for_each`-key
  and overlong-address budgets both need the keys, so "before anything is
  evaluated" would be too strong.
- `internal/live/identity` refuses a resource whose identity cannot be
  computed from the configuration alone. This is where most of a real
  migration's friction is, and until this section existed almost none of it
  was written down anywhere.
- `internal/live/stamp`, `internal/live/discovery` and
  `internal/live/projection` are the three stages that need a cloud: writing
  the markers, finding the live objects that carry them, and reading those
  objects back into prior state. Their refusals show a dash in the frequency
  columns because no corpus run can reach them, which is exactly why they
  are the ones to write down.
- `internal/live/passthrough` is the class this fork does not author.
  Identity resolution evaluates every `count`, every `for_each` and every
  identity-bearing argument through the static evaluator, and passes on
  whatever that evaluation says. Those diagnostics come from
  `internal/configs`, from the address parser, or from HCL itself. The
  single largest blocker measured anywhere in this repository is one of
  them, and so are two more of the top seven.

**What a pass-through refusal is telling you** is usually the same thing:
something in an identity, a `count` or a `for_each` cannot be worked out
from `var`, `local`, `path` and `terraform` alone. That is the binding rule
of this whole mode, and all three of the pass-through refusals that actually
fire in the corpus are instances of it. The wording varies because the step
that failed varies. A few in the list below are not about static
evaluability at all, such as a working directory the operating system
refused, and each says so in its own entry.

<!-- limits-gen:begin refusal-table -->
| Configs | Sites | Layer | Refusal | Severity | Raised by | Documented at |
|---|---|---|---|---|---|---|
| 125 | 11346 | dataread | Resolves at plan time via a data-source read | error | `internal/live/dataread` | "Resolves at plan time via a data-source read" |
| 116 | 1522 | lint | unadmitted-type | error | `internal/live/lint` | "unadmitted-type" |
| 94 | 441 | lint | markerless-type | error | `internal/live/lint` | "markerless-type" |
| 70 | 512 | lint | logical-resource | error | `internal/live/lint` | "null-resource" / "terraform-data" / "local-file" / "random-password" / "time-sleep" |
| 66 | 1131 | identity | Unable to compute static value | error | `internal/configs` | "Unable to compute static value" |
| 52 | 1032 | lint | count-index | error | `internal/live/lint` | "count-index-in-tag" |
| 51 | 430 | identity | Dynamic value in static context | error | `internal/configs` | "Dynamic value in static context" |
| 36 | 143 | identity | Unresolvable identity | error | `internal/live/identity` | "Unresolvable identity" |
| 31 | 237 | identity | Module output not supported in static context | error | `internal/configs` | "Module output not supported in static context" |
| 30 | 55 | stamp | Unmarked apply of a marker-only resource | error | `internal/live/stamp` | "Unmarked apply of a marker-only resource" |
| 27 | 86 | identity | Identity not resolvable from configuration | error | `internal/live/identity` | "Identity not resolvable from configuration" |
| 23 | 81 | identity | Non-static identity argument | error | `internal/live/identity` | "Non-static identity argument" |
| 20 | 73 | identity | Non-static for_each expression | error | `internal/live/identity` | "Non-static for_each expression" |
| 10 | 51 | identity | Non-static count expression | error | `internal/live/identity` | "Non-static count expression" |
| 10 | 31 | identity | Not an identity attribute | error | `internal/live/identity` | "Not an identity attribute" |
| 7 | 19 | lint | child-module | error | `internal/live/lint` | "child-module" |
| 7 | 16 | dataread | Data source not readable before resolution | error | `internal/live/dataread` | "Data source not readable before resolution" |
| 6 | 7 | identity | Ambiguous list-valued identity argument | error | `internal/live/identity` | "Ambiguous list-valued identity argument" |
| 4 | 43 | identity | Null identity argument | error | `internal/live/identity` | "Null identity argument" |
| 4 | 37 | lint | moved-block | error | `internal/live/lint` | "moved-block" |
| 3 | 5 | identity | Identity argument not set | error | `internal/live/identity` | "Identity argument not set" |
| 2 | 2 | lint | provisioner | error | `internal/live/lint` | "local-exec" / "remote-exec" |
| 1 | 12 | dataread | Data source provider not configurable | error | `internal/live/dataread` | "Data source provider not configurable" |
| 1 | 4 | identity | Invalid operand | error | `hcl` | "Invalid operand" |
| 1 | 2 | lint | module-providers | error | `internal/live/lint` | "module-providers" |
| 1 | 1 | identity | Resource type outside the live-markers subset | error | `internal/live/identity` | "unadmitted-type" |
| 1 | 1 | identity | Two resources with the same identity | error | `internal/live/identity` | "duplicate-identity" |
| 0 | 0 | dataread | Cross-stack outputs unavailable | error | `internal/live/dataread` | "Cross-stack outputs unavailable" |
| 0 | 0 | dataread | Cross-stack state unavailable | error | `internal/live/dataread` | "Cross-stack state unavailable" |
| 0 | 0 | dataread | Data source read failed | error | `internal/live/dataread` | "Data source read failed" |
| - | - | discovery | Address too long to carry an ownership marker | error | `internal/live/discovery` | "overlong-address" |
| - | - | discovery | Cloud Control identifier could not be composed | error | `internal/live/discovery` | "Cloud Control identifier could not be composed" |
| - | - | discovery | Failed to list a resource type | error | `internal/live/discovery` | "Failed to list a resource type" |
| - | - | discovery | Incomplete sweep for undeclared resources | warning | `internal/live/discovery` | "Incomplete sweep for undeclared resources" |
| - | - | discovery | Indistinguishable instances without per-instance markers | error | `internal/live/discovery` | "Indistinguishable instances without per-instance markers" |
| - | - | discovery | Invalid estate name | error | `internal/live/discovery` | "Invalid estate name" |
| - | - | discovery | Listed resource matched more than one tagged resource | error | `internal/live/discovery` | "Listed resource matched more than one tagged resource" |
| - | - | discovery | Listed resource with no identity | error | `internal/live/discovery` | "Listed resource with no identity" |
| - | - | discovery | Listed resource with no readable name | error | `internal/live/discovery` | "Listed resource with no readable name" |
| - | - | discovery | Listed resource with no tags | error | `internal/live/discovery` | "Listed resource with no tags" |
| - | - | discovery | Live resource displaced from the address it is marked for | warning | `internal/live/discovery` | "Live resource displaced from the address it is marked for" |
| - | - | discovery | Malformed ownership marker | error | `internal/live/discovery` | "Malformed ownership marker" |
| - | - | discovery | Malformed slot marker | error | `internal/live/discovery` | "Malformed slot marker" |
| - | - | discovery | No AWS account ID from the provider | warning | `internal/live/discovery` | "No AWS account ID from the provider" |
| - | - | discovery | No configuration to discover against | error | `internal/live/discovery` | "No configuration to discover against" |
| - | - | discovery | No provider access | error | `internal/live/discovery` | "No provider access" |
| - | - | discovery | No slot left to mint | error | `internal/live/discovery` | "No slot left to mint" |
| - | - | discovery | One marker value for two declared addresses | error | `internal/live/discovery` | "One marker value for two declared addresses" |
| - | - | discovery | Owned resource of a type the sweep cannot cover | warning | `internal/live/discovery` | "Owned resource of a type the sweep cannot cover" |
| - | - | discovery | Partial slot markers on a count set | error | `internal/live/discovery` | "Partial slot markers on a count set" |
| - | - | discovery | Resolved resource missing from the configuration | error | `internal/live/discovery` | "Resolved resource missing from the configuration" |
| - | - | discovery | Tagged resource's ARN could not be joined to a resource type | warning | `internal/live/discovery` | "Tagged resource's ARN could not be joined to a resource type" |
| - | - | discovery | Two live resources claiming one address | error | `internal/live/discovery` | "Two live resources claiming one address" |
| - | - | discovery | Two live resources claiming one slot | error | `internal/live/discovery` | "Two live resources claiming one slot" |
| - | - | discovery | Unbound instance with unreadable live markers of its type | warning | `internal/live/discovery` | "Unbound instance with unreadable live markers of its type" |
| - | - | discovery | Unclassified discovery problem | error | `internal/live/discovery` | "Unclassified discovery problem" |
| - | - | discovery | Unique name matched more than one resource | error | `internal/live/discovery` | "Unique name matched more than one resource" |
| - | - | discovery | Unlistable marker-discovered type | error | `internal/live/discovery` | "Unlistable marker-discovered type" |
| - | - | discovery | Unscoped account reconciliation refused | error | `internal/live/discovery` | "policy-scope" |
| 0 | 0 | identity | Ambiguous attribute key | error | `hcl` | "Ambiguous attribute key" |
| 0 | 0 | identity | Attempt to get attribute from null value | error | `hcl` | "Attempt to get attribute from null value" |
| 0 | 0 | identity | Attempt to index null value | error | `hcl` | "Attempt to index null value" |
| 0 | 0 | identity | Call to unknown function | error | `hcl` | "Call to unknown function" |
| 0 | 0 | identity | Circular for_each reference | error | `internal/live/identity` | "Circular for_each reference" |
| 0 | 0 | identity | Circular identity reference | error | `internal/live/identity` | "Circular identity reference" |
| 0 | 0 | identity | Circular reference | error | `internal/configs` | "Circular reference" |
| 0 | 0 | identity | Condition is null | error | `hcl` | "Condition is null" |
| 0 | 0 | identity | Configuration loaded without a static evaluator | error | `internal/live/identity` | "Configuration loaded without a static evaluator" |
| 0 | 0 | identity | Duplicate object key | error | `hcl` | "Duplicate object key" |
| 0 | 0 | identity | Empty per-element identity argument | error | `internal/live/identity` | "Empty per-element identity argument" |
| 0 | 0 | identity | Ephemeral value not allowed | error | `internal/configs` | "Ephemeral value not allowed" |
| 0 | 0 | identity | Error in function call | error | `hcl` | "Error in function call" |
| 0 | 0 | identity | Expression not evaluable here | error | `internal/live/identity` | "Expression not evaluable here" |
| 0 | 0 | identity | Failed to get working directory | error | `internal/configs` | "Failed to get working directory" |
| 0 | 0 | identity | Function calls not allowed | error | `hcl` | "Function calls not allowed" |
| 0 | 0 | identity | Identity derived from a sensitive value | error | `internal/live/identity` | "Identity derived from a sensitive value" |
| 0 | 0 | identity | Identity derived from an impure function | error | `internal/live/identity` | "Identity derived from an impure function" |
| 0 | 0 | identity | Identity table and provider schema disagree | error | `internal/live/identity` | "Identity table and provider schema disagree" |
| 0 | 0 | identity | Inconsistent conditional result types | error | `hcl` | "Inconsistent conditional result types" |
| 0 | 0 | identity | Incorrect condition type | error | `hcl` | "Incorrect condition type" |
| 0 | 0 | identity | Incorrect key type | error | `hcl` | "Incorrect key type" |
| 0 | 0 | identity | Invalid "path" attribute | error | `internal/configs` | "Invalid "path" attribute" |
| 0 | 0 | identity | Invalid "terraform" attribute | error | `internal/configs` | "Invalid "terraform" attribute" |
| 0 | 0 | identity | Invalid 'for' condition | error | `hcl` | "Invalid 'for' condition" |
| 0 | 0 | identity | Invalid attribute in static context | error | `internal/configs` | "Invalid attribute in static context" |
| 0 | 0 | identity | Invalid count | error | `internal/live/identity` | "Invalid count" |
| 0 | 0 | identity | Invalid default value for module argument | error | `internal/configs` | "Invalid default value for module argument" |
| 0 | 0 | identity | Invalid expanding argument value | error | `hcl` | "Invalid expanding argument value" |
| 0 | 0 | identity | Invalid for_each condition | error | `internal/live/identity` | "Invalid for_each condition" |
| 0 | 0 | identity | Invalid for_each key | error | `internal/live/identity` | "Invalid for_each key" |
| 0 | 0 | identity | Invalid for_each set | error | `internal/live/identity` | "Invalid for_each set" |
| 0 | 0 | identity | Invalid for_each value | error | `internal/live/identity` | "Invalid for_each value" |
| 0 | 0 | identity | Invalid function argument | error | `hcl` | "Invalid function argument" |
| 0 | 0 | identity | Invalid index | error | `hcl` | "Invalid index" |
| 0 | 0 | identity | Invalid index key | error | `internal/addrs` | "Invalid index key" |
| 0 | 0 | identity | Invalid nested splat expressions | error | `hcl` | "Invalid nested splat expressions" |
| 0 | 0 | identity | Invalid object key | error | `hcl` | "Invalid object key" |
| 0 | 0 | identity | Invalid path step | error | `hcl` | "Invalid path step" |
| 0 | 0 | identity | Invalid reference | error | `internal/addrs` | "Invalid reference" |
| 0 | 0 | identity | Invalid template interpolation value | error | `hcl` | "Invalid template interpolation value" |
| 0 | 0 | identity | Invalid value for input variable | error | `internal/configs` | "Invalid value for input variable" |
| 0 | 0 | identity | Iteration over non-iterable value | error | `hcl` | "Iteration over non-iterable value" |
| 0 | 0 | identity | Iteration over null value | error | `hcl` | "Iteration over null value" |
| 0 | 0 | identity | Missing map element | error | `hcl` | "Missing map element" |
| 0 | 0 | identity | No configuration to resolve | error | `internal/live/identity` | "No configuration to resolve" |
| 0 | 0 | identity | No configuration to scan | error | `internal/live/identity` | "No configuration to scan" |
| 0 | 0 | identity | Non-static lifecycle.enabled expression | error | `internal/live/identity` | "Non-static lifecycle.enabled expression" |
| 0 | 0 | identity | Non-string identity argument | error | `internal/live/identity` | "Non-string identity argument" |
| 0 | 0 | identity | Not enough function arguments | error | `hcl` | "Not enough function arguments" |
| 0 | 0 | identity | Null condition | error | `hcl` | "Null condition" |
| 0 | 0 | identity | Null value as key | error | `hcl` | "Null value as key" |
| 0 | 0 | identity | Operation failed | error | `hcl` | "Operation failed" |
| 0 | 0 | identity | Per-element identity argument not resolvable | error | `internal/live/identity` | "Per-element identity argument not resolvable" |
| 0 | 0 | identity | Provider function in static context | error | `internal/configs` | "Provider function in static context" |
| 0 | 0 | identity | Reference to a module instance that does not exist | error | `internal/live/identity` | "Reference to a module instance that does not exist" |
| 0 | 0 | identity | Reference to a resource instance that does not exist | error | `internal/live/identity` | "Reference to a resource instance that does not exist" |
| 0 | 0 | identity | Reference to undeclared resource | error | `internal/live/identity` | "Reference to undeclared resource" |
| 0 | 0 | identity | Required variable not set | error | `internal/configs` | "Required variable not set" |
| 0 | 0 | identity | Reserved symbol name | error | `internal/addrs` | "Reserved symbol name" |
| 0 | 0 | identity | Resource type has no orphan recovery | error | `internal/live/identity` | "Resource type has no orphan recovery" |
| 0 | 0 | identity | Sensitive count expression | error | `internal/live/identity` | "Sensitive count expression" |
| 0 | 0 | identity | Sensitive for_each expression | error | `internal/live/identity` | "Sensitive for_each expression" |
| 0 | 0 | identity | Sensitive lifecycle.enabled expression | error | `internal/live/identity` | "Sensitive lifecycle.enabled expression" |
| 0 | 0 | identity | Sensitive value not allowed | error | `internal/configs` | "Sensitive value not allowed" |
| 0 | 0 | identity | Splat of null value | error | `hcl` | "Splat of null value" |
| 0 | 0 | identity | The identity table names something the provider does not have | error | `internal/live/identity` | "The identity table names something the provider does not have" |
| 0 | 0 | identity | Too many function arguments | error | `hcl` | "Too many function arguments" |
| 0 | 0 | identity | Unable to parse provider function | error | `internal/addrs` | "Unable to parse provider function" |
| 0 | 0 | identity | Unable to use variable in static context | error | `internal/configs` | "Unable to use variable in static context" |
| 0 | 0 | identity | Undefined local | error | `internal/configs` | "Undefined local" |
| 0 | 0 | identity | Undefined variable | error | `internal/configs` | "Undefined variable" |
| 0 | 0 | identity | Unknown variable | error | `hcl` | "Unknown variable" |
| 0 | 0 | identity | Unsupported attribute | error | `hcl` | "Unsupported attribute" |
| 0 | 0 | identity | Unsupported each.value reference | error | `internal/live/identity` | "Unsupported each.value reference" |
| 0 | 0 | identity | Unusable data-source result | error | `internal/live/identity` | "Unusable data-source result" |
| 0 | 0 | identity | Variables not allowed | error | `hcl` | "Variables not allowed" |
| 0 | 0 | identity | for_each key cannot be recorded as a marker | error | `internal/live/identity` | live/MARKERS.md, "Ownership semantics" |
| 0 | 0 | identity | for_each over a resource that is not keyed | error | `internal/live/identity` | "for_each over a resource that is not keyed" |
| 0 | 0 | lint | child-live-config | error | `internal/live/lint` | "child-live-config" |
| 0 | 0 | lint | for-each-key | error | `internal/live/lint` | "foreach-invalid-key" |
| 0 | 0 | lint | ignore-changes | error | `internal/live/lint` | "ignore-changes" |
| 0 | 0 | lint | module-provider-block | error | `internal/live/lint` | "module-provider-block" |
| 0 | 0 | lint | overlong-address | error | `internal/live/lint` | "overlong-address" |
| 0 | 0 | lint | policy-scope | error | `internal/live/lint` | "policy-scope" |
| 0 | 0 | lint | policy-threshold | error | `internal/live/lint` | "policy-threshold" |
| 0 | 0 | lint | policy-verb | error | `internal/live/lint` | "policy-verb" |
| 0 | 0 | lint | receipt-leaf | error | `internal/live/lint` | live/RECEIPTS.md, "Guard 4. The leaf rule" |
| 0 | 0 | lint | receipt-secret | error | `internal/live/lint` | live/RECEIPTS.md, "Secrets discipline" |
| 0 | 0 | lint | receipt-value | error | `internal/live/lint` | live/RECEIPTS.md, "Guard 2. Hash-only values, and never SecureString" |
| 0 | 0 | lint | state-backend | warning | `internal/live/lint` | "backend-block" / "cloud-block" |
| 0 | 0 | lint | undeclared-provider-alias | error | `internal/live/lint` | "undeclared-provider-alias" |
| - | - | projection | Cannot decode a persisted record | error | `internal/live/projection` | "Cannot decode a persisted record" |
| - | - | projection | Cannot encode a projected object | error | `internal/live/projection` | "Cannot encode a projected object" |
| - | - | projection | Cannot import for projection | error | `internal/live/projection` | "Cannot import for projection" |
| - | - | projection | Cannot list the record store | error | `internal/live/projection` | "Cannot list the record store" |
| - | - | projection | Cannot persist a record | error | `internal/live/projection` | "Cannot persist a record" |
| - | - | projection | Cannot read a located record | error | `internal/live/projection` | "Cannot read a located record" |
| - | - | projection | Cannot read a parent's identity from the projection | error | `internal/live/projection` | "Cannot read a parent's identity from the projection" |
| - | - | projection | Cannot read a persisted record | error | `internal/live/projection` | "Cannot read a persisted record" |
| - | - | projection | Cannot read for projection | error | `internal/live/projection` | "Cannot read for projection" |
| - | - | projection | Cannot record a located identity | error | `internal/live/projection` | "Cannot record a located identity" |
| - | - | projection | Could not write the discovery hint | error | `internal/live/projection` | "Could not write the discovery hint" |
| - | - | projection | Cyclic parent-derived identities | error | `internal/live/projection` | "Cyclic parent-derived identities" |
| - | - | projection | Empty import identity | error | `internal/live/projection` | "Empty import identity" |
| - | - | projection | Ignoring an additional imported object | error | `internal/live/projection` | "Ignoring an additional imported object" |
| - | - | projection | Live resource marked for another address | error | `internal/live/projection` | "Live resource marked for another address" |
| - | - | projection | Live resource outside this estate | error | `internal/live/projection` | "Live resource outside this estate" |
| - | - | projection | No configuration to project | error | `internal/live/projection` | "No configuration to project" |
| - | - | projection | No identity resolutions to project | error | `internal/live/projection` | "No identity resolutions to project" |
| - | - | projection | No provider access | error | `internal/live/projection` | "No provider access" |
| - | - | projection | No provider for an undeclared resource | error | `internal/live/projection` | "No provider for an undeclared resource" |
| - | - | projection | No state returned by the provider | error | `internal/live/projection` | "No state returned by the provider" |
| - | - | projection | Parent-derived identity with no formula | error | `internal/live/projection` | "Parent-derived identity with no formula" |
| - | - | projection | Persisted record does not match the current schema | error | `internal/live/projection` | "Persisted record does not match the current schema" |
| - | - | projection | Provider produced an invalid object | error | `internal/live/projection` | "Provider produced an invalid object" |
| - | - | projection | Provider unavailable | error | `internal/live/projection` | "Provider unavailable" |
| - | - | projection | Record store write conflict | error | `internal/live/projection` | "Record store write conflict" |
| - | - | projection | Record-backed instance with no record store | error | `internal/live/projection` | "Record-backed instance with no record store" |
| - | - | projection | Record-located instance with no record store | error | `internal/live/projection` | "Record-located instance with no record store" |
| - | - | projection | Resolved instance missing from the configuration | error | `internal/live/projection` | "Resolved instance missing from the configuration" |
| - | - | projection | Unsupported resource type for the provider | error | `internal/live/projection` | "Unsupported resource type for the provider" |
| 0 | 0 | stamp | No configuration to stamp | error | `internal/live/stamp` | "No configuration to stamp" |
| 0 | 0 | stamp | No estate name to stamp with | error | `internal/live/stamp` | "No estate name to stamp with" |
| 0 | 0 | stamp | No provider schemas for marker stamping | error | `internal/live/stamp` | "No provider schemas for marker stamping" |
| 0 | 0 | stamp | Ownership marker conflict | error | `internal/live/stamp` | "Ownership marker conflict" |
| 0 | 0 | stamp | Ownership marker could not be checked | error | `internal/live/stamp` | "Ownership marker could not be checked" |
| 0 | 0 | stamp | Ownership markers not stamped | error | `internal/live/stamp` | "Ownership markers not stamped" |
| - | - | stamp | Two resources share one configuration body | error | `internal/live/stamp` | "Two resources share one configuration body" |

**188 refusals**, from every registry the live path has: `internal/live/lint`'s rule table, and `internal/live/identity`'s, `internal/live/passthrough`'s, `internal/live/stamp`'s and `internal/live/discovery`'s. A refusal blocking nothing is not an error in this table - it is the interesting end of it, and a set assembled by watching output could never contain one. **Severity** is `error` (fatal, stops the run) unless marked `warning`. Two layers can declare `warning` today: a lint rule (GitHub issue #214's `state-backend`) and a discovery refusal, whose severity is read from the same call the diagnostic is built from. A `warning` does not stop the run - it says this run saw less than the whole picture, or found something outside its own coverage - so it is not a blocker and should not be ranked as one.

Counts are from `live/corpus-refusals.json`, over the corpus that artifact names. Read them as a ranking and not as a rate: the corpus leans on module `examples/`, which use variables, conditionals and `dynamic` blocks harder than an ordinary estate does. A dash means the refusal is in the registries but was not measured. Every `stamp` and `discovery` row shows one: those two passes need a cloud, so no corpus run reaches them.
<!-- limits-gen:end refusal-table -->

**The entries below** are the refusals with no hand-written treatment of
their own. Each heading is exactly what that refusal's `DocsRef` points at,
so a refusal in one of the registries always has somewhere to be read
about: `internal/live/check`'s `TestEveryRefusalDocsRefIsResolvable` fails
when one points at a heading nobody wrote.

Two limits on that, stated rather than left to be discovered. The
documentation for four refusals lives outside this file. Three
receipt rules in `live/RECEIPTS.md` and the marker character-set rule
in `live/MARKERS.md` - and the table's "Documented at" column says so. And
the guarantee is over the registries, not over the source: what keeps a new
diagnostic from escaping them is each package's own `refusalscan` test, not
this section.

(The headings here are `####` rather than `###` on purpose. `###` is
reserved for the limits wing's fixture directories, and
`TestLimitationsDocCoversDirs` requires one to exist for each.)

<!-- limits-gen:begin refusal-entries -->
#### Resolves at plan time via a data-source read

**What.** Not a refusal: live-check's finding for a data-source reference in an identity-bearing position that a live-plan resolves by reading the data source before resolution. No edit to the configuration is needed, but the read itself has not been performed - it can still fail at plan time.

**Where.** The dataread pass, raised by `internal/live/dataread`.

**How often.** Blocked 125 configurations in the measured corpus, at 11346 sites.

#### Unable to compute static value

**What.** Something an identity argument, a count or a for_each depends on could not be computed. It is the trailing half of another refusal: the diagnostic before it names what actually failed, and this one names the chain that led there.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked 66 configurations in the measured corpus, at 1131 sites.

#### Dynamic value in static context

**What.** An identity argument, a count or a for_each reads a value that only exists once something has been applied: another resource's attribute, or a data source. It is the catch-all of the static-context checks - a module output and a provider function each get their own refusal instead.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked 51 configurations in the measured corpus, at 430 sites.

#### Unresolvable identity

**What.** An identity could not be built because a reference it depends on failed; the reference's own error explains why.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked 36 configurations in the measured corpus, at 143 sites.

#### Module output not supported in static context

**What.** An identity argument, a count or a for_each reads a child module's output. Module outputs are produced by evaluating the module, which has not happened yet.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked 31 configurations in the measured corpus, at 237 sites.

#### Unmarked apply of a marker-only resource

**What.** Markers could not be written, on a resource whose instances can only ever be found by their ownership marker. It is the error form of the two warnings above - "Ownership markers not stamped" and "Ownership marker could not be checked" - because applying this one unmarked would create a live object no later run could recognise as this estate's.

**Where.** The stamp pass, raised by `internal/live/stamp`.

**How often.** Blocked 30 configurations in the measured corpus, at 55 sites.

#### Identity not resolvable from configuration

**What.** An identity argument reads something resolution cannot follow: a value through a function or operator, an indexed or two-step traversal, an ephemeral resource, or a root it does not evaluate.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked 27 configurations in the measured corpus, at 86 sites.

#### Non-static identity argument

**What.** An identity argument cannot be evaluated from configuration alone, including an impure call reached through a local or written in .tf.json.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked 23 configurations in the measured corpus, at 81 sites.

#### Non-static for_each expression

**What.** A for_each expression cannot be resolved from configuration alone - computed from another resource's attributes, or reading a root that is not statically evaluable.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked 20 configurations in the measured corpus, at 73 sites.

#### Non-static count expression

**What.** A count expression evaluates to null, or to a value not knowable from configuration alone.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked 10 configurations in the measured corpus, at 51 sites.

#### Not an identity attribute

**What.** An identity argument reads an attribute of another resource that is not part of that resource's identity.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked 10 configurations in the measured corpus, at 31 sites.

#### Data source not readable before resolution

**What.** A data source's value is needed to resolve an identity, a count or a for_each, but the data source depends on a managed resource, names one in depends_on, or has an argument that is not statically evaluable, so it cannot be read before the plan.

**Where.** The dataread pass, raised by `internal/live/dataread`.

**How often.** Blocked 7 configurations in the measured corpus, at 16 sites.

#### Ambiguous list-valued identity argument

**What.** A Component.SoleElement identity argument is a statically-written list or set construct with zero elements or more than one; the AWS API, not the configuration's own list order, decides how more than one value composes, so this package will not guess which one to use.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked 6 configurations in the measured corpus, at 7 sites.

#### Null identity argument

**What.** An identity argument evaluates to null.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked 4 configurations in the measured corpus, at 43 sites.

#### Identity argument not set

**What.** The argument carrying this type's identity has no value - most often a *_prefix argument used in place of the name itself.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked 3 configurations in the measured corpus, at 5 sites.

#### Data source provider not configurable

**What.** A data source the phase must read belongs to a provider configuration that cannot be built before the plan: its provider block needs full evaluation, or the provider's own configure call refused - bad or missing credentials land here, quoted.

**Where.** The dataread pass, raised by `internal/live/dataread`.

**How often.** Blocked 1 configuration in the measured corpus, at 12 sites.

#### Invalid operand

**What.** An operator inside a statically evaluated expression was given an operand of the wrong type - arithmetic on a string, for instance.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked 1 configuration in the measured corpus, at 4 sites.

#### Cross-stack outputs unavailable

**What.** A tfe_outputs value the phase must read has no auth surface available: no token argument, no TFE_TOKEN environment variable, and no credentials entry for its host in the CLI configuration (checked offline, at read time, before any read is attempted); or the read itself failed - workspace not found, no current state version, insufficient permissions - quoted from the provider.

**Where.** The dataread pass, raised by `internal/live/dataread`.

**How often.** Blocked no configuration in the measured corpus.

#### Cross-stack state unavailable

**What.** A terraform_remote_state value the phase must read could not be read from its backend: the backend type is not one this binary links, the backend could not be configured or reached, no state exists for the named key or workspace, or the state snapshot could not be decoded (a newer format, or encryption this fork cannot open) - quoted from the backend at read time.

**Where.** The dataread pass, raised by `internal/live/dataread`.

**How often.** Blocked no configuration in the measured corpus.

#### Data source read failed

**What.** The provider returned an error for a pre-resolution data-source read, quoted verbatim. Fatal for the run: resolution built on a missing value would plan to create things that exist.

**Where.** The dataread pass, raised by `internal/live/dataread`.

**How often.** Blocked no configuration in the measured corpus.

#### Cloud Control identifier could not be composed

**What.** A live resource was listed, but the primary identifier Cloud Control needs to describe it could not be assembled from what the list returned.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Failed to list a resource type

**What.** Listing one resource type failed - most often a permission the run does not have, or a service not available in the region. Discovery continues with the types it could list, so an estate spanning that type is only partly seen.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Incomplete sweep for undeclared resources

**What.** The estate-wide sweep could not cover every admitted type, so an owned-but-undeclared resource may exist that this run did not find. A removal plan built on it is not a complete reconciliation.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Indistinguishable instances without per-instance markers

**What.** Several live resources carry one address marker for a count-expanded or for_each-expanded block, with no tofu-slot marker to tell them apart, so which instance is which cannot be decided.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Invalid estate name

**What.** The estate name does not match the tofu-estate marker grammar (a lowercase letter, then letters, digits or hyphens, at most 128 characters).

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Listed resource matched more than one tagged resource

**What.** A live resource was listed with no ownership marker of its own, and its identifier matched more than one resource in the estate's tag index whose marker names this very type. Attaching either one's tags would risk adopting the other's resource, so none was attached.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Listed resource with no identity

**What.** A live resource carries this estate's markers but the listing returned nothing that identifies it, so it cannot be bound to a configuration address.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Listed resource with no readable name

**What.** A live resource of a type this fork recognises by its account-unique name was listed with no readable name at the property the CloudFormation schema says carries it, so it cannot be compared against the configuration - and the type has no tags argument to fall back on.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Listed resource with no tags

**What.** A live resource was listed with no tags at all where markers were expected, so ownership cannot be read from it.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Live resource displaced from the address it is marked for

**What.** A live resource carries this estate's marker for an address the configuration still declares, but the identity that address resolves to names a different live resource - so two resources answer to one address. Nothing is proposed for it; a human says which is which.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Malformed ownership marker

**What.** A live resource carries a tofu-address or tofu-estate tag whose value is not in the marker grammar - hand-edited, truncated, or written by something other than this tool.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Malformed slot marker

**What.** A live resource's tofu-slot tag is not a slot value this run can read.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No AWS account ID from the provider

**What.** The account this run is against could not be resolved, so identities embedding the account cannot be computed and marker discovery has to stand in for them.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No configuration to discover against

**What.** Discovery was given no configuration to match markers against. A caller error, not a configuration one.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No provider access

**What.** Discovery was given no configured provider handle to list live resources with. A caller error, not a configuration one.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No slot left to mint

**What.** Every slot value for a fungible set is taken, so a new instance has nothing to be marked with.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### One marker value for two declared addresses

**What.** Two declared instances escape to the same tofu-address value, so a marker cannot say which of them a live object belongs to. Binding either would be a guess.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Owned resource of a type the sweep cannot cover

**What.** A live resource carries this estate's ownership marker, the configuration no longer declares it, and its type is outside the sweep's universe - admitted by the provider's identity schema rather than by the generated admission table. It is not planned for destruction and no later run will propose one.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Partial slot markers on a count set

**What.** Some instances of a count-expanded resource carry tofu-slot markers and some do not, so the set cannot be read either as slotted or as positional.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Resolved resource missing from the configuration

**What.** Discovery was asked to find a resource the configuration it was given does not declare. The resolutions and the configuration came from different runs; a bug in whatever assembled them, not in the configuration.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Tagged resource's ARN could not be joined to a resource type

**What.** A resource carrying this estate's markers was found by tag, but its ARN does not map to a resource type this run knows, so nothing further can be read about it.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Two live resources claiming one address

**What.** Two live resources carry the same tofu-address marker, so both claim one configuration address. Binding either would be a guess.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Two live resources claiming one slot

**What.** Two live resources carry the same tofu-slot marker within one fungible set.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Unbound instance with unreadable live markers of its type

**What.** A declared instance bound to nothing, so the plan proposes creating it, while the run listed live resources of its type whose ownership markers it could not read. One of them may be this instance's own resource, in which case applying creates a duplicate carrying the same marker instead of adopting it.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Unclassified discovery problem

**What.** Discovery reported a problem whose kind this package has no summary for. A gap in this package rather than anything the configuration did; the kind is named in the detail.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Unique name matched more than one resource

**What.** A resource type recognised by a name AWS documents as unique per account and region turned out not to match one thing: either several live resources carry the declared name, or several declared instances state it. Binding on either would be a guess, so nothing was bound.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Unlistable marker-discovered type

**What.** A type that can only be found by its ownership marker has no listing this run can perform, so resources of that type cannot be discovered at all.

**Where.** The discovery pass, raised by `internal/live/discovery`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Ambiguous attribute key

**What.** An object key in a statically evaluated expression is a bare name that could be either a variable reference or a literal string, so which was meant cannot be decided.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Attempt to get attribute from null value

**What.** An identity argument, a count or a for_each reads an attribute of something that evaluated to null.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Attempt to index null value

**What.** An identity argument, a count or a for_each indexes into something that evaluated to null.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Call to unknown function

**What.** A statically evaluated expression calls a function this run does not have. Static evaluation offers the pure standard library only; a provider-defined function needs a running provider.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Circular for_each reference

**What.** A resource's for_each depends on its own instances, directly or through another resource's for_each.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Circular identity reference

**What.** A resource's identity is composed, directly or transitively, from its own identity.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Circular reference

**What.** A local is defined, directly or transitively, in terms of itself. Only local-to-local cycles are detected here: the static scope pushes a frame when it resolves a local and not when it resolves a variable.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Condition is null

**What.** A conditional inside a statically evaluated expression has a null condition, so neither branch can be chosen.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Configuration loaded without a static evaluator

**What.** The configuration was not loaded through configs.Parser.LoadConfigDir or the configload package. A caller error, not a configuration one.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Duplicate object key

**What.** An object constructor in a statically evaluated expression sets the same key twice.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Empty per-element identity argument

**What.** A Component.PerElement identity argument is a collection with no elements at all; the provider's import identity for such a type is one segment per value, so an empty one names no object.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Ephemeral value not allowed

**What.** A module source or a backend argument resolves to an ephemeral value. It is raised while decoding those two expressions, not during identity resolution: an ephemeral value in an identity argument is refused by identity itself, under "Identity derived from a sensitive value".

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Error in function call

**What.** A function inside a statically evaluated expression returned an error - jsondecode over text that is not JSON, for instance.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Expression not evaluable here

**What.** Static evaluation of an identity argument panicked and was recovered; most often an expression inside a keyed module resolving, several layers down, back to the module call's own each.key or each.value.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Failed to get working directory

**What.** path.cwd could not be resolved because the operating system refused the working directory. An environment failure, not a configuration one.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Function calls not allowed

**What.** A function is called where the surrounding context permits none at all.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Identity derived from a sensitive value

**What.** An identity argument reads a sensitive or ephemeral value. Import identities are written to logs and plan output, so neither can be part of one. When the value is not genuinely secret - tfe_outputs marking its whole result sensitive is the common case - the remedy is nonsensitive(...) around the specific value.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Identity derived from an impure function

**What.** An identity argument calls uuid(), timestamp() or bcrypt(), which return a different value on every evaluation.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Identity table and provider schema disagree

**What.** The identity table and the installed provider's schema differ about a type in a way that is not fatal; reported as a warning.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Inconsistent conditional result types

**What.** A conditional's two branches produce types that cannot be reconciled into one.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Incorrect condition type

**What.** A conditional's condition is not a boolean and cannot be converted to one - most often a string used where a bool was meant.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Incorrect key type

**What.** A map or object is indexed with a key of the wrong type.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid "path" attribute

**What.** path is read with an attribute other than cwd, module or root.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid "terraform" attribute

**What.** terraform is read with an attribute other than workspace, including the terraform.env removed in v0.12.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid 'for' condition

**What.** A for expression's if clause does not evaluate to a boolean.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid attribute in static context

**What.** terraform.applying is read where only configuration is available; it has a value during plan and apply, and none here.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid count

**What.** A count expression is not a whole non-negative number.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid default value for module argument

**What.** A variable's default does not fit its own type constraint, so no value for it can be produced.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid expanding argument value

**What.** A function call expands an argument with ... over something that is not a list or tuple.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid for_each condition

**What.** The if clause of a for_each comprehension over another resource's keys did not evaluate to a known boolean, even though it never reads the comprehension's value variable.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid for_each key

**What.** The key clause of a for_each comprehension over another resource's keys did not evaluate to a known string, even though it never reads the comprehension's value variable.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid for_each set

**What.** A for_each set's element type is not a string.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid for_each value

**What.** A for_each value is neither a map nor a set of strings.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid function argument

**What.** A function inside a statically evaluated expression was given an argument of the wrong type or an unacceptable value.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid index

**What.** A collection is indexed out of range, or with a key it does not have.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid index key

**What.** A reference indexes a resource or module with a key this fork's address parser cannot read - one that is not a literal string or whole number.

**Where.** Raised by `internal/addrs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid nested splat expressions

**What.** Two splat expressions are nested, which has no defined meaning.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid object key

**What.** An object constructor's key does not evaluate to a string and cannot be converted to one.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid path step

**What.** A traversal steps into a value in a way its type does not support.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid reference

**What.** A reference is not a shape this fork's address parser recognises at all - an operator, an index, or a traversal into something that has no attributes.

**Where.** Raised by `internal/addrs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid template interpolation value

**What.** A ${...} interpolation produces a value with no string form, such as a list or an object.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Invalid value for input variable

**What.** The value supplied for a variable does not convert to its declared type.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Iteration over non-iterable value

**What.** A for expression iterates over something that is not a collection.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Iteration over null value

**What.** A for expression iterates over null.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Missing map element

**What.** A map is indexed with a key it does not contain.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### No configuration to resolve

**What.** Resolution was handed an empty configuration. A caller error, not a configuration one.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### No configuration to scan

**What.** Signal collection was handed an empty configuration. A caller error, not a configuration one.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Non-static lifecycle.enabled expression

**What.** A lifecycle.enabled expression cannot be resolved from configuration alone.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Non-string identity argument

**What.** An identity argument evaluates to a value that is not a string.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Not enough function arguments

**What.** A function inside a statically evaluated expression was called with too few arguments.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Null condition

**What.** A for expression's if clause evaluates to null.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Null value as key

**What.** A null is used as an object or map key.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Operation failed

**What.** An arithmetic or comparison operator inside a statically evaluated expression failed - division by zero, for instance.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Per-element identity argument not resolvable

**What.** A Component.PerElement identity argument is neither a list written in configuration, nor a variable or local holding one, nor a for_each element bound to one, so this package cannot say how many segments the identity has or what they are.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Provider function in static context

**What.** A statically evaluated expression calls a provider-defined function, which needs a configured provider this run has not started.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Reference to a module instance that does not exist

**What.** A reference names a module instance the configuration does not expand to.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Reference to a resource instance that does not exist

**What.** A reference names an instance key the target resource does not expand to, or omits one it requires.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Reference to undeclared resource

**What.** A reference, or a for_each parent, names a resource the module does not declare.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Required variable not set

**What.** A non-nullable variable with no default was given no value, so nothing depending on it can be evaluated.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Reserved symbol name

**What.** A reference uses a name this fork reserves for future use, so it cannot be read as a reference to anything that exists.

**Where.** Raised by `internal/addrs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Resource type has no orphan recovery

**What.** The type is admitted by the provider's identity schema rather than by the generated admission table, so it plans and applies but the estate-wide sweep will not list it: deleting its last block leaves the live resource with no run proposing to remove it. Reported as a warning.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Sensitive count expression

**What.** A count expression reads a sensitive or ephemeral value; the instance keys it produces become marker values.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Sensitive for_each expression

**What.** A for_each expression reads a sensitive or ephemeral value; instance keys become marker values, which are written to the cloud.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Sensitive lifecycle.enabled expression

**What.** A lifecycle.enabled expression reads a sensitive or ephemeral value, so whether the resource exists is decided by something this run may not record.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Sensitive value not allowed

**What.** A module source or a backend argument resolves to a sensitive value. Same decoding step as the ephemeral case above, and not the one an identity argument goes through.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Splat of null value

**What.** A splat expression is applied to null.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### The identity table names something the provider does not have

**What.** The identity table builds a type's identity from an argument the installed provider's schema has no such name for; usually provider-version skew.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Too many function arguments

**What.** A function inside a statically evaluated expression was called with too many arguments.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Unable to parse provider function

**What.** A provider:: function reference is not in the form the address parser accepts.

**Where.** Raised by `internal/addrs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Unable to use variable in static context

**What.** A variable declared const = false is read where only configuration is available.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Undefined local

**What.** A reference names a local the module does not declare.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Undefined variable

**What.** A reference names a variable the module does not declare.

**Where.** Raised by `internal/configs` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Unknown variable

**What.** A reference names a symbol that reached evaluation with nothing bound to it - most often each or count read where this run does not supply repetition data.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Unsupported attribute

**What.** A statically evaluated expression reads an attribute the value does not have.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### Unsupported each.value reference

**What.** each.value is used as other than each.value.<attr> when for_each iterates over a resource.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Unusable data-source result

**What.** The data-read phase handed resolution a result it cannot index: not an absolute data resource instance address, or one resource's instances mixing key kinds. A caller error, not a configuration one.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Variables not allowed

**What.** A reference appears where the surrounding context permits no variables at all.

**Where.** Raised by `hcl` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.

**How often.** Blocked no configuration in the measured corpus.

#### for_each over a resource that is not keyed

**What.** for_each iterates a resource that has no instance keys to iterate - one expanded with count, or one using neither count nor for_each.

**Where.** The identity pass, raised by `internal/live/identity`.

**How often.** Blocked no configuration in the measured corpus.

#### Cannot decode a persisted record

**What.** A record read from the record store could not be decoded into the type it describes - a record written by a different version of this tool, or one edited by hand.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cannot encode a projected object

**What.** A live object read from the cloud could not be encoded against the provider's schema for its type.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cannot import for projection

**What.** The provider refused the import this projection needed to read a resource's current state.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cannot list the record store

**What.** The record store could not be listed, so record-backed resources whose configuration block was removed cannot be found.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cannot persist a record

**What.** Writing a record for an effect back to the record store failed.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cannot read a located record

**What.** The record saying which live object a markerless resource owns could not be read: the store failed, the payload did not decode, or it names a different resource address. Reading on would bind the instance to another object's identity.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cannot read a parent's identity from the projection

**What.** A resource whose identity is derived from its parent's could not read that parent, because the parent is not in this projection.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cannot read a persisted record

**What.** The record store could not be read.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cannot read for projection

**What.** The provider refused the read this projection needed to fill in a resource's current state.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cannot record a located identity

**What.** An applied resource whose live object carries no ownership marker had no identity that could be written to the record store, so no later run could find it again and the next plan would propose creating a second one.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Could not write the discovery hint

**What.** Guided discovery's plan-cost hint could not be written to the estate's record store, so the next run pays a full estate sweep instead of a narrowed one.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Cyclic parent-derived identities

**What.** Two or more resources derive their identities from each other, directly or transitively, so none of them can be built first.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Empty import identity

**What.** A resource resolved to an import identity with no content, which no provider can import.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Ignoring an additional imported object

**What.** An import returned more than one object where one was expected; the extra objects are dropped and this says so rather than choosing silently.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Live resource marked for another address

**What.** A live object at the identity a declared instance names carries this estate's marker under a different resource address, or under no address at all, so it is another instance's object (or a malformed marker) and is not projected.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Live resource outside this estate

**What.** A live object bound by discovery carries an estate marker other than this run's, so it belongs to a different estate and is not projected.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No configuration to project

**What.** Projection was given no configuration. A caller error, not a configuration one.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No identity resolutions to project

**What.** Projection was given no identity resolutions to build from. A caller error, not a configuration one.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No provider access

**What.** Projection was given no configured provider handle to read live state with. A caller error, not a configuration one.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No provider for an undeclared resource

**What.** An owned-but-undeclared resource was found whose provider this run has no handle for, so its current state cannot be read. Reported as a warning: the sweep still knows the resource exists.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No state returned by the provider

**What.** A provider read or import returned no object at all, so there is nothing to project for that resource.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Parent-derived identity with no formula

**What.** A resource's identity is meant to be derived from its parent's, and the identity table carries no formula saying how.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Persisted record does not match the current schema

**What.** A record in the record store was written against a different schema for its type than the installed provider now offers.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Provider produced an invalid object

**What.** A provider returned an object that does not conform to its own declared schema.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Provider unavailable

**What.** The provider configuration a resource needs could not be started or configured.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Record store write conflict

**What.** Two runs wrote the same record concurrently, so this run's write was rejected rather than overwriting the other's.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Record-backed instance with no record store

**What.** An effect resource that keeps its whole state in a record was projected with no record_store configured, so there is nowhere to read its prior state from.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Record-located instance with no record store

**What.** A resource whose live object can carry no ownership marker was projected with no record_store configured, so nothing can say which live object it is. Declaring a record_store in the live block is the fix.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Resolved instance missing from the configuration

**What.** Projection was handed a resolution for an address the configuration does not declare. The resolutions and the configuration came from different runs; a bug in whatever assembled them.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### Unsupported resource type for the provider

**What.** A resource's type is not one the configured provider serves.

**Where.** The projection pass, raised by `internal/live/projection`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

#### No configuration to stamp

**What.** Stamping was given no configuration to rewrite. A caller error, not a configuration one.

**Where.** The stamp pass, raised by `internal/live/stamp`.

**How often.** Blocked no configuration in the measured corpus.

#### No estate name to stamp with

**What.** Stamping was given no estate name, or one outside the tofu-estate marker grammar, so there is no value to write into the markers.

**Where.** The stamp pass, raised by `internal/live/stamp`.

**How often.** Blocked no configuration in the measured corpus.

#### No provider schemas for marker stamping

**What.** Stamping was given no provider schemas, so which types can carry a marker cannot be read. A caller error, not a configuration one.

**Where.** The stamp pass, raised by `internal/live/stamp`.

**How often.** Blocked no configuration in the measured corpus.

#### Ownership marker conflict

**What.** The configuration already sets an ownership tag by hand, to a value other than the one this estate's markers require. Overwriting it would move ownership of a live resource without anyone saying so, so the run stops instead.

**Where.** The stamp pass, raised by `internal/live/stamp`.

**How often.** Blocked no configuration in the measured corpus.

#### Ownership marker could not be checked

**What.** An ownership tag is already set in the configuration to an expression this run cannot evaluate, so whether it agrees with this estate's markers is unknown. A warning: a resource that can only be found by its marker gets the error below instead, under its own heading, because [stamper.unstampableAt] swaps the summary as well as the severity.

**Where.** The stamp pass, raised by `internal/live/stamp`.

**How often.** Blocked no configuration in the measured corpus.

#### Ownership markers not stamped

**What.** A resource's tags could not be given this estate's ownership markers - most often an untaggable type, or a tags argument this pass cannot append to. Reported as a warning, because the resource is still identifiable from its configuration. Also the form a marker-only resource takes when this run could not read its type's schema at all: whether it can carry a marker is then unknown rather than known to be impossible, and an unknown is never reported as the error below. A third case is a type that HAS a settable tags map whose documented vocabulary an ownership marker cannot be spelled in - a key space the provider defines rather than the configuration, or a character set and length the escaped address does not fit. GCP's resource-manager tag bindings are the found example: keys must name TagKey objects that already exist, and on several types the field forces replacement when mutated. A type with no tag surface at all stays silent, because being identified by an argument instead of a marker is ordinary and hundreds of types are; a tag surface that exists and cannot be used is not, so it is said out loud.

**Where.** The stamp pass, raised by `internal/live/stamp`.

**How often.** Blocked no configuration in the measured corpus.

#### Two resources share one configuration body

**What.** Two resources in the configuration reached one HCL body, so the ownership marker written for one of them would be the marker the other carries too. A module source called more than once is parsed once - every call shares the syntax tree - and each call is supposed to get its own body for a resource's arguments; this fires when one did not. It is a defect in how the run loaded the configuration rather than a fault in the configuration, and it is a hard error because a marker shared between two live objects is worse than no marker at all.

**Where.** The stamp pass, raised by `internal/live/stamp`.

**How often.** Not measured: absent from the corpus artifact this was generated against.

<!-- limits-gen:end refusal-entries -->

## Behavioral limits (runtime, not lint)

The entries above are lint matters. Each has a fixture directory and an
asserted rule. The limits below are runtime behaviors of the implemented
mode, documented here and asserted by the integration tests named in each.

**`terraform_remote_state` is read from its own backend before resolution
needs it, with backend credentials assumed present.** #179 stage 3 gives it
the same eligibility and read pipeline stage 1 gives an ordinary provider
data source and stage 2 gives `tfe_outputs`: its own arguments (`backend`,
`config`, `workspace`) must be statically evaluable - the same rule any data
source's own arguments draw - and the maintainer's ruling on #181 treats the
backend's actual reachability and credentials as a fact about this run, not
about the configuration, exactly as stage 1 already treats the `aws`
provider block. A backend this binary does not link, one that cannot be
reached, a missing key or workspace, or a state snapshot this fork cannot
decode (a newer format, or encryption it cannot open) all refuse under
"Cross-stack state unavailable", quoted from the backend, never guessed at.
(`internal/live/dataread/read.go`, `reader.readSource`'s `RemoteState`
branch; `internal/builtin/providers/tf/provider.go`'s
`ReadDataSourceEncrypted` is the actual entry point a plain `ReadDataSource`
call has no room for, since it needs the resource's own address and this
run's encryption setup.)

**A foreign stack's remote state can go stale with no way to detect it.**
The read above is honest about what the backend holds right now, but
nothing in a state file marks that its own stack has moved off state
entirely. Once a producer estate adopts live markers itself, it stops
writing that state file - `terraform_remote_state` pointed at it then keeps
reading a snapshot frozen at migration time: real-looking values, silently
stale, the same wrong-marker shape #178 calls data loss, reached through a
foreign stack instead of a local one. This is not mechanically detectable
from this side of the read: a state file carries no "abandoned as of"
marker, and a live-markers estate has no reason to keep writing one just so
a consumer elsewhere can tell. The eventual native answer is an estate
publishing its own outputs into its `record_store` and cross-stack reads
pointed there instead of at a state file that may no longer be truthful;
that is its own design, filed when stage 3's experience says what it needs
(#179's design issue, "Staleness is the honest limitation"). Until then, the
forwarding address for a producer estate that has already adopted live
markers is the same one `terraform_remote_state` itself has always had for
any other cross-stack situation: pass the value across explicitly, or read
the producer's own live resource with a data source of its own type
filtered on its `tofu-estate`/`tofu-address` marker tags (`live/OUTPUTS.md`).

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
ancestor module call, at any depth). The module's several instances share
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
(`site/content/compatibility.md`, "Resources inside a keyed module need
hand-written markers") for the three-line pattern, and
`live/e2e/estate-module-keyed/` for the fixture it comes from. This is not
a lint refusal. A keyed module is admitted (see "child-module" above), and
this is a standing property of what the stamping pass can and cannot
inject into a shared configuration body. (`internal/live/stamp/stamp.go`,
`SkipModuleKeyed` and `moduleKeyedResource`.)

**A `count`'d module call is stamped when it has exactly one instance, and
refused when it has more.** `count` on a module block is answered
differently from `for_each`, and the difference is not a preference: a
`for_each`'d call has a supported hand-written idiom, and a `count`'d call
does not. `RuleChildModule` refuses a module call whose own arguments read
`count.index` (see "child-module" above), so no variable can carry an
instance's index into the child module, so no hand-written marker inside it
can vary per instance either. Nothing but the stamping pass can produce a
correct address there. So stamping resolves the call's `count` itself, with
`identity.ChildModuleCountKeys` - the same evaluation identity resolution
uses to decide the call's instances exist at all - and takes one of three
paths. A `count` of exactly 1 is stamped with that instance's key, so the
marker on `live/e2e/limits/child-module/counted`'s VPC reads
`module.counted[0].aws_vpc.main`, which is the address identity resolution
computes for it; this is also the `count = var.enabled ? 1 : 0` idiom's on
branch. A `count` of 0 has no instances, so the module's resources are not
walked at all and nothing is stamped or reported - the same reading
`resolver.walkModule` gives it by recursing once per instance key. Anything
else - a `count` above 1, or one this pass cannot evaluate - is
`SkipModuleKeyed`, exactly as a `for_each`'d call is, and its forwarding
address is to replace `count` with `for_each`, move the module's resources
into the root module, or give the module an estate of its own.

Before this, stamping read only a module call's `for_each` and qualified
every resource under a `count`'d call with the UNKEYED module path. A
`count = 1` module therefore carried `module.counted.aws_vpc.main`, an
address discovery never looks for; a `count = 3` module put one literal
address onto three real cloud objects, which is GitHub issue #280's defect
by a third route. Both are wrong markers rather than missing ones.
(`internal/live/stamp/stamp.go`, `childExpansion` and `markerBase`;
`internal/live/stamp/modulecontext_test.go`.)

**Marker discovery goes through one provider configuration per run.** An
estate's managed resources may span several provider configurations, and the
sweep handles that. `statelessDiscover` runs one discovery pass per
configuration and `discovery.Merge` combines them, with
`discovery.Request.ScopeProvider` keeping a pass from binding a live object
through the wrong account (issue #69). What is bounded is narrower. The
resources *waiting on marker discovery* must all use one provider
configuration, because a list issued against the wrong account or region
would report an estate as missing rather than as unreachable. A client-named
resource, whose identity is already in the configuration, needs no discovery
and spans provider configurations freely. A server-assigned one does not.
The forwarding address is to split the configuration so the
discovery-needing resources share one provider configuration, and `-target`
does not help, because the check runs over the whole configuration during
discovery before any target filter applies. This is a v0 bound rather than a
permanent one. The multi-pass machinery exists and `ScopeProvider` is
alias-aware, so lifting it is work rather than redesign.
(`internal/command/live_plan.go`, `statelessDiscoveryProvider`. Proven
multi-configuration behavior in `internal/live/discovery`'s
`TestAliasedProvidersAgainstFloci`, fixture at
`internal/live/discovery/testdata/alias-e2e/`.)

**A multi-configuration estate's adoption hint may name the wrong region.**
The hint's `--region` and `--endpoint-url` flags come from the provider
configuration that ran the needs-discovery scan, or from the first of the
sweep's providers in sorted order when nothing needed discovery. For a
foreign resource found under a different provider configuration, that can be
the wrong region. What is wrong is the printed command rather than the plan,
and splitting the hint by provider needs a larger change to
`internal/live/foreign`. Materializing undeclared instances does not go
through the hint: callers use the per-address provider map instead, so an
undeclared instance is created through whichever configuration found it.
(`internal/command/live_plan.go`, `statelessDiscover`'s third return value.)

**Untaggable types carry no ownership marker of their own.** <!-- survey-gen:begin untaggable-admitted -->
`aws_acmpca_certificate_authority_certificate`, `aws_acmpca_policy`,
`aws_alb_listener_certificate`, `aws_alb_target_group_attachment`,
`aws_amplify_domain_association`, `aws_api_gateway_base_path_mapping`,
`aws_api_gateway_documentation_version`, `aws_api_gateway_gateway_response`,
`aws_api_gateway_integration`, `aws_api_gateway_integration_response`,
`aws_api_gateway_method`, `aws_api_gateway_method_response`,
`aws_api_gateway_method_settings`, `aws_api_gateway_model`,
`aws_api_gateway_rest_api_policy`, `aws_api_gateway_usage_plan_key`,
`aws_appflow_connector_profile`, `aws_appstream_fleet_stack_association`,
`aws_appstream_user`, `aws_appsync_api_cache`, `aws_appsync_domain_name`,
`aws_appsync_domain_name_api_association`,
`aws_arczonalshift_autoshift_observer_notification_status`,
`aws_arczonalshift_zonal_autoshift_configuration`, `aws_autoscaling_group`,
`aws_backup_restore_testing_selection`,
`aws_bedrock_model_invocation_logging_configuration`,
`aws_bedrockagentcore_resource_policy`,
`aws_bedrockagentcore_workload_identity`, `aws_cloudfront_cache_policy`,
`aws_cloudfront_monitoring_subscription`,
`aws_cloudfront_origin_request_policy`,
`aws_cloudfront_realtime_log_config`,
`aws_cloudfront_response_headers_policy`, `aws_cloudwatch_dashboard`,
`aws_cloudwatch_event_api_destination`, `aws_cloudwatch_event_archive`,
`aws_cloudwatch_event_connection`, `aws_cloudwatch_event_endpoint`,
`aws_cloudwatch_event_permission`, `aws_cloudwatch_event_target`,
`aws_cloudwatch_log_account_policy`, `aws_cloudwatch_log_metric_filter`,
`aws_cloudwatch_log_resource_policy`, `aws_cloudwatch_log_stream`,
`aws_cloudwatch_log_subscription_filter`, `aws_cloudwatch_log_transformer`,
`aws_cloudwatch_otel_enrichment`,
`aws_codeartifact_domain_permissions_policy`,
`aws_codeartifact_repository_permissions_policy`, `aws_codebuild_webhook`,
`aws_codedeploy_deployment_config`,
`aws_cognito_identity_pool_provider_principal_tag`,
`aws_cognito_identity_pool_roles_attachment`,
`aws_cognito_identity_provider`, `aws_cognito_resource_server`,
`aws_cognito_risk_configuration`, `aws_cognito_user`,
`aws_cognito_user_group`, `aws_cognito_user_in_group`,
`aws_cognito_user_pool_domain`, `aws_cognito_user_pool_ui_customization`,
`aws_config_conformance_pack`, `aws_config_organization_conformance_pack`,
`aws_config_remediation_configuration`,
`aws_connect_user_hierarchy_structure`, `aws_controltower_control`,
`aws_datazone_environment_blueprint_configuration`,
`aws_db_instance_role_association`, `aws_db_proxy_default_target_group`,
`aws_detective_member`, `aws_devopsguru_resource_collection`,
`aws_dynamodb_global_secondary_index`, `aws_dynamodb_global_table`,
`aws_dynamodb_kinesis_streaming_destination`,
`aws_dynamodb_resource_policy`, `aws_ebs_snapshot_block_public_access`,
`aws_ec2_client_vpn_route`, `aws_ec2_managed_prefix_list_entry`,
`aws_ec2_transit_gateway_metering_policy_entry`,
`aws_ec2_transit_gateway_policy_table_association`,
`aws_ec2_transit_gateway_route`,
`aws_ec2_transit_gateway_route_table_association`,
`aws_ec2_transit_gateway_route_table_propagation`,
`aws_ecr_lifecycle_policy`, `aws_ecr_pull_through_cache_rule`,
`aws_ecr_pull_time_update_exclusion`,
`aws_ecr_repository_creation_template`, `aws_ecr_repository_policy`,
`aws_ecrpublic_repository_policy`, `aws_ecs_cluster_capacity_providers`,
`aws_eks_access_policy_association`,
`aws_elasticache_user_group_association`, `aws_emr_security_configuration`,
`aws_emr_studio_session_mapping`, `aws_fsx_s3_access_point_attachment`,
`aws_glue_catalog_table`, `aws_glue_catalog_table_optimizer`,
`aws_glue_classifier`, `aws_glue_data_catalog_encryption_settings`,
`aws_glue_security_configuration`, `aws_glue_user_defined_function`,
`aws_guardduty_member`, `aws_guardduty_organization_admin_account`,
`aws_guardduty_organization_configuration`, `aws_iam_group`,
`aws_iam_group_policy`, `aws_iam_group_policy_attachment`,
`aws_iam_role_policy`, `aws_iam_role_policy_attachment`,
`aws_iam_user_group_membership`, `aws_iam_user_policy`,
`aws_iam_user_policy_attachment`, `aws_inspector2_delegated_admin_account`,
`aws_inspector2_member_association`, `aws_iot_thing`,
`aws_kinesis_resource_policy`, `aws_kms_alias`,
`aws_lambda_function_event_invoke_config`,
`aws_lambda_layer_version_permission`, `aws_lambda_permission`,
`aws_launch_configuration`, `aws_lb_listener_certificate`,
`aws_lb_target_group_attachment`, `aws_lexv2models_bot_locale`,
`aws_lightsail_domain`, `aws_lightsail_lb_certificate`,
`aws_lightsail_static_ip`, `aws_location_tracker_association`,
`aws_macie2_organization_admin_account`, `aws_msk_cluster_policy`,
`aws_msk_scram_secret_association`,
`aws_msk_single_scram_secret_association`, `aws_msk_topic`,
`aws_nat_gateway_eip_association`, `aws_network_acl_rule`,
`aws_network_interface_sg_attachment`,
`aws_networkfirewall_logging_configuration`,
`aws_networkmanager_core_network_policy_attachment`,
`aws_networkmanager_customer_gateway_association`,
`aws_networkmanager_link_association`,
`aws_networkmanager_prefix_list_association`,
`aws_networkmanager_transit_gateway_registration`,
`aws_notifications_notification_hub`,
`aws_opensearchserverless_access_policy`,
`aws_opensearchserverless_lifecycle_policy`,
`aws_opensearchserverless_security_policy`,
`aws_paymentcryptography_key_alias`,
`aws_pinpointsmsvoicev2_resource_policy`,
`aws_prometheus_alert_manager_definition`,
`aws_prometheus_query_logging_configuration`,
`aws_prometheus_scraper_logging_configuration`,
`aws_rds_cluster_role_association`, `aws_redshift_endpoint_access`,
`aws_route`, `aws_route53_cidr_collection`, `aws_route53_cidr_location`,
`aws_route53_hosted_zone_dnssec`, `aws_route53_key_signing_key`,
`aws_route53_record`, `aws_route53_resolver_firewall_rule`,
`aws_route53_zone_association`, `aws_route_table_association`,
`aws_s3_bucket_lifecycle_configuration`,
`aws_s3_bucket_object_lock_configuration`, `aws_s3_bucket_policy`,
`aws_s3_bucket_public_access_block`,
`aws_s3_bucket_replication_configuration`,
`aws_s3_bucket_server_side_encryption_configuration`,
`aws_s3_bucket_versioning`, `aws_s3control_bucket_policy`,
`aws_s3control_multi_region_access_point`, `aws_s3files_file_system_policy`,
`aws_s3tables_table_bucket_policy`, `aws_s3vectors_vector_bucket_policy`,
`aws_sagemaker_model_package_group_policy`, `aws_scheduler_schedule`,
`aws_secretsmanager_secret_policy`, `aws_secretsmanager_secret_rotation`,
`aws_security_group_rule`,
`aws_securityhub_configuration_policy_association`,
`aws_securityhub_member`, `aws_securityhub_organization_admin_account`,
`aws_securityhub_standards_control`,
`aws_securityhub_standards_control_association`,
`aws_service_discovery_instance`, `aws_servicecatalog_portfolio_share`,
`aws_servicecatalogappregistry_attribute_group_association`,
`aws_ses_receipt_filter`, `aws_ses_receipt_rule`,
`aws_ses_receipt_rule_set`, `aws_ses_template`, `aws_sns_topic_policy`,
`aws_sqs_queue_policy`, `aws_ssm_patch_group`, `aws_ssm_resource_data_sync`,
`aws_ssm_service_setting`, `aws_ssoadmin_account_assignment`,
`aws_ssoadmin_application_assignment`,
`aws_ssoadmin_customer_managed_policy_attachments_exclusive`,
`aws_ssoadmin_instance_access_control_attributes`,
`aws_ssoadmin_managed_policy_attachments_exclusive`,
`aws_ssoadmin_permission_set_inline_policy`,
`aws_ssoadmin_permissions_boundary_attachment`,
`aws_transfer_web_app_customization`, `aws_volume_attachment`,
`aws_vpc_block_public_access_options`, `aws_vpc_dhcp_options_association`,
`aws_vpc_endpoint_policy`, `aws_vpc_endpoint_private_dns`,
`aws_vpc_endpoint_route_table_association`,
`aws_vpc_endpoint_security_group_association`,
`aws_vpc_endpoint_subnet_association`, `aws_vpc_ipam_pool_cidr`,
`aws_vpclattice_auth_policy`, `aws_vpclattice_resource_policy`,
`aws_wafregional_web_acl_association`,
`aws_wafv2_web_acl_logging_configuration`, `aws_wafv2_web_acl_rule`,
`aws_workspacesweb_browser_settings_association`,
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
identity, since a bucket policy's `bucket` is the same string as the bucket's
own identity, and the same holds for a role, a topic, a queue, a
route table or a hosted zone, does not need a marker of its own: reading
the parent tells the sweep the child's identity too, so the child's live
existence is one read away with no memory required. This is derived, not a
second hand list: `internal/live/identity`'s `ParentOf` reads the same
`Components` every identity resolution already reads, matched against
which admitted types are themselves taggable
(`live/survey-full.json`'s signal here, the provider's own schema at run
time), and `SingleParentComponent` narrows that to the case where nothing
besides the parent's value is needed, the "named-singleton child" the
identity table's own comments already name for `aws_s3_bucket_policy` and
`aws_sns_topic_policy`. <!-- survey-gen:begin untaggable-parent-read -->
| Type | Parent | Removed by this leg |
|---|---|---|
| `aws_acmpca_certificate_authority_certificate` | `aws_acmpca_certificate_authority` | no (report-only) |
| `aws_alb_listener_certificate` | `aws_alb_listener` | no (report-only) |
| `aws_alb_target_group_attachment` | `aws_alb_target_group` | no (report-only) |
| `aws_amplify_domain_association` | `aws_amplify_app` | no (report-only) |
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
| `aws_appsync_api_cache` | `aws_appsync_api` | no (report-only) |
| `aws_cloudfront_monitoring_subscription` | `aws_cloudfront_distribution` | no (report-only) |
| `aws_cloudwatch_event_target` | `aws_cloudwatch_event_rule` | no (report-only) |
| `aws_cloudwatch_log_transformer` | `aws_cloudwatch_log_group` | no (report-only) |
| `aws_codeartifact_domain_permissions_policy` | `aws_codeartifact_domain` | no (report-only) |
| `aws_codeartifact_repository_permissions_policy` | `aws_codeartifact_repository` | no (report-only) |
| `aws_cognito_identity_pool_provider_principal_tag` | `aws_cognito_identity_pool` | no (report-only) |
| `aws_cognito_identity_pool_roles_attachment` | `aws_cognito_identity_pool` | no (report-only) |
| `aws_cognito_identity_provider` | `aws_cognito_user_pool` | no (report-only) |
| `aws_cognito_resource_server` | `aws_cognito_user_pool` | no (report-only) |
| `aws_cognito_risk_configuration` | `aws_cognito_user_pool` | no (report-only) |
| `aws_cognito_user` | `aws_cognito_user_pool` | no (report-only) |
| `aws_cognito_user_group` | `aws_cognito_user_pool` | no (report-only) |
| `aws_cognito_user_in_group` | `aws_cognito_user_pool` | no (report-only) |
| `aws_cognito_user_pool_ui_customization` | `aws_cognito_user_pool` | no (report-only) |
| `aws_datazone_environment_blueprint_configuration` | `aws_datazone_domain` | no (report-only) |
| `aws_detective_member` | `aws_detective_graph` | no (report-only) |
| `aws_ec2_client_vpn_route` | `aws_ec2_client_vpn_endpoint` | no (report-only) |
| `aws_ec2_managed_prefix_list_entry` | `aws_ec2_managed_prefix_list` | no (report-only) |
| `aws_ec2_transit_gateway_metering_policy_entry` | `aws_ec2_transit_gateway_metering_policy` | no (report-only) |
| `aws_ec2_transit_gateway_policy_table_association` | `aws_ec2_transit_gateway_policy_table` | no (report-only) |
| `aws_ec2_transit_gateway_route_table_association` | `aws_ec2_transit_gateway_route_table` | no (report-only) |
| `aws_ec2_transit_gateway_route_table_propagation` | `aws_ec2_transit_gateway_route_table` | no (report-only) |
| `aws_ecr_lifecycle_policy` | `aws_ecr_repository` | no (report-only) |
| `aws_ecr_repository_policy` | `aws_ecr_repository` | no (report-only) |
| `aws_ecrpublic_repository_policy` | `aws_ecrpublic_repository` | no (report-only) |
| `aws_elasticache_user_group_association` | `aws_elasticache_user_group` | no (report-only) |
| `aws_emr_studio_session_mapping` | `aws_emr_studio` | no (report-only) |
| `aws_glue_catalog_table` | `aws_glue_catalog` | no (report-only) |
| `aws_glue_catalog_table_optimizer` | `aws_glue_catalog` | no (report-only) |
| `aws_glue_data_catalog_encryption_settings` | `aws_glue_catalog` | no (report-only) |
| `aws_glue_user_defined_function` | `aws_glue_catalog` | no (report-only) |
| `aws_guardduty_member` | `aws_guardduty_detector` | no (report-only) |
| `aws_guardduty_organization_configuration` | `aws_guardduty_detector` | no (report-only) |
| `aws_iam_group_policy_attachment` | `aws_iam_policy` | no (report-only) |
| `aws_iam_role_policy` | `aws_iam_role` | no (report-only) |
| `aws_iam_role_policy_attachment` | `aws_iam_role` | no (report-only) |
| `aws_iam_user_group_membership` | `aws_iam_user` | no (report-only) |
| `aws_iam_user_policy` | `aws_iam_user` | no (report-only) |
| `aws_iam_user_policy_attachment` | `aws_iam_user` | no (report-only) |
| `aws_lambda_function_event_invoke_config` | `aws_lambda_function` | no (report-only) |
| `aws_lb_listener_certificate` | `aws_lb_listener` | no (report-only) |
| `aws_lb_target_group_attachment` | `aws_lb_target_group` | no (report-only) |
| `aws_lexv2models_bot_locale` | `aws_lexv2models_bot` | no (report-only) |
| `aws_lightsail_lb_certificate` | `aws_lightsail_lb` | no (report-only) |
| `aws_location_tracker_association` | `aws_location_tracker` | no (report-only) |
| `aws_msk_cluster_policy` | `aws_msk_cluster` | no (report-only) |
| `aws_msk_scram_secret_association` | `aws_msk_cluster` | no (report-only) |
| `aws_msk_single_scram_secret_association` | `aws_msk_cluster` | no (report-only) |
| `aws_msk_topic` | `aws_msk_cluster` | no (report-only) |
| `aws_nat_gateway_eip_association` | `aws_nat_gateway` | no (report-only) |
| `aws_network_acl_rule` | `aws_network_acl` | no (report-only) |
| `aws_network_interface_sg_attachment` | `aws_network_interface` | no (report-only) |
| `aws_networkfirewall_logging_configuration` | `aws_networkfirewall_firewall` | no (report-only) |
| `aws_networkmanager_core_network_policy_attachment` | `aws_networkmanager_core_network` | no (report-only) |
| `aws_networkmanager_customer_gateway_association` | `aws_networkmanager_global_network` | no (report-only) |
| `aws_networkmanager_link_association` | `aws_networkmanager_link` | no (report-only) |
| `aws_networkmanager_prefix_list_association` | `aws_networkmanager_core_network` | no (report-only) |
| `aws_networkmanager_transit_gateway_registration` | `aws_networkmanager_global_network` | no (report-only) |
| `aws_prometheus_alert_manager_definition` | `aws_prometheus_workspace` | no (report-only) |
| `aws_prometheus_query_logging_configuration` | `aws_prometheus_workspace` | no (report-only) |
| `aws_prometheus_scraper_logging_configuration` | `aws_prometheus_scraper` | no (report-only) |
| `aws_route` | `aws_route_table` | no (report-only) |
| `aws_route53_record` | `aws_route53_zone` | no (report-only) |
| `aws_route53_resolver_firewall_rule` | `aws_route53_resolver_firewall_domain_list` | no (report-only) |
| `aws_route53_zone_association` | `aws_route53_zone` | no (report-only) |
| `aws_route_table_association` | `aws_route_table` | no (report-only) |
| `aws_s3_bucket_lifecycle_configuration` | `aws_s3_bucket` | no (report-only) |
| `aws_s3_bucket_object_lock_configuration` | `aws_s3_bucket` | no (report-only) |
| `aws_s3_bucket_policy` | `aws_s3_bucket` | yes |
| `aws_s3_bucket_public_access_block` | `aws_s3_bucket` | no (report-only) |
| `aws_s3_bucket_replication_configuration` | `aws_s3_bucket` | no (report-only) |
| `aws_s3_bucket_server_side_encryption_configuration` | `aws_s3_bucket` | no (report-only) |
| `aws_s3_bucket_versioning` | `aws_s3_bucket` | no (report-only) |
| `aws_s3control_bucket_policy` | `aws_s3control_bucket` | no (report-only) |
| `aws_s3files_file_system_policy` | `aws_s3files_file_system` | no (report-only) |
| `aws_s3tables_table_bucket_policy` | `aws_s3tables_table_bucket` | no (report-only) |
| `aws_s3vectors_vector_bucket_policy` | `aws_s3vectors_vector_bucket` | no (report-only) |
| `aws_sagemaker_model_package_group_policy` | `aws_sagemaker_model_package_group` | no (report-only) |
| `aws_secretsmanager_secret_policy` | `aws_secretsmanager_secret` | no (report-only) |
| `aws_secretsmanager_secret_rotation` | `aws_secretsmanager_secret` | no (report-only) |
| `aws_security_group_rule` | `aws_security_group` | no (report-only) |
| `aws_service_discovery_instance` | `aws_service_discovery_service` | no (report-only) |
| `aws_servicecatalog_portfolio_share` | `aws_servicecatalog_portfolio` | no (report-only) |
| `aws_servicecatalogappregistry_attribute_group_association` | `aws_servicecatalogappregistry_attribute_group` | no (report-only) |
| `aws_sns_topic_policy` | `aws_sns_topic` | no (report-only) |
| `aws_sqs_queue_policy` | `aws_sqs_queue` | no (report-only) |
| `aws_ssm_patch_group` | `aws_ssm_patch_baseline` | no (report-only) |
| `aws_ssoadmin_account_assignment` | `aws_ssoadmin_permission_set` | no (report-only) |
| `aws_ssoadmin_application_assignment` | `aws_ssoadmin_application` | no (report-only) |
| `aws_ssoadmin_customer_managed_policy_attachments_exclusive` | `aws_ssoadmin_permission_set` | no (report-only) |
| `aws_ssoadmin_managed_policy_attachments_exclusive` | `aws_ssoadmin_permission_set` | no (report-only) |
| `aws_ssoadmin_permission_set_inline_policy` | `aws_ssoadmin_permission_set` | no (report-only) |
| `aws_ssoadmin_permissions_boundary_attachment` | `aws_ssoadmin_permission_set` | no (report-only) |
| `aws_transfer_web_app_customization` | `aws_transfer_web_app` | no (report-only) |
| `aws_volume_attachment` | `aws_ebs_volume` | no (report-only) |
| `aws_vpc_endpoint_policy` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_endpoint_private_dns` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_endpoint_route_table_association` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_endpoint_security_group_association` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_endpoint_subnet_association` | `aws_vpc_endpoint` | no (report-only) |
| `aws_vpc_ipam_pool_cidr` | `aws_vpc_ipam_pool` | no (report-only) |
| `aws_wafregional_web_acl_association` | `aws_wafregional_web_acl` | no (report-only) |
| `aws_wafv2_web_acl_rule` | `aws_wafv2_web_acl` | no (report-only) |
| `aws_workspacesweb_browser_settings_association` | `aws_workspacesweb_browser_settings` | no (report-only) |
| `aws_workspacesweb_data_protection_settings_association` | `aws_workspacesweb_data_protection_settings` | no (report-only) |
| `aws_workspacesweb_ip_access_settings_association` | `aws_workspacesweb_ip_access_settings` | no (report-only) |
| `aws_workspacesweb_network_settings_association` | `aws_workspacesweb_network_settings` | no (report-only) |
| `aws_workspacesweb_session_logger_association` | `aws_workspacesweb_session_logger` | no (report-only) |
| `aws_workspacesweb_trust_store_association` | `aws_workspacesweb_trust_store` | no (report-only) |
| `aws_workspacesweb_user_access_logging_settings_association` | `aws_workspacesweb_user_access_logging_settings` | no (report-only) |
| `aws_workspacesweb_user_settings_association` | `aws_workspacesweb_user_settings` | no (report-only) |

**Total.** 127 types swept via a parent read.
<!-- survey-gen:end untaggable-parent-read -->

Being parent-readable only says the sweep can *see* the child. Whether it
can also *remove* it is a narrower, per-type question the parent read
alone does not settle, and the "Removed by this leg" column above is that
answer today rather than a promise about the rest of the row. Wired for
removal this pass: `aws_s3_bucket_policy`, this fork's first read-based
removal, because S3's `GetBucketPolicy` returns a clean "not found" when a bucket
carries none, so a parent read gives the sweep the same yes/no answer a
marker would have, and the bucket name is the whole of the policy's
identity end to end (`internal/live/discovery`'s gated e2e exercises this
against floci). Everything else in the table stays report-only: a plan
still names it, under "Not swept for removal", but does not propose
destroying it. `aws_iam_role_policy` and `aws_iam_role_policy_attachment`
each carry a second, free-standing argument the parent alone does not
supply (the inline policy's own name, the attached policy's ARN).
`aws_route`, `aws_route53_record`, `aws_route_table_association` and
`aws_lb_target_group_attachment` are the same, one component short of
what the parent alone determines. The S3 siblings besides the policy,
and the SNS/SQS policy pair, are structurally named-singleton children
that would let a future pass wire them the same way `aws_s3_bucket_policy`
was wired here, once each one's own "found vs. not found" provider
behavior is checked the way the bucket policy's was. See
`internal/live/identity/parent.go`'s `parentReadRemovable` for the
per-type reasoning as it stands.

**The residue.** <!-- survey-gen:begin untaggable-residue -->
`aws_acmpca_policy`, `aws_appflow_connector_profile`,
`aws_appstream_fleet_stack_association`, `aws_appstream_user`,
`aws_appsync_domain_name`, `aws_appsync_domain_name_api_association`,
`aws_arczonalshift_autoshift_observer_notification_status`,
`aws_arczonalshift_zonal_autoshift_configuration`, `aws_autoscaling_group`,
`aws_backup_restore_testing_selection`,
`aws_bedrock_model_invocation_logging_configuration`,
`aws_bedrockagentcore_resource_policy`,
`aws_bedrockagentcore_workload_identity`, `aws_cloudfront_cache_policy`,
`aws_cloudfront_origin_request_policy`,
`aws_cloudfront_realtime_log_config`,
`aws_cloudfront_response_headers_policy`, `aws_cloudwatch_dashboard`,
`aws_cloudwatch_event_api_destination`, `aws_cloudwatch_event_archive`,
`aws_cloudwatch_event_connection`, `aws_cloudwatch_event_endpoint`,
`aws_cloudwatch_event_permission`, `aws_cloudwatch_log_account_policy`,
`aws_cloudwatch_log_metric_filter`, `aws_cloudwatch_log_resource_policy`,
`aws_cloudwatch_log_stream`, `aws_cloudwatch_log_subscription_filter`,
`aws_cloudwatch_otel_enrichment`, `aws_codebuild_webhook`,
`aws_codedeploy_deployment_config`, `aws_cognito_user_pool_domain`,
`aws_config_conformance_pack`, `aws_config_organization_conformance_pack`,
`aws_config_remediation_configuration`,
`aws_connect_user_hierarchy_structure`, `aws_controltower_control`,
`aws_db_instance_role_association`, `aws_db_proxy_default_target_group`,
`aws_devopsguru_resource_collection`, `aws_dynamodb_global_secondary_index`,
`aws_dynamodb_global_table`, `aws_dynamodb_kinesis_streaming_destination`,
`aws_dynamodb_resource_policy`, `aws_ebs_snapshot_block_public_access`,
`aws_ec2_transit_gateway_route`, `aws_ecr_pull_through_cache_rule`,
`aws_ecr_pull_time_update_exclusion`,
`aws_ecr_repository_creation_template`,
`aws_ecs_cluster_capacity_providers`, `aws_eks_access_policy_association`,
`aws_emr_security_configuration`, `aws_fsx_s3_access_point_attachment`,
`aws_glue_classifier`, `aws_glue_security_configuration`,
`aws_guardduty_organization_admin_account`, `aws_iam_group`,
`aws_iam_group_policy`, `aws_inspector2_delegated_admin_account`,
`aws_inspector2_member_association`, `aws_iot_thing`,
`aws_kinesis_resource_policy`, `aws_kms_alias`,
`aws_lambda_layer_version_permission`, `aws_lambda_permission`,
`aws_launch_configuration`, `aws_lightsail_domain`,
`aws_lightsail_static_ip`, `aws_macie2_organization_admin_account`,
`aws_notifications_notification_hub`,
`aws_opensearchserverless_access_policy`,
`aws_opensearchserverless_lifecycle_policy`,
`aws_opensearchserverless_security_policy`,
`aws_paymentcryptography_key_alias`,
`aws_pinpointsmsvoicev2_resource_policy`,
`aws_rds_cluster_role_association`, `aws_redshift_endpoint_access`,
`aws_route53_cidr_collection`, `aws_route53_cidr_location`,
`aws_route53_hosted_zone_dnssec`, `aws_route53_key_signing_key`,
`aws_s3control_multi_region_access_point`, `aws_scheduler_schedule`,
`aws_securityhub_configuration_policy_association`,
`aws_securityhub_member`, `aws_securityhub_organization_admin_account`,
`aws_securityhub_standards_control`,
`aws_securityhub_standards_control_association`, `aws_ses_receipt_filter`,
`aws_ses_receipt_rule`, `aws_ses_receipt_rule_set`, `aws_ses_template`,
`aws_ssm_resource_data_sync`, `aws_ssm_service_setting`,
`aws_ssoadmin_instance_access_control_attributes`,
`aws_vpc_block_public_access_options`, `aws_vpc_dhcp_options_association`,
`aws_vpclattice_auth_policy`, `aws_vpclattice_resource_policy`,
`aws_wafv2_web_acl_logging_configuration` and `aws_xray_resource_policy`<!-- survey-gen:end untaggable-residue --> are neither taggable nor
parent-readable: the three ECR registry types are account-level singletons
with no admitted parent resource to read at all, and the dashboard, the
KMS alias and the Lambda layer version are each client-named on their own
terms, with no dependency on any other admitted type's identity. The WAF
Classic and WAF Classic Regional match-set entries are a third shape: they
carry no `tags` argument in the pinned v6.59.0 provider (only the rules and
web ACLs of those two services do), and their identity is a bare
server-minted id with no parent argument in it, so neither path reaches
them. For these,
issue #60 changes nothing: destroy the resource before removing its block,
or delete it out of band. Every plan still names this narrower list under
"Not swept for removal". The parent-readable set above is reported there
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
in-place update forever, a standing non-empty plan rather than a stray
line beside a real change. `aws_kms_key`'s `deletion_window_in_days` and
`aws_route53_zone`'s `force_destroy` are the two in the v0 subset: KMS and
Route 53 never return either one, and both are consulted only on destroy.
The estate fixture leaves both at their defaults for exactly this reason
(`live/e2e/estate/keys.tf`, `dns.tf`). If you need a non-default
value for one of these, a marker run will re-propose it on every plan,
that is the cost, and it is visible rather than silent.

## Exclusion cohorts

The residue roster (issue #49): the entries above are what *is* implemented
and enforced. This section names what is not, as a set instead of an
implication, every cohort a resource type can be excluded by, with a
count and a one-sentence reason each, so that "not covered" answers a
per-type question without reading code. `live/residue.go` is the source:
every count below is either computed straight from `live/mapping.json`
(issue #43) and `live/registry.json` (issue #42), or, where a table cannot
carry the judgment, curated data with its evidence in that file's own
comments, the same way `internal/live/lint/admission.go`'s `opsExcluded`
carries the credential and waiter judgments above. A type in none of these
cohorts and also outside the v0 admission table is simply not wired yet,
the scoping boundary the unadmitted-type entry above already describes,
not an exclusion.

When a refused type falls in one of these cohorts, `internal/live/lint`'s
admission refusal names it: one more sentence, in the schema clause's own
voice, appended to the base "not in the table" message. A type in no
cohort gets nothing appended. `internal/live/lint/residue_test.go` pins one
such refusal per cohort below that a TF configuration can actually name,
`CFN-only constructs` cannot be, since no Terraform type maps to a
CloudFormation-only construct, so that cohort is doc-only.

#### Deprecated or EOL services

Eight AWS services this fork holds out of scope by policy: retired,
end-of-life, or being wound down. The service list and its judgment are
curated (`live/residue.go`'s `DeprecatedServices`). Each service's
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
stack mechanics, not AWS infrastructure, the CFN-side mirror of this
fork's logical-resource family above (`null_resource`, `local_file`,
`random_password`, `time_sleep`): a construct whose whole value is
something a template-processing engine tracks, with no live twin any
provider could read back. The list is curated
(`live/residue.go`'s `CFNOnlyConstructs`). The "no TF counterpart" claim
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
`none` is an explicitly counted **unclassified** remainder, never a silent
default. `tools/mapping-gen` assigns `tf-only` and `deprecated-service`
mechanically, each requiring corroboration beyond a name match before it
classifies anything (a name pattern plus the provider's own schema showing
no importable identity, for `tf-only`, and a TF prefix's entire
CloudFormation Registry footprint shipping no working handler, for
`deprecated-service`).
`cfn-unmodeled` is curated only, in `tools/mapping-gen/overlay.json`'s
`cfn_unmodeled` table, since proving a real resource has no CFN model at all
is a per-family judgment call this pass does not make on its own. The three
sections below and "Unclassified Terraform types" further down are what the
754 split into after this pass. A drift test (`TestNoBareNoneOnceEnforced`
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
here, because proving the negative is a family sweep's job, not this pass's).
`via: "cfn-unmodeled"` in `live/mapping.json`, curated only, so this table is
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
mechanical classifier. Entirely computed, grouped by the row's own note.
The registry-backed admission path (issue #40) cannot reach any type in
this cohort by definition. The survey's other admission paths (client-named,
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
`read`, `update`, `delete` and `list` all false), the registry-backed
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
e2e residue (`live/e2e/run.sh`'s `RESIDUE_UNOWNED` and `RESIDUE_CHANGED`),
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
