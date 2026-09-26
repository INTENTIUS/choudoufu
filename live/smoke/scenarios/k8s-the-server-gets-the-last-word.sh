# k8s-the-server-gets-the-last-word
# CLAIM 26 - Admission runs after the plan and the server decides what is stored: a fail-closed webhook's rejection is reported in the API server's own words with nothing changed and the approved plan file still applying unchanged once the webhook answers again, a mutation to a declared field reads as the same perpetual drift stock reads and the estate keeps its marker, and a mutation that strips the marker on the way in is named by the run that made it - the create warns that the marker it sent is not on the object the server stored, and the adopting update that follows fails rather than reporting a change nothing kept. ~4 min.
#
# The second fault of #1110. Everything a plan says is a statement about
# what the API server will accept, made before it was asked. Admission is
# the gap: a ValidatingWebhookConfiguration can refuse the write the plan
# approved, and a MutatingAdmissionPolicy or MutatingWebhookConfiguration
# can store something other than what was sent. The three questions #1110
# asks are the three parts here, and the third is the one nothing in the
# lane covered.
#
# On the transport: two of the three faults below are injected with an
# admission POLICY rather than a webhook server, because the API server's
# in-process admission chain produces the identical effect on the stored
# object with no certificate, no image and no pod to go wrong - the same
# choice claim 23 and claim 24 already make. The one thing a policy cannot
# reproduce is a webhook that is not there, so the first fault uses a real
# ValidatingWebhookConfiguration with failurePolicy: Fail pointing at a
# Service that does not exist. That is the webhook fault operators
# actually hit, it needs no server either, and it is the API server's own
# "failed calling webhook" error and not a stand-in for it.
#
# What the third part measures, and it is the boundary case: a policy
# webhook enforcing a label scheme will strip or rewrite the labels it does
# not recognise, and tofu-estate is a label it does not recognise. The
# object is created and the marker never lands, so the estate would
# otherwise believe it owns an object no marker says is its.
#
# #1192 was that nothing in the write path checked whether the marker it
# sent was the marker the server stored, and it is closed here. No read-back
# was needed: the object the provider returns from ApplyResourceChange IS
# the stored object, and core was already computing the exact difference
# ('.metadata[0].labels: element "tofu-estate" has vanished') and logging it
# at WARN, because objchange.AssertObjectCompatible is deliberately
# tolerated for a legacy-SDK provider. Steps 6 and 7 now assert the two
# halves of the answer, and they are different on purpose: a create that
# loses its marker warns, because the object really was added and the count
# is true about it, while an adopting update that loses its marker is an
# error, because its whole content was the marker and nothing it wrote
# lasted - proved in step 7 by a resourceVersion that does not move across
# two runs. An exit code is what a nightly gate reads, so the second one
# could not stay a warning.
#
# BREAK=1 installs the same stripping policy against a DECOY label instead
# of tofu-estate - identical machinery, identical namespace, one key
# different - and requires the opposite outcome: the decoy stripped (so the
# policy is provably in the chain and provably mutating), the marker landed,
# no "Ownership marker was not stored" anywhere in the run, live-ls listing
# the object as this estate's, and the second apply not wedging. Without
# that control the whole third part would read the same if choudoufu simply
# never wrote a label, and its two new assertions would read the same if the
# diagnostic fired unconditionally.

SCEN="k8s-the-server-gets-the-last-word"

SMOKE_WORK="$SMOKE_WORKROOT/$SCEN"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

NS="smoke-k8s"
ESTATE="smoke-k8s"

# versions_block writes the terraform block. $1 is the declared_untagged
# verb, or empty for the compatible default (which warns and refuses to
# touch an object carrying no marker).
versions_block() {
  local policy=""
  [ -n "${1:-}" ] && policy="    policy {
      declared_untagged = \"$1\"
    }
"
  cat > "$SMOKE_WORK/versions.tf" <<TF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "$ESTATE"
$policy  }

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}
TF
}

# config_block writes the one declared object. $1 is data.greeting, $2 is
# an optional extra label line.
config_block() {
  local labels=""
  [ -n "${2:-}" ] && labels="    labels    = { $2 }
"
  cat > "$SMOKE_WORK/main.tf" <<TF
resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = "$NS"
$labels  }

  data = {
    greeting = "$1"
  }
}
TF
}

versions_block
config_block hello

cluster_up

# Every choudoufu call below that runs while a step has a webhook or a
# policy in the admission chain is chdf_bounded and not chdf (#1457): those
# are the calls whose answer depends on the API server keeping to a timeout,
# and one that never returns fails here by name inside CHDF_TIMEOUT_SECS.

# probe_admission runs a throwaway ConfigMap through the whole admission
# chain and prints the labels the server actually stored, or REJECTED if
# admission refused it. wait_admission polls it, so every step below waits
# for the policy it just installed to be live instead of sleeping a guess -
# a registered webhook or policy takes a second or two to reach the
# admission plugins and a fixed sleep is how these scenarios flake.
#
# A request that times out fails like a refused one and is reported as
# REJECTED with kubectl's own message, which is why step 1 waits for the
# webhook's words and not for the bare verdict (#1457).
probe_admission() {
  local out
  kc delete configmap admission-probe -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  if ! out="$(kc create configmap admission-probe -n "$NS" --from-literal=a=b 2>&1)"; then
    echo "REJECTED on create: $out"; return
  fi
  # --overwrite is load-bearing: a policy that has already rewritten one of
  # these keys makes a plain kubectl label exit non-zero with a usage error,
  # and the probe would report REJECTED for something admission never saw.
  if ! out="$(kc label configmap admission-probe -n "$NS" --overwrite tofu-estate=probe smoke-decoy=present owner=payments-team 2>&1)"; then
    echo "REJECTED on label: $out"; return
  fi
  # A go-template range over a map walks it in sorted key order, so the
  # probe's output is stable and a step can match on it exactly.
  kc get configmap admission-probe -n "$NS" \
    -o go-template='{{range $k, $v := .metadata.labels}}{{$k}}={{$v}} {{end}}'
  echo
}

# wait_admission polls the probe until the stored labels match $1 and, if
# $2 is not empty, do NOT match $2 - absence is half of what these steps
# wait for, and no single pattern can say it. $3 describes the state for
# the failure line.
#
# The bound is a deadline on the clock, not a number of tries (#1457). Thirty
# tries bounded nothing while one try could take for ever, and with a request
# timeout on every call thirty slow tries would still add up to a quarter of
# an hour.
#
# The probe runs right after a fail-closed webhook or policy goes into the
# chain, which is when a request is most likely to stall, so every request in
# here gets a shorter timeout than kc's default. The deletes that tidy the
# probe away are `|| true` for the same reason: under `set -e` one of them
# timing out would end the run before the fail line below is printed.
wait_admission() {
  local want="$1" absent="$2" what="$3" out="" deadline
  local KC_REQUEST_TIMEOUT="${PROBE_REQUEST_TIMEOUT:-10s}"
  deadline=$(( $(date +%s) + ${ADMISSION_WAIT_SECS:-120} ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    out="$(probe_admission)"
    if grep -qE "$want" <<< "$out"; then
      if [ -z "$absent" ] || ! grep -qE "$absent" <<< "$out"; then
        kc delete configmap admission-probe -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
        return 0
      fi
    fi
    sleep 2
  done
  kc delete configmap admission-probe -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  fail "$SCEN" "admission was not in the state this step needs after ${ADMISSION_WAIT_SECS:-120}s ($what); last probe: ${out:-<nothing>}"
}

# The rejecting webhook: a real ValidatingWebhookConfiguration, fail-closed,
# whose endpoint is a Service nobody created. No server, no certificate, and
# the API server's own error - which is the shape of every policy-engine
# outage an operator has ever been paged for.
cat > "$SMOKE_WORK/validating-webhook.yaml" <<YAML
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: smoke-policy-gate
webhooks:
  - name: gate.smoke.choudoufu.io
    admissionReviewVersions: ["v1"]
    sideEffects: None
    failurePolicy: Fail
    timeoutSeconds: 5
    namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: $NS
    rules:
      - apiGroups:   [""]
        apiVersions: ["v1"]
        operations:  ["CREATE", "UPDATE"]
        resources:   ["configmaps"]
        scope:       "Namespaced"
    clientConfig:
      service:
        name: policy-gate
        namespace: $NS
        path: /validate
        port: 443
YAML

# The rewriting policy: forces every ConfigMap in the namespace to carry
# owner=platform-team, whatever the writer asked for. ApplyConfiguration
# merges the labels map, so the estate marker is untouched: this is drift,
# not a change of ownership.
cat > "$SMOKE_WORK/rewriter.yaml" <<YAML
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: owner-label-rewriter
spec:
  matchConstraints:
    resourceRules:
      - apiGroups:   [""]
        apiVersions: ["v1"]
        operations:  ["CREATE", "UPDATE"]
        resources:   ["configmaps"]
    namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: $NS
  failurePolicy: Fail
  reinvocationPolicy: IfNeeded
  mutations:
    - patchType: ApplyConfiguration
      applyConfiguration:
        expression: >
          Object{ metadata: Object.metadata{ labels: {"owner": "platform-team"} } }
---
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicyBinding
metadata:
  name: owner-label-rewriter
spec:
  policyName: owner-label-rewriter
YAML

# The stripping policy, written once as a template so the main arm and the
# BREAK arm differ in exactly one string: the label key removed.
stripper_yaml() { # $1 = label key to strip, $2 = policy name
  cat > "$SMOKE_WORK/stripper.yaml" <<YAML
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: $2
spec:
  matchConstraints:
    resourceRules:
      - apiGroups:   [""]
        apiVersions: ["v1"]
        operations:  ["CREATE", "UPDATE"]
        resources:   ["configmaps"]
    namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: $NS
  failurePolicy: Fail
  reinvocationPolicy: IfNeeded
  mutations:
    - patchType: JSONPatch
      jsonPatch:
        expression: >
          has(object.metadata.labels) && '$1' in object.metadata.labels
            ? [JSONPatch{op: "remove", path: "/metadata/labels/$1"}]
            : []
---
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicyBinding
metadata:
  name: $2
spec:
  policyName: $2
YAML
}

step "0. a namespace, and one ConfigMap the estate owns"
explain \
  "The namespace is made with kubectl so nothing in the run waits on a" \
  "namespace delete. The estate is the one ConfigMap inside it, created" \
  "by choudoufu and carrying tofu-estate=$ESTATE - the only thing that" \
  "says the object is this estate's."
cmd "kubectl create namespace $NS && choudoufu init && choudoufu apply -auto-approve"
kc create namespace "$NS" >/dev/null || fail "$SCEN" "could not create the namespace"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "$SCEN" "init failed"
APPLY1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "apply failed: $APPLY1"
grep -E 'Apply complete!' <<< "$APPLY1" | evidence
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY1" \
  || fail "$SCEN" "the first apply did not report one added: $APPLY1"
kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.name} labels={.metadata.labels}{"\n"}' | evidence
[ "$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}')" = "$ESTATE" ] \
  || fail "$SCEN" "the marker did not land on the baseline object; nothing below would mean anything"
proof "one object, marked. With no admission policy in the way the marker choudoufu sends is the marker the server stores."

step "1. a plan is saved and approved - then a fail-closed webhook appears"
explain \
  "choudoufu plan -out writes the artifact a reviewer approves. Between" \
  "that approval and the apply, a cluster admin installs a validating" \
  "webhook with failurePolicy: Fail whose endpoint does not answer -" \
  "a policy engine mid-rollout, or one whose pods are gone. Every" \
  "CREATE and UPDATE of a ConfigMap in this namespace now fails closed." \
  "Nothing in the plan could have seen this coming: the plan was made" \
  "before the webhook existed, and for a built-in type the provider" \
  "sends no dry run the server could refuse."
cmd "choudoufu plan -out=saved.tfplan   # then: kubectl apply -f validating-webhook.yaml"
config_block goodbye
PLAN_SAVED="$(cd "$SMOKE_WORK" && chdf plan -out=saved.tfplan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "plan -out failed: $PLAN_SAVED"
grep -E '^Plan:' <<< "$PLAN_SAVED" | evidence
grep -qE 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$PLAN_SAVED" \
  || fail "$SCEN" "the saved plan is not the one change this step needs: $PLAN_SAVED"
[ -f "$SMOKE_WORK/saved.tfplan" ] || fail "$SCEN" "plan -out wrote no artifact"
kc apply -f "$SMOKE_WORK/validating-webhook.yaml" >/dev/null \
  || fail "$SCEN" "could not install the validating webhook"
wait_admission 'REJECTED.*failed calling webhook' '' "the fail-closed webhook refusing writes to ConfigMaps in $NS"
kc get validatingwebhookconfiguration smoke-policy-gate -o jsonpath='{.webhooks[0].name} failurePolicy={.webhooks[0].failurePolicy} endpoint={.webhooks[0].clientConfig.service.name}{"\n"}' | evidence
proof "a real webhook is in the admission chain, fail-closed, with nothing behind it. The approved plan is on disk and was written before any of this."

step "2. apply the approved plan - the write is refused in the server's own words"
explain \
  "The apply reaches the API server and admission refuses it. What" \
  "matters is what the run tells you: the server's message verbatim," \
  "naming the webhook, the endpoint it could not reach and the timeout," \
  "rather than a generic failure. Nothing changed in the cluster, and" \
  "the object keeps its marker - a refused write is not a partial one."
cmd "choudoufu apply saved.tfplan"
APPLY_RC=0
APPLY2="$(cd "$SMOKE_WORK" && chdf_bounded apply -input=false -no-color saved.tfplan 2>&1)" || APPLY_RC=$?
grep -E '^Error: ' <<< "$APPLY2" | head -1 | evidence
[ "$APPLY_RC" != "0" ] \
  || fail "$SCEN" "the apply exited 0 under a fail-closed webhook; the write cannot have been refused: $APPLY2"
grep -q 'failed calling webhook "gate.smoke.choudoufu.io"' <<< "$APPLY2" \
  || fail "$SCEN" "the error does not quote the server's own webhook message: $(grep -E '^Error' <<< "$APPLY2" | head -2)"
grep -q 'service "policy-gate" not found' <<< "$APPLY2" \
  || fail "$SCEN" "the error does not name the endpoint the server could not reach: $(grep -E '^Error' <<< "$APPLY2" | head -2)"
LIVE_GREETING="$(kc get configmap app-config -n "$NS" -o jsonpath='{.data.greeting}')"
LIVE_MARKER="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}')"
echo "greeting=$LIVE_GREETING  tofu-estate=$LIVE_MARKER" | evidence
[ "$LIVE_GREETING" = "hello" ] \
  || fail "$SCEN" "the refused apply changed the object anyway (greeting=$LIVE_GREETING)"
[ "$LIVE_MARKER" = "$ESTATE" ] \
  || fail "$SCEN" "the refused apply cost the object its marker (tofu-estate=$LIVE_MARKER)"
[ -f "$SMOKE_WORK/saved.tfplan" ] \
  || fail "$SCEN" "the refused apply consumed the plan artifact; the approval would have to be sought again"
proof "refused, in the API server's words, with the object untouched and the approved artifact still on disk. A rejection after approval is a rejection, not a half-applied estate."

step "3. the webhook answers again - the same approved plan applies unchanged"
explain \
  "The policy engine comes back. Nothing is re-planned and nothing is" \
  "re-approved: the same file from step 1 is applied, and it lands. That" \
  "is the whole recovery - a re-run of the artifact that was already" \
  "reviewed, with no state surgery and no import."
cmd "kubectl delete -f validating-webhook.yaml && choudoufu apply saved.tfplan"
kc delete -f "$SMOKE_WORK/validating-webhook.yaml" >/dev/null 2>&1 || true
wait_admission 'tofu-estate=probe' '' "the webhook gone and writes accepted again"
APPLY3="$(cd "$SMOKE_WORK" && chdf apply -input=false -no-color saved.tfplan 2>&1)" \
  || fail "$SCEN" "the retried apply of the approved plan failed: $APPLY3"
grep -E 'Apply complete!' <<< "$APPLY3" | evidence
grep -qE 'Apply complete! Resources: 0 added, 1 changed, 0 destroyed' <<< "$APPLY3" \
  || fail "$SCEN" "the retried apply did not report the one change: $APPLY3"
kc get configmap app-config -n "$NS" -o jsonpath='greeting={.data.greeting} tofu-estate={.metadata.labels.tofu-estate}{"\n"}' | evidence
[ "$(kc get configmap app-config -n "$NS" -o jsonpath='{.data.greeting}')" = "goodbye" ] \
  || fail "$SCEN" "the retried apply did not write the value the approved plan carried"
proof "the same approved plan, applied a second time, does exactly what it said. The webhook's rejection cost a re-run and nothing else."

step "4. a mutating policy rewrites a declared field - the same perpetual drift stock shows"
explain \
  "Now the server stores something other than what was sent. The" \
  "configuration declares owner=payments-team; a MutatingAdmissionPolicy" \
  "overwrites it with owner=platform-team on every write. The apply" \
  "succeeds - nothing refused anything - and the next plan reads the" \
  "cluster and finds the field it just wrote already changed. It will" \
  "propose the same one change on every run until somebody settles the" \
  "argument, which is exactly what stock does, measured below. The" \
  "marker is not part of this: an ApplyConfiguration merges the labels" \
  "map, so tofu-estate survives and the object stays this estate's."
cmd "kubectl apply -f rewriter.yaml && choudoufu apply -auto-approve && choudoufu plan   # twice"
kc apply -f "$SMOKE_WORK/rewriter.yaml" >/dev/null \
  || fail "$SCEN" "could not install the rewriting policy (it needs a cluster serving admissionregistration.k8s.io/v1 MutatingAdmissionPolicy)"
wait_admission 'owner=platform-team' '' "the rewriting policy overwriting owner on every write"
config_block goodbye 'owner = "payments-team"'
APPLY4="$(cd "$SMOKE_WORK" && chdf_bounded apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the apply under the rewriting policy failed: $APPLY4"
grep -E 'Apply complete!' <<< "$APPLY4" | evidence
kc get configmap app-config -n "$NS" -o jsonpath='labels={.metadata.labels}{"\n"}' | evidence
[ "$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels.owner}')" = "platform-team" ] \
  || fail "$SCEN" "the policy did not rewrite owner; there is no mutation to measure"
[ "$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}')" = "$ESTATE" ] \
  || fail "$SCEN" "the rewriting policy took the marker off; this step is supposed to leave ownership alone"
rm -f "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
for n in 1 2; do
  PLAN_D="$(cd "$SMOKE_WORK" && chdf_bounded plan -input=false -no-color 2>&1)" \
    || fail "$SCEN" "drift plan $n failed: $PLAN_D"
  grep -E '^Plan:' <<< "$PLAN_D" | sed "s/^/plan $n: /" | evidence
  grep -qE 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$PLAN_D" \
    || fail "$SCEN" "drift plan $n is not the same one change: $PLAN_D"
  grep -q '"platform-team" -> "payments-team"' <<< "$PLAN_D" \
    || fail "$SCEN" "drift plan $n does not name the rewritten value: $(grep -E 'owner' <<< "$PLAN_D" | head -3)"
done
proof "the same one change on every plan, naming the value the policy wrote and the value the configuration asks for - read from the cluster, with the cache deleted before the first of the two. The object is still this estate's throughout."

step "5. stock, on the same fault, says the same thing"
explain \
  "The oracle. A plain terraform root with no live block, its own state" \
  "file, one ConfigMap declaring the same label under the same policy." \
  "If choudoufu's answer above were a fork-specific misreading this is" \
  "where it would show."
if ! command -v terraform >/dev/null 2>&1; then
  fail "$SCEN" "the terraform binary is not on PATH - this step needs the stock oracle for the day-2 comparison"
fi
mkdir -p "$SMOKE_WORK/stock"
cat > "$SMOKE_WORK/stock/main.tf" <<TF
terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}

resource "kubernetes_config_map" "s" {
  metadata {
    name      = "stock-config"
    namespace = "$NS"
    labels    = { owner = "payments-team" }
  }

  data = {
    greeting = "hello"
  }
}
TF
cmd "terraform apply -auto-approve && terraform plan   # plain stock, its own state file"
( cd "$SMOKE_WORK/stock" && terraform init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "$SCEN" "stock init failed"
STOCK_APPLY="$(cd "$SMOKE_WORK/stock" && terraform apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "stock apply failed: $(tail -5 <<< "$STOCK_APPLY")"
grep -E 'Apply complete!' <<< "$STOCK_APPLY" | evidence
STOCK_PLAN="$(cd "$SMOKE_WORK/stock" && terraform plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "stock plan failed: $(tail -5 <<< "$STOCK_PLAN")"
grep -E '^Plan:' <<< "$STOCK_PLAN" | evidence
grep -qE 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$STOCK_PLAN" \
  || fail "$SCEN" "stock does not show the same perpetual drift; the comparison in step 4 is wrong: $STOCK_PLAN"
( cd "$SMOKE_WORK/stock" && terraform destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
proof "stock: applied, then 0 to add, 1 to change, 0 to destroy, forever. A mutating webhook over a declared field is drift, and choudoufu reads it exactly as stock does."
kc delete -f "$SMOKE_WORK/rewriter.yaml" >/dev/null 2>&1 || true
wait_admission 'owner=payments-team' '' "the rewriting policy gone"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip a DECOY label instead of the marker; everything must then work"
  explain \
    "You asked for proof the assertions below can fail. Steps 6 and 7" \
    "rest on one manufactured fact: a policy is removing tofu-estate on" \
    "the way in. This arm installs that policy against smoke-decoy" \
    "instead - same kind, same namespace, same JSONPatch, one key" \
    "different - and requires the opposite outcome. The decoy must be" \
    "stripped, so the policy is provably in the chain and provably" \
    "mutating. The marker must land, the run must NOT say the marker" \
    "was not stored, live-ls must list the object as this estate's, and" \
    "the second apply must not wedge. If any of that failed here, the" \
    "main arm would be measuring choudoufu failing to write a label, or" \
    "a diagnostic that fires whatever the server does, and the stripper" \
    "would be scenery."
  cmd "kubectl apply -f stripper.yaml   # strips smoke-decoy, not tofu-estate"
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
    || fail "$SCEN" "BREAK: could not clear the estate before the control"
  stripper_yaml smoke-decoy decoy-scheme-enforcer
  kc apply -f "$SMOKE_WORK/stripper.yaml" >/dev/null \
    || fail "$SCEN" "BREAK: could not install the decoy stripper"
  wait_admission 'tofu-estate=probe' 'smoke-decoy' "the decoy stripper removing smoke-decoy and leaving tofu-estate"
  config_block hello 'smoke-decoy = "present"'
  BAPPLY="$(cd "$SMOKE_WORK" && chdf_bounded apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "$SCEN" "BREAK: apply failed: $BAPPLY"
  grep -E 'Apply complete!' <<< "$BAPPLY" | evidence
  BLABELS="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels}')"
  echo "$BLABELS" | evidence
  if grep -q 'smoke-decoy' <<< "$BLABELS"; then
    fail "$SCEN" "BREAK: the decoy label was not stripped, so the policy is not in the admission chain and the main arm's stripper would prove nothing: $BLABELS"
  fi
  grep -q "\"tofu-estate\":\"$ESTATE\"" <<< "$BLABELS" \
    || fail "$SCEN" "BREAK: with only the decoy stripped the marker STILL did not land - the main arm would be measuring a broken marker write, not a stripped one: $BLABELS"
  # The control for #1192's new assertions: with the marker landing, the
  # diagnostic steps 6 and 7 require must be absent. Without this the two
  # greps over there would pass just as well against a run that printed
  # "Ownership marker was not stored" unconditionally.
  if grep -q 'Ownership marker was not stored' <<< "$BAPPLY"; then
    fail "$SCEN" "BREAK: the marker landed and the run still said it was not stored, so steps 6 and 7 are asserting scenery: $BAPPLY"
  fi
  BLS="$(cd "$SMOKE_WORK" && chdf_bounded live-ls -estate="$ESTATE" -no-color . 2>&1)" \
    || fail "$SCEN" "BREAK: live-ls failed: $BLS"
  grep -E 'carry its marker' <<< "$BLS" | evidence
  grep -q "Estate \"$ESTATE\": 1 resource(s) carry its marker" <<< "$BLS" \
    || fail "$SCEN" "BREAK: live-ls does not list the object as this estate's: $BLS"
  BAPPLY2_RC=0
  BAPPLY2="$(cd "$SMOKE_WORK" && chdf_bounded apply -auto-approve -input=false -no-color 2>&1)" || BAPPLY2_RC=$?
  grep -E 'Apply complete!|^Error: ' <<< "$BAPPLY2" | head -1 | evidence
  [ "$BAPPLY2_RC" = "0" ] \
    || fail "$SCEN" "BREAK: the second apply wedged even with the marker intact: $(grep -E '^Error' <<< "$BAPPLY2" | head -2)"
  if grep -q 'already exists' <<< "$BAPPLY2"; then
    fail "$SCEN" "BREAK: the second apply hit the name collision the main arm measures, with the marker on the object: $BAPPLY2"
  fi
  ( cd "$SMOKE_WORK" && chdf_bounded apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  kc delete -f "$SMOKE_WORK/stripper.yaml" >/dev/null 2>&1 || true
  kc delete namespace "$NS" --wait=false >/dev/null 2>&1 || true
  proof "caught. With the identical policy pointed at a decoy key the decoy is gone, the marker landed, live-ls lists the object as this estate's and the second apply is a no-op - so the wedge the main arm measures is the stripped marker's doing and not a label choudoufu never wrote."
  exit 0
fi

step "6. a mutating policy strips tofu-estate on the way in - the create is reported, and so is the marker that did not land"
explain \
  "The boundary case, and the one a real cluster will do to you: a" \
  "policy webhook enforcing a label scheme removes the labels it does" \
  "not recognise, and tofu-estate is one of them. choudoufu sends the" \
  "marker on the create and the server stores the object without it." \
  "The object really was added, so \"1 added\" is true - but it is not" \
  "the whole truth, and #1192 was that the run said nothing else. It" \
  "does now: the object the provider hands back after ApplyResourceChange" \
  "is the stored object, and the marker is not in it. No extra read is" \
  "issued to learn that; the value was already in the process."
cmd "kubectl apply -f stripper.yaml && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "$SCEN" "could not clear the estate before the stripping step"
if kc get configmap app-config -n "$NS" >/dev/null 2>&1; then
  fail "$SCEN" "app-config survived the destroy; this step needs a clean create"
fi
stripper_yaml tofu-estate label-scheme-enforcer
kc apply -f "$SMOKE_WORK/stripper.yaml" >/dev/null \
  || fail "$SCEN" "could not install the stripping policy"
wait_admission 'smoke-decoy=present' 'tofu-estate' "the stripping policy removing tofu-estate and leaving every other label"
config_block hello
APPLY6="$(cd "$SMOKE_WORK" && chdf_bounded apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the apply under the stripping policy failed: $APPLY6"
grep -E 'Creation complete|Apply complete!|^Warning: Ownership marker was not stored' <<< "$APPLY6" | evidence
grep -qE 'Apply complete! Resources: 1 added, 0 changed, 0 destroyed' <<< "$APPLY6" \
  || fail "$SCEN" "the run did not report the create; the object is real and the count about it is true, so this line must not change: $APPLY6"
kc get configmap app-config -n "$NS" >/dev/null 2>&1 \
  || fail "$SCEN" "the object was not created at all; the policy is rejecting rather than mutating"
STORED="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels}')"
echo "stored labels: ${STORED:-<none>}" | evidence
case "$STORED" in
  *tofu-estate*) fail "$SCEN" "the marker survived the stripping policy; there is no fault to measure: $STORED" ;;
esac
# #1192's first half. The create is honest about the object and must now
# also be honest about the marker, at warning severity: the object exists,
# the run really did add it, and the next plan is loud on its own.
grep -q 'Warning: Ownership marker was not stored' <<< "$APPLY6" \
  || fail "$SCEN" "the create said nothing about the marker the server discarded (#1192): $APPLY6"
grep -A6 'Ownership marker was not stored' <<< "$APPLY6" | grep -qE 'tofu-estate: sent "'"$ESTATE"'", not stored' \
  || fail "$SCEN" "the diagnostic does not name the marker it sent and what came back: $(grep -A8 'Ownership marker was not stored' <<< "$APPLY6")"
if grep -q 'Error: Ownership marker was not stored' <<< "$APPLY6"; then
  fail "$SCEN" "the create was refused; a created object must not be an error - the object exists and something has to say so: $APPLY6"
fi
proof "\"1 added\", says the run, and the object really was added. What it also says now is that the tofu-estate label it sent is not on the object the server stored - read off the value the provider already returned, with no extra request. That was #1192's first half."

step "7. what the next run says, and what the remedy it names is worth"
explain \
  "The plan does not pretend. It reads the cluster, finds an object at" \
  "the name this block declares that carries no marker, and stops with" \
  "an error - so the estate is not silently wrong, and no plan proposes" \
  "the create the API server would answer with 409 (#1546). But it is" \
  "wrong about whose object it is: this is the estate's own object," \
  "created seconds ago by this estate, read back as somebody else's." \
  "The apply stops at the same refusal. Both remedies the" \
  "error names - write the label with kubectl, or set" \
  "declared_untagged = adopt - are writes, and the policy strips them" \
  "too. The kubectl relabel vanishes. The adopting run used to report" \
  "\"0 added, 1 changed, 0 destroyed\" and exit 0 over a label that was" \
  "never written, on every run forever, which is the half of #1192" \
  "nothing else in the run would ever have corrected. An adopting" \
  "update carries nothing but the marker, so when the marker does not" \
  "land the write accomplished nothing at all: it is now an error, and" \
  "the run prints no completion line. Nothing is stranded by that - the" \
  "object is exactly as it was before the run, and step 8 adopts it in" \
  "one apply once the policy allows the label."
cmd "choudoufu plan && choudoufu apply -auto-approve && kubectl label ... && (declared_untagged = \"adopt\") choudoufu apply -auto-approve"
PLAN7_RC=0
PLAN7="$(cd "$SMOKE_WORK" && chdf_bounded plan -input=false -no-color 2>&1)" || PLAN7_RC=$?
PLAN7_FLAT="$(tr '\n' ' ' <<< "$PLAN7" | tr -s ' ')"
grep -E '^Plan:|^Error: ' <<< "$PLAN7" | head -2 | evidence
echo "plan exit: $PLAN7_RC" | evidence
[ "$PLAN7_RC" = "1" ] \
  || fail "$SCEN" "the plan after the stripped create exited $PLAN7_RC, want 1: an unlabelled object read at the declared name must stop the plan (#1546): $PLAN7"
grep -q 'Error: Unlabelled live object holds the declared name' <<< "$PLAN7" \
  || fail "$SCEN" "the plan does not refuse the unmarked object at the declared name by name: $PLAN7"
grep -q 'carries no tofu-estate label' <<< "$PLAN7_FLAT" \
  || fail "$SCEN" "the refusal does not name the missing marker: $(grep -A6 'holds the declared name' <<< "$PLAN7" | head -8)"
grep -q 'policy { declared_untagged = "adopt" }' <<< "$PLAN7_FLAT" \
  || fail "$SCEN" "the refusal does not name the setting that adopts the object: $PLAN7"
if grep -qE '^Plan:' <<< "$PLAN7"; then
  fail "$SCEN" "a plan was produced alongside the refusal, proposing a create the API server answers with 409: $(grep -E '^Plan:' <<< "$PLAN7")"
fi
LS7="$(cd "$SMOKE_WORK" && chdf_bounded live-ls -estate="$ESTATE" -no-color . 2>&1)" \
  || fail "$SCEN" "live-ls failed: $LS7"
grep -E 'carry its marker' <<< "$LS7" | evidence
grep -q "Estate \"$ESTATE\": 0 resource(s) carry its marker" <<< "$LS7" \
  || fail "$SCEN" "live-ls does not report the estate empty; the marker must have landed after all: $LS7"
WEDGE_RC=0
WEDGE="$(cd "$SMOKE_WORK" && chdf_bounded apply -auto-approve -input=false -no-color 2>&1)" || WEDGE_RC=$?
grep -E '^Error: ' <<< "$WEDGE" | head -1 | evidence
[ "$WEDGE_RC" != "0" ] \
  || fail "$SCEN" "the apply exited 0; it was supposed to stop at the unmarked object holding the name: $WEDGE"
grep -q 'Error: Unlabelled live object holds the declared name' <<< "$WEDGE" \
  || fail "$SCEN" "the apply did not stop at the refusal: $(grep -E '^Error' <<< "$WEDGE" | head -2)"
if grep -q 'configmaps "app-config" already exists' <<< "$WEDGE"; then
  fail "$SCEN" "the apply reached the API server and was refused there; the plan was supposed to stop it first (#1546): $WEDGE"
fi
kc label configmap app-config -n "$NS" "tofu-estate=$ESTATE" --overwrite >/dev/null 2>&1 || true
HAND="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}')"
echo "after kubectl label: tofu-estate=${HAND:-<none>}" | evidence
[ -z "$HAND" ] \
  || fail "$SCEN" "the by-hand relabel survived the policy; the remedy the refusal names would work and this step is wrong: $HAND"
versions_block adopt
# resourceVersion is the API server's own answer to "did this write change
# the stored object": it is set to the etcd revision of the object's last
# write and does not move when a write stores something byte-identical. The
# first adopting run is allowed to move it - the provider's update sends a
# labels map where the stripped create left none, and the policy removing
# the marker from it still leaves an empty map behind. What must not move is
# the second one: that is the "on every run, for ever" part of #1192, and it
# is the difference between a loop that converges on nothing and one that is
# actually writing something each time.
RV_BEFORE="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.resourceVersion}')"
declare -a RVS=()
for n in 1 2; do
  ADOPT_RC=0
  ADOPT="$(cd "$SMOKE_WORK" && chdf_bounded apply -auto-approve -input=false -no-color 2>&1)" || ADOPT_RC=$?
  grep -E 'Apply complete!|^Error: ' <<< "$ADOPT" | head -1 | sed "s/^/adopt run $n: /" | evidence
  # #1192's second half, and the one an exit code reads. An adopting
  # update whose whole content is the marker, applied against a server
  # that discards the marker, changed nothing - so the run must not exit
  # 0, and must not print a completion line counting a change.
  [ "$ADOPT_RC" != "0" ] \
    || fail "$SCEN" "adopt run $n exited 0 over a marker the server did not store (#1192): $ADOPT"
  grep -q 'Error: Ownership marker was not stored' <<< "$ADOPT" \
    || fail "$SCEN" "adopt run $n failed for some other reason than the unstored marker: $(grep -E '^Error' <<< "$ADOPT" | head -2)"
  if grep -qE 'Apply complete! Resources: 0 added, 1 changed, 0 destroyed' <<< "$ADOPT"; then
    fail "$SCEN" "adopt run $n still reports one change over a label that was never written (#1192): $ADOPT"
  fi
  AFTER="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}')"
  [ -z "$AFTER" ] \
    || fail "$SCEN" "adopt run $n actually wrote the marker under the stripping policy: tofu-estate=$AFTER"
  RVS+=("$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.resourceVersion}')")
done
echo "resourceVersion: $RV_BEFORE before, ${RVS[0]} after adopt run 1, ${RVS[1]} after adopt run 2" | evidence
[ "${RVS[0]}" = "${RVS[1]}" ] \
  || fail "$SCEN" "the second adopting run changed the stored object; it was supposed to be the same write landing on nothing, for ever: ${RVS[0]} -> ${RVS[1]}"
kc get configmap app-config -n "$NS" -o jsonpath='labels={.metadata.labels}{"\n"}' | evidence
proof "the plan is honest that no marker is there and refuses to treat the object as the estate's, stopping with exit 1 rather than proposing a create the server would answer with 409. The adopting run is now honest too: it names the marker the server did not store and exits non-zero, with no completion line. resourceVersion ${RVS[0]} after the first adopting run and ${RVS[1]} after the second, so the run repeats a write the server keeps nothing of - and reporting \"0 added, 1 changed, 0 destroyed\" and exit 0 over that, on every run forever, was #1192."

step "8. the policy is lifted - the adoption lands and the estate is whole"
explain \
  "The platform team adds tofu-estate to the labels their scheme allows." \
  "Nothing else changes: the same adopting run, the same object, and the" \
  "marker sticks. No import, no state edit - the recovery is a re-run," \
  "the same as every other fault in this lane."
cmd "kubectl delete -f stripper.yaml && choudoufu apply -auto-approve && choudoufu plan"
kc delete -f "$SMOKE_WORK/stripper.yaml" >/dev/null 2>&1 || true
wait_admission 'tofu-estate=probe' '' "the stripping policy gone"
FIX="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the adopting apply with the policy lifted failed: $FIX"
grep -E 'Apply complete!' <<< "$FIX" | evidence
kc get configmap app-config -n "$NS" -o jsonpath='labels={.metadata.labels}{"\n"}' | evidence
[ "$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}')" = "$ESTATE" ] \
  || fail "$SCEN" "the marker still did not land with the policy gone"
FIXPLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the plan after the adoption failed: $FIXPLAN"
grep -E '^No changes|^Plan:' <<< "$FIXPLAN" | head -1 | evidence
grep -q 'No changes.' <<< "$FIXPLAN" \
  || fail "$SCEN" "the plan after the adoption is not empty: $FIXPLAN"
LS8="$(cd "$SMOKE_WORK" && chdf live-ls -estate="$ESTATE" -no-color . 2>&1)" \
  || fail "$SCEN" "live-ls after the adoption failed: $LS8"
grep -E 'carry its marker' <<< "$LS8" | evidence
grep -q "Estate \"$ESTATE\": 1 resource(s) carry its marker" <<< "$LS8" \
  || fail "$SCEN" "live-ls does not list the adopted object: $LS8"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
LEFT="$(kc get configmaps -n "$NS" -l "tofu-estate=$ESTATE" -o name | tr '\n' ' ')"
echo "left: ${LEFT:-none}" | evidence
[ -z "$LEFT" ] || fail "$SCEN" "objects carrying the estate label survived the destroy: $LEFT"
kc delete namespace "$NS" --wait=false >/dev/null 2>&1 || true
proof "one apply later the object is marked, the plan is empty and live-ls lists it. Nothing needed repairing but the policy."

echo "  What you watched: three things admission can do to a write the plan"
echo "  already approved. A fail-closed webhook refused it, and the run said"
echo "  so in the server's own words with the approved artifact still on disk"
echo "  and the object untouched. A mutating policy rewrote a declared field,"
echo "  and both choudoufu and stock proposed the same change forever. A"
echo "  mutating policy removed the estate marker, and the run that created"
echo "  the object said so - naming the marker it sent and what came back,"
echo "  off the value the provider had already returned. The adopting run"
echo "  that follows writes nothing that lasts, and no longer reports a"
echo "  change: it fails, with the object left exactly as it was, and one"
echo "  apply adopts it the moment the label scheme allows the marker."
