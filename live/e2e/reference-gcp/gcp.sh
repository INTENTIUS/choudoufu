# shellcheck shell=bash
# live/e2e/reference-gcp/gcp.sh: reference-gcp's own floci-gcp helpers
# (#1119), sourced by reference-gcp/run.sh after live/e2e/lib/gcp.sh. The
# substrate's shared helpers (the emulator, the provider block, the REST
# calls, the address label) live in that library; these two depend on this
# estate's shape.
#
# The caller sets ROOT and sources live/e2e/lib/gauntlet.sh and
# live/e2e/lib/gcp.sh first.

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
