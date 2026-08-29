# Readiness Tiers: What "100% Coverage" Means

Issue: https://github.com/INTENTIUS/choudoufu/issues/417

"Every type stock supports is admitted" (`HANDOFF.md`, "The foundation") is
the type-parity promise. It says nothing about how an admitted type's
identity survives the loss of anything - a record store, a state file, the
tool itself. "100% coverage for AWS" is a claim people will eventually make
about this fork, and today nothing in the repository says what the claim
covers. This document is the definition: every one of the provider's
resource types is assigned exactly one of four readiness tiers, by what
recovers its identity and at what cost when the strongest recovery path is
gone. It does not say how many types land in each tier - that is a count,
computed from committed artifacts, and it is the next unit's job
(#418) to build the generator that emits it. This document only fixes the
four names, their order of precedence, and what each one means, precisely
enough that #418 can implement against it without a second design pass.

Roughly half the roster has no tag surface at all
(`live/survey-full.json`'s own `path` classification: 786 of 1699 types
taggable, as of the pin this file records - see "Evidence" below for how
that number is read). No tiering scheme that requires every type to reach
the strongest tier can therefore describe this provider. "100% coverage"
has to mean "every type is classified," not "every type is marker-carried."

## Authority

`live/marker_identity_split_test.go`'s doc comment states the distinction
this whole document formalizes, and it is quoted rather than paraphrased
because paraphrase is exactly what has drifted before:

> The marker answers "may I delete this". It does not answer "which object
> is this".

That file exists because admission had drifted toward treating "no marker"
and "no identity" as the same fact, when a resource can be perfectly
identified by its own declaration and still have nowhere to hang a tag -
every association, attachment and membership in the provider is that shape.
`TestMarkerlessVetoNeverContradictsClientNaming` is the guard: it reads
`live/survey-full.json`'s client-named classification and
`internal/live/identity.MarkerlessTypes`' server-minted veto and fails if
the same type is ever both, because "the provider mints this identity" and
"the configuration already supplies it" are contradictory claims that must
never both be true of one type. Tier B and tier C below are exactly the two
sides of that guard, named. `HANDOFF.md`'s "The foundation" section carries
the same idea today under the vocabulary this document also uses -
"rung: tag-governable, derived from configuration, or record-only" - the
tiers below are that rung, split into the four cases the code already
distinguishes and given fixed names.

## The four tiers

### Tier A: marker-carried

**Population.** Every taggable type: the schema carries a settable
top-level `tags` argument (`markers.Taggable`, `internal/live/markers`).
The tag on the object is both the identity - which config address this
object binds to - and the governance surface - what an IAM condition can
name. `live/survey-full.json` classifies 786 types this way at its current
pin (the `"marker"` path in the survey's own taxonomy); `live/MARKERS.md`'s
live-generated count says 682 of the 1027 currently *admitted* types are
marker-governable today, a smaller, admission-gated figure computed the
same way at HEAD. The two numbers answer different questions - one is a
property of the provider's schema, the other of what this fork has ratified
so far - and neither is quoted as the other.

**Mechanism.** Admission path 2 in the code's own vocabulary
(`internal/live/identity.ClassNeedsDiscovery`, `internal/live/lint/lint.go`'s
"three admission paths: client-assigned identity, marker, or
parent-derived"). Binding a declared resource to its live object goes
through a per-type listing against the service's own API
(`live/MARKERS.md`, "Ownership semantics"); the estate-wide sweep for
resources this estate owns but no longer declares goes through the Resource
Groups Tagging API's `GetResources`, which is eventually consistent but
bounded in the safe direction - a sweep can be a run behind, never a marker
lost.

**What `live-import` does.** Reads the state's declared identity, verifies
it against the live object, and - on approval - stamps `tofu-estate` and
`tofu-address` directly onto the resource, the same tag write
`choudoufu live-mv` performs
(`internal/live/liveimport/doc.go`: "read the live object, change only
tofu-estate and tofu-address, refuse any plan that would replace the
resource or move anything else, apply"). After that write the marker is
self-sufficient: no further dependency on the state file that was migrated
from.

**Record loss.** The one tier where losing the record store is not a
structural loss. `HANDOFF.md`'s foundation ruling states it directly: "a
lost record is rebuilt from tags where tags exist." The marker sweep and
the per-type listing are the recovery path, and they require nothing but
the live cloud - no record, no state, no memory of a prior run.

**What an `aws:ResourceTag`-conditioned grant reaches.** Everything, by
construction: the condition reads a tag that exists on the object itself.
`live/MARKERS.md`'s "Granting an estate" section is the worked policy - two
statements, `aws:ResourceTag/tofu-estate` for acting on what the estate
already owns and `aws:RequestTag/tofu-estate` for creating into it - and its
generated service roster (`live/iam-reference.json`) is what says the
condition key is actually honored per action, service by service (495 of
793 EC2 actions, 42 of 68 AutoScaling, and so on). A tier A grant is only as
good as that roster says it is for the service in question, which is why
that section exists as generated fact rather than assertion.

### Tier B: declaration-carried

**Population.** Untaggable types whose identity is fully supplied by the
configuration - either directly (admission path 1, `ClassConcrete`: every
identity attribute is a required argument, the classic case being a
client-assigned name) or composed from a parent's live identity plus
configuration data (admission path 3, `ClassParentDerived`). The attachment
and association family the authority section's test file names by example:
`aws_iam_group_policy_attachment`, untaggable, no ARN, admitted with a
composite of `{group}` `"/"` `{policy_arn}`, both client-supplied.
`live/survey-full.json`'s four matching path buckets - client-named (117),
parent-derived (48), account-derived (48), unique-name (4) - sum to 217 at
its pin; `live/MARKERS.md`'s live count of admitted untaggable types (345 of
1027, joined from the survey's taggability signal against the admission
table) is tier B's admitted population today, the same admission-gated
distinction tier A's two numbers drew.

**Mechanism.** No marker is ever written for a tier B instance - there is
no `tags` argument to write it into. The table row (`DefaultTable`) states
the identity's components; resolution folds them from the evaluated
configuration and, for `ClassParentDerived`, from the parent's own resolved
identity.

**What `live-import` does.** The same read-and-verify pass as tier A, but
`Approve` has no tag to stamp. What it writes instead is the estate's
per-instance record - ruling 1 of `rfc/20260823-foundation-order-ruling.md`
("the record holds the identity of every instance, written by `live-import`
and by every apply") applies to every tier, not only the ones with nowhere
else to put it.

**Record loss.** Recoverable, by recomputing the same formula against the
current configuration and the parent's current identity - the marker
identity split's own point: an untaggable type can still be perfectly
identified by its own declaration. This is not unconditionally free,
though: a `ClassParentDerived` instance's recovery is only as good as its
parent's. A parent that is itself tier A grounds the recursion in a marker
sweep; a parent that is tier B grounds it one level further down. The
recursion is not exception-free in the current code - `RecordFallbackType`
(`internal/live/identity/located.go`) exists precisely because
`aws_autoscaling_group`'s row resolves an instance that states `name`
literally but has nothing to fall back to for the sibling that used
`name_prefix` instead, and falls back to the record store for that one
instance rather than to a marker that was never written. A tier B type's
"recoverable from config plus tagged parents" promise holds for the
declared, name-stating case; the instances that fall through to
`RecordFallbackType` are, for that one instance, borrowing tier C's
recovery cost rather than tier B's.

**What an `aws:ResourceTag`-conditioned grant reaches.** Nothing, on the
object itself - there is no tag. `live/MARKERS.md` states the bound
directly: "Being identifiable without a tag is a different property from
being governable by one, and only the second is what an IAM condition
needs." The 345-type population above is exactly this: admitted, and
identified without a tag. What actually governs a tier B resource under a
marker-scoped policy is its *parent's* tag - `aws:ResourceTag` on the
parent resource, or a `Resource` ARN naming it - which is coarser than a
per-address grant and has to be built by hand into the policy rather than
falling out of the marker grammar the way tier A's does.

### Tier C: record-carried

**Population.** `internal/live/identity.MarkerlessTypes`
(`internal/live/identity/markerless_generated.go`), currently 159 types -
untaggable *and* server-minted, the exact pair
`identity.MarkerlessReason` names: "the provider mints this type's identity
and the type has no tags argument, so every instance would need marker
discovery to be found again and there is nowhere to write the marker."
Guarded disjoint from tier B by the same test the authority section quotes:
`TestMarkerlessVetoNeverContradictsClientNaming` fails the build if a type
is ever classified both client-named and markerless at once.

**Mechanism, as it stands today.** Not a single honest refusal across the
whole population - the record-located route (`identity.LocatedType`, issue
#270) already reaches a subset of it. A markerless type is admitted as
`ClassRecordLocated` when its identity is *fully* recordable: importable
(`identity.NotImportable` clears it - see "Cross-cutting notes" below),
carries no ratified table row of its own, and its provider identity schema
is either a single top-level string `id` or every component of a documented
composite the provider's schema corroborates, with no secret among the
attributes the mechanism reads (`internal/live/identity/located.go`,
`LocatedType` and `recordableIdentitySchema`). With a `record_store`
declared, such a type is admitted silently; without one, the plan refusal
says the support exists and names the missing block
(`markerlessLocatedDetail`, `internal/live/lint/admission.go`) - a fixable
gap, not a permanent one.

The composite half of that mechanism is not actually load-bearing yet.
`located.go`'s own doc comment describes recording "every component of the
provider's own identity schema for a type whose identity is composite," but
the write-back and materialize paths today only ever carry a flat `id`
string (`writeBackLocated` / `materializeLocated`); issue #429
(`identity: record-located payload carries composite identity`) is the
named unlock, evidenced by `tools/row-gen/rejected.json`'s
`aws_cognito_user_pool_client` entry: 18 untaggable types have a partially
read-only primary identifier, 16 of which need a composite payload and are
refused rather than admitted with a fragment, specifically because
recording only part of a composite identity "would trade a plan refusal for
an apply-time failure... recording only PART of a composite identity is the
same trade wearing a disguise."

A markerless type whose identity attribute itself carries a secret is
refused independent of the `record_store` question, under any `strict {
secrets }` setting - `live/LIMITATIONS.md`'s "What the setting does not
reach yet" section: a located record holds only the identity, and for a
type like `aws_iam_access_key` the identity's own secret half is never
returned by a read, so admitting the type would trade a loud refusal for a
silent, permanent loss stock never has.

Two narrower rescues exist and are worth naming precisely rather than
folding into "the mechanism," because neither is the general case. A
**unique-name property** (`tools/row-gen`'s `uniquename.go`, issue #272)
lifts a type out of `MarkerlessTypes` entirely when the provider documents
a name argument as unique within its scope, so the configured name doubles
as the import ID; four types are admitted this way today
(`aws_cloudfront_cache_policy`, `aws_cloudfront_origin_request_policy`,
`aws_cloudfront_response_headers_policy`, `aws_route53_cidr_collection`,
matching `live/survey-full.json`'s own `"unique-name"` path bucket, four
types). A rescued type moves to tier B, not tier C - its identity is once
again fully declaration-supplied. **Parent-scoped confirmation** narrows
candidates by a known parent's identity, but the ledger's own evidence
(`tools/row-gen/rejected.json`'s `aws_api_gateway_resource` and
`aws_apigatewayv2_route` entries) is explicit that this alone never
supplies an identity: "parent-scoped enumeration can CONFIRM an identity
the configuration has already narrowed to one, it cannot SUPPLY one." Every
type that entry names as successfully admitted this way
(`aws_ecs_task_set`, `aws_eks_pod_identity_association`,
`aws_prometheus_anomaly_detector`,
`aws_service_discovery_private_dns_namespace`) is itself taggable - tier A -
and uses the parent scope only to narrow before the marker confirms. An
untaggable type cannot be admitted by parent-scoping alone; it needs the
record store or a unique name.

**What `live-import` does.** For the located-eligible subset with a
`record_store` declared: reads the live object and writes its identity into
the store's separate located namespace, never the delete-authority one -
`ClassRecordLocated`'s own doc comment states why: "Reading a located
record as delete authority would turn a lost or stale key into a cloud
deletion," which is what the separate, enumeration-free namespace in
`internal/live/projection/located.go` exists to make structurally
impossible. For the rest of the 159 - composite identities pending #429,
secret-bearing identities, or types with no schema recordable at all -
there is nothing to migrate, because the type cannot be declared in a
live-markers configuration in the first place; the refusal text says so
without offering a next step ("No configuration edit changes that, and no
future batch reaches it," `internal/live/lint/lint.go`). The object stays
exactly where stock left it.

**Record loss.** For the located-eligible subset, the record is the only
handle - losing it loses the object, the way losing a stock state file
loses it for the same resource under plain OpenTofu. Recovery needs a fresh
migration pass against whatever named the object before (an old state file,
if one still exists) or one of the two narrow rescues above; absent either,
the estate is exactly where a cold adoption of unmarked cloud infrastructure
is - a feature with its own ladder, and explicitly outside the promise
(`HANDOFF.md`: "Cold adoption... is not part of the promise, and a number
about it is not a number about the product"). For the rest of the
population, there was never a record to lose.

**What an `aws:ResourceTag`-conditioned grant reaches.** Nothing - the same
bound tier B has, sharpened. Tier B substitutes a tagged parent as the
practical governance surface; tier C often has no config-computed formula
tying the object to a parent at all, so there is nothing to substitute at
the AWS resource-tag layer. What actually gates a tier C object is IAM on
the record store's own backend (SSM parameter policy, S3 bucket policy, or
local-file access, per `rfc/20260814-micro-state-store-ruling.md`) - a
different, and coarser, boundary than a resource tag.

### Tier D: excluded by design

**The current precedent list, corrected against the code rather than
assumed.** The population this document's issue names -
"the `aws_iam_access_key` / `aws_iot_certificate` family" - is stale as of
ruling 5 in `rfc/20260823-foundation-order-ruling.md` (2026-08-23): both
types moved off the unconditional veto and onto the `strict { secrets }`
toggle. Both resolve `ClassRecordLocated` by default today - they are
**tier C**, not tier D - and are refused only under `strict { secrets =
"refuse" }` (`internal/live/identity/located.go`'s
`strictSecretsLocatedExclusion` and `LocatedStrictSecretsRefusal`;
confirmed live in `internal/live/identity/located_test.go`, which asserts
`aws_iam_access_key.this` resolves `ClassRecordLocated` under the default
setting and is refused, by name, only under the strict one).
`aws_iot_certificate` is additionally vetoed by the orthogonal
`NotImportable` rule regardless of the secrets setting (see "Cross-cutting
notes"), which makes it unreachable in practice but for a reason that has
nothing to do with credentials.

The actual, current, enforced precedent list is exactly two types:
`aws_appstream_directory_config` and `aws_ivs_playback_key_pair`. This is
not a reading of prose - it is a live ratchet,
`internal/live/harness/assumptions.go`'s `credentialExclusionsAreTwo`
(rendered at `live/HARNESS.md`, `credential-exclusions-are-exactly-two`),
which checks both names against `tools/row-gen/rejected.json` and against
`internal/live/identity.DefaultTable` on every run and additionally fails
if any *other* rejected-ledger entry cites credential material as its
reason. Each carries its own stated reason in `tools/row-gen/rejected.json`:

- `aws_appstream_directory_config`: "credential material:
  `service_account_credentials.account_password` is a Required plaintext AD
  service-account password persisted in config and state... Ruled by the
  maintainer 2026-08-15 (#175); the one class where rejection is the end
  state."
- `aws_ivs_playback_key_pair`: "`public_key` is Required, ForceNew and
  write-only (no IVS read returns the material), so a marker-rebuilt plan
  always proposes replacement... Same ground as SURVEY.md's
  credential-material exclusion (`aws_iam_access_key`, #125), extended to
  client-supplied write-only material."

Neither type is named in `live/LIMITATIONS.md` today (checked directly:
zero matches for either name in that file). The issue text for this RFC
assumed a stated LIMITATIONS.md reason for tier D the way `markerless-type`
has one for tier C; that assumption does not hold yet. The reason lives
only in the row-gen ledger and the harness ratchet's rendered page. Giving
these two a named `live/LIMITATIONS.md` entry, the way `markerless-type`
gives `MarkerlessTypes` one, is a natural follow-up and is out of scope
here.

**Precedence.** Tier D overrides whatever tier a type's own schema would
otherwise produce - it is a ruling about a property none of the other three
tiers' definitions reach at all: whether admitting the type would force
this fork to persist plaintext credential material it can never read back
and verify again, independent of how recoverable the identity itself is.
`aws_ivs_playback_key_pair` is independently taggable
(`live/survey-full.json`: `signals.taggable = true`) and would otherwise be
tier A; `aws_appstream_directory_config` is untaggable
(`signals.taggable = false`) and would otherwise be tier B or C depending on
whether its identity is server-minted. Both are tier D instead, by the
2026-08-15 ruling (#175), ahead of any tier the identity taxonomy would
assign.

**What `live-import` does.** Nothing. The type cannot appear in a
live-markers configuration at all, refused today by the same
`RuleUnadmittedType` an ordinary not-yet-ratified type gets - see the
mechanism note below for why that matters.

**Record loss.** Moot. No record, located or backed, is ever written for a
tier D type, so there is nothing to lose.

**What an `aws:ResourceTag`-conditioned grant reaches.** Moot. No object of
this type is ever brought inside an estate's tag or record boundary, so no
`aws:ResourceTag` or `aws:RequestTag` statement scoped to an estate is ever
evaluated against it. Whatever governs it lives entirely outside this
fork's model - ordinary account and resource-ARN scoped IAM, the same as
any resource nobody has declared.

**Mechanism note for #418.** Tiers A through C are each a live, generated
roster: taggability is a schema signal, `MarkerlessTypes` is regenerated by
`tools/row-gen` on every run from `live/survey-full.json`. Tier D is not.
Confirmed directly: neither `aws_appstream_directory_config` nor
`aws_ivs_playback_key_pair` appears in `MarkerlessTypes`,
`NotImportableTypes`, or `DefaultTable` - the type simply has no row and no
derived veto naming it, which is indistinguishable, to anything that reads
only the generated artifacts, from an ordinary type no ratification batch
has reached yet. The only thing that currently tells the two apart is
`internal/live/harness`'s hand-written `sanctionedCredentialExclusions`
list, cross-checked by free-text matching against
`tools/row-gen/rejected.json`'s `reason` field
(`credentialReason`'s own doc comment: "matching free text is the weakest
part of this check"). A generator computing tier D's population has to read
that same hand list - or an exported form of it - rather than deriving it
from a schema signal, because "generates credential material this fork can
never read back" is a judgment call the maintainer made per type, not a
fact `live/survey-full.json` states.

## Cross-cutting notes

**`NotImportableTypes` is orthogonal, not a fifth tier.**
`internal/live/identity.NotImportableTypes` (67 types today) refuses
admission because the provider offers no classic `Importer` for the type at
all (issue #331) - a fact independent of taggability or identity shape.
It can further block a type that would otherwise sit in tier B or tier C:
`aws_iot_certificate` is `MarkerlessTypes`-shaped (untaggable,
server-minted - nominally tier C) but `NotImportable`'s check runs first in
`LocatedType` (condition 0) and refuses it regardless of the secrets
setting or whether a `record_store` is declared. Every tier's "what
`live-import` does" answer above already assumes the type clears this
check; a generator built against this document should apply
`identity.NotImportable` as a filter ahead of the four-tier split, not as a
fifth outcome of it.

**`ClassRecordBacked` names no tier today.** `identity.Class` has a fifth
value, `ClassRecordBacked` - "the identity IS the record: a logical
resource type whose whole existence is the persisted micro-state record,
not a cloud object" - declared as groundwork for issue #73's projection
work. Its own doc comment is explicit that no resolver in the package
produces it yet: "a record-admitted type's resources are refused by lint...
before resolution ever runs." No provider resource type occupies it as of
this RFC, so it is named here only so a future reader does not mistake its
absence from the four tiers above for an oversight. Whether it becomes a
fifth tier or folds into tier C when #73 lands is a decision for whoever
lands #73.

**Scope of "every resource type."** The tiers above classify a type by its
intrinsic identity shape - taggable or not, server-minted or not - which is
readable from the provider's schema whether or not `tools/row-gen` has
ratified a table row for it yet. That is a deliberate reading of this RFC's
own opening claim ("AWS gave roughly half the types no tag surface" is a
fact about the whole 1699-type roster, not about the smaller admitted
subset) and it is what lets tier A and tier B's populations be quoted two
ways above - the full-roster count from `live/survey-full.json`'s path
buckets, and the smaller, admission-gated count `live/MARKERS.md` already
publishes. A type nobody has ratified a row for yet - `live/LIMITATIONS.md`'s
`unadmitted-type` entry, `aws_acm_certificate_validation` is its current
example - is not unclassified under this scheme: its schema already says
which tier it is destined for (that example's own survey entry classifies
it parent-derived, tier B), independent of whether admission has caught up.
#418 has to decide how to represent that distinction - destined-for versus
currently-admitted-at - in whatever artifact it emits; this document fixes
only the four names and their meaning, not that representation.

## What this settles for #418

- The four tier names, in this exact spelling, are the vocabulary a
  generator built against this RFC must emit: `marker-carried`,
  `declaration-carried`, `record-carried`, `excluded by design`.
- Precedence is fixed: tier D overrides whatever tier A, B, or C a type's
  schema would otherwise imply; `NotImportableTypes` is a filter applied
  ahead of the split, not a fifth value.
- Tier C's population is `identity.MarkerlessTypes`, unconditionally - not
  the narrower "admitted via the located route today" subset, which is a
  fact about #270/#429's progress, not about the tier.
- Tier D's population is not derivable from a generated roster today. A
  generator must read `internal/live/harness`'s hand list (or export it)
  rather than infer it from `tools/row-gen/rejected.json`'s free text,
  because that matching is explicitly the weakest part of even the harness
  ratchet that already relies on it.
- No aggregate coverage number is asserted by this document. The counts
  quoted above are either existing, dated figures
  (`live/survey-full.json`'s path buckets, `live/MARKERS.md`'s live
  placeholders, `live/HARNESS.md`'s ratchet) or exact roster sizes read
  directly from a generated file (`MarkerlessTypes`, `NotImportableTypes`).
  None of them is a new sum computed for this document, and none is a
  coverage percentage - that number comes from #418's generator, against
  the tiers fixed here.
