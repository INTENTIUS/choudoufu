# Receipts

This file is the spec for the receipts pattern, which is how a
marker-managed estate carries memory of an effect that has no queryable
live state of its own. A migration that ran, a cache that was invalidated,
a notification that was sent. It does this without rebuilding any part of
the store that live markers exist to remove. It is normative for anything
calling itself a receipt in this mode. The demonstration is
`stateless/e2e/estate/receipts.tf`, two resources side by side.
`aws_ssm_parameter.demo_existence` is the EXISTENCE flavor and this spec's
default recommendation, and `aws_ssm_parameter.demo_effect` is the HASH
flavor. See "Two flavors, prefer the simpler" below for what distinguishes
them, and `stateless/e2e/run.sh`'s `receipt-cycle`/`receipt-cycle-existence`
steps for both exercised live.

## The problem a receipt answers

Some effects leave nothing in the live system that names them. A database
migration changes rows, not a resource an API can list. A CDN cache purge
changes what a CDN serves, not a record OpenTofu can read back. The whole
design of live markers rests on recovering identity and state from the
live system with no memory (the admission rule, internal/stateless package
documentation), and an effect like this is exactly the case where the live
system has nothing to recover. Deciding whether such an effect needs to
run again requires *some* memory, somewhere. A receipt is where that
memory goes. It is a resource, in the same estate, whose only job is to
carry a fingerprint of the effect's declared inputs, so a plan can compare
what the config says the effect's inputs are now against what they were
the last time the effect ran, and treat a mismatch as the signal that the
effect needs to run again.

Every design decision below follows from one constraint. The receipt
itself must stay inside the same subset every other resource in the estate
lives in. It is not a special case, and it is not new machinery.

## Guard 1. A receipt is a plain declared resource

A receipt is `resource "aws_ssm_parameter" "whatever" { ... }`. Nothing
about reading, writing, or reasoning about it is different from any other
client-named resource in the estate. There is no `choudoufu receipt`
subcommand, no `list`, no `repair`, no `verify`, and none will be added at
any phase, even as a convenience wrapper around the same plan/apply cycle
every other resource already goes through.

This is not a style preference. The moment a receipt gets its own tooling,
state operations have been rebuilt under a new name. A `choudoufu receipt
list` command is a `choudoufu state list` command that only shows one
resource type. A `choudoufu receipt repair` command is `choudoufu state rm`
plus `choudoufu import` wearing a disguise. The entire point of removing
the state file is that there is no special class of record requiring
special commands to inspect or fix. Every record is a resource, and every
resource is inspected and fixed the same way. Change the config, run a
plan, look at the diff. A receipt that needed its own verb would be
admitting that plan/apply is not enough for it, which would mean it is not
actually a plain resource. It would be state with extra steps. So it stays
a plain resource, permanently, by construction. Any future need that seems
to call for receipt-specific tooling is a sign the need belongs somewhere
else (the harness, a CI script, an Op), never a reason to add a verb here.

## Guard 2. Hash-only values, and never SecureString

A receipt's value is a hash of the effect's declared inputs, in the demo
`sha256(jsonencode(local.demo_effect_inputs))`. It is never the inputs
themselves, and never anything derived from the effect's *output*. There
are two reasons, one mechanical and one about the resource's type.

Why a hash and not the raw inputs. The receipt's whole job is equality
comparison, whether this plan's inputs match the last recorded run's
inputs, and a hash answers that with a fixed-size, content-opaque value
regardless of how large or structured the real inputs are. It also keeps
the receipt from becoming a second copy of configuration data that now has
to be kept in sync with the first, which is exactly the kind of derivative
record this mode exists to avoid growing.

Why the type is always `String` and never `SecureString`. A hash is not a
secret. It reveals nothing about the inputs that produced it beyond the
fact that some inputs produced this fixed-size value, and it cannot be
inverted back to them. Marking it `SecureString` would buy no
confidentiality and would cost real complexity. `SecureString` parameters
are encrypted through KMS, which drags key custody, key policy, and
decrypt permissions into a mechanism whose entire purpose was to be a
plain, cheap, readable comparison point. Convention enforces this today. A
lint heuristic is the natural next step (flagging a `String`-typed receipt
whose value expression is not visibly a hash function, or an
`aws_ssm_parameter` matching the receipt naming convention below declared
with `type = "SecureString"`), but is not implemented as part of this
task.

## Guard 3. The executor never runs the effect

`choudoufu plan` and `choudoufu apply` touch the receipt resource and
nothing else. Reading the receipt's current value, computing the new hash,
and showing the diff between them is entirely within the plan engine's
existing job, comparing declared config against live state. Actually
*running* the effect the receipt stands for, the migration or the purge or
the notification, never happens inside OpenTofu's process, in either
command, under any flag. It happens in the layer above. A CI step, an Ops
runbook, an agent-driven harness, anything that can (a) see the plan's
proposed receipt change, (b) run the real effect, and (c) let `apply`
write the new receipt value only if the effect succeeded.

This split is what makes the receipt's plan diff meaningful rather than
decorative. Because OpenTofu itself never runs the effect, a proposed
change to `aws_ssm_parameter.demo_effect`'s value in a plan is not noise.
It is the entire reviewable signal that this apply is going to trigger
something with real-world consequences outside the resources OpenTofu
manages directly. An operator reading a plan and seeing the receipt change
is seeing exactly and only that. The effect's declared inputs changed
since the value currently on record, so the layer above is about to run
it. Anything that tried to make OpenTofu run the effect itself would
collapse that signal. The diff would still show a value change, but it
would no longer be a preview of what is about to happen outside the plan.
It would be the thing happening, mid-plan, which is a provisioner by
another name (and provisioners are already banned, see
stateless/LIMITATIONS.md).

The plan/apply/failure semantics, precisely.

1. **Plan** shows the receipt's value changing from the old hash to the
   new one whenever the effect's declared inputs have changed. This is the
   "effect will fire" signal, and it is the only place that signal lives.
   There is no separate `--show-pending-effects` output. The ordinary
   resource diff already carries it.
2. **The effect runs first**, driven by the layer above, outside apply.
3. **The receipt is written on success only**, as the very last step, once
   the layer above has confirmed the effect completed. `apply` then
   persists the new hash, an ordinary resource write, no different from
   updating a tag.
4. **At-least-once, not exactly-once.** If the effect runs but the process
   crashes, or the network drops, before the receipt gets written, the
   next plan sees no change in the receipt (the old hash is still on
   record) and proposes the same "effect will fire" diff again, so the
   effect reruns. This is a deliberate acceptance of at-least-once
   semantics rather than exactly-once. The receipt cannot make an effect
   idempotent by itself (only the effect's own implementation can do
   that), but it guarantees that under-running never happens silently.
   Every unconfirmed effect stays visible as a pending diff until a
   receipt write proves it succeeded.

## Guard 4. The leaf rule

Nothing may reference a receipt's attributes. No resource argument, no
`depends_on` entry, no output. Nothing anywhere in the configuration reads
`aws_ssm_parameter.demo_effect.value`, `.arn`, `.id`, or any other
attribute of a receipt, ever. A receipt is a leaf in the dependency graph.
Things can point into it (its own value expression reads other resources'
attributes, same as the demo's `local.demo_effect_inputs` does), but
nothing points out of it.

The reasoning is the same authority-creep argument that governs every
other banned construct in this fork. The moment some other resource's plan
depends on a receipt's value, that receipt has stopped being a record of
what already happened and has started being an input other things need to
be correct, which is precisely the definition of authoritative state. "A
stored claim about what exists that the tool believes over the world
itself... if the record being wrong makes the tool do wrong things to the
world, it is authoritative." A receipt that nothing depends on can be
wrong, stale, or simply gone, and the only consequence is one idempotent
re-run of the effect it stands for. Costly in time, possibly, but never in
correctness. A receipt that something else depends on turns that same loss
into a wrong plan for whatever depended on it. Leaf status is what keeps
losing the receipt a cheap, recoverable event instead of the first crack
through which a record grows back into state. This is also why the demo's
local, `demo_effect_inputs`, only ever reads *other* resources' attributes
into the receipt, never the other way around. The data flow is
one-directional by construction, not merely by convention.

Leaf-ness is lint-enforceable, because it is a static graph property. The
question is whether any traversal in the configuration reaches into a
resource that is statically recognizable as a receipt. See "Lint
enforcement" below for what is implemented and its boundary.

## Naming convention

A receipt's `name` argument (or equivalent client-assigned identity
argument, for whichever resource type carries it) follows this shape.

```
/tofu-receipts/<estate>/<effect>
```

- `<estate>` is the owning estate's name, exactly as `tofu-estate` markers
  spell it (see `stateless/MARKERS.md`).
- `<effect>` is a short, stable name for the effect the receipt stands
  for. It is `demo-effect` in the demonstration, and something like
  `db-migration` or `cdn-purge` in a real estate.

This convention is what makes a receipt statically recognizable. An
`aws_ssm_parameter` whose `name` argument is a literal string matching
this prefix is a receipt, and the lint rule below treats it as one without
needing any other marker or annotation.

## Prior art

The pattern is not new. It is a narrow, config-native restatement of ideas
already load-bearing elsewhere.

- Schema-migration tables (Rails' `schema_migrations`, Flyway's
  `flyway_schema_history`, and similar) record which migrations have run,
  read back before the next migration run to decide what is still
  pending. A receipt is the same idea with the table replaced by an
  ordinary declared resource, and the record of which migrations ran
  replaced by a hash of this migration's declared inputs.
- Kubernetes' `kubectl.kubernetes.io/last-applied-configuration`
  annotation is a record, carried on the object itself, of what was last
  declared for it, read back on the next apply to compute a three-way
  diff. A receipt narrows this further, carrying only a fingerprint of the
  prior configuration rather than the whole thing, but the shape (a record
  of what was declared last time, attached to the system rather than to a
  separate store) is the same move.

## Lint enforcement

Implemented as a rule in `internal/stateless/lint` (`RuleReceiptLeaf`).
Any managed resource argument or output expression that contains a direct
traversal into an `aws_ssm_parameter` resource whose `name` argument is a
literal string starting with `/tofu-receipts/` is rejected, citing this
file's leaf rule.

The boundary, stated precisely so it is not mistaken for full data-flow
analysis. The rule catches *direct* traversals only, meaning an expression
that names the receipt resource's address right there, such as
`aws_ssm_parameter.demo_effect.value` used as (part of) another resource's
argument, a `depends_on` entry naming the receipt, or an output's
expression. It does not follow a value through an intermediate `local`.
`locals { x = aws_ssm_parameter.demo_effect.value }` followed elsewhere by
a reference to `local.x` is not caught, because that requires tracing
value flow through locals rather than reading traversals off one
expression. This is the conservative direction to fail in. The rule never
flags a reference that is not really there, at the cost of not catching
every possible indirection. Closing that gap (tracing through locals, and
in principle through module boundaries) is future work, not part of this
task.

## Two flavors, prefer the simpler

A receipt answers one of two questions, and the simpler question needs no
hash at all.

**Existence receipt (the default).** For run-once effects, migrations and
one-time kicks, the parameter's existence is the entire bit. The value is
a constant (e.g. "done"). The plan signal is the cleanest possible, since
"will be created" means "will fire". Zero secret risk, zero value
semantics. Reach for this first.

**Hash receipt (run-on-change only).** When the requirement is to re-run
whenever these inputs change, the value is sha256 over the declared
inputs, as specified above. Use it only when that requirement is real.

Both ship as fixtures, side by side, in `stateless/e2e/estate/receipts.tf`.
`aws_ssm_parameter.demo_existence` is the existence flavor and
`aws_ssm_parameter.demo_effect` the hash flavor. The recommendation above
is not only prose. The existence flavor is the one this spec tells you to
reach for first, and it is also the one with nothing to scrub and nothing
to go stale. Its value carries no information by design, so there is no
hash to keep correct and no secret-shaped mistake to make in computing
one. `stateless/e2e/run.sh`'s `receipt-cycle-existence` step exercises the
flavor live. The receipt starts owned and clean. It is broken THE
EXISTENCE WAY, with an out-of-band `aws ssm delete-parameter` rather than
a value overwrite, because there is no changed value to write. The next
plan re-arms exactly one create on it ("will be created" meaning "will
fire"). Recreating it (the layer above playing the effect) and re-adopting
its markers converges the next plan back to clean. This is symmetric with
`receipt-cycle`, which exercises the same shape for the hash flavor via an
out-of-band value overwrite instead of a delete.

## Secrets discipline

Effect inputs MUST reference secrets by pointer, meaning the secret's ARN
and version identifier, never by value. A hash over low-entropy secret
material (a password) is offline-guessable by anyone holding the receipt
and knowing the input shape. A hash over the secret's version-id leaks
nothing and answers the actual question ("did it rotate?") from metadata.
A future lint heuristic can flag sensitive-marked values flowing into a
receipt's value expression. Until then this is a review rule.

## Why SSM, and the Dynamo comparison

Standard-tier SSM parameters cost nothing at estate scale. No storage
charge, no throughput charge, path-prefix IAM scoping, and native
versioning that retains old hashes as a free per-effect audit trail.
DynamoDB adds a table and per-request billing. S3 needs a bucket to exist
first. A tag on an anchor resource is genuinely free and fine when an
effect has a natural anchor, but external effects, the unobservable case
receipts exist for, have none.

The honest comparison someone will make is that this is a Dynamo lock
table without the locking. Nearly exact, and the difference is the design.
The lock table is tool infrastructure, owned by no estate, load-bearing
for every operation, coordinating writers through conditional writes.
Receipts are declared resources inside the estate, planned and destroyed
by the normal lifecycle, gating one effect each, with no coordination
protocol and no reader beyond declared-vs-live diffing. The lock existed
to protect an authoritative record. With no record to protect, what
remains is a memo. Their KV store guards the workflow. This one remembers
the effects.

## Cross-cloud boundary

The receipt PATTERN is provider-agnostic by construction. Guard 3 means
the executor has no concept of a receipt anywhere in its code, so any
provider's plain key-value resource qualifies structurally. Client-named
(admission path 1), holds a string, stays a leaf. Nothing AWS-shaped
exists in the machinery.

The DEMO and the ENFORCEMENT are AWS-only today. The estate's receipt is
an `aws_ssm_parameter`, and the leaf rule's recognition is hardcoded to
that type with a literal `/tofu-receipts/` name. On another cloud you can
follow this spec and the leaf rule will not protect you, silently. Do not
mistake AWS-only enforcement for cross-cloud enforcement.

Generalizing is a table, not a redesign. One recognition row per provider
(resource type, name attribute, path convention) in the lint rule, plus a
recommended-type entry here. Candidates when that work lands are
`azurerm_app_configuration_key` on Azure (plain key-value, client-named),
and on GCP, which has no plain parameter store,
`google_compute_project_metadata_item` or a GCS object, chosen
deliberately, because Secret Manager is the WRONG home. Hashes are not
secrets, and parking receipts in a secret store inverts guard 2's
reasoning.
