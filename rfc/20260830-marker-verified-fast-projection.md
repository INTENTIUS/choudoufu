# Marker-Verified Fast Projection

Issue: https://github.com/INTENTIUS/choudoufu/issues/579

`live-plan` already runs with refresh disabled, because the projection it
plans against was built from live reads moments earlier
(`internal/command/live_plan.go`, step 9 of the command's own doc comment).
The projection build *is* the refresh, so any work on plan cost belongs
there. #579 proposes a mode that keeps the estate-wide sweep and drops the
per-instance read pass, on the argument that the sweep already settles
existence, ownership and identity in one paginated call, and that this is
strictly better than stock's `-refresh=false`, which settles nothing.

This document settles the design and reports the measurement #579 asked for.
Its headline is a partial refutation of the issue's own premise, which the
issue was written to invite, so that leads.

## Summary of findings

1. **The read pass is not the dominant term at the sizes measured.** At
   terralith scale 1 (79 resolved instances) it is 13.3% of a full
   projection's API calls. It passes the estate-wide sweep at roughly 553
   instances.
2. **The estate-wide sweep is not one paginated call.** It is one
   `GetResources` call *plus* a per-type list attempt for 992 of the 1027
   admitted types the sweep covers, because `partitionSweepTypes` routes a
   type to the tagging leg only when the hand-curated `arnJoinTable` covers
   its CFN type. #579's asymmetry table costs `sweepViaTagging` in
   isolation, not what `Request.Sweep` actually costs.
3. **This makes the mode's potential saving much larger than #579 claims,
   and relocates its risk.** A projection built from the tag index alone
   costs one call at every scale measured. What it gives up is mostly
   undeclared-resource detection, not attribute freshness.
4. **The sweep is a one-sided oracle.** A marker present proves the object
   exists. A marker absent proves nothing, because the tag index lags
   writes. So the mode cannot propose a create from an absent reading, which
   is a narrower contract than #579's asymmetry table assumes.
5. **The tier-C population figure in #579 and #535 is wrong**, and the
   population that actually bounds the mode is a third, larger one. See
   "The populations" below.

## The measurement

Measured at commit `f4611196e55952b1d990e5d3ae62d6d9ea8aa135` (`origin/main`,
"Merge pull request #577"), against
`ghcr.io/lex00/floci@sha256:c55d74e13e96c8b132056677337dba0084bb0b427cb039be2dbf9a8b7efc0948`
on darwin/arm64, with no product-code change: the only addition was
`internal/live/discovery/sweep_split_bench_test.go`, which is
`terralith_ceiling_bench_test.go`'s run with the `flocitest.CountingProxy`
read once between `Discover` and `projection.BuildFrom` so the two terms are
separately reported.

Reproduce any row with:

```
SWEEP_SPLIT_SCALE=4 TF_FLOCI_TEST=1 \
  env -u PWD go test ./internal/live/discovery/ -run TestSweepSplitAgainstFloci -v -timeout 30m
```

### The two terms

| scale | instances | sweep (Discover) | read pass (BuildFrom) | total | read pass share | `GetResources` calls |
|---|---|---|---|---|---|---|
| 1 | 79 | 560 | 86 | 646 | 13.3% | 1 |
| 4 | 301 | 593 | 341 | 934 | 36.5% | 1 |
| 10 | 745 | 659 | 851 | 1510 | 56.4% | 1 |

Both terms are linear in the instance count and the fit through the three
points is exact to one call at the middle point:

```
sweep     = 548.3 + 0.1486 * N     (593.0 predicted at N=301, 593 measured)
read pass = 1.1486 * N - 4.7       (341.0 predicted at N=301, 341 measured)
```

So the read pass overtakes the sweep at **N = 553 instances**, about scale
7.4 of this fixture.

The per-instance read cost is **1.15 calls per resolved instance**, or 1.97
calls per *materialized* instance. `live/plan-budget.json`'s figure is 22
calls per instance for `aws_s3_bucket`, which is twenty times larger. #579
flagged that extrapolating the S3 slope onto the terralith would be wrong,
and it would have been wrong by a factor of twenty.

`pagination_total` is 0 in every phase of every run, consistent with
`terralith_ceiling_bench_test.go`'s own finding and with lex00/floci#185:
the emulator returns every list in a single page at any size these fixtures
reach.

### Why the sweep costs 560 calls and not 1

`TestSweepUniversePartitionIsMostlyNative` in the same file answers this
offline, in under a second, with no emulator:

```
sweep universe=1027 tagging_leg=35 native_leg=992
```

`partitionSweepTypes` sends a type to `sweepViaTagging`'s one estate-filtered
`GetResources` call only when `arnJoinReaches` holds, which requires the type
to be in the roster *and* its CFN type to be covered by `arnJoinTable`, a
hand-curated table `bindtags.go` describes as covering 26 of 876 possible
answers. Everything else takes a per-type list attempt. Of the 992 in the
native leg, most have no list route at all and cost nothing. In the scale-1
run, 435 of the 560 discovery-phase calls were Cloud Control `ListResources`;
the remaining 125 were native provider list calls, the config-driven scan's
own reads, and the one `GetResources`.

This is worth stating plainly because it changes what the mode is: the saving
available is not "drop the read pass", it is "drop the read pass **and** the
992-type native leg, and keep the one `GetResources` answer". At scale 1 that
is 646 calls down to 1.

### What the measurement does not cover

- **The fixture is unmigrated.** `tools/terralith-gen` emits stock Terraform
  with no `tofu-estate` or `tofu-address` anywhere, so `bound=0` in every run
  and only 431 of 745 instances materialize at scale 10. A migrated estate
  materializes more, so the measured read pass is a **lower bound**. At the
  measured 1.97 calls per materialized instance, a fully-materialized
  scale-10 terralith would read about 1471 calls, putting the read pass at
  roughly 69% and moving the crossover to roughly 300 instances. That is
  arithmetic on a measured per-instance cost, not a measurement, and it is
  the single largest thing this document did not verify.
- **The sweep's page count on real AWS.** `cloudcontrol.Client.GetResources`
  paginates `PaginationToken` to exhaustion and sets no `ResourcesPerPage`,
  so the real page count is a property of the Resource Groups Tagging API
  that no floci-backed run can report (lex00/floci#185). Every number above
  reads `GetResources calls = 1` for that reason and not because the sweep is
  free. #546E / #567's real-AWS tier is where that number has to come from.
- **`TOFU_LIVE_CLOUDCONTROL=off`.** With no Tagging client the sweep falls
  back to per-type listing over the whole 1027-type universe, so the numbers
  above are the *cheapest* production shape, not the worst one.

## The populations

### The tier-C figure in #579 and #535 is wrong

Both issues say "471 types are record-carried (`identity.MarkerlessTypes`)".
Those are two different populations and they differ threefold.

At `f4611196e5`, counted by running the symbol rather than reading a document:

- `identity.MarkerlessTypes` holds **159** types
  (`internal/live/identity/markerless_generated.go`).
- `live/readiness.json` classifies **471** types as tier C. Of those, **158**
  are `markerless: true`; the other **313** are untaggable types with no
  admission row yet whose `live/survey-full.json` path is "moves to Ops" or
  "enumerable, unbindable", which `tools/readiness-gen`'s `destinedTier`
  assigns to tier C by elimination. They are *destined* tier C, not
  record-carried today.
- The 159th member of `MarkerlessTypes`, `aws_wafv2_api_key`, is tier D
  (excluded by design, credential material), so it never reaches
  `classify`'s markerless branch. That accounts for 159 against 158 exactly.

`rfc/20260828-readiness-tiers.md` already states the correct figure ("tier C
population: `identity.MarkerlessTypes`, currently 159 types"). The 471 came
from the artifact's tier count, which answers a different question.

### The population that actually bounds this mode

Neither 159 nor 471. What bounds a marker sweep is **untaggability**, and at
provider 6.59.0 that is **852 of 1699 types** (1699 total, 847 with a
taggable signal in `live/readiness.json`, of which 846 are tier A and one is
the taggable tier-D type).

Tier B is the half #579 does not mention and it is the larger operational
problem, because tier B types are admitted and in-contract today and appear
in every ordinary estate. Measured on the terralith:

| scale | instances | taggable (sweep can see) | untaggable (sweep cannot) |
|---|---|---|---|
| 1 | 79 | 38 (48.1%) | 41 |
| 4 | 301 | 137 (45.5%) | 164 |
| 10 | 745 | 335 (45.0%) | 410 |

The untaggable half is entirely tier B on this fixture:
`aws_iam_role_policy` (10 per scale unit), `aws_iam_role_policy_attachment`
(21), `aws_route53_record` (10). No instance of any `MarkerlessTypes` member
appears in it at all (`markerless_instances=0` at every scale), so the
terralith says nothing about tier C's practical weight and this document does
not claim it does.

So the honest form of #579's asymmetry table is per-instance, not per-estate:

| | live calls | existence | ownership | identity | attributes |
|---|---|---|---|---|---|
| stock `plan -refresh=false` | 0 | assumed | assumed | assumed | assumed |
| sweep only, tier A instance | 1 paginated | verified **one-sidedly** | verified | verified | assumed |
| sweep only, tier B/C instance | 1 paginated | **unknown** | **unknown** | re-derived from config | assumed |
| full projection, any instance | 1 + per-instance | verified | verified | verified | verified |

On this fixture that second row covers 45% of instances and the third covers
55%.

## The six answers

### 1. The contract

**The mode verifies four things and declines everything else, and it is
structurally incapable of expressing a claim it did not verify.**

Verified, live, at the moment of the run, for every instance whose type
carries a tag surface and whose marker the sweep returned:

- the object exists;
- it carries this estate's `tofu-estate`;
- its `tofu-address` still names the address that claims it;
- no second object carries the same address (the sweep's existing ambiguity
  rule, `markerIndex.join`'s `joinAmbiguous`, already computes this).

Declined:

- every attribute value;
- existence and ownership of any instance whose type has no tag surface;
- the existence of anything nobody's marker names, because enumerating
  unclaimed and foreign resources is exactly the 992-type native leg this
  mode drops.

The enforcement must be structural rather than advisory. A fast artifact that
merely *looks* like a plan would be worse than not having one, so the mode
must not be able to emit an `Update` or `Replace` action at all. That rules
out the obvious implementation, which is to run the stock plan walk over a
partially-populated prior state: that would render attribute diffs against
zero-valued objects and propose replacements for them. The mode's result is
instead set arithmetic between the addresses the configuration declares and
the addresses the tag index reports, rendered in its own view.

The consequence is a scope statement the rest of this document depends on:
**this is not a cheaper plan, it is a different artifact.**

### 2. What attributes diff against

**Nothing.** The mode declines attribute values entirely and reports only
existence, ownership and address integrity.

This is the option #579 itself names as honest, and the measurement supports
choosing it rather than reaching for stored prior attributes. The read pass
on this fixture is 1.15 calls per instance, not 22, so "read attributes for
some narrowed subset" is not the cheap middle ground it looks like. The
expensive term is the sweep's native leg, and no amount of stored attribute
data reduces that.

Recorded explicitly because the constraint is load-bearing: this design does
**not** require stored prior attributes, and this document does not propose
them. #109's closing ruling stands - "the live system is authoritative and
readable at any time, so a stored snapshot is a stale copy of something
always re-derivable" - and a design that needed a stored snapshot would have
to argue that ruling down first. If a future unit wants attribute answers
more cheaply, the lever is narrowing *which instances get read* (the ones the
sweep could not settle, or the ones under `-target`), never storing what a
read returned.

### 3. Tier C

**Render unknown. Do not answer from the record store, do not fall back to
reads, do not refuse the estate.**

Three reasons, in the order they were decisive:

- **The record store holds identity, not existence.** A located record says
  which live object an address means (`identity.LocatedType`,
  `internal/live/identity/located.go`). It does not say the object is still
  there. Substituting the record for a live existence check is the stale-copy
  shape #109 rejected, one level down, and it would be the exact defect this
  design is meant to avoid.
- **Refusing would refuse nearly every estate.** 852 of 1699 types are
  untaggable, and the terralith - a deliberately ordinary IAM-shaped estate -
  is 45% untaggable by instance. A refusal keyed on "the estate contains an
  untaggable type" is a refusal of the product.
- **Falling back to reads gives most of the saving away.** On this fixture it
  would reintroduce the per-instance term for 55% of instances.

So a tier B or tier C instance is listed by address under a heading that says
this run did not check it, and carries no proposed action.

One refinement is free and worth taking, because it is strictly more than
stock's `-refresh=false` knows. A tier-B instance whose identity is composed
from parents that are themselves tier A - `aws_iam_role_policy_attachment`
is `{role}` `/` `{policy_arn}` - has its identity's *referents* verified by
the same sweep: both parents exist and are still this estate's. Report that
as its own weaker line ("identity referents verified; object existence not
checked"), distinct from a bare unknown. It is the difference between "we
know nothing" and "we know the thing this address is built out of is intact".

### 4. Tag-index lag

**An address the configuration declares and the sweep does not report is
UNKNOWN, unconditionally, and can never become a proposed create.**

`bindtags.go` records that "a real account's tag index lags behind a write by
minutes", and its rule for that case is unconditional: the join finds nothing,
the run degrades, "it never guesses". This mode follows the same precedent,
and specifically does not acquire a heuristic - no created-at timestamp, no
"within the last N minutes" window. Any such window is a guess wearing a
fact's clothes, and the failure it would produce is #266's duplicate creation:
propose a create for a resource that already exists, apply it, and get a
second live object carrying the first one's marker.

The consequence is the sharpest finding in this design, and #579's asymmetry
table does not survive it intact: **the sweep is a one-sided oracle.** A
marker present proves existence. A marker absent proves nothing. So
"existence: verified" is true in the positive direction only.

That would leave the mode unable to propose a create at all, which is a
serious loss - a missing resource is the thing an operator most wants a cheap
check to catch. The recommended refinement recovers it without giving up the
rule:

**Read only what the sweep could not settle.** For each tier-A address the
configuration declares that the sweep did not report, issue one confirming
per-instance read. If the read finds the object, the tag index was lagging
and the answer is "present, marker not yet indexed". If the read finds
nothing, existence is settled negatively by a live read rather than by an
absence, and a create may be proposed.

This makes the mode's cost proportional to *disagreement* rather than to
estate size:

```
calls = sweep pages + 1 read per tier-A declared address the sweep did not report
```

On a converged, fully migrated estate that second term is zero. On a drifted
one it is exactly as large as the drift. Tier B and C addresses are not read,
per answer 3, so this does not degrade into the full read pass.

### 5. The surface

**A separate subcommand, `tofu live-verify`. Not a flag on `live-plan`, and
under no circumstances `-refresh`.**

- The artifact is not a plan. It cannot express update or replace, it does
  not enumerate unclaimed resources, and it says "unknown" about 55% of this
  fixture's instances. Anything named `plan` gets read as a plan, pasted into
  a review, and trusted for what it did not check. Answer 1 requires the
  output to be self-describing, and the strongest self-description available
  is the command's own name.
- It cannot be applied from (answer 6), and everything else in this fork
  named `plan` can be. A flag that silently removes appliability from
  `live-plan` is precisely the overload that costs a reader.
- Overloading `-refresh` is the worst available option. It is currently
  accepted-and-ignored with an explicit promise in the usage text - "`-refresh`
  is accepted and has no effect: the projection is already fresh" - and giving
  a no-op flag a meaning silently changes every existing invocation that
  passes it.
- Automatic above some estate size is rejected for the same reason
  `-refresh=false` is dangerous. The operator has to choose to accept
  blindness; a size threshold makes the tool choose for them, and the estates
  large enough to trip it are exactly the ones where the blindness matters
  most.

### 6. Whether it can be applied from

**No, and structurally: the mode produces no plan object, so there is nothing
to apply.**

`live_mode.go:303-309` already refuses saved planfiles, and this lands in the
same place from a different direction, which the refusal text should say
rather than borrowing the planfile sentence. A saved planfile is refused
because it is **stale** - it records a state snapshot that may have moved. A
`live-verify` result is not stale; it was live seconds ago. It is
**incomplete**: it never read the attributes an apply would have to
reconcile, so applying it would reconcile against nothing.

What an operator does with a finding is run `live-plan` scoped to it.
`-target` already exists and needs no change, so the pairing is "verify the
whole estate cheaply, then plan the disagreement at full cost".

## What the user sees

#579's third Accept criterion asks for the blindness statement in the form a
user encounters, not only in a document. The proposal is a header printed
before any finding, and a footer that repeats the population counts for this
specific estate, both unconditional and not suppressible by `-no-color` or
brevity flags.

```
Marker verification for estate "payments-prod" (not a plan).

Checked, live, just now:
  - the live object exists and carries this estate's marker
  - its tofu-address still names the address that claims it
  - no two objects claim the same address

NOT checked. This run read no resource attributes, so it cannot tell you
whether anything has drifted, and it proposes no updates or replacements.

  335 of 745 declared instances were verified.
  410 were not, because their resource type has nowhere to carry a tag:
      aws_iam_role_policy (100), aws_iam_role_policy_attachment (210),
      aws_route53_record (100).
      For 210 of those, both parents named by the address were verified.

  Nothing here enumerates resources this estate does not own. Run
  `tofu live-plan` for that.

Findings: 2 addresses to destroy, 1 to create, 0 unknown.
Run `tofu live-plan -target=...` to plan any of these at full cost.
```

Two properties of that text are deliberate. The counts are per-instance and
per-type rather than a percentage, because a percentage is the shape of
number this repository has repeatedly had to re-derive. And the sentence
about unclaimed resources is present even when there is nothing to say,
because its absence is the thing a reader would otherwise not notice.

## Open questions, and what would settle each

- **The read pass on a migrated estate.** Every figure here comes from an
  unmigrated fixture where `bound=0`. Settled by running
  `TestSweepSplitAgainstFloci`'s split against a terralith that has been
  through `live-import`, or applied under a `live` block so `stamp` writes
  markers. Until then the read-pass share is a lower bound and the 69%
  projection above is arithmetic, not evidence.
- **The sweep's real page count.** Settled only by real AWS (#546E / #567).
  If `GetResources` returns 100 ARNs per page, a 4000-instance estate at this
  fixture's 45% taggable rate is roughly 18 pages, and the mode's whole cost
  is 18 calls. If the page size is materially smaller the arithmetic changes
  but the shape does not.
- **Whether the native leg can shrink.** `arnJoinReaches` requires
  `arnJoinTable` to cover the type's CFN type, but `markerTFType` already
  reads the TF type straight off the object's own `tofu-address` marker, with
  no ARN join at all. Whether the 992-type native leg is therefore larger
  than it needs to be is a question for its own unit, and if the answer is
  yes it improves the ordinary `live-plan` as much as it improves this mode.
  This document did not investigate it.
- **Whether `live-verify` should also verify the record store.** Tier C
  instances are rendered unknown here. A cheap store-side check ("does the
  record still hold an identity for this address") answers a different
  question than existence and might be worth its own line. Not designed here.
