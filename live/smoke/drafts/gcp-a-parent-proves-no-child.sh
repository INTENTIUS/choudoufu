# gcp-a-parent-proves-no-child
# CLAIM 49 (gcp) - A parent's marker never proves its child exists. ~3 min.
#
# This proof: on the GCP emulator, a derived child is read for itself on
# every plan, never assumed from its parent. Two children are deleted
# out of band: a subnetwork, whose parent network is itself
# declaration-carried, and a google_storage_bucket_iam_member, whose
# parent bucket carries the estate's labels. The next plan proposes
# recreating both while the network stands and the bucket keeps its
# labels, and the apply puts them back.
#
# Claim D2 of #2119. BREAK=1 runs the identical deletes and then holds the
# plan to what a plan that inferred the children from their parents would
# read, `No changes.`. That assertion must fail, and the control passes
# only when the plan instead proposes the two creates: the shape of the
# Kubernetes control in k8s-a-deleted-namespace-is-gone.
#
# Not yet run, so its cell reads unproven. Assumed rather than confirmed
# from the fork's docs: that floci-gcp serves a bucket's IAM policy at
# storage/v1/b/<bucket>/iam for GET and PUT, and Compute's subnetwork
# delete (lex00/floci-gcp#5 is the Compute verify gap).

SMOKE_WORK="$SMOKE_WORKROOT/gcp-a-parent-proves-no-child"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-gcp-parent"
P="$SMOKE_GCP_PROJECT"
R="$SMOKE_GCP_REGION"
BUCKET="${P}-parent-data"
SA="parent-reader"
MEMBER="serviceAccount:${SA}@${P}.iam.gserviceaccount.com"
ROLE="roles/storage.objectViewer"
STORE=$'    record_store "local" {\n      path = ".tofu-records"\n    }'

gcp_up
gcp_write_root "$SMOKE_WORK" "$ESTATE" "$P" "$STORE" || fail "gcp-a-parent-proves-no-child" "could not write the root"
cat > "$SMOKE_WORK/main.tf" <<TF
resource "google_compute_network" "main" {
  project                 = "$P"
  name                    = "parent-net"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "a" {
  project       = "$P"
  region        = "$R"
  name          = "parent-subnet-a"
  network       = google_compute_network.main.id
  ip_cidr_range = "10.30.0.0/24"
}

resource "google_service_account" "reader" {
  project    = "$P"
  account_id = "$SA"
}

resource "google_storage_bucket" "data" {
  project                     = "$P"
  name                        = "$BUCKET"
  location                    = "US"
  uniform_bucket_level_access = true
  force_destroy               = true
}

resource "google_storage_bucket_iam_member" "reader" {
  bucket = google_storage_bucket.data.name
  role   = "$ROLE"
  member = "serviceAccount:\${google_service_account.reader.email}"
}
TF
NET_PATH="compute/v1/projects/$P/global/networks/parent-net"
SUB_PATH="compute/v1/projects/$P/regions/$R/subnetworks/parent-subnet-a"
plan() { ( cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 ); }
bucket_estate() { gcpl GET "storage/v1/b/$BUCKET" | gcp_field 'd.get("labels", {}).get("tofu-estate", "")'; }

step "1. two parents, each with a derived child"
explain \
  "The network and the bucket are parents. The subnetwork is the" \
  "network's child and the IAM member is the bucket's; neither child" \
  "has a labels argument, so each is found by the identity its" \
  "configuration computes from its parent. The bucket carries the" \
  "estate's labels; the network, like its subnetwork, carries none."
cmd "choudoufu init && choudoufu apply -auto-approve"
logged gcp-parent-init "gcp-a-parent-proves-no-child" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-parent-proves-no-child" "apply failed: $APPLY_OUT"
{ grep -E 'Apply complete!' <<< "$APPLY_OUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 5 added' <<< "$APPLY_OUT" \
  || fail "gcp-a-parent-proves-no-child" "apply did not report 5 added: $APPLY_OUT"
[ "$(bucket_estate)" = "$ESTATE" ] \
  || fail "gcp-a-parent-proves-no-child" "the bucket does not carry tofu-estate=$ESTATE"
gcpl GET "$SUB_PATH" >/dev/null 2>&1 || fail "gcp-a-parent-proves-no-child" "the subnetwork is not on the emulator after the apply"
gcp_bucket_has_member "$BUCKET" "$ROLE" "$MEMBER" \
  || fail "gcp-a-parent-proves-no-child" "the bucket's IAM policy does not grant $ROLE to $MEMBER after the apply"
proof "5 created: the bucket labelled for $ESTATE, the subnetwork and the IAM member present, read back with the REST API."

step "2. delete both children out of band"
explain \
  "Someone deletes the subnetwork and takes the member out of the" \
  "bucket's policy, each with the REST API. Both parents are untouched:" \
  "the network is there and the bucket still carries the estate's" \
  "labels. The records and the cache still remember both children."
cmd "curl -X DELETE \$GCP_ENDPOINT/$SUB_PATH; PUT storage/v1/b/$BUCKET/iam without $MEMBER"
gcpl DELETE "$SUB_PATH" >/dev/null || fail "gcp-a-parent-proves-no-child" "could not delete the subnetwork"
gcp_bucket_drop_member "$BUCKET" "$ROLE" "$MEMBER" || fail "gcp-a-parent-proves-no-child" "could not take the member out of the bucket's policy"
if gcpl GET "$SUB_PATH" >/dev/null 2>&1; then
  fail "gcp-a-parent-proves-no-child" "the subnetwork answered after its delete"
fi
if gcp_bucket_has_member "$BUCKET" "$ROLE" "$MEMBER"; then
  fail "gcp-a-parent-proves-no-child" "the bucket's policy still grants $ROLE to $MEMBER after the PUT"
fi
gcpl GET "$NET_PATH" >/dev/null 2>&1 || fail "gcp-a-parent-proves-no-child" "the network went with its subnetwork"
[ "$(bucket_estate)" = "$ESTATE" ] || fail "gcp-a-parent-proves-no-child" "the bucket lost its label in the policy write"
echo "subnetwork: 404; $ROLE on $BUCKET: no $MEMBER; network: present; bucket tofu-estate: $ESTATE" | evidence
proof "both children gone, both parents present, the bucket still marked."

step "3. the next plan reads each child for itself"
explain \
  "A plan that took a marked, present parent as proof of its children" \
  "would read No changes here. The children are read on their own" \
  "identities, both are missing, so the plan must propose creating both" \
  "and nothing else."
cmd "choudoufu plan"
POUT="$(plan)" || fail "gcp-a-parent-proves-no-child" "plan failed: $POUT"
printf '%s\n' "$POUT" > "$SMOKE_WORKROOT/logs/gcp-a-parent-proves-no-child.plan"
{ grep -E '^Plan:|^No changes|will be (created|destroyed)|must be replaced' <<< "$POUT" || true; } | evidence

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - hold the plan to what an inferring plan reads; that must fail"
  explain \
    "You asked for proof the assertions can fail. A plan that inferred" \
    "the children from their parents reads No changes. This holds the" \
    "real plan to that and requires the check to refuse it, because the" \
    "plan proposed the two creates instead. Without this control the" \
    "creates above would read the same if every plan proposed them."
  BRC=0
  ( grep -q "No changes." <<< "$POUT" ) || BRC=$?
  [ "$BRC" != "0" ] \
    || fail "gcp-a-parent-proves-no-child" "BREAK: the plan reads No changes with both children deleted - it took the parents as proof"
  grep -qE 'Plan: 2 to add, 0 to change, 0 to destroy' <<< "$POUT" \
    || fail "gcp-a-parent-proves-no-child" "BREAK: the inferring check failed, but the plan is not the two creates either (full plan in $SMOKE_WORKROOT/logs/gcp-a-parent-proves-no-child.plan)"
  proof "caught. Held to No changes, the plan fails the check: it proposes the two creates, so the parents proved nothing about their children."
  exit 0
fi

if grep -q "No changes." <<< "$POUT"; then
  fail "gcp-a-parent-proves-no-child" "the plan reads No changes with both children deleted - it took the parents as proof"
fi
grep -qE 'Plan: 2 to add, 0 to change, 0 to destroy' <<< "$POUT" \
  || fail "gcp-a-parent-proves-no-child" "the plan is not exactly two creates (full plan in $SMOKE_WORKROOT/logs/gcp-a-parent-proves-no-child.plan)"
grep -qE 'google_compute_subnetwork\.a will be created' <<< "$POUT" \
  || fail "gcp-a-parent-proves-no-child" "the plan does not recreate google_compute_subnetwork.a: $POUT"
grep -qE 'google_storage_bucket_iam_member\.reader will be created' <<< "$POUT" \
  || fail "gcp-a-parent-proves-no-child" "the plan does not recreate google_storage_bucket_iam_member.reader: $POUT"
proof "two creates, one per deleted child, and no change to either parent."

step "4. apply - the children come back, the parents stay marked"
cmd "choudoufu apply -auto-approve"
APPLY2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-parent-proves-no-child" "apply failed: $APPLY2"
{ grep -E 'Apply complete!' <<< "$APPLY2" || true; } | evidence
grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$APPLY2" \
  || fail "gcp-a-parent-proves-no-child" "apply did not add exactly the two children: $APPLY2"
gcpl GET "$SUB_PATH" >/dev/null 2>&1 || fail "gcp-a-parent-proves-no-child" "the subnetwork is not back"
gcp_bucket_has_member "$BUCKET" "$ROLE" "$MEMBER" || fail "gcp-a-parent-proves-no-child" "the member is not back in the bucket's policy"
[ "$(bucket_estate)" = "$ESTATE" ] || fail "gcp-a-parent-proves-no-child" "the bucket lost its label"
REPLAN="$(plan)" || fail "gcp-a-parent-proves-no-child" "the replan failed: $REPLAN"
grep -q "No changes." <<< "$REPLAN" || fail "gcp-a-parent-proves-no-child" "the replan after the recreate is not empty: $REPLAN"
proof "both children recreated, the bucket still marked for $ESTATE, and the replan empty."

step "5. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-parent-proves-no-child" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "gcp-a-parent-proves-no-child" 5 "$DESTROY_OUT"
proof "5 destroyed."

echo "  What you watched: two derived children deleted under parents that"
echo "  stayed, one of them marked, and the next plan proposing both back."
echo "  A marker answers for the object that carries it and nothing below it."
