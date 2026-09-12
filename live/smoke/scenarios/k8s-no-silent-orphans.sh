# k8s-no-silent-orphans
# CLAIM 22 - No silent orphans on Kubernetes: an object this estate owns whose block is deleted is proposed for removal by the next plan, found by its label with one list per kind, and a controller's copies of that label are never touched. ~3 min.
#
# The Kubernetes sibling of claim 1 (#1065, under #1016's ruling). The
# estate label is the only thing that says an object is this estate's; a
# block deleted from source leaves a live object nothing declares, and the
# sweep - one cluster-wide, label-selected list per kind - is what finds it.
# The hazard the sweep has to survive is that a Deployment's pod template
# copies its labels onto ReplicaSets and Pods: this scenario puts the
# estate label in a template on purpose, and the plan must propose the one
# orphan and never a Pod or ReplicaSet. BREAK=1 strips the label from the
# orphan and requires the replan to leave it alone: an unowned object is
# not this estate's to destroy.

SMOKE_WORK="$SMOKE_WORKROOT/k8s-no-silent-orphans"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-k8s/." "$SMOKE_WORK/"

# The orphan-to-be, in a file of its own so deleting the block is deleting
# the file. And a Deployment whose pod template carries the estate label -
# the shape #1016 names as a wrong marker nobody wrote.
cat > "$SMOKE_WORK/orphan.tf" <<'TF'
resource "kubernetes_config_map" "doomed" {
  metadata {
    name      = "doomed-config"
    namespace = "smoke-k8s"
  }
  data = { fate = "orphan" }
  depends_on = [kubernetes_namespace.app]
}
TF
cat > "$SMOKE_WORK/deployment.tf" <<'TF'
resource "kubernetes_deployment" "web" {
  metadata {
    name      = "web"
    namespace = "smoke-k8s"
  }
  spec {
    replicas = 1
    selector {
      match_labels = { app = "web" }
    }
    template {
      metadata {
        labels = {
          app         = "web"
          tofu-estate = "smoke-k8s"
        }
      }
      spec {
        container {
          name  = "pause"
          image = "registry.k8s.io/pause:3.10"
        }
      }
    }
  }
  wait_for_rollout = false
  depends_on       = [kubernetes_namespace.app]
}
TF

cluster_up

kc() { kubectl --kubeconfig "$KUBECONFIG" "$@"; }

step "1. a Kubernetes estate with a Deployment whose pod template carries the estate label"
explain \
  "Six objects under a live block: the namespace, two ConfigMaps, a" \
  "ServiceAccount, a Service and a Deployment. The Deployment's pod" \
  "template carries tofu-estate=smoke-k8s on purpose - a controller will" \
  "copy it onto a ReplicaSet and a Pod nobody declared. That is the shape" \
  "a naive sweep would destroy."
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "k8s-no-silent-orphans" "init failed"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-no-silent-orphans" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 6 added' <<< "$APPLY_OUT" \
  || fail "k8s-no-silent-orphans" "apply did not report 6 added: $APPLY_OUT"
proof "six objects created, one of them a Deployment whose template copies the estate label onward."

step "2. the controller's copies exist and carry the label - read with kubectl"
explain \
  "Before anything is deleted, confirm the hazard is real: the ReplicaSet" \
  "and Pod the Deployment created carry this estate's label, and neither" \
  "is declared anywhere. They also carry an ownerReference, which is the" \
  "one fact that keeps them out of the sweep."
cmd "kubectl get replicasets,pods -n smoke-k8s -l tofu-estate=smoke-k8s"
COPIES=""
for i in $(seq 1 30); do
  COPIES="$(kc get replicasets,pods -n smoke-k8s -l tofu-estate=smoke-k8s -o name 2>/dev/null || true)"
  grep -q "replicaset" <<< "$COPIES" && grep -q "pod/" <<< "$COPIES" && break
  sleep 2
done
echo "$COPIES" | evidence
grep -q "replicaset" <<< "$COPIES" && grep -q "pod/" <<< "$COPIES" \
  || fail "k8s-no-silent-orphans" "the controller never produced a labelled ReplicaSet and Pod: $COPIES"
OWNED="$(kc get pods -n smoke-k8s -l tofu-estate=smoke-k8s -o jsonpath='{.items[0].metadata.ownerReferences[0].kind}' 2>/dev/null)"
[ "$OWNED" = "ReplicaSet" ] || fail "k8s-no-silent-orphans" "the Pod carries no ReplicaSet ownerReference: $OWNED"
proof "a ReplicaSet and a Pod carry tofu-estate=smoke-k8s, undeclared, each with an ownerReference. The label reached them; the sweep must not."

step "3. delete the ConfigMap's block from source"
explain \
  "The block is gone from the configuration. The live object is still in" \
  "the cluster, carrying the estate's label, and nothing declares it. In" \
  "stock this is the moment an object falls out of every future plan." \
  "Here the plan lists the estate by label, one list per kind."
cmd "rm orphan.tf && choudoufu plan"
rm -f "$SMOKE_WORK/orphan.tf"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip the orphan's label; the replan must leave it alone"
  explain \
    "You asked for proof the assertions can fail. This removes the" \
    "tofu-estate label from the orphaned ConfigMap with kubectl. It is" \
    "now nobody's: an object with no marker is foreign, and a plan that" \
    "still proposed destroying it would be acting on something other" \
    "than the marker - the whole scenario would be scenery."
  cmd "kubectl label configmap doomed-config -n smoke-k8s tofu-estate-"
  kc label configmap doomed-config -n smoke-k8s tofu-estate- >/dev/null || fail "k8s-no-silent-orphans" "BREAK: could not strip the label"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if grep -q "doomed" <<< "$BOUT"; then
    fail "k8s-no-silent-orphans" "BREAK: the plan still names the unlabelled ConfigMap - the destroy is not the marker's doing: $BOUT"
  fi
  grep -E '^No changes|^Plan:' <<< "$BOUT" | head -1 | evidence
  kc get configmap doomed-config -n smoke-k8s >/dev/null 2>&1 || fail "k8s-no-silent-orphans" "BREAK: the ConfigMap is gone"
  proof "caught. With its label gone the object is foreign and the plan leaves it alone, so the removal below is proposed because of the marker and nothing else."
  exit 0
fi

PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-no-silent-orphans" "plan failed: $PLAN_OUT"
grep -E '^Plan:|will be destroyed' <<< "$PLAN_OUT" | head -3 | evidence
grep -qE 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$PLAN_OUT" \
  || fail "k8s-no-silent-orphans" "the plan does not propose exactly one destroy: $PLAN_OUT"
grep -q 'doomed-config' <<< "$PLAN_OUT" \
  || fail "k8s-no-silent-orphans" "the one destroy is not the orphaned ConfigMap: $PLAN_OUT"
if grep -qiE 'kubernetes_pod|replica_set|replicaset' <<< "$PLAN_OUT"; then
  fail "k8s-no-silent-orphans" "the plan touches a controller-owned copy: $PLAN_OUT"
fi
proof "exactly one destroy, the ConfigMap nobody declares, found by its label. The ReplicaSet and the Pod carrying the same label are untouched: an ownerReference keeps a controller's copies out of every delete."

step "4. apply - the orphan goes, the copies stay"
cmd "choudoufu apply -auto-approve"
APPLY2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-no-silent-orphans" "apply failed: $APPLY2"
grep -E 'Apply complete' <<< "$APPLY2" | head -1 | evidence
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$APPLY2" \
  || fail "k8s-no-silent-orphans" "apply did not destroy exactly one object: $APPLY2"
if kc get configmap doomed-config -n smoke-k8s >/dev/null 2>&1; then
  fail "k8s-no-silent-orphans" "the orphaned ConfigMap still exists after apply"
fi
STILL="$(kc get replicasets,pods -n smoke-k8s -l tofu-estate=smoke-k8s -o name 2>/dev/null)"
grep -q "replicaset" <<< "$STILL" && grep -q "pod/" <<< "$STILL" \
  || fail "k8s-no-silent-orphans" "a controller-owned copy was destroyed: $STILL"
proof "the orphan is gone and the ReplicaSet and Pod carrying its label stand."

step "5. the replan is empty, and destroy removes exactly what remains"
PLAN3="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-no-silent-orphans" "replan failed: $PLAN3"
grep -q "No changes." <<< "$PLAN3" || fail "k8s-no-silent-orphans" "replan is not empty: $PLAN3"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-no-silent-orphans" "apply -destroy failed: $DESTROY_OUT"
grep -E 'Destroy complete|Apply complete' <<< "$DESTROY_OUT" | head -1 | evidence
grep -qE "Resources: 0 added, 0 changed, 5 destroyed" <<< "$DESTROY_OUT" \
  || fail "k8s-no-silent-orphans" "destroy did not remove exactly the 5 remaining objects: $DESTROY_OUT"
proof "5 destroyed, 0 added, 0 changed. The estate is gone."

echo "  What you watched: an object whose block was deleted found by its label"
echo "  and removed, while a controller's copies of that same label were left"
echo "  alone. On Kubernetes the sweep is one list per kind, and the"
echo "  ownerReference is what makes it safe."
