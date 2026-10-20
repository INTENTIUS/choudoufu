# gcp-a-markerless-type-is-refused
# CLAIM 51 (gcp) - An untaggable server-minted type is refused by name. ~2 min.
#
# This proof: on the GCP emulator, a hashicorp/google type whose identity
# the API mints and which has no labels argument is refused by name before
# anything is created. google_monitoring_alert_policy is one: Cloud
# Monitoring names each policy itself (projects/<p>/alertPolicies/<id>), so
# no run can compute what the object will be called, and there is no
# labels map to carry a marker. It is in the google section's markerless
# list in live/readiness.json (identity.GoogleMarkerlessTypes, 133 types
# across google and google-beta at 8.6.0), and the refusal is the
# markerless-type lint rule.
#
# Claim D4 of #2119. BREAK=1 swaps the alert policy for a service account,
# also unlabelled but client-named, in the same root, and requires the plan
# to go through with no markerless-type finding: the refusal is the
# type's, not the root's or the emulator's.
#
# Not yet run, so its cell reads unproven, and read from the code it may
# fail. Every live block implies a local record store (configs.Live), and
# lint admits a markerless type whose provider schema can be recorded as
# record-located (#270, identity.LocatedType) rather than refusing it.
# LocatedType does not exclude google types, while live/READINESS.md says
# the located-record route is not established for GCP. A lint unit check
# with a one-attribute schema (id, a string) admits this type under a
# live block. If the 8.6.0 schema does the same, step 1 fails, and the
# fix is a GCP exclusion in LocatedType or this cell restated. The plan
# never reaches Cloud Monitoring either way: a refused plan stops at lint,
# and an admitted one only proposes a create.

SMOKE_WORK="$SMOKE_WORKROOT/gcp-a-markerless-type-is-refused"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-gcp-markerless"
P="$SMOKE_GCP_PROJECT"

gcp_up
gcp_write_root "$SMOKE_WORK" "$ESTATE" || fail "gcp-a-markerless-type-is-refused" "could not write the root"
cat > "$SMOKE_WORK/main.tf" <<TF
resource "google_pubsub_topic" "events" {
  project = "$P"
  name    = "markerless-events"
}
TF
cat > "$SMOKE_WORK/minted.tf" <<TF
resource "google_monitoring_alert_policy" "cpu" {
  project      = "$P"
  display_name = "markerless-cpu"
  combiner     = "OR"
  conditions {
    display_name = "cpu above 90%"
    condition_threshold {
      filter          = "metric.type=\"compute.googleapis.com/instance/cpu/utilization\" AND resource.type=\"gce_instance\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0.9
      duration        = "60s"
    }
  }
}
TF
logged gcp-markerless-init "gcp-a-markerless-type-is-refused" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - swap the alert policy for a client-named type; the plan must go through"
  explain \
    "You asked for proof the refusal is the type's. The alert policy's" \
    "file is replaced by a service account: no labels argument either," \
    "but its identity is its account_id, which the configuration states." \
    "If the plan is still refused, something other than the server-minted" \
    "type was refusing it."
  cat > "$SMOKE_WORK/minted.tf" <<TF
resource "google_service_account" "app" {
  project    = "$P"
  account_id = "markerless-app"
}
TF
  cmd "choudoufu plan   (google_service_account.app in place of the alert policy)"
  BRC=0
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || BRC=$?
  { grep -E '^Plan:|^Error: ' <<< "$BOUT" || true; } | head -3 | evidence
  [ "$BRC" = "0" ] \
    || fail "gcp-a-markerless-type-is-refused" "BREAK: with the server-minted type gone the plan still exited $BRC: $BOUT"
  if grep -q 'markerless-type' <<< "$BOUT"; then
    fail "gcp-a-markerless-type-is-refused" "BREAK: a markerless-type finding with no markerless type declared: $BOUT"
  fi
  grep -qE 'Plan: 2 to add, 0 to change, 0 to destroy' <<< "$BOUT" \
    || fail "gcp-a-markerless-type-is-refused" "BREAK: the plan is not the two creates: $BOUT"
  proof "caught. Without the server-minted type the same root plans its two creates, so the refusal below is the type's and nothing else."
  exit 0
fi

step "1. a server-minted, unlabelled type is refused by name"
explain \
  "The root declares a Pub/Sub topic, which carries the estate's labels," \
  "and an alert policy, which can carry nothing and whose name only" \
  "Cloud Monitoring knows. Applying it would make an object no later run" \
  "could find, and every plan after would propose another. The plan" \
  "refuses it, names the block and the rule, and stops."
cmd "choudoufu plan"
RC=0
OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || RC=$?
printf '%s\n' "$OUT" > "$SMOKE_WORKROOT/logs/gcp-a-markerless-type-is-refused.plan"
{ grep -E '^Error: |google_monitoring_alert_policy|Rule: ' <<< "$OUT" || true; } | head -4 | evidence
echo "plan exit: $RC" | evidence
[ "$RC" = "1" ] \
  || fail "gcp-a-markerless-type-is-refused" "the plan exited $RC, want 1: a server-minted, unlabelled type must stop the plan (full plan in $SMOKE_WORKROOT/logs/gcp-a-markerless-type-is-refused.plan)"
grep -q 'Error: Resource type has nowhere to write an ownership marker' <<< "$OUT" \
  || fail "gcp-a-markerless-type-is-refused" "the plan is not refused under markerless-type's summary (full plan in $SMOKE_WORKROOT/logs/gcp-a-markerless-type-is-refused.plan)"
grep -q 'google_monitoring_alert_policy.cpu' <<< "$OUT" \
  || fail "gcp-a-markerless-type-is-refused" "the refusal does not name google_monitoring_alert_policy.cpu: $OUT"
grep -q 'Rule: markerless-type' <<< "$OUT" \
  || fail "gcp-a-markerless-type-is-refused" "the refusal does not name the markerless-type rule: $OUT"
if grep -qE '^Plan:' <<< "$OUT"; then
  fail "gcp-a-markerless-type-is-refused" "a plan was printed beside the refusal: $OUT"
fi
proof "refused with exit 1, the block and the rule named, and no plan printed."

step "2. nothing was created"
explain \
  "The refusal is at plan time, so the topic declared beside the alert" \
  "policy must not exist either: the estate is refused whole, not in part."
cmd "curl \$GCP_ENDPOINT/v1/projects/$P/topics/markerless-events"
if gcpl GET "v1/projects/$P/topics/markerless-events" >/dev/null 2>&1; then
  fail "gcp-a-markerless-type-is-refused" "the topic exists after a refused plan"
fi
echo "projects/$P/topics/markerless-events: 404" | evidence
proof "the emulator holds nothing from this root."

echo "  What you watched: a type whose name the API mints, with no labels"
echo "  argument, refused by name before anything was made. The veto is the"
echo "  same on GCP and AWS: no marker and no computable identity is no handle."
