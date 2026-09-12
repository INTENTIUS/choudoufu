# k8s-greenfield
# A Kubernetes estate from nothing, on a real kind cluster: apply, replan empty, cache disposable, out-of-band delete caught, destroy exact. No emulator. ~2 min.
#
# The first Kubernetes scenario (#1057, the harness unit #1016's ruling
# put first). A demo, not a claim, until the label carrier lands: today a
# kubernetes_* resource plans from its own name and namespace and carries
# no marker, so there is no label to read back or strip. Its BREAK=1
# deletes the ConfigMap out of band instead and requires the replan to
# propose the create, which proves the empty-replan assertions are real.

SMOKE_WORK="$SMOKE_WORKROOT/k8s-greenfield"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-k8s/." "$SMOKE_WORK/"

cluster_up

kc() { kubectl --kubeconfig "$KUBECONFIG" "$@"; }

step "1. a Kubernetes estate, one plain apply"
explain \
  "The configuration has a live block, a Kubernetes provider, and nothing" \
  "else - no AWS provider, no backend, nothing pre-created. The cluster is" \
  "a real API server (kind), not an emulator. Stock OpenTofu would write" \
  "an authoritative terraform.tfstate here; choudoufu keeps only a" \
  "disposable cache."
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "k8s-greenfield" "init failed"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-greenfield" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || fail "k8s-greenfield" "apply did not report 2 added: $APPLY_OUT"
[ ! -f "$SMOKE_WORK/terraform.tfstate" ] \
  || fail "k8s-greenfield" "a terraform.tfstate appeared - a live-block apply must never write an authoritative state file"
proof "a namespace and a ConfigMap created, and no terraform.tfstate exists."

step "2. the object, read back with kubectl - no choudoufu in the loop"
explain \
  "The ConfigMap is a real object in a real cluster. This asks the API" \
  "server for it directly. Note what is NOT there yet: no tofu-estate" \
  "label. Kubernetes resources plan from their own name and namespace" \
  "today (#326) and carry no marker; the carrier is the next unit (#1016)."
cmd "kubectl get configmap app-config -n smoke-k8s -o jsonpath='{.data}{\" labels=\"}{.metadata.labels}'"
CM="$(kc get configmap app-config -n smoke-k8s -o jsonpath='{.data}{" labels="}{.metadata.labels}' 2>&1)" \
  || fail "k8s-greenfield" "kubectl could not read the ConfigMap: $CM"
echo "$CM" | evidence
grep -q '"greeting":"hello"' <<< "$CM" || fail "k8s-greenfield" "the ConfigMap does not carry the declared data: $CM"
if grep -q 'tofu-estate' <<< "$CM"; then
  fail "k8s-greenfield" "the ConfigMap carries a tofu-estate label - the carrier landed; promote this scenario to a claim whose BREAK strips the label (#1057)"
fi
proof "the object is there, with the declared data and no marker. When a label appears here, this demo becomes a claim."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - delete the ConfigMap out of band; the replan must catch it"
  explain \
    "You asked for proof the assertions can fail. This deletes the" \
    "ConfigMap with kubectl, behind choudoufu's back. If the next plan is" \
    "still empty, the empty-replan claims below are scenery and the run" \
    "fails itself."
  cmd "kubectl delete configmap app-config -n smoke-k8s"
  kc delete configmap app-config -n smoke-k8s >/dev/null || fail "k8s-greenfield" "BREAK: could not delete the ConfigMap"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "k8s-greenfield" "BREAK: the plan is still empty after the ConfigMap was deleted - the empty-replan assertion below is scenery"
  fi
  grep -E '^Plan:|will be created|Error:' <<< "$BOUT" | head -2 | evidence
  proof "caught. The deleted object changed the plan, so every empty-plan claim in this scenario is a real check."
  exit 0
fi

step "3. the replan - prior state rebuilt from the cluster"
explain \
  "With no state file, the next plan asks the cluster what exists. Each" \
  "resource is found by the name and namespace the configuration" \
  "declares. If binding is right, the plan is empty."
cmd "choudoufu plan"
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-greenfield" "plan failed: $PLAN_OUT"
grep -E 'No changes\.' <<< "$PLAN_OUT" | head -1 | evidence
grep -q "No changes." <<< "$PLAN_OUT" || fail "k8s-greenfield" "replan is not empty: $PLAN_OUT"
proof "an empty plan, with nothing stored anywhere that could have remembered the estate."

step "4. the state cache - present, disposable, and never trusted"
explain \
  "The apply wrote a cache under .terraform/. Same three rules as on AWS:" \
  "never consulted for ownership, live wins, losing it costs a read." \
  "Delete it, replan, require the identical answer."
cmd "rm .terraform/choudoufu-cache.tfstate && choudoufu plan"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
[ -f "$CACHE" ] || fail "k8s-greenfield" "no cache at $CACHE after a plain apply"
rm -f "$CACHE"
PLAN2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-greenfield" "plan without the cache failed"
grep -E 'No changes\.' <<< "$PLAN2" | head -1 | evidence
grep -q "No changes." <<< "$PLAN2" || fail "k8s-greenfield" "deleting the cache changed the plan: $PLAN2"
proof "the cache was there and its loss changed nothing."

step "5. destroy - exactly what was made"
explain \
  "Teardown must remove exactly the two objects this scenario created" \
  "and leave the cluster's own namespaces alone."
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-greenfield" "apply -destroy failed: $DESTROY_OUT"
grep -E 'Destroy complete|Apply complete' <<< "$DESTROY_OUT" | head -1 | evidence
grep -qE "Resources: 0 added, 0 changed, 2 destroyed" <<< "$DESTROY_OUT" \
  || fail "k8s-greenfield" "destroy did not remove exactly the 2 created resources: $DESTROY_OUT"
if kc get namespace smoke-k8s >/dev/null 2>&1; then
  fail "k8s-greenfield" "the smoke-k8s namespace still exists after destroy"
fi
kc get namespace kube-system >/dev/null 2>&1 || fail "k8s-greenfield" "kube-system is gone; destroy reached past the estate"
proof "2 destroyed, 0 added, 0 changed. The estate is gone and the cluster's own namespaces stand."

echo "  What you watched: a Kubernetes estate live its whole life on a real"
echo "  cluster without an authoritative state file. What you did not watch:"
echo "  a marker. That is the next unit."
