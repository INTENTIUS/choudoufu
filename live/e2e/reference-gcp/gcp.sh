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

# refgcp_display_name_only_diff: reads a plan on stdin and succeeds only
# when its whole diff is lex00/floci-gcp#24 on the service account: exactly
# one in-place update, of google_service_account.app, whose one changed
# attribute is display_name going to null. The emulator defaults
# displayName to the account id where Google leaves it empty, and its PATCH
# ignores updateMask, so the diff never converges. Anything more in the
# plan, or anything else in its place, is not #24 and fails this check.
refgcp_display_name_only_diff() {
  python3 -c '
import re, sys
s = sys.stdin.read()
if "Plan: 0 to add, 1 to change, 0 to destroy." not in s:
    sys.exit(1)
heads = re.findall(r"^\s*# (\S+) will be (?:updated in-place|created|destroyed|replaced)", s, re.M)
heads += re.findall(r"^\s*# (\S+) must be replaced", s, re.M)
if heads != ["google_service_account.app"]:
    sys.exit(1)
attrs = [l for l in s.splitlines() if re.match(r"^\s+[~+-] (?!resource )[A-Za-z_\"][^=]*= ", l)]
if len(attrs) != 1 or not re.match(r"^\s+- display_name\s+= \".*\" -> null\s*$", attrs[0]):
    sys.exit(1)
'
}

# refgcp_neg_endpoint_lines: reads a networkEndpointGroups
# listNetworkEndpoints response on stdin and prints one "ip|fqdn|port" line
# per endpoint, sorted, an unset field empty. day2_endpoints reads each
# scratch NEG with it, so the endpoint the emulator holds is compared field
# by field with the one the block declared.
refgcp_neg_endpoint_lines() {
  python3 -c '
import json, sys
d = json.load(sys.stdin)
out = []
for it in d.get("items", []):
    e = it.get("networkEndpoint", it)
    out.append("%s|%s|%s" % (e.get("ipAddress", ""), e.get("fqdn", ""), e.get("port", "")))
print("\n".join(sorted(out)))
'
}

# refgcp_endpoint_record <records dir> <address> <expected import id> <ip>
# <fqdn> <port>: day2_endpoints' check that the estate's record for a
# network endpoint names the object stock names. Exits 1, printing why,
# when no record carries <address> or the record disagrees with the
# expected import id (the empty segment where ip_address or fqdn is unset,
# #2145 item 6). A one-string identity (import_id or secondary_id) must
# equal <expected import id> exactly; identity attrs must carry <ip>,
# <fqdn> and <port>, an unset one absent or empty. Prints what it compared,
# for the verdict line.
refgcp_endpoint_record() {
  python3 - "$@" <<'PY'
import json, os, sys
root, addr, want, ip, fqdn, port = sys.argv[1:7]
found = None
for d, _, files in os.walk(root):
    for f in files:
        try:
            env = json.load(open(os.path.join(d, f)))
        except Exception:
            continue
        if isinstance(env, dict) and env.get("address") == addr:
            found = env
if found is None:
    print(f"no record under {root} carries {addr}")
    sys.exit(1)
ident = found.get("identity") or {}
strings = {k: ident[k] for k in ("import_id", "secondary_id") if ident.get(k)}
attrs = ident.get("attrs") or {}
if not strings and not attrs:
    print(f"the record for {addr} carries no identity: {json.dumps(found.get('identity'))}")
    sys.exit(1)
bad = [f"{k}={v!r}, want {want!r}" for k, v in sorted(strings.items()) if v != want]
for k, v in (("ip_address", ip), ("fqdn", fqdn), ("port", port)):
    if attrs and str(attrs.get(k, "") or "") != v:
        bad.append(f"attrs.{k}={attrs.get(k)!r}, want {v!r}")
if bad:
    print(f"the record for {addr} disagrees: " + "; ".join(bad))
    sys.exit(1)
parts = [f"{k} {v}" for k, v in sorted(strings.items())]
if attrs:
    parts.append("attrs " + json.dumps(attrs, sort_keys=True))
print(", ".join(parts))
PY
}

# ── record_store_gcs's helpers ─────────────────────────────────────────────
# The stage runs record_store "gcs" against emulator A's Cloud Storage (the
# store's own calls, pkg/staterecord/gcs.go), and reads the bucket
# behind its back with these: the JSON API through the run's fake token,
# never through choudoufu.

# refgcp_record_key <estate> <address>: the object name choudoufu's record
# for <address> is written at, with no key_prefix: tofu-records/<estate>/
# <type>/<the address in unpadded URL-safe base64>, chunked into 230-byte
# segments only past 255 bytes (internal/live/projection/record.go's
# RecordKey). live/gcp_later_stages_test.go holds it to RecordKey itself.
refgcp_record_key() {
  python3 - "$1" "$2" <<'PY'
import base64, re, sys
estate, addr = sys.argv[1], sys.argv[2]
typ = re.sub(r'^(module\.[^.\[]+(\[[^\]]*\])?\.)*', "", addr).split(".")[0]
enc = base64.urlsafe_b64encode(addr.encode()).decode().rstrip("=")
if len(enc) > 255:
    enc = "/".join(enc[i:i + 230] for i in range(0, len(enc), 230))
print(f"tofu-records/{estate}/{typ}/{enc}")
PY
}

# refgcp_gcs_make_bucket <endpoint> <project> <bucket>: the record bucket,
# created with the four settings examples/record-store-gcs/CONTRACT.md
# asserts: Object Versioning, a Delete rule for noncurrent versions with a
# window, public access prevention enforced, uniform bucket-level access.
refgcp_gcs_make_bucket() {
  gcp_rest "$1" POST "storage/v1/b?project=$2" "$(printf '{"name":"%s","location":"US","versioning":{"enabled":true},"lifecycle":{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]},"iamConfiguration":{"uniformBucketLevelAccess":{"enabled":true},"publicAccessPrevention":"enforced"}}' "$3")" >/dev/null
}

# refgcp_gcs_bucket_problems: reads a storage.buckets.get response on stdin
# and prints, joined by "; ", each contract setting it does not carry, or
# nothing when it carries all four. Unreadable input is a problem too.
refgcp_gcs_bucket_problems() {
  python3 -c '
import json, sys
try:
    b = json.load(sys.stdin)
except Exception:
    print("the bucket'"'"'s settings are unreadable"); sys.exit(0)
p = []
if not (b.get("versioning") or {}).get("enabled"):
    p.append("versioning is not enabled")
rules = (b.get("lifecycle") or {}).get("rule") or []
if not any((r.get("action") or {}).get("type") == "Delete" and "daysSinceNoncurrentTime" in (r.get("condition") or {}) for r in rules):
    p.append("no Delete rule with daysSinceNoncurrentTime")
iam = b.get("iamConfiguration") or {}
if iam.get("publicAccessPrevention") != "enforced":
    p.append("publicAccessPrevention reads %r" % iam.get("publicAccessPrevention"))
if not (iam.get("uniformBucketLevelAccess") or {}).get("enabled"):
    p.append("uniform bucket-level access is not enabled")
print("; ".join(p))
'
}

# refgcp_gcs_records <endpoint> <bucket> <prefix>: every live object under
# <prefix>, one "name tofu-estate" line each, sorted, "-" where the object
# carries no tofu-estate metadata. Exits non-zero when the listing fails.
refgcp_gcs_records() {
  EP="$1" BUCKET="$2" PREFIX="$3" TOKEN="$GOOGLE_OAUTH_ACCESS_TOKEN" python3 - <<'PY'
import json, os, urllib.parse, urllib.request
ep, b, pre, tok = (os.environ[k] for k in ("EP", "BUCKET", "PREFIX", "TOKEN"))
out, token = [], None
while True:
    q = {"prefix": pre}
    if token:
        q["pageToken"] = token
    req = urllib.request.Request(f"{ep}/storage/v1/b/{b}/o?" + urllib.parse.urlencode(q), headers={"Authorization": f"Bearer {tok}"})
    with urllib.request.urlopen(req, timeout=30) as r:
        d = json.load(r)
    for o in d.get("items", []):
        out.append("%s %s" % (o["name"], (o.get("metadata") or {}).get("tofu-estate") or "-"))
    token = d.get("nextPageToken")
    if not token:
        break
print("\n".join(sorted(out)))
PY
}

# refgcp_gcs_delete <endpoint> <bucket> <object name>: deletes the live
# object, as another writer would; the stage's Break control uses it.
refgcp_gcs_delete() {
  gcp_rest "$1" DELETE "storage/v1/b/$2/o/$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$3")" >/dev/null
}

# refgcp_gcs_scrub <endpoint> <bucket>: every version of every object in the
# bucket deleted, then the bucket. Best effort: the emulator goes with the
# run, and this only keeps a failed stage from leaving the bucket to the
# next one.
refgcp_gcs_scrub() {
  EP="$1" BUCKET="$2" TOKEN="$GOOGLE_OAUTH_ACCESS_TOKEN" python3 - <<'PY'
import json, os, sys, urllib.error, urllib.parse, urllib.request
ep, b, tok = (os.environ[k] for k in ("EP", "BUCKET", "TOKEN"))
def call(method, path):
    req = urllib.request.Request(f"{ep}/{path}", method=method, headers={"Authorization": f"Bearer {tok}"})
    with urllib.request.urlopen(req, timeout=30) as r:
        body = r.read()
        return json.loads(body) if body else {}
try:
    items = call("GET", f"storage/v1/b/{b}/o?versions=true").get("items", [])
except urllib.error.HTTPError as e:
    sys.exit(0 if e.code == 404 else 1)
for o in items:
    try:
        call("DELETE", f"storage/v1/b/{b}/o/{urllib.parse.quote(o['name'], safe='')}?generation={o['generation']}")
    except urllib.error.HTTPError:
        pass
try:
    call("DELETE", f"storage/v1/b/{b}")
except urllib.error.HTTPError:
    sys.exit(1)
PY
}

# refgcp_gcs_race_verdict <first> <second>: record_store_gcs's race. Each
# argument is a path prefix with <p>.out (an apply's output) and <p>.rc
# (its exit status). Two applies of one estate each created time_sleep.race
# and wrote its record, which did not exist when either planned, so each
# write went out under ifGenerationMatch=0: exactly one may land. Prints
# "<winner> <loser>" (1 or 2) and exits 0 when exactly one apply succeeded
# and the other was refused with the store's write conflict naming
# time_sleep.race; otherwise prints what happened and exits 1.
refgcp_gcs_race_verdict() {
  python3 - "$1" "$2" <<'PY'
import re, sys
runs = []
for p in sys.argv[1:3]:
    try:
        out = open(p + ".out").read()
        rc = int(open(p + ".rc").read().strip())
    except Exception as e:
        print(f"apply {len(runs) + 1} left no output or exit status ({e})"); sys.exit(1)
    runs.append((out, rc))
created = [bool(re.search(r"^time_sleep\.race: Creation complete", o, re.M)) for o, _ in runs]
if not all(created):
    print("the two applies did not overlap: %d of 2 created time_sleep.race, so one planned after the other had written its record; nothing raced" % sum(created)); sys.exit(1)
ok = [rc == 0 for _, rc in runs]
def first_error(o):
    m = re.search(r"^Error: .*$", o, re.M)
    return m.group(0) if m else "no Error: line"
if all(ok):
    print("both applies created time_sleep.race and both succeeded: the second record write under ifGenerationMatch=0 was not refused, so the store's create-only precondition did not hold on this emulator"); sys.exit(1)
if not any(ok):
    print("both applies failed (exit %d: %s; exit %d: %s)" % (runs[0][1], first_error(runs[0][0]), runs[1][1], first_error(runs[1][0]))); sys.exit(1)
w = ok.index(True); l = 1 - w
lo = runs[l][0]
# tofu wraps a diagnostic's body, so the address and "Nothing was
# overwritten" can each straddle a line break (the first gcs run); match
# them on the body with its whitespace collapsed.
flat = " ".join(lo.split())
if not (re.search(r"^Error: Record store write conflict", lo, re.M) and "time_sleep.race" in flat and "Nothing was overwritten" in flat):
    print("apply %d failed (exit %d) but not with the store's write conflict on time_sleep.race: %s" % (l + 1, runs[l][1], first_error(lo))); sys.exit(1)
print(w + 1, l + 1)
PY
}
