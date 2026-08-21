# Receipts

This file is the spec for the receipts pattern, which is how a
marker-managed estate carries memory of an effect that has no queryable
live state of its own. A migration that ran, a cache that was invalidated,
a notification that was sent. It does this without rebuilding any part of
the store that live markers exist to remove. It is normative for anything
calling itself a receipt in this mode. The demonstration is
`live/e2e/estate/receipts.tf`, two resources side by side.
`aws_ssm_parameter.demo_existence` is the existence flavor and this spec's
default recommendation, and `aws_ssm_parameter.demo_effect` is the hash
flavor. See "Two flavors, prefer the simpler" below for what distinguishes
them, and the `receipt-cycle`/`receipt-cycle-existence` steps in
`live/e2e/run.sh` for both running live.

## Boundary: receipts never migrate onto the record store

GitHub issue #73 gives `null_resource`, `terraform_data`, `time_*`, and
non-sensitive `random_*` a persisted micro-state record once a `live`
block's `record_store` is configured (`internal/live/staterecord`). A
receipt is not a candidate for that move, and this is the line: receipts
stay ordinary declared estate resources, forever, under the no-state-ops
rules above, never carried by a `record_store`.

Two reasons, both about what stays visible.

First, a receipt's plan diff is the entire reviewable signal that guard 3
depends on. `aws_ssm_parameter.demo_effect`'s value changing in a plan is
what tells an operator, a reviewer, or a CI gate that this apply is about to
trigger something with real-world consequences outside the resources
OpenTofu manages directly. That diff exists because a receipt is a plain
declared resource going through the ordinary plan/apply cycle described in
Guard 1 above. A record-backed resource's prior state comes from
`internal/live/staterecord.Store.Get` instead of a cloud read, but its
*plan diff* is exactly as visible either way. What moves is where the
resource's *value* is read from, not whether a plan shows a change to it.
Receipts stay off the record store because a receipt is deliberately
AWS-native
(`aws_ssm_parameter`, `/tofu-receipts/<estate>/<effect>`) so its value stays
readable with a plain `aws ssm get-parameter` by anyone with read-only IAM
access and no `choudoufu` binary at all, whether a person, a script, or an
incident responder at 3am. A `staterecord` payload (internal/live/projection's
`recordPayload`, a self-describing ctyjson envelope) is tool-internal by
design: readable by this fork's own code, not meant as an operator-facing
artifact the way an SSM parameter's plain string value is. Moving a
receipt's value onto that payload format would trade a `aws ssm
get-parameter` away for "read `choudoufu`'s internal JSON envelope",
which is strictly worse visibility for the exact artifact whose whole job
is being visible.

Second, and more concretely: **using `terraform_data`'s `triggers_replace`
as a pseudo-receipt is exactly the anti-pattern this boundary forbids.** It
is tempting, because `terraform_data` is record-backed now, so a
`triggers_replace` fingerprint sitting on it looks like it might do a
receipt's job. But it hides the fingerprint inside the tool's own record
store rather than in an ordinary declared resource, which loses the same
plain-AWS-CLI visibility the paragraph above describes, and it collapses
Guard 4's leaf rule and Guard 3's plan/apply/failure semantics into
`terraform_data`'s much narrower "did an input change" question, with no
existence flavor, no hash flavor, and no `/tofu-receipts/<estate>/<effect>`
naming convention for the lint rules to recognize it by. `terraform_data`
is for graph-internal plumbing, meaning ordering an apply's create/update/delete
sequence, feeding `replace_triggered_by`, standing in for a resource that
does nothing itself, and never for recording an external effect. Receipts
are for external effects. `terraform_data` is for the graph. Keep them
apart.

## The problem a receipt answers

Some effects leave nothing in the live system that names them. A database
migration changes rows, not a resource an API can list. A CDN cache purge
changes what a CDN serves, not a record OpenTofu can read back. The whole
design of live markers rests on recovering identity and state from the
live system with no memory (the admission rule, internal/live package
documentation), and an effect like this is exactly the case where the live
system has nothing to recover. Deciding whether such an effect needs to
run again requires *some* memory, somewhere. A receipt is where that
memory goes. It is a resource, in the same estate, whose only job is to
carry a fingerprint of the effect's declared inputs, so a plan can compare
what the config says the effect's inputs are now against what they were
the last time the effect ran, and treat a mismatch as the signal that the
effect needs to run again.

Every design decision below follows from one constraint: the receipt must
stay inside the same subset every other resource in the estate lives in,
with no special cases and no new machinery.

## Guard 1. A receipt is a plain declared resource

A receipt is `resource "aws_ssm_parameter" "whatever" { ... }`. Nothing
about reading, writing, or reasoning about it is different from any other
client-named resource in the estate. There is no `choudoufu receipt`
subcommand, no `list`, no `repair`, no `verify`, and none will be added,
even as a convenience wrapper around the same plan/apply cycle every
other resource already goes through.

Receipt-specific tooling would rebuild state operations under a new name:
a `choudoufu receipt list` command is a `choudoufu state list` command that
only shows one resource type, and a `choudoufu receipt repair` command is
`choudoufu state rm` plus `choudoufu import`. The point of removing the
state file is that no class of record requires special commands to inspect
or fix. Every record is a resource, and every resource is inspected and
fixed the same way: change the config, run a plan, look at the diff. A
receipt that needed a verb of its own would not actually be a plain
resource. Any future need that seems to call for receipt-specific tooling
belongs somewhere else (the harness, a CI script, an Op), and is never a
reason to add a verb here.

## Guard 2. Hash-only values, and never SecureString

A receipt's value is a hash of the effect's declared inputs, in the demo
`sha256(jsonencode(local.demo_effect_inputs))`. It is never the inputs
themselves, and never anything derived from the effect's *output*. There
are two reasons, one mechanical and one about the resource's type.

Why a hash and not the raw inputs. The receipt's job is equality
comparison, whether this plan's inputs match the last recorded run's
inputs, and a hash answers that with a fixed-size, content-opaque value
regardless of how large or structured the real inputs are. It also keeps
the receipt from becoming a second copy of configuration data that has to
be kept in sync with the first, which is the kind of derivative record
this mode exists to avoid.

Why the type is always `String` and never `SecureString`. A hash is not a
secret. It reveals nothing about the inputs that produced it beyond the
fact that some inputs produced this fixed-size value, and it cannot be
inverted back to them. Marking it `SecureString` would buy no
confidentiality and would cost real complexity. `SecureString` parameters
are encrypted through KMS, which drags key custody, key policy, and
decrypt permissions into a mechanism meant to be a plain, readable
comparison point.

Lint enforces this guard. `RuleReceiptValue` in `internal/live/lint`
rejects a statically recognizable receipt declared with a literal
`type = "SecureString"`, and flags a receipt whose value expression is not
visibly one of the two documented flavors, meaning a hash function as the
outermost call (the hash flavor) or a constant literal such as `"done"`
(the existence flavor). The check reads the expression on the page and
never traces value flow, so a hash computed in a `local` and referenced
from the receipt is flagged until the hash call is inlined where the
receipt declares it, and a `type` argument built from a variable rather
than a literal is not judged at all. See "Lint enforcement" below for the
recognition boundary this shares with the leaf rule.

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

This split is what makes the receipt's plan diff meaningful. Because
OpenTofu itself never runs the effect, a proposed change to
`aws_ssm_parameter.demo_effect`'s value in a plan is the reviewable signal
that this apply will trigger something with real-world consequences
outside the resources OpenTofu manages directly. An operator who sees the
receipt change in a plan knows the effect's declared inputs changed since
the last recorded run, so the layer above is about to run it. If OpenTofu
ran the effect itself, the diff would stop being a preview of what is
about to happen outside the plan and become the thing happening mid-plan,
which is a provisioner.

That used to end the argument, because provisioners were banned outright.
They are not any more: GitHub issue #353 admits `local-exec`, `remote-exec`
and `file` for any estate that declares a `record_store`, since a
create-time provisioner's one piece of memory (the tainted flag a failed one
sets) then has somewhere to live. So the argument has to be made honestly
rather than by pointing at a ban, and it still holds, because the two
mechanisms answer different questions:

- A provisioner runs **once, when its resource is created**, and never
  again. There is no plan-time signal that it is about to run, nothing
  re-examines it on a later plan, and it cannot express "run again because
  the inputs changed" - stock OpenTofu has no memory of a provisioner's
  content and never re-runs one because its command changed.
- A receipt tracks **staleness across the resource's whole lifetime**. Its
  diff is the standing, reviewable answer to "have this effect's declared
  inputs changed since it last ran", asked on every plan, for as long as the
  resource exists.

A provisioner is therefore not a stricter receipt or a smaller one; it is a
different tool. Nothing in issue #353 gives choudoufu a memory of what a
provisioner did or what it was configured with - the record it writes is one
bit, "this create-time provisioner failed", which is exactly what stock keeps
in the state file and no more. See live/LIMITATIONS.md's `local-exec` entry.

The plan/apply/failure semantics:

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
   effect reruns. This accepts at-least-once semantics over
   exactly-once. The receipt cannot make an effect
   idempotent by itself (only the effect's own implementation can do
   that), but it guarantees that under-running never happens silently.
   Every unconfirmed effect stays visible as a pending diff until a
   receipt write proves it succeeded.

## Guard 4. The leaf rule

Nothing may reference a receipt's attributes. No resource argument, no
`depends_on` entry, no output. Nothing anywhere in the configuration reads
`aws_ssm_parameter.demo_effect.value`, `.arn`, `.id`, or any other
attribute of a receipt. A receipt is a leaf in the dependency graph.
Things can point into it (its own value expression reads other resources'
attributes, same as the demo's `local.demo_effect_inputs` does), but
nothing points out of it.

The reasoning is the same authority argument behind every other banned
construct in this fork. Once another resource's plan depends on a
receipt's value, the receipt is no longer a record of what already
happened. It becomes an input other things need to be correct, which is the
definition of authoritative state. "A stored claim about what exists that
the tool believes over the world itself... if the record being wrong makes
the tool do wrong things to the world, it is authoritative." A receipt
that nothing depends on can be wrong, stale, or gone, and the only
consequence is one idempotent re-run of its effect, a cost in time but not
in correctness. A receipt that something else depends on turns that same
loss into a wrong plan for whatever depended on it. Leaf status keeps
losing a receipt recoverable instead of letting the record grow back into
state. This is also why the demo's local, `demo_effect_inputs`, only reads
*other* resources' attributes into the receipt, never the other way
around. The data flow is one-directional by design.

Leaf-ness is lint-enforceable, because it is a static graph property. The
question is whether any traversal in the configuration reaches into a
resource that is statically recognizable as a receipt. See "Lint
enforcement" below for what is implemented and its boundary.

## Naming convention

A receipt's `name` argument (or equivalent client-assigned identity
argument, for whichever resource type carries it) follows this format.

```
/tofu-receipts/<estate>/<effect>
```

- `<estate>` is the owning estate's name, matching the `tofu-estate`
  marker value (see `live/MARKERS.md`).
- `<effect>` is a short, stable name for the effect the receipt stands
  for. It is `demo-effect` in the demonstration, and something like
  `db-migration` or `cdn-purge` in a real estate.

This convention is what makes a receipt statically recognizable. An
`aws_ssm_parameter` whose `name` argument is a literal string matching
this prefix is a receipt, and the lint rule below treats it as one without
needing any other marker or annotation.

## Prior art

The pattern is a narrow, config-native restatement of ideas proven
elsewhere.

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
  prior configuration instead of the whole thing, but the idea (a record
  of what was declared last time, attached to the system instead of a
  separate store) is the same.

## Lint enforcement

Implemented as rules in `internal/live/lint`. The leaf rule is
`RuleReceiptLeaf`. Any managed resource argument or output expression that
contains a direct traversal into an `aws_ssm_parameter` resource whose
`name` argument is a literal string starting with `/tofu-receipts/` is
rejected, citing this file's leaf rule.

Guard 2 and the secrets discipline are enforced from the same package, by
`RuleReceiptValue` and `RuleReceiptSecret` (described in their own
sections). All three rules recognize a receipt the same way, through the
literal `/tofu-receipts/` name, and all three read one expression at a
time within the boundary stated next. A parameter whose name is built from
a variable or an interpolation is recognized by none of them. The rules
only recognize a receipt when it is evident on the page.

The exact boundary, so it is not mistaken for full data-flow
analysis: the rule catches *direct* traversals only, meaning an expression
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
one-time kicks, the parameter's existence carries the entire answer. The
value is a constant (e.g. "done"). The plan signal is as clean as it gets:
"will be created" means "will fire". There is no secret risk and no value
semantics. Use this flavor first.

**Hash receipt (run-on-change only).** When the requirement is to re-run
whenever these inputs change, the value is sha256 over the declared
inputs, as specified above. Use it only when that requirement is real.

Both ship as fixtures, side by side, in `live/e2e/estate/receipts.tf`.
`aws_ssm_parameter.demo_existence` is the existence flavor and
`aws_ssm_parameter.demo_effect` the hash flavor. Beyond being the default
recommendation, the existence flavor has nothing to scrub and nothing to
go stale: its value carries no information, so there is no hash to keep
correct and no secret to mishandle in computing one. The
`receipt-cycle-existence` step in `live/e2e/run.sh` runs the flavor live.
The receipt starts owned and clean. The step breaks it with an
out-of-band `aws ssm delete-parameter` instead of a value overwrite,
because there is no changed value to write. The next plan proposes
exactly one create on it ("will be created" meaning "will fire").
Recreating it (the layer above playing the effect) and re-adopting its
markers converges the next plan back to clean. This is symmetric with
`receipt-cycle`, which runs the same cycle for the hash flavor via an
out-of-band value overwrite instead of a delete.

## Secrets discipline

Effect inputs MUST reference secrets by pointer, meaning the secret's ARN
and version identifier, never by value. A hash over low-entropy secret
material (a password) is offline-guessable by anyone holding the receipt
and knowing how the input was built. A hash over the secret's version-id leaks
nothing and answers the actual question ("did it rotate?") from metadata.
Lint enforces the directly visible case. `RuleReceiptSecret` in
`internal/live/lint` flags a receipt whose value expression contains
a direct reference to an input variable declared `sensitive = true` in the
same module, whether or not the reference sits inside a hash call, since
hashing does not launder a guessable secret. The boundary is the same
direct-traversal boundary the leaf rule states under "Lint enforcement". A
sensitive value routed through a `local`, arriving from another module, or
read from a resource attribute that a provider schema marks sensitive is
not caught, because each of those needs data-flow analysis or provider
schema knowledge the lint pass deliberately does not have. Those cases
remain a review rule.

## Why SSM, and the Dynamo comparison

Standard-tier SSM parameters cost nothing at estate scale. No storage
charge, no throughput charge, path-prefix IAM scoping, and native
versioning that retains old hashes as a free per-effect audit trail.
DynamoDB adds a table and per-request billing. S3 needs a bucket to exist
first. A tag on an anchor resource is free and fine when an effect has a
natural anchor, but external effects, the unobservable case receipts
exist for, have none.

The obvious comparison is a Dynamo lock table without the locking. The
difference is in the design. The lock table is tool infrastructure, owned
by no estate, required by every operation, coordinating writers through
conditional writes. Receipts are declared resources inside the estate,
planned and destroyed by the normal lifecycle, gating one effect each,
with no coordination protocol and no reader beyond declared-vs-live
diffing. The lock existed to protect an authoritative record. A receipt
records an effect that already happened, which makes it a memo.

## Cross-cloud boundary

The receipt pattern itself is provider-agnostic. Guard 3 means the
executor has no concept of a receipt anywhere in its code, so any
provider's plain key-value resource qualifies structurally: client-named
(admission path 1), holds a string, stays a leaf. Nothing AWS-specific
exists in the machinery.

The demo and the enforcement are AWS-only today. The estate's receipt is
an `aws_ssm_parameter`, and the leaf rule's recognition is hardcoded to
that type with a literal `/tofu-receipts/` name. On another cloud you can
follow this spec, but the leaf rule will not protect you, and its absence
is silent.

Generalizing needs a table, not a redesign: one recognition row per
provider (resource type, name attribute, path convention) in the lint
rule, plus a recommended-type entry here. Whatever type another cloud's
row names, it should be a plain key-value store rather than a secret
store: hashes are not secrets, and parking receipts in a secret store
inverts guard 2's reasoning.
