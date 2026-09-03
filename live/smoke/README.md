# The smoke stack

Paste this to a coding agent (Claude Code or similar) and it will run the
whole thing for you:

```
Clone https://github.com/INTENTIUS/choudoufu, then do the following.

1. Confirm Docker is running (`docker info` must succeed) and the AWS CLI
   is installed (`aws --version`).
2. If Go is installed, skip this step. Otherwise pick the latest release
   tag from https://github.com/INTENTIUS/choudoufu/releases and
   export CHOUDOUFU_VERSION=<that tag> so the smoke runs a prebuilt binary.
3. From the repo root, run: just smoke import
4. Then run: just smoke greenfield
5. Report each step's verdict line as it prints, and each scenario's final
   PASS or FAIL line.

Exit code 0 means every claim held: an estate stood up by stock OpenTofu
survived losing its state file, and a brand-new estate carried its
ownership markers from the first create call. Non-zero names the step
that failed.
```

## What this is

One `docker compose` stack - the pinned floci emulator and the pinned
stock OpenTofu oracle - and one scenario per invocation, each tracing a
real user path with a verdict line per step. The harness is versioned
(`VERSION`, printed in every banner) so a report can name what measured it.

```
just smoke                # list scenarios
just smoke greenfield     # a new estate from nothing
just smoke import         # stock estate -> delete the state file -> adopt
just smoke full           # the comprehensive 15-step harness (~6 minutes)
```

## Scenarios

- **greenfield** - a live-block configuration, one plain apply: markers
  ride the create calls, the replan is empty, the state cache exists and
  is disposable, and `apply -destroy` removes exactly what was made.
- **import** - the migration path: the stock oracle (in its container)
  stands the estate up with a plain `terraform.tfstate`; the state file is
  deleted; the receipts are adopted with two CLI tag writes; the count
  pool takes its slot markers by the values the plan names; the estate
  plans empty from markers alone; one identity is asserted by value with
  the AWS CLI and no choudoufu in the loop.
- **full** - wraps `live/e2e/run.sh --expect 5`, the 15-step harness.

## Claim scenarios

Each claim scenario states one product claim up front, proves it against
the live stack while narrating why each step matters, asserts the
claim's honest boundary out loud, and carries a `BREAK=1` control that
manufactures the one corruption the claim cannot cover - passing only by
showing its own checks would have caught it.

- **no-silent-orphans** - *Claim 1: a resource this estate owns cannot
  fall out of its plans unnoticed.* A crash-shaped create (resource made,
  markers written, nothing ever recorded) and a deleted resource block
  both surface as named plan lines and are removed by an ordinary apply;
  the two types the sweep cannot recover are announced by the apply
  itself; and the same guarantee is proven for record-backed resources,
  whose deleted block surfaces from the record store's own List with no
  cloud involved. The BREAK control creates the resource unmarked - the one shape
  the claim excludes - and proves the naming check fails without the
  marker.
- **no-self-managed-locks** - *Claim 2: contention settles at the
  platform API, never in a lock this tool holds.* force-unlock refuses
  with the true reason (no lock exists to force); two simultaneous
  applies of the same client-named resource are refereed by the cloud's
  own uniqueness constraint with no lock ever taken; the loser's whole
  recovery is a clean re-plan; and the one unrefereeable race - a true
  duplicate of a server-assigned resource - surfaces as a named pair for
  a human to resolve with one delete. The BREAK control strips the
  winner's marker and proves the convergence check fails without it.
- **staleness-costs-reads** - *Claim 3: staleness costs reads, never
  results.* A cache holding dead ids (saved before a full
  destroy-and-recreate), an absent cache, and a fresh one produce
  byte-identical plans - against the cloud estate and against the
  record store, where the ancient cache holds a phantom resource that
  exists nowhere and still bends nothing; an out-of-band drift stays
  visible straight through a fresh cache (the #712 regression, pinned
  forever); and the
  one opt-in path, -refresh=false, demonstrably serves from the cache
  while losing it changes only work - with the honest footnote that
  today's wire savings are small until #692's vouch widening lands. The
  BREAK control drifts the live world and proves the three-way equality
  comparator can fail.
- **backend-sets-itself-up** - *Claim 4: the live backend sets itself
  up automatically when configured.* A live block with no storage
  declared gets a local record store the way stock implies a local state
  file - a .tofu-records directory appears beside the module at first
  use, sentinel already written; declaring record_store "ssm" {} is the
  entire cloud setup, and the store provisions its own sentinel into
  Parameter Store where any AWS tool can read it; none of stock's
  bucket/lock-table/IAM/migration ceremony exists to perform. The BREAK
  control makes only the SSM store unreachable (the provider stays
  healthy) and proves the run refuses by name instead of planning an
  empty-looking estate - the #693 failure class, permanently on watch.
- **recovery-is-a-rerun** - *Claim 5: recovery is a re-run, never
  surgery.* An apply that died after its first create call (resource
  made, markers stamped, run gone) recovers by being run again: the plan
  binds the crashed vpc by its marker, builds the rest around it, and
  duplicates nothing; then every local file is deleted and the next plan
  is still clean, with the narration noting the disposable cache is the
  one file allowed to hold attribute material. The BREAK control
  withholds the markers - the re-run must refuse to bind and build a
  second vpc, stock's crash behavior surfacing as the claim's boundary.
- **roundtrip** - *Claim 6: one command in, one file out.* A tagless
  stock estate is adopted by live-import (reads the state file once,
  stamps markers on what verifies), operated with its state file
  deleted, then handed back: the cache is copied to terraform.tfstate,
  the live block removed, and stock strips the marker tags and destroys
  the whole estate from the returned file. The BREAK control skips
  live-import - the plan must propose a duplicate estate, the
  documented quiet failure of migrations that flip the block on and
  bind nothing.
- **identity-is-a-tag** - *Claim 7: identity is a tag you can read and
  move.* Two estates share one account with nothing but their estate
  tags between them - both plan clean, neither ever names the other's
  resources; the plain AWS CLI's tagging API answers ownership with the
  tool absent; and a code rename settles as one live-mv tag rewrite
  with a clean plan after, where stock demands state surgery. The BREAK
  control renames the code but skips the retag - the plan must propose
  stock's destroy-and-recreate, proving the tag is the identity.
- **stock-when-you-need-it** - *Claim 8: stock behavior is the
  fallback, whole and exact - and the live backend's cost scales with
  your estate, not your account.*
  Choudoufu with the live block removed plans a state-backed estate
  against the pinned stock oracle, both under TF_LOG: filtered plan
  texts equal, request counts identical. Then the live estate stands up
  and twenty foreign resources appear in the account - the plan's
  request count must not move, because every read is estate-scoped. The
  BREAK control runs the choudoufu leg with the live block on: the
  measurement must show the difference, or the parity comparison
  compares nothing.
- **unchanged-is-free** - *Claim 9: unchanged is free.* The same
  -refresh=false plan runs under the default selective policy and under
  the reads="full" off switch: selective serves the vouched instances
  and measurably drops the request count, full serves nothing and pays
  every read, and the two outputs are byte-identical - the toggle
  prices the plan, never changes it. The record-backed half proves the
  record is the attestation: an out-of-band edit surfaces on the next
  default plan as a named reconvergence. The BREAK control overwrites
  the record with garbage - the run must refuse naming the exact
  address, never plan against improvised values.
- **cache-serves-the-whole-estate** - *Claim 10: the cache serves the
  whole estate.* On -refresh=false every converged instance is served
  from the state cache - server-assigned needs-discovery resources
  (VPCs, subnets, security groups) included, not just the schema-admitted
  slice - so one estate of a terralith plans without re-reading the
  cloud. A default plan still refreshes (the read is drift detection),
  and the serving is existence-vouched: the BREAK control deletes a
  resource out of band and the plan surfaces it, never serving a gone
  object from cache.
- **count-is-a-fungible-set** - *Claim 11: a count pool is a fungible
  set.* A `count` block's members are interchangeable, so each one is
  named by a `tofu-slot` marker rather than by its index. Scaling a pool
  of three down to two removes exactly one member and creates nothing.
  The middle survivor stays the same live object, where stock would
  renumber and rebuild the tail. The BREAK control deletes the local
  record, then strips one member's slot, and the plan must refuse the
  half-slotted set by name rather than bind the odd member by a guess.

- **carve-by-retag** - *Claim 12: carve by retag.* Needs Go. The pinned
  stock oracle stands up terralith-gen's scale-1 terralith (79 resources,
  one state file, no markers); live-import adopts it and the file is
  deleted; then a team of six leaves for its own estate through three
  runs of `live-mv -from-estate`, one tag write each, with its inline
  policy and two attachments following their parent unwritten; the ECS
  execution role leaves for an IAM estate while the task definition that
  stays reads it through a data source; every side plans clean, the
  carved estate plans for a fraction of the monolith's requests, and each
  estate is torn down by its own destroy. The BREAK control moves the six
  blocks and skips the retag: the monolith must propose destroying the
  leavers and the new estate must propose building them again, stock's
  two-ledger window made visible.

- **the-tag-is-the-boundary** - *Claim 13: the tag is the boundary.*
  Ownership is a tag, so the cloud's own policy engine governs who may
  act on what, per resource. Two roles share one estate, each fenced to
  its half by a condition on the ownership tag; each converges its half
  and is refused on the other's by AWS. Then one role carves her half
  into a new estate with a single `live-mv -from-estate` tag write, the
  other role's attempt at the same move is refused, and both estates
  plan clean under their own roles. Runs with the emulator's IAM
  enforcement on. The BREAK control drops the conditions from one role's
  grant, and the cross-half refusal must vanish.

## Knobs

| Variable | Effect |
|---|---|
| `CHOUDOUFU_VERSION=v0.8.0` | run a pinned release binary instead of building from source |
| `CHOUDOUFU_BIN=/path` | run an explicit binary |
| `FLOCI_IMAGE=...` | override the pinned emulator image (default: `live/floci-image`) |
| `FLOCI_PORT=4650` | pin the emulator host port; unset, the kernel assigns a free one, so concurrent runs never collide |
| `OPENTOFU_IMAGE=...` | override the stock oracle (default: `live/oracle-versions.json`'s tofu) |
| `SMOKE_INSTRUMENT=1` | capture every request (choudoufu's own clients included, per #682) and print request/retry counts with a top-operations table |
| `BREAK=1` | corrupt one expected fact mid-scenario; the scenario passes only by CATCHING it - proof its assertions are load-bearing |

choudoufu builds from source by default and supports pinning; floci is
always the pinned image, never built here - that split is deliberate
(issue #713).

## Reading a run

Every step prints a `=== N. name ===` banner and an indented verdict
line. Trust the verdict lines, never the exit code alone; the exit code is
the summary, the lines are the evidence. A scenario that cannot fail is
not a check, which is what `BREAK=1` exists to disprove on demand.
