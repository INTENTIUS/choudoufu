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
just smoke k8s-greenfield # the same life on a real kind cluster, one label as the marker (#1061)
just smoke k8s-no-silent-orphans # a deleted block's object found by its label; a controller's copies untouched (#1065)
just smoke k8s-the-label-is-the-boundary # one admission policy on the label fences every write; the API server refuses a plain kubectl across estates (#1066)
just smoke k8s-custom-resource # a kubernetes_manifest block binds by the natural key inside its manifest, carries the label and is swept by it (#1079)
just smoke k8s-a-held-delete-is-not-gone # a finalizer holds a delete: the run says destroyed, the object stays, and every plan proposes it again until it is gone (#1110)
just smoke k8s-the-server-gets-the-last-word # admission after the plan: a fail-closed webhook refuses an approved write, a mutating policy rewrites a declared field, and one that strips tofu-estate leaves an object the estate cannot claim (#1110)
just smoke k8s-a-label-is-a-change # a label or annotation edited in the configuration plans and applies like any other change, and a key the configuration never declared stays the server's (#1177)
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

## Kubernetes

`k8s-greenfield` is the first Kubernetes scenario and the harness every
Kubernetes unit runs against (#1057, the order #1016's ruling set). It
starts no emulator: `cluster_up` in `lib.sh` creates a kind cluster named
by the run, writes its kubeconfig under the run's own work directory, and
exports `KUBE_CONFIG_PATH` for the provider; `cluster_down` deletes it on
exit. A kind cluster is a real API server, so what the scenario asserts is
what any cluster answers. Needs `kind` and `kubectl` on PATH.

It is claim 21 (#1061): the ConfigMap and the namespace it creates carry
one `tofu-estate` label, written on the create and read back with kubectl in
step 2, and listed by `live-ls` in step 3 (#1081) - the substrate learned
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

`k8s-no-silent-orphans` is claim 22 (#1065), the Kubernetes sibling of
claim 1: a ConfigMap's block is deleted and the next plan proposes exactly
that object's removal, found by one cluster-wide, label-selected list per
kind, while the ReplicaSet and Pod a Deployment's template gave the same
label to are never touched. Its `BREAK=1` strips the orphan's label and
requires the replan to leave the object alone. The Deployment's container
is `registry.k8s.io/pause`, which kind's node image already carries, and
`wait_for_rollout` is off, so the scenario needs no image pull.

Every Kubernetes scenario runs in CI on every pull request that touches
the Kubernetes surface, each followed by its `BREAK=1` control, on a kind
cluster the runner creates (`.github/workflows/k8s-smoke.yml`, #1080;
`live/k8s_ci_test.go` holds that matrix to this directory, so a new `k8s-*`
scenario has to be added there too). The nightly gauntlet runs the
kubernetes lane's estates the same way.

`k8s-custom-resource` is claim 24 (#1079's first unit): a CRD installed
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
and propose destroying exactly it.

`k8s-a-held-delete-is-not-gone` is claim 25 (#1110's first fault): a
finalizer added out of band holds a ConfigMap's delete, so the API accepts
it, the run prints `Destruction complete after 0s` and counts one
destroyed, and the object is still in the cluster with a
`deletionTimestamp` and its `tofu-estate` label. The sweep lists it like
any other live object and every plan proposes the same one destroy until
the finalizer clears, at which point the object goes and the plan is
empty; `apply -destroy` over a held object likewise reports the estate
destroyed and exits 0, and the plan after it proposes exactly the one
create that is genuinely missing. The false summary line is #1184; the
plan is what corrects it. Its `BREAK=1` removes the finalizer before the
destroying apply and requires the object gone in one apply and the replan
empty - without it the scenario would read the same if choudoufu never
deleted a ConfigMap at all. The namespace is made with kubectl rather than
declared, because a `kubernetes_namespace` delete waits on everything
inside it and that five-minute timer would hide the answer.

`k8s-a-label-is-a-change` is claim 27 (#1177). On `kubernetes_manifest` an
edit to `metadata.labels` or `metadata.annotations` used to be invisible:
the plan said `No changes.` and the apply wrote nothing, silently. The
provider's `computed_fields` default takes the LIVE value at those paths
unless the configuration differs from the PRIOR MANIFEST, and a stateless
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

`k8s-the-server-gets-the-last-word` is claim 26 (#1110's second fault):
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
`live-ls` reports the estate empty and the next apply wedges on
`configmaps "app-config" already exists`. #1192 was that the run making it
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

`k8s-the-label-is-the-boundary` is claim 23 (#1066), the Kubernetes
sibling of claim 13: the cluster admin installs
`live/kubernetes/estate-boundary.yaml`, one `ValidatingAdmissionPolicy`
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
- **backend-sets-itself-up** - *Claim 4: the backend is a bucket with
  no lock table and no lock: nothing is held, so nothing gets stuck.*
  **Real AWS, maintainer-run, for now** (the pinned emulator's
  CloudFormation applies none of a bucket's properties). A live block
  with no storage declared gets a local record store the way stock
  implies a local state file - a .tofu-records directory appears beside
  the module at first use, sentinel already written. The cloud store is
  a bucket, stood up with `just up` and checked by the binary with
  `just verify`: the same bucket, versioning and IAM as stock, plus a
  lifecycle rule and a public-access block stock never listed, minus
  the lock table. An apply is then killed with SIGKILL mid-flight and
  the very next run finishes the work, because nothing was held. The
  teardown admits there is a bucket to take down, and `just down`
  refuses while it holds record versions. The BREAK control makes only
  the record store unreachable and proves the run refuses by name
  instead of planning an empty-looking estate - the #693 failure class,
  permanently on watch (#1349). Needs jq, just, node and npm.
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
  the world as it now is, and the reviewed change lands. The BREAK
  control is the inverse of the usual one: it leaves the world UNMOVED
  and the same file must apply, because a comparison that refuses every
  plan file is not a check.
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

- **the-boundary-holds-across-regions** - *Claim 16: the boundary holds
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

- **record-only-survives-cache-loss** - *Claim 17: a record-only
  composite identity survives cache loss without a duplicate create.*
  `aws_iam_group_policy` with its `name` left for the provider to
  assign carries no tags argument and no list route this fork uses, so
  once it is created neither half of its two-part identity (group,
  policy name) is a literal or a reference its own configuration
  states - the record an apply writes is the only place that identity
  is ever held. Losing the disposable state cache costs nothing, the
  same as any other resource, and the recovered plan's own read is
  checked by value against the group and name the record printed
  earlier. The BREAK control deletes the identity record itself before
  that same re-plan: the plan must propose one create, named, rather
  than silently reporting no changes.

- **a-shadow-is-not-a-claimant** - *Claim 18: a replaced object's
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

- **the-boundary-holds-across-accounts** - *Claim 19: the boundary holds
  across accounts.* Claim 16's estate with the other axis swapped: two
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

- **plan-cost-under-foreign-load** - *Claim 20: scale - the estate
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

- **a-name-prefix-shares-no-keys** - *Claim 28: two estates whose names
  prefix one another share a bucket and none of each other's keys.*
  smoke-prod and smoke-prod-eu apply into one bucket; the AWS CLI shows
  that the bare prefix tofu-records/smoke-prod names both estates and
  the delimited one names one; smoke-prod then plans with the request
  log on, and every LIST it sends ends in a slash while no request in
  the run names its neighbour; it tears down and the neighbour's keys
  and plan are unchanged. The delimiter is a line inside the binary, so
  the BREAK control rebuilds choudoufu with it dropped (go build
  -overlay, needs Go, refuses a release binary) and passes only when
  the wire shows smoke-prod fetching smoke-prod-eu's record (#1335).

- **a-wrong-bucket-is-refused** - *Claim 29: a record store bucket that
  cannot keep its records is refused by name before anything is
  applied.* A correct bucket costs an apply nothing; then five arms each
  break one thing with the AWS CLI - versioning suspended, no
  lifecycle, a lifecycle that exists and expires nothing, no
  public-access block, and a lifecycle that also expires current objects
  and so deletes records (#1377) - and each apply must fail naming it and
  the bucket, with the record store's object versions unchanged. A plan
  against the drifted bucket goes through, because the assertions do not
  run on every plan, and the step says what that costs. A brand-new
  estate's first plan is refused twice running and leaves nothing under
  its prefix. The BREAK control runs an arm with nothing corrupted and
  requires the refusal check to find nothing (#1339).

- **a-waiver-names-what-it-waives** - *Claim 30: a bucket waiver waives
  only the assertion it names, and says so on every run.* A bucket with
  no versioning and `allow_insecure = ["versioning"]`: the apply
  proceeds, warns with what the waiver costs, and says the bucket really
  does fail the waived assertion. A plan and a second apply of the
  unchanged estate each warn again. The lifecycle and the public-access
  block, broken in turn, are each still refused by name. A misspelt name
  is refused at configuration load. The BREAK control rebuilds choudoufu
  so the warning appears on an estate's first run only (go build
  -overlay, needs Go, refuses a release binary) and passes only when run
  two is caught proceeding in silence (#1340).

- **a-bulk-read-is-complete-or-it-fails** - *Claim 31: a record read
  that fails mid-fanout fails the read; a short map never reaches a
  plan.* Twelve record-backed resources, then a small proxy in front of
  S3 that can answer one record's GET with a 500, which nothing else can
  do from outside the binary. A control plan through the unarmed proxy
  is empty; one GET failed once leaves the plan true; the same GET
  failed every time makes the run refuse and name the record. The BREAK
  control rebuilds choudoufu so a failed GET drops its key (go build
  -overlay, needs Go, refuses a release binary) and passes only when the
  plan is caught proposing to create a resource that exists (#1336).
  Needs python3.

- **two-writers-one-record** - *Claim 32: two writers, one record: the
  loser is named, nothing is clobbered, and nothing is held.* Two
  checkouts of one estate contend for one record. The smoke proxy
  (`live/smoke/s3proxy.py`) holds both writers' conditional PUTs until
  both have arrived and releases them in a chosen order, alternating
  between rounds, so the race is a race every time. Exactly one apply
  lands per round; the other gets a record store write conflict naming
  the expected and the found version; no state lock appears in either
  output; the loser re-plans and converges; a writer killed with SIGKILL
  mid-write leaves nothing to unlock. The BREAK control rebuilds
  choudoufu with no If-Match on the write (go build -overlay, needs Go,
  refuses a release binary) and passes only when both applies are caught
  reporting success (#1338). Needs python3.

- **cas-holds-under-every-sse-flavour** - *Claim 33: compare-and-swap
  holds under every SSE flavour.* **Real AWS, maintainer-run, not in
  CI**: it refuses to start without `SMOKE_REAL_AWS=1`, because an
  emulator does not reproduce the ETag semantics it measures. It creates
  a bucket per flavour (SSE-S3, SSE-KMS with the AWS-managed key, SSE-KMS
  with a customer managed key, DSSE-KMS) and one KMS key, or reuses
  `SMOKE_KMS_KEY_ARN`, and removes what it made. Each flavour is first
  checked for what it is, including that its ETag is or is not the
  payload's MD5; then the record store's conformance suite runs against
  it, then an estate's whole lifecycle with every count checked. The
  BREAK control rebuilds choudoufu so the store checks each ETag against
  an MD5, and passes only when that binary works under SSE-S3 and fails
  on that check under all three KMS flavours (#1344). Needs Go and
  python3.

- **a-new-estate-writes-its-first-record** - *Claim 34: under the
  published IAM policy a new estate's first write succeeds, and so does
  every write after it.* **Real AWS, maintainer-run**
  (`SMOKE_REAL_AWS=1`). A control role that is allowed nothing is
  denied; a brand-new estate applies as its scoped role into an empty
  prefix under `render-policy.sh`'s output, then updates, replans and
  destroys with every count checked. Each policy is proven live by a
  marker statement before it is tested. The BREAK control changes one
  key, `s3:RequestObjectTag` to `s3:ExistingObjectTag`, and the first
  create must be denied (#1343). Needs jq.
- **one-bucket-many-estates** - *Claim 35: reading a neighbour's records
  takes two mistakes, not one.* **Real AWS, maintainer-run.** Two
  estates under their own roles in one bucket; one role is refused the
  other's records, outputs and listings, and the bare prefix; with its
  prefix deliberately widened it is still refused the read, by the tag;
  the same widened role overwriting and deleting a neighbour's object is
  shown allowed, because nothing but the prefix defends that; and a
  `--reads-outputs-of` grant opens the other estate's outputs and nothing else.
  The BREAK control widens the prefix and removes the tag's Deny, and
  the read must then succeed (#1343). Needs jq.
- **objects-carry-the-estate-tag** - *Claim 36: every record store
  object carries its estate's tag, and the tag is load-bearing.* **Real
  AWS, maintainer-run.** An estate applies as its scoped role and every
  object is read back tagged, records with the marker form of their
  address. One record is retagged out of band as another estate's under
  this estate's own prefix: the role is denied it, and the plan fails
  naming the record instead of planning around it. The BREAK control
  rebuilds choudoufu so the store sends no tags, and the published
  policy must deny its first write (#1337). Needs jq and Go.
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
  policy, and the run must be refused naming the key and its policy
  (#1345). Needs jq, just, node and npm.

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
