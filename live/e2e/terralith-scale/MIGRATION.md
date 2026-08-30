# The migration measurement (#566)

The subject, per epic #546: can a real stock-Terraform terralith migrate to
choudoufu? This measures the real path end to end against `tools/terralith-gen`
(#564) at scale 1 (55 resources) and scale 4 (205 resources) — the two tiers
#564's own PR proved apply/destroy at. #565 (floci's ceiling) had not reported
when this ran, so these two tiers are what's trustworthy; nothing here should
be read as generalising past 205 resources.

Every run is against a dedicated `floci` container (`ghcr.io/lex00/floci`,
the pin in `live/floci-image`), timed with the shell's `time` builtin around
the whole process, wall clock (`real`). Each tier's container was destroyed
before moving to the next — teardown by discarding the whole emulator rather
than a resource-by-resource destroy, since each tier's floci instance is
private to this measurement.

## Headline

`choudoufu live-import` — read the state file once (ratify), then stamp with
`-approve` — is what actually completes the migration. The plan-based manual
loop `docs/use/migrate.md` documents (`choudoufu plan` → read `Adoptable` →
paste tag-write commands → replan) leaves real gaps at this scale; see
"The manual path's blind spots" below. **Reported ratio, not asserted: 0% of
this estate needed a hand-typed marker.** Every resource that needed a marker
got one from a single `-approve` run; the rest need no marker at all because
their identity is untaggable and composes from an already-stamped parent.

The predicted for_each/count bottleneck (`docs/use/migrate.md`: "a pre-existing
unmarked resource is never offered for adoption" for an indexed instance)
**does not fire for this generator, and that is a scoping limit on this
measurement, not a finding about choudoufu.** `tools/terralith-gen` emits zero
`for_each` and zero `count` meta-arguments anywhere (`grep -rn 'for_each\|count
=' tools/terralith-gen/*.go`, excluding tests: no matches) — every one of its
"many instances" is a separately named block (`team_0000`, `team_0001`, ...,
`rec_0000`, `rec_0001`, ...). A real terralith's team/record fan-out very
plausibly *would* use `for_each` over a map, which is exactly the shape the
doc's warning targets. This measurement cannot speak to that case; it is a
gap in the generator, not something this run resolves.

## Pipeline, both tiers, every number timed

| Step | Scale 1 (55 resources) | Scale 4 (205 resources) |
|---|---|---|
| `terraform apply` (stock, real state) | 36.5s, 55 added | 82.6s, 205 added |
| `choudoufu plan`, pre-migration (establishes the ratio) | 7.5s | 2.6s |
| `choudoufu live-import` (ratify, read-only) | **1.475s** | **1.479s** |
| `choudoufu live-import -approve` (stamp) | **33.1s** | **127.6s** |
| `choudoufu plan`, post-migration | 273.6s (4:33.6) | 680.5s (11:20.5) |
| Final plan empty? | **No — 3/55 unresolved** | **No — 9/205 unresolved** |

Ratify and stamp are genuinely different costs, as the issue predicted: ratify
is close to flat between tiers (~1.48s both times — dominated by provider
startup and a batch of reads, not by resource count in this range) and cheap;
stamp is roughly linear in what gets stamped (26 resources → 33.1s ≈ 1.27s/
resource; 89 resources → 127.6s ≈ 1.43s/resource), because each stamp is a
live tag-write API round trip against floci, one per resource, not batched.

**Unflattering and worth its own line: the post-migration plan is dramatically
slower than the pre-migration one** — 36x slower at scale 1 (7.5s → 273.6s),
262x at scale 4 (2.6s → 680.5s) — and scaled worse than resource count alone
would predict (3.7x more resources, 2.5x more wall time, but off two very
different bases). CPU stayed under 2% throughout the slow runs (`docker stats`
during the scale-4 post-migration plan), so this reads as network/API-latency
bound — each of the now-tagged resources gets read and diffed individually
against floci — not CPU-bound. This is a day-2 planning cost, not a migration
cost, but it showed up inside the acceptance criterion ("plan again, require
empty") and is too large to leave unreported. It was not this issue's job to
diagnose further; #565/#546E own the emulator-cost question.

## The automatic-vs-hand-written ratio

Measured via `choudoufu live-import`'s own ratification report (read-only,
before any write):

| | Scale 1 | Scale 4 |
|---|---|---|
| VERIFIED + DRIFTED (eligible for stamping — needs a marker) | 26/55 (47.3%) | 89/205 (43.4%) |
| UNTAGGABLE (no tags argument in the provider schema — needs no marker, composes from an already-stamped parent) | 29/55 (52.7%) | 116/205 (56.6%) |
| Needed a **hand-typed** marker | **0** | **0** |

The 0% holds because `live-import` reads every instance's identity straight out
of the state file — role ARNs, policy ARNs, zone IDs, everything — so it never
hits the "content match only considers instances with no index or key" wall
`docs/use/migrate.md` warns about. One `-approve` run stamped all 26 (scale 1)
/ 89 (scale 4) eligible resources; nobody wrote a `tofu-address` string by
hand. The untaggable 53–57% (`aws_iam_role_policy`, `aws_iam_role_policy_
attachment`, `aws_route53_record` — confirmed via the ratification report's
own "no tags argument in the provider's schema" reason on every line) need no
action at all: their identity is a composite of already-stamped parents
(role name + inline-policy name; role name + attached ARN; zone ID + record
name + type), matching `docs/use/migrate.md`'s "What has no adoption path"
section.

## The manual path's blind spots (why live-import is the real answer)

Ran `choudoufu plan` against the same freshly-delete-stated, live-block-added
scale-1 estate *before* running live-import, to see what the plan-based
`Adoptable`/`Unowned` loop alone would have required:

- **13 resources** (all `aws_iam_role` and `aws_iam_instance_profile`) landed
  in `Unowned`, `[ADOPTABLE]`. No per-resource copy-paste command — IAM has
  its own tagging call per type — but bulk-adoptable in one shot via
  `policy { declared_untagged = "adopt" }`.
- **4 resources** (`aws_vpc`, `aws_subnet`, `aws_security_group`,
  `aws_route53_zone`) landed in `Adoptable` via content matching. Three of
  the four (vpc/subnet/sg) get a ready-to-paste `aws ec2 create-tags`
  command; the zone gets marker values and no command (Route53 has its own
  tagging call, undocumented by the plan itself).
- **7 resources** (6 `aws_iam_policy`, 1 `aws_ecs_task_definition`) got **no
  adoption path anywhere in the plan output** — not `Adoptable`, not
  `Unowned`. Each just carries a `[NEEDS_DISCOVERY]` note buried in the
  42-entry "Not read from the live system" list explaining that IAM mints
  the policy's own ARN at create time, so there is no name-derived import
  identity, but that it *is* "taggable and listable... recoverable by
  ordinary tag-filtered list" — with no tool-provided ARN, no tagging
  command, nothing actionable printed. A user relying on the plan-based loop
  alone would have to independently discover these 7 needed markers, find
  their live ARNs themselves (`aws iam list-policies`, `aws ecs describe-
  task-definition`), and hand-write the tag calls.
- Those 7 cascade: the 6 `aws_iam_policy` block the 6
  `aws_iam_role_policy_attachment.*_custom_attach` resources
  (`[PARENT_UNAVAILABLE]`), which resolve for free once the policy is
  stamped by some other means.

So the plan-based loop alone gets 17/55 resources to a *documented* adoption
path (13 bulk-adoptable + 4 content-matched) and leaves 7/55 (12.7%) with a
genuine silent gap — not flagged as blocking, not offered a command, easy to
miss at a glance. `live-import` (using the same state file) closes all of
this in one bulk operation with no gap, which is the whole reason it's the
issue's headline rather than the plan-based loop.

## Is the `Adoptable` report reviewable at this size?

**Marginally, and the noise floor is the reason, not the resource count.**
The `Adoptable` + `Unowned` sections themselves are short and readable — 4 and
13 entries at scale 1, 4 and 52 at scale 4, each entry self-contained (what
matched, what to run). The problem is what surrounds them: the full plan
output is 2,885 lines at scale 1 and 7,649 lines at scale 4, and the
`Adoptable`+`Unowned` block is 161 and 422 of those respectively — **5.6% and
5.5% of the output, at both tiers.** The other ~94% is a per-type
"estate-wide sweep" over the whole ~525-type AWS provider surface: 40
`[OBJECT_UNTAGGED]` entries (each 8–9 lines of a provider API error for a type
this estate never declares, e.g. `aws_workspaces_pool`, `aws_securityhub_
connector_v2` — real services floci doesn't emulate, nothing to do with this
estate) plus a `Not swept for removal: ~524 resource types` list. That floor
is roughly fixed regardless of estate size (it's bounded by the provider's
type count, not this estate's), while the actionable signal grows with the
estate — so the ratio should improve at larger scale, but at both tiers
tested here a reader has to scroll past about 18 lines of cross-account sweep
noise for every 1 line about their own estate. Excerpt, scale 1 (verbatim,
the whole `Adoptable` section):

```
Adoptable: 4 live resources matches a declared resource

Each of these matches a declared resource that discovery could not find,
exactly, on the arguments that identify that resource type. None of them was
bound: ownership is the tofu-estate and tofu-address tag pair and nothing
else, so claiming one is a tag write you make on purpose.

  aws_route53_zone.main <- aws_route53_zone Z6ULYQAYZAD0GR7 (tl1.terralith.test)
      matched on: name=tl1.terralith.test
      or write tofu-estate=tl1-terralith and
      tofu-address=aws_route53_zone.main onto it with any tool that honors
      live/MARKERS.md.
  aws_security_group.ecs <- aws_security_group sg-625dfa25c07ed54c9 (tl1-ecs-sg)
      matched on: name=tl1-ecs-sg
      adopt with: aws ec2 create-tags --resources 'sg-625dfa25c07ed54c9' --tags 'Key=tofu-estate,Value=tl1-terralith' 'Key=tofu-address,Value=aws_security_group.ecs' --region 'us-east-1' --endpoint-url 'http://127.0.0.1:4747'
      ...
```

Verdict: the sections that exist to be read are reviewable on their own; the
report they're embedded in is not, because nothing in the plan's own structure
separates "your estate" from "the rest of the provider's type surface." A
`choudoufu plan --adoption-only` (or similar filtered view) would fix this
without touching the underlying data.

## An unresolved defect found along the way

The final plan was **not** empty at either tier: 3/55 (scale 1) and 9/205
(scale 4) resources never converge, always the same family —
`aws_ecs_cluster` plus, per service, `aws_ecs_service` and `aws_ecs_task_
definition`: 1 cluster + (N services × 2) = 1 + 2 = 3 at scale 1 (1 service),
1 + 8 = 9 at scale 4 (4 services) — matches exactly. The plan reports `[ABSENT]`/`[PARENT_UNAVAILABLE]` ("the provider reports no
aws_ecs_cluster exists with identity 'tl1-cluster'") and proposes creating a
duplicate, even though `live-import` already stamped it — confirmed via the
stamp report and independently via `aws ecs describe-clusters`, which shows
the live object correctly tagged (`tofu-estate=tl1-terralith`, `tofu-
address=aws_ecs_cluster.main`) with a complete ARN including the account ID
(`arn:aws:ecs:us-east-1:000000000000:cluster/tl1-cluster`).

Root-caused at scale 1: `versions.tf`'s `skip_requesting_account_id = true`
(terralith-gen's own provider block, a common "point stock Terraform at a
local/test AWS endpoint" setting) is what breaks it. Toggling it to `false`
and re-running a targeted plan against the same live, already-stamped ECS
resources (`choudoufu plan -target=aws_ecs_cluster.main -target=aws_ecs_
service.svc_0000 -target=aws_ecs_task_definition.svc_0000`) produced `No
changes. Your infrastructure matches the configuration.` Some internal ARN
construction for ECS cluster/service/task-definition identity resolution
falls back to an empty account-ID segment when STS-based account resolution
is skipped, producing an ARN (`arn:aws:ecs:us-east-1::cluster/tl1-cluster`)
that doesn't match the live object's real ARN — even though ordinary provider
reads (`aws ecs describe-clusters`, and the tag write itself) use the correct
one throughout. This is a choudoufu identity-resolution defect, not a floci
fidelity gap: raw AWS CLI calls against the same floci container return
correct, complete ARNs.

Not fixed here — out of this issue's scope (a measurement, not a fix) — and
not re-verified at scale 4 (root-caused once, at scale 1, and the scale-4
residual count matches the predicted formula exactly, which is why this
report treats it as confirmed rather than re-running the toggle experiment a
second time). Filed as #572.

## What I did NOT verify

- **Scale beyond 205 resources.** #565 had not reported when this ran; these
  two tiers are the ones #564 itself proved apply/destroy at.
- **The `for_each`/`count` adoption blind spot `docs/use/migrate.md` warns
  about.** `tools/terralith-gen` emits none, so this measurement cannot
  exercise it. A generator change to fan teams or records out via `for_each`
  would be a different, and per the issue's own framing possibly more
  important, measurement.
- **Completing the manual plan-based adoption loop end to end.** I read what
  it would require (13 bulk-adoptable, 4 content-matched, 7 with no guidance)
  but did not execute the individual/bulk tag-write commands it prints —
  I used `live-import` for the actual working migration and only inspected
  the plan-based path to characterize its gaps.
- **The ECS identity-resolution defect at scale 4**, or against real AWS —
  root-caused once, at scale 1, against floci only.
- **`golangci-lint`, `just ci`.** No code changed in this checkout; this is a
  measurement document only.
- **Whether the post-migration plan's 36x/262x slowdown is a floci artifact
  or would reproduce against real AWS.** CPU stayed near-idle throughout, so
  it looks network/latency-bound, but that is an inference from `docker
  stats`, not a live-AWS confirmation.
