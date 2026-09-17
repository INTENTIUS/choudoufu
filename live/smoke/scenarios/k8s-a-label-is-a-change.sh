# k8s-a-label-is-a-change
# CLAIM 27 - An edit to a Kubernetes object's labels or annotations is an ordinary change: a label edited in the configuration plans one in-place update and the apply writes it, stock's own answer for the same edit alongside it, an annotation added and then changed does the same, and a key the configuration never declared - the API server's own kubernetes.io/metadata.name, a controller's annotation - stays the server's and churns nothing. ~4 min.
#
# GitHub issue #1177, and the one place a stateless run pays for having no
# last-applied value.
#
# kubernetes_manifest declares its whole object in one dynamic `manifest`
# argument, and the provider's computed_fields argument (default
# metadata.annotations and metadata.labels) tells it to take the LIVE
# object's value at those paths unless the configuration differs from the
# PRIOR MANIFEST. A state-backed run's prior manifest is what was last
# applied, so an edit differs from it and plans. choudoufu has no state: it
# rebuilds the prior on every plan, and configuredAttrsSeed seeds
# `manifest` from the CURRENT configuration on the rule that a non-Computed
# attribute is one nothing but configuration can set. That rule is right
# for every provider that does not read the prior - which is nearly all of
# them, since ProposedNew takes the configured value regardless - and on
# this one type it made the provider compare the configuration with itself.
# It could never differ. Every edit to a label or an annotation planned as
# "No changes." and applied as nothing written, silently, with no refusal
# to look up and no warning in the plan.
#
# It was not this fork's diff that was wrong. Doctor a stock state file to
# hold exactly what the seed produces - prior manifest carrying the NEW
# label, object left at the old one - and STOCK prints "No changes." for
# the same edit. The fix (internal/live/projection/nodestamp_manifest.go,
# mirrorManifestComputedFields) gives the prior manifest the LIVE object's
# value for every key the configuration declares, which is what
# #1079's marker arm already did for tofu-estate alone.
#
# What step 5 measures, and it is the boundary case: the mirror is
# restricted to the keys configuration declares, on purpose. Mirroring the
# live maps wholesale would put every server-written key in the prior, the
# configuration would then differ from it, and the provider's rule takes
# the configuration for the WHOLE path when it does - so every plan would
# propose deleting kubernetes.io/metadata.name,
# kubectl.kubernetes.io/last-applied-configuration, cert-manager.io/* and
# meta.helm.sh/*, forever, against a server that re-adds them. That is the
# half of computed_fields that has to survive the fix, and step 5 is where
# it is measured rather than assumed.
#
# The one difference from stock this leaves, step 6: a DECLARED label
# changed out of band now plans, where stock's computed_fields swallows it.
# "The configuration was edited" and "the live object drifted" are the same
# observation without a last-applied value to tell them apart, so making
# the first visible necessarily makes the second visible. Step 6 runs the
# same kubectl command against both runners and prints both answers, so the
# difference stays measured rather than worked around. A key REMOVED from
# the configuration is the remaining gap (#1211).
#
# BREAK=1 runs the identical kubectl command against a key the
# configuration does NOT declare - same object, same --overwrite, one key
# different - and requires the opposite outcome: "No changes.". Without
# that control every plan in this scenario would read the same if
# choudoufu simply planned on any difference between the configuration and
# the live object, and the whole of step 5 would be scenery.

SCEN="k8s-a-label-is-a-change"

SMOKE_WORK="$SMOKE_WORKROOT/$SCEN"
mkdir -p "$SMOKE_WORK/live" "$SMOKE_WORK/stock"; export SMOKE_WORK

NS="smoke-label"
STOCK_NS="smoke-label-stock"
ESTATE="smoke-label"

cat > "$SMOKE_WORK/live/versions.tf" <<TF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "$ESTATE"
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

# live_config writes the declared objects. $1 is metadata.labels.tier, $2 an
# optional annotations block body (empty for none). The namespace is
# declared in the same root, as a manifest too, because step 5 needs an
# object the API SERVER writes a label onto: every Namespace carries
# kubernetes.io/metadata.name and no configuration can declare it away.
live_config() {
  local ann=""
  [ -n "${2:-}" ] && ann="      \"annotations\" = {
$2
      }
"
  cat > "$SMOKE_WORK/live/main.tf" <<TF
resource "kubernetes_manifest" "ns" {
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "Namespace"
    "metadata" = {
      "name" = "$NS"
    }
  }
}

resource "kubernetes_manifest" "cm" {
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "ConfigMap"
    "metadata" = {
$ann      "labels" = {
        "tier" = "$1"
      }
      "name"      = "app-config"
      "namespace" = "$NS"
    }
    "data" = {
      "greeting" = "hello"
    }
  }
  depends_on = [kubernetes_manifest.ns]
}
TF
}

live_config one

cluster_up

kc() { kubectl --kubeconfig "$KUBECONFIG" "$@"; }

step "1. apply - one ConfigMap and its namespace, declared as manifests, no state file"
explain \
  "kubernetes_manifest is the type every custom resource is declared" \
  "through, and the one type whose whole object is a single dynamic" \
  "argument. The estate's tofu-estate label goes inside that argument," \
  "at manifest.metadata.labels, beside the tier label the configuration" \
  "itself writes. Everything after this step edits that map."
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK/live" && chdf init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "$SCEN" "init failed"
APPLY1="$(cd "$SMOKE_WORK/live" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the first apply failed: $(tail -20 <<< "$APPLY1")"
{ grep -E 'Apply complete!' <<< "$APPLY1" || true; } | evidence
grep -qE 'Resources: 2 added' <<< "$APPLY1" \
  || fail "$SCEN" "the first apply did not create the namespace and the ConfigMap: $(grep -E 'Apply complete' <<< "$APPLY1")"
LABELS1="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels}')"
echo "$LABELS1" | evidence
grep -q '"tier":"one"' <<< "$LABELS1" \
  || fail "$SCEN" "the object does not carry the configuration's own label: $LABELS1"
grep -q "\"tofu-estate\":\"$ESTATE\"" <<< "$LABELS1" \
  || fail "$SCEN" "the object does not carry the estate marker: $LABELS1"
[ ! -f "$SMOKE_WORK/live/terraform.tfstate" ] || fail "$SCEN" "a terraform.tfstate appeared"
proof "the object exists with the configuration's tier=one and the estate's own tofu-estate label, and no terraform.tfstate was written."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the identical kubectl command against an UNDECLARED key must NOT plan"
  explain \
    "You asked for proof the assertions can fail. Steps 3 and 6 below" \
    "require a plan whenever the live object's value for a DECLARED key" \
    "differs from the configuration. If a plan appeared for a key the" \
    "configuration never mentions as well, none of that would be" \
    "evidence about declared keys: it would just be choudoufu planning" \
    "on any difference at all, and the provider's computed_fields rule -" \
    "which is what keeps kubernetes.io/metadata.name and every" \
    "controller's annotation out of the plan - would be scenery." \
    "Same object, same --overwrite, one key different. It must be quiet."
  cmd "kubectl label configmap app-config -n $NS --overwrite owner=payments-team && choudoufu plan"
  kc label configmap app-config -n "$NS" --overwrite owner=payments-team >/dev/null \
    || fail "$SCEN" "BREAK: could not write the decoy label"
  kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels}' | evidence
  BPLAN="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
    || fail "$SCEN" "BREAK: the plan failed: $(tail -20 <<< "$BPLAN")"
  { grep -E '^No changes|^Plan:' <<< "$BPLAN" | head -1 || true; } | evidence
  grep -q 'No changes.' <<< "$BPLAN" \
    || fail "$SCEN" "BREAK: a key the configuration never declared planned a change, so nothing in this scenario is evidence about DECLARED keys: $(grep -E '^Plan:|will be' <<< "$BPLAN" | head -3)"
  grep -q '"owner":"payments-team"' <<< "$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels}')" \
    || fail "$SCEN" "BREAK: the decoy label is not on the object, so the control wrote nothing and proves nothing"
  ( cd "$SMOKE_WORK/live" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  proof "caught. The same command against a key the configuration does not declare leaves the plan empty and the label on the object, so every plan the main arm requires is about declared keys and the server's own additions are still the server's."
  exit 0
fi

step "2. stock is the oracle - the same edit on a stock root, with a real state file"
explain \
  "One kubernetes_manifest, plain terraform, its own state file, its own" \
  "object. Edit the tier label in the configuration and read what stock" \
  "proposes. That answer is the target for step 3, and it is measured" \
  "here on this cluster rather than quoted."
command -v terraform >/dev/null 2>&1 \
  || fail "$SCEN" "the terraform binary is not on PATH - this step needs the stock oracle"
kc create namespace "$STOCK_NS" >/dev/null \
  || fail "$SCEN" "could not create the oracle's own namespace"
cat > "$SMOKE_WORK/stock/main.tf" <<TF
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}

resource "kubernetes_manifest" "cm" {
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "ConfigMap"
    "metadata" = {
      "labels" = {
        "tier" = "one"
      }
      "name"      = "stock-config"
      "namespace" = "$STOCK_NS"
    }
    "data" = {
      "greeting" = "hello"
    }
  }
}
TF
cmd "terraform apply -auto-approve   # plain stock, a real terraform.tfstate"
( cd "$SMOKE_WORK/stock" && terraform init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "$SCEN" "stock init failed"
STOCK_APPLY="$(cd "$SMOKE_WORK/stock" && terraform apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "stock apply failed: $(tail -10 <<< "$STOCK_APPLY")"
grep -qF "Apply complete! Resources: 1 added" <<< "$STOCK_APPLY" \
  || fail "$SCEN" "stock did not create its object: $(grep -E 'Apply complete' <<< "$STOCK_APPLY")"
[ -f "$SMOKE_WORK/stock/terraform.tfstate" ] || fail "$SCEN" "stock left no terraform.tfstate"
cmd "sed tier one -> two && terraform plan"
sed_i "$SMOKE_WORK/stock/main.tf" 's/"tier" = "one"/"tier" = "two"/'
STOCK_PLAN="$(cd "$SMOKE_WORK/stock" && terraform plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "stock plan failed: $(tail -10 <<< "$STOCK_PLAN")"
{ grep -E 'tier +=|^Plan:' <<< "$STOCK_PLAN" | head -3 || true; } | evidence
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$STOCK_PLAN" \
  || fail "$SCEN" "stock did not propose the label edit, so there is no oracle for step 3: $(grep -E '^Plan:|No changes' <<< "$STOCK_PLAN" | head -2)"
( cd "$SMOKE_WORK/stock" && terraform apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "$SCEN" "stock would not apply its own edit, so step 6 has no settled oracle to compare against"
proof "stock, with a state file holding what it last applied, proposes exactly one in-place update for a one-label edit. That is the answer step 3 has to match."

step "3. the same edit under a live block - one in-place update, and the apply writes it"
explain \
  "No state file on this side: the prior manifest is rebuilt from the" \
  "live object on every plan, and mirrorManifestComputedFields is what" \
  "puts the SERVER's value for each declared key into it. Before #1177" \
  "this plan read \"No changes.\", the apply reported 0 changed, and" \
  "kubectl still said tier=one."
cmd "sed tier one -> two && choudoufu plan"
live_config two
PLAN3="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the plan after the label edit failed: $(tail -20 <<< "$PLAN3")"
{ grep -E 'tier +=|^Plan:' <<< "$PLAN3" | head -3 || true; } | evidence
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$PLAN3" \
  || fail "$SCEN" "the label edit did not plan - this is #1177: $(grep -E '^Plan:|No changes' <<< "$PLAN3" | head -2)"
grep -qE 'tier +=.*"one".*->.*"two"' <<< "$PLAN3" \
  || fail "$SCEN" "the plan changes something, but not the label: $(grep -E 'will be|tier' <<< "$PLAN3" | head -5)"
cmd "choudoufu apply -auto-approve   # then: kubectl get configmap app-config -n $NS -o jsonpath='{.metadata.labels}'"
APPLY3="$(cd "$SMOKE_WORK/live" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the apply after the label edit failed: $(tail -20 <<< "$APPLY3")"
{ grep -E 'Apply complete!' <<< "$APPLY3" || true; } | evidence
LABELS3="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels}')"
echo "$LABELS3" | evidence
grep -q '"tier":"two"' <<< "$LABELS3" \
  || fail "$SCEN" "the apply reported success and the object still reads the old label: $LABELS3"
PLAN3B="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the replan failed: $(tail -20 <<< "$PLAN3B")"
{ grep -E '^No changes|^Plan:' <<< "$PLAN3B" | head -1 || true; } | evidence
grep -q 'No changes.' <<< "$PLAN3B" \
  || fail "$SCEN" "the replan is not empty, so the mirror churns: $(grep -E '^Plan:|will be' <<< "$PLAN3B" | head -3)"
proof "the same edit stock proposes, choudoufu proposes, applies and writes - and the next plan is empty, so the prior it rebuilds from the live object settles instead of churning."

step "4. the annotation half - #1177's own reproduction"
explain \
  "The provider's computed_fields default names metadata.annotations" \
  "beside metadata.labels, and on this provider a great deal of" \
  "Kubernetes semantics lives in annotations: ingress class," \
  "cert-manager.io/*, Prometheus scrape config. The issue's own" \
  "reproduction adds one annotation to a Namespace and finds kubectl" \
  "printing nothing after the apply."
cmd "add metadata.annotations.reviewed = \"yes\" && choudoufu apply -auto-approve"
live_config two '        "reviewed" = "yes"'
PLAN4="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the plan after adding the annotation failed: $(tail -20 <<< "$PLAN4")"
{ grep -E 'reviewed|^Plan:' <<< "$PLAN4" | head -3 || true; } | evidence
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$PLAN4" \
  || fail "$SCEN" "adding an annotation did not plan: $(grep -E '^Plan:|No changes' <<< "$PLAN4" | head -2)"
APPLY4="$(cd "$SMOKE_WORK/live" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the apply after adding the annotation failed: $(tail -20 <<< "$APPLY4")"
ANN4="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.annotations}')"
echo "${ANN4:-<nothing>}" | evidence
grep -q '"reviewed":"yes"' <<< "$ANN4" \
  || fail "$SCEN" "the apply reported success and the annotation is not on the object: ${ANN4:-<nothing>}"
cmd "change it to \"no\" && choudoufu plan"
live_config two '        "reviewed" = "no"'
PLAN4B="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the plan after changing the annotation failed: $(tail -20 <<< "$PLAN4B")"
{ grep -E 'reviewed|^Plan:' <<< "$PLAN4B" | head -3 || true; } | evidence
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$PLAN4B" \
  || fail "$SCEN" "changing an annotation did not plan: $(grep -E '^Plan:|No changes' <<< "$PLAN4B" | head -2)"
live_config two '        "reviewed" = "yes"'
( cd "$SMOKE_WORK/live" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "$SCEN" "could not put the annotation back"
proof "an annotation added and then changed each plan one in-place update and the apply writes them, which is the reproduction #1177 was filed on."

step "5. what the configuration never declared stays the server's"
explain \
  "computed_fields exists so that a label or an annotation the API" \
  "server or a controller writes does not churn the plan, and Kubernetes" \
  "writes plenty. The namespace this estate declares carries" \
  "kubernetes.io/metadata.name, written by the API server itself and" \
  "impossible to declare away. If the prior manifest mirrored the live" \
  "maps wholesale instead of the declared keys, every plan from here on" \
  "would propose deleting it - and the server would put it straight" \
  "back. Two undeclared keys are added by hand as well, one label and" \
  "one annotation, standing in for every controller that writes one."
cmd "kubectl get namespace $NS -o jsonpath='{.metadata.labels}'"
NSLABELS="$(kc get namespace "$NS" -o jsonpath='{.metadata.labels}')"
echo "$NSLABELS" | evidence
grep -q '"kubernetes.io/metadata.name"' <<< "$NSLABELS" \
  || fail "$SCEN" "the API server did not write kubernetes.io/metadata.name onto the namespace, so this step has nothing to measure: $NSLABELS"
cmd "kubectl label configmap app-config -n $NS owner=payments-team && kubectl annotate configmap app-config -n $NS scraped=true && choudoufu plan"
kc label configmap app-config -n "$NS" owner=payments-team >/dev/null \
  || fail "$SCEN" "could not write the undeclared label"
kc annotate configmap app-config -n "$NS" scraped=true >/dev/null \
  || fail "$SCEN" "could not write the undeclared annotation"
PLAN5="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the plan after the undeclared keys failed: $(tail -20 <<< "$PLAN5")"
{ grep -E '^No changes|^Plan:' <<< "$PLAN5" | head -1 || true; } | evidence
grep -q 'No changes.' <<< "$PLAN5" \
  || fail "$SCEN" "a key the configuration never declared churned the plan: $(grep -E '^Plan:|will be|owner|scraped|metadata.name' <<< "$PLAN5" | head -5)"
kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels}{"  "}{.metadata.annotations}' | evidence
proof "the server's own kubernetes.io/metadata.name, a hand-written label and a hand-written annotation are all still there and the plan is empty. The mirror reaches the keys the configuration declares and no others."

step "6. the one difference from stock, measured on both runners"
explain \
  "\"The configuration was edited\" and \"the live object drifted\" are" \
  "the same observation - configuration differs from live - unless you" \
  "have a last-applied value to tell them apart. A state file has one;" \
  "a stateless run does not. So making step 3 visible necessarily makes" \
  "this visible too: an out-of-band change to a key the configuration" \
  "DECLARES reads as a difference and the plan proposes writing the" \
  "configuration back, where stock's computed_fields takes the live" \
  "value and says nothing changed. The same kubectl command runs" \
  "against both objects here and both answers are printed."
cmd "kubectl label configmap app-config -n $NS --overwrite tier=zzz   # the DECLARED key, out of band"
kc label configmap app-config -n "$NS" --overwrite tier=zzz >/dev/null \
  || fail "$SCEN" "could not move the declared label out of band"
kc label configmap stock-config -n "$STOCK_NS" --overwrite tier=zzz >/dev/null \
  || fail "$SCEN" "could not move stock's declared label out of band"
STOCK_PLAN6="$(cd "$SMOKE_WORK/stock" && terraform plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "stock's plan after the out-of-band change failed: $(tail -10 <<< "$STOCK_PLAN6")"
PLAN6="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the plan after the out-of-band change failed: $(tail -20 <<< "$PLAN6")"
{
  echo "stock:      $(grep -E '^No changes|^Plan:' <<< "$STOCK_PLAN6" | head -1)"
  echo "choudoufu:  $(grep -E '^No changes|^Plan:' <<< "$PLAN6" | head -1)"
} | evidence
grep -q 'No changes.' <<< "$STOCK_PLAN6" \
  || fail "$SCEN" "stock proposed something for the out-of-band change, so the difference this step records is not the difference it says it is: $(grep -E '^Plan:|No changes' <<< "$STOCK_PLAN6" | head -2)"
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$PLAN6" \
  || fail "$SCEN" "choudoufu did not propose restoring the declared label: $(grep -E '^Plan:|No changes' <<< "$PLAN6" | head -2)"
grep -qE 'tier +=.*"zzz".*->.*"two"' <<< "$PLAN6" \
  || fail "$SCEN" "choudoufu plans something, but not the label restore: $(grep -E 'will be|tier' <<< "$PLAN6" | head -5)"
proof "stock says \"No changes.\" and choudoufu proposes restoring the declared label. That difference is the price of having no last-applied value and it is recorded in live/LIMITATIONS.md, not hidden: a saved plan's staleness check can see an out-of-band kubectl label here, and stock's cannot."

( cd "$SMOKE_WORK/live" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
( cd "$SMOKE_WORK/stock" && terraform destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
kc delete namespace "$STOCK_NS" --wait=false >/dev/null 2>&1 || true

echo "  What you watched: a label edited in the configuration proposed as one"
echo "  in-place update and written to the object, the same answer stock gave"
echo "  for the same edit with a state file behind it; an annotation added and"
echo "  changed doing the same; and the API server's own label, a controller's"
echo "  label and a controller's annotation left alone by every plan. The"
echo "  prior this fork rebuilds on each run carries the server's value for"
echo "  the keys the configuration names, and nothing else - which is what a"
echo "  state file's last-applied manifest says, except for a declared key"
echo "  someone moved by hand."
