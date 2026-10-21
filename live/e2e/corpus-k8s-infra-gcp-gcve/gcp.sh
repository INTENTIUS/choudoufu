# shellcheck shell=bash
# live/e2e/corpus-k8s-infra-gcp-gcve/gcp.sh: this estate's own floci-gcp
# helpers (#1119), sourced by its run.sh after live/e2e/lib/gauntlet.sh and
# live/e2e/lib/gcp.sh. The substrate's shared helpers live in that library;
# these depend on this estate's shape: one labelled type (the bucket the
# simple_bucket module makes, plus the day-2 stages' scratch buckets) and an
# inventory of the network skeleton, the bucket, the five services and the
# five project roles.
#
# The caller sets ROOT and sources both libraries first.

# gcve_labelled_count <endpoint> <project> <estate>: how many buckets in
# <project> carry tofu-estate=<estate>. Buckets are the only labelled type
# this estate declares; the network, subnetwork, firewalls, services and
# IAM bindings carry no label and are never in it.
gcve_labelled_count() {
  EP="$1" PROJECT="$2" ESTATE_LABEL="$3" TOKEN="$GOOGLE_OAUTH_ACCESS_TOKEN" python3 - <<'PY'
import json, os, urllib.request
ep, p, est, tok = (os.environ[k] for k in ("EP", "PROJECT", "ESTATE_LABEL", "TOKEN"))
req = urllib.request.Request(f"{ep}/storage/v1/b?project={p}", headers={"Authorization": f"Bearer {tok}"})
with urllib.request.urlopen(req, timeout=30) as r:
    items = json.load(r).get("items", [])
print(sum(1 for b in items if (b.get("labels") or {}).get("tofu-estate") == est))
PY
}

# gcve_service_state <endpoint> <project> <service>: the Service Usage state
# of one API (ENABLED, DISABLED), or MISSING when the emulator has no record
# of it.
gcve_service_state() {
  EP="$1" PROJECT="$2" SVC="$3" TOKEN="$GOOGLE_OAUTH_ACCESS_TOKEN" python3 - <<'PY'
import json, os, urllib.error, urllib.request
ep, p, svc, tok = (os.environ[k] for k in ("EP", "PROJECT", "SVC", "TOKEN"))
req = urllib.request.Request(f"{ep}/v1/projects/{p}/services/{svc}", headers={"Authorization": f"Bearer {tok}"})
try:
    with urllib.request.urlopen(req, timeout=30) as r:
        print(json.load(r).get("state", "MISSING"))
except urllib.error.HTTPError as e:
    if e.code == 404:
        print("MISSING")
    else:
        raise
PY
}

# gcve_inventory <endpoint> <project> <region> <bucket> [drop]: the shape's
# objects on one emulator, normalised to what the configuration declares and
# nothing the server set, labels never compared, so stock's cold deploy and
# choudoufu's greenfield apply compare object by object. The project policy
# is cut down to the five roles iam.tf binds, because the emulator may hold
# bindings nobody here wrote. <drop> removes one key (BREAK's control).
gcve_inventory() {
  EP="$1" PROJECT="$2" REGION="$3" BUCKET="$4" DROP="${5:-}" TOKEN="$GOOGLE_OAUTH_ACCESS_TOKEN" python3 - <<'PY'
import json, os, urllib.error, urllib.request
ep, p, r, bucket, drop, tok = (os.environ[k] for k in ("EP", "PROJECT", "REGION", "BUCKET", "DROP", "TOKEN"))
def call(path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(f"{ep}/{path}", data=data, method="GET" if body is None else "POST",
                                 headers={"Authorization": f"Bearer {tok}", "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise
def leaf(link):
    return (link or "").rsplit("/", 1)[-1]
def bindings(policy, roles=None):
    out = {}
    for b in (policy or {}).get("bindings", []):
        if roles is None or b.get("role") in roles:
            out[b["role"]] = sorted(b.get("members", []))
    return out
inv = {}
o = call(f"compute/v1/projects/{p}/global/networks/maintenance-vpc-network")
inv["network/maintenance-vpc-network"] = None if o is None else {"autoCreateSubnetworks": o.get("autoCreateSubnetworks")}
o = call(f"compute/v1/projects/{p}/regions/{r}/subnetworks/maintenance-subnet")
inv["subnetwork/maintenance-subnet"] = None if o is None else {"ipCidrRange": o.get("ipCidrRange"), "network": leaf(o.get("network"))}
for fw in ["maintenance-firewall-internet", "maintenance-firewall-internal"]:
    o = call(f"compute/v1/projects/{p}/global/firewalls/{fw}")
    inv["firewall/" + fw] = None if o is None else {"network": leaf(o.get("network")), "allowed": o.get("allowed"), "sourceRanges": o.get("sourceRanges")}
o = call(f"storage/v1/b/{bucket}")
inv["bucket/" + bucket] = None if o is None else {
    "location": (o.get("location") or "").upper(),
    "versioning": (o.get("versioning") or {}).get("enabled"),
    "iam": bindings(call(f"storage/v1/b/{bucket}/iam"), {"roles/storage.admin"})}
for svc in ["cloudasset.googleapis.com", "compute.googleapis.com", "essentialcontacts.googleapis.com",
            "secretmanager.googleapis.com", "vmwareengine.googleapis.com"]:
    o = call(f"v1/projects/{p}/services/{svc}")
    inv["service/" + svc] = None if o is None else o.get("state")
inv["project-iam"] = bindings(call(f"v1/projects/{p}:getIamPolicy", {}),
    {"roles/admin", "roles/secretmanager.secretAccessor", "roles/viewer",
     "roles/serviceusage.serviceUsageConsumer", "roles/bigquery.admin"})
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}

# GCVE_SERVICES: the five APIs main.tf's google_project_service enables.
GCVE_SERVICES="cloudasset.googleapis.com compute.googleapis.com essentialcontacts.googleapis.com secretmanager.googleapis.com vmwareengine.googleapis.com"

# gcve_service_mismatches <endpoint A> <endpoint B> <project>: each of the
# five services whose state on A differs from its state on B, as
# "<service> (A=<state>, B=<state>)" joined by "; ", or nothing when all
# five agree. day2_teardown's check: what a destroy does to a service is
# stock's call. At hashicorp/google 8.6.0 disable_on_destroy is Optional
# with no default and the k8s.io root leaves it unset, so Delete drops the
# resource and never calls the API
# (resource_google_project_service.go:334-338); stock's own destroy on the
# oracle is the expectation, never "not ENABLED".
gcve_service_mismatches() {
  local epa="$1" epb="$2" project="$3" svc a b out=""
  for svc in $GCVE_SERVICES; do
    a="$(gcve_service_state "$epa" "$project" "$svc" 2>/dev/null)" || a="unread"
    b="$(gcve_service_state "$epb" "$project" "$svc" 2>/dev/null)" || b="unread"
    [ -n "$a" ] || a="unread"
    [ -n "$b" ] || b="unread"
    if [ "$a" != "$b" ] || [ "$a" = "unread" ]; then
      out="${out:+$out; }$svc (A=$a, B=$b)"
    fi
  done
  printf '%s' "$out"
}
