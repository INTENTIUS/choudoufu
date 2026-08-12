# Limitations

This file is the limits wing's index. Every construct listed here has a
fixture directory under `stateless/e2e/limits/<name>/` — minimal, self
contained configuration that loads and triggers exactly the behavior
described — and an assertion in `internal/stateless/lint/limits_test.go` that
pins that behavior today. Doc, fixture, and test are required to agree:
`TestLimitationsDocCoversDirs` fails if this file names a directory that does
not exist or omits one that does, `TestLimitsDirsMatchTable` fails if the test
table drifts from the directory tree, and `TestLimitsEnforced` /
`TestLimitsNotYetEnforced` fail if the lint rule that actually fires stops
matching what is written below. Nothing here is asserted from memory.

Two kinds of entry:

- **Enforced today** — lint (`internal/stateless/lint`) rejects the
  construct now, with the named rule constant.
- **Documented, not yet enforced** — the roadmap bans or bounds the
  construct, but no check exists yet (or the check exists in a different
  package, at a different phase, than lint). The fixture directory loads
  clean today; the test asserts zero issues on purpose, so that the day
  enforcement lands, the test fails loudly instead of the gap staying quiet.
  Only new enforcement is allowed to move an entry from this section to the
  one above.

The recurring justification, quoted once here rather than in every entry
below: "Every banned feature exists to maintain or repair the store; that is
the test for edge cases."

## Enforced today

### local-exec

**Construct:** `provisioner "local-exec"` on a resource.

**Why banned:** a provisioner runs an effect, not a resource. Whether it
already ran is knowable only from a stored record of the run — exactly the
authority stateless mode gives up. The store test applies directly.

**Forwarding address:** the lifecycle layer — run it in Ops/CI, outside the
plan/apply cycle, where a real execution log can say whether it happened.

**Enforcement:** `RuleProvisioner`, `internal/stateless/lint/lint.go`
(`checkProvisioners`). Fixture: `stateless/e2e/limits/local-exec/`.

### remote-exec

**Construct:** `provisioner "remote-exec"` plus the `connection` block that
configures it.

**Why banned:** same as local-exec — an effect with no stored record of
whether it ran. The connection block only exists to reach a provisioner, so
it is rejected in its own right rather than tolerated once the provisioner
using it is gone.

**Forwarding address:** the lifecycle layer — Ops/CI, same as local-exec.

**Enforcement:** `RuleProvisioner` (fires twice: once for the provisioner,
once for the connection block). Fixture:
`stateless/e2e/limits/remote-exec/`.

### null-resource

**Construct:** `null_resource` with a `triggers` map.

**Why banned:** a `null_resource` has no existence outside the record kept
of it; `triggers` is state used to decide when to re-run something attached
to it. That record is the store. Logical-resource family, per "Banned, and
why".

**Forwarding address:** the receipts pattern — a declared, leaf resource
whose value is a hash of inputs, read back to decide whether an effect needs
to re-run, without any of `null_resource`'s implicit re-trigger machinery.
Documented in `stateless/RECEIPTS.md` (forthcoming, PE.3).

**Enforcement:** `RuleLogicalResource` (prefix `null_`),
`internal/stateless/lint/admission.go` (`logicalType`). Fixture:
`stateless/e2e/limits/null-resource/`.

### local-file

**Construct:** `local_file`.

**Why banned:** the file's content is generated once and stored in state;
there is no live system to read it back from on the next run. Logical-resource
family.

**Forwarding address:** a build artifact — render it as a build step (CI,
a Makefile, a chant task) that produces the file on disk before OpenTofu
runs, not as a resource OpenTofu tracks.

**Enforcement:** `RuleLogicalResource` (prefix `local_`). Fixture:
`stateless/e2e/limits/local-file/`.

### random-password

**Construct:** `random_password`.

**Why banned:** the generated value only exists because state remembered it;
regenerating it from the live system is impossible by construction — a
random value has no live twin. Logical-resource family.

**Forwarding address:** a secret-store Op — generate and store the secret
in a secret manager (outside OpenTofu's model entirely), and have
configuration reference it by ARN/path, never by value. The same forwarding
applies to `tls_*`, banned for the same reason.

**Enforcement:** `RuleLogicalResource` (prefix `random_`). Fixture:
`stateless/e2e/limits/random-password/`.

### time-sleep

**Construct:** `time_sleep`.

**Why banned:** a `time_*` resource's entire value is "did this already
happen, and when" — a question only a stored record answers. Logical-resource
family.

**Forwarding address:** scheduling in the lifecycle layer — sequence the
delay in Ops/CI (a wait step, a dependency on an external readiness check),
not as a resource in the graph.

**Enforcement:** `RuleLogicalResource` (prefix `time_`). Fixture:
`stateless/e2e/limits/time-sleep/`.

### remote-state

**Construct:** `data "terraform_remote_state"`.

**Why banned:** it reads a state file, and stateless mode has no state file
to read. Named explicitly in "Banned, and why".

**Forwarding address:** live data sources — read the same live objects the
other estate reads with a data source of their own type, or pass values
across explicitly as variables or module outputs.

**Enforcement:** `RuleRemoteState`, `internal/stateless/lint/lint.go`
(`checkDataResources`). Fixture: `stateless/e2e/limits/remote-state/`.

### moved-block

**Construct:** a `moved` block.

**Why banned:** it rewrites which state entry belongs to which address, and
there is no state entry to rewrite. Named explicitly in "Banned, and why".

**Forwarding address:** `choudoufu live-mv <old-address> <new-address>`
(P3.3) — the marker rewrite that plays the same role by editing the live
resource's `tofu-address` tag directly (`stateless/MARKERS.md`, "The rename
rule").

**Enforcement:** `RuleMovedBlock`, `internal/stateless/lint/lint.go`
(`checkMovedBlocks`). Fixture: `stateless/e2e/limits/moved-block/`.

### child-module

**Construct:** a `module` block, at any depth.

**Why banned:** stateless mode v0 is a root-module mode. Identity resolution,
discovery, marker stamping and the projection all stop at the root, and module
expansion — `count` or `for_each` on a module block — changes every resource
address inside the module, which is exactly what a `tofu-address` marker
records. Binding markers under an expansion that can renumber them is the
ambiguity the marker exists to remove.

**Forwarding address:** move the module's resources into the root module, or
give the module an estate of its own — its own directory, its own `live`
block, its own `estate` name. Two estates are two independent runs, which is
the separation a child module was standing in for.

**Enforcement:** `RuleChildModule`, `internal/stateless/lint/child_module.go`
(`checkChildModules`). Fixture: `stateless/e2e/limits/child-module/`, which is
a tree rather than a single file and needs `choudoufu get` before the rule can
be reached: an uninstalled module block is refused while the configuration is
still being loaded, earlier than any stateless code runs.
The five packages downstream of lint (`identity`, `discovery`, `stamp`,
`projection`, `mv`) each still refuse a configuration with children, but as a
one-line internal invariant: lint runs first in both commands, so reaching one
of them with a child module means the pipeline ran out of order.

### backend-block

**Construct:** `terraform { backend "..." { } }`.

**Why banned:** a backend configures where authoritative state is stored and
locked. Stateless mode has no state file to store and nothing for a lock to
protect. Named explicitly in "Banned, and why".

**Forwarding address:** none — remove the block. The projection (rebuilt
from the live system every run, discarded after) replaces what a backend
would have stored; there is nothing to point it at instead.

**Enforcement:** `RuleStateBackend`, `internal/stateless/lint/lint.go`
(`checkStateBackends`). Fixture: `stateless/e2e/limits/backend-block/`.

### cloud-block

**Construct:** `terraform { cloud { } }`.

**Why banned:** a remote state backend under another name, with remote
locking attached — the same problem as `backend-block` by a different
syntax.

**Forwarding address:** none — remove the block, same as `backend-block`.

**Enforcement:** `RuleStateBackend` (same rule as `backend-block`; the two
fixtures exist separately because they are two distinct HCL forms of one
rule, and each should be provably caught on its own). Fixture:
`stateless/e2e/limits/cloud-block/`.

### unadmitted-type

**Construct:** `aws_instance`, a resource type outside the v0 admission
table.

**Why bounded:** "The admission rule" — a type participates only if its
identity is recoverable from the live system with no memory, by one of the
four admission paths. `aws_instance` is in the AWS provider survey (65 of 68
top types admitted) but is not yet in the hardcoded v0 table
(`internal/stateless/lint/admission.go`); this is a scoping boundary, not a
permanent ban.

**Forwarding address:** the provider survey / v0 admission table — grows as
later phases add types and, eventually, as provider identity schemas
(opentofu#2854) make most of the table derivable instead of hardcoded.

**Enforcement:** `RuleUnadmittedType`, `internal/stateless/lint/lint.go`
(`checkManagedResources`). Fixture: `stateless/e2e/limits/unadmitted-type/`.

### count-index-in-tag

**Construct:** `count.index` interpolated into a tag value
(`tags = { Name = "vpc-${count.index}" }`).

**Why banned:** "Banned, and why" — `count.index` in an identity-bearing
property is banned, because a marker written from an index has no
correspondence once instances are added, removed, or reordered; the
replacement is a `for_each` key, which is stable by construction.

**Forwarding address:** `for_each` — key the resource by a stable string
instead of a positional index.

**Deliberate carve-out:** the `tofu-address` marker tag value (see
`stateless/MARKERS.md`) is exempt from this rule — `markerKeysExemptFromCountIndex`
in `count_index.go` allows `count.index` there and there alone, because a
count instance's canonical address permanently includes its instance index,
the marker doing its specified job rather than leaking into an
identity-bearing property; `tofu-slot` is deliberately not among the
exempted keys.

**Enforcement:** `RuleCountIndex`, `internal/stateless/lint/count_index.go`
(`checkCountIndex`) — rejects `count.index` anywhere it is reachable from a
managed resource's own configuration body (arguments, tag values, nested
blocks, and conditional/template expressions that reference it indirectly);
the count expression itself and the other meta-argument positions are out of
scope by construction (see `internal/stateless/lint/doc.go`, "Scope of the
count.index rule"). Fixture: `stateless/e2e/limits/count-index-in-tag/`.

### foreach-dotted-key

**Construct:** a `for_each` key containing `.` (e.g. `"a.b"`), or any other
character outside the safe set named below.

**Why banned:** `stateless/MARKERS.md`'s escaping rule for `tofu-address`
cannot unambiguously round-trip a key containing `.` or `:` — the escaped
address format uses `.` as the segment separator and `:` to introduce an
instance key, so either character inside a key collides with the escaping
rule itself. Both are AWS-legal in a tag value, which is exactly what made
this class of key dangerous before this rule existed: it passed lint,
stamped a marker, and applied cleanly, and only the *next* run found the
wedge (a `:` key reads back as a malformed marker; a `.` key makes
`discovery.UnescapeAddress` refuse on deletion), with no in-band way out.

**Forwarding address:** pick a `for_each` key drawn from the intersection of
the AWS-allowed tag character set and the two escaping separators removed:
letters, digits, space, and `+ - = _ / @` — the AWS tag-value set from
`stateless/MARKERS.md` **minus** `.` and `:`. An empty key is also
rejected, since an escaped address cannot end in a bare separator either.

**Enforcement:** `RuleForEachKey`, `internal/stateless/lint/foreach_key.go`
(`checkForEachKeys`) — for every `for_each` expression it can evaluate
statically, rejects any key outside Unicode letters, Unicode digits, space,
and `+ - = _ / @`, including the empty string. The same bound is enforced a
second time in `internal/stateless/identity` (`ValidForEachKey`), so a
configuration that reaches identity resolution without passing lint still
cannot mint a marker nothing can read back. Fixture:
`stateless/e2e/limits/foreach-dotted-key/`.

## Documented, not yet enforced

### overlong-address

**Construct:** a resource whose escaped `tofu-address` would exceed 256
characters (an absurdly long resource label).

**Why bounded:** AWS caps a tag value at 256 Unicode characters, a hard
limit `stateless/MARKERS.md` states directly: "An address that does not fit
is a lint-time error under stateless mode, not a truncation — silently
truncating an ownership key is worse than refusing to admit the resource."

**Forwarding address:** shorten the resource address — a shorter label, or
less module nesting (module path is part of the address).

**Enforcement:** documented, not yet enforced — a MARKERS-adjacent rule, no
roadmap task number assigned yet. No lint rule measures address length
today. Fixture: `stateless/e2e/limits/overlong-address/`, asserted to
produce zero issues by `TestLimitsNotYetEnforced`.

### duplicate-identity

**Construct:** two resource blocks (of an admitted, client-named type) that
resolve to the same identity — two `aws_s3_bucket` blocks both naming
bucket `estate-shared`.

**Why bounded:** `stateless/MARKERS.md`, "Ownership semantics" — two live
resources claiming one address is a named error, never a guess. The
identity package enforces the analogous rule on the config side: two config
blocks that would both bind to the same live object is an ambiguity to name,
not resolve.

**Forwarding address:** give each resource a distinct client-assigned
identity (a distinct bucket name, role name, etc.) — there is no automatic
resolution, by design; the whole point is that a human has to choose.

**Enforcement:** resolve-time, not lint —
`internal/stateless/identity/resolve.go` (`checkCollisions`), not
`internal/stateless/lint`. This split is intentional and documented rather
than papered over: lint has no notion of identity, only construct and type
shape, so it cannot see two `bucket` attributes colliding; identity
resolution runs later in the pipeline and is where that check belongs.
Fixture: `stateless/e2e/limits/duplicate-identity/`, asserted to produce
zero *lint* issues by `TestLimitsNotYetEnforced` (a parallel fixture at
`internal/stateless/identity/testdata/duplicate-identity/` already exercises
the resolve-time error itself).

## Behavioral limits (runtime, not lint)

The entries above are lint matters: each has a fixture directory and an
asserted rule. The limits below are runtime behaviors of the implemented
mode — documented here, asserted by the integration tests named in each.

**Removal coverage is the admission table.** A resource block deleted from
the configuration is planned as a destroy because the estate-wide sweep
lists every type in the admission table for this estate's markers. A
resource carrying this estate's markers at a type *outside* the admission
table is not swept and its deletion is not planned. Adoption is a tag
write, so this is reachable by hand-stamping markers onto an unadmitted
type. The markers are the contract; the admission table is the list of
types the contract is defined over. (Asserted in
internal/stateless/lifecycle/exactness_test.go.)

**Untaggable types cannot be removed by the sweep.** `aws_route`,
`aws_route_table_association`, `aws_s3_bucket_policy` and
`aws_iam_role_policy_attachment` carry no tags, so they can carry no
ownership marker and the sweep has nothing to search on. Their identity is
built from their own configuration, which means deleting the resource
block deletes the only record of which resource it was. Destroy the
resource before removing its block, or delete it out of band. Every plan
names these types under "Not swept for removal".

**An import-derived prior state cannot hold config-only attributes.** A
provider attribute that the cloud does not store and the configuration
does not set has no value in a projection: no read can return it. When the
resource changes for any other reason, the provider's default arrives in
the diff as a null-to-default line beside the real change.
`aws_security_group`'s `revoke_rules_on_delete` is the case in the v0
subset. This is not specific to stateless mode: a stock `choudoufu import` of
the same resource followed by the same drift prints the identical line. It
is cosmetic — the attribute is only consulted on delete — and not
recoverable at OpenTofu's layer, because provider defaults live in the SDK
and not in the schema OpenTofu is served.
