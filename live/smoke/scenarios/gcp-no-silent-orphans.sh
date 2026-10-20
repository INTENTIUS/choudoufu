# gcp-no-silent-orphans
# CLAIM 1 (gcp) - Owned resources never fall out of a plan. ~3 min.
#
# This proof: on the GCP emulator, a resource this estate owns cannot drop
# out of its plans unnoticed. A deleted block's bucket is proposed for
# removal; a crash-shaped topic carrying the estate's label for an address
# nothing declares is named in the plan's own warning; and an object whose
# label was stripped is nobody's and is left alone.
#
# The GCP sibling of claim 1, under #1121's sweep ruling: Cloud Asset
# Inventory search per project, filtered on the estate label. A GCP address
# label is matched, never decoded, so an orphan whose label encodes nothing
# the configuration declares is reported and never planned; the estate's
# record for a block is what proposes that block's removal. Step 3 is the
# reported half and step 4 the planned half.
#
# Not yet run, so its cell reads unproven, and it needs
# Cloud Asset search on the emulator (lex00/floci-gcp#7) and labels served
# back as written (lex00/floci-gcp#6). BREAK=1 strips tofu-estate from the
# bucket before its block is deleted and requires the replan to leave it
# alone: an unlabelled object is not this estate's to destroy, so a plan
# that still proposed it would be acting on something other than the
# label.

SMOKE_WORK="$SMOKE_WORKROOT/gcp-no-silent-orphans"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-gcp-orphans"
P="$SMOKE_GCP_PROJECT"
DOOMED="${P}-doomed"

gcp_up
gcp_write_root "$SMOKE_WORK" "$ESTATE" || fail "gcp-no-silent-orphans" "could not write the root"
cat > "$SMOKE_WORK/main.tf" <<TF
resource "google_pubsub_topic" "events" {
  project = "$P"
  name    = "orphans-events"
}
TF
# The orphan-to-be, in a file of its own so deleting the block is deleting
# the file.
cat > "$SMOKE_WORK/doomed.tf" <<TF
resource "google_storage_bucket" "doomed" {
  project       = "$P"
  name          = "$DOOMED"
  location      = "US"
  force_destroy = true
}
TF

step "1. stand the estate up"
cmd "choudoufu init && choudoufu apply -auto-approve"
logged gcp-no-silent-orphans-init "gcp-no-silent-orphans" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-no-silent-orphans" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || fail "gcp-no-silent-orphans" "apply did not report 2 added: $APPLY_OUT"
D_LABELS="$(gcpl GET "storage/v1/b/$DOOMED" | gcp_field 'd.get("labels", {})')"
grep -q "\"tofu-estate\": \"$ESTATE\"" <<< "$D_LABELS" \
  || fail "gcp-no-silent-orphans" "the bucket carries no tofu-estate=$ESTATE label: $D_LABELS"
proof "a topic and a bucket, each carrying the estate's labels from the create call."

step "2. the crash shape - a topic created, then everyone forgot"
explain \
  "Simulate an apply that died right after a create succeeded: this makes" \
  "a Pub/Sub topic with the REST API and writes the estate's labels on it," \
  "with a tofu-address for google_pubsub_topic.crashed, an address no" \
  "block declares and no record holds."
CRASHED_ADDR="$(gcp_address_label google_pubsub_topic.crashed google_pubsub_topic.crashed)"
cmd "curl -X PUT \$GCP_ENDPOINT/v1/projects/$P/topics/orphans-crashed -d '{\"labels\":{...}}'"
gcpl PUT "v1/projects/$P/topics/orphans-crashed" \
  "{\"labels\":{\"tofu-estate\":\"$ESTATE\",\"tofu-address\":\"$CRASHED_ADDR\"}}" >/dev/null \
  || fail "gcp-no-silent-orphans" "could not create the crash-shaped topic"
echo "created projects/$P/topics/orphans-crashed, labelled tofu-address=$CRASHED_ADDR - and no run has ever heard of it" | evidence
proof "a labelled topic nothing declares and nothing recorded."

step "3. the next plan names the crash-shaped topic - nobody had to remember"
explain \
  "The plan searches the project for the estate's label. The topic's" \
  "address label encodes nothing this configuration declares, and an" \
  "encoded address cannot be turned back into a block, so the plan" \
  "reports it by name as owned and undeclared and does not destroy it." \
  "On GCP that warning is what keeps it from being a silent orphan."
cmd "choudoufu plan"
POUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "gcp-no-silent-orphans" "plan failed: $POUT"
grep -E "GCP resource carries this estate's label|orphans-crashed|^No changes|^Plan:" <<< "$POUT" | head -4 | evidence
grep -q "GCP resource carries this estate's label and no declared address" <<< "$POUT" \
  || fail "gcp-no-silent-orphans" "the plan does not warn about the crash-shaped topic - a silent orphan: $POUT"
grep -q "orphans-crashed" <<< "$POUT" \
  || fail "gcp-no-silent-orphans" "the warning does not name the crash-shaped topic: $POUT"
if grep -qE 'orphans-crashed.*will be destroyed|google_pubsub_topic\.crashed will be destroyed' <<< "$POUT"; then
  fail "gcp-no-silent-orphans" "the plan proposes destroying a resource whose address it cannot decode: $POUT"
fi
proof "the crash-shaped topic surfaced by its own label, named in the plan, and left for an operator: reported, never guessed at."

step "4. delete the bucket's block from source"
explain \
  "The everyday case. The live bucket still carries the estate's labels" \
  "and its record still names google_storage_bucket.doomed, which no" \
  "block declares now, so the next plan must propose destroying it."
cmd "rm doomed.tf && choudoufu plan"
rm -f "$SMOKE_WORK/doomed.tf"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip the bucket's label; the replan must leave it alone"
  explain \
    "You asked for proof the assertions can fail. This removes tofu-estate" \
    "from the bucket with a REST PATCH. It is now nobody's, and a plan" \
    "that still proposed destroying it would be acting on something other" \
    "than the label: the whole scenario would be scenery."
  cmd "curl -X PATCH \$GCP_ENDPOINT/storage/v1/b/$DOOMED -d '{\"labels\":{\"tofu-estate\":null}}'"
  gcpl PATCH "storage/v1/b/$DOOMED" '{"labels":{"tofu-estate":null}}' >/dev/null \
    || fail "gcp-no-silent-orphans" "BREAK: could not strip the bucket's label"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  grep -E '^No changes|^Plan:|^Error' <<< "$BOUT" | head -2 | evidence
  if grep -qE 'google_storage_bucket\.doomed.* will be destroyed' <<< "$BOUT"; then
    fail "gcp-no-silent-orphans" "BREAK: the plan still proposes destroying the unlabelled bucket - the destroy is not the label's doing: $BOUT"
  fi
  gcpl GET "storage/v1/b/$DOOMED" >/dev/null 2>&1 \
    || fail "gcp-no-silent-orphans" "BREAK: the bucket is gone"
  proof "caught. With its label gone the bucket is foreign and the plan leaves it alone, so the removal below is proposed because of the label and nothing else."
  exit 0
fi

PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "gcp-no-silent-orphans" "plan failed: $PLAN_OUT"
grep -E '^Plan:|will be destroyed' <<< "$PLAN_OUT" | head -3 | evidence
grep -qE 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$PLAN_OUT" \
  || fail "gcp-no-silent-orphans" "the plan does not propose exactly one destroy: $PLAN_OUT"
grep -qE 'google_storage_bucket\.doomed will be destroyed' <<< "$PLAN_OUT" \
  || fail "gcp-no-silent-orphans" "the one destroy is not google_storage_bucket.doomed: $PLAN_OUT"
proof "exactly one destroy, the bucket nobody declares, at the address its label and its record both name."

step "5. apply - the orphan goes, the crash-shaped topic stays for its operator"
cmd "choudoufu apply -auto-approve"
APPLY2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-no-silent-orphans" "apply failed: $APPLY2"
grep -E 'Apply complete' <<< "$APPLY2" | head -1 | evidence
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$APPLY2" \
  || fail "gcp-no-silent-orphans" "apply did not destroy exactly one resource: $APPLY2"
if gcpl GET "storage/v1/b/$DOOMED" >/dev/null 2>&1; then
  fail "gcp-no-silent-orphans" "the orphaned bucket still exists after apply"
fi
gcpl GET "v1/projects/$P/topics/orphans-crashed" >/dev/null 2>&1 \
  || fail "gcp-no-silent-orphans" "the crash-shaped topic is gone; it was reported, not planned, and nothing may have deleted it"
proof "the orphan is gone and the reported topic stands."

step "6. clean up by hand, and destroy exactly what remains"
gcpl DELETE "v1/projects/$P/topics/orphans-crashed" >/dev/null \
  || fail "gcp-no-silent-orphans" "could not delete the crash-shaped topic"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-no-silent-orphans" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "gcp-no-silent-orphans" 1 "$DESTROY_OUT"
proof "1 destroyed. The estate is gone."

echo "  What you watched: a bucket whose block was deleted found by its label"
echo "  and removed, and a labelled topic nobody declared named in the plan"
echo "  rather than guessed at. On GCP the sweep is a Cloud Asset search per"
echo "  project, and the encoded address label is matched, never decoded."
