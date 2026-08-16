# Marker spec

Spec version 1

This file is the format for live resource markers, the ownership records
this fork keeps on resources instead of in a state file. It is the only
integration surface external tools rely on: this document is a contract,
and nothing else about the mode's internals is.

A marker is an ownership record carried on the resource itself, as AWS
resource tags. There is no side channel, no registry, and no shared library.
If a tool reads and writes these three tags according to the grammar below,
it can identify, adopt, and safely modify resources that belong to a
marker-managed estate, with no dependency on OpenTofu itself.

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

These limits come from AWS, and everything
below is designed to fit inside them.

- A tag value holds at most 256 Unicode characters.
- A tag key holds at most 128 Unicode characters. This is not a concern
  here, since all three keys are short, fixed strings.
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

**Grammar vs. current builds.** The `module.` segment below is already
implemented: identity resolution, projection, discovery, stamping
and the rename all traverse `cfg.Children`, and a `tofu-address` value
carries the full module-qualified address for a resource inside a static
module tree or a `for_each`-keyed module call with statically-evaluable
keys, at any nesting depth (issue #59, phases 1-2 / "59b"/"59c"). The one
segment this grammar allows but no build ever produces is a `count`
instance key on a `module.` segment: a module block expanded with `count`
is refused outright before anything reads the live system, permanently,
because the position-based renumbering it causes is exactly the ambiguity
a `tofu-address` marker exists to remove (`RuleChildModule` in
`internal/live/lint/child_module.go`, and `live/LIMITATIONS.md`'s
"child-module" entry). A resource inside a `for_each`-keyed module's
instances is not auto-written even though its address is supported: stamping
cannot inject a marker into a shared configuration body, so that address is
built by hand instead. `live/LIMITATIONS.md` records this as the "keyed
module" behavioral limit, and the concept page's "Modules" section has the
idiom.

The unescaped grammar, in informal EBNF matching OpenTofu's address
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
- `module.subnets[2].aws_subnet.this` (spec-only: a count-expanded module
  is refused permanently, see above)

### Escaping rule

AWS tag values cannot contain `[`, `]`, or `"`. The escaping is a
substitution over the whole address string, applied before writing the tag
and never reversed by any code path that only needs to *compare*.
Comparison is always "escape the known config address, compare strings,"
never "decode the tag blind."

1. Escape the content of every instance key first (see "for_each key
   escaping", directly below) - this is a no-op for a `count` index, which
   is only ever digits.
2. Replace every `[` with `:`.
3. Delete every `]`.
4. Delete every `"`.

A `:` in an escaped value therefore always means an index starts there. The
index runs to the next `.` or the end of the string - which step 1 is what
guarantees: an instance key's own `.` and `:` are never raw by the time
steps 2-4 run.

| Unescaped | Escaped (`tofu-address` value) |
|---|---|
| `aws_vpc.this` | `aws_vpc.this` |
| `aws_subnet.this["a"]` | `aws_subnet.this:a` |
| `aws_eip.this[2]` | `aws_eip.this:2` |
| `module.subnets["a"].aws_subnet.this` | `module.subnets:a.aws_subnet.this` |
| `aws_subnet.this["alice.smith"]` | `aws_subnet.this:alice@dsmith` |
| `aws_subnet.this["at@sign"]` | `aws_subnet.this:at@@sign` |

A single tag value holds at most 256 characters (the AWS hard cap on tag
values). An escaped address that does not fit is carried across several
tags instead (see "tofu-address continuation tags", directly below), up to
a total of 1024 characters. Past that wider ceiling, the original rule
still holds without exception: an address that does not fit is a lint-time
error, not a truncation. Silently truncating an ownership key is worse than
refusing to admit the resource.

### for_each key escaping

A `for_each` instance key may contain any character AWS allows in a tag
value: letters and numbers representable in UTF-8, space, and
`+ - = . _ : / @` (`internal/live/markerkey`, `RuleForEachKey`). Three of
those characters - `@`, `.` and `:` - would collide with the address-level
escaping above if embedded raw, so a key's own instance of any of them is
substituted first, in this order:

1. Every `@` becomes `@@`.
2. Every `.` becomes `@d`.
3. Every `:` becomes `@c`.

The order is load-bearing: doubling `@` first guarantees that every `@`
steps 2 and 3 introduce is never itself mistaken for one that needs
doubling. Reading it back reverses the same three substitutions in a single
left-to-right scan, not as three independent reverse replacements, because
two adjacent escaped characters (an escaped `@` immediately followed by an
escaped `.`, say) have to be read as two two-character units rather than
reprocessed as if the first's output could be the second's input.

Issue #178 introduced this: before it, `.` and `:` were excluded from a
`for_each` key entirely rather than escaped, because they collided with the
address-level rule and nothing decoded a key on its own to tell a literal
`.` apart from a segment separator. `@` was always admitted and was never
escaped - it is legal in a tag value and does not collide with anything the
address-level rule touches on its own - which is exactly what makes it the
one character both grammars admit but escape differently. See "for_each key
migration", below, for what that means for a marker a run wrote before this
issue landed.

### for_each key migration

Every candidate character this escaping could have used as its leader was
already legal, unescaped, inside a `for_each` key before issue #178 - the
whole admitted set before this issue was `+ - = _ / @`, and every one of
those five was already legal on the wire. `@` is the character issue #178
chose, which means a `for_each` key containing `@` is the one shape where a
marker a prior run stamped differs from what this run would stamp for the
same key: `aws_subnet.this["at@sign"]` escaped, before issue #178, to
`aws_subnet.this:at@sign` (see the table above; `@` passed through
untouched), and escapes now to `aws_subnet.this:at@@sign` (doubled). A key
containing only `.` or `:` cannot have this problem, because both were
refused by lint before this issue and so never reached a live marker.

This fork's compatibility answer is not a spec version bump (see
"Versioning", below, for why: every marker written under spec version 1
still parses and still names the instance it always named). It is that the
DECLARED side of every ownership comparison computes both the current
escaping and the pre-#178 one for the same address, and accepts either as a
match, while every write always uses the current escaping. A resource whose
live marker still reads `aws_subnet.this:at@sign` binds to
`aws_subnet.this["at@sign"]` exactly as it always did; the address a fresh
`live-mv`, adoption, or stamp writes for that same instance is
`aws_subnet.this:at@@sign`, and the next comparison recognizes that too.
Nothing in this fork rewrites an existing marker to the new escaping on its
own - a live resource carries whichever grammar last wrote it until
something explicitly restamps it.

**A residual ambiguity, not a guarantee both ways.** Decoding a marker back
into an address (removal planning's display label, `markerTypeOf`'s
best-effort type guess) is not the same operation as comparing it against a
known declared address, and it cannot always tell which grammar produced
the bytes it is reading: a pre-#178 key that happened to contain the
literal two-character sequence `@d`, `@c` or `@@` is indistinguishable, in
the tag text alone, from a post-#178 key whose escaping produced the same
bytes. Nothing that identifies or acts on a live resource depends on
resolving that ambiguity correctly - see `markers.UnescapeAddress`'s doc
comment - so the worst it can do is mislabel a resource in a message, never
bind, adopt, or destroy the wrong one.

### `tofu-address` continuation tags

Deep module trees and long `for_each` keys can produce an escaped address
longer than one 256-character tag value holds. Instead of refusing every
such address outright, `tofu-address` carries the first 256 characters and
up to three more tags (`tofu-address-2`, `tofu-address-3`,
`tofu-address-4`) carry the rest, in order, 256 characters at a time. A
reader concatenates `tofu-address`, then `tofu-address-2` if present, then
`tofu-address-3`, then `tofu-address-4`, and the result is the one escaped
address that would not fit in a single tag. This is the only sanctioned way
to read a split address. The continuation tags are never meaningful on
their own, individually or out of order.

This raises the effective limit to 1024 characters (four tags of 256), and
the limit remains bounded. An address that does not fit in four tags is still a
lint-time error (RuleOverlongAddress, `internal/live/lint/overlong_address.go`)
for the same reason the original 256-character refusal existed:
truncating an ownership key is worse than refusing to admit the resource,
and a fourth tier of continuation would just move the same question further
out without answering it. Four tags is deliberately generous headroom
against the 50-tag-per-resource AWS limit (minus whatever tags the
configuration's `tags` block already uses) while staying a small, fixed
number instead of a knob a configuration can turn.

A resource whose address fits in 256 characters (the overwhelming common
case, and every marker written before this addition existed) carries only
`tofu-address` and no continuation tags at all, exactly as before. Nothing
about a short address changes.

**Reading a corrupt chain.** A continuation tag can only exist because
something wrote the whole set together. The three tags below `tofu-address`
are never independently meaningful. A tag map where `tofu-address-3` is
present but `tofu-address-2` is not (the middle of the chain deleted by a
hand edit, a tag policy misfire, or two racing writes) cannot be
concatenated into anything, and per "Ownership semantics" below it is
malformed: reported loudly and by name, never silently read as the address
up to the gap and never treated as unowned.

**Writing one.** A tool that stamps markers and encounters an address over
256 characters splits it the same way: the first 256 characters into
`tofu-address`, the next 256 into `tofu-address-2`, and so on, stopping at
whichever tag holds the last of the address (a shorter final chunk is
normal and is not padded). A tool that only ever writes addresses under 256
characters never has to think about this section at all.

**Known limitation, left for the lint layer to enforce.** The escaping is
lossy in one way, by design, and ambiguous in one further way as a residue
of issue #178's migration.

- A bare integer `for_each`/`count` index and a quoted string index with
  the same digits collide. `this[2]` and `this["2"]` both escape to
  `this:2`. This is harmless in practice because a single resource block
  uses either `count` or `for_each`, never both, so the two never compete
  for the same address.
- A `for_each` key outside the AWS-allowed tag character set cannot be
  written as a marker at all, escaping or not, and is rejected by lint. A
  key inside that set - including one containing `.`, `:` or `@` - always
  escapes, per "for_each key escaping" above; there is no character left
  that the escaping rule refuses to handle.
- Reading a marker's key back (rather than comparing it against a known
  declared address) carries the narrow, coincidental ambiguity "for_each
  key migration" describes: a pre-#178 key that happened to contain the
  literal bytes `@d`, `@c` or `@@` decodes the same way a post-#178 key
  whose escaping produced those bytes would. Nothing that binds, adopts, or
  destroys a resource depends on resolving it.

Unescaping. Removal planning turns a marker back into an address, which the
escaping rule supports for every value a lint-clean configuration produces
today (the `.`/`:` refusal this paragraph used to describe retired with
issue #178; see "for_each key escaping" above). A key of all digits is
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
unrecoverable. The guarantee is therefore as follows. The assignment
high-water mark is the highest slot among the block's live resources. A
slot is never reused while any resource holds it, and never duplicated
within a set. A slot whose resource has been deleted may be assigned again
later.

The format is an unsigned base-10 integer, ASCII digits only, no leading
zeros (the value `0` is written as `0`, not `00`). Slots are assigned from
a monotonic counter per `count` resource block (not per instance address,
since an instance's address contains the index a slot is deliberately
independent of), starting at `0`. The first instance of `aws_eip.this`
gets slot `0`, the second gets slot `1`, and so on. New instances are
assigned slots above the live high-water mark. Ten digits (up to
4294967295) is the ceiling. No realistic `count` approaches it.

For a `count` instance carrying a slot, the slot is what binds it to a
declared instance, and the address follows. The k-th lowest live slot
binds to index k. `tofu-address` remains mandatory and remains the full
indexed address, but a value naming a different index than the slot bound
to is stale, never a rival claim. It is repaired by the next plan's ordinary
tag write, and it is never a collision. Scale-down deletes the highest
slots, compared numerically, so every survivor keeps the index it already
occupied. That is the no-churn rule.

Slot values are compared numerically, not lexicographically, because they
are carried as strings in a tag. `"9"` is a lower slot than `"10"` even
though it sorts after it as a string. Any tool implementing the scale-down
rule ("surplus deletes the highest slots") must parse before comparing.

`tofu-slot` is independent of `tofu-address`. A plain rename that does not
change cardinality leaves slot assignments untouched. Only a
change in the number of live instances assigns or retires slots.

## Ownership semantics

- A resource carrying a `tofu-estate` tag belongs to that estate. The
  value is the entire ownership claim, and there is no secondary
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
  gap in it (a `tofu-address-3` present while `tofu-address-2` is not) is
  the same malformed case: it cannot be concatenated into anything, so it
  is reported, never read as the address up to the gap.
- **How markers are read back has a timing property.** Two
  reads find them, and only one of the two is eventually consistent. Binding
  a declared resource to its live object goes through a per-type listing
  against the service's own API, so nothing about a marker written moments
  ago can be missed there. The estate-wide sweep for resources this estate
  owns but no longer declares may instead go through the Resource Groups
  Tagging API's `GetResources`, filtered on `tofu-estate`, and that index is
  eventually consistent: a resource whose tags have not yet propagated is
  simply not returned. The consequence is bounded and is in the safe
  direction - a sweep can be a run behind, so an orphan may be reported one
  run late. It cannot cause a marker to be lost, and it cannot make a
  declared resource look absent, because that question is never asked of the
  tag index.
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
tag write is the move operation: there is no state to edit, no `moved`
block to author, and no two-step migration. The old
address is simply gone from the tag the instant the new one is written,
because a single tag value cannot hold both.

`choudoufu live-mv <old-address> <new-address>` performs exactly this. It escapes both addresses, finds the live resource whose
`tofu-address` equals the old escaped value within the target estate, and
overwrites it with the new escaped value in one tag-update call. After it
runs, a plan against the old address finds nothing (it was never a delete,
since the resource was never bound to "old" as far as anything after the
rewrite is concerned), and a plan against the new address finds the
resource already bound. The result is zero churn, with no special-casing
of renames in the plan engine.

Old markers never linger. There is exactly one `tofu-address` value on a
resource at any time, and after a rewrite that value is the new address,
not a history of addresses it once had. This holds for continuation tags
too: a rename onto a shorter address writes fewer `tofu-address-*` tags
than the old one carried, and a rename tool is expected to delete whichever
continuation tags the new address does not reach, so that no stale
tag remains for a later read to concatenate onto the new value.

## Versioning

The header at the top of this file ("Spec version 1") versions this
document, not the resources it describes. There is no
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
the other way instead: a reader built only against spec version 1's single
`tofu-address` tag will silently read a new split marker's first 256
characters as the whole address instead of erroring. That is a real gap for
anything that has not been updated to read continuation tags, and it is
accepted without a version bump, because the
definition above is about old data under new code, not new data under old
code. The versioning number cannot help with the latter no matter which way
it is called.

A change is breaking (version bump required) if it invalidates that
guarantee. Renaming a tag key, changing the escaping rule, changing what
absence of a tag means, or narrowing a grammar so previously valid values
become invalid all qualify. A breaking change requires a coordinated
rewrite pass over every live estate before tools built against the new
version can trust what they read, and the version number here is what lets
a tool detect that an estate's markers predate what it understands, so it
can refuse to guess instead of misreading them.

**Issue #178 widened the escaping rule and did not bump the version.**
"Changing the escaping rule" above is the general case, where a version
bump is the only honest option because old markers would otherwise be
misread. Issue #178 is the narrower case the additive definition already
covers: it admits `.` and `:` into a `for_each` key rather than narrowing
anything, and every marker written under spec version 1 - both before and
after this issue - still parses under the current code and still names the
instance it always named, per "for_each key migration" above. The one
place the two escapings can disagree (a key containing `@`) is handled by
comparing both on the declared side rather than by asking a reader to know
which grammar wrote what it is looking at, which is what makes a version
bump unnecessary rather than merely inconvenient to add. A future change
that made a *reader* need to know which grammar wrote a marker - rather
than a *writer* needing to compute both to find it - would not get the same
pass.

## Interop

This file is the entire contract. Nothing about the mode's Go internals
(the projection builder, the lint rules, the identity resolution) is
required for another tool to participate. Any tool that reads and
writes `tofu-estate`, `tofu-address`, and `tofu-slot` per the grammar
above, on the resource types it manages, can discover, classify, and
safely mutate resources in a marker-managed estate.

This grammar is designed for external adoption. Any tool, in any language,
can read and write these three tags without linking against this fork or
knowing it exists. No known implementation of this spec exists outside
this fork today, which is expected at this stage: the spec is written to
be the stable integration surface a future tool builds against.

## Granting an estate

An estate is inherited by being granted access to it, and this is the grant.
The marker is an ordinary resource tag, so IAM can condition on it directly
through `aws:ResourceTag`, with no second permission model to keep in sync.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ActOnWhatTheEstateAlreadyOwns",
      "Effect": "Allow",
      "Action": [
        "ec2:TerminateInstances",
        "ec2:ModifyInstanceAttribute",
        "ec2:DeleteTags"
      ],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:ResourceTag/tofu-estate": "prod-networking"}
      }
    },
    {
      "Sid": "CreateOnlyIntoThisEstate",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances",
        "ec2:CreateTags"
      ],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:RequestTag/tofu-estate": "prod-networking"}
      }
    }
  ]
}
```

**Two statements, because creation and mutation are conditioned by different
keys.** `aws:ResourceTag` reads a tag off a resource that already exists, so it
governs everything the estate acts on. It cannot govern a create: there is no
resource yet to carry the tag, and a `RunInstances` under a `ResourceTag`
condition never matches. What the creating principal supplies is
`aws:RequestTag`, and conditioning on it is what makes the second statement a
grant to create *into this estate* rather than a grant to create anything.

The actions above are illustrative, not the full scope: a real
grant names the actions the estate's own types need, which
[`site/content/reference.md`](https://intentius.io/choudoufu/reference.html)
lists per stage, and the resource types the configuration declares.

**Handover is two IAM changes and no tag writes.** Attach that policy to the
receiving role, detach it from the sending one, and the estate has moved.
Nothing about the resources changes, no state is exported, and the two roles
never both hold it unless you want an overlap. The receiving team can list
what it inherited before running anything, with `aws resourcegroupstaggingapi
get-resources --tag-filters Key=tofu-estate,Values=prod-networking` and no
`choudoufu` binary.

**Splitting an estate is a tag rewrite, then two policies.** Rewrite
`tofu-estate` on the resources that are leaving, and the same statement with
the new estate name governs them. The split is a tag write and a policy
copy, and neither half moves.

### Which services this actually reaches

The condition key is evaluated per action, per service, and support is not
uniform. This roster is generated from AWS's own Service Authorization
Reference (`live/iam-reference.json`, `just iamref`), so it is checkable
rather than asserted.

<!-- iamref-gen:begin resource-tag-services -->
| Service | IAM prefix | Actions naming `aws:ResourceTag` |
|---|---|---|
| EC2 | `ec2` | 495 of 793 |
| ResilienceHub, ResilienceHubV2 | `resiliencehub` | 57 of 128 |
| SES | `ses` | 48 of 228 |
| AutoScaling | `autoscaling` | 42 of 68 |
| ECS | `ecs` | 37 of 81 |
| Kinesis | `kinesis` | 31 of 40 |
| CertificateManager | `acm` | 20 of 41 |
| CleanRooms | `cleanrooms` | 12 of 107 |
| ElastiCache | `elasticache` | 11 of 77 |
| CloudWatch | `cloudwatch` | 7 of 67 |
| SageMaker | `sagemaker` | 7 of 444 |
| WorkSpaces | `workspaces` | 7 of 101 |
| KafkaConnect | `kafkaconnect` | 2 of 18 |
| AuditManager | `auditmanager` | 1 of 62 |
| Batch | `batch` | 1 of 45 |
| CUR | `cur` | 1 of 12 |
| SSMQuickSetup | `ssm-quicksetup` | 1 of 14 |

17 of the 154 IAM prefixes this estate's admitted types reach name the key on at least one action. The remaining 137 are **unmeasured, not disproven**: the reference does not set out to enumerate every global condition key per action, and `lambda:GetFunction` lists none at all while Lambda does support tag-based authorization. Read this as the set a marker-scoped grant is known to bite on, never as its complement.
<!-- iamref-gen:end resource-tag-services -->

The asymmetry in that last sentence is the whole of how to use this section,
and it is the same caution the SCP below states about `aws:TagKeys`: a
statement that looks correct and silently does nothing for one service is
worse than no policy. Build a grant on the services above and it constrains
what you expect. Assume the ones absent from it are unreachable and you will
be wrong about several, because the reference is silent rather than negative.

For a service outside the roster, the reachable guarantees are the ordinary
ones: the account boundary, the resource types named in the policy, and the
region. Those are coarser than a marker, and they are what an estate not yet
covered by tag-based authorization inherits.

**This governs API calls, not tag survival.** A principal that cannot act on
an estate's resources can still, with tagging permissions elsewhere, remove
the markers that define it. That is the next section's subject.

## Protecting the markers

Markers are plain resource tags, and nothing about that grammar stops
anyone with tagging permissions from removing them: `aws ec2 delete-tags`,
a console "manage tags" cleanup, or an unrelated tag-hygiene automation
that untags whatever it does not recognize. What that costs depends on the
type.

For a client-named resource (`aws_s3_bucket`, `aws_iam_role`, the rest of
admission path 1) it is a nuisance and nothing worse: the next plan reports
the resource `[UNOWNED]` with the exact adoption command, because the
cloud's uniqueness constraint means a duplicate can never actually be
created under the same name. For a server-assigned resource (`aws_vpc`,
`aws_security_group`, and the rest of admission path 2's table above) the
marker is the only handle discovery has. Strip it off a live one and the
declared address it used to bind to looks exactly like a resource that was
never created: the next plan proposes CREATE, and unless something
intervenes, apply produces a second, functionally identical resource
sitting beside the orphaned first one. This section is about that case:
nothing about it looks like an error until the bill or the drift shows up.

### What actually stops it, checked rather than assumed

Two AWS Organizations mechanisms sound like they cover this. One does not,
and the other only partially.

**Tag policies enforce values, not survival.** Per AWS's documentation
([Tag policies](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_tag-policies.html),
[Enforce tagging consistency](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_tag-policies-enforcement.html)),
a tag policy "can specify that when the `CostCenter` tag is attached to a
resource, it must use the case treatment and tag values that the tag
policy defines," and enforcement mode "prevents noncompliant tagging
requests on specified resource types from completing." That is a check on
the value a tag is set to, run when a tag is written, on resource types the
feature explicitly supports. Nothing in that mechanism inspects a
`DeleteTags`-style call at all, and AWS says so directly: "Basic
compliance rules do not enforce tag compliance on resources that are
created without tags. This capability does not enforce missing tag keys."
A tag policy cannot be configured to block a tag from being removed,
because removing a tag is not the kind of event it evaluates. Do not rely
on one for this.

**SCPs can block the untagging call, but only inside the org, only in
member accounts, and only where the condition key is honored.** A service
control policy is a guardrail on what IAM principals in an organization's
*member* accounts can do
([Service control policies](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_scps.html)):
it never grants anything, it can `Deny` an action outright, and, per that
same page, it has no effect on the management account or on any
principal outside the organization. Denying the tag-removal actions for
the marker keys (`tofu-estate`, `tofu-slot`, and `tofu-address` together
with its `tofu-address-2` through `tofu-address-4` continuation tags),
with an exception for whichever principal runs `choudoufu`, is the closest
thing to a real backstop:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "DenyUntaggingMarkers",
      "Effect": "Deny",
      "Action": [
        "ec2:DeleteTags",
        "kms:UntagResource",
        "elasticloadbalancing:RemoveTags",
        "route53:ChangeTagsForResource",
        "acm:RemoveTagsFromCertificate",
        "states:UntagResource",
        "sns:UntagResource",
        "tag:UntagResources"
      ],
      "Condition": {
        "ForAnyValue:StringEquals": {
          "aws:TagKeys": ["tofu-estate", "tofu-address", "tofu-address-2", "tofu-address-3", "tofu-address-4", "tofu-slot"]
        },
        "ArnNotLike": {
          "aws:PrincipalArn": ["arn:aws:iam::*:role/choudoufu-automation"]
        }
      },
      "Resource": "*"
    }
  ]
}
```

The key list is every marker key this fork ever writes as of issue #71's
`tofu-address` continuation tags, not just the three literal keys a run
without a long address ever needs: `aws:TagKeys` in an SCP condition is a
set-membership check, with no prefix wildcard for "any `tofu-address-*`
key," so a policy that lists only the bare `tofu-address` leaves
`tofu-address-2` through `tofu-address-4` unprotected on any resource whose
escaped address needed them. Stripping just the continuation tags corrupts
the same ownership record as stripping `tofu-address` itself, since a
reader that cannot gather every chunk cannot reconstruct the address at
all. See "`tofu-address` continuation tags," above.

The eight actions in that statement are illustrative. The
exhaustive list is generated, because each service's tag-removal verb is
resolvable from botocore's service models the same way its tagging verb
already was, and whether AWS evaluates `aws:TagKeys` on it is resolvable
from the Service Authorization Reference.

<!-- iamref-gen:begin scp-untag-actions -->
127 tag-removal actions across this estate's services name `aws:TagKeys` in the Service Authorization Reference, so a `Deny` conditioned on it is evaluated for them. Each service's removal verb is resolved from botocore's own service models (`live/tag-verbs.json`), not written by hand.

<details>
<summary>The full action list, for pasting into the policy above</summary>

```json
"Action": [
  "acm-pca:UntagCertificateAuthority",
  "airflow:UntagResource",
  "amplify:UntagResource",
  "aoss:UntagResource",
  "app-integrations:UntagResource",
  "appconfig:UntagResource",
  "appflow:UntagResource",
  "apprunner:UntagResource",
  "appsync:UntagResource",
  "arc-region-switch:UntagResource",
  "athena:UntagResource",
  "auditmanager:UntagResource",
  "backup:UntagResource",
  "batch:UntagResource",
  "bcm-data-exports:UntagResource",
  "bedrock:UntagResource",
  "billing:UntagResource",
  "ce:UntagResource",
  "chatbot:UntagResource",
  "cleanrooms:UntagResource",
  "cloud9:UntagResource",
  "cloudfront:UntagResource",
  "cloudwatch:UntagResource",
  "codeartifact:UntagResource",
  "codecommit:UntagResource",
  "codeconnections:UntagResource",
  "codeguru-reviewer:UntagResource",
  "codepipeline:UntagResource",
  "codestar-connections:UntagResource",
  "codestar-notifications:UntagResource",
  "comprehend:UntagResource",
  "config:UntagResource",
  "controltower:UntagResource",
  "cur:UntagResource",
  "datapipeline:RemoveTags",
  "datazone:UntagResource",
  "detective:UntagResource",
  "directconnect:UntagResource",
  "dlm:UntagResource",
  "dms:RemoveTagsFromResource",
  "docdb-elastic:UntagResource",
  "dsql:UntagResource",
  "dynamodb:UntagResource",
  "ec2:DeleteTags",
  "ecr:UntagResource",
  "ecs:UntagResource",
  "eks:UntagResource",
  "elasticache:RemoveTagsFromResource",
  "elasticloadbalancing:RemoveTags",
  "elasticmapreduce:RemoveTags",
  "emr-containers:UntagResource",
  "emr-serverless:UntagResource",
  "events:UntagResource",
  "fis:UntagResource",
  "fsx:UntagResource",
  "gamelift:UntagResource",
  "geo:UntagResource",
  "globalaccelerator:UntagResource",
  "grafana:UntagResource",
  "guardduty:UntagResource",
  "imagebuilder:UntagResource",
  "internetmonitor:UntagResource",
  "invoicing:UntagResource",
  "iot:UntagResource",
  "ivs:UntagResource",
  "ivschat:UntagResource",
  "kafkaconnect:UntagResource",
  "kendra:UntagResource",
  "kinesisanalytics:UntagResource",
  "kms:UntagResource",
  "lambda:UntagResource",
  "lightsail:UntagResource",
  "m2:UntagResource",
  "medialive:DeleteTags",
  "mediapackage:UntagResource",
  "mediapackagev2:UntagResource",
  "memorydb:UntagResource",
  "network-firewall:UntagResource",
  "networkmanager:UntagResource",
  "notifications-contacts:UntagResource",
  "notifications:UntagResource",
  "oam:UntagResource",
  "observabilityadmin:UntagResource",
  "odb:UntagResource",
  "organizations:UntagResource",
  "payment-cryptography:UntagResource",
  "pipes:UntagResource",
  "qbusiness:UntagResource",
  "ram:UntagResource",
  "rbin:UntagResource",
  "rds:RemoveTagsFromResource",
  "redshift-serverless:UntagResource",
  "redshift:DeleteTags",
  "rekognition:UntagResource",
  "resiliencehub:UntagResource",
  "resource-explorer-2:UntagResource",
  "rolesanywhere:UntagResource",
  "route53-recovery-readiness:UntagResource",
  "route53profiles:UntagResource",
  "route53resolver:UntagResource",
  "rum:UntagResource",
  "s3files:UntagResource",
  "s3tables:UntagResource",
  "s3vectors:UntagResource",
  "sagemaker:DeleteTags",
  "scheduler:UntagResource",
  "secretsmanager:UntagResource",
  "securitylake:UntagResource",
  "servicecatalog:UntagResource",
  "servicediscovery:UntagResource",
  "shield:UntagResource",
  "sns:UntagResource",
  "sqs:UntagQueue",
  "ssm-contacts:UntagResource",
  "ssm-incidents:UntagResource",
  "ssm-quicksetup:UntagResource",
  "ssm:RemoveTagsFromResource",
  "states:UntagResource",
  "storagegateway:RemoveTagsFromResource",
  "synthetics:UntagResource",
  "transfer:UntagResource",
  "verifiedpermissions:UntagResource",
  "vpc-lattice:UntagResource",
  "wafv2:UntagResource",
  "workspaces-web:UntagResource",
  "workspaces:DeleteTags",
  "xray:UntagResource"
]
```

</details>

**2 do not name it, and these are where the warning above actually bites:** `route53:ChangeTagsForResource` and `securityhub:UntagResource`. The reference is silent rather than negative here, so this is not proof the `Deny` fails - but it is the difference between a statement checked and a statement assumed, and these are the ones to verify against the service's own reference page before relying on them.

27 further services have no removal verb resolved in `live/tag-verbs.json` at all, either because the service's model offers more than one candidate or none. They are absent from the list above rather than silently covered by it.
<!-- iamref-gen:end scp-untag-actions -->

Before deploying anything like it:

- **`route53:ChangeTagsForResource`** folds adding and removing tags into
  one call keyed by a "keys to remove" parameter instead of a dedicated
  untag action. It is one of the two actions above the reference does not
  name `aws:TagKeys` on, so verify it against that action's own reference
  page before trusting it rather than assuming the condition keys off it.

- **The management account and any standalone (non-Organizations) account
  are outside SCP reach entirely.** A principal there needs ordinary
  least-privilege IAM to protect the marker keys, because no
  organization-level guardrail reaches it.
- **Nothing here stops the resource from being deleted outright**, only
  its markers being stripped while the resource survives. Outright
  deletion is a different, already-handled case: the next plan's estate
  sweep reports the address as gone and proposes nothing, since there is
  no live resource left to warn about.

### The residual risk, and the last line of defense

Even a correct SCP leaves gaps: the management account, a standalone
account, a compromised or misused exemption for the automation principal,
a service whose untag action does not honor `aws:TagKeys`, or simply a
policy nobody has written yet. Prevention cannot cover every case, which
is why this fork does not rely on it alone.

At plan time, when a declared resource of an admitted type would be
created and the estate sweep saw one or more live resources of the same
type that this estate does not own, the plan runs the same content-match
machinery that offers adoption elsewhere (`internal/live/foreign`'s match
table and its one-to-one rule) against the declared configuration. On a
match it does not change what the plan does (the create may be
intended), but the create's entry in the plan gains a `[POSSIBLE
DUPLICATE]` warning, naming the matched live resource's ID and the exact
command that adopts it instead. A type with no content-match rule (a route
table, an EIP: nothing in their configuration distinguishes one from
another) still gets a generic warning when exactly one same-type unowned
resource exists, naming it the same way. Either way the warning sits
immediately above the plan diff itself, not buried in a report an operator
could plan past without reading. This is the guard that assumes the tags
will get stripped sometime, by someone, despite whatever policy is in
place, and catches it anyway.

Taken together: a tag policy cannot do this job at all, an
SCP narrows who can strip a marker and where, and the plan-time guard
catches it when someone does it regardless.
