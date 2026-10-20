# gcp-a-removed-derived-block-is-destroyed
# CLAIM 50 (gcp) - A removed derived block is destroyed from its record. ~4 min.
#
# This proof: on the GCP emulator, a type with no labels argument cannot
# be found by the sweep, because the Cloud Asset search filters on the
# estate label and it carries none. So when its block is deleted, the
# estate's record is the only thing that still names it. With the record,
# the plan proposes the destroy at the deleted block's address. With the
# record lost as well, the plan cannot destroy it, and must say so by
# name rather than drop it without a word.
#
# Claim D3 of #2119, claim 1's sibling for this tier. BREAK=1 loses the
# firewall's record before its block is deleted and requires the destroy
# to disappear with it: if the plan still proposed it, the destroy came
# from something other than the record, and the first half would prove
# nothing about records.
#
# Not yet run, so its cell reads unproven. Step 4's warning names the
# type and the estate, never the address: the address is exactly what was
# lost with the record, so a warning that printed one would be guessing.
# Read from the code on 2026-10-20, no warning of that shape exists on the
# GCP path yet. The GCP leg's two warnings, "GCP sweep incomplete" and
# "GCP resource carries this estate's label and no declared address", name
# the estate and only labelled objects; the TYPE_NOT_TAGGABLE sweep gap
# names the type, raises no warning, and is filed only by the AWS
# per-type sweep. Until one is built, step 4 fails, and that is the
# finding #2119 lists as "to design". Compute on floci-gcp is
# lex00/floci-gcp#5.

SMOKE_WORK="$SMOKE_WORKROOT/gcp-a-removed-derived-block-is-destroyed"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-gcp-removed"
P="$SMOKE_GCP_PROJECT"
R="$SMOKE_GCP_REGION"
STORE=$'    record_store "local" {\n      path = ".tofu-records"\n    }'
RECORDS="$SMOKE_WORK/.tofu-records"
LOST="$SMOKE_WORK/lost-records"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"

gcp_up
gcp_write_root "$SMOKE_WORK" "$ESTATE" "$P" "$STORE" || fail "gcp-a-removed-derived-block-is-destroyed" "could not write the root"
cat > "$SMOKE_WORK/main.tf" <<TF
resource "google_compute_network" "main" {
  project                 = "$P"
  name                    = "removed-net"
  auto_create_subnetworks = false
}
TF
# Each removable block in a file of its own, so deleting the block is
# deleting the file.
cat > "$SMOKE_WORK/ssh.tf" <<TF
resource "google_compute_firewall" "ssh" {
  project       = "$P"
  name          = "removed-allow-ssh"
  network       = google_compute_network.main.name
  source_ranges = ["10.40.0.0/16"]
  allow {
    protocol = "tcp"
    ports    = ["22"]
  }
}
TF
cat > "$SMOKE_WORK/edge.tf" <<TF
resource "google_compute_subnetwork" "edge" {
  project       = "$P"
  region        = "$R"
  name          = "removed-subnet-edge"
  network       = google_compute_network.main.id
  ip_cidr_range = "10.40.1.0/24"
}
TF
NET_PATH="compute/v1/projects/$P/global/networks/removed-net"
FW_PATH="compute/v1/projects/$P/global/firewalls/removed-allow-ssh"
SUB_PATH="compute/v1/projects/$P/regions/$R/subnetworks/removed-subnet-edge"
plan() { ( cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 ); }
# lose_record <type> moves every record of one resource type out of the
# store. Each type here has one instance, so that is one record. A record
# key is tofu-records/<estate>/<type>/<encoded address>
# (projection.RecordKey), so the type is a directory of its own.
lose_record() {
  local dir
  dir="$(find "$RECORDS" -type d -name "$1" | head -1)"
  [ -n "$dir" ] || return 1
  mkdir -p "$LOST" && mv "$dir" "$LOST/$1"
}

step "1. a network, a firewall and a subnetwork, none of them labelled"
cmd "choudoufu init && choudoufu apply -auto-approve"
logged gcp-removed-init "gcp-a-removed-derived-block-is-destroyed" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-removed-derived-block-is-destroyed" "apply failed: $APPLY_OUT"
{ grep -E 'Apply complete!' <<< "$APPLY_OUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 3 added' <<< "$APPLY_OUT" \
  || fail "gcp-a-removed-derived-block-is-destroyed" "apply did not report 3 added: $APPLY_OUT"
( cd "$SMOKE_WORK" && find .tofu-records -type d -name 'google_*' | sort ) | evidence
for t in google_compute_firewall google_compute_subnetwork; do
  [ -n "$(find "$RECORDS" -type d -name "$t" 2>/dev/null)" ] \
    || fail "gcp-a-removed-derived-block-is-destroyed" "no record for $t under .tofu-records after the apply"
done
proof "3 created, and the estate's record store holds a record for the firewall and for the subnetwork."

step "2. delete the firewall's block; its record proposes the destroy"
explain \
  "The firewall carries no label, so the sweep cannot list it. Its" \
  "record still names google_compute_firewall.ssh, which no block" \
  "declares now, so the record is what must propose the destroy. The" \
  "cache is deleted first, so the record is the only thing that remembers."
cmd "rm ssh.tf .terraform/choudoufu-cache.tfstate && choudoufu plan"
rm -f "$SMOKE_WORK/ssh.tf" "$CACHE"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - lose the firewall's record first; the destroy must go with it"
  explain \
    "You asked for proof the destroy is the record's. This moves the" \
    "firewall's record out of the store before the plan. Nothing else" \
    "can name a deleted, unlabelled firewall, so the plan must propose no" \
    "destroy. If it still does, the destroy came from somewhere other" \
    "than the record."
  lose_record google_compute_firewall || fail "gcp-a-removed-derived-block-is-destroyed" "BREAK: could not find the firewall's record to lose"
  BOUT="$(plan)" || fail "gcp-a-removed-derived-block-is-destroyed" "BREAK: the plan failed: $BOUT"
  { grep -E '^Plan:|^No changes|will be destroyed' <<< "$BOUT" || true; } | evidence
  if grep -qE 'google_compute_firewall\.ssh will be destroyed' <<< "$BOUT"; then
    fail "gcp-a-removed-derived-block-is-destroyed" "BREAK: with its record lost the plan still proposes destroying the firewall - the destroy is not the record's doing: $BOUT"
  fi
  gcpl GET "$FW_PATH" >/dev/null 2>&1 \
    || fail "gcp-a-removed-derived-block-is-destroyed" "BREAK: the firewall is gone after a plan"
  proof "caught. Without its record the deleted firewall's destroy is not proposed, so the destroy below is the record's and nothing else."
  exit 0
fi

PLAN_OUT="$(plan)" || fail "gcp-a-removed-derived-block-is-destroyed" "plan failed: $PLAN_OUT"
{ grep -E '^Plan:|will be destroyed' <<< "$PLAN_OUT" || true; } | head -3 | evidence
grep -qE 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$PLAN_OUT" \
  || fail "gcp-a-removed-derived-block-is-destroyed" "the plan does not propose exactly one destroy: $PLAN_OUT"
grep -qE 'google_compute_firewall\.ssh will be destroyed' <<< "$PLAN_OUT" \
  || fail "gcp-a-removed-derived-block-is-destroyed" "the one destroy is not google_compute_firewall.ssh: $PLAN_OUT"
proof "exactly one destroy, at the address the deleted block declared and its record holds."

step "3. apply - the firewall goes, its network stays"
cmd "choudoufu apply -auto-approve"
APPLY2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-removed-derived-block-is-destroyed" "apply failed: $APPLY2"
{ grep -E 'Apply complete!' <<< "$APPLY2" || true; } | evidence
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$APPLY2" \
  || fail "gcp-a-removed-derived-block-is-destroyed" "apply did not destroy exactly one resource: $APPLY2"
if gcpl GET "$FW_PATH" >/dev/null 2>&1; then
  fail "gcp-a-removed-derived-block-is-destroyed" "the firewall still exists after the apply"
fi
gcpl GET "$NET_PATH" >/dev/null 2>&1 || fail "gcp-a-removed-derived-block-is-destroyed" "the network went with its firewall"
proof "the firewall is gone (REST: 404) and the network it sat on is still there."

step "4. lose the subnetwork's record, then delete its block"
explain \
  "Now both things that name the subnetwork go at once: its record is" \
  "moved out of the store, its block is deleted, and the cache with" \
  "them. No plan can propose destroying an object nothing names, so" \
  "the subnetwork stays live. What the plan must not do is say nothing:" \
  "a warning names the type, google_compute_subnetwork, and the estate," \
  "$ESTATE, and never an address, because the address went with the record."
cmd "mv .tofu-records/.../google_compute_subnetwork lost-records/ && rm edge.tf && choudoufu plan"
lose_record google_compute_subnetwork || fail "gcp-a-removed-derived-block-is-destroyed" "could not find the subnetwork's record to lose"
rm -f "$SMOKE_WORK/edge.tf" "$CACHE"
LOUT="$(plan)" || fail "gcp-a-removed-derived-block-is-destroyed" "the plan with the record lost failed: $LOUT"
printf '%s\n' "$LOUT" > "$SMOKE_WORKROOT/logs/gcp-a-removed-derived-block-is-destroyed-lost.plan"
{ grep -E '^Plan:|^No changes|will be destroyed|^Warning:' <<< "$LOUT" || true; } | head -8 | evidence
if grep -qE 'will be destroyed|[1-9][0-9]* to destroy' <<< "$LOUT"; then
  fail "gcp-a-removed-derived-block-is-destroyed" "the plan proposes a destroy with nothing left to name the subnetwork: $LOUT"
fi
# warnings prints each "Warning:" diagnostic of the plan as one line, its
# summary and detail joined, so one grep can ask what a single warning says.
warnings() {
  awk '
    /^Warning: / { if (w != "") print w; w = $0; next }
    /^(Error: |Plan: |No changes|Not swept for removal|Swept via parent read)/ { if (w != "") print w; w = ""; next }
    w != "" { w = w " " $0 }
    END { if (w != "") print w }
  ' <<< "$1"
}
NAMED="$(warnings "$LOUT" | grep -F 'google_compute_subnetwork' | grep -F "$ESTATE" || true)"
[ -n "$NAMED" ] \
  || fail "gcp-a-removed-derived-block-is-destroyed" "no warning names both google_compute_subnetwork and estate $ESTATE: the lost subnetwork dropped out silently (full plan in $SMOKE_WORKROOT/logs/gcp-a-removed-derived-block-is-destroyed-lost.plan)"
if grep -qF 'google_compute_subnetwork.edge' <<< "$NAMED"; then
  fail "gcp-a-removed-derived-block-is-destroyed" "the warning names google_compute_subnetwork.edge, an address nothing left in this run can know: $NAMED"
fi
printf '%s\n' "$NAMED" | cut -c1-200 | evidence
gcpl GET "$SUB_PATH" >/dev/null 2>&1 \
  || fail "gcp-a-removed-derived-block-is-destroyed" "the subnetwork is gone; nothing named it, so nothing may have deleted it"
proof "no destroy proposed, the subnetwork still live, and a warning naming its type and the estate, never its address."

step "5. clean up by hand, and destroy exactly what remains"
gcpl DELETE "$SUB_PATH" >/dev/null || fail "gcp-a-removed-derived-block-is-destroyed" "could not delete the subnetwork by hand"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-removed-derived-block-is-destroyed" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "gcp-a-removed-derived-block-is-destroyed" 1 "$DESTROY_OUT"
proof "1 destroyed. The estate is gone."

echo "  What you watched: an unlabelled firewall whose block was deleted"
echo "  destroyed from its record, and an unlabelled subnetwork whose record"
echo "  and block went together left live, named by type and estate and"
echo "  never dropped without a word. Keep the record store and the second"
echo "  case is gone."
