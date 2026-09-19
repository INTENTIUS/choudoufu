# `plan -adoption-only`

During a migration the question is which live resources this estate can
claim. A plan answers it, but in pieces, spread across three sections that
are each about something else and surrounded by a report whose size is set by
the provider's type count rather than by the estate. Measured on a generated
55-resource terralith at commit `e1dec69cef` (2026-08-30, #587), the sections
carrying an adoption path were 5.6% of 2,885 lines; at 205 resources they were
5.5% of 7,649. The admission table has grown since (see the [readiness
tiers](https://intentius.io/choudoufu/docs/use/compatibility/#readiness-tiers) table for
the current type count), so a fresh plan's line count will not match these
two exactly; the ratio is the point, not the byte count.

`choudoufu plan -adoption-only` (or `choudoufu live-plan -adoption-only`)
prints that question and nothing else. Every declared instance lands in one
of two halves:

- **Identity by declaration.** The provider's schema for the type has no tags
  argument, so the resource carries no ownership marker and never will: its
  identity is composed from its own declaration and from parents that do
  carry markers. Nothing is adopted here, and nothing is written here. On a
  real estate this is routinely the larger half - on the generated terralith
  it is 41 of 79 instances at scale 1, all of them
  `aws_iam_role_policy_attachment`, `aws_route53_record` and
  `aws_iam_role_policy`.
- **Identity by marker.** Split into what this estate already owns, what a
  tag write would claim (with the values, and a command where the type has
  one), what needs a marker but has no live resource to offer, and what
  another estate holds.

Warnings are compacted: each is printed as one line, its summary with a
count when the same summary recurs. A heading says how many there were and
that the same command without `-adoption-only` shows them in full. Errors
are never touched. Against `live/e2e/estate-block` plus an IAM role and its
inline policy on the pinned emulator at commit `e1dec69cef` (2026-08-30,
#587), a plain plan was 926 lines and the adoption-only run of the same
estate was 53 lines. **Stale**: since `09d180f921` an ordinary plan prints
fewer sweep warnings, and the line counts have not been re-measured (see
[what a plan
costs](https://github.com/INTENTIUS/choudoufu/blob/main/live/costs/plan-cost.md#when-the-native-leg-is-narrowed-and-when-it-is-not)).

The mode changes what is printed, and since `09d180f921` it also changes what
is done. The live reads and the plan are the same, and every verdict in the
ledger is the one an ordinary run would have printed. The sweep is not the
same: `-adoption-only` is what turns the estate-wide sweep's account-inventory
question **on**, so it enumerates every admitted type this estate has no
evidence of ever having used, and an ordinary plan of an adopted estate does
not. On the 79-instance terralith that is 710 API calls against 157, about
4.5x. It is also the flag a migrating operator is told to reach for, which is
correct, because during a migration the account-wide question is the point.

Budget for the wider run.
[What a plan costs](https://intentius.io/choudoufu/docs/model/plan-cost/) has the split, the
conditions under which an ordinary plan narrows, and
`TOFU_LIVE_COLLECT_UNCLAIMED` for asking or declining the question
independently of this flag.

It needs a `live` block; a state-backed plan refuses it.

Identity resolution and marker stamping run through the plan-node seam
(GitHub issue #388) by default. It tries the record, then the marker index,
then the provider's identity schema over the plan's own evaluated
configuration, at the same graph node where stock plans a resource.
`CHOUDOUFU_NODE_RESOLVE=0` in the environment that runs a plan or apply
opts back out to the older pre-walk static evaluator and HCL-rewriting
stamp. That path still ships and is scheduled for retirement, so the
variable exists for an estate the node path does not yet handle. It is a
build-migration switch and belongs in the environment that invokes the
binary, never in a `live` block.

