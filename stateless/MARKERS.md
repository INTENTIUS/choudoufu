# Marker spec

Spec version: 1

This file is the marker format for OpenTofu stateless mode. It is the only
integration surface external tools rely on. Nothing else about stateless
mode's internals is a contract; this document is.

A marker is an ownership record carried on the resource itself, as AWS
resource tags. There is no side channel, no registry, no shared library —
if a tool reads and writes these three tags according to the grammar below,
it can identify, adopt, and safely modify resources that belong to a
stateless estate, whether or not it has ever heard of OpenTofu.

## Tag keys

Three tag keys are defined. All three are plain resource tags, stamped on
every taggable resource that stateless mode manages.

| Key | Meaning | Present on |
|---|---|---|
| `tofu-estate` | The estate that owns the resource. | Every managed resource. |
| `tofu-address` | The resource's canonical config address. | Every managed resource. |
| `tofu-slot` | A stable, opaque cardinality slot. | `count` instances only. |

A resource carrying `tofu-estate` and `tofu-address` is fully identified: an
estate and a place within that estate's configuration. `tofu-slot` is
additional information layered on top for resources that come from a
`count` block; it does not replace `tofu-address`, which still carries the
full indexed address (see below).

## AWS tag constraints

These are hard limits imposed by AWS, not choices made here, and everything
below is designed to fit inside them:

- Tag value: at most 256 Unicode characters.
- Tag key: at most 128 Unicode characters (not a concern here — all three
  keys are short, fixed strings).
- Allowed characters in a value: letters and numbers representable in
  UTF-8, space, and the characters `+ - = . _ : / @`. No other punctuation,
  including `[`, `]`, and `"`, is permitted by AWS in a tag value.

A canonical OpenTofu resource address uses `[` `]` and `"` to express
instance keys (`aws_subnet.this["a"]`, `aws_eip.this[2]`), all three of
which are outside the AWS-allowed set. `tofu-address` values therefore go
through the escaping rule below before being written as a tag.

## `tofu-estate`

The name of the estate. An estate is the unit of ownership: everything
carrying the same `tofu-estate` value is one management domain, matched by
one `live` config block.

Grammar: `[a-z][a-z0-9-]{0,127}` — lowercase ASCII letter, digit, or
hyphen; must start with a letter; 1 to 128 characters. This is narrower
than the AWS-allowed character set on purpose: an estate name is meant to
be typed, grepped, and read aloud, not to carry arbitrary content. Example:
`stateless-e2e` (the P0.1 fixture's estate).

## `tofu-address`

The resource's canonical address as OpenTofu's config address formatter
produces it, including module path and any `for_each` or `count` instance
key, escaped per the rule below. This is the field that answers "which
config block owns this resource" — the entire binding mechanism for the
marker admission path (path 2) rests on this value matching an address
that exists in configuration.

**Grammar vs. what ships today.** The grammar and the `module.` segment
below describe the address format in full generality, because a marker
written under this spec has to remain readable by whatever eventually
reads module-qualified addresses. Nothing in the fork writes one yet: the
stateless subset lint refuses every module call outright, with "Child
modules are not available in stateless mode", before anything reads the
live system (`RuleChildModule`, `internal/stateless/lint/child_module.go`).
Identity resolution, projection, discovery, stamping and the rename each
still refuse a configuration with children, but as an internal invariant -
lint runs first, so reaching one of them means the pipeline ran out of order.
Every `tofu-address` value a current build stamps is a root-module address;
the `module.` segment is forward-looking spec, not shipped behavior.

Unescaped grammar (informal EBNF, matching OpenTofu's own address syntax):

```
address    = segment { "." segment } ;
segment    = ( "module" "." ident [ index ] ) | ( ident "." ident [ index ] ) ;
index      = "[" ( digits | quoted-key ) "]" ;
quoted-key = '"' key-chars '"' ;
```

Examples of unescaped addresses (the last one is spec-only — see above):

- `aws_vpc.this`
- `aws_subnet.this["a"]`
- `aws_eip.this[2]`
- `module.subnets["a"].aws_subnet.this` (not produced by any shipped build)

### Escaping rule

AWS tag values cannot contain `[`, `]`, or `"`. The escaping is a
three-character substitution over the whole address string, applied before
writing the tag and never reversed by any code path that only needs to
*compare* — comparison is always "escape the known config address, compare
strings," never "decode the tag blind":

1. Replace every `[` with `:`.
2. Delete every `]`.
3. Delete every `"`.

A `:` in an escaped value therefore always means "an index starts here";
the index runs to the next `.` or the end of the string.

| Unescaped | Escaped (`tofu-address` value) |
|---|---|
| `aws_vpc.this` | `aws_vpc.this` |
| `aws_subnet.this["a"]` | `aws_subnet.this:a` |
| `aws_eip.this[2]` | `aws_eip.this:2` |
| `module.subnets["a"].aws_subnet.this` | `module.subnets:a.aws_subnet.this` |

The full escaped value must be at most 256 characters (the AWS hard cap on
tag values). An address that does not fit is a lint-time error under
stateless mode, not a truncation — silently truncating an ownership key is
worse than refusing to admit the resource.

**Known limitation, left for the lint layer to enforce, not this spec to
paper over:** the escaping is lossy in two ways, both by design rather than
oversight:

- A bare integer `for_each`/`count` index and a quoted string index with
  the same digits collide: `this[2]` and `this["2"]` both escape to
  `this:2`. This is harmless in practice because a single resource block
  uses either `count` or `for_each`, never both, so the two never compete
  for the same address.
- A `for_each` key that itself contains `.` or `:` produces an address that
  cannot be unambiguously split back into segments. The escaping rule does
  not attempt to handle this case; `for_each` keys containing `.` or `:`
  (or any character outside the AWS-allowed set enumerated above) are
  outside the stateless subset and should be rejected by lint, the same way
  banned constructs are.

Unescaping. Removal planning turns a marker back into an address, which
the escaping rule supports for every value a lint-clean configuration
produces and refuses for two. A key containing `.` or `:` cannot be
located, because those separate the segments of an escaped address. A key
of all digits is read as a count index; a quoted string key of the same
digits escapes to the same value, and the reading cannot mislead, because
the resource is identified by its live import ID and the address is only
the label the plan prints — a live resource whose declared instance really
was the string key would have bound during discovery, since the comparison
is between two escaped values and those two are the same string.

## `tofu-slot`

Present only on resources that come from a `count` block. An opaque,
stable identifier for one member of a fungible set, assigned when the
instance is created.

Reuse is bounded, not absolute (amended, spec v1): a stateless estate has
no registry and no side channel — the only record of a slot is the tag on
the live resource, so once that resource is gone the slot is unrecoverable.
The guarantee is therefore: the minting high-water mark is the highest slot
among the block's live resources; a slot is never reused while any resource
holds it, and never duplicated within a set; a slot whose resource has been
deleted may be minted again later.

Format: an unsigned base-10 integer, ASCII digits only, no leading zeros
(the value `0` is written as `0`, not `00`). Assigned from a monotonic
counter per `count` resource block (not per instance address — an
instance's address contains the index a slot is deliberately independent
of), starting at `0`: the first instance of `aws_eip.this` gets slot `0`,
the second gets slot `1`, and so on. New instances mint above the live
high-water mark. Ten digits (up to 4294967295) is the ceiling; no
realistic `count` approaches it.

Slots bind; addresses follow. For a `count` instance carrying a slot, the
slot is what binds it to a declared instance: the k-th lowest live slot
binds to index k. `tofu-address` remains mandatory and remains the full
indexed address, but a value naming a different index than the slot bound
to is STALE, not a rival claim — it is repaired by the next plan's
ordinary tag write, and it is never a collision. Scale-down deletes the
highest slots, compared numerically, so every survivor keeps the index it
already occupied — the no-churn rule stated precisely.

Slot values are compared numerically, not lexicographically, because they
are carried as strings in a tag. `"9"` is a lower slot than `"10"` even
though it sorts after it as a string — any tool implementing the
scale-down rule ("surplus deletes the highest slots") must parse before
comparing.

`tofu-slot` is independent of `tofu-address`: a plain rename (P3.3) that
does not change cardinality leaves slot assignments untouched. Only a
change in the number of live instances mints or retires slots.

## Ownership semantics

- A resource carrying a `tofu-estate` tag belongs to that estate. Full
  stop — the value is the entire ownership claim, and there is no
  secondary check.
- A resource carrying neither `tofu-estate` nor `tofu-address` is foreign:
  outside every estate's ownership, reported, protected, never
  auto-deleted (see the roadmap's foreign rule).
- A resource carrying `tofu-estate` but missing or carrying an unparseable
  `tofu-address` is malformed, not foreign and not owned. This is a named
  error surfaced to the operator, the same way a binding ambiguity is (two
  live resources claiming one address) — never guessed at, never silently
  treated as either "belongs to no one" or "belongs to whichever address
  looks close enough."
- Two resources carrying the same `tofu-estate` and the same
  `tofu-address` at once is also a named error: the marker admission path
  assumes at most one live resource per address per estate, and a
  collision means something upstream (a manual tag edit, a botched
  `live-mv`) needs a human to resolve it.

## The rename rule

Renaming a resource in config — changing its address, moving it into or
out of a module, or changing a `for_each` key — is done by rewriting the
`tofu-address` tag on the live resource to the new escaped address. That
tag write *is* the move operation. There is no state to surgically edit,
no `moved` block to author, no two-step "mark old, create new" dance: the
old address is simply gone from the tag the instant the new one is
written, because a single tag value cannot hold both.

`choudoufu live-mv <old-address> <new-address>` (P3.3) performs exactly
this: escape both addresses, find the live resource whose `tofu-address`
equals the old escaped value within the target estate, and overwrite it
with the new escaped value in one tag-update call. After it runs, a plan
against the old address finds nothing (it was never a delete — the
resource was never bound to "old" as far as anything after the rewrite is
concerned), and a plan against the new address finds the resource already
bound: zero-churn, by construction rather than by special-casing renames
in the plan engine.

Old markers never linger. There is exactly one `tofu-address` value on a
resource at any time, and after a rewrite that value is the new address,
not a history of addresses it once had.

## Versioning

The header at the top of this file (`Spec version: 1`) versions this
*document*, not the resources it describes — there is no `tofu-marker-version`
tag, and none is planned. Markers written under an older revision of this
spec remain on live resources indefinitely; nothing rewrites them
proactively.

A change is additive (no version bump) if every marker already written
under the current spec still parses and means the same thing under the new
one — for example, documenting a previously-implicit rule more precisely,
or adding a new optional tag key that absence-tolerant readers can ignore.

A change is breaking (version bump required) if it invalidates that
guarantee — renaming a tag key, changing the escaping rule, changing what
absence of a tag means, or narrowing a grammar so previously-valid values
become invalid. A breaking change requires a coordinated rewrite pass over
every live estate before tools built against the new version can trust
what they read, and the version number here is what lets a tool detect
"this estate's markers predate what I understand" and refuse to guess
instead of misreading them.

## Interop

This file is the entire contract. Nothing about stateless mode's Go
internals — the projection builder, the lint rules, the identity
resolution — is load-bearing for another tool to participate. Any tool
that reads and writes `tofu-estate`, `tofu-address`, and `tofu-slot` per
the grammar above, on the resource types it manages, can discover,
classify, and safely mutate resources in a stateless estate.

This grammar is designed for external adoption: any tool, in any language,
can read and write these three tags without linking against this fork or
knowing it exists. No known implementation of this spec exists outside
this fork today. That is expected at this stage — the spec is written to
be the stable integration surface a future tool builds against, not proof
that one already has.
