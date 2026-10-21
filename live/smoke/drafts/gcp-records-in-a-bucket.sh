# gcp-records-in-a-bucket
# CLAIM 29 (gcp) - The record store refuses what it can't trust. ~4 min.
#
# This proof: record_store "gcs" keeps an estate's records as objects in a
# Cloud Storage bucket, and refuses a bucket that fails its contract
# (examples/record-store-gcs/CONTRACT.md): Object Versioning on, a
# lifecycle rule that deletes noncurrent versions and none that deletes a
# live record, and public access prevention enforced. A wrong bucket is
# refused before anything is created, each setting by name; the same bucket
# put right is accepted and holds the records; a rule that would delete
# live records is refused and allow_insecure does not waive it; and the
# estate replans empty from the records alone.
#
# The GCP sibling of a-wrong-bucket-is-refused (S3) and
# k8s-records-in-the-cluster. BREAK=1 removes the fences: the wrong bucket's
# three settings are waived with allow_insecure, and the apply the contract
# refused must then go through, so the refusal is the contract's doing.
#
# Not yet run, so its cell reads unproven. record_store "gcs" (#2122)
# reaches the emulator through its own storage_custom_endpoint argument,
# stock gcs's name for it.

SMOKE_WORK="$SMOKE_WORKROOT/gcp-records-in-a-bucket"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-gcp-records"
P="$SMOKE_GCP_PROJECT"
RECORDS="${P}-records"

gcp_up

# store_block [allow_insecure list] prints the record_store block.
store_block() {
  printf '    record_store "gcs" {\n      bucket                  = "%s"\n      storage_custom_endpoint = "%s/storage/v1/"\n' "$RECORDS" "$GCP_ENDPOINT"
  [ -z "${1:-}" ] || printf '      allow_insecure          = %s\n' "$1"
  printf '    }'
}
write_root() { gcp_write_root "$SMOKE_WORK" "$ESTATE" "$P" "$(store_block "${1:-}")" || fail "gcp-records" "could not write the root"; }
write_root
cat > "$SMOKE_WORK/main.tf" <<TF
resource "google_pubsub_topic" "events" {
  project = "$P"
  name    = "records-events"
}

resource "google_service_account" "app" {
  project    = "$P"
  account_id = "records-app"
}
TF
record_objects() { gcpl GET "storage/v1/b/$RECORDS/o" | gcp_field '[o["name"] for o in d.get("items", [])]'; }

step "1. a bucket that fails all three settings"
explain \
  "The bucket is made with the REST API and nothing else: no versioning," \
  "no lifecycle, public access prevention left inherited. Each of those" \
  "is a way to lose a record or publish one, so the store refuses the" \
  "bucket before the estate writes anything."
cmd "curl -X POST \$GCP_ENDPOINT/storage/v1/b?project=$P -d '{\"name\":\"$RECORDS\"}'"
gcpl POST "storage/v1/b?project=$P" "{\"name\":\"$RECORDS\",\"location\":\"US\",\"iamConfiguration\":{\"uniformBucketLevelAccess\":{\"enabled\":true}}}" >/dev/null \
  || fail "gcp-records" "could not create the records bucket"
gcpl GET "storage/v1/b/$RECORDS" | gcp_field '{"versioning": d.get("versioning"), "lifecycle": d.get("lifecycle"), "pap": d.get("iamConfiguration", {}).get("publicAccessPrevention")}' | evidence
logged gcp-records-init "gcp-records" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - waive the three settings; the refused apply must go through"
  explain \
    "You asked for proof the refusal is the contract's. allow_insecure" \
    "names all three settings, so the store opens the same wrong bucket" \
    "with a warning per setting. If the apply still fails, something other" \
    "than the contract was refusing it."
  write_root '["object_versioning", "noncurrent_lifecycle", "public_access_prevention"]'
  cmd "choudoufu apply -auto-approve   (allow_insecure = all three)"
  BOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "gcp-records" "BREAK: with every setting waived the apply still failed - the refusal was not the contract's: $BOUT"
  grep -E 'Apply complete!|is waived' <<< "$BOUT" | head -4 | evidence
  grep -qE 'Apply complete! Resources: 2 added' <<< "$BOUT" \
    || fail "gcp-records" "BREAK: the waived apply did not add the estate's 2 resources: $BOUT"
  [ -n "$(record_objects | tr -d '[]" ')" ] \
    || fail "gcp-records" "BREAK: the waived apply wrote no record object into $RECORDS"
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  proof "caught. With the three settings waived the store opened the wrong bucket and the apply went through, so the refusal below is the contract and nothing else."
  exit 0
fi

cmd "choudoufu plan"
RRC=0
ROUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || RRC=$?
grep -E '^Error: ' <<< "$ROUT" | evidence
[ "$RRC" = "1" ] || fail "gcp-records" "the plan against the wrong bucket exited $RRC, want 1: $ROUT"
for s in object_versioning noncurrent_lifecycle public_access_prevention; do
  grep -q "Error: The record store bucket fails its $s assertion" <<< "$ROUT" \
    || fail "gcp-records" "the refusal does not name $s: $ROUT"
done
if gcpl GET "v1/projects/$P/topics/records-events" >/dev/null 2>&1; then
  fail "gcp-records" "the refused plan left a topic behind"
fi
proof "refused, each of the three settings named, and nothing created."

step "2. the bucket put right, and the estate's records written into it"
explain \
  "Versioning on, one rule deleting versions thirty days after they stop" \
  "being live, and public access prevention enforced: the contract's" \
  "\"correct bucket\". The same configuration now applies, and every" \
  "record is an object under the estate's prefix."
cmd "curl -X PATCH \$GCP_ENDPOINT/storage/v1/b/$RECORDS -d '{versioning, lifecycle, iamConfiguration}'"
gcpl PATCH "storage/v1/b/$RECORDS" \
  '{"versioning":{"enabled":true},"lifecycle":{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]},"iamConfiguration":{"publicAccessPrevention":"enforced","uniformBucketLevelAccess":{"enabled":true}}}' >/dev/null \
  || fail "gcp-records" "could not put the bucket right"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-records" "the apply against the corrected bucket failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || fail "gcp-records" "apply did not report 2 added: $APPLY_OUT"
OBJS="$(record_objects)"
echo "$OBJS" | evidence
grep -q "$ESTATE" <<< "$OBJS" \
  || fail "gcp-records" "no object in $RECORDS names estate $ESTATE: $OBJS"
[ ! -d "$SMOKE_WORK/.tofu-records" ] \
  || fail "gcp-records" "a local .tofu-records directory appeared; the records belong in the bucket"
proof "accepted, and the records are objects in the bucket, read back with the REST API."

step "3. replan from the records alone"
explain \
  "Delete the cache and replan. The service account carries no label, so" \
  "its record is what the plan reads it back from; an empty plan means" \
  "the bucket holds what the estate needs."
rm -f "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "gcp-records" "the replan failed: $PLAN_OUT"
grep -E 'No changes\.' <<< "$PLAN_OUT" | head -1 | evidence
grep -q "No changes." <<< "$PLAN_OUT" || fail "gcp-records" "the replan from the bucket's records is not empty: $PLAN_OUT"
proof "empty, with no cache and no local store."

step "4. a rule that deletes live records is refused, and cannot be waived"
explain \
  "A Delete rule with an age and no noncurrent condition matches live" \
  "objects: it deletes records on a timer. The store refuses it under its" \
  "own headline, and allow_insecure naming noncurrent_lifecycle does not" \
  "cover it."
gcpl PATCH "storage/v1/b/$RECORDS" '{"lifecycle":{"rule":[{"action":{"type":"Delete"},"condition":{"age":30}}]}}' >/dev/null \
  || fail "gcp-records" "could not install the live-deleting rule"
write_root '["noncurrent_lifecycle"]'
LRC=0
LOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || LRC=$?
grep -E '^Error: ' <<< "$LOUT" | evidence
[ "$LRC" = "1" ] || fail "gcp-records" "the plan against a live-deleting rule exited $LRC, want 1: $LOUT"
grep -q "Error: The record store bucket's lifecycle deletes records" <<< "$LOUT" \
  || fail "gcp-records" "the refusal is not the live-deleting rule's headline: $LOUT"
proof "refused under its own headline with the waiver in place."

step "5. put the rule back and destroy exactly what was made"
gcpl PATCH "storage/v1/b/$RECORDS" '{"lifecycle":{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]}}' >/dev/null \
  || fail "gcp-records" "could not restore the noncurrent rule"
write_root
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-records" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "gcp-records" 2 "$DESTROY_OUT"
proof "2 destroyed."

echo "  What you watched: a GCP estate keep its records in a bucket it can"
echo "  trust, and refuse one it cannot, each setting by name, before writing"
echo "  anything. The same contract the S3 store and the cluster store hold."
