# k8s-custom-resource
# CLAIM 24 - A custom resource binds by its natural key: a kubernetes_manifest block is found again by the apiVersion, kind, namespace and name written inside its manifest, with no state file and no label, and an object deleted out of band walks back into the plan as a create. ~2 min.
#
# The first unit of #1079 (ruled 2026-09-12): every custom resource is
# declared through kubernetes_manifest, whose whole object is one dynamic
# argument, and the natural key #1016 ruled on is four keys inside that
# argument's object constructor. Identity resolution reads them without
# evaluating the manifest (internal/live/identity/manifest.go) and renders
# the provider's own import id, so a replan with nothing stored anywhere
# re-binds the object. What this claim does NOT say, on purpose: the object
# carries no tofu-estate label yet (the stamp into manifest.metadata.labels
# is the ruling's next unit), so no sweep finds it and no policy fences it;
# it is bound by its declaration alone, the way an untaggable AWS type is.
# BREAK=1 deletes the object with kubectl and requires the replan to
# propose creating it: if the plan stayed empty, nothing was reading the
# cluster by that key and the empty replans above were scenery.

SMOKE_WORK="$SMOKE_WORKROOT/k8s-custom-resource"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

# The CRD is the one the Kubernetes documentation uses to explain custom
# resources (crontabs.stable.example.com), installed with kubectl before
# the estate is planned: the provider reads a custom kind's schema from the
# cluster at plan time, which is why a manifest whose CRD is not served is
# refused by name rather than defaulted (#1079's fourth ruling).
cat > "$SMOKE_WORK/crd.yaml" <<'YAML'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: crontabs.stable.example.com
spec:
  group: stable.example.com
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                cronSpec:
                  type: string
                image:
                  type: string
                replicas:
                  type: integer
  scope: Namespaced
  names:
    plural: crontabs
    singular: crontab
    kind: CronTab
    shortNames:
      - ct
YAML
cat > "$SMOKE_WORK/versions.tf" <<'TF'
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "smoke-crd"
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
resource "kubernetes_namespace" "crd" {
  metadata {
    name = "smoke-crd"
  }
}

resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "my-crontab"
      namespace = "smoke-crd"
    }
    spec = {
      cronSpec = "* * * * */5"
      image    = "my-awesome-cron-image"
    }
  }
  depends_on = [kubernetes_namespace.crd]
}
TF

cluster_up

kc() { kubectl --kubeconfig "$KUBECONFIG" "$@"; }

step "1. a CRD the cluster serves, installed with kubectl"
explain \
  "The provider reads a custom kind's schema from the cluster when it" \
  "plans, so the CRD comes first, through kubectl, the way an operator's" \
  "install step or a platform team would put it there. Nothing about it" \
  "is this tool's; it is the CronTab from the Kubernetes documentation."
cmd "kubectl apply -f crd.yaml"
kc apply -f "$SMOKE_WORK/crd.yaml" >/dev/null || fail "k8s-custom-resource" "could not install the CRD"
kc wait --for=condition=Established crd/crontabs.stable.example.com --timeout=60s >/dev/null || fail "k8s-custom-resource" "the CRD never became Established"
kc get crd crontabs.stable.example.com -o jsonpath='{.metadata.name}{" "}{.spec.scope}{"\n"}' | evidence
proof "crontabs.stable.example.com is served and namespaced. The estate below declares one CronTab through kubernetes_manifest."

step "2. the estate applies: a namespace and a custom resource, no state file"
explain \
  "One kubernetes_manifest block. Its identity is the natural key written" \
  "inside the manifest - apiVersion, kind, metadata.namespace," \
  "metadata.name - which identity resolution reads out of the object" \
  "constructor without evaluating the manifest, rendered as the provider's" \
  "own import id. choudoufu keeps only a disposable cache."
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "k8s-custom-resource" "init failed"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" || fail "k8s-custom-resource" "apply did not report 2 added: $APPLY_OUT"
[ ! -f "$SMOKE_WORK/terraform.tfstate" ] || fail "k8s-custom-resource" "a terraform.tfstate appeared"
cmd "kubectl get crontab my-crontab -n smoke-crd"
CT="$(kc get crontab my-crontab -n smoke-crd -o jsonpath='{.spec.cronSpec}{" "}{.spec.image}{"\n"}' 2>&1)" \
  || fail "k8s-custom-resource" "kubectl cannot read the CronTab: $CT"
echo "$CT" | evidence
proof "the CronTab exists in the cluster with the spec the configuration declared, and no terraform.tfstate exists."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - delete the custom resource out of band; the replan must propose creating it"
  explain \
    "You asked for proof the assertions can fail. This deletes the CronTab" \
    "with kubectl, behind choudoufu's back. If the next plan is still" \
    "empty, nothing was reading the cluster by the natural key and every" \
    "empty replan in this scenario is scenery."
  cmd "kubectl delete crontab my-crontab -n smoke-crd"
  kc delete crontab my-crontab -n smoke-crd >/dev/null || fail "k8s-custom-resource" "BREAK: could not delete the CronTab"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "k8s-custom-resource" "BREAK: the plan is still empty after the CronTab was deleted - the natural key is not what the plan reads"
  fi
  grep -E '^Plan:|will be created' <<< "$BOUT" | head -2 | evidence
  grep -q 'kubernetes_manifest.crontab will be created' <<< "$BOUT" \
    || fail "k8s-custom-resource" "BREAK: the plan changed but does not propose creating kubernetes_manifest.crontab: $BOUT"
  proof "caught. The deleted object is exactly what the plan proposes to create, so the empty replans below are real checks and the natural key is load-bearing."
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "3. the replan - prior state rebuilt from the cluster by the natural key"
explain \
  "With no state file, the next plan asks the cluster for the object the" \
  "manifest names: apiVersion=stable.example.com/v1,kind=CronTab," \
  "namespace=smoke-crd,name=my-crontab, the provider's own import id. If" \
  "binding is right, the plan is empty."
cmd "choudoufu plan"
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "plan failed: $PLAN_OUT"
grep -q "No changes." <<< "$PLAN_OUT" || fail "k8s-custom-resource" "replan is not empty: $(grep -E '^Plan:|will be' <<< "$PLAN_OUT" | head -3)"
grep -E 'No changes\.' <<< "$PLAN_OUT" | head -1 | evidence
proof "an empty plan, the custom resource found by the four keys written in its manifest and nothing else."

step "4. the cache is disposable"
cmd "rm .terraform/choudoufu-cache.tfstate && choudoufu plan"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
[ -f "$CACHE" ] || fail "k8s-custom-resource" "no cache at $CACHE after a plain apply"
rm -f "$CACHE"
PLAN2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "plan without the cache failed"
grep -q "No changes." <<< "$PLAN2" || fail "k8s-custom-resource" "deleting the cache changed the plan: $(grep -E '^Plan:|will be' <<< "$PLAN2" | head -3)"
grep -E 'No changes\.' <<< "$PLAN2" | head -1 | evidence
proof "the cache was there and its loss changed nothing."

step "5. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-custom-resource" "apply -destroy failed: $DESTROY_OUT"
grep -E 'Destroy complete|Apply complete' <<< "$DESTROY_OUT" | head -1 | evidence
grep -qE "Resources: 0 added, 0 changed, 2 destroyed" <<< "$DESTROY_OUT" \
  || fail "k8s-custom-resource" "destroy did not remove exactly the 2 created resources: $DESTROY_OUT"
if kc get crontab my-crontab -n smoke-crd >/dev/null 2>&1; then
  fail "k8s-custom-resource" "the CronTab still exists after destroy"
fi
kc get crd crontabs.stable.example.com >/dev/null 2>&1 || fail "k8s-custom-resource" "the CRD is gone; destroy reached past the estate"
proof "2 destroyed, 0 added, 0 changed. The custom resource is gone and the CRD, which nothing declared, stands."

echo "  What you watched: a custom resource live its whole life without a state"
echo "  file, found again each time by the apiVersion, kind, namespace and name"
echo "  written inside its manifest. What you did not watch: a label on it."
echo "  That is the next unit of #1079, and until it lands nothing sweeps or"
echo "  fences an object declared this way."
