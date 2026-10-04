#!/usr/bin/env bash
# corpus-govuk-cluster-services (#1878, epic #1885): the kubernetes lane's
# first PUBLISHED root to drive the field-granular types (#1191, #1869),
# crossed on the kind substrate. alphagov/govuk-infrastructure's own
# terraform/deployments/cluster-services at commit
# c02504fa4abb439234669e9ea9b662bdb35209e9 (live/corpus-manifest.json, MIT):
# GOV.UK's in-cluster platform services for its EKS clusters, as its
# operators run it.
#
# Most of that root is not an estate's to own. 14 helm_release and 3
# kubectl_manifest blocks are refused in a live root (unadmitted-type,
# #1105), and its AWS IAM roles, policies and S3 bucket have nothing to talk
# to on kind. The Kubernetes compatibility page's answer is a split: the
# Helm and kubectl blocks go to a stock root of their own beside the
# estate. split.py, beside this script, makes that split mechanically from
# the published text (see its docstring for the rule), and what is left is
# the live slice this estate crosses:
#
#   kubernetes_namespace_v1.monitoring
#   module.gatekeeper.kubernetes_namespace_v1.gatekeeper   (a local module)
#   kubernetes_secret_v1.dex_client            for_each, 15 Secrets over 3 namespaces
#   kubernetes_secret_v1.eph_account           count = 0 outside an ephemeral env
#   kubernetes_labels.argocd_secret            for_each, 3: a label on a Secret
#                                              the same estate creates whole
#   kubernetes_annotations.rm_default_storageclass
#                                              force = true, on a StorageClass
#                                              the platform made, not the estate
#   random_bytes / random_password x3 / random_uuid   17 instances feeding them
#
# 38 instances. The two field-granular types are the reason this estate
# exists: nothing else in the lane writes a field of an object it does not
# own, and none of #1869's removal, migration and force rows has a
# published root behind it.
#
# The stock half (every block the split takes out, verbatim, under
# cluster-services-platform/ beside the root) is RECORDED, not applied: its
# charts need IRSA roles, an AWS load balancer controller and public DNS,
# none of which kind has, and its terraform_data.dex_ready polls a public
# Dex URL forever. Its block counts are asserted in cold_deploy so a moved
# pin fails loudly.
#
# Deltas, each asserted so a moved pin fails loudly rather than silently:
#   1. the split itself (split.py): the stock half out; depends_on entries
#      naming a stock block dropped from live blocks (dex_client's and
#      module.gatekeeper's on the load balancer controller's chart, the
#      annotations block's on the EBS CSI driver's chart);
#   2. the root's terraform and provider blocks go with the stock half
#      (the cloud{} workspace, required_version ~> 1.15, the EKS token
#      provider wiring); gauntlet_versions.tf keeps the root's own
#      hashicorp/kubernetes "~> 3.0" constraint and an empty kubernetes
#      provider, so KUBE_CONFIG_PATH names the run's kind cluster;
#   3. data.tfe_outputs.cluster_infrastructure becomes
#      var.cluster_infrastructure, whose placeholder value is appended to
#      terraform.tfvars beside the published integration tfvars
#      (common.tfvars and cluster-services.tfvars, the env
#      live/corpus-manifest.json's var_file_layout picks);
#   4. the annotations block names kind's own default StorageClass,
#      "standard", where the root names EKS's "gp2";
#   5. the namespaces the stock half would have made (cluster-services,
#      which its charts create, and apps, which argo-bootstrap does) are
#      created with kubectl on both clusters before the first apply. They
#      carry no estate label and no stage counts them;
#   6. choudoufu's roots get a live block with a local record store;
#   7. dex_client's lifecycle { ignore_changes = [metadata[0].labels] }
#      is narrowed to the one key the root's own kubernetes_labels
#      .argocd_secret writes onto those Secrets,
#      metadata[0].labels["app.kubernetes.io/part-of"]. Ignoring the whole
#      labels map is refused in a live root (live/LIMITATIONS.md,
#      "ignore-changes"): it would throw away the tofu-estate stamp. The
#      single foreign key is the admitted form, and it keeps the reason the
#      upstream root ignores labels at all, so it applies to the stock
#      oracle too and both clusters plan the same shape.
# And three additions, each in a file of its own, because the published
# shape has none: day2_count's two-instance count ConfigMap, day2_crash's
# Secret/ConfigMap pair with an edge between them, and the shared
# day2_replace/day2_crash rename blocks live/e2e/lib/gauntlet.sh adds and
# removes. All in the monitoring namespace, which the estate owns.
#
# What it measures on top of the stage contract (#1878):
#   - field manager choudoufu:<estate> owns the annotation on the
#     StorageClass and the label on each of the three Secrets, read off
#     managedFields after every stage that leaves them in place;
#   - migrate hands those fields from stock's "Terraform" manager to the
#     estate's, with the record's handover_from as the evidence;
#   - day2_remove of the annotations block releases the field and leaves
#     the StorageClass, the same end state stock's destroy leaves on the
#     oracle cluster;
#   - greenfield's force = true over the platform's own manager (kind's,
#     restored first) is accepted, because that manager is not an estate's.
#
# .corpus is read, never written: the root is copied out per run. Two kind
# clusters, both created for the run: A holds the estate (stock
# cold-deploys it, choudoufu adopts it and runs every day-2 stage, tears it
# down, then applies the same shape fresh with a live block); B is the
# oracle, where stock applies the identical root and every day-2 change.
#
#   go run ./tools/gauntlet run corpus-govuk-cluster-services
#   bash live/e2e/corpus-govuk-cluster-services/run.sh
#
# Needs `just corpus-fetch` first, then kind, kubectl, terraform (the stock
# binary), python3 and Docker on PATH.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls: a second Secret tampered
#                  (the single-object assertion must fail); the monitoring
#                  namespace's own metadata.name changed (the zero-churn
#                  assertion must fail); the StorageClass's annotation
#                  dropped from greenfield's expected inventory (the match
#                  must fail).
#   BREAK_REMOVE   keep the annotations block; no destroy may be proposed.
#   BREAK_COUNT    assert the wrong instance was destroyed on the scale-down.
#   BREAK_APPROVAL apply the saved plan after the world moved and expect success.
#   BREAK_REPLACE  day2_replace's Break line (live/e2e/lib/gauntlet.sh).
#   BREAK_CRASH    after the same real interrupt, assert nothing is proposed
#                  (day2_crash's own Break line); must fail.
#   BREAK_CRASH_UNBOUND
#                  strip the tofu-estate label off the object the
#                  interrupted apply did create; the recovery check must fail.
#   BREAK_STRICT   turn secrets back to "store"; the refusal must vanish.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$ROOT/live/e2e/corpus-govuk-cluster-services"
source "$ROOT/live/e2e/lib/gauntlet.sh"

# The shared provider plugin cache, and the cross-process lock real terraform
# needs in order to use it safely (#1300). live/e2e/lib/gauntlet.sh carries the
# measured reasons for both; this is the only place a script chooses either.
gauntlet_plugin_cache
ESTATE="corpus-govuk-cluster-services"
MGR="choudoufu:$ESTATE"
NS="monitoring"
KINDS="namespaces secrets configmaps"
SRC="$ROOT/.corpus/govuk-infrastructure"
PIN_WANT="c02504fa4abb439234669e9ea9b662bdb35209e9"
RD="terraform/deployments/cluster-services"
PD="terraform/deployments/cluster-services-platform"
SC="standard"
DEFAULT_ANN="storageclass.kubernetes.io/is-default-class"
PART_OF="app.kubernetes.io/part-of"
ARGOCD_NAMESPACES="cluster-services apps monitoring"
STANDINS="cluster-services apps"
# The live slice's instances in the integration environment, by kind of
# thing, so every count below is derived from one place: 2 namespaces and
# 15 Secrets carry the estate label; 3 labels and 1 annotations block are
# field-granular and carry no label of their own; 17 random_* values live
# only in the record store.
LABELLED_N=17
FIELD_N=4
RANDOM_N=17
STATE_N=$((LABELLED_N + FIELD_N + RANDOM_N))
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="chdf-gcs-a-$$"; CLUSTER_B="chdf-gcs-b-$$"
export TF_IN_AUTOMATION=1
log() { printf '%s\n' "$*"; }

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
cleanup() {
  gauntlet_kind_down "$CLUSTER_A"
  gauntlet_kind_down "$CLUSTER_B"
  rm -rf "$WORK"
}
trap cleanup EXIT
gauntlet_begin

# ── 0. tools and the pinned source ───────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed as the stock oracle"
command -v kind >/dev/null 2>&1 || fail "kind is not on PATH (brew install kind)"
command -v kubectl >/dev/null 2>&1 || fail "kubectl is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"
[ -d "$SRC/$RD" ] || fail "$SRC/$RD is missing - run \`just corpus-fetch\` first"
PIN="$(git -C "$SRC" rev-parse HEAD 2>/dev/null)"
[ "$PIN" = "$PIN_WANT" ] || fail "the fetched govuk-infrastructure is at $PIN, not the pinned $PIN_WANT - run \`just corpus-fetch\`"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU"
fi

# day2_crash needs a build with e2eTestingFeatures set, the same ldflags
# gate TOFU_E2E_APPLY_RESOURCE_PANIC sits behind, so the engine's own
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook (internal/command/
# apply_e2etesting_crash.go) is reachable. Built from THIS tree's source
# whatever $TOFU came from; identical to $TOFU everywhere else, because the
# hook is a no-op unless the variable names an applied address.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the root, copied out of .corpus and split ────────────────────────────
# write_root <dst> <stock|live>: copies the root, the variables file its
# variables-common.tf links to and the shared module it calls into <dst>,
# keeping the repository's layout so every relative path still resolves;
# runs split.py (delta 1) and applies deltas 2-4 and 7, plus 6 for "live". The
# working root is <dst>/$RD and the recorded stock half <dst>/$PD.
write_root() {
  local dst="$1" mode="$2" r
  rm -rf "$dst"
  mkdir -p "$dst/terraform/deployments" "$dst/terraform/variables" "$dst/terraform/shared-modules"
  cp -R "$SRC/$RD" "$dst/$RD" || fail "could not copy the root out of .corpus"
  rm -rf "$dst/$RD/.terraform"
  cp "$SRC/terraform/variables/variables-common.tf" "$dst/terraform/variables/" || fail "could not copy variables-common.tf, which the root links to"
  cp -R "$SRC/terraform/shared-modules/s3" "$dst/terraform/shared-modules/s3" || fail "could not copy shared-modules/s3, which tempo.tf calls"
  r="$dst/$RD"

  # delta 1: the split. The report is asserted whole, so a pin that moves
  # one block from one side to the other fails here and says which.
  python3 "$HERE/split.py" "$r" "$dst/$PD" > "$dst/split.json" || fail "split.py could not split the root - the corpus pin has moved or the root no longer parses"
  python3 - "$dst/split.json" <<'PY' || fail "the split is not the one this estate was built against - the corpus pin has moved (report above)"
import json, sys
r = json.load(open(sys.argv[1]))
want_live = {
    "module gatekeeper": 1,
    "resource kubernetes_annotations": 1,
    "resource kubernetes_labels": 1,
    "resource kubernetes_namespace_v1": 2,
    "resource kubernetes_secret_v1": 2,
    "resource random_bytes": 1,
    "resource random_password": 3,
    "resource random_uuid": 1,
}
bad = []
if r["live"] != want_live:
    bad.append("live side %s, want %s" % (r["live"], want_live))
for key, n in (("resource helm_release", 14), ("resource kubectl_manifest", 3)):
    if r["stock"].get(key) != n:
        bad.append("stock side holds %s %s block(s), want %d" % (r["stock"].get(key), key, n))
if r["dangling"]:
    bad.append("live blocks still reference the stock half: %s" % r["dangling"])
if r["tfe_vars"] != ["cluster_infrastructure"]:
    bad.append("tfe_outputs workspaces read by the live slice: %s, want [cluster_infrastructure]" % r["tfe_vars"])
want_dropped = 4
if len(r["depends_on_dropped"]) != want_dropped:
    bad.append("%d depends_on entries dropped, want %d: %s" % (len(r["depends_on_dropped"]), want_dropped, r["depends_on_dropped"]))
if bad:
    print("\n".join(bad), file=sys.stderr)
    sys.exit(1)
PY

  # delta 2: the root's own terraform and provider blocks went with the
  # stock half; this is what the live slice needs of them on kind.
  cat > "$r/gauntlet_versions.tf" <<'EOF'
# Added by live/e2e/corpus-govuk-cluster-services/run.sh (delta 2): the
# published root's terraform block (Terraform Cloud workspace,
# required_version ~> 1.15) and its EKS-token provider blocks are in the
# stock half. The hashicorp/kubernetes constraint is the root's own.
terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 3.0"
    }
  }
}

provider "kubernetes" {}
EOF

  # delta 3: data.tfe_outputs.cluster_infrastructure became a variable in
  # the split; declare it, and give it the values the live slice reads.
  grep -q 'data\.tfe_outputs' "$r"/*.tf && fail "delta 3 left a data.tfe_outputs reference in the live slice"
  cat > "$r/gauntlet_inputs.tf" <<'EOF'
# Added by live/e2e/corpus-govuk-cluster-services/run.sh (delta 3):
# data.tfe_outputs.cluster_infrastructure.nonsensitive_values, which reads
# the cluster-infrastructure workspace's outputs from Terraform Cloud, is
# var.cluster_infrastructure in the live slice. terraform.tfvars carries
# placeholder values for the outputs the slice reads.
variable "cluster_infrastructure" {
  type        = any
  description = "Placeholder for the cluster-infrastructure workspace's outputs."
}
EOF
  { cat "$SRC/terraform/variables/integration/common.tfvars" \
        "$SRC/terraform/variables/integration/cluster-services.tfvars" || fail "the integration tfvars are missing - the corpus pin has moved"
    cat <<'EOF'

# Added by live/e2e/corpus-govuk-cluster-services/run.sh (delta 3).
cluster_infrastructure = {
  cluster_id                 = "govuk"
  cluster_oidc_provider_arn  = "arn:aws:iam::000000000000:oidc-provider/placeholder"
  cluster_services_namespace = "cluster-services"
  external_dns_zone_name     = "eks.integration.govuk.digital"
  monitoring_namespace       = "monitoring"
}
EOF
  } > "$r/terraform.tfvars"

  # delta 4: the default class kind ships, not the one EKS does.
  perl -0777 -pi -e 's/(resource "kubernetes_annotations" "rm_default_storageclass" \{.*?metadata \{ name = )"gp2"( \})/$1"standard"$2 # delta 4: kind'"'"'s default class, not EKS'"'"'s gp2/s' "$r/aws_ebs_csi_driver.tf"
  grep -q 'name = "standard" } # delta 4' "$r/aws_ebs_csi_driver.tf" || fail "delta 4 did not match aws_ebs_csi_driver.tf (no gp2 annotations block) - the corpus pin has moved"

  # delta 7: narrow dex_client's ignore_changes from the whole labels map
  # (refused, rule ignore-changes) to the key kubernetes_labels.argocd_secret
  # writes. The original line is asserted first, so a moved pin fails here.
  grep -q '^    ignore_changes = \[metadata\[0\]\.labels\]$' "$r/dex.tf" || fail "delta 7: dex.tf no longer carries ignore_changes = [metadata[0].labels] - the corpus pin has moved"
  grep -q '"app.kubernetes.io/part-of" = "argocd"' "$r/argo.tf" || fail "delta 7: kubernetes_labels.argocd_secret no longer writes app.kubernetes.io/part-of - the corpus pin has moved"
  perl -pi -e 's/^(    ignore_changes = \[metadata\[0\]\.labels)\]$/$1\["app.kubernetes.io\/part-of"\]] # delta 7: the key kubernetes_labels.argocd_secret writes, not the whole map/' "$r/dex.tf"
  [ "$(grep -c 'ignore_changes = \[metadata\[0\]\.labels\["app.kubernetes.io/part-of"\]\] # delta 7' "$r/dex.tf")" = "1" ] || fail "delta 7 did not land exactly once in dex.tf"
  grep -q '^    ignore_changes = \[metadata\[0\]\.labels\]$' "$r/dex.tf" && fail "delta 7 left a whole-map ignore_changes in dex.tf"

  if [ "$mode" = "live" ]; then
    cat >> "$r/gauntlet_versions.tf" <<EOF

terraform {
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}
EOF
    grep -q "estate = \"$ESTATE\"" "$r/gauntlet_versions.tf" || fail "delta 6 did not land in gauntlet_versions.tf"
  fi
}

# ── cluster helpers ──────────────────────────────────────────────────────
kca() { kubectl --kubeconfig "$KCA" "$@"; }
kcb() { kubectl --kubeconfig "$KCB" "$@"; }
stock_b() { ( cd "$ORACLE/$RD" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
chdf_a() { local dir="$1"; shift; ( cd "$dir/$RD" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
# KINDS is a word list on purpose: one kubectl get per kind.
# shellcheck disable=SC2086
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }
# delta 5: the namespaces the stock half's charts would have created.
standins() { # $1 kubectl wrapper
  local ns
  for ns in $STANDINS; do
    "$1" create namespace "$ns" >/dev/null 2>&1 || "$1" get namespace "$ns" >/dev/null 2>&1 || return 1
  done
}

# field_owners <kubeconfig> <kind> <namespace or ""> <name> <labels|annotations> <key>:
# the managers that own metadata.<map>.<key> on one object, comma-joined
# and sorted, read off managedFields (subresource entries skipped); empty
# when nobody does or the object is gone.
field_owners() {
  local cfg="$1" kind="$2" ns="$3" name="$4" map="$5" key="$6"
  local args=(get "$kind" "$name" -o json --show-managed-fields)
  [ -n "$ns" ] && args+=(-n "$ns")
  kubectl --kubeconfig "$cfg" "${args[@]}" 2>/dev/null | python3 -c '
import json, sys
try:
    obj = json.load(sys.stdin)
except Exception:
    sys.exit(0)
key = "f:" + sys.argv[2]
owners = set()
for e in obj["metadata"].get("managedFields", []):
    if e.get("subresource"):
        continue
    if key in e.get("fieldsV1", {}).get("f:metadata", {}).get("f:" + sys.argv[1], {}):
        owners.add(e["manager"])
print(",".join(sorted(owners)))
' "$map" "$key"
}
# field_value <kubeconfig> <kind> <namespace or ""> <name> <labels|annotations> <key>
field_value() {
  local cfg="$1" kind="$2" ns="$3" name="$4" map="$5" key="$6"
  local args=(get "$kind" "$name" -o json)
  [ -n "$ns" ] && args+=(-n "$ns")
  kubectl --kubeconfig "$cfg" "${args[@]}" 2>/dev/null | python3 -c '
import json, sys
try:
    obj = json.load(sys.stdin)
except Exception:
    sys.exit(0)
print((obj["metadata"].get(sys.argv[1]) or {}).get(sys.argv[2], ""))
' "$map" "$key"
}
has_owner() { case ",$1," in *",$2,"*) return 0 ;; esac; return 1; }

# field_check_a <annotation: 1|0>: the estate's field-granular writes on
# cluster A, by value and by owner. Prints nothing when every field reads
# the declared value and is owned by $MGR, and no "Terraform" entry still
# holds it; otherwise one line naming the first field that does not.
# With 0 the StorageClass is not checked (day2_remove released it).
field_check_a() {
  local o v ns
  if [ "$1" = "1" ]; then
    v="$(field_value "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
    o="$(field_owners "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
    [ "$v" = "false" ] || { printf 'StorageClass %s reads %s=%s, want false\n' "$SC" "$DEFAULT_ANN" "${v:-<unset>}"; return; }
    has_owner "$o" "$MGR" || { printf 'StorageClass %s annotation %s is owned by [%s], not %s\n' "$SC" "$DEFAULT_ANN" "$o" "$MGR"; return; }
    has_owner "$o" "Terraform" && { printf 'StorageClass %s annotation %s is still co-owned by stock'"'"'s Terraform manager [%s]\n' "$SC" "$DEFAULT_ANN" "$o"; return; }
  fi
  for ns in $ARGOCD_NAMESPACES; do
    v="$(field_value "$KCA" secret "$ns" dex-client-argocd labels "$PART_OF")"
    o="$(field_owners "$KCA" secret "$ns" dex-client-argocd labels "$PART_OF")"
    [ "$v" = "argocd" ] || { printf 'Secret %s/dex-client-argocd reads %s=%s, want argocd\n' "$ns" "$PART_OF" "${v:-<unset>}"; return; }
    has_owner "$o" "$MGR" || { printf 'Secret %s/dex-client-argocd label %s is owned by [%s], not %s\n' "$ns" "$PART_OF" "$o" "$MGR"; return; }
    has_owner "$o" "Terraform" && { printf 'Secret %s/dex-client-argocd label %s is still co-owned by stock'"'"'s Terraform manager [%s]\n' "$ns" "$PART_OF" "$o"; return; }
  done
}
# field_gate <annotation: 1|0> <where>: fails the current stage on any
# field_check_a finding.
field_gate() {
  local why
  why="$(field_check_a "$1")"
  [ -z "$why" ] || fail "$2: $why"
}
FIELD_NOTE="field manager $MGR owns the StorageClass's $DEFAULT_ANN annotation (false) and $PART_OF on all three dex-client-argocd Secrets, read off managedFields with no Terraform entry left"
FIELD_NOTE_NOANN="field manager $MGR owns $PART_OF on all three dex-client-argocd Secrets, read off managedFields with no Terraform entry left"

# inventory prints the estate's objects on cluster $1, normalised to what
# the configuration declares - Secret data keys and never values (the
# random_* values differ between any two applies), the estate's two
# field-granular writes by value, never the estate label - so stock's cold
# deploy and choudoufu's greenfield apply compare object by object. $2 is a
# key to drop (BREAK's control).
inventory() {
  local cfg="$1" drop="${2:-}"
  KUBECONFIG="$cfg" DROP="$drop" SC="$SC" ANN="$DEFAULT_ANN" PART_OF="$PART_OF" NSS="$ARGOCD_NAMESPACES" python3 - <<'PY'
import json, os, subprocess
drop = os.environ.get("DROP", "")
def get(*args):
    out = subprocess.run(["kubectl", "get"] + list(args) + ["-o", "json"], capture_output=True, text=True)
    return json.loads(out.stdout) if out.returncode == 0 else None
inv = {}
for ns in ("monitoring", "gatekeeper-system"):
    o = get("namespace", ns)
    labels = {} if o is None else {k: v for k, v in (o["metadata"].get("labels") or {}).items()
                                   if k not in ("tofu-estate", "kubernetes.io/metadata.name")}
    inv["namespace/" + ns] = {"exists": o is not None, "labels": labels}
for ns in os.environ["NSS"].split():
    lst = get("secrets", "-n", ns) or {"items": []}
    for o in lst["items"]:
        name = o["metadata"]["name"]
        if not name.startswith("dex-client-"):
            continue
        rec = {"keys": sorted((o.get("data") or {}).keys()), "type": o.get("type")}
        part_of = (o["metadata"].get("labels") or {}).get(os.environ["PART_OF"])
        if part_of is not None:
            rec["part_of"] = part_of
        inv["secret/%s/%s" % (ns, name)] = rec
sc = get("storageclass", os.environ["SC"])
inv["storageclass/" + os.environ["SC"]] = {"exists": sc is not None,
    "default_class": None if sc is None else (sc["metadata"].get("annotations") or {}).get(os.environ["ANN"])}
if drop:
    inv.pop(drop, None)
print(json.dumps(inv, sort_keys=True, indent=1))
PY
}

# ── 1. cold_deploy: stock stands the live slice up on A (and B, the oracle) ─
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters, stock terraform applies the split root on each ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
KIND_VERSION="$(kca version 2>/dev/null | gauntlet_k8s_server_version)"
log "  cluster A: $KIND_VERSION; cluster B: $CLUSTER_B"
kca get storageclass "$SC" >/dev/null 2>&1 || fail "kind's default StorageClass $SC is missing on A - delta 4 has nothing to annotate"
SC_BEFORE_VALUE="$(field_value "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
SC_BEFORE_OWNER="$(field_owners "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
[ "$SC_BEFORE_VALUE" = "true" ] || fail "kind's $SC StorageClass reads $DEFAULT_ANN=${SC_BEFORE_VALUE:-<unset>} before anything ran, want true"
[ -n "$SC_BEFORE_OWNER" ] || fail "nobody owns $DEFAULT_ANN on kind's $SC StorageClass before anything ran - there is no platform manager for force to take it from"
case "$SC_BEFORE_OWNER" in *choudoufu:*|*Terraform*) fail "$DEFAULT_ANN on a fresh kind cluster is already owned by [$SC_BEFORE_OWNER]";; esac
log "  kind's $SC StorageClass: $DEFAULT_ANN=true, owned by [$SC_BEFORE_OWNER]"
write_root "$STOCK" stock
write_root "$ORACLE" stock
standins kca || fail "could not create the stand-in namespaces ($STANDINS) on A"
standins kcb || fail "could not create the stand-in namespaces ($STANDINS) on B"
SPLIT_LIVE="$(python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); print(", ".join("%s x%d" % (k, v) for k, v in sorted(r["live"].items())))' "$STOCK/split.json")"
SPLIT_STOCK="$(python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); print(", ".join("%s x%d" % (k, v) for k, v in sorted(r["stock"].items())))' "$STOCK/split.json")"
log "  live slice:  $SPLIT_LIVE"
log "  stock half:  $SPLIT_STOCK (recorded at $PD, not applied)"
( cd "$STOCK/$RD" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
COLD_OUT="$(cd "$STOCK/$RD" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: $STATE_N added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly $STATE_N instances on A"; }
STOCK_N="$(cd "$STOCK/$RD" && terraform state list | wc -l | tr -d ' ')"
[ "$STOCK_N" = "$STATE_N" ] || fail "stock's state holds $STOCK_N instances, want $STATE_N"
UNMARKED="$(count_a)"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply"
SC_STOCK_VALUE="$(field_value "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
SC_STOCK_OWNER="$(field_owners "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
[ "$SC_STOCK_VALUE" = "false" ] || fail "after stock's apply $SC reads $DEFAULT_ANN=${SC_STOCK_VALUE:-<unset>}, want false"
has_owner "$SC_STOCK_OWNER" "Terraform" || fail "after stock's forced apply $DEFAULT_ANN on $SC is owned by [$SC_STOCK_OWNER], not stock's Terraform manager"
for ns in $ARGOCD_NAMESPACES; do
  [ "$(field_value "$KCA" secret "$ns" dex-client-argocd labels "$PART_OF")" = "argocd" ] || fail "after stock's apply $ns/dex-client-argocd does not carry $PART_OF=argocd"
  has_owner "$(field_owners "$KCA" secret "$ns" dex-client-argocd labels "$PART_OF")" "Terraform" || fail "after stock's apply $PART_OF on $ns/dex-client-argocd is not owned by stock's Terraform manager"
done
inventory "$KCA" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( stock_b init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"
{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "Apply complete! Resources: $STATE_N added" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock cold deploy failed on B"; }
gauntlet_stage cold_deploy pass "$STATE_N instances from plain terraform on the published root's live slice (2 namespaces, one in the local gatekeeper module; 15 dex-client Secrets over 3 namespaces; kubernetes_labels.argocd_secret x3 on three of those Secrets; kubernetes_annotations.rm_default_storageclass with force = true; 17 random_* values), split mechanically by split.py from the stock half ($SPLIT_STOCK - recorded beside the root at $PD, not applied: its charts need IRSA, an AWS load balancer controller and public DNS), against kind $KIND_VERSION; a real terraform.tfstate with $STOCK_N instances and zero tofu-estate labels. Stock's force = true took $DEFAULT_ANN on kind's $SC StorageClass from the platform's own manager [$SC_BEFORE_OWNER] (true -> false, now [$SC_STOCK_OWNER]); $PART_OF=argocd on the three dex-client-argocd Secrets owned by Terraform; the identical root cold-deployed by stock on a second cluster as every later stage's oracle"

# ── 2. migrate ────────────────────────────────────────────────────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
write_root "$ADOPTED" live
( chdf_a "$ADOPTED" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(chdf_a "$ADOPTED" live-import -state="$STOCK/$RD/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
[ "$(field_owners "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")" = "$SC_STOCK_OWNER" ] || fail "the read-only live-import moved the owner of $DEFAULT_ANN on $SC"
APPROVE_OUT="$(chdf_a "$ADOPTED" live-import -state="$STOCK/$RD/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
# A field-granular hand-over is reported as stamped; the random_* values,
# which have no live object, as recorded.
STAMP_WANT=$((LABELLED_N + FIELD_N))
WANT_LINE="$STAMP_WANT resource(s) newly stamped, 0 already stamped, $RANDOM_N newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped."
grep -qF "$WANT_LINE" <<< "$APPROVE_OUT" || { printf '%s\n' "$APPROVE_OUT" | grep -iE 'failed|skipped|refus|handed' | head -20; fail "live-import -approve did not stamp $STAMP_WANT and record $RANDOM_N cleanly: ${SUMMARY_LINE:-no summary line}"; }
HANDED_ANN="$(grep -c 'Handed kubernetes_annotations' <<< "$APPROVE_OUT")"
HANDED_LBL="$(grep -c 'Handed kubernetes_labels' <<< "$APPROVE_OUT")"
if [ "$HANDED_ANN" != "1" ] || [ "$HANDED_LBL" != "3" ]; then
  printf '%s\n' "$APPROVE_OUT" | grep -E 'kubernetes_(labels|annotations)' | head -10
  fail "live-import -approve reported $HANDED_ANN annotations and $HANDED_LBL labels hand-over(s), want 1 and 3"
fi
LABELLED="$(count_a)"
[ "$LABELLED" = "$LABELLED_N" ] || fail "live-import stamped $STAMP_WANT but $LABELLED object(s) carry tofu-estate=$ESTATE, want $LABELLED_N (the namespaces and Secrets; the field-granular writes carry no label)"
field_gate 1 "after live-import -approve"
# The migration evidence a later plan reads (#1869): the record names the
# manager the fields were handed over from.
HANDOVER="$(python3 - "$ADOPTED/$RD/.tofu-records" <<'PY'
import json, os, sys
want = 'kubernetes_annotations.rm_default_storageclass'
for d, _, names in os.walk(sys.argv[1]):
    for n in names:
        try:
            r = json.load(open(os.path.join(d, n)))
        except Exception:
            continue
        if isinstance(r, dict) and r.get('address') == want:
            print((r.get('field_granular') or {}).get('handover_from', ''))
            sys.exit(0)
PY
)"
[ "$HANDOVER" = "Terraform" ] || fail "the record for kubernetes_annotations.rm_default_storageclass carries handover_from='${HANDOVER}', want Terraform - the migration left no evidence a later plan can read"
gauntlet_stage migrate pass "$SUMMARY_LINE: $LABELLED_N namespaces and Secrets stamped tofu-estate=$ESTATE (read back with kubectl), the 4 field-granular instances handed over by field manager rather than labelled (1 kubernetes_annotations, 3 kubernetes_labels, each reported as Handed ... from \"Terraform\"), the $RANDOM_N random_* values seeded into the record store; $FIELD_NOTE; the annotations instance's record carries handover_from = Terraform, the migration evidence #1869's later plans read"

# ── 3. test_plan ──────────────────────────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
IDS_OK=1
kca get namespace "$NS" >/dev/null 2>&1 || IDS_OK=0
kca get namespace gatekeeper-system >/dev/null 2>&1 || IDS_OK=0
for ns in $ARGOCD_NAMESPACES; do
  for client in argocd grafana; do
    kca get secret "dex-client-$client" -n "$ns" >/dev/null 2>&1 || IDS_OK=0
  done
done
kca get storageclass "$SC" >/dev/null 2>&1 || IDS_OK=0
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  field_gate 1 "after the post-migration plan"
  gauntlet_stage test_plan pass "the plan with no state file is empty; both namespaces, six of the fifteen Secrets (argocd and grafana in each namespace) and the patched StorageClass confirmed present by name with kubectl; $FIELD_NOTE"
else
  # The whole plan, not only its Plan: line: which attribute of which
  # block moved is the finding (#1885).
  log "  the post-migration plan with no state file:"
  printf '%s\n' "$PLAN_OUT"
  gauntlet_stage test_plan fail "the plan with no state file is not empty or an identity is missing (ids ok: $IDS_OK): $(grep -E '^Plan:|No changes' <<< "$PLAN_OUT" | head -1)"
  ADOPT_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$ADOPT_OUT" | tail -20; fail "the converging apply failed"; }
  REPLAN_OUT="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"
  grep -q "No changes." <<< "$REPLAN_OUT" || { log "  the replan after the converging apply:"; printf '%s\n' "$REPLAN_OUT"; fail "the replan after the converging apply is not empty"; }
fi

# ── 4. test_apply ─────────────────────────────────────────────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count and the field owners must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
[ "$BEFORE_N" = "$(count_a)" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $(count_a)"
field_gate 1 "after the no-op apply"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across namespaces, Secrets and ConfigMaps, counted with kubectl; $FIELD_NOTE"

# ── 5. drift_reconverge ───────────────────────────────────────────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: kubectl patch one dex-client Secret on A and B; stock's plan on B is the oracle ==="
DRIFT_ADDR='kubernetes_secret_v1.dex_client["monitoring-grafana"]'
tamper() { "$1" patch secret "$2" -n "$NS" --type merge -p '{"stringData":{"clientID":"tampered"}}' >/dev/null; }
tamper kca dex-client-grafana || fail "could not tamper dex-client-grafana on A"
tamper kcb dex-client-grafana || fail "could not tamper dex-client-grafana on B"
if [ "${BREAK:-}" = "1" ]; then
  tamper kca dex-client-prometheus || fail "BREAK: could not tamper dex-client-prometheus on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
grep -qF "$DRIFT_ADDR" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name $DRIFT_ADDR"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" && fail "BREAK=1: two Secrets were tampered but the plan still proposes exactly one change"
  log "  BREAK=1: caught - with a second Secret tampered the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with two Secrets tampered the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one tamper does not propose exactly one change"; }
  grep -qF "$DRIFT_ADDR" <<< "$DRIFT_PLAN" || fail "the plan does not name $DRIFT_ADDR"
  RECONV="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one object"
  [ "$(kca get secret dex-client-grafana -n "$NS" -o jsonpath='{.data.clientID}' | base64 -d 2>/dev/null)" != "tampered" ] || fail "dex-client-grafana still reads tampered after reconverging"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after reconverging is not empty"
  field_gate 1 "after drift_reconverge"
  gauntlet_stage drift_reconverge pass "the monitoring/dex-client-grafana Secret's clientID tampered with kubectl patch; choudoufu proposed exactly $DRIFT_ADDR (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle cluster for the same tamper; apply changed 1, the value reads back as the record store's random_bytes value and the replan is empty; $FIELD_NOTE, untouched by the Secret's own whole-object write. BREAK=1 tampers dex-client-prometheus too and the single-object assertion correctly fails"
fi

# ── 6. plan_approval ──────────────────────────────────────────────────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: the monitoring namespace gains a label in configuration; a saved plan, an out-of-band label elsewhere, a refusal ==="
add_reviewed() { perl -0777 -pi -e 's/(resource "kubernetes_namespace_v1" "monitoring" \{\n  metadata \{\n    name = "monitoring"\n)/$1    labels = { reviewed = "yes" }\n/' "$1/$RD/external_secrets.tf"; grep -q 'reviewed = "yes"' "$1/$RD/external_secrets.tf" || fail "the reviewed-label edit did not match external_secrets.tf - the corpus pin has moved"; }
add_reviewed "$ADOPTED"; add_reviewed "$ORACLE"
P_PLAN="$(chdf_a "$ADOPTED" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
kca label namespace gatekeeper-system stray=yes >/dev/null || fail "could not move the world (label namespace/gatekeeper-system) on A"
P_APPLY="$(chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca label namespace gatekeeper-system stray- >/dev/null
  ( chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed label on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  [ -z "$(kca get namespace "$NS" -o jsonpath='{.metadata.labels.reviewed}')" ] || fail "the namespace gained reviewed despite the refusal"
  kca label namespace gatekeeper-system stray- >/dev/null || fail "could not put the world back"
  P_APPLY2="$(chdf_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get namespace "$NS" -o jsonpath='{.metadata.labels.reviewed}')" = "yes" ] || fail "the namespace does not read reviewed=yes after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  field_gate 1 "after plan_approval"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the monitoring namespace gains reviewed=yes); the world then moved out of band (a stray label on the gatekeeper-system namespace, kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back; stock's own planfile applied on the oracle cluster in the unchanged case; $FIELD_NOTE. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 7. day2_rename ────────────────────────────────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_namespace_v1.monitoring becomes .monitoring_namespace through a moved block; its reference follows ==="
rename_ns() { # $1 root: the block, its one reference (dex_client's depends_on), and the moved block
  perl -pi -e 's/kubernetes_namespace_v1" "monitoring"/kubernetes_namespace_v1" "monitoring_namespace"/' "$1/$RD/external_secrets.tf"
  perl -pi -e 's/kubernetes_namespace_v1\.monitoring\b/kubernetes_namespace_v1.monitoring_namespace/g' "$1/$RD/dex.tf"
  grep -q '"monitoring_namespace"' "$1/$RD/external_secrets.tf" || fail "the rename did not match external_secrets.tf - the corpus pin has moved"
  grep -q 'kubernetes_namespace_v1\.monitoring_namespace' "$1/$RD/dex.tf" || fail "dex_client's depends_on on the monitoring namespace did not follow the rename - the corpus pin has moved"
  grep -qE 'kubernetes_namespace_v1\.monitoring([^_[:alnum:]]|$)' "$1/$RD"/*.tf && fail "a reference to kubernetes_namespace_v1.monitoring survived the rename"
  printf '\nmoved {\n  from = kubernetes_namespace_v1.monitoring\n  to   = kubernetes_namespace_v1.monitoring_namespace\n}\n' >> "$1/$RD/external_secrets.tf"
}
if [ "${BREAK:-}" = "1" ]; then
  perl -0777 -pi -e 's/(resource "kubernetes_namespace_v1" "monitoring" \{\n  metadata \{\n    name = )"monitoring"/$1"monitoring-renamed"/' "$ADOPTED/$RD/external_secrets.tf"
  grep -q '"monitoring-renamed"' "$ADOPTED/$RD/external_secrets.tf" || fail "BREAK: could not rename the namespace's metadata.name"
  R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK: the plan after renaming the object failed"; }
  grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN" || fail "BREAK=1: renaming the namespace's own name did not plan a replace - the marker-rewritten-in-place assertion is not load-bearing: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  log "  BREAK=1: caught - renaming metadata.name plans $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  perl -pi -e 's/"monitoring-renamed"/"monitoring"/' "$ADOPTED/$RD/external_secrets.tf"
  rename_ns "$ADOPTED"; rename_ns "$ORACLE"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the moved-block apply failed"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the namespace's own metadata.name plans a replace ($(grep -E '^Plan:' <<< "$R_PLAN" | head -1)), so the marker-rewritten-in-place assertion correctly fails to hold; the moved block then applied"
else
  rename_ns "$ADOPTED"; rename_ns "$ORACLE"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
  grep -qE 'will be (created|destroyed)' <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a create or a destroy - not the marker rewritten in place"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan is not exactly one in-place change (the address annotation rewrite)"; }
  grep -qE '~ +"choudoufu\.intentius\.io/tofu-address" = ".*" -> ".*"' <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN"; fail "the moved-block plan does not show the tofu-address annotation being rewritten"; }
  R_APPLY_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY_OUT" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$R_APPLY_OUT" || { printf '%s\n' "$R_APPLY_OUT" | tail -10; fail "the moved-block apply was not exactly one in-place change"; }
  kca get namespace "$NS" >/dev/null 2>&1 || fail "the monitoring namespace is gone after the rename"
  [ "$(count_a)" = "$LABELLED_N" ] || fail "$(count_a) labelled objects after the rename, want $LABELLED_N"
  field_gate 1 "after day2_rename"
  gauntlet_stage day2_rename pass "moved block: kubernetes_namespace_v1.monitoring -> .monitoring_namespace, with dex_client's depends_on following, no add and no destroy, one in-place change confined to the address annotation rewrite (0 add, 1 change, 0 destroy) - the marker rewritten in place, the same shape the other kind estates assert; the namespace and the five Secrets in it untouched and still labelled; stock's plan for the same moved block on the oracle cluster is zero churn, since stock never writes this annotation; $FIELD_NOTE. BREAK=1 renames the namespace's own metadata.name and the assertion correctly fails"
fi

# ── 8. day2_remove ────────────────────────────────────────────────────────
#
# The annotations block leaves the configuration (#1878, #1869's removal
# path). The estate never owned the StorageClass, only one field of it, so
# the removal has to release that field and leave the object: the orphan is
# found by the field manager, not by a label, and destroyed by the
# provider's own Delete, an apply of an empty map under the estate's
# manager. Stock's destroy on B is the oracle for the end state.
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the annotations block leaves the configuration ==="
remove_ann() { python3 - "$1/$RD/aws_ebs_csi_driver.tf" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
s2 = re.sub(r'# Patch the obsolete gp2 StorageClass.*?\nresource "kubernetes_annotations" "rm_default_storageclass" \{.*?\n\}\n', '', s, count=1, flags=re.S)
assert s2 != s and 'kubernetes_annotations' not in s2, "the annotations block did not match aws_ebs_csi_driver.tf - the corpus pin has moved"
open(p, 'w').write(s2)
PY
}
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the annotations block kept, no destroy is proposed; the real check is skipped"
  remove_ann "$ADOPTED" || fail "BREAK_REMOVE: could not remove the block afterwards"; remove_ann "$ORACLE" || fail "BREAK_REMOVE: could not remove the block from the oracle root afterwards"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  remove_ann "$ADOPTED" || fail "could not remove the annotations block from the adopted root"
  remove_ann "$ORACLE" || fail "could not remove the annotations block from the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  O_SC_VALUE="$(field_value "$KCB" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
  kcb get storageclass "$SC" >/dev/null 2>&1 || fail "stock's destroy of the annotations block deleted the $SC StorageClass on B"
  D_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan is not exactly one destroy"; }
  D_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | head -1)"
  D_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$D_LINE")"
  grep -qE '^kubernetes_annotations\.orphan_' <<< "$D_ADDR" || { printf '%s\n' "$D_PLAN" | grep -E 'destroyed|^Plan:'; fail "the one destroy is ${D_ADDR:-unnamed}, not the annotations write at a kubernetes_annotations orphan address"; }
  grep -q "$SC" <<< "$D_ADDR" || fail "the orphan address $D_ADDR does not name the $SC StorageClass it patches"
  D_APPLY="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$D_APPLY" | tail -20; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$D_APPLY" || fail "the remove apply did not destroy exactly one instance"
  kca get storageclass "$SC" >/dev/null 2>&1 || fail "removing the annotations block deleted the $SC StorageClass itself - the estate only ever owned one field of it"
  D_OWNER="$(field_owners "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
  has_owner "$D_OWNER" "$MGR" && fail "$MGR still owns $DEFAULT_ANN on $SC after the removal applied: [$D_OWNER]"
  D_SC_VALUE="$(field_value "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
  [ "$D_SC_VALUE" = "$O_SC_VALUE" ] || fail "after the removal $SC reads $DEFAULT_ANN='${D_SC_VALUE}' on A, but stock's destroy left '${O_SC_VALUE}' on B"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the remove is not empty"
  [ "$(count_a)" = "$LABELLED_N" ] || fail "$(count_a) labelled objects after the remove, want $LABELLED_N - a field-granular removal touched a labelled object"
  field_gate 0 "after day2_remove"
  gauntlet_stage day2_remove pass "deleting kubernetes_annotations.rm_default_storageclass's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at the sweep's field-manager orphan address $D_ADDR - an object the estate never owned, found by the fields $MGR held on it, not by a label - applied cleanly; the $SC StorageClass still exists, $MGR no longer owns $DEFAULT_ANN (owners now [${D_OWNER}]), and the annotation reads '${D_SC_VALUE}', the same end state stock's destroy of the same block left on the oracle cluster; the next plan is empty and the labelled count unchanged at $LABELLED_N; $FIELD_NOTE_NOANN. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

# ── 9. day2_count ─────────────────────────────────────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: a two-instance count ConfigMap the script declares scales 2 -> 1 -> 2 ==="
write_shards() { # $1 root, $2 count
  cat > "$1/$RD/count_test.tf" <<EOF
# Added by live/e2e/corpus-govuk-cluster-services/run.sh for the gauntlet's
# day2_count stage: the published root's live slice declares no count block
# that can scale (eph_account's count is 0 outside an ephemeral env).
resource "kubernetes_config_map_v1" "shard" {
  count = $2
  metadata {
    name      = "shard-\${count.index}"
    namespace = kubernetes_namespace_v1.monitoring_namespace.id
  }
  data = { shard = tostring(count.index) }
}
EOF
}
write_shards "$ADOPTED" 2; write_shards "$ORACLE" 2
{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock could not add the two shards on B"; }
{ APPLY_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "choudoufu could not add the two shards on A"; }
write_shards "$ADOPTED" 1; write_shards "$ORACLE" 1
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
C_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$C_PLAN" | head -1)"
C_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$C_LINE")"
grep -qE "^kubernetes_config_map(_v1)?\.(orphan_${NS}_shard-1|shard\[1\])$" <<< "$C_ADDR" || { printf '%s\n' "$C_PLAN" | grep -E 'destroyed|^Plan:'; fail "the scale-down destroys ${C_ADDR:-nothing named}, not shard-1"; }
{ APPLY_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "0 added, 0 changed, 1 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-down apply did not destroy exactly one object"; }
if [ "${BREAK_COUNT:-}" = "1" ]; then
  exists_a configmap shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold"
  log "  BREAK_COUNT=1: caught - shard-0 still exists, so asserting it was the one destroyed correctly fails"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (shard-0) was destroyed correctly fails to hold; the real check is skipped"
else
  exists_a configmap shard-0 || fail "shard-0 was destroyed on the scale-down"
  exists_a configmap shard-1 && fail "shard-1 still exists after the scale-down"
  write_shards "$ADOPTED" 2; write_shards "$ORACLE" 2
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
  grep -q 'kubernetes_config_map_v1.shard\[1\]' <<< "$U_PLAN" || fail "the scale-up does not create shard[1]"
  { APPLY_OUT="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "1 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-up apply did not create exactly one object"; }
  { exists_a configmap shard-0 && exists_a configmap shard-1; } || fail "both shards do not exist after the scale-up"
  grep -q "No changes." <<< "$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up is not empty"
  field_gate 0 "after day2_count"
  gauntlet_stage day2_count pass "a two-instance count ConfigMap added beside the published root in the monitoring namespace (the live slice's only count block is eph_account's, 0 outside an ephemeral env): scaling 2 to 1 destroyed exactly shard-1, planned at $C_ADDR (shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_config_map_v1.shard[1] under the same name; the next plan is empty; stock's plans for the same two changes on the oracle cluster have the identical shape; $FIELD_NOTE_NOANN. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 9b. day2_replace: a create_before_destroy rename ───────────────────
#
# #1541, switched on by #1641: the stage body is shared by the kind
# estates, live/e2e/lib/gauntlet.sh's gauntlet_kind_day2_replace, which adds
# its own block in the monitoring namespace and removes it again.
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a content-hashed ConfigMap renamed under create_before_destroy ==="
gauntlet_kind_day2_replace "$ADOPTED/$RD" "$ORACLE/$RD" "$NS"

# ── 10. day2_crash ────────────────────────────────────────────────────────
#
# day2_crash on the kind substrate (#1110, part 4), as the other kind
# estates take it: first the create_before_destroy rename window (#1768,
# the shared body), then an apply creating two objects with an edge between
# them, killed by the engine itself after the first create commits. The
# next plan has to propose exactly the remainder, with the object already
# created bound by its label rather than created a second time or swept.
# The first of the pair is a Secret, not a ConfigMap, because a
# kubernetes_config_map(_v1) records nothing (#1188, #1235) and the stage
# reads what the record contributes.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one object and the create of the next ==="
gauntlet_kind_day2_crash_rename "$ADOPTED/$RD" "$NS"

write_crash_pair() { # $1 root, $2 = "first" for crash-first alone
  cat > "$1/$RD/crash_test.tf" <<EOF
# Added by live/e2e/corpus-govuk-cluster-services/run.sh for the gauntlet's
# day2_crash stage: the published root declares no pair of objects with an
# edge between them that an interrupted apply could be caught halfway
# through. The first is a Secret because a kubernetes_config_map(_v1)
# records nothing (#1188, #1235).
resource "kubernetes_secret_v1" "crash_first" {
  metadata {
    name      = "crash-first"
    namespace = kubernetes_namespace_v1.monitoring_namespace.id
  }
  data = { step = "one" }
}
EOF
  [ "${2:-both}" = "first" ] && return 0
  cat >> "$1/$RD/crash_test.tf" <<EOF

resource "kubernetes_config_map_v1" "crash_second" {
  metadata {
    name      = "crash-second"
    namespace = kubernetes_namespace_v1.monitoring_namespace.id
  }
  data = { after = kubernetes_secret_v1.crash_first.metadata[0].name }
}
EOF
}

write_crash_pair "$ORACLE" first
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan on B is not exactly one add"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash-first apply failed on B"
write_crash_pair "$ORACLE"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" \
  || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add - the oracle for this stage is not what it should be"; }
grep -q 'kubernetes_config_map_v1.crash_second' <<< "$O_REMAINDER" || fail "stock's remainder plan on B does not name crash_second"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
log "  oracle: stock at crash-first alone plans exactly one add (crash_second) for the remainder, and applies it"

write_crash_pair "$ADOPTED"
X_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" \
  || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds - there is no two-object apply to interrupt"; }
X_RECORDS_BEFORE="$(gauntlet_record_envelope_count "$ADOPTED/$RD/.tofu-records")"

# The interrupt is delivered by the engine itself, so this runs in the
# plain foreground: no background process, no output tailing, no poll loop.
# A non-zero exit is the NORMAL outcome - the process was killed.
X_OUT="$( cd "$ADOPTED/$RD" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" \
  TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_secret_v1.crash_first" \
  "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1 )"; X_RC=$?
printf '%s\n' "$X_OUT" > "$WORK/day2_crash.log"
log "  interrupted apply exited $X_RC (a genuine crash is not expected to exit 0)"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted and this stage would measure a clean apply"; }
exists_a secret crash-first || { printf '%s\n' "$X_OUT" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed, so there is no crash window to recover from"; }
exists_a configmap crash-second && { printf '%s\n' "$X_OUT" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates, so there is no remainder to propose"; }
# The selector alone, never a selector next to a resource name: kubectl
# refuses that combination outright, which would read as an unlabelled
# object.
{ GET_OUT="$(kca get secret -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null)" && grep -qx "secret/crash-first" <<< "$GET_OUT"; } \
  || { printf '%s\n' "$GET_OUT"; fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE - the marker the rerun is supposed to find is not there"; }
X_RECORDS_AFTER="$(gauntlet_record_envelope_count "$ADOPTED/$RD/.tofu-records")"
log "  crash-first exists and is labelled; crash-second does not exist; records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER"

X_REC="$(gauntlet_record_file "$ADOPTED/$RD/.tofu-records" "kubernetes_secret_v1.crash_first")"
[ -n "$X_REC" ] || fail "the interrupted apply created crash-first but wrote no record for kubernetes_secret_v1.crash_first (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER); there is nothing for the recovery to read back and this stage cannot measure what the record contributes"
[ "$X_RECORDS_AFTER" = "$((X_RECORDS_BEFORE + 1))" ] || fail "records went $X_RECORDS_BEFORE -> $X_RECORDS_AFTER across the interrupted apply, want exactly one more - the apply is supposed to have written the record for the one object it did create, and nothing else"
X_RESIDUE="$(gauntlet_record_residue "$X_REC" | tr '\n' ' ' | sed 's/ $//')"
[ "$X_RESIDUE" = "wait_for_service_account_token" ] || fail "the record the interrupted apply wrote for kubernetes_secret_v1.crash_first carries residue [${X_RESIDUE:-none}], want wait_for_service_account_token (#1188, #1235)"
log "  the interrupted apply wrote one record for kubernetes_secret_v1.crash_first, carrying residue $X_RESIDUE"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  kca label secret crash-first -n "$NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
  log "  BREAK_CRASH_UNBOUND=1: stripped tofu-estate off crash-first with kubectl"
fi

R_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(grep -E '^Plan:|^No changes' <<< "$R_PLAN" | head -1 | sed 's/\.$//')"
# What the real check asserts, as one predicate, so the Break controls can
# require the SAME predicate to fail rather than approximating it.
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# kubernetes_config_map(_v1)?\.crash_second will be created' <<< "$R_PLAN" || return 1
  local r_proposed
  r_proposed="$(grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN")"
  grep -q 'crash_first\|crash-first' <<< "$r_proposed" && return 1
  return 0
}

if [ "${BREAK_CRASH:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH=1). assert nothing is proposed after the interrupt - this must fail ==="
  [ "$R_RC" -eq 0 ] || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $R_RC"; }
  grep -qF "No changes." <<< "$R_PLAN" \
    && { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty, so this stage's own check is not load-bearing"; }
  log "  BREAK_CRASH=1: caught - the plan proposes work ($R_LINE), so 'nothing is proposed' correctly fails to hold"
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so the stage's own Break line - interrupt and then assert nothing is proposed - correctly fails to hold; the real check is skipped $CRASH_RENAME_DETAIL"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  log "=== 10b (BREAK_CRASH_UNBOUND=1). the same check against an unbound object - this must fail ==="
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash-first carrying no tofu-estate label - it is not measuring whether the crashed-out object was bound at all"
  fi
  log "  BREAK_CRASH_UNBOUND=1: caught - with the label stripped the recovery check fails ($R_LINE)"
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the object the interrupted apply created - the unrecovered run this stage exists to catch - the recovery check correctly fails to hold ($R_LINE); the real check is skipped $CRASH_RENAME_DETAIL"
  kca delete secret crash-first -n "$NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first afterwards"
  ( chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
else
  if ! recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of kubernetes_secret_v1.crash_first and the create of kubernetes_config_map_v1.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). crash-first exists on the cluster carrying tofu-estate=$ESTATE and crash-second does not, both read with kubectl; stock, walked into the same position on the oracle cluster, plans exactly one add (crash_second). The interrupted apply wrote one record for kubernetes_secret_v1.crash_first carrying residue ${X_RESIDUE:-none} (records $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) $CRASH_RENAME_DETAIL"
  else
    # The record's contribution, measured rather than counted (#1235): take
    # the one record the interrupted apply wrote out of the store, replan
    # from the identical position, and the difference IS what it carried.
    mv "$X_REC" "$WORK/crash_first.record" || fail "could not move the crash record aside"
    N_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"; N_RC=$?
    N_LINE="$(grep -E '^Plan:|^No changes' <<< "$N_PLAN" | head -1 | sed 's/\.$//')"
    [ "$N_RC" -eq 0 ] || { printf '%s\n' "$N_PLAN" | tail -20; fail "the replan with the crash record taken out of the store exited $N_RC"; }
    grep -qF "Plan: 1 to add, 1 to change, 0 to destroy." <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E '^Plan:|will be|^ +[+~-] ' | head -20
           fail "with the one record the interrupted apply wrote taken out of the store, the recovery plan is $N_LINE, not the remainder plus one in-place update - so the record contributed nothing this stage can read"; }
    grep -qE '^[[:space:]]+\+ wait_for_service_account_token +=' <<< "$N_PLAN" \
      || { printf '%s\n' "$N_PLAN" | grep -E 'will be|^ +[+~-] ' | head -20
           fail "the extra in-place update the missing record produces does not propose wait_for_service_account_token back"; }
    mv "$WORK/crash_first.record" "$X_REC" || fail "could not put the crash record back"
    B_PLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$B_PLAN" \
      || { printf '%s\n' "$B_PLAN" | grep -E '^Plan:|will be' | head -10
           fail "putting the record back does not restore the exact-remainder plan, so the difference measured above is not the record's"; }
    log "  the record's contribution: with it, $R_LINE; without it, $N_LINE (+ wait_for_service_account_token on crash_first); with it again, the remainder"

    R_APPLY="$(chdf_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)"; R_APPLY_RC=$?
    [ "$R_APPLY_RC" -eq 0 ] || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply exited $R_APPLY_RC"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" \
      || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining object"; }
    exists_a configmap crash-second || fail "crash-second does not exist after the recovery apply"
    exists_a secret crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
    R_REPLAN="$(chdf_a "$ADOPTED" plan -input=false -no-color 2>&1)"; R_REPLAN_RC=$?
    [ "$R_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$R_REPLAN" | tail -30
      R_ERR="$(grep -E '^Error' <<< "$R_REPLAN" | head -1)"
      fail "the replan after the recovery exited $R_REPLAN_RC: ${R_ERR:-no Error: line; the last 30 lines of the plan are above this verdict in the log}"; }
    grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the recovery is not empty"; }
    field_gate 0 "after day2_crash"
    gauntlet_stage day2_crash pass "a Secret and a ConfigMap added beside the published root in the monitoring namespace, the second reading the first's name: the apply creating both was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 graph walker the instant kubernetes_secret_v1.crash_first's create committed; kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and crash-second does not. The next plan proposed exactly the remainder ($R_LINE, kubernetes_config_map_v1.crash_second created) and nothing for crash-first, which it bound by its label and its namespace and name, matching stock's own plan from the same position on the oracle cluster; the recovery apply added exactly one object and the plan after it is empty. The record's contribution is read, not counted: the interrupted apply wrote exactly one record (envelopes $X_RECORDS_BEFORE -> $X_RECORDS_AFTER) carrying residue $X_RESIDUE, and taking it out of the store turns the recovery plan from $R_LINE into $N_LINE, proposing wait_for_service_account_token back; putting it back restores the exact remainder; $FIELD_NOTE_NOANN. BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 strips the label off crash-first and the same recovery check correctly fails $CRASH_RENAME_DETAIL"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown ─────────────────────────────────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
# Not every instance carries the label: the destroy is the labelled objects
# plus the three label writes plus the random_* values (the annotations
# write left at day2_remove).
T_LABELLED="$(count_a)"
T_EXPECT=$((T_LABELLED + 3 + RANDOM_N))
T_OUT="$(chdf_a "$ADOPTED" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly $T_EXPECT instances ($T_LABELLED labelled objects, 3 label writes, $RANDOM_N random values)"; }
gauntlet_wait_until 120 "the monitoring and gatekeeper-system namespaces to finish deleting" -- \
  sh -c "! kubectl --kubeconfig '$KCA' get namespace '$NS' >/dev/null 2>&1 && ! kubectl --kubeconfig '$KCA' get namespace gatekeeper-system >/dev/null 2>&1" \
  || fail "the estate's namespaces still exist two minutes after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
for ns in $STANDINS; do
  GET_OUT="$(kca get secrets -n "$ns" -o name 2>/dev/null)"
  grep -q 'secret/dex-client-' <<< "$GET_OUT" && { printf '%s\n' "$GET_OUT"; fail "dex-client Secrets are left in the stand-in namespace $ns after the destroy"; }
done
kca get storageclass "$SC" >/dev/null 2>&1 || fail "the $SC StorageClass is gone after the destroy - the estate never owned it"
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT instances its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining instances in one apply ($T_LABELLED labelled objects, the three kubernetes_labels writes and $RANDOM_N random values), the monitoring and gatekeeper-system namespaces gone, no dex-client Secret left in the stand-in namespaces, no object of the estate's kinds carrying tofu-estate=$ESTATE (kubectl, every namespace), and kind's $SC StorageClass, never the estate's, still there; stock's destroy of the same estate on the oracle cluster also removed exactly the $O_EXPECT its state held"

# ── 12. greenfield ────────────────────────────────────────────────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the split root fresh, with a live block, on the now-empty cluster A ==="
# The platform's own write first: day2_remove released the annotation, so
# put it back the way kind ships it, under a manager that is not an
# estate's. force = true has to take it from that manager, and is not
# refused for doing so (only another estate's field is).
kca annotate --overwrite storageclass "$SC" "$DEFAULT_ANN=true" >/dev/null || fail "could not restore $DEFAULT_ANN=true on $SC"
G_SC_OWNER_BEFORE="$(field_owners "$KCA" storageclass "" "$SC" annotations "$DEFAULT_ANN")"
[ -n "$G_SC_OWNER_BEFORE" ] || fail "nobody owns $DEFAULT_ANN on $SC after kubectl annotate"
case "$G_SC_OWNER_BEFORE" in *choudoufu:*) fail "$DEFAULT_ANN on $SC is owned by an estate's manager [$G_SC_OWNER_BEFORE] before greenfield; force over it would be refused by design";; esac
write_root "$GREEN" live
( chdf_a "$GREEN" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
G_OUT="$(chdf_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)"; G_RC=$?
grep -q "Force refused" <<< "$G_OUT" && { printf '%s\n' "$G_OUT" | grep -A5 "Force refused"; fail "greenfield's force = true over the platform's own manager [$G_SC_OWNER_BEFORE] was refused - only another estate's field may be"; }
[ "$G_RC" -eq 0 ] || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply exited $G_RC"; }
grep -qF "Apply complete! Resources: $STATE_N added, 0 changed, 0 destroyed" <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -5; fail "greenfield apply did not add exactly $STATE_N instances"; }
[ ! -f "$GREEN/$RD/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
[ "$(count_a)" = "$LABELLED_N" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after the greenfield apply, want $LABELLED_N"
field_gate 1 "after the greenfield apply"
G_RECORDS="$(gauntlet_record_envelope_count "$GREEN/$RD/.tofu-records")"
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the greenfield replan is not empty"
rm -f "$GREEN/$RD/.terraform/choudoufu-cache.tfstate"
grep -q "No changes." <<< "$(chdf_a "$GREEN" plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache is not empty"
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="storageclass/$SC"
inventory "$KCA" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null && fail "BREAK=1: with the StorageClass dropped the two inventories still match"
  log "  BREAK=1: caught - the inventories differ once the StorageClass is dropped"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the StorageClass's annotation from the greenfield inventory makes the object-by-object comparison correctly fail; the estate applied ($STATE_N added, no terraform.tfstate) and replanned empty with and without the cache"
else
  if ! diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json"; then
    fail "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  fi
  gauntlet_stage greenfield pass "the split root applied fresh with a live block and no terraform.tfstate: $STATE_N instances, the $LABELLED_N namespaces and Secrets labelled tofu-estate=$ESTATE (kubectl), the record store holding $G_RECORDS record envelope(s); force = true took $DEFAULT_ANN on kind's $SC StorageClass from the platform's own manager [$G_SC_OWNER_BEFORE] with no refusal, since that manager is not an estate's; $FIELD_NOTE; replanned empty with and without the cache. The cluster's inventory (both namespaces and the gatekeeper labels, every dex-client Secret's keys and type in all three namespaces, $PART_OF on the three argocd Secrets, the StorageClass's default-class annotation) matches stock's cold deploy on the same cluster object by object, the estate label never compared. BREAK=1 drops the StorageClass from the expected inventory and the match correctly fails"
fi
( chdf_a "$GREEN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield teardown failed"

# ── 13. strict ────────────────────────────────────────────────────────────
gauntlet_begin_stage strict
STRICT="$WORK/strict/$RD"; mkdir -p "$STRICT"
strict_block() { cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "corpus-govuk-cluster-services-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["kubernetes_config_map_v1"]
      }
    }
  }
}

resource "random_password" "dex_secret" {
  length  = 32
  special = false
}
EOF
}
log "=== 13. strict: every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
( chdf_a "$WORK/strict" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(chdf_a "$WORK/strict" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block "store" > "$STRICT/main.tf"
  STRICT_OFF="$(chdf_a "$WORK/strict" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -20; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC"; }
  grep -q "^Error:" <<< "$STRICT_OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  grep -qF 'random_password.dex_secret will be created' <<< "$STRICT_OFF" || fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.dex_secret"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create"
else
  [ "$STRICT_ON_RC" -eq 1 ] || { printf '%s\n' "$STRICT_ON" | tail -20; fail "the every-toggle-on plan exited $STRICT_ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$STRICT_ON")" -eq 1 ] || { printf '%s\n' "$STRICT_ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_config_map_v1) against a scratch estate carrying the root's own random_password shape (dex_secret's): exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "corpus-govuk-cluster-services: done"
