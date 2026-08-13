# Marker spec

Spec version 1

This file is the format for live resource markers, the ownership records
this fork keeps on resources instead of in a state file. It is the only
integration surface external tools rely on. Nothing else about the mode's
internals is a contract. This document is.

A marker is an ownership record carried on the resource itself, as AWS
resource tags. There is no side channel, no registry, and no shared library.
If a tool reads and writes these three tags according to the grammar below,
it can identify, adopt, and safely modify resources that belong to a
marker-managed estate, whether or not it has ever heard of OpenTofu.

## Tag keys

Three tag keys are defined, plus an optional fourth family. All are plain
resource tags, stamped on every taggable resource the mode manages.

| Key | Meaning | Present on |
|---|---|---|
| `tofu-estate` | The estate that owns the resource. | Every managed resource. |
| `tofu-address` | The resource's canonical config address, or its first chunk. | Every managed resource. |
| `tofu-address-2`, `tofu-address-3`, `tofu-address-4` | The rest of `tofu-address`, in order, when it does not fit in one tag. | Only a resource whose escaped address is longer than one tag value. |
| `tofu-slot` | A stable, opaque cardinality slot. | `count` instances only. |

A resource carrying `tofu-estate` and `tofu-address` is fully identified by
an estate and a place within that estate's configuration. `tofu-slot` is
additional information layered on top for resources that come from a
`count` block. It does not replace `tofu-address`, which still carries the
full indexed address (see below). `tofu-address-2` through `tofu-address-4`
are additional information of a different kind: they do not stand on their
own, and exist only to carry the rest of a `tofu-address` value one tag
could not hold. See "tofu-address continuation tags" below.

## AWS tag constraints

These are hard limits imposed by AWS, not choices made here, and everything
below is designed to fit inside them.

- A tag value holds at most 256 Unicode characters.
- A tag key holds at most 128 Unicode characters. Not a concern here, since
  all three keys are short, fixed strings.
- A value may contain letters and numbers representable in UTF-8, space,
  and the characters `+ - = . _ : / @`. No other punctuation, including
  `[`, `]`, and `"`, is permitted by AWS in a tag value.

A canonical OpenTofu resource address uses `[` `]` and `"` to express
instance keys (`aws_subnet.this["a"]`, `aws_eip.this[2]`), all three of
which are outside the AWS-allowed set. `tofu-address` values therefore go
through the escaping rule below before being written as a tag.

## `tofu-estate`

The name of the estate. An estate is the unit of ownership. Everything
carrying the same `tofu-estate` value is one management domain, matched by
one `live` config block.

The grammar is `[a-z][a-z0-9-]{0,127}`. A lowercase ASCII letter, then
lowercase letters, digits, or hyphens, 1 to 128 characters in total. This is
narrower than the AWS-allowed character set on purpose. An estate name is
meant to be typed, grepped, and read aloud, not to carry arbitrary content.
An example is `stateless-e2e`, the demo estate's name
(`live/e2e/estate/`).

## `tofu-address`

The resource's canonical address as OpenTofu's config address formatter
produces it, including module path and any `for_each` or `count` instance
key, escaped per the rule below. This is the field that answers "which
config block owns this resource". The entire binding mechanism for the
marker admission path (path 2) rests on this value matching an address that
exists in configuration.

**Grammar vs. what ships today.** The `module.` segment below is shipped,
not forward-looking: identity resolution, projection, discovery, stamping
and the rename all traverse `cfg.Children`, and a `tofu-address` value
carries the full module-qualified address for a resource inside a static
module tree or a `for_each`-keyed module call with statically-evaluable
keys, at any nesting depth (issue #59, phases 1-2 / "59b"/"59c"). The one
segment shape this grammar allows but no build ever produces is a `count`
instance key on a `module.` segment - a module block expanded with `count`
is refused outright before anything reads the live system, permanently,
because the position-based renumbering it causes is exactly the ambiguity
a `tofu-address` marker exists to remove (`RuleChildModule`,
`internal/live/lint/child_module.go`; `live/LIMITATIONS.md`,
"child-module"). A resource inside a `for_each`-keyed module's own
instances is not auto-written even though its address is shipped: stamping
cannot inject a marker into a shared configuration body, so that address is
built by hand instead (`live/LIMITATIONS.md`'s "keyed module" behavioral
limit; the concept page's "Modules" section has the idiom).

The unescaped grammar, in informal EBNF matching OpenTofu's own address
syntax.

```
address    = segment { "." segment } ;
segment    = ( "module" "." ident [ index ] ) | ( ident "." ident [ index ] ) ;
index      = "[" ( digits | quoted-key ) "]" ;
quoted-key = '"' key-chars '"' ;
```

Some examples of unescaped addresses follow. The last one is spec-only,
see above: a `count` key on a `module.` segment, refused permanently.

- `aws_vpc.this`
- `aws_subnet.this["a"]`
- `aws_eip.this[2]`
- `module.subnets["a"].aws_subnet.this`
- `module.subnets[2].aws_subnet.this` (spec-only; a count-expanded module
  is refused permanently, see above)

### Escaping rule

AWS tag values cannot contain `[`, `]`, or `"`. The escaping is a
three-character substitution over the whole address string, applied before
writing the tag and never reversed by any code path that only needs to
*compare*. Comparison is always "escape the known config address, compare
strings," never "decode the tag blind."

1. Replace every `[` with `:`.
2. Delete every `]`.
3. Delete every `"`.

A `:` in an escaped value therefore always means an index starts there. The
index runs to the next `.` or the end of the string.

| Unescaped | Escaped (`tofu-address` value) |
|---|---|
| `aws_vpc.this` | `aws_vpc.this` |
| `aws_subnet.this["a"]` | `aws_subnet.this:a` |
| `aws_eip.this[2]` | `aws_eip.this:2` |
| `module.subnets["a"].aws_subnet.this` | `module.subnets:a.aws_subnet.this` |

A single tag value holds at most 256 characters (the AWS hard cap on tag
values). An escaped address that does not fit is carried across several
tags instead - see "tofu-address continuation tags", directly below - up to
a total of 1024 characters. Past that wider ceiling, the original rule
still holds without exception: an address that does not fit is a lint-time
error, not a truncation. Silently truncating an ownership key is worse than
refusing to admit the resource.

### `tofu-address` continuation tags

Deep module trees and long `for_each` keys can produce an escaped address
longer than one 256-character tag value holds. Rather than refuse every
such address outright, `tofu-address` carries the first 256 characters and
up to three more tags - `tofu-address-2`, `tofu-address-3`,
`tofu-address-4` - carry the rest, in order, 256 characters at a time. A
reader concatenates `tofu-address`, then `tofu-address-2` if present, then
`tofu-address-3`, then `tofu-address-4`, and the result is the one escaped
address that would not fit in a single tag. This is the only sanctioned way
to read a split address; the continuation tags are never meaningful on
their own, individually or out of order.

This raises the effective limit to 1024 characters (four tags of 256), not
an unbounded one. An address that does not fit in four tags is still a
lint-time error - RuleOverlongAddress, `internal/live/lint/overlong_address.go`
- for the same reason the original 256-character refusal existed:
truncating an ownership key is worse than refusing to admit the resource,
and a fourth tier of continuation would just move the same question further
out without answering it. Four tags is deliberately generous headroom
against the 50-tag-per-resource AWS limit (minus whatever tags the
configuration's own `tags` block already uses) while staying a small, fixed
number rather than a knob a configuration can turn.

A resource whose address fits in 256 characters - the overwhelming common
case, and every marker written before this addition existed - carries only
`tofu-address` and no continuation tags at all, exactly as before. Nothing
about a short address changes.

**Reading a corrupt chain.** A continuation tag can only exist because
something wrote the whole set together; the three tags below `tofu-address`
are never independently meaningful. A tag map where `tofu-address-3` is
present but `tofu-address-2` is not - the middle of the chain deleted by a
hand edit, a tag policy misfire, or two racing writes - cannot be
concatenated into anything, and per "Ownership semantics" below it is
malformed: reported loudly and by name, never silently read as the address
up to the gap and never treated as unowned.

**Writing one.** A tool that stamps markers and encounters an address over
256 characters splits it the same way: the first 256 characters into
`tofu-address`, the next 256 into `tofu-address-2`, and so on, stopping at
whichever tag holds the last of the address (a shorter final chunk is
normal and is not padded). A tool that only ever writes addresses under 256
characters never has to think about this section at all.

**Known limitation, left for the lint layer to enforce, not this spec to
paper over.** The escaping is lossy in two ways, both by design rather than
oversight.

- A bare integer `for_each`/`count` index and a quoted string index with
  the same digits collide. `this[2]` and `this["2"]` both escape to
  `this:2`. This is harmless in practice because a single resource block
  uses either `count` or `for_each`, never both, so the two never compete
  for the same address.
- A `for_each` key that itself contains `.` or `:` produces an address that
  cannot be unambiguously split back into segments. The escaping rule does
  not attempt to handle this case. `for_each` keys containing `.` or `:`
  (or any character outside the AWS-allowed set enumerated above) are
  outside the admitted subset and should be rejected by lint, the same way
  banned constructs are.

Unescaping. Removal planning turns a marker back into an address, which the
escaping rule supports for every value a lint-clean configuration produces
and refuses for two. A key containing `.` or `:` cannot be located, because
those separate the segments of an escaped address. A key of all digits is
read as a count index. A quoted string key of the same digits escapes to
the same value, and the reading cannot mislead, because the resource is
identified by its live import ID and the address is only the label the plan
prints. A live resource whose declared instance really was the string key
would have bound during discovery, since the comparison is between two
escaped values and those two are the same string.

## `tofu-slot`

Present only on resources that come from a `count` block. An opaque, stable
identifier for one member of a fungible set, assigned when the instance is
created.

Reuse is bounded, not absolute (amended, spec v1). A marker-managed estate
has no registry and no side channel. The only record of a slot is the tag
on the live resource, so once that resource is gone the slot is
unrecoverable. The guarantee is therefore as follows. The minting
high-water mark is the highest slot among the block's live resources. A
slot is never reused while any resource holds it, and never duplicated
within a set. A slot whose resource has been deleted may be minted again
later.

The format is an unsigned base-10 integer, ASCII digits only, no leading
zeros (the value `0` is written as `0`, not `00`). Slots are assigned from
a monotonic counter per `count` resource block (not per instance address,
since an instance's address contains the index a slot is deliberately
independent of), starting at `0`. The first instance of `aws_eip.this`
gets slot `0`, the second gets slot `1`, and so on. New instances mint
above the live high-water mark. Ten digits (up to 4294967295) is the
ceiling. No realistic `count` approaches it.

Slots bind, addresses follow. For a `count` instance carrying a slot, the
slot is what binds it to a declared instance. The k-th lowest live slot
binds to index k. `tofu-address` remains mandatory and remains the full
indexed address, but a value naming a different index than the slot bound
to is STALE, not a rival claim. It is repaired by the next plan's ordinary
tag write, and it is never a collision. Scale-down deletes the highest
slots, compared numerically, so every survivor keeps the index it already
occupied. That is the no-churn rule stated precisely.

Slot values are compared numerically, not lexicographically, because they
are carried as strings in a tag. `"9"` is a lower slot than `"10"` even
though it sorts after it as a string. Any tool implementing the scale-down
rule ("surplus deletes the highest slots") must parse before comparing.

`tofu-slot` is independent of `tofu-address`. A plain rename that does not
change cardinality leaves slot assignments untouched. Only a
change in the number of live instances mints or retires slots.

## Ownership semantics

- A resource carrying a `tofu-estate` tag belongs to that estate, full
  stop. The value is the entire ownership claim, and there is no secondary
  check.
- A resource carrying neither `tofu-estate` nor `tofu-address` is foreign.
  It sits outside every estate's ownership and is reported, protected, and
  never auto-deleted.
- A resource carrying `tofu-estate` but missing or carrying an unparseable
  `tofu-address` is malformed, not foreign and not owned. This is a named
  error surfaced to the operator, the same way a binding ambiguity is (two
  live resources claiming one address). It is never guessed at, and never
  silently treated as either "belongs to no one" or "belongs to whichever
  address looks close enough." A `tofu-address` continuation chain with a
  gap in it - a `tofu-address-3` present while `tofu-address-2` is not - is
  the same malformed case: it cannot be concatenated into anything, so it
  is reported rather than read as the address up to the gap.
- Two resources carrying the same `tofu-estate` and the same
  `tofu-address` at once is also a named error. The marker admission path
  assumes at most one live resource per address per estate, and a
  collision means something upstream (a manual tag edit, a botched
  `live-mv`) needs a human to resolve it. This holds regardless of region
  or provider configuration: an address is unique estate-wide, not
  estate-wide-per-region, so two live resources in two different regions
  both carrying one estate's marker for one address are the same named
  collision as two in one region, not two legitimate resources that happen
  to sit in different places. A multi-provider estate (issue #69) reports
  it the same way, naming every region involved.

## The rename rule

Renaming a resource in config, whether changing its address, moving it into
or out of a module, or changing a `for_each` key, is done by rewriting the
`tofu-address` tag on the live resource to the new escaped address. That
tag write *is* the move operation. There is no state to surgically edit, no
`moved` block to author, no two-step "mark old, create new" dance. The old
address is simply gone from the tag the instant the new one is written,
because a single tag value cannot hold both.

`choudoufu live-mv <old-address> <new-address>` performs exactly this. It escapes both addresses, finds the live resource whose
`tofu-address` equals the old escaped value within the target estate, and
overwrites it with the new escaped value in one tag-update call. After it
runs, a plan against the old address finds nothing (it was never a delete,
since the resource was never bound to "old" as far as anything after the
rewrite is concerned), and a plan against the new address finds the
resource already bound. Zero churn, by construction rather than by
special-casing renames in the plan engine.

Old markers never linger. There is exactly one `tofu-address` value on a
resource at any time, and after a rewrite that value is the new address,
not a history of addresses it once had. This holds for continuation tags
too: a rename onto a shorter address writes fewer `tofu-address-*` tags
than the old one carried, and a rename tool is expected to delete whichever
continuation tags the new address does not reach rather than leave a stale
tag that a later read would concatenate onto the new value.

## Versioning

The header at the top of this file ("Spec version 1") versions this
*document*, not the resources it describes. There is no
`tofu-marker-version` tag, and none is planned. Markers written under an
older revision of this spec remain on live resources indefinitely. Nothing
rewrites them proactively.

A change is additive (no version bump) if every marker already written
under the current spec still parses and means the same thing under the new
one. Documenting a previously implicit rule more precisely qualifies, as
does adding a new optional tag key that absence-tolerant readers can
ignore. The `tofu-address` continuation tags are exactly this: every marker
written before they existed has no `tofu-address-*` tag and reads exactly
as it always did, so no existing marker is invalidated. The asymmetry runs
the other way instead - a reader built only against spec version 1's single
`tofu-address` tag will read a NEW split marker's first 256 characters as
the whole address, silently, rather than erroring. That is a real gap for
anything that has not been updated to read continuation tags, and it is
accepted rather than papered over with a version bump, because the
definition above is about old data under new code, not new data under old
code; the versioning number cannot help with the latter no matter which way
it is called.

A change is breaking (version bump required) if it invalidates that
guarantee. Renaming a tag key, changing the escaping rule, changing what
absence of a tag means, or narrowing a grammar so previously valid values
become invalid all qualify. A breaking change requires a coordinated
rewrite pass over every live estate before tools built against the new
version can trust what they read, and the version number here is what lets
a tool detect that an estate's markers predate what it understands, so it
can refuse to guess instead of misreading them.

## Interop

This file is the entire contract. Nothing about the mode's Go internals
(the projection builder, the lint rules, the identity resolution) is
load-bearing for another tool to participate. Any tool that reads and
writes `tofu-estate`, `tofu-address`, and `tofu-slot` per the grammar
above, on the resource types it manages, can discover, classify, and
safely mutate resources in a marker-managed estate.

This grammar is designed for external adoption. Any tool, in any language,
can read and write these three tags without linking against this fork or
knowing it exists. No known implementation of this spec exists outside
this fork today. That is expected at this stage. The spec is written to be
the stable integration surface a future tool builds against, not proof
that one already has.
