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
3. From the repo root, run: just smoke roundtrip
4. Report each step's verdict line as it prints, and the final PASS or
   FAIL line.

A closing `PASS: smoke scenario '<name>' - every claim held` line means
every claim held: an estate stood up by stock OpenTofu was adopted with
one command, survived losing its state file, and went back to stock as
one file. Anything else ends on a `FAIL [...]` line
naming the step that failed.
```

## What this is

One `docker compose` stack - the pinned floci emulator and the pinned
stock OpenTofu oracle - and one scenario per invocation, each tracing a
real user path with a verdict line per step. The harness is versioned
(`VERSION`, printed in every banner) so a report can name what measured it.

```
just smoke                # list scenarios
just smoke roundtrip      # stock estate -> live-import -> no state file -> back to stock
just smoke k8s-greenfield # the same life on a real kind cluster, one label as the marker (#1061)
just smoke k8s-no-silent-orphans # a deleted block's object found by its label; a controller's copies untouched (#1065)
just smoke k8s-the-label-is-the-boundary # one admission policy on the label fences every write; the API server refuses a plain kubectl across estates (#1066)
just smoke k8s-custom-resource # a kubernetes_manifest block binds by the natural key inside its manifest, carries the label and is swept by it (#1079)
just smoke k8s-a-held-delete-is-not-gone # a finalizer holds a delete: the run says destroyed, the object stays, and every plan proposes it again until it is gone (#1110)
just smoke k8s-the-server-gets-the-last-word # admission after the plan: a fail-closed webhook refuses an approved write, a mutating policy rewrites a declared field, and one that strips tofu-estate leaves an object the estate cannot claim (#1110)
just smoke k8s-a-label-is-a-change # a label or annotation edited in the configuration plans and applies like any other change, a key the configuration never declared stays the server's, and a second directory removes the same label over records shared as Secrets and as bucket objects (#1177, #1394; the bucket half needs Docker and the AWS CLI as well as kind)
just smoke k8s-a-deleted-namespace-is-gone # fault 5 of #1110: kubectl delete namespace under a converged estate; the plan reads its objects as gone and proposes stock's creates, and a deleted record-store namespace is refused by name (#1765)
just smoke k8s-staleness-costs-reads # claim 3 on kind: a fresh, an ancient and no cache give one byte-identical plan, and a kubectl edit shows through a fresh cache
just smoke k8s-roundtrip   # claim 6 on kind: stock Terraform's estate adopted with one live-import, run without its state file, handed back to stock as one file
just smoke k8s-stock-when-you-need-it # claim 6 on kind: no live block, the same plan and the same request count as stock OpenTofu at the pinned tofu_version
just smoke k8s-unchanged-is-free # claim 3 on kind: -refresh=false serves vouched objects from the cache and sends fewer requests, same answer
just smoke k8s-plan-cost-tracks-the-estate # claim 14 on kind: 80 foreign ConfigMaps do not move one plan's request count
just smoke k8s-an-estate-reads-another-by-declaring-it # claim 28 on kind: reading another estate's outputs is declared, granted one get, and refused by name without either
just smoke a-held-delete-is-not-gone # claim 1 on AWS: a secret deleted with a recovery window stays in the account, and the plans after it read it as the provider does, empty (#1599)
just smoke the-server-gets-the-last-word # claim 15 on AWS: a Deny landing after approval refuses the approved apply in AWS's words, and the same plan file applies once it lifts (#1599)
```

## Scenarios

The `import` and `greenfield` demos were dropped: `roundtrip` (claim 6)
runs the stock-estate-without-its-state-file path end to end, and the
gauntlet's migrate, greenfield and test_plan stages run both on every
estate. The `full` demo, which wrapped the legacy 15-step harness
`live/e2e/run.sh`, was retired with that harness (#2288): its steps are
gauntlet stages or smoke claims, and its lint-rejects step is
`internal/live/lint/limits_test.go`.

## Kubernetes

`k8s-greenfield` is the first Kubernetes scenario and the harness every
Kubernetes unit runs against (#1057, the order #1016's ruling set). It
starts no emulator: `cluster_up` in `lib.sh` creates a kind cluster named
by the run, writes its kubeconfig under the run's own work directory, and
exports `KUBE_CONFIG_PATH` for the provider; `cluster_down` deletes it on
exit. A kind cluster is a real API server, so what the scenario asserts is
what any cluster answers. Needs `kind` and `kubectl` on PATH.

Every `k8s-*` scenario runs inside bounds (#1457), because a call to an API
server can stall for ever and one did, for 35 minutes of a CI job that left
no log. `smoke.sh` gives the scenario a time limit. When it runs out the run
prints `FAIL [<scenario>]: stalled in step "<step>" and killed after <n>s.`,
names the commands that were still running, kills them, deletes the cluster as on
any other exit, and exits 124. Scenarios call kubectl through `kc`, or
`kc_as <kubeconfig>` for another identity, and both pass
`--request-timeout`. `kubectl config`, which edits a local file, is the
only kubectl a scenario calls bare. A choudoufu call made while a step has
the admission chain failing or rewriting goes through `chdf_bounded`, which
fails the scenario by name the same way. The three variables are under
Knobs. `bash live/smoke/selftest-bounds.sh` (or `just smoke-selftest`)
proves each bound against a `kubectl` and a `choudoufu` that never return,
with no cluster and no Docker, and reads the scenarios for a bare kubectl.

It is claim 7 on Kubernetes (#1061): the ConfigMap and the namespace it
creates carry one `tofu-estate` label, written on the create and read back
with kubectl in step 2, and listed by `live-ls` in step 3 (#1081) - the substrate learned
from the provider block, one label-selected list per kind, each object
joined to its block on the kind and the natural key - with the listing
empty again after the destroy; its `BREAK=1` strips the label and requires
`live-ls` to drop the object and the replan to refuse it by name (#1108: an
unlabelled object is nobody's, and adoption is an operator's write). The
marker is the estate alone, never the address (#1016). Its step 6 rewrites
the ConfigMap block from `kubernetes_config_map` to
`kubernetes_config_map_v1` with no `moved` block and requires the replan
to plan no create and no destroy (#1081, item 2: an `api_version` change
is not a move).

`k8s-no-silent-orphans` is claim 1 on Kubernetes (#1065): a ConfigMap's block is deleted and the next plan proposes exactly
that object's removal, found by one cluster-wide, label-selected list per
kind, while the ReplicaSet and Pod a Deployment's template gave the same
label to are never touched. Its `BREAK=1` strips the orphan's label and
requires the replan to leave the object alone. The Deployment's container
is `registry.k8s.io/pause`, which kind's node image already carries, and
`wait_for_rollout` is off, so the scenario needs no image pull.

The Kubernetes scenarios run by hand on a kind cluster, each followed by
its `BREAK=1` control; no workflow runs them. The kind-gated Go tests in
pkg/staterecord run the same way, with `just test-kind`.

`k8s-custom-resource` is claim 7 (#1079's first unit): a CRD installed
with kubectl, one `kubernetes_manifest` block declaring a CronTab, applied
and replanned empty with nothing stored anywhere, the object found again
by the apiVersion, kind, namespace and name inside its manifest, and
created with the one `tofu-estate` label the configuration never wrote
(#1079's second unit, the stamp into `manifest.metadata.labels`). Its
`BREAK=1` strips the label with kubectl and requires the replan to propose
the update that restores it, strips it again with the block removed and
requires the replan not to list the object, then deletes the object and
requires the replan to propose creating it. Removing the block for real
(step 5) has the sweep, which lists every kind the cluster serves under
`kubernetes_manifest` (#1079's third unit), find the CronTab by its label
and propose destroying exactly it. Its last step (#1396, #1434) migrates
two stock-made ConfigMaps under a 64-character estate name and requires
the read-only `live-import` to refuse once, naming `2 resource instances`
and the 63-character cap, with no per-object line, exit 1 and nothing
changed on the cluster; `BREAK=1`'s last control runs the same migration
under a 60-character name and requires the report and exit 0.

`k8s-a-held-delete-is-not-gone` is claim 1 (#1110's first fault): a
finalizer added out of band holds a ConfigMap's delete, so the API accepts
it, the run prints `Destruction complete after 0s` and counts one
destroyed, and the object is still in the cluster with a
`deletionTimestamp` and its `tofu-estate` label. The sweep lists it like
any other live object and every plan proposes the same one destroy until
the finalizer clears, at which point the object goes and the plan is
empty; `apply -destroy` over a held object likewise reports the estate
destroyed and exits 0, and the plan after it proposes exactly the one
create that is genuinely missing. The provider's summary line stays as
stock prints it; since #1184 one warning after it, `Delete accepted, object
not gone`, names the held object and its finalizer, and the plan is what
corrects the count. Its `BREAK=1` removes the finalizer before the
destroying apply and requires the object gone in one apply and the replan
empty - without it the scenario would read the same if choudoufu never
deleted a ConfigMap at all. The namespace is made with kubectl rather than
declared, because a `kubernetes_namespace` delete waits on everything
inside it and that five-minute timer would hide the answer.

`k8s-a-label-is-a-change` is claim 45 (#1177). On `kubernetes_manifest` an
edit to `metadata.labels` or `metadata.annotations` used to be invisible:
the plan said `No changes.` and the apply wrote nothing, silently. The
provider's `computed_fields` default takes the LIVE value at those paths
unless the configuration differs from the PRIOR MANIFEST, and a live
run was seeding that prior from the current configuration - so the
comparison compared the configuration with itself. Stock reproduces it
exactly when handed the same prior. Step 2 measures what stock proposes for
the same one-label edit on the same cluster rather than quoting it, step 3
requires choudoufu to match, step 4 does the annotation half (the issue's
own reproduction), step 5 requires a Namespace's server-written
`kubernetes.io/metadata.name` and two hand-written keys to churn nothing,
and step 6 prints the one difference from stock side by side: an
out-of-band change to a DECLARED key plans here and does not there. Its
`BREAK=1` runs the identical `kubectl label --overwrite` against a key the
configuration does not declare and requires `No changes.` - without it
every plan the scenario requires would read the same if choudoufu simply
planned on any difference at all.

Steps 8 and 9 are #1394. Everything above them runs on the implied local
store, where the record a removal reads is a file beside the module, so the
removal works for whoever holds that directory and for nobody else. Each of
the two applies from one working directory and deletes the label from a
second that never applied anything, and requires the second to propose the
removal, write it and settle - over a store the two share. Step 8 is
`record_store "kubernetes"` (#1392), records as Secrets in the cluster the
claim already runs on; it names no namespace, so what is used is what the
estate name derives, and the step creates it, because the records namespace
is the read boundary and nothing in this fork creates one. Step 9 is
`record_store "s3"` on the pinned floci emulator, which makes this the one
scenario here needing both substrates at once.

Both read what no Kubernetes claim had read before. On the cluster store:
the record Secret's `tofu-estate` label, its `tofu-address` and record-key
annotations, and its `resourceVersion` moving across the second directory's
write, with a `kubectl replace` of the copy from before that write refused
as a conflict. On the bucket: the object's `tofu-estate` and `tofu-address`
tags and the conditional-write header on each record PUT, through
`s3proxy.py`. Sub-step 9b holds the second directory's write at the proxy,
deletes the object underneath it and requires the refusal (#1344's case,
which the emulator answers 404 exactly as real S3 does). One `BREAK=1`
control covers both: the same two directories on `record_store "local"`,
where the second must propose nothing. Step 9 refuses rather than skips
when Docker or the AWS CLI is missing.

`k8s-the-server-gets-the-last-word` is claim 15 (#1110's second fault):
three things admission can do to a write the plan already approved. A real
`ValidatingWebhookConfiguration` with `failurePolicy: Fail` and no endpoint
refuses the apply of a saved `-out` plan, and the run reports the API
server's own `failed calling webhook` message with the object untouched and
the artifact still on disk; removing the webhook and applying the same file
lands it unchanged. A `MutatingAdmissionPolicy` that overwrites a declared
label produces the same perpetual `0 to add, 1 to change, 0 to destroy` on
every plan that plain stock produces, measured side by side in step 5, with
the marker untouched. The third is the boundary case: a policy that strips
`tofu-estate` on the way in, which is what a label-scheme enforcer does to
a key it does not recognise. The object is created and no marker is stored,
so the next plan reads the estate's own object as somebody else's,
`live-ls` reports the estate empty, and plan and apply both stop with
`Unlabelled live object holds the declared name` (#1546) rather than
proposing a create the API server would refuse. #1192 was that the run making it
said nothing: `Apply complete! Resources: 1 added` with no mention of the
marker, and `declared_untagged = "adopt"` reporting `0 added, 1 changed, 0
destroyed` and exit 0 over a label it never wrote, on every run for ever.
Steps 6 and 7 now assert the answer. The create warns, because the object
really was added; the adopting run errors and prints no completion line,
because its whole content was the marker and nothing it wrote lasted -
shown by a `resourceVersion` that does not move across two runs. The
judgement is made on the object the provider already returned from
`ApplyResourceChange`, so it costs no extra request. Its `BREAK=1` points
the identical policy at a decoy label instead of the marker and requires
the decoy stripped, the marker landed, no `Ownership marker was not stored`
in the run, `live-ls` listing the object and the second apply not wedging -
without it the whole third part would read the same if choudoufu never
wrote a label at all.

`k8s-the-label-is-the-boundary` is claim 13 on Kubernetes (#1066): the cluster
admin installs `live/kubernetes/estate-boundary.yaml`, one `ValidatingAdmissionPolicy`
whose CEL reads the estate label off the object and asks the authorizer
whether the caller holds `use` on `estates.choudoufu.intentius.io/<estate>`;
two ServiceAccounts hold two estates through
`live/kubernetes/estate-grant.yaml`, each is refused on the other's
objects by the API server, through choudoufu and through plain kubectl
alike, and one object is carved into a new estate by a relabel the policy
refuses from both sides until a binding moves. Its `BREAK=1` deletes the
policy and requires the refused writes to go through. The scenario mints
each ServiceAccount a token and a kubeconfig of its own under the run's
work directory; `as_role` points both `KUBECONFIG` (kubectl) and
`KUBE_CONFIG_PATH` (the provider and the sweep) at it.

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
- **apply-what-was-approved** - *Claim 15: apply exactly what was
  approved.* A plan is saved with `-out`, the world moves underneath the
  approval (a marked subnet appears for an address the configuration
  does not declare), and `apply <planfile>` re-reads the live system,
  compares its own fresh plan with the file's, and refuses by name -
  `The approved plan no longer matches the live system` - with exit
  status 3, naming the row nobody approved. Then it refuses a second
  time for the subtler case: the change sets agree exactly and the
  configuration was edited after the approval, so the same resource,
  same action and same live object plan a different value - refused
  with `after.retention_in_days` named. Then the same two commands over
  the world as it now is, and the reviewed change lands. Steps 8-14 were
  two-saved-plans-one-attribute (#1504): two checkouts approve plans
  changing one SQS queue's visibility timeout from 30, the first applies,
  and the second is refused with exit 3 naming
  `before.visibility_timeout_seconds`, the cloud left on 60. `BREAK=1`
  rebuilds choudoufu with the before-values comparison removed (Go
  needed), skips steps 1-7, and the second apply must be caught reporting
  success with the queue on 90. `BREAK_UNMOVED=1` is the inverse control:
  it leaves the world unmoved and the same file must apply, because a
  comparison that refuses every plan file is not a check.
- **stock-when-you-need-it** - *Claim 6 (claim 8 until it merged into 6): stock behavior is the
  fallback, whole and exact.*
  Choudoufu with the live block removed plans a state-backed estate
  against the pinned stock oracle, both under TF_LOG: filtered plan
  texts equal, request counts identical. What the live backend costs
  against the account around the estate is claim 14's. The
  BREAK control runs the choudoufu leg with the live block on: the
  measurement must show the difference, or the parity comparison
  compares nothing.
- **unchanged-is-free** - *Claim 3 (claim 9 until it merged into 3): unchanged is free.* The same
  -refresh=false plan runs under the default selective policy and under
  the reads="full" off switch: selective serves the vouched instances
  and measurably drops the request count, full serves nothing and pays
  every read, and the two outputs are byte-identical - the toggle
  prices the plan, never changes it. The record-backed half proves the
  record is the attestation: an out-of-band edit surfaces on the next
  default plan as a named reconvergence. The BREAK control overwrites
  the record with garbage - the run must refuse naming the exact
  address, never plan against improvised values.
- **cache-serves-the-whole-estate** - *Claim 3 (claim 10 until #1817, then claim 9): the cache serves the
  whole estate.* On -refresh=false every converged instance is served
  from the state cache - server-assigned needs-discovery resources
  (VPCs, subnets, security groups) included, not just the schema-admitted
  slice - so one estate of a terralith plans without re-reading the
  cloud. A default plan still refreshes (the read is drift detection),
  and the serving is existence-vouched: the BREAK control deletes a
  resource out of band and the plan surfaces it, never serving a gone
  object from cache.
- **count-is-a-fungible-set** - *Claim 7 (claim 11 until #1817): a count pool is a fungible
  set.* Where nothing in the configuration says which live resource is
  which, a `count` block's members are interchangeable and a `tofu-slot`
  marker is what names each one rather than its index. Scaling a pool
  of three down to two removes exactly one member and creates nothing.
  The middle survivor stays the same live object, where stock would
  renumber and rebuild the tail. Then the boundary of the claim, on one
  type and one AWS CLI call: two `count` blocks of
  `aws_cloudwatch_log_group` differing in one property - one names its
  members (`name = "/svc/${count.index}"`), the other leaves the name to
  the provider (`name_prefix`) - and every tag read back off the live
  groups as a whole key set, the named pair carrying `tofu-estate` and
  `tofu-address` and no `tofu-slot` while the pair beside it carries
  slots `0` and `1`, with the next plan empty so both kinds bind (#969,
  #976). Two BREAK controls, one per direction: `BREAK=1` deletes the
  local record then strips one member's slot, and the plan must refuse
  the half-slotted set by name rather than bind the odd member by a
  guess; `BREAK_SLOT=1` stamps a `tofu-slot` onto a member the
  configuration names, where none belongs, and the same tag read that
  passes in the ordinary run must fail on it - an absence can only be
  tested by a tag that should not be there.

- **carve-by-retag** - *Claim 7 (claim 12 until #1817): carve by retag.* Needs Go. The pinned
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
  and is refused on the other's by AWS. The fence binds the credential,
  not the binary: with no choudoufu anywhere in the call, a plain AWS CLI
  write and a plain destructive call against the other role's half are
  both refused by the identical condition, and a plain CLI write the role
  is permitted to make lands with no choudoufu involved and is still
  surfaced by the next plan. Then one role carves her half into a new
  estate with a single `live-mv -from-estate` tag write, the other role's
  attempt at the same move is refused, and both estates plan clean under
  their own roles. Runs with the emulator's IAM enforcement on. The BREAK
  control drops the conditions from one role's grant, and both the
  cross-half refusal through choudoufu and the tool-less cross-half
  refusal must vanish.

- **a-role-holds-an-address-pattern** - *Claim 13 (#1955): a role scoped
  to an address pattern inside one estate.* One role holds one statement,
  conditioned on `aws:ResourceTag/tofu-address` = `module.app.*` and on
  nothing else. In one estate its apply changes `module.app`, and its
  change on `module.db` is refused by AWS, as are a plain `aws ec2
  create-tags` and `terminate-instances` on that instance. Runs with the
  emulator's IAM enforcement on. The BREAK control rewrites the grant with
  the same actions and no condition, and the change on `module.db` must
  land.

- **the-boundary-holds-across-regions** - *Claim 13 (claim 16 until #1817): the boundary holds
  across provider configurations.* One estate spans two aliased
  providers, a region each, under one `tofu-estate` marker and one
  record store; a client-chosen name mirrored into both regions stays
  two distinct objects, so deleting one names that region's instance
  and only that one while the other region keeps being served from
  cache (the #745 defect, pinned); each pass lists its own
  configuration's declarations, so an account-global type declared
  under one provider is listed once rather than once per region; and
  the two honest edges are pinned rather than argued - a region whose
  last declaration is removed drops out of the sweep with its provider
  configuration, and a region change is a replace whose other half no
  address can express, so it is refused by name rather than half-done -
  and permitted, still by name, under
  `strict { provider_change = "recreate" }` (#906). Per-pass request counts come off
  the wire, from the region each request was signed for. The BREAK
  control points the west provider at the east region and strips the
  surviving object's markers, so the same name in the other region is
  the only live evidence for the deleted instance - the plan reports it
  unchanged and the run must fail on that line.

- **a-shadow-is-not-a-claimant** - *Claim 1 (claim 18 until #1817): a replaced object's
  shadow is not a second claimant.* Two ForceNew replaces at one
  declared address leave two terminated instances still wearing its
  markers - AWS's own documented lag, read back through the plain CLI -
  so three objects claim one address. The record settles it: its
  identity names the live object and its tombstone member is a LIST of
  the identities this estate's own applies destroyed, capped at eight
  per address, both asserted by value off the record file. The plan
  then exits 0, drops exactly those two, names each in a displaced-marker
  warning that proposes nothing, and binds the address to the third. Then
  a create_before_destroy replace under a role the platform denies
  ec2:TerminateInstances leaves the old instance running and deposed, and
  the record names it under deposed and nowhere under tombstone (#901):
  nothing destroyed it, so nothing says it was. The BREAK control puts a
  second genuinely RUNNING instance behind the same markers with no
  tombstone naming it: the plan must refuse with "Two live resources
  claiming one address" naming both live ids, because a tombstone is
  evidence an object is dead and never permission to touch one that is
  not; and then patches the record to call the deposed, running object
  destroyed, which the read must catch.

- **the-boundary-holds-across-accounts** - *Claim 13 (claim 19 until #1817): the boundary holds
  across accounts.* Claim 13's estate with the other axis swapped: two
  AWS accounts, one region, one `tofu-estate` marker and one record
  store. The same client-chosen name is declared in both accounts and
  the two objects are told apart by account alone - `sts:GetCallerIdentity`
  answers with a different id under each credential, each object is
  invisible to the other account's own listing, and the emulator refuses
  the second create outright when both blocks are pointed at one
  account. Deleting the other account's object names that account's
  instance and only that one while the home account stays served from
  cache (the #745 defect across the account axis), losing the state
  cache and the whole record store still replans empty in both accounts
  at once, and one destroy empties both. Per-account request counts come
  off the wire, from the credential each request was signed with - the
  account id IS the access key id here. The BREAK control swaps the
  second provider's credential for the first account's and strips the
  surviving object's markers, so the same name in the other account is
  the only live evidence for the deleted instance - the plan reports it
  unchanged and the run must fail on that line.

- **plan-cost-under-foreign-load** - *Claim 14 (claim 20 until #1817): scale - the estate
  boundary holds when the account around it is a terralith.* Claim 14's
  question asked where it is load-bearing: a generated terralith
  (`tools/terralith-gen`, so this scenario needs Go) is applied under a
  second `tofu-estate` marker beside the estate under test, and the estate
  is replanned unchanged. The estate-scoped legs do not move with the
  neighbour; the legs that read the account rather than the estate are
  counted on their own and named rather than folded into a total - Cloud
  Control's `ListResources`, which takes no tag filter at all, and the
  types whose provider list resource offers no filter block either.
  `FOREIGN_SCALE` and `OWNED_SCALE` set the two terraliths' sizes, so the
  same scenario a reader runs in five minutes is the one that produced the
  3,705-resource row. The BREAK control asks the same estate the
  account-wide question (`-adoption-only`), which is exactly the branch
  that drops the server-side estate filter, and the cost must explode.

- Six bucket scenarios left in GitHub issue #2288, because a Go test in
  CI's fast tier already held what each one's `BREAK=1` arm broke:
  two-writers-one-record and cas-holds-under-every-sse-flavour (claim 2),
  a-name-prefix-shares-no-keys (claim 28), and a-wrong-bucket-is-refused,
  a-waiver-names-what-it-waives and a-bulk-read-is-complete-or-it-fails
  (claim 29). Their cells cite those tests as `go_test` proofs in
  `claims.json`; the real-AWS one, `TestS3StoreCASUnderEverySSEFlavour`,
  runs with `SMOKE_REAL_AWS=1 just test-sse`. Steps 1, 7, 10 and 12 of
  `k8s-records-in-the-cluster` went the same way, the kind-gated ones to
  `just test-kind`.

- **one-bucket-many-estates** - *Claim 28 (claim 35 until #1817): reading a neighbour's records
  takes two mistakes, not one.* **Real AWS, maintainer-run.** Two
  estates under their own roles in one bucket; one role is refused the
  other's records, outputs and listings, and the bare prefix; with its
  prefix deliberately widened it is still refused the read, by the tag;
  the same widened role overwriting and deleting a neighbour's object is
  shown allowed, because nothing but the prefix defends that; and a
  `--reads-outputs-of` grant opens the other estate's outputs and nothing else.
  Under the widened prefix the role also tries to relabel a neighbour's
  record as its own and to strip its tag, and both are refused (#1381).
  The BREAK control has two arms: without the relabel Deny the retag and
  then the read both succeed, and without the read Deny the read
  succeeds (#1343). Since claims 34 and 36 folded in, it also checks that
  both estates' first writes go through the published policy, that every
  object carries its estate's tag (and a record its escaped
  `tofu-address`), that updates work under `If-Match`, and that a record
  retagged as another estate's is refused to its own estate by name.
  `BREAK_FIRST_WRITE=1` conditions the write on `s3:ExistingObjectTag`
  and a new estate's first write must be denied. A store that sends no
  tags is caught by `TestS3StoreTagsEveryObjectItWrites` in CI, so this
  scenario no longer carries that arm (#2288). Needs jq.
- **the-recommended-secure-configuration** - *Claim 37: the
  recommended secure configuration works end to end, including
  recovering a deleted record.* **Real AWS, maintainer-run.** The bucket
  is stood up with `just up` from `examples/record-store-bucket` under a
  customer managed key whose key policy names who may use it, and the
  estate's role carries the rendered `--kms` policy, unedited. The role
  runs an estate's life, a record destroyed by mistake is recovered from
  its noncurrent version by the operator (the role is refused the same
  act), the S3 actions in the request log are reconciled with the
  policy's grants in both directions, and `just down` refuses while
  versions remain. The BREAK control takes the role out of the key
  policy, and the run must be refused naming the key, the KMS action and
  the role, not just the words "KMS key" (#1345, #1379). Needs jq, just,
  node and npm.
- **a-read-only-role-can-plan** - *Claim 28 (claim 38 until #1817): a role with the read-only
  policy plans an established estate and writes nothing, and a store with
  no sentinel is still refused by name.* **Real AWS, maintainer-run.** An
  estate is recorded once under the full policy, which provisions the
  sentinel. A second role carries `render-policy.sh --read-only`, with
  the Allow half of a second read-only render merged in for the estate
  name step 4 uses. As that role the plan is empty, a direct
  `put-object` under the estate's prefix is denied, and the three
  namespaces hold the same object versions after the plan as before, the
  sentinel included. The same role against an estate name with no
  sentinel is refused by name, naming the key and saying this identity
  may not write it, and proposes nothing. The plan's own S3 calls are
  then reconciled with the rendering in both directions, with the
  denied sentinel write as the one expected difference and no
  bucket-configuration read at all. The BREAK control rebuilds choudoufu
  so a denied sentinel write is returned rather than carried past to the
  List, and the read-only plan must then fail (#1370). Needs jq and Go.

- **no-secret-survives-in-what-the-tool-keeps** - *Claim 40: no secret
  the run generates or sets survives in what the tool keeps.* Under
  `strict { secrets = "refuse" }` a `random_password` and an
  `aws_iam_access_key` are refused by name and nothing is written. A
  database's master password, passed only through `TF_VAR_db_password`,
  is then applied and grepped for by value in every file the run kept
  (the record, the data dir, the lock file, the configuration), with zero
  hits and no cache file. A saved plan and a `TF_LOG=debug` file are read
  on their own: the plan's `tfplan` member and the provider's
  `CreateDBInstance` request line hold it, as they do on stock. The
  default run keeps it in the record and the cache and still plans
  `No changes.` with the cache deleted. The BREAK control applies the same
  estate under `secrets = "store"`, and the same scan must find the value.
  The replan under refuse proposes the password again, rotated or not,
  which is what the refusal costs (#1503).
- **a-killed-apply-hides-nothing** - *Claim 5 (claim 42 until #1817): a killed apply hides
  nothing it marked.* A real apply is killed with SIGKILL at a point
  pinned by a count read off the account - hosted zones named
  `killed-apply.example.`, 0 then 1 - never by a timer. The VPC it
  created carries its markers, because they rode the create call, so the
  next plan proposes nothing for it and the re-run binds it. The hosted
  zone does not: `aws_route53_zone` reads `tag_on_create: false`, so the
  markers land only when the provider's create step returns, measured
  15.0s after the zone appeared. The next plan proposes a second zone,
  the re-run builds it, and the orphan is deleted by hand - the only
  surgery in the run. A record-carried `aws_iam_group_policy` with a
  provider-assigned name, a `random_password` and a `terraform_data` with
  a provisioner had all returned before the kill, and since #1944 each
  record is written the moment its instance returns: the next plan binds
  the policy and proposes none of the three, and the re-run leaves one
  inline policy, the same password and an effect that ran once. The BREAK
  control strips every marker and requires a duplicate VPC.
  `BREAK_EARLY_RECORD=1` rebuilds choudoufu with that mid-apply record
  write switched off (go build -overlay, needs Go, refuses a release
  binary) and passes only when the re-run is caught leaving two inline
  policies on the group and the plan proposes the password again.
- **an-estate-reads-another-by-declaring-it** - *Claim 28 (claim 44 until it merged into 28): an estate
  reads another estate's outputs only by declaring the read.* Estate
  `network` applies and records one root output. Estate `app` declares
  `data "terraform_estate_outputs"` naming `network`, and its role is
  rendered with `--reads-outputs-of network`; its plan uses the value and
  warns that it is as of network's last apply. `network` is destroyed, its
  `tofu-outputs/network/` object goes, and `app`'s plan then stops with
  `Another estate has not recorded this output`. The BREAK control renders
  `app`'s role without the flag and requires the plan to refuse naming
  estate `network`. Emulator; the tag-conditioned half is claim 28's.

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
| `BREAK_SLOT=1` | count-is-a-fungible-set's second control: the one corruption an absence assertion can be tested with, a tag that should not be there |
| `BREAK_EARLY_RECORD=1` | a-killed-apply-hides-nothing's second control: choudoufu rebuilt with no record written until the apply's walk is over (#1944); passes only when the re-run is caught duplicating the record-carried inline policy |
| `SMOKE_TIMEOUT_SECS=600` | seconds before a k8s-* scenario with no verdict is killed (default max(600, 2 x claims.json minutes)) |
| `CHDF_TIMEOUT_SECS=300` | one choudoufu call made behind a failing or rewriting admission chain |
| `KC_REQUEST_TIMEOUT=30s` | one kubectl request |
| `FOREIGN_SCALE=50` | plan-cost-under-foreign-load: how large the foreign terralith beside the estate is, in `tools/terralith-gen` scale (74N + 5 resources; default 1) |
| `OWNED_SCALE=50` | plan-cost-under-foreign-load: how large the estate under test is, same units (default 1) |

choudoufu builds from source by default and supports pinning; floci is
always the pinned image, never built here - that split is deliberate
(issue #713).

## Reading a run

Every step prints a `=== N. name ===` banner and an indented verdict
line. Trust the verdict lines, never the exit code alone; the exit code is
the summary, the lines are the evidence. A scenario that cannot fail is
not a check, which is what `BREAK=1` exists to disprove on demand.

Every run ends on exactly one closing line, printed by `smoke.sh` and
never by a scenario (#1439): `PASS: smoke scenario '<name>' - every claim
held`, or for a control run `PASS: smoke scenario '<name>' - the control
(BREAK) caught what it broke, N proof line(s)`, or a `FAIL [<name>]: ...`
naming what broke and the step it was in. A run whose scenario died under
`set -e` mid-step, which used to end with no line at all, now ends on
`FAIL [<name>]: no verdict line - a command failed under set -e in step
"<step>" ...`; a scenario that exits 0 on its own outside a control arm
ends on `FAIL [<name>]: no PASS line ...`; and a control run (`BREAK=1`
or any `BREAK_<NAME>=1`) in which no control printed a `-> caught` proof
line ends on `FAIL [<name>]: no '-> caught' line ...`. The convention
every scenario follows: a control arm ends with `proof "caught ..."`, and
then either exits 0 or runs on into the steps it shares with the main
arm. `live/smoke/ci-run.sh --check <scenario> <log>` reads a saved log for
those lines rather than the exit code, and `bash
live/smoke/selftest-verdict.sh` proves each shape against stubs in a few
seconds; `go test ./live/` runs it.

No workflow runs a claim scenario: #2018 deleted the three that did. A
proof counts toward its cell only through a dated run, which is a
`live/smoke/evidence/<name>.lastrun.json` written from the two logs of a
hand run (the main arm and `BREAK=1`) with a `date` field, a proof's
carried `last_run`, or a gauntlet stage the cell cites as
`<estate>:<stage id>` while that stage reads pass in `live/gauntlet.json`.
A cell with any proof lacking all three reads `unproven` in `claims.json`,
and `live/proven_cell_ci_test.go` fails the day a cell says otherwise. A
release never resets a result: `live/CLAIMS.md` marks a run older than the
newest release stale. The `last_run` entries were restored from
`claims.json` at 958bcf7f2d, the last commit before #2028, and
`live/claims_last_run_test.go` holds each one to that history.

A cell can also cite a Go test (`go_test`, GitHub issue #2288). One with no
gate runs in CI's fast tier, and its dated run is a successful push run of
the CI workflow on main at a commit, recorded with `go run
./tools/gotest-record -ci-run <run id>`; one behind an environment gate
runs from a recipe (`just test-kind`, `SMOKE_REAL_AWS=1 just test-sse`),
and its dated run is that recipe's log, recorded with `go run
./tools/gotest-record -log <log>`. Both land under
`live/smoke/evidence/go-test/`, and nothing else writes there. A Go test
proof with no recorded run yet reads "waiting for its first recorded run"
in `live/CLAIMS.md` and does not count toward its cell, which still needs
a dated proof of its own (`live/smoke_claims_gotest_test.go`).

A cell can also cite a row a CI product recorded (kind `product-scenario`,
GitHub issue #2299). terragucci, plywood and sql-yodeler run scenarios on
the shared e2e harness (INTENTIUS/chant#4587) against a released choudoufu,
with a plain run that must pass and a `BREAK=1` run that must be caught,
and commit one row per scenario and claim. The proof is a flat object in
`claims.json`:

```json
{"kind": "product-scenario", "repo": "INTENTIUS/terragucci",
 "scenario": "tg-estate-overlap", "claim": "choudoufu/rows-overlap",
 "commit": "<full sha>", "date": "YYYY-MM-DD", "engine_version": "0.31.0",
 "note": "what the row shows that the claim needs"}
```

These are the kind and keys plunge's and sql-yodeler's claims use, so all
three engines cite a product row the same way. The row is kept beside the
claims as `live/smoke/evidence/product-scenario/<repo>--<scenario>--<claim>.json`,
a copy of the row from the product's committed row file with that file's
path (`row_file`), the product, the engine binary, the floci digest and both
verdicts. `live/CLAIMS.md` links each proof to `row_file` at `commit`.

A product row counts toward its cell only when its `engine_version` is the
release the claims are about, the newest `live/history/vX.Y.Z.json`. A row
at another release, or a proof whose row is not kept yet, reads as not
counting in `live/CLAIMS.md` and stands beside the cell's dated proofs, the
way a waiting Go test does. So a row that ran v0.28.0 never proves a claim
about v0.31.0, and every product proof stops counting at a release until its
product records a row on the new one.

A product row never replaces an engine scenario (ruling E on
INTENTIUS/chant#4586). The engine's `BREAK=1` arms rebuild choudoufu from
source with an overlay, which a product running a released binary cannot
do, so every cell that cites a product row keeps an engine proof as well,
and `live/smoke_claims_product_test.go` fails a cell whose only proofs are
product rows.
