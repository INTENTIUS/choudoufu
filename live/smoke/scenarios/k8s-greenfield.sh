# k8s-greenfield
# CLAIM 21 - The marker is a label on a real cluster: one tofu-estate label rides the create, any kubectl reads it back, a stripped label is repaired by the next plan, and the estate lives its whole life without a state file. ~2 min.
#
# The first Kubernetes claim (#1061, under #1016's ruling of an estate-only
# label; #1057's harness made it a demo first). The marker is ONE label,
# tofu-estate, in metadata.labels; the address stays off the object because
# group, kind, namespace and name are the join key back to configuration.
# BREAK=1 strips the label with kubectl and requires the replan to propose
# restoring it, which proves the empty-replan assertions are real and the
# marker is what the plan reads.

SMOKE_WORK="$SMOKE_WORKROOT/k8s-greenfield"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-k8s/." "$SMOKE_WORK/"

cluster_up

kc() { kubectl --kubeconfig "$KUBECONFIG" "$@"; }

step "1. a Kubernetes estate, one plain apply"
explain \
  "The configuration has a live block, a Kubernetes provider, and nothing" \
  "else - no AWS provider, no backend, nothing pre-created. Four objects:" \
  "a namespace, a ConfigMap, a ServiceAccount and a Service. The cluster is" \
  "a real API server (kind), not an emulator. Stock OpenTofu would write" \
  "an authoritative terraform.tfstate here; choudoufu keeps only a" \
  "disposable cache."
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "k8s-greenfield" "init failed"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-greenfield" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 4 added' <<< "$APPLY_OUT" \
  || fail "k8s-greenfield" "apply did not report 4 added: $APPLY_OUT"
[ ! -f "$SMOKE_WORK/terraform.tfstate" ] \
  || fail "k8s-greenfield" "a terraform.tfstate appeared - a live-block apply must never write an authoritative state file"
proof "a namespace, a ConfigMap, a ServiceAccount and a Service created, and no terraform.tfstate exists. Two of those types have no ratified identity row: they resolve through the object-metadata rule (#1064)."

step "2. the marker, read back with kubectl - no choudoufu in the loop"
explain \
  "If the ownership record really is on the object, any Kubernetes tool" \
  "can read it. This asks the API server for the ConfigMap's labels" \
  "directly. One label, tofu-estate, says which estate owns it. There is" \
  "no tofu-address label on purpose: the object's own kind, namespace and" \
  "name are the way back to the configuration, so the address never goes" \
  "on the object (#1016)."
cmd "kubectl get configmap app-config -n smoke-k8s -o jsonpath='{.metadata.labels}'"
CM_LABELS="$(kc get configmap app-config -n smoke-k8s -o jsonpath='{.metadata.labels}' 2>&1)" \
  || fail "k8s-greenfield" "kubectl could not read the ConfigMap: $CM_LABELS"
echo "$CM_LABELS" | evidence
grep -q '"tofu-estate":"smoke-k8s"' <<< "$CM_LABELS" \
  || fail "k8s-greenfield" "the ConfigMap carries no tofu-estate=smoke-k8s label: $CM_LABELS"
if grep -q 'tofu-address' <<< "$CM_LABELS"; then
  fail "k8s-greenfield" "the ConfigMap carries a tofu-address label; the Kubernetes marker is the estate alone (#1016)"
fi
NS_LABELS="$(kc get namespace smoke-k8s -o jsonpath='{.metadata.labels}' 2>&1)"
grep -q '"tofu-estate":"smoke-k8s"' <<< "$NS_LABELS" \
  || fail "k8s-greenfield" "the namespace carries no tofu-estate label: $NS_LABELS"
SA_LABELS="$(kc get serviceaccount app -n smoke-k8s -o jsonpath='{.metadata.labels}' 2>&1)"
grep -q '"tofu-estate":"smoke-k8s"' <<< "$SA_LABELS" \
  || fail "k8s-greenfield" "the service account, a type with no ratified row, carries no tofu-estate label: $SA_LABELS"
proof "the label rode the create call itself, on the ConfigMap and on the namespace. Any tool that can read a label can list this estate: kubectl get all -A -l tofu-estate=smoke-k8s."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip the label; the replan must propose restoring it"
  explain \
    "You asked for proof the assertions can fail. This removes the" \
    "tofu-estate label from the ConfigMap with kubectl, behind" \
    "choudoufu's back - the kind of edit a hostile or careless hand" \
    "would make. If the next plan is still empty, the marker is not what" \
    "the plan reads and this whole scenario is scenery."
  cmd "kubectl label configmap app-config -n smoke-k8s tofu-estate-"
  kc label configmap app-config -n smoke-k8s tofu-estate- >/dev/null || fail "k8s-greenfield" "BREAK: could not strip the label"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "k8s-greenfield" "BREAK: the plan is still empty after the label was stripped - the marker is not what the plan reads"
  fi
  grep -E '^Plan:|tofu-estate' <<< "$BOUT" | head -3 | evidence
  grep -q '"tofu-estate" = "smoke-k8s"' <<< "$BOUT" \
    || fail "k8s-greenfield" "BREAK: the plan changed but does not propose restoring tofu-estate: $BOUT"
  proof "caught. The stripped label is exactly what the plan proposes to restore, so every empty-plan claim in this scenario is a real check and the marker is load-bearing."
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

step "5. an api_version change is not a move"
explain \
  "The provider ships two spellings of most kinds: kubernetes_config_map" \
  "and kubernetes_config_map_v1 both manage a ConfigMap, and the suffix" \
  "names the API version the block is written against, not a different" \
  "object. Uniqueness on a cluster is group, kind, namespace and name, so" \
  "this edits the block's type from the plain spelling to _v1 with the" \
  "same metadata and no moved block. On AWS a type change with no moved" \
  "block is a destroy and a create; here the natural key is unchanged," \
  "the marker carries no address, and the replan must find the same" \
  "object. An in-place update for a representation difference is" \
  "allowed; a create or a destroy is not (#1081, item 2)."
cmd "sed -i 's/resource \"kubernetes_config_map\" \"app\"/resource \"kubernetes_config_map_v1\" \"app\"/' main.tf && choudoufu plan"
sed_i "$SMOKE_WORK/main.tf" 's/^resource "kubernetes_config_map" "app"/resource "kubernetes_config_map_v1" "app"/'
grep -q '^resource "kubernetes_config_map_v1" "app"' "$SMOKE_WORK/main.tf" \
  || fail "k8s-greenfield" "the type rewrite did not take; main.tf still declares the plain spelling"
PLAN3="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-greenfield" "plan after the api_version change failed: $PLAN3"
if grep -qE 'will be (created|destroyed|replaced)|must be replaced' <<< "$PLAN3"; then
  fail "k8s-greenfield" "the api_version change planned a create or a destroy; the object's natural key did not change, so it should have been found: $PLAN3"
fi
if grep -q "No changes." <<< "$PLAN3"; then
  grep -E 'No changes\.' <<< "$PLAN3" | head -1 | evidence
  proof "an empty plan. The ConfigMap the plain spelling created is the one the _v1 block now declares: same kind, same namespace and name, same label."
else
  grep -E '^Plan:|will be updated in-place' <<< "$PLAN3" | head -3 | evidence
  grep -qE '^Plan: 0 to add, [1-9][0-9]* to change, 0 to destroy\.' <<< "$PLAN3" \
    || fail "k8s-greenfield" "the api_version change planned something other than an in-place update: $PLAN3"
  grep -q 'kubernetes_config_map_v1.app will be updated in-place' <<< "$PLAN3" \
    || fail "k8s-greenfield" "the in-place update is not on the ConfigMap that changed spelling: $PLAN3"
  proof "found, and updated in place for a representation difference between the two spellings; nothing is created and nothing is destroyed."
fi

step "6. destroy - exactly what was made"
explain \
  "Teardown must remove exactly the four objects this scenario created" \
  "and leave the cluster's own namespaces alone."
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-greenfield" "apply -destroy failed: $DESTROY_OUT"
grep -E 'Destroy complete|Apply complete' <<< "$DESTROY_OUT" | head -1 | evidence
grep -qE "Resources: 0 added, 0 changed, 4 destroyed" <<< "$DESTROY_OUT" \
  || fail "k8s-greenfield" "destroy did not remove exactly the 4 created resources: $DESTROY_OUT"
if kc get namespace smoke-k8s >/dev/null 2>&1; then
  fail "k8s-greenfield" "the smoke-k8s namespace still exists after destroy"
fi
kc get namespace kube-system >/dev/null 2>&1 || fail "k8s-greenfield" "kube-system is gone; destroy reached past the estate"
proof "4 destroyed, 0 added, 0 changed. The estate is gone and the cluster's own namespaces stand."

echo "  What you watched: a Kubernetes estate live its whole life on a real"
echo "  cluster without an authoritative state file, its ownership carried as"
echo "  one label any tool can read. What you did not watch: a sweep or a"
echo "  gate. Those are the next units (#1016)."
