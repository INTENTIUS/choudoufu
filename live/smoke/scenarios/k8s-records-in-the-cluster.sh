# k8s-records-in-the-cluster
# CLAIM 39 - Records live in the cluster: a Kubernetes-only estate keeps its records as Secrets under resourceVersion with no AWS in the environment, an apply killed with SIGKILL leaves no lock behind, and a role scoped to one records namespace cannot read another estate's records. ~8 min.
#
# GitHub issue #1392, under the #1398 ruling. Until this, a Kubernetes-only
# estate had two choices for its records: "local", which is one machine's
# disk, or "s3", which is an AWS account, a bucket and a role for a team
# that runs nothing on AWS. record_store "kubernetes" is the third: Secrets
# in a namespace, metadata.resourceVersion as the conditional write, and no
# lock and no Lease.
#
# Five steps, each measuring one of the things that would make the store a
# bad idea if it were not true.
#
#   1. The Store contract, against this cluster's own API server. The same
#      suite internal/live/staterecord holds the local and bucket stores to.
#   2. An estate with a Kubernetes provider, a Kubernetes record store and
#      NO AWS credentials in the environment at all - the variables unset,
#      the config files pointed at /dev/null, IMDS disabled - applies end to
#      end, and the record is a Secret any kubectl can see.
#   3. Claim 4's no-lock property on this store: an apply killed with
#      SIGKILL mid-flight, and the very next run carries on. Nothing in the
#      namespace is a Lease or anything else lock-shaped.
#   4. Read isolation. RBAC cannot condition on a label and admission never
#      sees a get, so the namespace is the boundary. The no-RBAC control
#      runs first and READS the other estate's records, so the refusal that
#      follows is the Role's doing and not an empty namespace.
#   5. Write isolation with no new policy: live/kubernetes/estate-boundary.yaml
#      already matches every object carrying tofu-estate, and every record
#      Secret carries it.
#
# BREAK=1 takes the two fences away and requires what they refused to go
# through: the plan role is given cluster-wide secret reads and must then
# read Bob's records, and the admission policy is removed and Bob's write
# into Alice's record Secret must then land.

SMOKE_WORK="$SMOKE_WORKROOT/k8s-records-in-the-cluster"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
W="$SMOKE_WORK"

cluster_up

kc() { kubectl --kubeconfig "$KUBECONFIG" "$@"; }

# no_aws runs a command with every way of reaching AWS taken out of the
# environment: the variables unset, both config files pointed at /dev/null
# and the instance metadata service disabled. A run that still succeeds
# made no AWS call, because there was nothing it could have made one with.
# A subshell body, not a braced one, so the unsets cannot leak back into the
# steps around it - and so `chdf`, which is a shell function, is still
# callable through it, which `env` would not be.
no_aws() (
  unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN \
        AWS_PROFILE AWS_REGION AWS_DEFAULT_REGION AWS_ENDPOINT_URL \
        AWS_ENDPOINT_URL_S3 AWS_ROLE_ARN AWS_WEB_IDENTITY_TOKEN_FILE
  export AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null
  export AWS_EC2_METADATA_DISABLED=true
  # Nothing here may reach AWS, and this is the check that the environment
  # really is empty of it rather than the assertion that it is.
  env | grep -E '^AWS_(ACCESS|SECRET|SESSION|PROFILE|REGION|DEFAULT_REGION|ROLE_ARN|WEB_IDENTITY|ENDPOINT)' \
    && fail "k8srec" "an AWS credential variable survived into the no-AWS run"
  "$@"
)

# versions writes the provider and live block for estate $1 with its record
# store in namespace $2. No AWS provider appears anywhere in this scenario.
versions() {
  cat > "$3/versions.tf" <<TFEOF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "$1"

    record_store "kubernetes" {
      namespace = "$2"
    }
  }

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}
TFEOF
}

RECORDS_NS="tofu-records-k8srec-alice"
BOB_NS="tofu-records-k8srec-bob"
APP="$W/app"
mkdir -p "$APP"

step "1. the Store contract, against this cluster's own API server"
explain \
  "The record store is an interface with one contract: a conditional" \
  "create, a conditional update, a conditional delete, and versions that" \
  "are opaque except that an empty one means no record. The local store" \
  "and the bucket store are held to it by the same suite. This runs that" \
  "suite against the kind cluster, so what is measured is the API" \
  "server's own optimistic concurrency and not a stand-in: client-go's" \
  "fake clientset assigns no resourceVersion at all, and every version" \
  "case here would pass vacuously against it."
kc create namespace "$RECORDS_NS" >/dev/null || fail "k8srec" "could not create the records namespace"
kc create namespace "$BOB_NS" >/dev/null || fail "k8srec" "could not create Bob's records namespace"
cmd "go test ./internal/live/staterecord -run TestKubernetesStore   # against the kind cluster"
CONF_OUT="$( cd "$ROOT" && CHOUDOUFU_K8S_RECORD_KUBECONFIG="$KUBECONFIG" CHOUDOUFU_K8S_RECORD_NAMESPACE="$RECORDS_NS" \
  go test ./internal/live/staterecord -run TestKubernetesStore -count=1 -v 2>&1 )" \
  || fail "k8srec" "the conformance suite failed against the cluster: $(grep -E '^\s+--- FAIL|FAIL' <<< "$CONF_OUT" | head -10)"
grep -c -- '--- PASS: TestKubernetesStoreConformance/' <<< "$CONF_OUT" | sed 's/^/conformance cases passed: /' | evidence
grep -q -- '--- SKIP: TestKubernetesStoreConformance' <<< "$CONF_OUT" \
  && fail "k8srec" "the conformance suite SKIPPED; a skip is not a pass and this step measured nothing"
CASES="$(grep -c -- '--- PASS: TestKubernetesStoreConformance/' <<< "$CONF_OUT")"
[ "$CASES" -ge 17 ] || fail "k8srec" "only $CASES conformance cases ran; the suite has 17 and a short run means cases were skipped"
grep -E -- '--- PASS: TestKubernetesStore(VersionIsTheResourceVersion|RefusesAMissingNamespaceByName)' <<< "$CONF_OUT" | evidence
proof "every case in the shared Store suite passes against a real API server, including the stale-version conflict and the absent-key answers, plus the two Kubernetes-specific ones: the version IS metadata.resourceVersion, and a namespace that does not exist is refused by name rather than read as an empty estate."

step "2. a Kubernetes-only estate applies with no AWS credentials in the environment"
explain \
  "The configuration has a kubernetes provider, a live block with" \
  "record_store \"kubernetes\", and no AWS provider. The run goes out with" \
  "every AWS variable unset, AWS_CONFIG_FILE and" \
  "AWS_SHARED_CREDENTIALS_FILE pointed at /dev/null and IMDS disabled, so" \
  "an AWS call would have nothing to make it with. terraform_data is a" \
  "record-backed type: its whole value lives in a record, so this run" \
  "cannot finish without the store working."
versions k8srec-alice "$RECORDS_NS" "$APP"
cat > "$APP/main.tf" <<'TF'
resource "kubernetes_namespace" "app" {
  metadata {
    name = "k8srec-app"
  }
}

resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = "k8srec-app"
  }
  data       = { greeting = "hello" }
  depends_on = [kubernetes_namespace.app]
}

resource "terraform_data" "effect" {
  input = "v1"
}
TF
cmd "env -u AWS_... AWS_CONFIG_FILE=/dev/null AWS_EC2_METADATA_DISABLED=true choudoufu apply -auto-approve"
( cd "$APP" && no_aws chdf init -input=false -no-color >/dev/null ) || fail "k8srec" "init failed"
A_OUT="$( cd "$APP" && no_aws chdf apply -auto-approve -input=false -no-color 2>&1 )" \
  || fail "k8srec" "the apply with no AWS credentials failed: $A_OUT"
grep -E 'Apply complete!' <<< "$A_OUT" | evidence
grep -qE 'Apply complete! Resources: 3 added' <<< "$A_OUT" \
  || fail "k8srec" "the apply did not report 3 added: $A_OUT"
grep -qiE 'aws|credential' <<< "$A_OUT" \
  && fail "k8srec" "the apply output mentions AWS or credentials: $A_OUT"
cmd "kubectl get secrets -n $RECORDS_NS -l tofu-estate=k8srec-alice"
REC_NAMES="$(kc get secrets -n "$RECORDS_NS" -l tofu-estate=k8srec-alice -o name 2>&1)" \
  || fail "k8srec" "could not list the record Secrets: $REC_NAMES"
echo "$REC_NAMES" | evidence
[ -n "$REC_NAMES" ] || fail "k8srec" "no record Secret carries tofu-estate=k8srec-alice; the estate wrote its records somewhere else, or nowhere"
grep -q 'secret/tofu-record-' <<< "$REC_NAMES" \
  || fail "k8srec" "a record Secret is not named tofu-record-<hash>: $REC_NAMES"
ONE="$(head -1 <<< "$REC_NAMES" | sed 's|^secret/||')"
cmd "kubectl get secret $ONE -n $RECORDS_NS -o jsonpath='{.metadata.annotations}'"
ANN="$(kc get secret "$ONE" -n "$RECORDS_NS" -o jsonpath='{.metadata.annotations}' 2>&1)"
echo "$ANN" | evidence
grep -q 'choudoufu.intentius.io/record-key' <<< "$ANN" \
  || fail "k8srec" "the record Secret carries no record-key annotation, so nothing says which record it is: $ANN"
# The apply is idempotent and the record is what makes it so: a second plan
# reads the record back and proposes nothing.
P_OUT="$( cd "$APP" && no_aws chdf plan -input=false -no-color 2>&1 )" || fail "k8srec" "the replan failed: $P_OUT"
grep -E 'No changes|Plan:' <<< "$P_OUT" | head -1 | evidence
grep -q 'No changes' <<< "$P_OUT" \
  || fail "k8srec" "the replan proposes changes, so the records it just wrote were not read back: $P_OUT"
proof "a Kubernetes-only estate applied and replanned empty with no way to reach AWS, and its records are Secrets in the cluster, named tofu-record-<sha256 of the key> with the key itself in an annotation."

step "3. an apply killed with SIGKILL, and the very next run carries on"
explain \
  "Claim 4's headline, on this store. A second resource takes a while to" \
  "create; the apply is killed with SIGKILL while it is in flight, so no" \
  "handler runs and nothing cleans up. The stock kubernetes backend takes" \
  "a coordination.k8s.io Lease per workspace, and a run that dies holding" \
  "one leaves it behind until somebody decides force-unlock is safe. This" \
  "store takes none, so there is nothing to leave behind."
rm -f "$W/slow-started" "$W/slow.pid"
cat >> "$APP/main.tf" <<TFEOF

resource "terraform_data" "slow" {
  input = "v1"

  provisioner "local-exec" {
    command = "echo \$\$ > '$W/slow.pid'; touch '$W/slow-started'; exec sleep 120"
  }
}
TFEOF
cmd "choudoufu apply -auto-approve &   # then: kill -9, mid-apply"
( cd "$APP" && no_aws "$TOFU" apply -auto-approve -input=false -no-color >"$W/killed.out" 2>&1 ) &
APPLY_PID=$!
for _ in $(seq 1 90); do [ -f "$W/slow-started" ] && break; sleep 1; done
[ -f "$W/slow-started" ] || { kill -9 "$APPLY_PID" 2>/dev/null; fail "k8srec" "the slow resource never started creating, so there was no apply in flight to kill: $(cat "$W/killed.out")"; }
kill -0 "$APPLY_PID" 2>/dev/null || fail "k8srec" "the apply had already exited before it could be killed: $(cat "$W/killed.out")"
kill -9 "$APPLY_PID"; wait "$APPLY_PID" 2>/dev/null && fail "k8srec" "the killed apply exited 0"
# SIGKILL orphans the provisioner's own process. It is this scenario's to end.
kill "$(cat "$W/slow.pid" 2>/dev/null)" 2>/dev/null || true
grep -q "Apply complete" "$W/killed.out" && fail "k8srec" "the apply completed; it was not killed in flight"
echo "killed with SIGKILL while terraform_data.slow was creating" | evidence
# The slow resource stays declared and stops being slow, so the next run is
# the ordinary "finish what was started" and not another two minutes of
# waiting. This is claim 4's own shape: the block is not removed.
sed -i.bak 's/exec sleep 120/exec sleep 1/' "$APP/main.tf" && rm -f "$APP/main.tf.bak"
grep -q 'exec sleep 1"' "$APP/main.tf" || fail "k8srec" "the slow provisioner was not shortened; the next run would wait two minutes"
cmd "kubectl get leases,secrets -n $RECORDS_NS   # nothing lock-shaped"
LOCKS="$(kc get leases -n "$RECORDS_NS" -o name 2>&1)"
echo "leases in the records namespace: ${LOCKS:-none}" | evidence
[ -z "$LOCKS" ] || fail "k8srec" "a Lease exists in the records namespace; this store takes no lock: $LOCKS"
kc get secrets -n "$RECORDS_NS" -o name | grep -i 'lock' && fail "k8srec" "an object in the records namespace is named like a lock"
cmd "choudoufu apply -auto-approve   # the very next run, nothing done in between"
N_OUT="$( cd "$APP" && no_aws chdf apply -auto-approve -input=false -no-color 2>&1 )" \
  || fail "k8srec" "the run after the killed one failed: $N_OUT"
grep -qiE "state lock|acquiring the lock|force-unlock" <<< "$N_OUT" \
  && fail "k8srec" "the run after the killed one talks about a lock: $N_OUT"
grep -E 'Apply complete!' <<< "$N_OUT" | head -1 | evidence
grep -q 'Apply complete!' <<< "$N_OUT" || fail "k8srec" "the run after the killed one did not complete: $N_OUT"
# And it converged, which is the other half of carrying on: the estate the
# killed run left half-built is whole again.
C_OUT="$( cd "$APP" && no_aws chdf plan -input=false -no-color 2>&1 )" || fail "k8srec" "the plan after the recovery run failed: $C_OUT"
grep -E 'No changes|Plan:' <<< "$C_OUT" | head -1 | evidence
grep -q 'No changes' <<< "$C_OUT" \
  || fail "k8srec" "the estate did not converge after the killed run: $C_OUT"
proof "the run after the killed one just worked and the estate converged, and there is no Lease and no lock-shaped object anywhere in the records namespace. Contention here settles at the API server's own resourceVersion, one record at a time."

step "4. a role scoped to one records namespace cannot read another estate's records"
explain \
  "RBAC cannot condition on a label, and admission is never consulted for" \
  "a get or a list, so a label cannot fence a READ. The namespace is what" \
  "can. Bob's estate writes its records into its own namespace; Alice's" \
  "plan identity is given the Role the docs will recommend - get, list," \
  "create, update and delete on secrets, in Alice's namespace only." \
  "The control comes first: with no Role bound at all but cluster-admin" \
  "credentials, Bob's records ARE readable, so the refusal below is the" \
  "Role's doing and not an empty namespace."
BOB="$W/bob"
mkdir -p "$BOB"
versions k8srec-bob "$BOB_NS" "$BOB"
cat > "$BOB/main.tf" <<'TF'
resource "terraform_data" "bobs_secret_value" {
  input = "bob-only"
}
TF
( cd "$BOB" && no_aws chdf init -input=false -no-color >/dev/null ) || fail "k8srec" "Bob's init failed"
B_OUT="$( cd "$BOB" && no_aws chdf apply -auto-approve -input=false -no-color 2>&1 )" \
  || fail "k8srec" "Bob's apply failed: $B_OUT"
grep -qE 'Apply complete! Resources: 1 added' <<< "$B_OUT" || fail "k8srec" "Bob's apply: $B_OUT"

cmd "kubectl get secrets -n $BOB_NS   # the CONTROL, as cluster-admin: Bob's records are readable"
CTRL="$(kc get secrets -n "$BOB_NS" -l tofu-estate=k8srec-bob -o name 2>&1)"
echo "$CTRL" | evidence
[ -n "$CTRL" ] || fail "k8srec" "the control read nothing: Bob wrote no records, so the refusal below would prove nothing"

# planner is a ServiceAccount with its own kubeconfig, bound to a Role that
# reaches Alice's records namespace and nothing else.
kc create serviceaccount planner -n default >/dev/null || fail "k8srec" "could not create the planner ServiceAccount"
kc create role records-rw -n "$RECORDS_NS" --verb=get,list,create,update,delete --resource=secrets >/dev/null \
  || fail "k8srec" "could not create the records Role"
kc create rolebinding planner-records -n "$RECORDS_NS" --role=records-rw --serviceaccount=default:planner >/dev/null \
  || fail "k8srec" "could not bind the records Role"
PLANNER_KC="$W/planner.kubeconfig"
TOK="$(kc create token planner -n default --duration=2h)" || fail "k8srec" "could not mint a token for the planner"
cp "$KUBECONFIG" "$PLANNER_KC"
kubectl --kubeconfig "$PLANNER_KC" config set-credentials planner --token="$TOK" >/dev/null
kubectl --kubeconfig "$PLANNER_KC" config set-context --current --user=planner >/dev/null

cmd "kubectl --as the planner get secrets -n $RECORDS_NS   # its own estate's records"
OWN="$(kubectl --kubeconfig "$PLANNER_KC" get secrets -n "$RECORDS_NS" -l tofu-estate=k8srec-alice -o name 2>&1)"
echo "$OWN" | evidence
grep -q 'secret/tofu-record-' <<< "$OWN" \
  || fail "k8srec" "the scoped role cannot read its OWN estate's records, so the refusal below would be a role that reads nothing at all: $OWN"

cmd "kubectl --as the planner get secrets -n $BOB_NS   # the other estate's"
if CROSS="$(kubectl --kubeconfig "$PLANNER_KC" get secrets -n "$BOB_NS" -o name 2>&1)"; then
  fail "k8srec" "the scoped role READ another estate's records: $CROSS"
fi
echo "$CROSS" | head -2 | evidence
grep -qiE 'forbidden|cannot list' <<< "$CROSS" \
  || fail "k8srec" "the cross-estate read failed for some reason other than a refusal: $CROSS"
proof "the same identity reads its own estate's records and is refused the other estate's, by name, from the API server's own authorizer. The namespace is the boundary, which is exactly what the docs have to say: one records namespace per estate, and a Role that names it."

step "5. the estate boundary fences a write to a record Secret with no new policy"
explain \
  "live/kubernetes/estate-boundary.yaml matches every object carrying a" \
  "tofu-estate label on CREATE, UPDATE and DELETE, and every record" \
  "Secret carries one. So the policy an estate already installs fences" \
  "writes to its records with nothing added: an identity bound to Bob's" \
  "estate and holding Secrets in Alice's namespace still cannot write" \
  "Alice's record objects."
kc apply -f "$ROOT/live/kubernetes/estate-boundary.yaml" >/dev/null \
  || fail "k8srec" "could not install the estate boundary policy"
# The API server has to observe the policy and type-check its CEL before it
# enforces anything, and a write sent in that window goes through. Claim 23
# waits for observedGeneration for the same reason.
for _ in $(seq 1 30); do
  OBS="$(kc get validatingadmissionpolicy choudoufu-estate-boundary -o jsonpath='{.status.observedGeneration}' 2>/dev/null || true)"
  [ -n "$OBS" ] && break; sleep 1
done
[ -n "$OBS" ] || fail "k8srec" "the API server never observed the estate boundary policy"
TC="$(kc get validatingadmissionpolicy choudoufu-estate-boundary -o jsonpath='{.status.typeChecking.expressionWarnings}')"
[ -z "$TC" ] || fail "k8srec" "the policy's CEL has type-check warnings: $TC"
sed -e "s/ESTATE/k8srec-bob/g" -e "s/PRINCIPAL_NAMESPACE/default/g" -e "s/PRINCIPAL/planner/g" \
  "$ROOT/live/kubernetes/estate-grant.yaml" | kc apply -f - >/dev/null \
  || fail "k8srec" "could not grant estate k8srec-bob to the planner"
TARGET="$(kc get secrets -n "$RECORDS_NS" -l tofu-estate=k8srec-alice -o name | head -1 | sed 's|^secret/||')"
[ -n "$TARGET" ] || fail "k8srec" "no record Secret of Alice's to write to"
# The write is an UPDATE and not a patch, on purpose. `kubectl annotate` and
# `kubectl label` both send a PATCH, and the Role the docs recommend - get,
# list, create, update, delete, which is what the store itself uses - does not
# carry patch. A patch here is refused by RBAC before admission is ever
# consulted, so it would read as the boundary holding while measuring nothing
# about it. That is exactly what the first version of this step did.
kc get secret "$TARGET" -n "$RECORDS_NS" -o json > "$W/target.json"
python3 - "$W/target.json" <<'EDIT'
import json, sys
path = sys.argv[1]
obj = json.load(open(path))
obj["metadata"].setdefault("annotations", {})["bob"] = "was-here"
for drop in ("creationTimestamp", "managedFields", "uid"):
    obj["metadata"].pop(drop, None)
json.dump(obj, open(path, "w"))
EDIT
# Observing the policy and enforcing it are not the same instant: the binding
# reaches the admission chain a moment later. A server-side dry run goes
# through the identical chain and changes nothing, so it is what waits the
# window out. If it never refuses, the fence is not in force and this step
# says so rather than measuring a write nobody was judging.
FENCED=""
for _ in $(seq 1 30); do
  if ! kubectl --kubeconfig "$PLANNER_KC" replace --dry-run=server -f "$W/target.json" >/dev/null 2>&1; then
    FENCED=yes; break
  fi
  sleep 1
done
[ -n "$FENCED" ] || fail "k8srec" "30s after the policy was observed, a dry-run cross-estate write on a record Secret was still accepted; the fence is not in force and the assertion below would measure nothing"
cmd "kubectl --as the planner (bound to estate k8srec-bob) replace -f <Alice's record Secret>"
if WROTE="$(kubectl --kubeconfig "$PLANNER_KC" replace -f "$W/target.json" 2>&1)"; then
  fail "k8srec" "an identity bound to estate k8srec-bob wrote estate k8srec-alice's record object: $WROTE"
fi
echo "$WROTE" | head -3 | evidence
grep -q 'not bound to that estate' <<< "$WROTE" \
  || fail "k8srec" "the write was refused by something other than the estate boundary policy, whose message says \"not bound to that estate\"; an RBAC refusal here would measure nothing about the boundary: $WROTE"
proof "the record objects are inside the estate fence the cluster already has, because they carry the same label every other object in the estate carries. No policy was added for the record store."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - take the two fences away, and what they refused must go through"
  explain \
    "What this arm proves: step 4's refusal is the Role's scope and step" \
    "5's is the admission policy, not something else about the cluster." \
    "The planner is given cluster-wide secret reads, and Bob's records" \
    "must then be readable; the policy is removed, and Bob's write into" \
    "Alice's record Secret must then land. If either still fails, the" \
    "step above was passing for the wrong reason."
  # Not the built-in "view" ClusterRole: Kubernetes leaves Secrets out of it
  # on purpose, so binding it would widen nothing that matters here and the
  # control would fail for a reason that is not the one under test.
  kc create clusterrole secrets-everywhere --verb=get,list --resource=secrets >/dev/null \
    || fail "k8srec" "BREAK: could not create the cluster-wide secrets role"
  kc create clusterrolebinding planner-reads-everything --clusterrole=secrets-everywhere --serviceaccount=default:planner >/dev/null \
    || fail "k8srec" "BREAK: could not widen the planner's reads"
  for _ in $(seq 1 30); do
    kubectl --kubeconfig "$PLANNER_KC" get secrets -n "$BOB_NS" >/dev/null 2>&1 && break
    sleep 1
  done
  cmd "kubectl --as the planner, now cluster-wide, get secrets -n $BOB_NS"
  CROSS="$(kubectl --kubeconfig "$PLANNER_KC" get secrets -n "$BOB_NS" -l tofu-estate=k8srec-bob -o name 2>&1)" \
    || fail "k8srec" "BREAK: with cluster-wide reads, the planner still could not list Bob's records: $CROSS"
  echo "$CROSS" | evidence
  grep -q 'secret/tofu-record-' <<< "$CROSS" \
    || fail "k8srec" "BREAK: the widened read returned no record Secret, so step 4's refusal was never about the Role: $CROSS"

  kc delete validatingadmissionpolicybinding choudoufu-estate-boundary >/dev/null \
    || fail "k8srec" "BREAK: could not remove the binding"
  kc delete validatingadmissionpolicy choudoufu-estate-boundary >/dev/null \
    || fail "k8srec" "BREAK: could not remove the policy"
  # Removing a policy propagates the same way installing one does, so the
  # dry run waits the window out here too.
  for _ in $(seq 1 30); do
    kubectl --kubeconfig "$PLANNER_KC" replace --dry-run=server -f "$W/target.json" >/dev/null 2>&1 && break
    sleep 1
  done
  cmd "kubectl --as the planner replace -f <Alice's record Secret>   # policy gone"
  WROTE="$(kubectl --kubeconfig "$PLANNER_KC" replace -f "$W/target.json" 2>&1)" \
    || fail "k8srec" "BREAK: with the policy gone, the cross-estate write was still refused: $WROTE"
  echo "$WROTE" | evidence
  kc get secret "$TARGET" -n "$RECORDS_NS" -o jsonpath='{.metadata.annotations.bob}' | grep -q 'was-here' \
    || fail "k8srec" "BREAK: the write reported success and did not land"
  proof "both refusals came from the fences they were attributed to: widening the Role makes the cross-estate read succeed, and removing the policy makes the cross-estate write land."
fi
