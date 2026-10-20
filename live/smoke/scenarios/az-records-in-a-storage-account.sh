# az-records-in-a-storage-account
# CLAIM 29 (azure) - The record store refuses what it can't trust. ~6 min.
#
# This proof: on floci-az, record_store "azurerm" opens a storage account
# with blob versioning on, a lifecycle rule that deletes previous versions
# only, and public blob access off, writes its records as blobs carrying
# the estate as a blob index tag, and refuses by name, with nothing
# written, each account that fails one of the three: versioning off, no
# lifecycle rule, public access allowed, and a rule that deletes current
# records (the one refusal allow_insecure cannot waive). BREAK=1 removes
# the fences - all three settings at once - and waives the three with
# allow_insecure: the same apply must then go through and name each waiver,
# so the main arm's refusals are shown to be the checks' and not some other
# failure of the account.
#
# Written for GitHub issue #1120 (unit 15). Not yet run, so its cell reads
# unproven until the maintainer runs both arms on floci-az and the evidence
# file lands. Blob service properties, the lifecycle policy and
# allowBlobPublicAccess (071516d), tags on storage accounts (95f1bc9) and a
# configurable storage suffix (c760e26), which azure_up sets so the store's
# blob calls stay on the emulator, are served by floci-az since 30000b0, not
# yet run.
#
# What floci-az cannot answer today (live/FLOCI.md, Azure): it stores the
# three settings and reads them back, but makes no blob versions and runs
# no lifecycle rule; every plan's sweep is a Resource Graph query, and its
# KQL subset refuses the query's union, mv-expand and bag_keys; and it
# stores ABAC conditions on role assignments without evaluating them, so
# the read fence (live/azure/record-store-abac.condition) is not measured
# here.

SMOKE_WORK="$SMOKE_WORKROOT/az-records-in-a-storage-account"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-az-records"
RG="smoke-az-records-rg"
ACCOUNT="smokeazrecords"
CONTAINER="records"
SUB="/subscriptions/${AZURE_SUBSCRIPTION_ID}"
ST_API="2023-05-01"
ACCT_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.Storage/storageAccounts/${ACCOUNT}"

azure_up

# The account is the operator's, made before the first plan, with ARM calls
# a portal or `az` would send.
LIFECYCLE_OK='{"properties":{"policy":{"rules":[{"name":"expire-previous-record-versions","enabled":true,"type":"Lifecycle","definition":{"filters":{"blobTypes":["blockBlob"],"prefixMatch":["records/"]},"actions":{"version":{"delete":{"daysAfterCreationGreaterThan":30}}}}}]}}}'
LIFECYCLE_EATS='{"properties":{"policy":{"rules":[{"name":"expire-records","enabled":true,"type":"Lifecycle","definition":{"filters":{"blobTypes":["blockBlob"],"prefixMatch":["records/"]},"actions":{"baseBlob":{"delete":{"daysAfterModificationGreaterThan":30}},"version":{"delete":{"daysAfterCreationGreaterThan":30}}}}}]}}}'
account_set() { # <versioning true|false> <public true|false> <lifecycle json|none>
  arm PUT "${ACCT_ID}/blobServices/default?api-version=${ST_API}" "{\"properties\":{\"isVersioningEnabled\":$1}}" >/dev/null || return 1
  arm PATCH "${ACCT_ID}?api-version=${ST_API}" "{\"properties\":{\"allowBlobPublicAccess\":$2}}" >/dev/null || return 1
  if [ "$3" = "none" ]; then
    arm DELETE "${ACCT_ID}/managementPolicies/default?api-version=${ST_API}" >/dev/null 2>&1 || true
  else
    arm PUT "${ACCT_ID}/managementPolicies/default?api-version=${ST_API}" "$3" >/dev/null || return 1
  fi
}

# blob_count prints how many blobs the container holds, through floci-az's
# path-style blob endpoint.
blob_count() {
  curl -sS --fail-with-body --max-time "$ARM_REQUEST_SECS" --cacert "$AZURE_CA_FILE" \
    -H "Authorization: Bearer floci-az" -H "x-ms-version: 2023-11-03" \
    "${AZURE_ENDPOINT}/${ACCOUNT}/${CONTAINER}?restype=container&comp=list&include=tags" \
    | python3 -c 'import sys,re; print(len(re.findall(r"<Blob>", sys.stdin.read())))'
}

step "0. the operator's storage account"
cmd "PUT resource group, storage account, container; versioning on, lifecycle rule, public access off"
arm PUT "${SUB}/resourcegroups/${RG}?api-version=2021-04-01" '{"location":"eastus"}' >/dev/null \
  || fail "az-records" "could not create the records resource group"
arm PUT "${ACCT_ID}?api-version=${ST_API}" '{"location":"eastus","kind":"StorageV2","sku":{"name":"Standard_LRS"},"properties":{"allowBlobPublicAccess":false}}' >/dev/null \
  || fail "az-records" "could not create the storage account"
arm PUT "${ACCT_ID}/blobServices/default/containers/${CONTAINER}?api-version=${ST_API}" '{"properties":{}}' >/dev/null \
  || fail "az-records" "could not create the container"
account_set true false "$LIFECYCLE_OK" || fail "az-records" "could not set the account's three settings"
proof "a correct account, stated as data: versioning on, a previous-versions lifecycle rule, public access off."

# write_estate <dir> <estate> [allow_insecure list] writes one working copy:
# the record store block and a resource group to apply. Each refusal case
# below is its own estate, so each run is that estate's first contact with
# the container, the run the checks guard.
write_estate() {
  local dir="$1" estate="$2" waive="${3:-}"
  mkdir -p "$dir"
  azure_terraform_block "$estate" "record_store \"azurerm\" {
      storage_account_name = \"${ACCOUNT}\"
      container_name       = \"${CONTAINER}\"
      resource_group_name  = \"${RG}\"${waive:+
      allow_insecure       = ${waive}}
    }" > "$dir/versions.tf" || return 1
  cat > "$dir/main.tf" <<TF
resource "azurerm_resource_group" "app" {
  name     = "${estate}-rg"
  location = "eastus"
}
TF
}
write_estate "$SMOKE_WORK" "$ESTATE" || fail "az-records" "could not read the azurerm and azapi pins from live/oracle-versions.json"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - remove the fences and waive them; the apply must go through"
  explain \
    "Versioning off, the lifecycle rule deleted, public access allowed, and" \
    "all three waived by name. The apply must now write its record, and the" \
    "run must name each waiver. If it still refuses, the main arm's" \
    "refusals were not the checks."
  account_set false true none || fail "az-records" "BREAK: could not take the fences away"
  write_estate "$SMOKE_WORK" "$ESTATE" '["blob_versioning", "lifecycle_management", "public_access"]'
  logged az-records-break-init "az-records" "BREAK: init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
  BOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "az-records" "BREAK: the waived apply still failed: $BOUT"
  for s in blob_versioning lifecycle_management public_access; do
    grep -q "The record store storage account's $s assertion is waived" <<< "$BOUT" \
      || fail "az-records" "BREAK: the run does not name the $s waiver: $BOUT"
  done
  [ "$(blob_count)" -gt 0 ] || fail "az-records" "BREAK: the waived apply wrote no record"
  proof "caught. With the fences gone and waived, the same apply went through and named every waiver, so the refusals the main arm reads are the checks'."
  exit 0
fi

step "1. the correct account: records are written, each tagged with the estate"
cmd "choudoufu init && choudoufu apply -auto-approve"
logged az-records-init "az-records" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-records" "the apply against a correct account failed: $AOUT"
grep -E 'Apply complete!' <<< "$AOUT" | evidence
BLOBS="$(curl -sS --max-time "$ARM_REQUEST_SECS" --cacert "$AZURE_CA_FILE" -H "Authorization: Bearer floci-az" -H "x-ms-version: 2023-11-03" \
  "${AZURE_ENDPOINT}/${ACCOUNT}/${CONTAINER}?restype=container&comp=list&include=tags" 2>&1)" \
  || fail "az-records" "could not list the container: $BLOBS"
grep -q "<Key>tofu-estate</Key><Value>${ESTATE}</Value>" <<< "$BLOBS" \
  || fail "az-records" "no record blob carries the blob index tag tofu-estate=${ESTATE}: $BLOBS"
proof "the record is a blob, and it carries the estate as a blob index tag written in the same request."

# refused <case> <summary> <versioning> <public> <lifecycle>: sets the
# account, applies a new estate against it, and requires the named refusal
# and no new blob.
refused() {
  local what="$1" summary="$2" before rc=0 out dir="$SMOKE_WORK/$1"
  account_set "$3" "$4" "$5" || fail "az-records" "$what: could not set the account"
  before="$(blob_count)" || fail "az-records" "$what: could not count blobs"
  write_estate "$dir" "smoke-az-$what" || fail "az-records" "$what: could not write the estate"
  logged "az-records-$what-init" "az-records" "$what: init failed" -- in_dir "$dir" chdf init -input=false -no-color
  out="$(cd "$dir" && chdf apply -auto-approve -input=false -no-color 2>&1)" || rc=$?
  grep -E '^Error' <<< "$out" | head -1 | evidence
  [ "$rc" != "0" ] || fail "az-records" "$what: the apply went through against an account it cannot trust"
  grep -qF "$summary" <<< "$out" || fail "az-records" "$what: the refusal is not '$summary': $out"
  [ "$(blob_count)" = "$before" ] || fail "az-records" "$what: a record was written before the refusal"
}

step "2. each broken setting is refused by name, with nothing written"
explain \
  "The account is broken one setting at a time, and each time a new estate" \
  "makes its first contact with the container."
refused versioning-off "The record store storage account fails its blob_versioning assertion" false false "$LIFECYCLE_OK"
refused no-lifecycle "The record store storage account fails its lifecycle_management assertion" true false none
refused public-access "The record store storage account fails its public_access assertion" true true "$LIFECYCLE_OK"
refused eats-records "The record store storage account's lifecycle policy removes records" true false "$LIFECYCLE_EATS"
proof "four accounts it cannot trust, four refusals by name, and no record written by any of them."

step "3. teardown"
account_set true false "$LIFECYCLE_OK" || fail "az-records" "could not restore the account"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-records" "apply -destroy failed: $DOUT"
proof "the estate is gone; the operator's account stays, as it would."
