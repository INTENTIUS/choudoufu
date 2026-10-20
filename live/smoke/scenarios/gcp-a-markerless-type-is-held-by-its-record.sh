# gcp-a-markerless-type-is-held-by-its-record
# CLAIM 51 (gcp) - A markerless type is held by its record, or refused. ~3 min.
#
# This proof: on the GCP emulator, a hashicorp/google type whose identity
# the API mints and which has no labels argument is held by the estate's
# record. google_secret_manager_secret_version is one: Secret Manager
# numbers each version itself, and the type has nowhere to carry a marker.
# It is in identity.GoogleMarkerlessTypes, and under a live block, whose
# record store is implied when none is declared, lint admits it as
# record-located (#270, identity.LocatedType): its identity, the secret and
# the version number, is written to the record at apply and read back from
# it on every plan. Its parent secret is a labelled type and carries the
# estate's marker as usual.
#
# Claim D4 of #2119, restated to the record rung: an untaggable,
# server-minted type is held by its record, or refused by name when its
# identity cannot be recorded. BREAK=1 is the second half. The same two
# blocks in a root with no live block have no record store to hold the
# identity, and live-check, which reads a configuration offline, must
# refuse the version by name under markerless-type.
#
# Why that BREAK and not a type whose identity a record cannot carry:
# measured on 2026-10-20 against the real hashicorp/google and google-beta
# 8.6.0 schemas, every member of identity.GoogleMarkerlessTypes that the
# provider serves has identity.LocatedType true, so no such GCP type exists
# at the pin. The no-store case is the one place the refusal still fires.
#
# Not yet run, so its cell reads unproven. Assumed: that floci-gcp serves
# Secret Manager under <endpoint>/v1/ (gcp_up's comment in lib.sh).

SMOKE_WORK="$SMOKE_WORKROOT/gcp-a-markerless-type-is-held-by-its-record"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-gcp-held"
P="$SMOKE_GCP_PROJECT"
SECRET="held-db"
STORE=$'    record_store "local" {\n      path = ".tofu-records"\n    }'
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"

gcp_up
resources() {
  cat <<TF
resource "google_secret_manager_secret" "db" {
  project   = "$P"
  secret_id = "$SECRET"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "db" {
  secret      = google_secret_manager_secret.db.id
  secret_data = "smoke-not-a-secret"
}
TF
}
gcp_write_root "$SMOKE_WORK" "$ESTATE" "$P" "$STORE" || fail "gcp-a-markerless-type-is-held-by-its-record" "could not write the root"
resources > "$SMOKE_WORK/main.tf"
SECRET_PATH="v1/projects/$P/secrets/$SECRET"
plan() { ( cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 ); }

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the same blocks with no live block; the version must be refused by name"
  explain \
    "You asked for proof the admission is the record's. This root has" \
    "the same secret and version and no live block, so there is no" \
    "estate and no record store: nothing could hold the version's" \
    "identity. live-check reads the configuration offline and must refuse" \
    "google_secret_manager_secret_version by name under markerless-type."
  NOSTORE="$SMOKE_WORK/no-store"
  mkdir -p "$NOSTORE"
  {
    printf 'terraform {\n  required_providers {\n%s\n  }\n}\n\n' "$(gcp_required_provider)"
    gcp_provider_block "$P"
  } > "$NOSTORE/versions.tf"
  resources > "$NOSTORE/main.tf"
  logged gcp-held-break-init "gcp-a-markerless-type-is-held-by-its-record" "BREAK: init failed" -- in_dir "$NOSTORE" chdf init -input=false -no-color
  cmd "choudoufu live-check .   (no live block)"
  BRC=0
  BOUT="$(cd "$NOSTORE" && chdf live-check -no-color . 2>&1)" || BRC=$?
  { grep -E 'cannot move|nowhere to write|google_secret_manager_secret_version|markerless-type' <<< "$BOUT" || true; } | head -5 | evidence
  echo "live-check exit: $BRC" | evidence
  [ "$BRC" != "0" ] \
    || fail "gcp-a-markerless-type-is-held-by-its-record" "BREAK: live-check passed a markerless type with no record store to hold it: $BOUT"
  grep -q 'Resource type has nowhere to write an ownership marker' <<< "$BOUT" \
    || fail "gcp-a-markerless-type-is-held-by-its-record" "BREAK: the refusal is not markerless-type's summary: $BOUT"
  grep -q 'google_secret_manager_secret_version' <<< "$BOUT" \
    || fail "gcp-a-markerless-type-is-held-by-its-record" "BREAK: the refusal does not name google_secret_manager_secret_version: $BOUT"
  grep -q '"markerless-type"' <<< "$BOUT" \
    || fail "gcp-a-markerless-type-is-held-by-its-record" "BREAK: the refusal does not point at markerless-type: $BOUT"
  proof "caught. With nowhere to record its identity the version is refused by name, so the admission below is the record's and nothing else."
  exit 0
fi

step "1. a secret and a version: one labelled, one with nowhere to carry a label"
explain \
  "The secret carries the estate's labels. The version has no labels" \
  "argument, and Secret Manager numbers it itself, so no run can compute" \
  "its identity from the configuration either. The plan admits it as" \
  "record-located: the apply writes its identity to the estate's record."
cmd "choudoufu init && choudoufu apply -auto-approve"
logged gcp-held-init "gcp-a-markerless-type-is-held-by-its-record" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-markerless-type-is-held-by-its-record" "apply failed: $APPLY_OUT"
{ grep -E 'Apply complete!' <<< "$APPLY_OUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || fail "gcp-a-markerless-type-is-held-by-its-record" "apply did not report 2 added: $APPLY_OUT"
OWNER="$(gcpl GET "$SECRET_PATH" | gcp_field 'd.get("labels", {}).get("tofu-estate", "")')"
[ "$OWNER" = "$ESTATE" ] \
  || fail "gcp-a-markerless-type-is-held-by-its-record" "the secret carries tofu-estate=$OWNER, want $ESTATE"
VERSIONS="$(gcpl GET "$SECRET_PATH/versions" | gcp_field '[v["name"] for v in d.get("versions", [])]')" \
  || fail "gcp-a-markerless-type-is-held-by-its-record" "could not list the secret's versions"
echo "versions: $VERSIONS" | evidence
grep -q "secrets/$SECRET/versions/" <<< "$VERSIONS" \
  || fail "gcp-a-markerless-type-is-held-by-its-record" "the secret has no version after the apply: $VERSIONS"
[ -n "$(find "$SMOKE_WORK/.tofu-records" -type d -name google_secret_manager_secret_version 2>/dev/null)" ] \
  || fail "gcp-a-markerless-type-is-held-by-its-record" "no record for google_secret_manager_secret_version under .tofu-records after the apply"
proof "planned and applied: the secret labelled for $ESTATE, a version the API numbered, and a record holding that version's identity."

step "2. delete the cache; the version is found again from its record"
explain \
  "With the cache gone, the plan has the secret's label and the" \
  "version's record. The version cannot be found by label or by a name" \
  "computed from the configuration; only the record says which version" \
  "this estate made. If the record holds it, the plan is empty."
cmd "rm .terraform/choudoufu-cache.tfstate && choudoufu plan"
rm -f "$CACHE"
PLAN_OUT="$(plan)" || fail "gcp-a-markerless-type-is-held-by-its-record" "plan failed: $PLAN_OUT"
{ grep -E '^No changes\.|^Plan:' <<< "$PLAN_OUT" || true; } | head -1 | evidence
grep -q "No changes." <<< "$PLAN_OUT" \
  || fail "gcp-a-markerless-type-is-held-by-its-record" "the plan without the cache is not empty, so the record did not hold the version: $PLAN_OUT"
if grep -q 'markerless-type' <<< "$PLAN_OUT"; then
  fail "gcp-a-markerless-type-is-held-by-its-record" "a markerless-type finding under a live block with a record store: $PLAN_OUT"
fi
proof "No changes., with no cache: the version is held by its record."

step "3. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-a-markerless-type-is-held-by-its-record" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "gcp-a-markerless-type-is-held-by-its-record" 2 "$DESTROY_OUT"
if gcpl GET "$SECRET_PATH" >/dev/null 2>&1; then
  fail "gcp-a-markerless-type-is-held-by-its-record" "the secret still exists after destroy"
fi
proof "2 destroyed, the secret gone (REST: 404)."

echo "  What you watched: a GCP type with no labels and an API-minted name"
echo "  held by the estate's record, found again with no cache, and the same"
echo "  block refused by name where there is no record to hold it."
