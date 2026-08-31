# Plan Approval: Tying a Reviewed Plan to the Apply That Follows

Issue: https://github.com/INTENTIUS/choudoufu/issues/74

Teams run Terraform in CI as "plan in the PR, a human approves, apply exactly
that". Stock OpenTofu implements this with `plan -out <planfile>` followed by
`apply <planfile>`. Live mode refuses both halves, and today nothing detects
that the live system moved between review and apply. The public docs name the
gap honestly: `site/content/day2.md` ("Plan, review, apply", lines 172-180)
says ordinary `apply` re-plans and re-confirms, "which is the honest
behaviour, but nothing today detects that the world moved between review and
apply", and `site/content/compatibility.md` (lines 210-213) flags `-out` as
"how most CI runs Terraform, so check it first". This ruling designs the
replacement.

The issue proposes the design direction: not a saved plan but a plan
fingerprint, printed at plan time, checked by apply against its own fresh
plan, with divergence a named refusal. This document works that proposal out
against the code, weighs it against two alternatives, and lists what still
needs a maintainer ruling.

## The problem, restated against the no-state-ops invariant

A live-markers run has no state file. Identity lives in cloud tags, the plan
is rebuilt from the live system every run, and the prior state is a
projection built in memory and discarded (`internal/command/live_mode.go`,
`statelessRunner.PriorState`). The refusal of saved plans follows directly;
the doc comment on `statelessRejections` (`internal/command/live_mode.go`,
lines 211-225) states it as the roadmap's own test: a saved plan file records
the state snapshot the plan was made against so that apply can check the
state has not moved, that record is authoritative, and "stateless mode exists
to have no such record". So `plan -out` produces nothing to apply, and
`apply <planfile>` is refused before the file is even opened
(`statelessRejectPlanFile`, same file, lines 268-280), because acting on a
record of how things were would skip discovery, marker stamping, and the
live read.

The consequence is that live mode's apply always re-plans. That is correct
and stays correct under every design below: the world remains authoritative,
and apply always reads it. What is missing is the connection backward - a way
for the apply to know whether what it is about to do is what the human
approved, and to stop loudly when it is not.

Any design here must add zero state operations. A stored artifact that the
apply treats as authoritative about the world would be a state file under
another name; the artifact can only ever be authoritative about what was
*approved*, with the world's current shape read fresh and compared against
it.

## What upstream's planfile provides, and what it actually checks

It is worth being precise about what the stock mechanism guarantees, because
it is less than commonly assumed. A stock planfile carries the prior state
snapshot, the configuration snapshot, and the planned changes. At apply,
`internal/backend/local/backend_local.go` (lines 404-432) compares the
planfile's stored state lineage and serial against the backend's current
state metadata, and errors with "Saved plan does not match the given state"
or "Saved plan is stale" on mismatch. That check detects an intervening
*state operation* - another apply, a state edit - and nothing else. A
resource changed out-of-band in the cloud does not bump the serial; stock
`apply <planfile>` executes the stored diff against a world it has not
re-read.

Live mode cannot fake any of this (no lineage, no serial, no stored prior
state) and must not try. But the comparison cuts the other way too: a
live-mode apply re-reads the actual cloud at apply time, so a
fingerprint check against apply's fresh plan compares the approval against a
newer reading of the world than upstream's serial check ever consults. The
artifact designed here can honestly promise something the stock planfile
does not: if anything moved - by another apply *or* out-of-band - in a way
that changes what this apply would do, the apply notices.

What the artifact cannot promise, under any of the designs below, is
atomicity between apply's own fresh plan and its execution. That residual
window is exactly the one stock `apply` (without a planfile) has between
rendering the plan and the operator typing "yes"
(`internal/backend/local/backend_apply.go`, plan at line 143, confirmation
at lines 181-231). No design here widens it and none can close it.

## The primitives already in place

The live path already produces, per run, everything a fingerprint would be
computed from:

- The change set: `plans.Plan.Changes.Resources`
  (`internal/plans/plan.go:57`, `internal/plans/changes.go:21`), each entry
  carrying the instance address, the action kind, and the before/after
  values as `plans.DynamicValue` (`internal/plans/changes_src.go`, lines
  22-57 and 251-257).
- The identity bindings: which live object each declared address resolved
  to, settled by discovery and the projection before the plan runs
  (`internal/command/live_mode.go`, `PriorState`).
- The policy outcomes of #67's matrix (`internal/live/policy/doc.go`): the
  reconcile roster's deletes enter the plan as ordinary destroys (merged as
  orphan resolutions in `PriorState`, lines 477-493), so they are already
  part of the change set. The one policy action that is *not* in the change
  set is the `undeclared_tagged = "untag"` verb's tag release: its targets
  are captured at plan time (`statelessRunner.untagTargets`, lines 580-589)
  and executed after the apply in `AfterApply` (line 621), outside
  `plans.Changes` entirely.
- Receipts (`live/RECEIPTS.md`) are plain declared resources whose plan diff
  is, by that spec's own words, "the entire reviewable signal". They flow
  through the change set and need nothing special.
- A precedent for hash-only artifacts: the observational snapshot's
  `AttributesHash` (`internal/live/projection/snapshot.go`, lines 150-156)
  is a SHA-256 over the attributes JSON, present so a diff can say "changed"
  without the artifact ever holding the values. Snapshots themselves are
  being retired (#109; see `rulings/20260814-projection-nativeness-audit.md`),
  but the redaction-by-construction principle is the one to reuse: a
  fingerprint artifact holds hashes, never values, so it can be pasted into
  a PR comment or a CI job output without a secrecy review.

One rule constrains design (c) below and is worth quoting: a plan must never
write to the live system. It is stated as the reason the untag work is
captured rather than acted on (`internal/command/live_mode.go`, lines
584-585), and it applies with equal force to any "plan records its approval
somewhere" scheme.

## Candidate designs

### (a) A plan fingerprint, checked at apply

Plan prints one line: a versioned digest, for example
`choudoufu-plan:v1:sha256:<hex>`, computed over a canonical encoding of

- every resource instance change, sorted by address: the address, the action
  kind, and SHA-256 hashes of the canonicalized before and after values;
- the identity binding each changed address resolved to (the structured
  identity or import ID the projection used);
- the untag roster: the tag key and the sorted target list the apply will
  release after the changes land.

Apply accepts an optional `-expect-plan=<digest>`. After computing its own
fresh plan it computes the same fingerprint; on mismatch it refuses with a
named diagnostic, having already rendered the fresh plan above the refusal,
so the operator sees what the world looks like now even though the artifact
cannot say what the reviewer saw then. On match, the run proceeds through
the ordinary confirmation (or `-auto-approve`; CI passes both flags).

The coverage rule that makes this list principled rather than ad hoc: the
fingerprint covers everything the apply will do to the live system, and
nothing else. The informational sections the live plan prints around the
change set - omissions, unowned, foreign, the policy report
(`internal/command/views/live_plan.go`, the `StatelessPlan` interface, lines
401-449) - describe the world rather than the apply's intent. Any world
change that alters what the apply would execute already alters the change
set, the bindings, or the untag roster; a change that alters only those
reports (a new foreign resource, say) does not change what this apply does.
The untag roster is the one section that is executed but not in
`plans.Changes`, which is exactly why it is the one section pulled into the
digest. This answers the issue's open question about the drift-report
sections and the #67 plan sections, subject to ratification below.

Before-value hashes are included deliberately: an update whose after-value
is unchanged but whose before-value differs means the world moved under the
resource, and the reviewer approved a transition, not just a destination.

Failure modes:

- TOCTOU. The review-to-apply window is closed: any drift that changes the
  proposed actions changes the fingerprint and the apply refuses. The
  plan-to-execution window inside the apply itself remains, identical to
  stock interactive apply, and the diagnostic text should not claim
  otherwise.
- Partial-apply restart. An apply that fails midway has changed the world;
  a re-run's fresh plan differs, the fingerprint mismatches, and the apply
  refuses. That is the correct behavior - the remainder was reviewed as part
  of a whole that no longer exists - but the diagnostic must name the
  possibility ("an earlier apply may have partially completed; re-plan and
  re-review") rather than leaving the operator to guess. There is no resume;
  re-review is the path.
- Multi-runner CI. The artifact is one line, passed through job outputs or a
  PR comment; nothing is written to disk or cloud. It requires the applying
  runner to check out the same configuration revision and resolve the same
  provider versions (the lockfile's job); a divergence there produces a
  differing fresh plan and an honest refusal rather than a subtle wrong
  apply.
- Diagnostic poverty. A bare digest cannot name which address diverged,
  because the approved content is not carried, only its hash. The refusal
  can say "the fingerprint differs" and point at the freshly rendered plan;
  it cannot print a reviewed-versus-now diff. Design (b) is the answer if
  that proves insufficient in practice.
- Encoding stability. Hashing the raw msgpack of `plans.DynamicValue` is
  not obviously stable across cosmetic re-orderings; the values should be
  decoded against the schema's implied type and re-encoded canonically
  (ctyjson's deterministic object-attribute ordering, or an equivalent
  purpose-built encoder; `internal/command/jsonplan` is prior art for
  schema-aware plan serialization). This is the issue's first open question
  and it needs a dedicated stability test, not an assumption.

Refuses versus warns: mismatch is always a refusal. A mode where divergence
merely warns and proceeds would be the artifact lying about what it
gates, and v1 should not have one.

### (b) An approved-summary document, diffed at apply

Same fingerprint inputs, but plan additionally emits (behind a flag, e.g.
`-approval-summary=<path>`) a small structured document: per-address rows of
action kind, before-hash, after-hash, identity binding, plus the untag
roster and a format version. Apply accepts the document instead of (or in
addition to) the bare digest, recomputes its own rows, and on mismatch names
exactly which addresses differ and how ("aws_x.y: approved an update, the
fresh plan proposes a replace").

This is (a) with a richer artifact, and shares its TOCTOU, partial-apply,
and multi-runner properties. What it adds is diagnostic quality; what it
costs:

- A file that looks like a saved plan. Users coming from `-out` will treat
  it as one, and the naming, the docs, and the refusal texts would all have
  to keep saying that the document carries no authority about the world and
  cannot be "applied", only compared. The hash-only rows keep it free of
  attribute values (the snapshot precedent), so secrecy is not the problem;
  the mental model is.
- An emit format, a parser, a diff renderer, and a compatibility story for
  the format across versions - a materially larger v1 than one hash line.

### (c) Approval as an estate record in the record_store

Persist the approval - digest, approver, timestamp - as a record in the
estate's `record_store` (`internal/live/staterecord`, the `Store` interface
in `store.go`; ruling in `rulings/20260814-micro-state-store-ruling.md`), keyed
by the digest. Apply computes its fresh fingerprint and looks the record up;
found means approved.

This is not an alternative to fingerprinting - the digest must exist for the
record to be keyed by it - but a different transport: instead of a line
piped from review to apply, a cloud lookup. It buys an audit trail and
frees multi-runner CI from carrying the artifact. It costs:

- A write at review time, and a plan must never write to the live system
  (`internal/command/live_mode.go`, lines 584-585). The write would have to
  come from a separate step (a `choudoufu approve` command, or CI calling
  the store directly), which is new command surface.
- Coupling to an optional block. `record_store` is per-estate configuration;
  estates without one - the default - could not use plan approval at all, or
  would need (a) as a fallback anyway.
- Lifecycle. Approval records accumulate and need expiry or cleanup, and
  versioned records with a garbage-collection story is state operations
  creeping back in under a friendlier name. The store's SSM backend also has
  the documented CAS asymmetry (`internal/live/staterecord/ssm.go`), though
  approvals keyed by digest would use `PutIfAbsent`, which is atomic on all
  three backends.

## Recommendation

Design (a), with the coverage rule as stated: the fingerprint is computed
over the sorted change set (address, action kind, canonical before/after
hashes), the identity bindings of the changed addresses, and the untag
roster; the informational plan sections stay out. One line out of plan, one
optional `-expect-plan=<digest>` into apply, mismatch is a refusal with a
named diagnostic that renders the fresh plan and names the partial-apply
possibility. Design (b) is the designated follow-up if field use shows the
bare-digest refusal is too blunt to act on; its artifact should then be an
extension of (a)'s digest (the digest being the hash of the document's
canonical form), so the two never disagree. Design (c) is declined for v1:
it needs (a) anyway, couples approval to an optional block, and its
record-lifecycle needs sit badly with the no-state-ops charter.

### The smallest honest v1

- Plan (plain `plan` under a live block, and `choudoufu live-plan`) prints
  the fingerprint line after the plan, on stdout, greppable - which matters
  because `-json` is refused under live markers
  (`internal/command/live_mode.go`, lines 233-236) and the line is the only
  machine-readable hook a CI job has.
- Apply gains `-expect-plan=<digest>`; the check runs after apply's fresh
  plan is computed and rendered, before confirmation
  (`internal/backend/local/backend_apply.go`, between lines 160 and 181).
  A malformed or wrongly-versioned digest is a refusal too, before any plan
  is computed.
- The flag is only meaningful under a live block; given without one it is
  refused the way the other live-only surfaces are.
- Two named diagnostics: "Plan fingerprint does not match" (with the
  partial-apply sentence) and "Malformed plan fingerprint".
- A fingerprint-stability test: the same configuration and world must
  produce the same digest across runs, and a fixture that permutes
  attribute order in the configuration must not change it.

Nothing is stored, nothing is written to the cloud, and an estate that never
uses the flag sees one extra line of plan output.

## What needs a maintainer ruling

1. The coverage rule. Ratify "everything the apply will execute, nothing
   informational": untag roster in, omissions/unowned/foreign/policy report
   sections out. This settles two of the issue's three open questions (the
   drift-report sections and the #67 plan-section interaction).
2. Canonical encoding. Schema-aware re-encoding via ctyjson versus a
   purpose-built canonical encoder for the hashed values; either way the
   stability test is the acceptance gate. This is the issue's
   attribute-ordering question and it should be settled by the test, not by
   argument.
3. Confirmation interplay. Recommended: `-expect-plan` and `-auto-approve`
   stay orthogonal (a match does not skip the prompt; CI passes both). The
   alternative - a match standing in for confirmation - reads nicely but
   makes the digest a credential, which is a bigger decision than v1 needs.
4. Refusal-only. Confirm there is no warn-and-proceed mode in v1.
5. Whether the fingerprint line prints unconditionally on every live plan
   and apply, or only when asked. Recommended: unconditionally, since the
   line is one row and conditional output is what scripts trip over.
6. The digest format string and its versioning policy (a coverage change
   must change the version and fail closed against old digests).
7. Whether design (b) is committed as a follow-up issue now or left until
   field evidence asks for it.

## What this artifact does not promise

Stated here so the docs and diagnostics can keep saying it: the fingerprint
does not freeze the world between apply's fresh plan and its execution; it
does not carry the reviewed content, only its hash; it does not verify who
approved, only that what was in front of the approver is what the apply
would now do; and it does not make `apply <planfile>` work, because there is
still, deliberately, no planfile.
