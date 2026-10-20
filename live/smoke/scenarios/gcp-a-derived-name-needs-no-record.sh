# gcp-a-derived-name-needs-no-record
# CLAIM 48 (gcp) - A derived identity needs no record and no cache. ~3 min.
#
# This proof: on the GCP emulator, a type that has no labels argument is
# found again by the identity its configuration computes, and by nothing
# else. A VPC network and a firewall on it are applied; the record store
# and the state cache are then deleted, so nothing the tool keeps knows
# the estate; and the next plan reads `No changes.` from the emulator
# alone. Both are client-named types under #1121's ruling: project and
# name are the whole identity (live/READINESS.md, GCP section).
#
# Claim D1 of #2119. BREAK=1 changes the firewall's name in the
# configuration after the store and the cache are gone, so the derivation
# no longer matches the live object, and requires the plan to propose
# creating the derived name and leave the live one alone: if the empty
# plan came from anywhere but the derivation, the renamed block would
# bind to the old firewall too.
#
# Not yet run, so its cell reads unproven. Compute on floci-gcp is the
# one service the fork's own verify pass does not cover yet
# (lex00/floci-gcp#5), and the sweep's Cloud Asset search is not served
# (lex00/floci-gcp#7); that gap is a warning on every plan here and
# changes no plan, since neither type carries a label to search on.

SMOKE_WORK="$SMOKE_WORKROOT/gcp-a-derived-name-needs-no-record"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-gcp-derived"
P="$SMOKE_GCP_PROJECT"
NET="derived-net"
FW="derived-allow-ssh"
STORE=$'    record_store "local" {\n      path = ".tofu-records"\n    }'

gcp_up
gcp_write_root "$SMOKE_WORK" "$ESTATE" "$P" "$STORE" || fail "gcp-a-derived-name-needs-no-record" "could not write the root"
write_main() { # <firewall name>
  cat > "$SMOKE_WORK/main.tf" <<TF
resource "google_compute_network" "main" {
  project                 = "$P"
  name                    = "$NET"
  auto_create_subnetworks = false
}

resource "google_compute_firewall" "ssh" {
  project       = "$P"
  name          = "$1"
  network       = google_compute_network.main.name
  source_ranges = ["10.20.0.0/16"]
  allow {
    protocol = "tcp"
    ports    = ["22"]
  }
}
TF
}
write_main "$FW"
NET_PATH="compute/v1/projects/$P/global/networks/$NET"
FW_PATH="compute/v1/projects/$P/global/firewalls/$FW"
plan() { ( cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 ); }

step "1. a network and a firewall, neither of which can carry a label"
explain \
  "google_compute_network and google_compute_firewall have no labels" \
  "argument in hashicorp/google 8.6.0, so no marker can ride their create" \
  "call. Each is declaration-carried: its identity is its project and its" \
  "name, recomputed from this configuration on every run."
cmd "choudoufu init && choudoufu apply -auto-approve"
logged gcp-derived-init "gcp-a-derived-name-needs-no-record" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-derived-name-needs-no-record" "apply failed: $APPLY_OUT"
{ grep -E 'Apply complete!' <<< "$APPLY_OUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || fail "gcp-a-derived-name-needs-no-record" "apply did not report 2 added: $APPLY_OUT"
gcpl GET "$NET_PATH" >/dev/null 2>&1 || fail "gcp-a-derived-name-needs-no-record" "the network is not on the emulator after the apply"
FW_LABELS="$(gcpl GET "$FW_PATH" | gcp_field 'd.get("labels")')" \
  || fail "gcp-a-derived-name-needs-no-record" "the firewall is not on the emulator after the apply"
[ -z "$FW_LABELS" ] \
  || fail "gcp-a-derived-name-needs-no-record" "the firewall came back with labels ($FW_LABELS); it has no labels field and nothing may have written one"
proof "a network and a firewall on the emulator, read back with the REST API, neither carrying a label."

step "2. delete the record store and the state cache"
explain \
  "The records under .tofu-records and the cache under .terraform/ are" \
  "the only two things this tool keeps. With both gone, nothing local" \
  "says the estate ever applied; the cloud is all the next plan has."
cmd "rm -r .tofu-records .terraform/choudoufu-cache.tfstate"
[ -d "$SMOKE_WORK/.tofu-records" ] \
  || fail "gcp-a-derived-name-needs-no-record" "no .tofu-records directory after the apply; the records were written somewhere this scenario is not deleting"
rm -rf "$SMOKE_WORK/.tofu-records" "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
[ ! -e "$SMOKE_WORK/.tofu-records" ] && [ ! -e "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate" ] \
  || fail "gcp-a-derived-name-needs-no-record" "the record store or the cache is still there"
proof "an empty record store and no cache."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - rename the firewall in the configuration; the plan must propose creating it"
  explain \
    "You asked for proof the empty plan is the derivation's. The block now" \
    "names $FW-renamed, and the live firewall is still $FW. The derived" \
    "identity matches nothing, so the plan must propose a create and must" \
    "not touch $FW, which no record, no cache and no label ties to this" \
    "block any more. An empty plan here would mean the binding came from" \
    "somewhere other than the name."
  write_main "$FW-renamed"
  cmd "sed -i 's/$FW/$FW-renamed/' main.tf && choudoufu plan"
  BOUT="$(plan)" || fail "gcp-a-derived-name-needs-no-record" "BREAK: the plan failed: $BOUT"
  printf '%s\n' "$BOUT" > "$SMOKE_WORKROOT/logs/gcp-a-derived-name-needs-no-record-break.plan"
  { grep -E '^Plan:|^No changes|will be (created|destroyed)|must be replaced' <<< "$BOUT" || true; } | evidence
  if grep -q "No changes." <<< "$BOUT"; then
    fail "gcp-a-derived-name-needs-no-record" "BREAK: the plan is still empty with the firewall's name changed - the binding did not come from the derived name"
  fi
  grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$BOUT" \
    || fail "gcp-a-derived-name-needs-no-record" "BREAK: the plan is not exactly one create (full plan in $SMOKE_WORKROOT/logs/gcp-a-derived-name-needs-no-record-break.plan)"
  grep -qE 'google_compute_firewall\.ssh will be created' <<< "$BOUT" \
    || fail "gcp-a-derived-name-needs-no-record" "BREAK: the one create is not google_compute_firewall.ssh: $BOUT"
  gcpl GET "$FW_PATH" >/dev/null 2>&1 \
    || fail "gcp-a-derived-name-needs-no-record" "BREAK: the live firewall $FW is gone after a plan"
  proof "caught. A derivation that differs from the live name plans a create, so the empty plan below is the derived name binding and nothing else."
  exit 0
fi

step "3. the replan, from the emulator and the configuration alone"
explain \
  "With nothing stored, the plan computes each block's identity from its" \
  "configuration and reads that object from the emulator. If the" \
  "derivation is right, both are found and the plan is empty."
cmd "choudoufu plan"
PLAN_OUT="$(plan)" || fail "gcp-a-derived-name-needs-no-record" "plan failed: $PLAN_OUT"
{ grep -E '^No changes\.|^Plan:' <<< "$PLAN_OUT" || true; } | head -1 | evidence
grep -q "No changes." <<< "$PLAN_OUT" \
  || fail "gcp-a-derived-name-needs-no-record" "the plan with an empty record store and no cache is not empty: $PLAN_OUT"
proof "No changes., with an empty record store, no cache and no label anywhere."

step "4. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-derived-name-needs-no-record" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "gcp-a-derived-name-needs-no-record" 2 "$DESTROY_OUT"
if gcpl GET "$FW_PATH" >/dev/null 2>&1; then
  fail "gcp-a-derived-name-needs-no-record" "the firewall still exists after destroy"
fi
if gcpl GET "$NET_PATH" >/dev/null 2>&1; then
  fail "gcp-a-derived-name-needs-no-record" "the network still exists after destroy"
fi
proof "2 destroyed, read back with the REST API."

echo "  What you watched: two GCP types with nowhere to put a label found"
echo "  again by the names their configuration computes, with the record"
echo "  store and the cache both gone. The record is what a removed block"
echo "  needs (claim 50); a declared one needs only its configuration."
