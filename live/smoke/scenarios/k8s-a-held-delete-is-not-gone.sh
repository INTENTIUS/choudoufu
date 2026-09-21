# k8s-a-held-delete-is-not-gone
# CLAIM 25 - A delete the platform accepted but has not finished leaves an object the estate still owns: the API returns success, the object stays with a deletionTimestamp and its tofu-estate label, and the sweep finds it and proposes the same one destroy on every plan until it is really gone, at which point the plan is empty with no surgery. ~3 min.
#
# The first fault of #1110, and the one operators hit weekly. A finalizer
# on an object turns DELETE into a request: the API server sets
# metadata.deletionTimestamp, returns 200, and the object stays until the
# controller that owns the finalizer removes it. The hashicorp/kubernetes
# provider's ConfigMap delete does not wait for the object to be gone, so
# the run reports "Destruction complete after 0s" over an object that is
# still in the cluster - and stock, which learns what exists from a state
# file, then has no entry for it and will never mention it again. That is a
# silent orphan made by a successful delete.
#
# What this scenario measures is the answer to the three questions #1110
# asks. (1) The next plan: the estate label is still on the terminating
# object, the sweep lists it, and the plan proposes the same one destroy -
# every run, for as long as the finalizer holds. (2) apply -destroy: it
# reports the estate destroyed and exits 0 while the held object is still
# there, and the plan that follows is what corrects it. (3) The marker: the
# label survives termination, so a terminating object reads as a live owned
# resource, which is what it is.
#
# The one line in all of this that is not true is the run's own summary:
# "Destruction complete after 0s" and "1 destroyed" are the provider's word
# for "the API accepted the delete", not for "the object is gone". Stock
# prints the same two lines, so they stay exactly as they are and this
# scenario asserts them verbatim, because they are what a user sees. What
# #1184 added is the line after them: once the apply is done, one list of
# the kind it deleted from, by the estate's label, and one warning - "Delete
# accepted, object not gone" - naming the object that is still there with a
# deletionTimestamp, and the finalizer holding it. A warning: the exit code
# is still the apply's. Steps 4 and 7 assert it by name and finalizer, and
# the BREAK arm asserts its absence over a delete that really finished.
#
# The namespace is created with kubectl rather than declared, on purpose: a
# kubernetes_namespace delete DOES wait for the namespace to be gone, and a
# finalizer inside it blocks that wait for the provider's whole delete
# timeout. That variant is measured on #1184 too (five minutes, then
# "timeout while waiting for resource to be gone (last state:
# 'Terminating'...)"); it is the provider's timer, not this claim's
# subject, and paying it twice a run buys nothing.
#
# BREAK=1 removes the finalizer before the destroying apply and requires
# the opposite outcome: the object really goes and the replan is empty.
# Without that control the scenario would read the same if choudoufu simply
# never deleted a ConfigMap.

SMOKE_WORK="$SMOKE_WORKROOT/k8s-a-held-delete-is-not-gone"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

cat > "$SMOKE_WORK/versions.tf" <<'TF'
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "smoke-k8s"
  }

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}
TF

cat > "$SMOKE_WORK/main.tf" <<'TF'
resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = "smoke-k8s"
  }

  data = {
    greeting = "hello"
  }
}
TF

held_block() {
  cat > "$SMOKE_WORK/held.tf" <<'TF'
resource "kubernetes_config_map" "held" {
  metadata {
    name      = "held-config"
    namespace = "smoke-k8s"
  }

  data = {
    fate = "held"
  }
}
TF
}
held_block

cluster_up

kc() { kubectl --kubeconfig "$KUBECONFIG" "$@"; }

FINALIZER="smoke.choudoufu.io/hold"

step "0. a namespace the estate does not own"
explain \
  "The platform team's namespace, made with kubectl. Two ConfigMaps in it" \
  "are the whole estate, so nothing in the run waits on a namespace" \
  "delete - a kubernetes_namespace delete blocks on everything inside it" \
  "and that timer would hide the answer this scenario is after."
cmd "kubectl create namespace smoke-k8s"
kc create namespace smoke-k8s >/dev/null || fail "k8s-a-held-delete-is-not-gone" "could not create the namespace"
kc get namespace smoke-k8s -o jsonpath='{.metadata.name} {.status.phase}{"\n"}' | evidence
proof "the namespace exists and is nobody's; the estate is the two ConfigMaps inside it."

step "1. the estate applies - two objects, both labelled"
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "k8s-a-held-delete-is-not-gone" "init failed"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-a-held-delete-is-not-gone" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || fail "k8s-a-held-delete-is-not-gone" "apply did not report 2 added: $APPLY_OUT"
LABELLED="$(kc get configmaps -n smoke-k8s -l tofu-estate=smoke-k8s -o name | sort | tr '\n' ' ')"
echo "$LABELLED" | evidence
[ "$LABELLED" = "configmap/app-config configmap/held-config " ] \
  || fail "k8s-a-held-delete-is-not-gone" "the estate label is not on both ConfigMaps: $LABELLED"
proof "two objects, each carrying tofu-estate=smoke-k8s. The label is the only thing that says they are this estate's."

step "2. an operator puts a finalizer on one of them"
explain \
  "This is the weekly hazard: a controller - a backup operator, a policy" \
  "agent, an owner-reference garbage collector - registers a finalizer on" \
  "an object so it can do something before the object disappears. Nothing" \
  "in the configuration mentions it, and a plan cannot see it coming."
cmd "kubectl patch configmap held-config -n smoke-k8s --type merge -p '{\"metadata\":{\"finalizers\":[\"$FINALIZER\"]}}'"
kc patch configmap held-config -n smoke-k8s --type merge \
  -p "{\"metadata\":{\"finalizers\":[\"$FINALIZER\"]}}" >/dev/null \
  || fail "k8s-a-held-delete-is-not-gone" "could not add the finalizer"
HOLD="$(kc get configmap held-config -n smoke-k8s -o jsonpath='{.metadata.finalizers}')"
echo "$HOLD" | evidence
[ "$HOLD" = "[\"$FINALIZER\"]" ] || fail "k8s-a-held-delete-is-not-gone" "the finalizer is not on the object: $HOLD"
proof "held-config is held. A DELETE against it from here on is a request, not an event."

step "3. the block is deleted from source - the plan proposes exactly one destroy"
cmd "rm held.tf && choudoufu plan"
rm -f "$SMOKE_WORK/held.tf"
PLAN1="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-a-held-delete-is-not-gone" "plan failed: $PLAN1"
grep -E '^Plan:' <<< "$PLAN1" | evidence
grep -qE 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$PLAN1" \
  || fail "k8s-a-held-delete-is-not-gone" "the plan does not propose exactly one destroy: $PLAN1"
grep -q 'held-config' <<< "$PLAN1" \
  || fail "k8s-a-held-delete-is-not-gone" "the one destroy is not held-config: $PLAN1"
proof "one destroy, found by the label, nothing else. So far this is claim 1 on Kubernetes."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - take the finalizer off first; the delete must then be real"
  explain \
    "You asked for proof the assertions can fail. Everything below rests" \
    "on one manufactured fact: a finalizer is holding the object. Remove" \
    "it and run the identical apply. If the object survived THIS too, the" \
    "scenario would be measuring choudoufu failing to delete a ConfigMap" \
    "and the finalizer would be scenery. It must go, and the replan must" \
    "be empty."
  cmd "kubectl patch configmap held-config -n smoke-k8s --type merge -p '{\"metadata\":{\"finalizers\":null}}' && choudoufu apply -auto-approve"
  kc patch configmap held-config -n smoke-k8s --type merge -p '{"metadata":{"finalizers":null}}' >/dev/null \
    || fail "k8s-a-held-delete-is-not-gone" "BREAK: could not remove the finalizer"
  BAPPLY="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "k8s-a-held-delete-is-not-gone" "BREAK: apply failed: $BAPPLY"
  grep -E 'Apply complete!' <<< "$BAPPLY" | evidence
  grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$BAPPLY" \
    || fail "k8s-a-held-delete-is-not-gone" "BREAK: apply did not report one destroy: $BAPPLY"
  if grep -q 'Delete accepted, object not gone' <<< "$BAPPLY"; then
    fail "k8s-a-held-delete-is-not-gone" "BREAK: the held-delete warning (#1184) was printed over a delete nothing was holding: $BAPPLY"
  fi
  for i in $(seq 1 15); do
    kc get configmap held-config -n smoke-k8s >/dev/null 2>&1 || break
    sleep 1
  done
  if kc get configmap held-config -n smoke-k8s >/dev/null 2>&1; then
    STILL="$(kc get configmap held-config -n smoke-k8s -o jsonpath='{.metadata.deletionTimestamp}')"
    fail "k8s-a-held-delete-is-not-gone" "BREAK: with no finalizer the object still did not go (deletionTimestamp=$STILL) - the rest of this scenario would be measuring a broken delete, not a held one"
  fi
  { kc get configmap held-config -n smoke-k8s 2>&1 || true; } | tail -1 | evidence
  BPLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
    || fail "k8s-a-held-delete-is-not-gone" "BREAK: replan failed: $BPLAN"
  grep -E '^No changes|^Plan:' <<< "$BPLAN" | head -1 | evidence
  grep -q 'No changes.' <<< "$BPLAN" \
    || fail "k8s-a-held-delete-is-not-gone" "BREAK: the replan is not empty, so the object is still owned and present: $BPLAN"
  kc delete namespace smoke-k8s --wait=false >/dev/null 2>&1 || true
  proof "caught. With nothing holding it the object is gone in one apply and the replan is empty, so the persistence measured in the main arm is the finalizer's doing and not a delete choudoufu never made."
  exit 0
fi

step "4. apply - the run says destroyed, the cluster says terminating"
explain \
  "The provider issues DELETE and returns. It does not wait, so the run" \
  "prints \"Destruction complete after 0s\" and counts one destroyed." \
  "That line is the API's acceptance, not the object's end: read the" \
  "cluster straight afterwards and the object is still there, with a" \
  "deletionTimestamp and the estate's label still on it. Those two lines" \
  "are stock's and stay as they are; what the run adds (#1184) is one" \
  "warning after them, from one list of the kind it just deleted from:" \
  "the object by name, and the finalizer that is holding it."
cmd "choudoufu apply -auto-approve  # then: kubectl get configmap held-config -n smoke-k8s"
APPLY2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-a-held-delete-is-not-gone" "apply failed: $APPLY2"
grep -E 'Destruction complete|Apply complete!' <<< "$APPLY2" | evidence
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$APPLY2" \
  || fail "k8s-a-held-delete-is-not-gone" "apply did not report one destroy: $APPLY2"
grep -E 'Delete accepted, object not gone| - ConfigMap ' <<< "$APPLY2" | evidence
grep -q 'Warning: Delete accepted, object not gone' <<< "$APPLY2" \
  || fail "k8s-a-held-delete-is-not-gone" "apply did not warn that the delete was only accepted (#1184): $APPLY2"
grep -qE -- "- ConfigMap smoke-k8s/held-config \(.*\), finalizers: $FINALIZER\$" <<< "$APPLY2" \
  || fail "k8s-a-held-delete-is-not-gone" "the warning does not name ConfigMap smoke-k8s/held-config and its finalizer $FINALIZER: $APPLY2"
kc get configmap held-config -n smoke-k8s >/dev/null 2>&1 \
  || fail "k8s-a-held-delete-is-not-gone" "held-config is gone; the finalizer did not hold and there is no fault to measure"
DEL_TS="$(kc get configmap held-config -n smoke-k8s -o jsonpath='{.metadata.deletionTimestamp}')"
ESTATE_LABEL="$(kc get configmap held-config -n smoke-k8s -o jsonpath='{.metadata.labels.tofu-estate}')"
HELD_BY="$(kc get configmap held-config -n smoke-k8s -o jsonpath='{.metadata.finalizers}')"
echo "deletionTimestamp=$DEL_TS  tofu-estate=$ESTATE_LABEL  finalizers=$HELD_BY" | evidence
[ -n "$DEL_TS" ] \
  || fail "k8s-a-held-delete-is-not-gone" "the object has no deletionTimestamp, so it was never asked to go"
[ "$ESTATE_LABEL" = "smoke-k8s" ] \
  || fail "k8s-a-held-delete-is-not-gone" "the terminating object lost its estate label: $ESTATE_LABEL"
[ "$HELD_BY" = "[\"$FINALIZER\"]" ] \
  || fail "k8s-a-held-delete-is-not-gone" "the finalizer is not what is holding it: $HELD_BY"
proof "one destroyed, says the run, and then: delete accepted, object not gone, held-config, held by $FINALIZER. deletionTimestamp set, tofu-estate=smoke-k8s still on it, says the cluster. This is the moment stock loses the object: its state file has no entry for it any more."

step "5. the next plan - the sweep finds it again, by the same label"
explain \
  "Nothing declares held-config and nothing in any cache remembers it." \
  "The plan lists the estate the only way it ever does, one label" \
  "selector per kind, and a terminating object is a live object: it comes" \
  "back as the same one destroy. Run the plan twice - the answer is not a" \
  "one-off, it is what every plan says until the object is really gone."
cmd "choudoufu plan   # twice"
for n in 1 2; do
  PLANn="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
    || fail "k8s-a-held-delete-is-not-gone" "plan $n failed: $PLANn"
  grep -E '^Plan:' <<< "$PLANn" | sed "s/^/plan $n: /" | evidence
  grep -qE 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$PLANn" \
    || fail "k8s-a-held-delete-is-not-gone" "plan $n does not propose the one destroy again: $PLANn"
  grep -q 'held-config' <<< "$PLANn" \
    || fail "k8s-a-held-delete-is-not-gone" "plan $n does not name held-config: $PLANn"
done
proof "the same destroy, every run. An accepted delete the platform has not finished does not take the object out of the estate, and no plan pretends it is gone."

step "6. the finalizer clears - the object goes and the plan is empty"
explain \
  "The controller finishes its work and removes its finalizer, which is" \
  "the only thing that was holding the object. No import, no state edit," \
  "no -refresh-only: the next plan reads the cluster and there is nothing" \
  "left to say."
cmd "kubectl patch configmap held-config -n smoke-k8s --type merge -p '{\"metadata\":{\"finalizers\":null}}' && choudoufu plan"
kc patch configmap held-config -n smoke-k8s --type merge -p '{"metadata":{"finalizers":null}}' >/dev/null \
  || fail "k8s-a-held-delete-is-not-gone" "could not clear the finalizer"
for i in $(seq 1 15); do
  kc get configmap held-config -n smoke-k8s >/dev/null 2>&1 || break
  sleep 1
done
{ kc get configmap held-config -n smoke-k8s 2>&1 || true; } | tail -1 | evidence
if kc get configmap held-config -n smoke-k8s >/dev/null 2>&1; then
  fail "k8s-a-held-delete-is-not-gone" "the object outlived its finalizer"
fi
PLAN_EMPTY="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-a-held-delete-is-not-gone" "plan failed: $PLAN_EMPTY"
grep -E '^No changes|^Plan:' <<< "$PLAN_EMPTY" | head -1 | evidence
grep -q 'No changes.' <<< "$PLAN_EMPTY" \
  || fail "k8s-a-held-delete-is-not-gone" "the plan is not empty once the object is gone: $PLAN_EMPTY"
proof "gone, and the plan is empty. The destroy took as long as the finalizer took, and recovery was a re-run."

step "7. apply -destroy over a held object - the run reports success it does not have"
explain \
  "The same fault at the end of an estate's life. held-config is back and" \
  "held again; apply -destroy deletes both ConfigMaps, and because no" \
  "declared object waits on another the run reports the estate destroyed" \
  "and exits 0 while one object is still in the cluster. The #1184 warning" \
  "names the one object of the two that is still there, and the exit code" \
  "stays 0. What the promise buys is the line after it: the very next plan" \
  "reads the cluster, sees the terminating object still carrying the" \
  "label, and proposes exactly the one create that is genuinely missing."
cmd "choudoufu apply -auto-approve && kubectl patch ... && choudoufu apply -destroy -auto-approve"
held_block
APPLY3="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-a-held-delete-is-not-gone" "re-apply failed: $APPLY3"
grep -qE 'Resources: 1 added' <<< "$APPLY3" \
  || fail "k8s-a-held-delete-is-not-gone" "re-apply did not add held-config back: $APPLY3"
kc patch configmap held-config -n smoke-k8s --type merge \
  -p "{\"metadata\":{\"finalizers\":[\"$FINALIZER\"]}}" >/dev/null \
  || fail "k8s-a-held-delete-is-not-gone" "could not re-add the finalizer"
DESTROY_RC=0
DESTROY="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || DESTROY_RC=$?
grep -E 'Destroy complete|Apply complete' <<< "$DESTROY" | head -1 | evidence
[ "$DESTROY_RC" = "0" ] \
  || fail "k8s-a-held-delete-is-not-gone" "apply -destroy did not exit 0; if it now refuses or waits, this claim's wording is out of date: $DESTROY"
grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$DESTROY" \
  || fail "k8s-a-held-delete-is-not-gone" "apply -destroy did not report both objects destroyed: $DESTROY"
grep -E 'Delete accepted, object not gone| - ConfigMap ' <<< "$DESTROY" | evidence
grep -q 'Warning: Delete accepted, object not gone' <<< "$DESTROY" \
  || fail "k8s-a-held-delete-is-not-gone" "apply -destroy did not warn that one delete was only accepted (#1184): $DESTROY"
grep -qE -- "- ConfigMap smoke-k8s/held-config \(.*\), finalizers: $FINALIZER\$" <<< "$DESTROY" \
  || fail "k8s-a-held-delete-is-not-gone" "the warning does not name ConfigMap smoke-k8s/held-config and its finalizer $FINALIZER: $DESTROY"
NAMED="$(grep -cE -- '- ConfigMap smoke-k8s/' <<< "$DESTROY" || true)"
[ "$NAMED" = "1" ] \
  || fail "k8s-a-held-delete-is-not-gone" "the warning names $NAMED objects, want exactly the held one: app-config really went: $DESTROY"
kc get configmap held-config -n smoke-k8s >/dev/null 2>&1 \
  || fail "k8s-a-held-delete-is-not-gone" "held-config is gone; the finalizer did not hold this time"
kc get configmap held-config -n smoke-k8s -o jsonpath='{.metadata.name} {.metadata.deletionTimestamp} {.metadata.labels.tofu-estate}{"\n"}' | evidence
PLAN_AFTER="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-a-held-delete-is-not-gone" "plan after destroy failed: $PLAN_AFTER"
grep -E '^Plan:' <<< "$PLAN_AFTER" | evidence
grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$PLAN_AFTER" \
  || fail "k8s-a-held-delete-is-not-gone" "the plan after the destroy does not propose exactly the one missing create: $PLAN_AFTER"
grep -q 'app-config' <<< "$PLAN_AFTER" \
  || fail "k8s-a-held-delete-is-not-gone" "the one create is not app-config: $PLAN_AFTER"
proof "the run says the estate is destroyed and, in the same breath, which one object is not and what holds it. The plan after it counts what is actually there and proposes one create, not two. The summary count is the provider's; the warning and the plan are the cluster's."

step "8. clear the hold and put the namespace back"
kc patch configmap held-config -n smoke-k8s --type merge -p '{"metadata":{"finalizers":null}}' >/dev/null \
  || fail "k8s-a-held-delete-is-not-gone" "could not clear the finalizer"
for i in $(seq 1 15); do
  kc get configmap held-config -n smoke-k8s >/dev/null 2>&1 || break
  sleep 1
done
LEFT="$(kc get configmaps -n smoke-k8s -l tofu-estate=smoke-k8s -o name | tr '\n' ' ')"
echo "left: ${LEFT:-none}" | evidence
[ -z "$LEFT" ] || fail "k8s-a-held-delete-is-not-gone" "objects carrying the estate label survived: $LEFT"
kc delete namespace smoke-k8s --wait=false >/dev/null 2>&1 || true
proof "nothing carrying tofu-estate=smoke-k8s is left in the cluster."

echo "  What you watched: a delete the API accepted and the cluster did not"
echo "  finish. The run's own summary said destroyed, and the warning after"
echo "  it named the object still there and its finalizer; the object stayed,"
echo "  terminating, with its estate label intact. Every plan after it"
echo "  proposed the same one destroy until the finalizer cleared, and then"
echo "  the plan was empty. Stock, which learns what exists from a state"
echo "  file it has already emptied, would never have mentioned the object"
echo "  again."
