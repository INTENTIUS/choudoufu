# k8s-stock-when-you-need-it
# CLAIM 8 (kubernetes) - Stock when you need it. ~3 min.
#
# This proof: with no live block, choudoufu plans a state-backed estate on a
# real cluster with stock OpenTofu's answer and stock OpenTofu's exact
# request count, measured on the wire, not promised.
#
# DRAFT: written and never run (live/smoke/drafts/README.md). Claim 8's
# Kubernetes cell stays open until both arms have run green.
#
# The AWS proof has a second half: with the live block on, foreign
# resources in the account do not move what a plan costs. On a cluster that
# is claim 14's whole Kubernetes proof (k8s-plan-cost-tracks-the-estate), so
# it is not repeated here.
#
# How it counts. Both plans run through live/smoke/k8sproxy.py with no fault
# armed; the proxy logs one line per request the client sent, so the count
# is this client's alone and not the cluster's controllers'. The oracle is
# stock OpenTofu at live/oracle-versions.json's tofu_version, the release
# this fork is built on, and the scenario refuses any other version: `tofu`
# on PATH, or ORACLE_TOFU naming a binary. Both resolve the same pinned
# hashicorp/kubernetes release from registry.opentofu.org, so the oracle
# plans from a byte copy of the state file.
#
# It was stock Terraform until its first run (2026-10-04). Terraform 1.15
# prints a "Deprecated value used" warning OpenTofu has no such diagnostic
# for, so the plan texts differed by a property of Terraform rather than
# of this fork, and the claim is that the fork is the OpenTofu it forks.
#
# BREAK=1 runs choudoufu's leg with the live block on. The machinery it asks
# for (API discovery, one label-selected list per kind) must show in the
# measurement, so the equality the main arm asserts is shown able to fail.

ORACLE_TOFU="${ORACLE_TOFU:-tofu}"
command -v "$ORACLE_TOFU" >/dev/null 2>&1 \
  || fail "k8s-parity" "no $ORACLE_TOFU on PATH - this scenario's oracle is stock OpenTofu (set ORACLE_TOFU to a binary)"
WANT_TOFU="$(python3 -c "import json;print(json.load(open('$ROOT/live/oracle-versions.json'))['tofu_version'])")"
GOT_TOFU="$("$ORACLE_TOFU" version 2>/dev/null | sed -n '1s/^OpenTofu v//p')"
[ "$GOT_TOFU" = "$WANT_TOFU" ] \
  || fail "k8s-parity" "the oracle is OpenTofu '$GOT_TOFU' and live/oracle-versions.json pins $WANT_TOFU; an unpinned oracle changes what stock means (set ORACLE_TOFU)"
W="$SMOKE_WORKROOT/k8s-parity"
CHDF_DIR="$W/choudoufu"; ORACLE_DIR="$W/oracle"
mkdir -p "$CHDF_DIR" "$ORACLE_DIR"
SMOKE_WORK="$W"; export SMOKE_WORK
ESTATE="smoke-k8s-parity"
for d in "$CHDF_DIR" "$ORACLE_DIR"; do cp "$ROOT/live/e2e/estate-k8s/main.tf" "$d/main.tf"; done
python3 - "$ROOT/live/e2e/estate-k8s/versions.tf" "$CHDF_DIR" "$ORACLE_DIR" "$ESTATE" <<'PYEOF'
import re, sys
src, chdf_dir, oracle_dir, estate = sys.argv[1:5]
text = open(src).read()
live = text.replace('estate = "smoke-k8s"', 'estate = "%s"' % estate)
assert 'estate = "%s"' % estate in live
stock = re.sub(r'\n  live \{\n    estate = "[^"]+"\n  \}\n', '\n', live)
assert 'live {' not in stock
open(chdf_dir + '/versions-live.tf.keep', 'w').write(live)
open(chdf_dir + '/versions-stock.tf.keep', 'w').write(stock)
open(chdf_dir + '/versions.tf', 'w').write(stock)
open(oracle_dir + '/versions.tf', 'w').write(stock)
PYEOF
use_versions() { cp "$CHDF_DIR/versions-$1.tf.keep" "$CHDF_DIR/versions.tf"; }
# plan_body is a plan's answer without its progress lines and with the
# product name taken out, since the two binaries name themselves.
plan_body() {
  grep -vE '^$|Refreshing state|Reading\.\.\.|Read complete|^Note:|^─|-out option|guarantee to take exactly|compared your real infrastructure|no changes are needed' "$1" \
    | sed -e 's/OpenTofu/<tool>/g' -e 's/Terraform/<tool>/g'
}

cluster_up
k8s_counting_proxy_up "$W/proxy" k8s-parity
trap 'kill "$PROXY_PID" 2>/dev/null || true; cleanup' EXIT
PLOG="$W/proxy/proxy.log"

# through_proxy <dir> <label> <binary...> runs one plan in <dir> through the
# proxy and prints the number of requests it sent; the plan's text is
# $W/<label>.plan and the request lines $W/<label>.requests.
through_proxy() {
  local dir="$1" label="$2"; shift 2
  : > "$PLOG"
  ( cd "$dir" && KUBECONFIG="$PROXY_KC" KUBE_CONFIG_PATH="$PROXY_KC" "$@" plan -input=false -no-color > "$W/$label.plan" 2>&1 ) || true
  cp "$PLOG" "$W/$label.requests"
  grep -c . "$PLOG" || true
}

step "the claim"
explain \
  "Stock behaviour is the fallback, whole and exact, one deleted live" \
  "block away. Measured, not promised: a plan over the same estate and" \
  "state file sends exactly the requests stock OpenTofu sends, and" \
  "prints the same answer."

step "1. a stock estate, stood up by choudoufu with no live block"
cmd "choudoufu init && choudoufu apply -auto-approve   # no live block anywhere"
logged k8s-parity-init "k8s-parity" "choudoufu init failed" -- in_dir "$CHDF_DIR" chdf init -input=false -no-color
A1="$(cd "$CHDF_DIR" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-parity" "stock-mode apply failed: $A1"
grep -qE 'Apply complete! Resources: 4 added' <<< "$A1" || fail "k8s-parity" "the apply did not add the 4 objects: $A1"
[ -f "$CHDF_DIR/terraform.tfstate" ] || fail "k8s-parity" "no terraform.tfstate: with no live block the fork must write one, as stock does"
LBL="$(kc get configmap app-config -n smoke-k8s -o jsonpath='{.metadata.labels.tofu-estate}' 2>/dev/null || true)"
[ -z "$LBL" ] || fail "k8s-parity" "the ConfigMap carries tofu-estate=$LBL with no live block in the configuration"
cp "$CHDF_DIR/terraform.tfstate" "$ORACLE_DIR/terraform.tfstate"
logged k8s-parity-oracle-init "k8s-parity" "the oracle's init failed" -- in_dir "$ORACLE_DIR" "$ORACLE_TOFU" init -input=false -no-color
proof "a plain state-backed estate, no label on anything, and the oracle holding the same state."

step "2. same plan, same requests"
explain \
  "Both tools plan the same estate from the same state, one after the" \
  "other, through the counting proxy. Each plans once first, so neither" \
  "pays a first-run cost inside the comparison. The plan text must match" \
  "and so must the request count, exactly."
cmd "choudoufu plan ; tofu plan   # both through the proxy"
through_proxy "$CHDF_DIR" chdf-warm "$TOFU" >/dev/null
through_proxy "$ORACLE_DIR" oracle-warm "$ORACLE_TOFU" >/dev/null
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: the choudoufu leg runs WITH the live block. Asked-for" \
    "machinery must show in the measurement; if the text and the count" \
    "still match stock's, the comparison compares nothing."
  use_versions live
  logged k8s-parity-break-init "k8s-parity" "BREAK: re-init with the live block failed" -- in_dir "$CHDF_DIR" chdf init -input=false -no-color
fi
REQ_CHDF="$(through_proxy "$CHDF_DIR" chdf "$TOFU")"
REQ_ORACLE="$(through_proxy "$ORACLE_DIR" oracle "$ORACLE_TOFU")"
[ "$REQ_ORACLE" -gt 0 ] || fail "k8s-parity" "the oracle's plan sent no request through the proxy, so the counter is not on its path: $(cat "$W/oracle.plan")"
grep -q "No changes." "$W/oracle.plan" || fail "k8s-parity" "the oracle does not plan the estate clean: $(cat "$W/oracle.plan")"
if [ "${BREAK:-0}" = "1" ]; then
  echo "choudoufu with the live block: $REQ_CHDF requests; stock: $REQ_ORACLE" | evidence
  if [ "$REQ_CHDF" = "$REQ_ORACLE" ] && diff -q <(plan_body "$W/chdf.plan") <(plan_body "$W/oracle.plan") >/dev/null 2>&1; then
    fail "k8s-parity" "BREAK: a live-block plan measured identical to stock's, so the comparison compares nothing"
  fi
  proof "caught: asking for the live backend shows in the measurement, so the equality the main arm asserts is a comparison that can fail."
  use_versions stock
  ( cd "$CHDF_DIR" && chdf init -input=false -no-color >/dev/null 2>&1 && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi
grep -q "No changes." "$W/chdf.plan" || fail "k8s-parity" "the choudoufu leg is not converged: $(cat "$W/chdf.plan")"
diff <(plan_body "$W/chdf.plan") <(plan_body "$W/oracle.plan") > "$W/plan-diff.txt" \
  || fail "k8s-parity" "the plan texts differ: $(cat "$W/plan-diff.txt")"
echo "choudoufu: $REQ_CHDF requests; stock OpenTofu: $REQ_ORACLE; plan texts equal once each tool's name is taken out" | evidence
[ "$REQ_CHDF" = "$REQ_ORACLE" ] \
  || fail "k8s-parity" "request counts differ: choudoufu $REQ_CHDF, stock $REQ_ORACLE; the difference: $(diff <(sed 's/ [0-9]*$//' "$W/chdf.requests" | sort) <(sed 's/ [0-9]*$//' "$W/oracle.requests" | sort) | head -20)"
proof "the same answer and the same $REQ_CHDF requests on the wire as stock OpenTofu. With no live block, none of the fork's machinery runs."

step "3. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$CHDF_DIR" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "k8s-parity" "teardown failed: $DOUT"
destroyed_exactly "k8s-parity" 4 "$DOUT"
proof "4 destroyed."

echo "  What you watched: the fork plan a state-backed estate on a real"
echo "  cluster with stock OpenTofu's answer and stock OpenTofu's request"
echo "  count, measured on the wire by the same proxy."
