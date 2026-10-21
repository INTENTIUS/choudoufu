# shellcheck shell=bash
# live/e2e/lib/gcp.sh: the floci-gcp substrate's shared helpers (#1119),
# sourced by every gcp-lane crossing script after live/e2e/lib/gauntlet.sh.
# They moved here from live/e2e/reference-gcp/gcp.sh when the lane's second
# estate, corpus-k8s-infra-gcp-gcve, needed them. What stays beside each
# estate is what depends on its own shape: which labelled types to count
# and which objects make up its inventory.
#
# The caller sets ROOT and sources live/e2e/lib/gauntlet.sh first.

# gcp_floci_image prints live/floci-gcp-image's pinned digest, or nothing.
gcp_floci_image() {
  : "${ROOT:?gcp_floci_image needs \$ROOT set}"
  cat "$ROOT/live/floci-gcp-image" 2>/dev/null
}

# gcp_floci_up <container name> <host port> <default project>: one floci-gcp
# container, started through gauntlet_floci_start so it carries the
# ownership labels the leak sweeper reads (#1312), and waited on until
# GET /health answers. 4588 is the one port every emulated API shares. No
# Docker socket is mounted: the gcp lane's estates use Compute, Cloud
# Storage, IAM, Pub/Sub, Service Usage and Resource Manager, none of which
# runs a sidecar.
gcp_floci_up() {
  local name="$1" port="$2" project="$3" image
  image="$(gcp_floci_image)"
  [ -n "$image" ] || { printf 'gcp_floci_up: could not read the pinned image from %s/live/floci-gcp-image\n' "$ROOT" >&2; return 1; }
  gauntlet_floci_start "$name" -p "127.0.0.1:${port}:4588" \
    -e "FLOCI_GCP_DEFAULT_PROJECT_ID=$project" \
    "$image" || return 1
  for _ in $(seq 1 45); do
    curl -fsS --max-time 5 "http://127.0.0.1:${port}/health" >/dev/null 2>&1 && return 0
    sleep 2
  done
  printf 'gcp_floci_up: %s never answered /health on :%s\n' "$name" "$port" >&2
  return 1
}

# gcp_endpoint_paths: "<provider argument> <path>" per service a gcp-lane
# estate reaches. The same list as live/smoke/lib.sh's, whose comment on gcp_up
# says which paths the fork's own documentation confirms and which are
# assumed (compute and iam_credentials are assumed), with one difference:
# Cloud Asset is under /cloudasset. floci-gcp mounts it there because
# /v1/projects belongs to IAM (docs/services/cloud-asset.md and
# CloudAssetController at the pinned 4bb6a04), and only rewrites a bare
# /v1/ path for a cloudasset.* host name, which 127.0.0.1 is not. The
# sweep (internal/live/gcpsweep's serviceRoot) keeps the prefix and drops
# the trailing v1, so it calls /cloudasset/v1/projects/<p>:searchAllResources.
#
# Two arguments are not the service's own name. google_service_account's
# client is the iambeta product (services/iambeta/client.go at v8.6.0), read
# from iam_beta_custom_endpoint, not iam_custom_endpoint. And a google_project
# read, the data source included, always calls Cloud Billing's
# GetBillingInfo (resource_google_project.go's resourceGoogleProjectRead),
# from cloud_billing_custom_endpoint. Unset, either goes to the real
# googleapis.com with the fake token and fails "authError, Invalid
# Credentials", which is how reference-gcp's first run stopped at cold_deploy
# on 2026-10-20. Both clients strip the trailing version
# (transport_tpg.RemoveBasePathVersion), so v1/ lands on the emulator's
# /v1/projects/... routes.
gcp_endpoint_paths() {
  cat <<'PATHS'
compute_custom_endpoint compute/v1/
storage_custom_endpoint storage/v1/
iam_custom_endpoint -
iam_beta_custom_endpoint v1/
pubsub_custom_endpoint v1/
resource_manager_custom_endpoint v1/
service_usage_custom_endpoint v1/
cloud_asset_custom_endpoint cloudasset/v1/
iam_credentials_custom_endpoint v1/
cloud_billing_custom_endpoint v1/
PATHS
}

# GCP_PROVIDER_ENDPOINTS: every *_custom_endpoint argument hashicorp/google
# accepts at the pinned version. See the file's own header.
GCP_PROVIDER_ENDPOINTS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/gcp-provider-endpoints.txt"

# gcp_all_endpoint_paths: gcp_endpoint_paths, then every other argument in
# GCP_PROVIDER_ENDPOINTS at v1/. The run fails closed: a product the list
# above never named still points at the emulator, where it fails with a 404,
# instead of at googleapis.com. Two missed arguments sent reference-gcp's and
# gcve's first runs to the real IAM and Cloud Billing APIs (2026-10-20).
gcp_all_endpoint_paths() {
  [ -r "$GCP_PROVIDER_ENDPOINTS" ] || { printf 'gcp_all_endpoint_paths: %s is unreadable\n' "$GCP_PROVIDER_ENDPOINTS" >&2; return 1; }
  local named
  named="$(gcp_endpoint_paths)"
  printf '%s\n' "$named"
  grep -v '^#' "$GCP_PROVIDER_ENDPOINTS" | while read -r arg; do
    [ -n "$arg" ] || continue
    printf '%s\n' "$named" | grep -q "^$arg " || printf '%s v1/\n' "$arg"
  done
}

# gcp_harness_token: the fake bearer token every request carries. The block
# writes it as access_token, which the provider prefers over Application
# Default Credentials (transport/config.go's GetCredentials at v8.6.0), so no
# real credential on the machine is ever picked up; floci-gcp ignores it with
# IAM enforcement off. Refused unless it is the harness's own fake.
gcp_harness_token() {
  case "${GOOGLE_OAUTH_ACCESS_TOKEN:-}" in
    chdf-*) printf '%s' "$GOOGLE_OAUTH_ACCESS_TOKEN" ;;
    *) printf 'gcp_harness_token: GOOGLE_OAUTH_ACCESS_TOKEN is not a chdf- fake token; refusing to write a real credential into an emulator run\n' >&2; return 1 ;;
  esac
}

# gcp_provider_block <endpoint> <project> <region> <zone>: a provider
# "google" block pointed at one emulator. Stock and choudoufu get the same
# block, so the only difference between their roots is the live block.
gcp_provider_block() {
  local ep="$1" project="$2" region="$3" zone="$4" arg path paths token
  paths="$(gcp_all_endpoint_paths)" || return 1
  token="$(gcp_harness_token)" || return 1
  printf 'provider "google" {\n  project = "%s"\n  region  = "%s"\n  zone    = "%s"\n  access_token = "%s"\n\n' "$project" "$region" "$zone" "$token"
  while read -r arg path; do
    [ "$path" = "-" ] && path=""
    printf '  %s = "%s/%s"\n' "$arg" "$ep" "$path"
  done <<< "$paths"
  printf '}\n'
}

# gcp_rest <endpoint> <METHOD> <path> [json body]: the emulator's REST API
# with the run's fake token. Every out-of-band read and write in the script
# goes through this, never through choudoufu or stock.
gcp_rest() {
  local ep="$1" method="$2" path="$3" body="${4:-}"
  if [ -n "$body" ]; then
    curl -fsS --max-time 30 -X "$method" -H "Authorization: Bearer $GOOGLE_OAUTH_ACCESS_TOKEN" \
      -H 'Content-Type: application/json' --data "$body" "$ep/$path"
  else
    curl -fsS --max-time 30 -X "$method" -H "Authorization: Bearer $GOOGLE_OAUTH_ACCESS_TOKEN" "$ep/$path"
  fi
}

# gcp_labels <endpoint> <path>: the labels map of the object at <path>, as
# sorted JSON, or {} when it has none.
gcp_labels() {
  gcp_rest "$1" GET "$2" | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin).get("labels", {}), sort_keys=True))'
}

# gcp_address_label <block> <instance>: the tofu-address label choudoufu
# writes for an instance (#1121, markers.GoogleAddressValue): two 26-char
# lowercase base32 prefixes of SHA-256, block then instance, joined by "-".
gcp_address_label() {
  python3 - "$1" "$2" <<'PY'
import base64, hashlib, sys
enc = lambda s: base64.b32encode(hashlib.sha256(s.encode()).digest()).decode().lower().rstrip("=")[:26]
print(enc(sys.argv[1]) + "-" + enc(sys.argv[2]))
PY
}

# gcp_request_count <TF_LOG=debug file>: the provider requests one run
# logged. hashicorp/google writes one "---[ REQUEST ]---" block per HTTP
# request at debug level (its transport's logging round tripper); that
# delimiter is an assumption from the provider's source, not measured here.
gcp_request_count() {
  grep -c -- '---\[ REQUEST \]---' "$1" 2>/dev/null || true
}

# gcp_migrate_counts: reads live-import -approve's output on stdin and
# prints five fields: newly stamped, skipped, skipped for a reason other
# than the missing marker surface, failed, and identities recorded ("?"
# where the line is absent). This is the count AWS's estates assert
# (reference-eks, corpus-eks-basic): a resource with no labels argument is
# SKIPPED, because the marker write is what was skipped, and its identity
# goes into the record store, which the "N identities recorded." line
# counts. "newly recorded" is a different thing, the record-backed value of
# a random_pet or a null_resource, and a google object never reaches it.
gcp_migrate_counts() {
  python3 -c '
import re, sys
s = sys.stdin.read()
def n(pat):
    m = re.search(pat, s)
    return m.group(1) if m else "?"
skipped_rows = []
block = re.search(r"^SKIPPED \(\d+\)[^\n]*\n((?:  .*\n)*)", s, re.M)
if block:
    skipped_rows = [l for l in block.group(1).splitlines() if l.strip()]
other = [l for l in skipped_rows if "has no tags argument in the provider" not in l]
print(n(r"(\d+) resource\(s\) newly stamped"), n(r"(\d+) skipped\."), len(other),
      n(r"(\d+) failed,"), n(r"(\d+) identit(?:y|ies) recorded\."))
'
}

# gcp_failed_rows: live-import -approve's FAILED rows on stdin, one line
# each with its spaces squeezed, at most three. A failed stamp is reported
# in that group, never on an "Error:" line, so a gate that read only the
# first Error: line printed "" for it (the first GCP run's reference-gcp
# migrate).
gcp_failed_rows() {
  sed -n '/^FAILED (/,/^$/p' | grep -E '^  [a-z]' | head -3 | tr -s ' ' | sed 's/^ //' | paste -sd ';' -
}
