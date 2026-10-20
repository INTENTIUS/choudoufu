# gcp-greenfield
# CLAIM 7 (gcp) - Identity is a tag you can read, move and carve by. ~3 min.
#
# This proof: on the GCP emulator, the marker is two labels in the labels
# map the stock provider already sends on the create call: tofu-estate,
# and tofu-address holding a fixed-length encoding of the block address
# (#1121). Any client reads them back; live-ls lists the estate by them;
# the untaggable skeleton (a network and a service account) is carried by
# the declaration instead; a stripped label takes the object out of the
# estate and the next plan refuses it by name; and the estate lives its
# whole life without a state file.
#
# Not yet run, so its cell reads unproven. It needs
# floci-gcp to serve labels back as written (lex00/floci-gcp#6) and Cloud
# Asset search (lex00/floci-gcp#7) before steps 3 and the BREAK arm can
# hold. The ordinary run reads the labels with the emulator's own REST API,
# never through choudoufu. BREAK=1 strips tofu-estate from the bucket and
# requires the listing to lose it and the replan to refuse it by name,
# the Kubernetes sibling's control (k8s-greenfield, #1108, #1546): an
# unlabelled object at a declared name is nobody's, and GCP answers a
# create at a held name with 409, so the plan stops rather than proposing
# one.

SMOKE_WORK="$SMOKE_WORKROOT/gcp-greenfield"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-gcp"
P="$SMOKE_GCP_PROJECT"
BUCKET="${P}-logs"

gcp_up
gcp_write_root "$SMOKE_WORK" "$ESTATE" || fail "gcp-greenfield" "could not write the root"
cat > "$SMOKE_WORK/main.tf" <<TF
# Two labelled types and two the provider gives no labels argument. The
# network and the service account are the skeleton #1119 item 3 names:
# their identity is recomputed from this configuration.
resource "google_compute_network" "main" {
  project                 = "$P"
  name                    = "smoke-gcp-net"
  auto_create_subnetworks = false
}

resource "google_service_account" "app" {
  project    = "$P"
  account_id = "smoke-gcp-app"
}

resource "google_storage_bucket" "logs" {
  project                     = "$P"
  name                        = "$BUCKET"
  location                    = "US"
  uniform_bucket_level_access = true
  force_destroy               = true
}

resource "google_pubsub_topic" "events" {
  project = "$P"
  name    = "smoke-gcp-events"
}
TF

# bucket_labels and topic_labels read the labels map with the emulator's
# REST API: the same read any GCP client makes.
bucket_labels() { gcpl GET "storage/v1/b/$BUCKET" | gcp_field 'd.get("labels", {})'; }
topic_labels() { gcpl GET "v1/projects/$P/topics/smoke-gcp-events" | gcp_field 'd.get("labels", {})'; }
live_ls() { ( cd "$SMOKE_WORK" && chdf live-ls -estate="$ESTATE" -no-color . 2>&1 ); }

step "1. a GCP estate, one plain apply"
explain \
  "The configuration has a live block, a google provider pointed at the" \
  "emulator, and nothing else: no backend, nothing pre-created. Four" \
  "resources: a VPC network, a service account, a Cloud Storage bucket" \
  "and a Pub/Sub topic. Stock OpenTofu would write an authoritative" \
  "terraform.tfstate here; choudoufu keeps only a disposable cache."
cmd "choudoufu init && choudoufu apply -auto-approve"
logged gcp-greenfield-init "gcp-greenfield" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-greenfield" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 4 added' <<< "$APPLY_OUT" \
  || fail "gcp-greenfield" "apply did not report 4 added: $APPLY_OUT"
[ ! -f "$SMOKE_WORK/terraform.tfstate" ] \
  || fail "gcp-greenfield" "a terraform.tfstate appeared - a live-block apply must never write an authoritative state file"
proof "a network, a service account, a bucket and a topic created, and no terraform.tfstate exists."

step "2. the marker, read back with the REST API - no choudoufu in the loop"
explain \
  "If ownership really is on the object, any GCP client can read it. This" \
  "asks the emulator for the bucket's and the topic's labels directly." \
  "tofu-estate says which estate owns each one. tofu-address is the block" \
  "address, encoded: two 26-character base32 prefixes of its SHA-256," \
  "the block's and the instance's, because a label value allows only" \
  "63 lowercase characters (#1121). It is matched, never decoded, so the" \
  "check computes the expected value from the address and compares."
cmd "curl \$GCP_ENDPOINT/storage/v1/b/$BUCKET | jq .labels"
B_LABELS="$(bucket_labels)" || fail "gcp-greenfield" "could not read the bucket: $B_LABELS"
echo "bucket: $B_LABELS" | evidence
T_LABELS="$(topic_labels)" || fail "gcp-greenfield" "could not read the topic: $T_LABELS"
echo "topic:  $T_LABELS" | evidence
WANT_B="$(gcp_address_label google_storage_bucket.logs google_storage_bucket.logs)"
WANT_T="$(gcp_address_label google_pubsub_topic.events google_pubsub_topic.events)"
grep -q "\"tofu-estate\": \"$ESTATE\"" <<< "$B_LABELS" \
  || fail "gcp-greenfield" "the bucket carries no tofu-estate=$ESTATE label: $B_LABELS"
grep -q "\"tofu-address\": \"$WANT_B\"" <<< "$B_LABELS" \
  || fail "gcp-greenfield" "the bucket's tofu-address label is not $WANT_B, the encoding of google_storage_bucket.logs: $B_LABELS"
grep -q "\"tofu-estate\": \"$ESTATE\"" <<< "$T_LABELS" \
  || fail "gcp-greenfield" "the topic carries no tofu-estate=$ESTATE label: $T_LABELS"
grep -q "\"tofu-address\": \"$WANT_T\"" <<< "$T_LABELS" \
  || fail "gcp-greenfield" "the topic's tofu-address label is not $WANT_T, the encoding of google_pubsub_topic.events: $T_LABELS"
if grep -q '"goog-' <<< "$B_LABELS$T_LABELS"; then
  fail "gcp-greenfield" "a goog- label appeared on an object choudoufu created; that prefix is Google's own (#1121)"
fi
N_LABELS="$(gcpl GET "compute/v1/projects/$P/global/networks/smoke-gcp-net" | gcp_field 'd.get("labels")')"
[ -z "$N_LABELS" ] \
  || fail "gcp-greenfield" "the network came back with labels ($N_LABELS); Compute networks have no labels field, and nothing may have written one"
proof "both labels rode the create call, on the bucket and on the topic, with the address encoded as the ruling says. The network carries none: it is declaration-carried."

step "3. the inventory - live-ls lists the estate by its label, no state file"
explain \
  "The AWS inventory is one tag-index call and the Kubernetes one is a" \
  "label-selected list per kind. On GCP it is one Cloud Asset Inventory" \
  "search per project the estate's resources name, filtered on the" \
  "label. Each hit is joined back to its block by its tofu-address label." \
  "The network and the service account carry no label, so the search" \
  "cannot list them; live-ls names them as declaration-carried instead."
cmd "choudoufu live-ls -estate=$ESTATE ."
LS_OUT="$(live_ls)" || fail "gcp-greenfield" "live-ls failed: $LS_OUT"
grep -E 'carry its marker|^google_|address:|declaration-carried' <<< "$LS_OUT" | evidence
grep -q "Estate \"$ESTATE\": 2 resource(s) carry its marker" <<< "$LS_OUT" \
  || fail "gcp-greenfield" "live-ls did not list exactly the 2 labelled resources: $LS_OUT"
grep -q 'address: google_storage_bucket.logs  (declared)' <<< "$LS_OUT" \
  || fail "gcp-greenfield" "the bucket was not joined back to google_storage_bucket.logs: $LS_OUT"
grep -q 'address: google_pubsub_topic.events  (declared)' <<< "$LS_OUT" \
  || fail "gcp-greenfield" "the topic was not joined back to google_pubsub_topic.events: $LS_OUT"
if grep -q 'undeclared' <<< "$LS_OUT"; then
  fail "gcp-greenfield" "live-ls reports an undeclared resource in an estate whose every resource has a block: $LS_OUT"
fi
if grep -qiE 'Tagging index|IAM (role )?listing' <<< "$LS_OUT"; then
  fail "gcp-greenfield" "live-ls tried the AWS listing against a configuration with no aws provider: $LS_OUT"
fi
proof "2 labelled resources listed and joined to their blocks from the emulator's own index. No AWS call was attempted."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip the bucket's label; the listing must lose it and the replan must refuse it by name"
  explain \
    "You asked for proof the assertions can fail. This removes tofu-estate" \
    "from the bucket with a REST PATCH, behind choudoufu's back. If" \
    "live-ls still lists the bucket, the inventory is not reading the" \
    "label; if the next plan is empty, the marker is not what the plan" \
    "reads. An unlabelled object is nobody's, and Cloud Storage would" \
    "answer a create of the held name with 409, so the plan stops with" \
    "an error naming it instead of proposing that create."
  cmd "curl -X PATCH \$GCP_ENDPOINT/storage/v1/b/$BUCKET -d '{\"labels\":{\"tofu-estate\":null}}'"
  gcpl PATCH "storage/v1/b/$BUCKET" '{"labels":{"tofu-estate":null}}' >/dev/null \
    || fail "gcp-greenfield" "BREAK: could not strip the bucket's label"
  STRIPPED="$(bucket_labels)"
  if grep -q '"tofu-estate"' <<< "$STRIPPED"; then
    fail "gcp-greenfield" "BREAK: the PATCH answered but the bucket still carries tofu-estate: $STRIPPED"
  fi
  BLS="$(live_ls || true)"
  grep -E 'carry its marker' <<< "$BLS" | evidence
  if grep -q 'google_storage_bucket.logs' <<< "$BLS"; then
    fail "gcp-greenfield" "BREAK: live-ls still lists the bucket after its label was stripped - the inventory is not reading the label: $BLS"
  fi
  BRC=0
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || BRC=$?
  printf '%s\n' "$BOUT" > "$SMOKE_WORKROOT/logs/gcp-greenfield-break.plan"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "gcp-greenfield" "BREAK: the plan is still empty after the label was stripped - the marker is not what the plan reads"
  fi
  grep -E '^Error: |^Plan:' <<< "$BOUT" | head -2 | evidence
  echo "plan exit: $BRC" | evidence
  [ "$BRC" = "1" ] \
    || fail "gcp-greenfield" "BREAK: the plan exited $BRC, want 1: an unlabelled object holding the declared name must stop the plan (full plan in $SMOKE_WORKROOT/logs/gcp-greenfield-break.plan)"
  grep -q 'Error: Unlabelled live object holds the declared name' <<< "$BOUT" \
    || fail "gcp-greenfield" "BREAK: the plan does not refuse the unlabelled bucket by name (full plan in $SMOKE_WORKROOT/logs/gcp-greenfield-break.plan)"
  grep -q 'google_storage_bucket.logs' <<< "$BOUT" \
    || fail "gcp-greenfield" "BREAK: the refusal does not name google_storage_bucket.logs: $BOUT"
  gcpl GET "storage/v1/b/$BUCKET" >/dev/null 2>&1 \
    || fail "gcp-greenfield" "BREAK: the unlabelled bucket is gone - a refused object must be left alone"
  proof "caught, twice. The stripped label took the bucket out of the listing and out of the estate: the plan refuses it by name with exit 1 rather than proposing a create Cloud Storage would answer with 409."
  exit 0
fi

step "4. the replan - prior state rebuilt from the emulator"
explain \
  "With no state file, the next plan asks the cloud what exists. The" \
  "labelled resources are found by their markers; the network and the" \
  "service account by the identity their configuration computes. If" \
  "binding is right, the plan is empty."
cmd "choudoufu plan"
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "gcp-greenfield" "plan failed: $PLAN_OUT"
grep -E 'No changes\.' <<< "$PLAN_OUT" | head -1 | evidence
grep -q "No changes." <<< "$PLAN_OUT" || fail "gcp-greenfield" "replan is not empty: $PLAN_OUT"
proof "an empty plan, with nothing stored anywhere that could have remembered the estate."

step "5. the state cache - present, disposable, and never trusted"
explain \
  "The apply wrote a cache under .terraform/. Delete it, replan, and" \
  "require the identical answer."
cmd "rm .terraform/choudoufu-cache.tfstate && choudoufu plan"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
[ -f "$CACHE" ] || fail "gcp-greenfield" "no cache at $CACHE after a plain apply"
rm -f "$CACHE"
PLAN2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "gcp-greenfield" "plan without the cache failed: $PLAN2"
grep -E 'No changes\.' <<< "$PLAN2" | head -1 | evidence
grep -q "No changes." <<< "$PLAN2" || fail "gcp-greenfield" "deleting the cache changed the plan: $PLAN2"
proof "the cache was there and its loss changed nothing."

step "6. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-greenfield" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "gcp-greenfield" 4 "$DESTROY_OUT"
if gcpl GET "storage/v1/b/$BUCKET" >/dev/null 2>&1; then
  fail "gcp-greenfield" "the bucket still exists after destroy"
fi
if gcpl GET "compute/v1/projects/$P/global/networks/smoke-gcp-net" >/dev/null 2>&1; then
  fail "gcp-greenfield" "the network still exists after destroy"
fi
proof "4 destroyed, 0 added, 0 changed, read back with the REST API."

echo "  What you watched: a GCP estate live its whole life without an"
echo "  authoritative state file, its ownership carried as two labels any"
echo "  client can read, and its unlabelled skeleton carried by the"
echo "  declaration. The sweep and the project fence are claims 1 and 13 on GCP."
