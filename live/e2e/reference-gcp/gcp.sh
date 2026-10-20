# shellcheck shell=bash
# live/e2e/reference-gcp/gcp.sh: the floci-gcp substrate (#1119), sourced by
# reference-gcp/run.sh. The kind substrate's helpers live in
# live/e2e/lib/gauntlet.sh because fifteen estates share them; these live
# beside the one estate that uses them, so a change here badges this row and
# no other (tools/gauntlet/scriptstale.go's SharedLibDir). They move to the
# library when a second gcp-lane estate needs them.
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
# Docker socket is mounted: the shape uses Compute, Cloud Storage, IAM and
# Pub/Sub, none of which runs a sidecar.
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

# gcp_endpoint_paths: "<provider argument> <path>" per service the shape
# reaches. The same list as live/smoke/lib.sh's, whose comment on gcp_up
# says which paths the fork's own documentation confirms and which are
# assumed (compute, cloud_asset and iam_credentials are assumed).
gcp_endpoint_paths() {
  cat <<'PATHS'
compute_custom_endpoint compute/v1/
storage_custom_endpoint storage/v1/
iam_custom_endpoint -
pubsub_custom_endpoint v1/
resource_manager_custom_endpoint v1/
service_usage_custom_endpoint v1/
cloud_asset_custom_endpoint v1/
iam_credentials_custom_endpoint v1/
PATHS
}

# gcp_provider_block <endpoint> <project> <region> <zone>: a provider
# "google" block pointed at one emulator. Stock and choudoufu get the same
# block, so the only difference between their roots is the live block.
gcp_provider_block() {
  local ep="$1" project="$2" region="$3" zone="$4" arg path
  printf 'provider "google" {\n  project = "%s"\n  region  = "%s"\n  zone    = "%s"\n\n' "$project" "$region" "$zone"
  while read -r arg path; do
    [ "$path" = "-" ] && path=""
    printf '  %s = "%s/%s"\n' "$arg" "$ep" "$path"
  done < <(gcp_endpoint_paths)
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

# gcp_labelled_count <endpoint> <project> <zone> <estate>: how many of the
# labelled types the shape declares carry tofu-estate=<estate>: buckets,
# Pub/Sub topics, and the zone's disks and instances. The floci-gcp
# substrate's answer to the tagging index's marker count. The untaggable
# skeleton (network, subnetwork, firewall, service account) carries no
# label and is never in it.
gcp_labelled_count() {
  EP="$1" PROJECT="$2" ZONE="$3" ESTATE_LABEL="$4" TOKEN="$GOOGLE_OAUTH_ACCESS_TOKEN" python3 - <<'PY'
import json, os, sys, urllib.request
ep, p, z, est, tok = (os.environ[k] for k in ("EP", "PROJECT", "ZONE", "ESTATE_LABEL", "TOKEN"))
def get(path):
    req = urllib.request.Request(f"{ep}/{path}", headers={"Authorization": f"Bearer {tok}"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)
n = 0
for path, key in [(f"storage/v1/b?project={p}", "items"),
                  (f"v1/projects/{p}/topics", "topics"),
                  (f"compute/v1/projects/{p}/zones/{z}/disks", "items"),
                  (f"compute/v1/projects/{p}/zones/{z}/instances", "items")]:
    for o in get(path).get(key, []):
        if o.get("labels", {}).get("tofu-estate") == est:
            n += 1
print(n)
PY
}

# gcp_inventory <endpoint> <project> <region> <zone> [drop]: the shape's
# objects on one emulator, normalised to what the configuration declares and
# nothing the server set, labels never compared, so stock's cold deploy and
# choudoufu's greenfield apply compare object by object. <drop> removes one
# key (BREAK's control).
gcp_inventory() {
  EP="$1" PROJECT="$2" REGION="$3" ZONE="$4" DROP="${5:-}" TOKEN="$GOOGLE_OAUTH_ACCESS_TOKEN" python3 - <<'PY'
import json, os, urllib.error, urllib.request
ep, p, r, z, drop, tok = (os.environ[k] for k in ("EP", "PROJECT", "REGION", "ZONE", "DROP", "TOKEN"))
def get(path):
    req = urllib.request.Request(f"{ep}/{path}", headers={"Authorization": f"Bearer {tok}"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise
def leaf(link):
    return (link or "").rsplit("/", 1)[-1]
inv = {}
o = get(f"compute/v1/projects/{p}/global/networks/refgcp-net")
inv["network/refgcp-net"] = None if o is None else {"autoCreateSubnetworks": o.get("autoCreateSubnetworks")}
o = get(f"compute/v1/projects/{p}/regions/{r}/subnetworks/refgcp-subnet")
inv["subnetwork/refgcp-subnet"] = None if o is None else {"ipCidrRange": o.get("ipCidrRange"), "network": leaf(o.get("network"))}
o = get(f"compute/v1/projects/{p}/global/firewalls/refgcp-ssh")
inv["firewall/refgcp-ssh"] = None if o is None else {"network": leaf(o.get("network")), "allowed": o.get("allowed"), "sourceRanges": o.get("sourceRanges")}
o = get(f"v1/projects/{p}/serviceAccounts/refgcp-app@{p}.iam.gserviceaccount.com")
inv["serviceaccount/refgcp-app"] = None if o is None else {"exists": True}
o = get(f"storage/v1/b/{p}-refgcp-logs")
inv["bucket/refgcp-logs"] = None if o is None else {"location": o.get("location"), "ubla": o.get("iamConfiguration", {}).get("uniformBucketLevelAccess", {}).get("enabled")}
for t in ["refgcp-events", "refgcp-shard-0", "refgcp-shard-1"]:
    o = get(f"v1/projects/{p}/topics/{t}")
    inv["topic/" + t] = None if o is None else {"exists": True}
o = get(f"compute/v1/projects/{p}/zones/{z}/disks/refgcp-boot")
inv["disk/refgcp-boot"] = None if o is None else {"sizeGb": str(o.get("sizeGb")), "type": leaf(o.get("type"))}
o = get(f"compute/v1/projects/{p}/zones/{z}/instances/refgcp-vm")
inv["instance/refgcp-vm"] = None if o is None else {
    "machineType": leaf(o.get("machineType")),
    "subnetworks": [leaf(n.get("subnetwork")) for n in o.get("networkInterfaces", [])],
    "disks": [leaf(d.get("source")) for d in o.get("disks", [])]}
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}

# gcp_request_count <TF_LOG=debug file>: the provider requests one run
# logged. hashicorp/google writes one "---[ REQUEST ]---" block per HTTP
# request at debug level (its transport's logging round tripper); that
# delimiter is an assumption from the provider's source, not measured here.
gcp_request_count() {
  grep -c -- '---\[ REQUEST \]---' "$1" 2>/dev/null || true
}
