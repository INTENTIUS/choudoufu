#!/usr/bin/env bash
# reference-k8s-cert-manager: the kubernetes lane's CRD estate (#1174,
# under #1173's cold-deploy ruling and #1016's marker ruling), crossed on
# the kind substrate.
#
# The configuration is cert-manager v1.21.2's own install bundle converted
# mechanically to 47 `kubernetes_manifest` blocks (convert.sh records the
# URL, the size, the sha256, the licence and the tfk8s version, and
# reproduces root/cert-manager.tf), plus three custom resources written
# here from cert-manager's own self-signed documentation: a cluster-scoped
# ClusterIssuer, a namespaced Issuer and a Certificate. 50 objects over 13
# kinds, six CRDs, three public quay.io images, no credentials. day2_count
# adds a fourth, counted, for the length of its own stage and takes it away
# again - see there for why it is not in the committed root.
#
# What it drives that nothing else in the lane does:
#
#   - `kubernetes_manifest` over real CRDs at every stage, where claim 24
#     proves one CronTab;
#   - a cluster-scoped custom kind (ClusterIssuer);
#   - the estate sweep over custom kinds at day2_remove and day2_count;
#   - two `failurePolicy: Fail` webhooks on the admission path of every
#     custom resource, including the server-side dry run
#     `kubernetes_manifest` does at PLAN time;
#   - a controller-created object that outlives its owner: the
#     Certificate's Secret `example-com-tls` carries
#     `controller.cert-manager.io/fao` and no estate label, which is the
#     controller-copy exclusion the sweep has to get right.
#
# ── the two applies, and why they are not a trick ────────────────────────
#
# A single root cannot PLAN a CRD and an object of that CRD:
# `kubernetes_manifest` builds the object's schema at plan time, so the
# plan fails before anything is created, `depends_on` does not help, and
# re-running fails identically forever. Stock terraform has exactly the
# same problem with exactly the same configuration - step 1 below runs the
# un-targeted plan as a CONTROL and requires it to fail.
#
# So cold_deploy declares a pre-apply (#1173): the 47 bundle addresses are
# listed in live/gauntlet/estates.json's `pre_apply`, applied with
# `-target` FIRST, and gauntlet_pre_apply drives both sides from that one
# list in one call so the estate and the stock oracle cannot diverge. The
# verdict line names it; tools/gauntlet's runner fails the stage if it does
# not.
#
# Two kind clusters, both created for this run and deleted after it, the
# same A/B shape reference-k8s uses:
#
#   A  the estate. Stock cold-deploys it, choudoufu adopts it from stock's
#      state and runs every day-2 stage on it, tears it down, then applies
#      the same shape fresh with a live block (greenfield).
#   B  the oracle. Stock cold-deploys the identical shape, pre-apply
#      included, and applies every day-2 change itself.
#
# A stage this script cannot pass records fail with the reason and the run
# continues; the runner decides what "clear" means. A fault in the
# substrate itself is a fail on the stage being set up and an exit.
#
#   go run ./tools/gauntlet run reference-k8s-cert-manager
#   bash live/e2e/reference-k8s-cert-manager/run.sh
#
# Needs kind, kubectl, terraform (the stock oracle), Docker and python3.
# No network beyond the three quay.io image pulls: the root is committed.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the go build.
#   BREAK          run drift_reconverge's and greenfield's negative
#                  controls instead of the real checks.
#   BREAK_PREAPPLY apply the whole root in ONE pass at cold deploy and
#                  require it to succeed; it must fail, which is what
#                  makes the pre-apply load-bearing rather than decorative.
#   BREAK_REMOVE   keep the Issuer block and assert no destroy is proposed.
#   BREAK_COUNT    assert the wrong shard was destroyed on the scale-down.
#   BREAK_APPROVAL apply the saved plan after the world moved and expect
#                  success.
#   BREAK_STRICT   turn secrets back to "store" and require the refusal to
#                  vanish.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"
ESTATE="reference-k8s-cert-manager"
NS="cert-manager"
# The estate's kinds, for the label count. The last four only exist once
# the CRDs are installed; kubectl prints nothing for a kind the API server
# does not serve, which counts as zero, which is correct.
KINDS="namespaces customresourcedefinitions serviceaccounts clusterroles clusterrolebindings roles rolebindings services deployments mutatingwebhookconfigurations validatingwebhookconfigurations clusterissuers issuers certificates"
BUNDLE_N=47   # objects in the converted bundle, the pre-apply's population
CUSTOM_N=3    # ClusterIssuer, Issuer, Certificate
TOTAL_N=$((BUNDLE_N + CUSTOM_N))
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
KCA="$WORK/a.kubeconfig"; KCB="$WORK/b.kubeconfig"
CLUSTER_A="chdf-refcm-a-$$"; CLUSTER_B="chdf-refcm-b-$$"
export TF_PLUGIN_CACHE_DIR="$WORK/plugin-cache"; mkdir -p "$TF_PLUGIN_CACHE_DIR"
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

# ── 0. tools ─────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed as the stock oracle"
command -v kind >/dev/null 2>&1 || fail "kind is not on PATH (brew install kind)"
command -v kubectl >/dev/null 2>&1 || fail "kubectl is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"
[ -f "$HERE/root/cert-manager.tf" ] || fail "$HERE/root/cert-manager.tf is missing; run convert.sh"
[ -f "$HERE/root/custom-resources.tf" ] || fail "$HERE/root/custom-resources.tf is missing"

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

# ── the shape ────────────────────────────────────────────────────────────
# The committed root is what runs: every working copy is a plain cp of
# root/, plus a versions.tf this script writes (the bundle carries no
# provider block, and only choudoufu's side gets a live block). Nothing
# else about the configuration is written here, so what a reader sees in
# root/ is what the crossing measured.
versions_block() { # $1 = "live" for the live block, anything else for stock
  cat <<EOF
terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
EOF
  if [ "$1" = "live" ]; then cat <<EOF
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
EOF
  fi
  cat <<'EOF'
}

provider "kubernetes" {}
EOF
}
write_root() { # $1 dir, $2 live|stock
  mkdir -p "$1"
  cp "$HERE/root/cert-manager.tf" "$HERE/root/custom-resources.tf" "$1/" || return 1
  versions_block "$2" > "$1/versions.tf"
}

# append_shards <dir> <n> / remove_shards <dir>: day2_count's counted
# custom resource. It lives here rather than in the committed root because
# choudoufu cannot re-plan it (see the stage), and a root that cannot be
# re-planned would fail test_plan and every stage after it - measuring one
# defect by hiding eleven other measurements.
append_shards() {
  cat >> "$1/custom-resources.tf" <<EOF

resource "kubernetes_manifest" "issuer_shard" {
  count = $2
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Issuer"
    "metadata" = {
      "name"      = "shard-\${count.index}"
      "namespace" = "$NS"
    }
    "spec" = {
      "selfSigned" = {}
    }
  }

  depends_on = [kubernetes_manifest.namespace_cert_manager]
}
EOF
}
remove_shards() {
  python3 - "$1/custom-resources.tf" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
out = re.sub(r'\nresource "kubernetes_manifest" "issuer_shard" \{.*?\n\}\n', '\n', s, flags=re.S)
assert 'issuer_shard' not in out, "the counted Issuer block was not removed"
open(p, 'w').write(out)
PY
}

# review_annotation <dir>: a metadata-side edit. plan_approval makes it
# first, as a measurement of its own - see there.
review_annotation() {
  python3 - "$1/custom-resources.tf" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old = '''    "metadata" = {
      "name" = "selfsigned"
    }'''
new = '''    "metadata" = {
      "annotations" = {
        "reviewed" = "yes"
      }
      "name" = "selfsigned"
    }'''
assert old in s, "the ClusterIssuer's metadata block is not the shape this edit expects"
open(p, 'w').write(s.replace(old, new, 1))
PY
}
# extra_dnsname <dir>: plan_approval's real one-field change. It is a
# SPEC-side field on purpose: measured 2026-09-16, choudoufu's plan does
# not see a change to metadata.labels or metadata.annotations on a
# kubernetes_manifest at all (stock plans one in-place update for the same
# edit; choudoufu plans No changes and applies nothing), so an
# annotation-shaped edit would make this stage re-measure that defect
# instead of measuring plan approval. The stage below measures BOTH: the
# metadata edit first, recorded as the gap it is, then this one.
extra_dnsname() {
  python3 - "$1/custom-resources.tf" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old = '''      "dnsNames" = [
        "example.com",
      ]'''
new = '''      "dnsNames" = [
        "example.com",
        "www.example.com",
      ]'''
assert old in s, "the Certificate's dnsNames list is not the shape this edit expects"
open(p, 'w').write(s.replace(old, new, 1))
PY
}

# rename_clusterissuer <dir>: the block rename day2_rename's moved block covers.
rename_clusterissuer() {
  python3 - "$1/custom-resources.tf" <<'PY'
import sys
p = sys.argv[1]; s = open(p).read()
old = 'resource "kubernetes_manifest" "clusterissuer_selfsigned" {'
new = 'resource "kubernetes_manifest" "clusterissuer_review" {'
assert old in s
s = s.replace(old, new, 1)
s += '''
moved {
  from = kubernetes_manifest.clusterissuer_selfsigned
  to   = kubernetes_manifest.clusterissuer_review
}
'''
open(p, 'w').write(s)
PY
}
# remove_issuer <dir>: day2_remove's block deletion (a namespaced custom kind).
remove_issuer() {
  python3 - "$1/custom-resources.tf" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
n = len(re.findall(r'^resource "kubernetes_manifest"', s, re.M))
out = re.sub(r'resource "kubernetes_manifest" "issuer_selfsigned" \{.*?\n\}\n\n', '', s, flags=re.S)
assert len(re.findall(r'^resource "kubernetes_manifest"', out, re.M)) == n - 1, "the Issuer block was not removed"
open(p, 'w').write(out)
PY
}
# remove_certificate <dir>: day2_remove's second half, the object whose
# controller created something else.
remove_certificate() {
  python3 - "$1/custom-resources.tf" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
n = len(re.findall(r'^resource "kubernetes_manifest"', s, re.M))
out = re.sub(r'resource "kubernetes_manifest" "certificate_example_com" \{.*?\n\}\n', '', s, flags=re.S)
assert len(re.findall(r'^resource "kubernetes_manifest"', out, re.M)) == n - 1, "the Certificate block was not removed"
open(p, 'w').write(out)
PY
}

# ── cluster helpers ──────────────────────────────────────────────────────
kca() { kubectl --kubeconfig "$KCA" "$@"; }
kcb() { kubectl --kubeconfig "$KCB" "$@"; }
tofu_a() { ( cd "$ADOPTED" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
stock_a() { ( cd "$STOCK"  && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" terraform "$@" ); }
stock_b() { ( cd "$ORACLE" && KUBECONFIG="$KCB" KUBE_CONFIG_PATH="$KCB" terraform "$@" ); }
count_a() { KUBECONFIG="$KCA" gauntlet_kind_count "$ESTATE" $KINDS; }
exists_a() { kca get "$1" "$2" -n "$NS" >/dev/null 2>&1; }

# webhook_admits <kubeconfig>: one server-side dry run of an Issuer. It is
# the only honest readiness test for a failurePolicy: Fail webhook - the
# Deployment being Available says the pods are up, not that the webhook
# service answers, and `kubernetes_manifest` does exactly this dry run at
# plan time.
cat > "$WORK/probe-issuer.yaml" <<EOF
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: webhook-readiness-probe
  namespace: $NS
spec:
  selfSigned: {}
EOF
webhook_admits() { kubectl --kubeconfig "$1" apply --dry-run=server -f "$WORK/probe-issuer.yaml"; }

# cert_manager_ready <kubeconfig> <label>: bounded, loud. Both waits fail
# the stage rather than falling through into the admission error a webhook
# that exists but is not yet serving produces (#1173).
cert_manager_ready() {
  local cfg="$1" label="$2"
  kubectl --kubeconfig "$cfg" wait --for=condition=Available --timeout=300s deployment --all -n "$NS" >/dev/null 2>&1 \
    || { printf 'cert_manager_ready: the cert-manager Deployments on %s did not become Available within 300s\n' "$label" >&2
         kubectl --kubeconfig "$cfg" get pods -n "$NS" >&2; return 1; }
  gauntlet_wait_until 180 "the cert-manager validating webhook on $label to admit an Issuer (failurePolicy: Fail)" -- webhook_admits "$cfg"
}

# ── 1. cold_deploy: the control, the declared pre-apply, then the rest ───
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two kind clusters; the un-targeted plan must fail, then the declared pre-apply on both sides ==="
gauntlet_kind_up "$CLUSTER_A" "$KCA" || fail "kind cluster A ($CLUSTER_A) did not come up"
gauntlet_kind_up "$CLUSTER_B" "$KCB" || fail "kind cluster B ($CLUSTER_B) did not come up"
export KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA"
K8S_VER="$(kca version 2>/dev/null | grep -io 'v1\.[0-9.]*' | tail -1)"
log "  cluster A: $CLUSTER_A (kubernetes $K8S_VER); cluster B: $CLUSTER_B"
write_root "$STOCK" stock  || fail "could not write the stock root on A"
write_root "$ORACLE" stock || fail "could not write the oracle root on B"
( stock_a init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
( stock_b init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"

# The control the issue asks for: without the pre-apply the plan does not
# merely take two tries, it cannot be planned at all, forever.
CTRL="$(stock_a plan -input=false -no-color 2>&1)"; CTRL_RC=$?
if [ "${BREAK_PREAPPLY:-}" = "1" ]; then
  if [ "$CTRL_RC" -eq 0 ]; then
    fail "BREAK_PREAPPLY=1: the un-targeted plan succeeded, so the declared pre-apply is not load-bearing and this estate proves nothing about #1173"
  fi
  log "  BREAK_PREAPPLY=1: caught - the one-pass plan exited $CTRL_RC: $(grep -m1 'no matches for kind' <<< "$CTRL")"
  gauntlet_stage cold_deploy pass "BREAK_PREAPPLY=1 control: applying the whole root in one pass exits $CTRL_RC before creating anything ($(grep -m1 'no matches for kind' <<< "$CTRL")), so 'one apply is enough' correctly fails; the real two-apply crossing is skipped"
else
  [ "$CTRL_RC" -ne 0 ] || fail "the un-targeted plan SUCCEEDED; the CRD plan-time refusal this estate exists to exercise is gone, and the declared pre-apply is now measuring nothing (delete it, or find out what changed)"
  grep -qF "CRD may not be installed" <<< "$CTRL" || { printf '%s\n' "$CTRL" | tail -20; fail "the un-targeted plan failed for some reason other than the missing CRD"; }
  CTRL_LINE="$(grep -m1 'no matches for kind' <<< "$CTRL" | sed 's/^ *//')"
  log "  control: the one-pass plan fails as documented - $CTRL_LINE"

  # The declared pre-apply. One call, both sides, one address list read
  # from live/gauntlet/estates.json - neither side can get a different one.
  pre_apply_estate() { stock_a apply -auto-approve -input=false -no-color "$@" >/dev/null; }
  pre_apply_oracle() { stock_b apply -auto-approve -input=false -no-color "$@" >/dev/null; }
  gauntlet_pre_apply "$ESTATE" estate:pre_apply_estate oracle:pre_apply_oracle \
    || fail "the declared pre-apply failed"
  PRE_NOTE="$(gauntlet_pre_apply_note)" || fail "the pre-apply ran but produced no note to put in the verdict"
  [ "$(stock_a state list | wc -l | tr -d ' ')" = "$BUNDLE_N" ] || fail "the pre-apply on A left $(stock_a state list | wc -l | tr -d ' ') instances in state, want $BUNDLE_N"

  cert_manager_ready "$KCA" "cluster A" || fail "cert-manager never became ready on A after the pre-apply"
  cert_manager_ready "$KCB" "cluster B" || fail "cert-manager never became ready on B after the pre-apply"

  COLD_OUT="$(stock_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock's main apply failed on A"; }
  grep -qF "Apply complete! Resources: $CUSTOM_N added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock's main apply on A did not add exactly the $CUSTOM_N custom resources"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's main apply failed on B"

  [ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
  STOCK_N="$(stock_a state list | wc -l | tr -d ' ')"
  [ "$STOCK_N" = "$TOTAL_N" ] || fail "stock's state holds $STOCK_N instances, want $TOTAL_N"
  UNMARKED="$(count_a)"
  [ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
  CERT_READY="$(kca get certificate example-com -n "$NS" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)"
  [ "$CERT_READY" = "True" ] || fail "the Certificate is not Ready after stock's cold deploy (status: ${CERT_READY:-none}); the self-signed issuance never completed and later stages would measure a broken estate"
  log "  $TOTAL_N objects on A ($BUNDLE_N pre-applied + $CUSTOM_N), a real terraform.tfstate, zero labels; the same on B for the oracle"
  gauntlet_stage cold_deploy pass "$TOTAL_N objects (cert-manager v1.21.2's 47-object bundle over 11 kinds plus $CUSTOM_N custom resources over 3 custom kinds) from plain terraform against kind $K8S_VER, a real terraform.tfstate with $TOTAL_N instances, zero tofu-estate labels read back with kubectl, and the Certificate Ready=True from the self-signed ClusterIssuer; the identical shape cold-deployed by stock on a second cluster as every later stage's oracle. Two applies, and the first is declared: $PRE_NOTE. Control, run first on this same cluster: the un-targeted one-pass plan exits $CTRL_RC before creating anything - \"$CTRL_LINE\" - so the pre-apply is load-bearing and not decoration. BREAK_PREAPPLY=1 requires that one-pass plan to succeed and correctly fails"
fi

# ── 2. migrate: live-import against stock's state ────────────────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
write_root "$ADOPTED" live || fail "could not write the adopted root"
( tofu_a init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(tofu_a live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
ELIGIBLE_LINE="$(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
log "  dry run: ${ELIGIBLE_LINE:-no eligibility line}"
APPROVE_OUT="$(tofu_a live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
if grep -qF "$TOTAL_N resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT"; then
  LABELLED="$(count_a)"
  if [ "$LABELLED" = "$TOTAL_N" ]; then
    gauntlet_stage migrate pass "$TOTAL_N of $TOTAL_N stamped, 0 skipped; every object carries tofu-estate=$ESTATE, counted back with kubectl across all 14 kinds including the three custom ones (ClusterIssuer, Issuer, Certificate) whose CRDs the pre-apply installed. The eligibility line was: ${ELIGIBLE_LINE:-none}"
  else
    gauntlet_stage migrate fail "live-import reported $TOTAL_N stamped but only $LABELLED object(s) carry tofu-estate=$ESTATE on the cluster"
  fi
else
  gauntlet_stage migrate fail "live-import -approve did not bind every instance: ${SUMMARY_LINE:-no summary line}"
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with kubectl ==="
PLAN_OUT="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
IDS_OK=1; IDS_MISSING=""
for spec in "namespace $NS" "customresourcedefinition clusterissuers.cert-manager.io" "customresourcedefinition certificates.cert-manager.io" "clusterissuer selfsigned" "issuer selfsigned" "certificate example-com" "deployment cert-manager-webhook" "validatingwebhookconfiguration cert-manager-webhook"; do
  read -r kind name <<< "$spec"
  case "$kind" in
    namespace|customresourcedefinition|clusterissuer|validatingwebhookconfiguration)
      kca get "$kind" "$name" >/dev/null 2>&1 || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $kind/$name"; } ;;
    *)
      exists_a "$kind" "$name" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $NS/$kind/$name"; } ;;
  esac
done
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all $TOTAL_N objects bind by namespace and name, and eight identities spanning every shape the root has - a cluster-scoped Namespace and two CRDs, a cluster-scoped custom kind (ClusterIssuer), two namespaced custom kinds (Issuer, Certificate), a Deployment and a ValidatingWebhookConfiguration - were confirmed present by value with kubectl"
else
  PLAN_LINE="$(grep -E '^Plan:|No changes' <<< "$PLAN_OUT" | head -1 | sed 's/\.$//')"
  gauntlet_stage test_plan fail "the plan with no state file is not empty (${PLAN_LINE:-no plan line}) or an identity is missing (identities confirmed: $IDS_OK;${IDS_MISSING:- none missing})"
  printf '%s\n' "$PLAN_OUT" | tail -20
fi

# ── 4. test_apply: no-op apply ───────────────────────────────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed) over a root of $TOTAL_N kubernetes_manifest instances; objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the estate's 14 kinds, counted with kubectl"

# ── 5. drift_reconverge: one custom resource tampered out of band ────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: the Certificate is deleted out of band on A and on B; stock's plan on B is the oracle ==="
# The out-of-band change is a `kubectl delete`, and the reason is the
# substrate's rather than a preference. Every object in this root is a
# `kubernetes_manifest`, which the provider applies SERVER-SIDE, so a
# `kubectl patch` makes kubectl a field manager for the field it touched
# and the reconverging apply then fails with a field-manager conflict -
# measured 2026-09-16, "You can override this conflict by setting
# force_conflicts to true", and it fails that way for STOCK too, since it
# is the provider doing it. A patch here would therefore measure
# server-side-apply ownership, not drift. Deleting the object involves no
# field manager at all: both sides see one object missing and both
# propose putting exactly it back. It is still kubectl, never the tool.
kca delete certificate example-com -n "$NS" >/dev/null || fail "could not delete the Certificate out of band on A"
kcb delete certificate example-com -n "$NS" >/dev/null || fail "could not delete the Certificate out of band on B"
if [ "${BREAK:-}" = "1" ]; then
  kca delete clusterissuer selfsigned >/dev/null || fail "BREAK: could not delete a second object on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the out-of-band delete exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one create"; }
grep -q "kubernetes_manifest.certificate_example_com" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name the Certificate"
O_RECONV="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_RECONV" | tail -20; fail "stock could not reconverge B"; }
DRIFT_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the out-of-band delete failed"; }
if [ "${BREAK:-}" = "1" ]; then
  if grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN"; then
    fail "BREAK=1: two objects were deleted but the plan still proposes exactly one create - the single-object assertion is not load-bearing"
  fi
  log "  BREAK=1: caught - with a second object deleted the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with a second custom resource deleted out of band the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one out-of-band delete does not propose exactly one create"; }
  grep -q "kubernetes_manifest.certificate_example_com" <<< "$DRIFT_PLAN" || fail "the plan does not name the Certificate"
  grep -qE 'clusterissuer_selfsigned|issuer_selfsigned' <<< "$DRIFT_PLAN" && fail "the plan touches an Issuer nobody deleted"
  RECONV="$(tofu_a apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$RECONV" || { printf '%s\n' "$RECONV" | tail -10; fail "the reconverging apply did not create exactly one object"; }
  CN="$(kca get certificate example-com -n "$NS" -o jsonpath='{.spec.commonName}')"
  [ "$CN" = "example.com" ] || fail "the Certificate's commonName reads ${CN:-nothing} after reconverging, want example.com"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after reconverging, want $TOTAL_N - the recreated object did not get its marker"
  gauntlet_stage drift_reconverge pass "a CUSTOM resource (the Certificate, kind cert-manager.io/v1) deleted out of band with kubectl; choudoufu proposed putting back exactly kubernetes_manifest.certificate_example_com (1 add, 0 change, 0 destroy), matching stock's own plan on the oracle cluster for the same deletion; apply created 1, spec.commonName reads back as configured, the recreated object carries the estate label again ($TOTAL_N labelled), and neither the ClusterIssuer nor the namespaced Issuer was touched. The change is a delete rather than a patch because every object here is a server-side-applied kubernetes_manifest: a kubectl patch makes kubectl a field manager and the reconverging apply then fails with a field-manager conflict on BOTH sides, which measures SSA ownership rather than drift. BREAK=1 deletes a second object and the single-object assertion correctly fails"
fi

# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a saved plan, an out-of-band label, a refusal; then the same file applies once the world is back ==="
# Part one: the metadata edit, which is a measurement rather than a step.
review_annotation "$ADOPTED" || fail "could not add the reviewed annotation to the adopted root"
review_annotation "$ORACLE"  || fail "could not add the reviewed annotation to the oracle root"
PO_PLAN="$(stock_b plan -input=false -no-color 2>&1)"
PO_LINE="$(grep -E '^Plan:|^No changes' <<< "$PO_PLAN" | head -1 | sed 's/\.$//')"
PA_PLAN="$(tofu_a plan -input=false -no-color 2>&1)"
PA_LINE="$(grep -E '^Plan:|^No changes' <<< "$PA_PLAN" | head -1 | sed 's/\.$//')"
log "  metadata edit: stock on B says '${PO_LINE:-none}', choudoufu on A says '${PA_LINE:-none}'"
META_GAP=""
if grep -q "^No changes" <<< "$PA_PLAN" && grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$PO_PLAN"; then
  META_GAP="Measured on the way in, and recorded rather than worked around: adding metadata.annotations.reviewed=yes to the cluster-scoped ClusterIssuer is ONE IN-PLACE UPDATE to stock on the oracle cluster (\"$PO_LINE\") and NOTHING AT ALL to choudoufu on the estate cluster (\"$PA_LINE\"). A change to metadata.labels or metadata.annotations of a kubernetes_manifest is invisible to the plan and an apply writes nothing; a change outside metadata (data, spec) is seen normally. The approval test below therefore uses a spec-side field, so that what it measures is plan approval. "
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not apply the annotation on B"
elif ! grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$PA_PLAN"; then
  META_GAP="The metadata edit behaved as neither side was expected to: stock said \"$PO_LINE\" and choudoufu said \"$PA_LINE\". "
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge A after the metadata edit"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge B after the metadata edit"
else
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge A after the metadata edit"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge B after the metadata edit"
fi

# Part two: the stage's own question, on a field the plan can see.
extra_dnsname "$ADOPTED" || fail "could not add the second dnsName to the adopted root"
extra_dnsname "$ORACLE"  || fail "could not add the second dnsName to the oracle root"
P_PLAN="$(tofu_a plan -out=approved.tfplan -input=false -no-color 2>&1)"; P_PLAN_RC=$?
P_PLAN_LINE="$(grep -E '^Plan:|^No changes' <<< "$P_PLAN" | head -1 | sed 's/\.$//')"
P_CHANGED="$(grep -E '^[[:space:]]*# .* will be' <<< "$P_PLAN" | sed -E 's/^[[:space:]#]*//' | tr '\n' ';')"
if [ "$P_PLAN_RC" -ne 0 ] || ! grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN"; then
  # Recorded and stepped over rather than aborting the run: the stages
  # below reach surfaces nothing else in the lane does.
  gauntlet_stage plan_approval fail "${META_GAP}adding a second dnsName to the Certificate did not plan as exactly one in-place update: ${P_PLAN_LINE:-the plan did not produce a summary line} (exit $P_PLAN_RC). What it proposed: ${P_CHANGED:-nothing named}"
  printf '%s\n' "$P_PLAN" | tail -30
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
  PLAN_APPROVAL_SKIPPED=1
fi
if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
# The world moves on a SPEC-side field, for the reason part one just
# measured: a stray LABEL is invisible to choudoufu on a
# kubernetes_manifest, so a label-shaped world-move made this stage
# re-measure that blindness (run of 2026-09-16: the saved plan applied
# cleanly at exit 0 where the refusal was expected, because the mover
# could not be seen). The cainjector Deployment declares "replicas" = 1
# in the bundle, and nothing below this stage applies that object, so
# moving it and putting it back leaves the estate exactly as it was.
kca patch deployment cert-manager-cainjector -n "$NS" --type merge -p '{"spec":{"replicas":2}}' >/dev/null || fail "could not move the world (the cainjector Deployment's replicas) on A"
P_APPLY="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  kca patch deployment cert-manager-cainjector -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
  ( tofu_a apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the change on B"
  gauntlet_stage plan_approval pass "${META_GAP}BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  if [ "$P_RC" -ne 3 ] || ! grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY"; then
    printf '%s\n' "$P_APPLY" | tail -20
    gauntlet_stage plan_approval fail "${META_GAP}the saved plan was applied after the world had moved out of band (the cainjector Deployment's replicas 1 -> 2, kubectl) and choudoufu exited $P_RC rather than refusing at 3 with \"The approved plan no longer matches the live system\". The saved plan's own change was one in-place update to the Certificate's spec.dnsNames, which stock plans identically"
    kca patch deployment cert-manager-cainjector -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null
    ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge A after plan_approval; nothing below would measure day-2 behaviour"
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge B after plan_approval; the oracle would be stale for every stage below"
    PLAN_APPROVAL_SKIPPED=1
  fi
  if [ -z "${PLAN_APPROVAL_SKIPPED:-}" ]; then
  DNS_NOW="$(kca get certificate example-com -n "$NS" -o jsonpath='{.spec.dnsNames}')"
  [ "$DNS_NOW" = '["example.com"]' ] || fail "the Certificate's dnsNames read $DNS_NOW despite the refusal, want only example.com"
  kca patch deployment cert-manager-cainjector -n "$NS" --type merge -p '{"spec":{"replicas":1}}' >/dev/null || fail "could not put the world back"
  P_APPLY2="$(tofu_a apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(kca get certificate example-com -n "$NS" -o jsonpath='{.spec.dnsNames}')" = '["example.com","www.example.com"]' ] || fail "the Certificate does not carry both dnsNames after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "${META_GAP}plan -out wrote one update to a namespaced custom kind (the Certificate gains a second dnsName); the world then moved out of band (the cainjector Deployment's replicas moved to 2 with kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied (kubectl still reads one dnsName); with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and both dnsNames read back; stock's own planfile applied on the oracle cluster. BREAK_APPROVAL=1 expects success after the move and correctly fails"
  fi
fi

fi

# ── 7. day2_rename: a moved block, zero churn ────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: kubernetes_manifest.clusterissuer_selfsigned becomes .clusterissuer_review through a moved block ==="
rename_clusterissuer "$ADOPTED" || fail "could not rename the ClusterIssuer block in the adopted root"
rename_clusterissuer "$ORACLE"  || fail "could not rename the ClusterIssuer block in the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
R_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
if grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$R_PLAN"; then
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the moved-block apply failed"
  kca get clusterissuer selfsigned >/dev/null 2>&1 || fail "the ClusterIssuer is gone after the rename"
  [ "$(count_a)" = "$TOTAL_N" ] || fail "$(count_a) labelled objects after the rename, want $TOTAL_N"
  gauntlet_stage day2_rename pass "moved block over a cluster-scoped custom kind: kubernetes_manifest.clusterissuer_selfsigned -> .clusterissuer_review with zero churn (no add, no change, no destroy), the live ClusterIssuer untouched and still labelled, read with kubectl; stock's plan for the same moved block on the oracle cluster is also zero churn. The moved-block half only: live-mv has no Kubernetes leg, because the object carries no address to rewrite (#1066)"
else
  gauntlet_stage day2_rename fail "the moved-block plan over a custom kind is not zero churn: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  printf '%s\n' "$R_PLAN" | tail -20
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not converge after the rename; nothing below would measure day-2 behaviour"
fi

# ── 8. day2_remove: two blocks leave the configuration ───────────────────
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the Issuer block leaves, then the Certificate whose controller created a Secret ==="
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed - the destroy below would not be the block removal's doing"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the Issuer block kept, no destroy is proposed; the real check is skipped"
  remove_issuer "$ADOPTED" && remove_issuer "$ORACLE" || fail "BREAK_REMOVE: could not remove the blocks afterwards"
  ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  remove_issuer "$ADOPTED" || fail "could not remove the Issuer block from the adopted root"
  remove_issuer "$ORACLE"  || fail "could not remove the Issuer block from the oracle root"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  D_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan is not exactly one destroy"; }
  D_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | head -1)"
  D_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$D_LINE")"
  [ "$D_ADDR" = "kubernetes_manifest.orphan_issuer_${NS}_selfsigned" ] || { printf '%s\n' "$D_PLAN" | grep -E 'destroyed|^Plan:'; fail "the one destroy is ${D_ADDR:-unnamed}, not the orphan address kubernetes_manifest.orphan_issuer_${NS}_selfsigned the sweep plans a label-found custom-kind object at"; }
  grep -q "Owned and undeclared: 1 live resource will be destroyed" <<< "$D_PLAN" || fail "the plan does not say the destroy is an owned, undeclared object"
  ( tofu_a apply -auto-approve -input=false -no-color 2>&1 | grep -qF "0 added, 0 changed, 1 destroyed" ) || fail "the remove apply did not destroy exactly one object"
  exists_a issuer selfsigned && fail "the Issuer still exists after the remove apply"

  # The controller-copy half. The Certificate's Secret is created by
  # cert-manager, not by the root: it carries controller.cert-manager.io/fao
  # and no estate label, so the sweep must not see it.
  kca get secret example-com-tls -n "$NS" >/dev/null 2>&1 || fail "the controller-created Secret example-com-tls does not exist before the Certificate is removed; the controller-copy check would measure nothing"
  remove_certificate "$ADOPTED" || fail "could not remove the Certificate block from the adopted root"
  remove_certificate "$ORACLE"  || fail "could not remove the Certificate block from the oracle root"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's certificate-removal apply failed on B"
  C_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the certificate-removal plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "removing the Certificate proposes more than one destroy - the controller-created Secret may have been swept in"; }
  grep -q "example-com-tls" <<< "$(grep -E 'will be destroyed' <<< "$C_PLAN")" && fail "the plan proposes destroying the controller-created Secret example-com-tls, which the root never created"
  ( tofu_a apply -auto-approve -input=false -no-color 2>&1 | grep -qF "0 added, 0 changed, 1 destroyed" ) || fail "the certificate-removal apply did not destroy exactly one object"
  exists_a certificate example-com && fail "the Certificate still exists after the remove apply"
  SECRET_LABELS="$(kca get secret example-com-tls -n "$NS" -o jsonpath='{.metadata.labels}' 2>/dev/null)"
  [ -n "$SECRET_LABELS" ] || fail "the controller-created Secret example-com-tls is gone after the Certificate was removed; something destroyed an object the root never created"
  D_REPLAN="$(tofu_a plan -input=false -no-color 2>&1)" || fail "the replan after the removes failed"
  grep -q "No changes." <<< "$D_REPLAN" || { printf '%s\n' "$D_REPLAN" | tail -20; fail "the replan after the removes is not empty"; }
  REMAIN=$((TOTAL_N - 2))
  [ "$(count_a)" = "$REMAIN" ] || fail "$(count_a) labelled objects after the removes, want $REMAIN"
  gauntlet_stage day2_remove pass "two blocks removed, one destroy each. Deleting the namespaced custom kind (Issuer) proposed exactly one destroy at the sweep's synthetic orphan address $D_ADDR (\"Owned and undeclared: 1 live resource will be destroyed\") - the object is found by its label, which carries no address. Deleting the Certificate then proposed exactly ONE destroy too, and crucially NOT the Secret its controller created: example-com-tls carries controller.cert-manager.io/fao and no tofu-estate label, and it is still on the cluster afterwards ($SECRET_LABELS), which is the controller-copy exclusion this estate exists to check. Stock's plans for both removals on the oracle cluster are also exactly one destroy each; the next plan is empty and $REMAIN objects remain labelled"
fi

# ── 9. day2_count: a counted custom resource ─────────────────────────────
#
# The counted block is added HERE and removed again at the end of the
# stage, rather than living in the committed root, because choudoufu cannot
# re-plan it: measured 2026-09-16, every plan after the instances exist
# proposes CREATING them again and the API server rejects the dry run with
# "already exists", while the sweep simultaneously reports the same objects
# as undeclared orphans. A root carrying that block fails test_plan and
# every stage after it, so keeping it in the root would trade eleven
# measurements for one. The stage below makes that one measurement on
# purpose and then cleans up.
gauntlet_begin_stage day2_count
log "=== 9. day2_count: kubernetes_manifest.issuer_shard, a counted custom kind, 2 -> 1 -> 2 ==="
COUNT_VERDICT=""
append_shards "$ADOPTED" 2 || fail "could not add the counted Issuer to the adopted root"
append_shards "$ORACLE" 2  || fail "could not add the counted Issuer to the oracle root"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan for the counted Issuer failed on B"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan for the counted Issuer on B is not exactly two adds"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not create the counted Issuers on B"
# Stock's own oracle for the scale-down, taken before choudoufu's side is
# measured, so the comparison exists whichever way choudoufu goes.
remove_shards "$ORACLE" && append_shards "$ORACLE" 1 || fail "could not scale the oracle root to 1"
O_DOWN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_DOWN" || { printf '%s\n' "$O_DOWN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
grep -q 'kubernetes_manifest.issuer_shard\[1\]' <<< "$O_DOWN" || fail "stock's scale-down on B does not destroy issuer_shard[1]"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"

A_UP="$(tofu_a plan -input=false -no-color 2>&1)"; A_UP_RC=$?
if [ "$A_UP_RC" -ne 0 ] || ! grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$A_UP"; then
  COUNT_VERDICT="choudoufu could not even plan the counted Issuer's creation: $(grep -E '^Plan:|^Error' <<< "$A_UP" | head -1)"
else
  ( tofu_a apply -auto-approve -input=false -no-color 2>&1 | grep -qF "2 added, 0 changed, 0 destroyed" ) || fail "the counted Issuer's creating apply did not add exactly two objects"
  exists_a issuer shard-0 && exists_a issuer shard-1 || fail "both counted Issuers do not exist after choudoufu created them"
  # The measurement. This is choudoufu replanning a root IT JUST APPLIED,
  # with nothing changed - so a create proposed here is not an adoption
  # question, it is the counted instance never binding to its own object.
  A_RE="$(tofu_a plan -input=false -no-color 2>&1)"; A_RE_RC=$?
  if [ "$A_RE_RC" -ne 0 ] || ! grep -q "No changes." <<< "$A_RE"; then
    REJECT="$(grep -m1 'refused the create' <<< "$A_RE" | sed 's/^ *//')"
    ORPHANED="$(grep -c 'orphan_issuer_'"$NS"'_shard-' <<< "$A_RE")"
    COUNT_VERDICT="replanning the unchanged root choudoufu itself had just applied is not empty. The two counted instances never bind to the objects they created: the plan proposes CREATING them again and the server-side dry run rejects it - \"${REJECT:-no rejection line}\" - while the estate sweep reports the same live objects as undeclared orphans at kubernetes_manifest.orphan_issuer_${NS}_shard-N ($ORPHANED line(s) naming one). Neither half is adoption: choudoufu applied these objects itself one command earlier. Every other kubernetes_manifest instance in this root - all $TOTAL_N of them, including the un-counted ClusterIssuer, Issuer and Certificate - re-plans empty, so it is count on kubernetes_manifest specifically. Stock replans the identical root clean, and its own 2 -> 1 scale-down on the oracle cluster destroys exactly kubernetes_manifest.issuer_shard[1]"
  fi
fi

if [ -n "$COUNT_VERDICT" ]; then
  gauntlet_stage day2_count fail "$COUNT_VERDICT"
  # Clean up so the stages below measure the estate and not the wreckage.
  remove_shards "$ADOPTED" || fail "could not remove the counted Issuer from the adopted root"
  remove_shards "$ORACLE"  || fail "could not remove the counted Issuer from the oracle root"
  kca delete issuer shard-0 shard-1 -n "$NS" --ignore-not-found >/dev/null 2>&1
  kcb delete issuer shard-0 shard-1 -n "$NS" --ignore-not-found >/dev/null 2>&1
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the oracle root would not converge after the counted Issuer was withdrawn"
  CLEAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$CLEAN" | tail -20; fail "the plan after withdrawing the counted Issuer failed; nothing below would measure day-2 behaviour"; }
  grep -q "No changes." <<< "$CLEAN" || { printf '%s\n' "$CLEAN" | tail -20; fail "the plan after withdrawing the counted Issuer is not empty; nothing below would measure day-2 behaviour"; }
else
  remove_shards "$ADOPTED" && append_shards "$ADOPTED" 1 || fail "could not scale the adopted root to 1"
  C_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
  C_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$C_PLAN" | head -1)"
  C_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$C_LINE")"
  ( tofu_a apply -auto-approve -input=false -no-color 2>&1 | grep -qF "0 added, 0 changed, 1 destroyed" ) || fail "the scale-down apply did not destroy exactly one object"
  if [ "${BREAK_COUNT:-}" = "1" ]; then
    exists_a issuer shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
    log "  BREAK_COUNT=1: caught - shard-0 still exists, so asserting it was the one destroyed correctly fails"
    gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (shard-0) was destroyed correctly fails to hold; the real check is skipped"
  else
    exists_a issuer shard-0 || fail "shard-0 was destroyed on the scale-down"
    exists_a issuer shard-1 && fail "shard-1 still exists after the scale-down"
    remove_shards "$ADOPTED" && append_shards "$ADOPTED" 2 || fail "could not scale the adopted root back to 2"
    remove_shards "$ORACLE" && append_shards "$ORACLE" 2  || fail "could not scale the oracle root back to 2"
    O_UP="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_UP" || { printf '%s\n' "$O_UP" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
    U_PLAN="$(tofu_a plan -input=false -no-color 2>&1)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
    ( tofu_a apply -auto-approve -input=false -no-color 2>&1 | grep -qF "1 added, 0 changed, 0 destroyed" ) || fail "the scale-up apply did not create exactly one object"
    gauntlet_stage day2_count pass "scaling a COUNTED CUSTOM KIND (kubernetes_manifest.issuer_shard, a cert-manager.io/v1 Issuer whose object name is shard-\${count.index} inside the manifest object) from 2 to 1 destroyed exactly shard-1, planned at $C_ADDR (shard-0 untouched, both read with kubectl); back to 2 created exactly one object under the same name; stock's plans for the same two changes on the oracle cluster have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
    remove_shards "$ADOPTED" && remove_shards "$ORACLE" || fail "could not withdraw the counted Issuer"
    ( tofu_a apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not withdraw the counted Issuers from A"
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not withdraw the counted Issuers from B"
  fi
fi

# ── 10. day2_teardown: destroy the adopted estate ────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 10. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$(count_a)"
T_OUT="$(tofu_a apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
grep -qF "Resources: 0 added, 0 changed, $T_EXPECT destroyed" <<< "$T_OUT" || { printf '%s\n' "$T_OUT" | tail -5; fail "apply -destroy did not remove exactly the $T_EXPECT remaining objects"; }
for _ in $(seq 1 60); do kca get namespace "$NS" >/dev/null 2>&1 || break; sleep 2; done
kca get namespace "$NS" >/dev/null 2>&1 && fail "the $NS namespace still exists after the destroy"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
CRDS_LEFT="$(kca get crd -o name 2>/dev/null | grep -c 'cert-manager.io')"
[ "$CRDS_LEFT" = "0" ] || fail "$CRDS_LEFT cert-manager CRD(s) survive the destroy"
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
gauntlet_stage day2_teardown pass "apply -destroy removed exactly the $T_EXPECT remaining objects in one apply - no second pass needed on the way down, although the way up took two - the namespace is gone, all six cert-manager CRDs are gone, and no object of any of the estate's 14 kinds carries tofu-estate=$ESTATE (kubectl, every namespace); stock's destroy of the same estate on the oracle cluster also removed exactly the $O_EXPECT its state held"

# ── 11. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 11. greenfield: choudoufu applies the shape fresh on the now-empty cluster A ==="
write_root "$GREEN" live || fail "could not write the greenfield root"
( cd "$GREEN" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
green() { ( cd "$GREEN" && KUBECONFIG="$KCA" KUBE_CONFIG_PATH="$KCA" "$TOFU" "$@" ); }
# The cluster is empty again, so the CRD constraint is back and greenfield
# needs the same two applies the cold deploy did. It uses the SAME declared
# list, read from the manifest - never a second list maintained here.
G_TARGETS=()
while IFS= read -r a; do [ -n "$a" ] && G_TARGETS+=("-target=$a"); done < <(gauntlet_pre_apply_targets "$ESTATE")
[ "${#G_TARGETS[@]}" = "$BUNDLE_N" ] || fail "the declared pre-apply list has ${#G_TARGETS[@]} addresses, want $BUNDLE_N"
G_PRE="$(green apply -auto-approve -input=false -no-color "${G_TARGETS[@]}" 2>&1)"; G_PRE_RC=$?
[ "$G_PRE_RC" -eq 0 ] || { printf '%s\n' "$G_PRE" | tail -30; fail "the greenfield pre-apply failed (exit $G_PRE_RC): $(grep -m1 -E '^Error|^\s*Error' <<< "$G_PRE" | sed 's/^ *//')"; }
cert_manager_ready "$KCA" "cluster A (greenfield)" || fail "cert-manager never became ready on A after the greenfield pre-apply"
G_OUT="$(green apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
grep -qF "Apply complete! Resources: $CUSTOM_N added, 0 changed, 0 destroyed" <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -5; fail "the greenfield main apply did not add exactly the $CUSTOM_N custom resources"; }
[ ! -f "$GREEN/terraform.tfstate" ] || fail "a terraform.tfstate appeared after a live-block apply"
G_LABELLED="$(count_a)"
G_RECORDS="$(gauntlet_record_count "$GREEN/.tofu-records")"
G_PLAN="$(green plan -input=false -no-color 2>&1)" || fail "the greenfield replan failed"
grep -q "No changes." <<< "$G_PLAN" || { printf '%s\n' "$G_PLAN" | tail -20; fail "the greenfield replan is not empty"; }
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
G_PLAN2="$(green plan -input=false -no-color 2>&1)" || fail "the greenfield replan without the cache failed"
grep -q "No changes." <<< "$G_PLAN2" || { printf '%s\n' "$G_PLAN2" | tail -20; fail "the greenfield replan without the cache is not empty"; }
G_WANT="$TOTAL_N"; [ "${BREAK:-}" = "1" ] && G_WANT=$((TOTAL_N + 1))
if [ "${BREAK:-}" = "1" ]; then
  if [ "$G_LABELLED" = "$G_WANT" ]; then
    fail "BREAK=1: the greenfield object count matched a deliberately wrong expectation ($G_WANT) - the count assertion is not load-bearing"
  fi
  log "  BREAK=1: caught - $G_LABELLED labelled objects is not the wrong expectation $G_WANT"
  gauntlet_stage greenfield pass "BREAK=1 control: expecting $G_WANT labelled objects where the estate has $G_LABELLED makes the count assertion correctly fail; the estate applied ($CUSTOM_N added after the pre-apply, no terraform.tfstate) and replanned empty with and without the cache"
else
  [ "$G_LABELLED" = "$TOTAL_N" ] || fail "$G_LABELLED object(s) carry tofu-estate=$ESTATE after the greenfield apply, want $TOTAL_N"
  G_CERT="$(kca get certificate example-com -n "$NS" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)"
  [ "$G_CERT" = "True" ] || fail "the greenfield Certificate is not Ready (status: ${G_CERT:-none}); stock's cold deploy produced a Ready one, so this is not the same estate"
  gauntlet_stage greenfield pass "$TOTAL_N objects applied fresh with a live block and no terraform.tfstate, every one labelled tofu-estate=$ESTATE (kubectl, 14 kinds), and the Certificate Ready=True from the self-signed ClusterIssuer exactly as stock's cold deploy left it; the record store held $G_RECORDS file(s); replanned empty with and without the cache. Greenfield needs the SAME declared pre-apply the cold deploy did, read from live/gauntlet/estates.json rather than repeated here - the cluster is empty again, so the CRD plan-time constraint is back and it is a property of the configuration, not of who is applying it. BREAK=1 expects a deliberately wrong object count and the assertion correctly fails"
fi
( green apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  note: greenfield teardown did not exit clean; the cluster is deleted below regardless"

# ── 12. strict: every toggle on, one refusal ─────────────────────────────
gauntlet_begin_stage strict
STRICT="$WORK/strict"
mkdir -p "$STRICT"
strict_block() { # $1 = the secrets setting under test
  cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "reference-k8s-cert-manager-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["kubernetes_manifest"]
      }
    }
  }
}

resource "random_password" "db" {
  length = 16
}
EOF
}
log "=== 12. strict: every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
( cd "$STRICT" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block "store" > "$STRICT/main.tf"
  STRICT_OFF="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -20; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC - a refusal appeared where none should"; }
  grep -q "^Error:" <<< "$STRICT_OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  grep -qF 'random_password.db will be created' <<< "$STRICT_OFF" || fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create; the real check is skipped"
else
  [ "$STRICT_ON_RC" -eq 1 ] || { printf '%s\n' "$STRICT_ON" | tail -20; fail "the every-toggle-on plan exited $STRICT_ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$STRICT_ON")" -eq 1 ] || { printf '%s\n' "$STRICT_ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_manifest) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
log "reference-k8s-cert-manager: done"
