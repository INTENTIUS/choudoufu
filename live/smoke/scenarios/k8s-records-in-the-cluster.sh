# k8s-records-in-the-cluster
# CLAIM 39 - Records live in the cluster: a Kubernetes-only estate keeps its records as Secrets under resourceVersion with no AWS in the environment, two writers held on the wire with one resourceVersion between them settle with one winner and one named conflict, an apply killed with SIGKILL leaves no lock behind, a role scoped to one records namespace cannot read another estate's records, and the store checks that namespace, its RBAC scope, encryption at rest and the estate boundary once, on first contact, before it writes a record. ~14 min.
#
# GitHub issue #1392, under the #1398 ruling. Until this, a Kubernetes-only
# estate had two choices for its records: "local", which is one machine's
# disk, or "s3", which is an AWS account, a bucket and a role for a team
# that runs nothing on AWS. record_store "kubernetes" is the third: Secrets
# in a namespace, metadata.resourceVersion as the conditional write, and no
# lock and no Lease.
#
# Ten steps, each measuring one of the things that would make the store a
# bad idea if it were not true. Steps 1 to 5 are the store (#1392); steps 6
# to 9 are what it checks about the cluster before it writes a record
# (#1393), which is the bucket contract's shape sized for a cluster; step 10
# is claim 32 on this store (#1441), which needs the cluster steps 1 to 9
# already stood up and so comes last rather than beside the other store
# steps.
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
#   6. The cluster contract runs on an estate's first contact with the store
#      and not on every plan, measured with the API server's own request
#      counter. What a scoped identity cannot read it says, by name, and
#      does not report as a pass.
#   7. Each of the four assertions made to fail and read back by name with
#      `choudoufu live-cluster`, which asks the same questions without
#      running a plan.
#   8. The same assertion stopping an apply: a Role one verb short of what
#      the store uses is refused at first contact, and the refusal leaves
#      the store as it found it.
#   9. #1370 on this store: an identity with get and list on the record
#      Secrets plans, writes nothing, and is told by name what it lacks for
#      an apply.
#  10. Claim 32 on this store (#1441): two writers, one record, both holding
#      one resourceVersion and both parked on the wire until the other has
#      arrived. Six update-vs-update rounds and six create-vs-create rounds,
#      each releasing the parked requests in an order this step picks, and
#      each requiring one winner, one VersionConflictError naming both
#      versions, and no trace of the loser's payload in the record.
#
# BREAK=1 takes the three fences away and requires what they refused to go
# through: the plan role is given cluster-wide secret reads and must then
# read Bob's records, the admission policy is removed and Bob's write into
# Alice's record Secret must then land, and the scoped identity step 6's
# contract passed is widened to read secrets cluster-wide, after which the
# same first contact must be refused on read_isolation. It adds a fourth
# control for step 10: each write becomes the stock backend's read-then-update
# (client.go:86) and the same twelve rounds must then end with both writes
# landed and nothing named.

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
#
# The waiver is real and is this cluster's, not a convenience. Steps 2 to 5
# run as kind's cluster-admin, and three of the cluster contract's four
# assertions (#1393) are genuinely false for that identity here. A
# cluster-admin can read every records namespace in the cluster, so there is
# no read isolation from it. kind's API server carries no
# --encryption-provider-config, so its Secrets are not encrypted at rest.
# And the estate boundary policy is not installed until step 5, which is
# where this scenario measures it properly. Each is named, and each is said
# out loud on every run below - that is what a waiver costs. Steps 6 to 9
# measure the same four assertions under the scoped identity the docs
# recommend, with no waiver at all.
versions() {
  cat > "$3/versions.tf" <<TFEOF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "$1"

    record_store "kubernetes" {
      namespace      = "$2"
      allow_insecure = ["read_isolation", "encryption_at_rest", "estate_boundary"]
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

### The cluster contract (#1393). Steps 1 to 5 measured the store. These
### measure what the store checks about the cluster before it writes a record.

CAROL_NS="tofu-records-k8srec-carol"
CAROL="$W/carol"
CAROL_KC="$W/carol.kubeconfig"

# scoped_identity <serviceaccount> <namespace> <estate> <kubeconfig> <verbs>
# is the arrangement the docs recommend: a ServiceAccount with a Role on
# secrets in one records namespace, bound to one estate, and nothing else.
scoped_identity() {
  local sa="$1" ns="$2" estate="$3" out="$4" verbs="$5" tok
  kc create serviceaccount "$sa" -n default >/dev/null || fail "k8srec" "could not create the $sa ServiceAccount"
  kc create role "$sa-records" -n "$ns" --verb="$verbs" --resource=secrets >/dev/null \
    || fail "k8srec" "could not create $sa's Role in $ns"
  kc create rolebinding "$sa-records" -n "$ns" --role="$sa-records" --serviceaccount="default:$sa" >/dev/null \
    || fail "k8srec" "could not bind $sa's Role"
  sed -e "s/ESTATE/$estate/g" -e "s/PRINCIPAL_NAMESPACE/default/g" -e "s/PRINCIPAL/$sa/g" \
    "$ROOT/live/kubernetes/estate-grant.yaml" | kc apply -f - >/dev/null \
    || fail "k8srec" "could not grant estate $estate to $sa"
  tok="$(kc create token "$sa" -n default --duration=2h)" || fail "k8srec" "could not mint a token for $sa"
  cp "$KUBECONFIG" "$out"
  kubectl --kubeconfig "$out" config set-credentials "$sa" --token="$tok" >/dev/null
  kubectl --kubeconfig "$out" config set-context --current --user="$sa" >/dev/null
}

# as_identity <kubeconfig> <command...> runs choudoufu under one identity's
# kubeconfig. KUBE_CONFIG_PATH is what the provider and the record store's
# own connection loader read.
as_identity() (
  local conf="$1"; shift
  export KUBECONFIG="$conf" KUBE_CONFIG_PATH="$conf"
  no_aws "$@"
)

# ssar_count is the API server's own counter of SelfSubjectAccessReview
# requests. kind runs no audit log, and this metric is the API server's
# record of the same thing: one line per handled request, summed over every
# label combination. It is read as cluster-admin, which the counted runs are
# not, so reading it cannot move it.
ssar_count() {
  local raw
  # `|| true` and a separate awk, not one pipeline: this scenario runs under
  # set -euo pipefail, so a pipeline whose first stage fails ends the run
  # with no verdict line at all.
  raw="$(kc get --raw /metrics 2>/dev/null || true)"
  awk -F' ' '/^apiserver_request_total\{.*resource="selfsubjectaccessreviews"/ {s+=$2} END {printf "%d\n", s}' <<< "$raw"
}

step "6. the cluster contract runs on an estate's first contact with the cluster, and not on every plan"
explain \
  "Before it writes a record, the store asks four things about the cluster:" \
  "that this identity can do to Secrets in the records namespace what the" \
  "store will ask, that it cannot read another estate's records, that the" \
  "API server is started with an encryption configuration, and that the" \
  "estate boundary policy is in force. The permission questions go to the" \
  "API server's own authorizer" \
  "as SelfSubjectAccessReviews - never by attempting a write, because the" \
  "only thing there is to write in that namespace is a record." \
  "" \
  "They are facts about the cluster and do not change between two plans, so" \
  "they are asked once: on the estate's FIRST contact with the store, which" \
  "is the one run that created the sentinel. The API server's own request" \
  "counter is what measures that."
kc create namespace "$CAROL_NS" >/dev/null || fail "k8srec" "could not create Carol's records namespace"
mkdir -p "$CAROL"
scoped_identity carol "$CAROL_NS" k8srec-carol "$CAROL_KC" get,list,create,update,delete
# No allow_insecure at all: the scoped identity satisfies what it can be
# asked, and is warned about what it cannot read.
cat > "$CAROL/versions.tf" <<TFEOF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "k8srec-carol"

    record_store "kubernetes" {
      namespace = "$CAROL_NS"
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
cat > "$CAROL/main.tf" <<'TF'
resource "terraform_data" "carols_value" {
  input = "carol-only"
}
TF
( cd "$CAROL" && as_identity "$CAROL_KC" chdf init -input=false -no-color >/dev/null ) || fail "k8srec" "Carol's init failed"
cmd "choudoufu apply   # as a ServiceAccount with secrets in one namespace and nothing else"
BEFORE="$(ssar_count)"
C_OUT="$( cd "$CAROL" && as_identity "$CAROL_KC" chdf apply -auto-approve -input=false -no-color 2>&1 )" \
  || fail "k8srec" "the first contact under the recommended Role was refused: $C_OUT"
FIRST="$(ssar_count)"
{ grep -E 'Apply complete!' <<< "$C_OUT" || true; } | head -1 | evidence
echo "SelfSubjectAccessReviews on first contact: $((FIRST-BEFORE))" | evidence
[ "$((FIRST-BEFORE))" -gt 0 ] \
  || fail "k8srec" "first contact asked the authorizer nothing, so the contract did not run and the count below would measure nothing"
# What a scoped identity cannot read, it says, by name, rather than passing.
{ grep -E 'could not be checked' <<< "$C_OUT" || true; } | evidence
# Three, not two, since #1448: a Role scoped to one namespace cannot list the
# cluster's namespaces either, so whether another estate keeps records in one
# it can reach is a question it cannot ask, and an unasked question is not a
# pass.
for setting in read_isolation encryption_at_rest estate_boundary; do
  grep -q "cluster's $setting could not be checked" <<< "$C_OUT" \
    || fail "k8srec" "a Role that cannot read $setting did not say so: $C_OUT"
done
grep -q "not readable from here, not checked" <<< "$C_OUT" \
  || fail "k8srec" "the warning does not use the words the report uses: $C_OUT"
cmd "choudoufu plan   # twice more, and the counter must not move"
for _ in 1 2; do
  ( cd "$CAROL" && as_identity "$CAROL_KC" chdf plan -input=false -no-color >/dev/null 2>&1 ) \
    || fail "k8srec" "a plan after first contact failed"
done
AFTER="$(ssar_count)"
echo "SelfSubjectAccessReviews across two further plans: $((AFTER-FIRST))" | evidence
[ "$((AFTER-FIRST))" -eq 0 ] \
  || fail "k8srec" "the contract ran again on a plan: $((AFTER-FIRST)) more SelfSubjectAccessReviews across two plans, and it is supposed to run once, on first contact"
proof "the estate's first contact asked the authorizer $((FIRST-BEFORE)) questions and the two plans after it asked none. The three properties this scoped identity cannot read - read_isolation, encryption_at_rest and estate_boundary - are warned about by name on every run, and none of them is reported as a pass."

step "7. each assertion refuses by name, on this cluster, for its own reason"
explain \
  "A check nobody has seen fail is not a check. Each of the four is made" \
  "to fail here and read back by name, with choudoufu live-cluster, which" \
  "asks the same four questions without running a plan and without" \
  "writing anything. kind supplies two of the failures by itself: its API" \
  "server carries no --encryption-provider-config, and a cluster-admin can" \
  "read every records namespace there is."
cmd "choudoufu live-cluster -namespace=$CAROL_NS   # as cluster-admin"
ADMIN_OUT="$( cd "$ROOT" && no_aws chdf live-cluster -namespace="$CAROL_NS" -no-color 2>&1 )" && ADMIN_RC=0 || ADMIN_RC=$?
{ grep -E '^  (read_isolation|encryption_at_rest|estate_boundary|namespace_access)' <<< "$ADMIN_OUT" || true; } | evidence
[ "$ADMIN_RC" != "0" ] || fail "k8srec" "live-cluster exited 0 on a cluster that fails two of its four assertions: $ADMIN_OUT"
grep -qE '^  read_isolation +FAIL .*another estate.s records namespace: tofu-records-' <<< "$ADMIN_OUT" \
  || fail "k8srec" "read_isolation did not refuse a cluster-admin who can read the other estates' records namespaces: $ADMIN_OUT"
grep -qE '^  encryption_at_rest +FAIL .*no --encryption-provider-config' <<< "$ADMIN_OUT" \
  || fail "k8srec" "encryption_at_rest did not refuse a kind cluster, whose API server carries no encryption configuration: $ADMIN_OUT"
grep -qE '^  estate_boundary +OK' <<< "$ADMIN_OUT" \
  || fail "k8srec" "estate_boundary did not pass although step 5 installed the policy and measured it refusing a write: $ADMIN_OUT"

# The policy being in force is half of estate_boundary. The other half is
# whether the identity holds `use` on its estate, which is what the policy's
# own CEL asks the authorizer for (#1448, B6). A report given no estate says
# it did not ask; named one, it asks, and a cluster-admin holds every estate
# the way the account root does on AWS.
grep -q 'no estate was named' <<< "$ADMIN_OUT" \
  || fail "k8srec" "a report with no estate named did not say that the grant half went unasked: $ADMIN_OUT"
cmd "choudoufu live-cluster -namespace=$CAROL_NS -estate=k8srec-carol   # name the estate and the grant is asked too"
GRANTED="$( cd "$ROOT" && no_aws chdf live-cluster -namespace="$CAROL_NS" -estate=k8srec-carol -no-color 2>&1 )" || true
{ grep -E '^  estate_boundary' <<< "$GRANTED" || true; } | cut -c1-200 | evidence
grep -qE '^  estate_boundary +OK .*use. on estates.choudoufu.intentius.io/k8srec-carol' <<< "$GRANTED" \
  || fail "k8srec" "naming the estate did not make the report ask the authorizer for the grant the boundary policy reads: $GRANTED"

cmd "kubectl delete validatingadmissionpolicybinding choudoufu-estate-boundary   # then ask again"
kc delete validatingadmissionpolicybinding choudoufu-estate-boundary >/dev/null \
  || fail "k8srec" "could not remove the binding"
UNBOUND="$( cd "$ROOT" && no_aws chdf live-cluster -namespace="$CAROL_NS" -no-color 2>&1 )" || true
{ grep -E '^  estate_boundary' <<< "$UNBOUND" || true; } | evidence
grep -qE '^  estate_boundary +FAIL .*inert' <<< "$UNBOUND" \
  || fail "k8srec" "a policy with no binding was not refused; it evaluates nothing and refuses nothing: $UNBOUND"
kc apply -f "$ROOT/live/kubernetes/estate-boundary.yaml" >/dev/null || fail "k8srec" "could not put the binding back"

cmd "choudoufu live-cluster -namespace=tofu-records-k8srec-nobody   # a namespace that does not exist"
ABSENT="$( cd "$ROOT" && no_aws chdf live-cluster -namespace=tofu-records-k8srec-nobody -no-color 2>&1 )" || true
{ grep -E '^  namespace_access' <<< "$ABSENT" || true; } | cut -c1-160 | evidence
grep -qE '^  namespace_access +FAIL .*does not exist' <<< "$ABSENT" \
  || fail "k8srec" "an absent records namespace was not refused: $ABSENT"
grep -q 'kubectl create namespace tofu-records-k8srec-nobody' <<< "$ABSENT" \
  || fail "k8srec" "the refusal does not carry the kubectl line the store's own NamespaceMissingError carries, so the contract and the store disagree about what to tell an operator: $ABSENT"
proof "each assertion was made to fail and each refusal named itself: read_isolation named the other estate's namespace, encryption_at_rest named the missing API server flag, estate_boundary named the binding it needs, and an absent namespace came back in the store's own words. estate_boundary also says which half it asked: with no estate named it says the grant went unasked, and with one it asks the authorizer for the same `use` the policy's CEL reads."

step "8. the fourth refusal is the run's, not just the report's: a Role short one verb is refused at first contact"
explain \
  "The report above reads the cluster. This is the same assertion stopping" \
  "an apply before it writes anything. Dan's Role has four of the five" \
  "verbs the store uses - no update - so his sentinel write goes through," \
  "which makes his run a first contact, and the contract then refuses it by" \
  "name. The sentinel is taken back out on the way, so the next run is a" \
  "first contact again and refuses again rather than proceeding against a" \
  "cluster the first run refused."
DAN_NS="tofu-records-k8srec-dan"
DAN="$W/dan"
DAN_KC="$W/dan.kubeconfig"
kc create namespace "$DAN_NS" >/dev/null || fail "k8srec" "could not create Dan's records namespace"
mkdir -p "$DAN"
scoped_identity dan "$DAN_NS" k8srec-dan "$DAN_KC" get,list,create,delete
sed -e "s/k8srec-carol/k8srec-dan/g" -e "s|$CAROL_NS|$DAN_NS|g" "$CAROL/versions.tf" > "$DAN/versions.tf"
cp "$CAROL/main.tf" "$DAN/main.tf"
( cd "$DAN" && as_identity "$DAN_KC" chdf init -input=false -no-color >/dev/null ) || fail "k8srec" "Dan's init failed"
cmd "choudoufu apply   # as a Role with get, list, create and delete, and no update"
if D_OUT="$( cd "$DAN" && as_identity "$DAN_KC" chdf apply -auto-approve -input=false -no-color 2>&1 )"; then
  fail "k8srec" "an apply under a Role that cannot update a record Secret was allowed to start: $D_OUT"
fi
{ grep -E 'namespace_access|may not update' <<< "$D_OUT" || true; } | head -3 | evidence
grep -q 'fails its namespace_access assertion' <<< "$D_OUT" \
  || fail "k8srec" "the refusal does not name the assertion: $D_OUT"
grep -q 'may not update secrets' <<< "$D_OUT" \
  || fail "k8srec" "the refusal does not name the verb that is missing, so nobody could act on it: $D_OUT"
cmd "kubectl get secrets -n $DAN_NS   # the refused first contact left nothing behind"
LEFT="$(kc get secrets -n "$DAN_NS" -o name 2>&1)"
echo "secrets in Dan's records namespace after the refusal: ${LEFT:-none}" | evidence
[ -z "$LEFT" ] || fail "k8srec" "the refused first contact left $LEFT behind; the next run would not be a first contact and would proceed against the cluster this one refused"
if D2="$( cd "$DAN" && as_identity "$DAN_KC" chdf apply -auto-approve -input=false -no-color 2>&1 )"; then
  fail "k8srec" "the run after a refused first contact was allowed through: $D2"
fi
grep -q 'fails its namespace_access assertion' <<< "$D2" \
  || fail "k8srec" "the second run was refused for a different reason: $D2"
proof "an apply whose identity is one verb short of what the store uses is refused before it writes a record, by name, naming the verb; and the refusal leaves the store as it found it, so the next run is refused the same way instead of proceeding."

step "9. a plan identity needs get and list on the record Secrets and nothing more"
explain \
  "GitHub issue #1370 asked this of the bucket and #1393 asks it of the" \
  "cluster: a CI plan job is given an identity that may read the estate's" \
  "records and nothing else. A plan writes no record. The one write on its" \
  "path is the provisioning sentinel, and once a writing run has left that" \
  "behind, a denial of it is carried past - the sentinel's presence is the" \
  "proof issue #693 wanted, and nothing about this run being unable to" \
  "repeat it makes the store less sound. What is measured is the plan's" \
  "verdict AND that no record Secret's resourceVersion moved."
PLAN_KC="$W/carolplan.kubeconfig"
kc create serviceaccount carolplan -n default >/dev/null || fail "k8srec" "could not create the carolplan ServiceAccount"
kc create role carol-records-ro -n "$CAROL_NS" --verb=get,list --resource=secrets >/dev/null \
  || fail "k8srec" "could not create the read-only records Role"
kc create rolebinding carolplan-records -n "$CAROL_NS" --role=carol-records-ro --serviceaccount=default:carolplan >/dev/null \
  || fail "k8srec" "could not bind the read-only records Role"
PLAN_TOK="$(kc create token carolplan -n default --duration=2h)" || fail "k8srec" "could not mint a token for carolplan"
cp "$KUBECONFIG" "$PLAN_KC"
kubectl --kubeconfig "$PLAN_KC" config set-credentials carolplan --token="$PLAN_TOK" >/dev/null
kubectl --kubeconfig "$PLAN_KC" config set-context --current --user=carolplan >/dev/null
for verb in get list create update delete; do
  # `|| true` is load-bearing: `kubectl auth can-i` exits 1 when the answer
  # is "no", and "no" is what three of these five must answer. Without it
  # this scenario ends here, under set -e, having printed no verdict line at
  # all - which is what the first run of this step did.
  ANS="$(kubectl --kubeconfig "$PLAN_KC" auth can-i "$verb" secrets -n "$CAROL_NS" 2>&1 || true)"
  case "$verb:$ANS" in
    get:yes|list:yes|create:no|update:no|delete:no) ;;
    *) fail "k8srec" "the plan identity answers $ANS to $verb on secrets in $CAROL_NS; it is supposed to hold get and list and nothing else" ;;
  esac
done
kc get secrets -n "$CAROL_NS" -o jsonpath='{range .items[*]}{.metadata.name}={.metadata.resourceVersion}{"\n"}{end}' > "$W/rv-before"
cmd "choudoufu plan   # as an identity with get and list on secrets, and no create, update or delete"
P2="$( cd "$CAROL" && as_identity "$PLAN_KC" chdf plan -input=false -no-color 2>&1 )" \
  || fail "k8srec" "a plan under a get/list-only identity was refused: $P2"
{ grep -E 'No changes' <<< "$P2" || true; } | head -1 | evidence
grep -q 'No changes' <<< "$P2" \
  || fail "k8srec" "the read-only plan did not read the records back, so it proposed changes: $P2"
kc get secrets -n "$CAROL_NS" -o jsonpath='{range .items[*]}{.metadata.name}={.metadata.resourceVersion}{"\n"}{end}' > "$W/rv-after"
cmd "diff <(resourceVersions before) <(resourceVersions after)"
if ! diff "$W/rv-before" "$W/rv-after" > "$W/rv-diff" 2>&1; then
  fail "k8srec" "a record Secret's resourceVersion moved across a plan, so the plan wrote something: $(cat "$W/rv-diff")"
fi
echo "every record Secret's resourceVersion is unchanged across the plan" | evidence
cmd "choudoufu live-cluster -plan-identity   # and without it, the apply question"
PI="$( cd "$CAROL" && as_identity "$PLAN_KC" chdf live-cluster -plan-identity -no-color 2>&1 )" || true
AI="$( cd "$CAROL" && as_identity "$PLAN_KC" chdf live-cluster -no-color 2>&1 )" || true
{ grep -E '^  namespace_access' <<< "$PI" || true; } | cut -c1-140 | evidence
{ grep -E '^  namespace_access' <<< "$AI" || true; } | cut -c1-140 | evidence
grep -qE '^  namespace_access +OK' <<< "$PI" \
  || fail "k8srec" "the contract refused a plan identity for lacking create, update and delete, which a plan does not use: $PI"
grep -qE '^  namespace_access +FAIL' <<< "$AI" \
  || fail "k8srec" "the same identity passed the APPLY question, which needs three verbs it does not hold: $AI"
grep -q 'may not create, update, delete secrets' <<< "$AI" \
  || fail "k8srec" "the apply question does not name the three verbs the identity lacks: $AI"
proof "a plan under an identity holding get and list on the record Secrets and nothing else read the estate back and proposed nothing, and no record Secret's resourceVersion moved. The contract agrees: that identity passes the plan question by name and fails the apply question by name, naming create, update and delete."

step "10. two writers, one record, held at the wire"
explain \
  "Claim 32 on the bucket store holds two PutObjects at a proxy until" \
  "both have arrived and then requires one winner and one named conflict" \
  "every round. This is that measurement on this store. Two writers, each" \
  "with its own connection to this API server, read one record and come" \
  "away with one resourceVersion. A RoundTripper wrapped around each" \
  "writer's client parks the first request of its write, unanswered, until" \
  "BOTH writers are parked, and then lets them through one at a time, each" \
  "finishing before the next starts." \
  "" \
  "Two goroutines calling Update without that would mostly serialise: one" \
  "write finishes before the other begins, which is a sequence and proves" \
  "nothing about a race. So every round reports the gap between the two" \
  "arrivals and how long each request sat on the wire, and a round whose" \
  "requests were not parked together is counted apart and fails the run." \
  "" \
  "Both conditional writes this store has are raced, six rounds each," \
  "alternating which parked request is released first: two updates" \
  "carrying one resourceVersion, and two creates of a key that holds no" \
  "record yet. Every round must land exactly one write, refuse the other" \
  "with a VersionConflictError naming the version it expected and the" \
  "version the store holds, and leave the refused writer's payload" \
  "nowhere in the record, which is read back through a third client that" \
  "the barrier never touches."
RACE_NS="tofu-records-k8srec-race"
kc create namespace "$RACE_NS" >/dev/null || fail "k8srec" "could not create the race records namespace"
cmd "go test ./internal/live/staterecord -run TestKubernetesTwoWritersOneRecord   # against the kind cluster"
RACE_OUT="$( cd "$ROOT" && CHOUDOUFU_K8S_RECORD_KUBECONFIG="$KUBECONFIG" CHOUDOUFU_K8S_RECORD_NAMESPACE="$RACE_NS" \
  go test ./internal/live/staterecord -run TestKubernetesTwoWritersOneRecord -count=1 -v 2>&1 )" && RACE_RC=0 || RACE_RC=$?
# `awk NR<=3` and not `head -3`: under pipefail a head that closes the pipe
# early can take the whole evidence pipeline down with it, and an evidence
# line that kills the run leaves the assertions below unreached.
{ grep -E 'RACE-ROUND' <<< "$RACE_OUT" || true; } | sed 's/^[[:space:]]*//; s/^[^:]*go:[0-9]*: //' | awk 'NR<=3' | evidence
{ grep -E 'RACE-SUMMARY' <<< "$RACE_OUT" || true; } | sed 's/^[[:space:]]*//; s/^[^:]*go:[0-9]*: //' | evidence
# Anchored at column zero: that is where `go test` writes a test's own
# result, while everything the test logs is indented. An unanchored
# --- PASS would also match the subtests, and an unanchored --- SKIP would
# match a log line quoting one.
grep -qE '^--- SKIP: TestKubernetesTwoWritersOneRecord ' <<< "$RACE_OUT" \
  && fail "k8srec" "the two-writer race SKIPPED; a skip is not a pass and this step measured nothing"
RACE_TOTAL="$(grep -oE 'RACE-SUMMARY case=total .*' <<< "$RACE_OUT" | tail -1 || true)"
[ -n "$RACE_TOTAL" ] \
  || fail "k8srec" "the race printed no verdict line at all, so this step measured nothing: $RACE_OUT"
# The verdict is this line and not the exit code, and every number in it is
# exact rather than a floor. rounds=overlapped is the part that says the
# requests really did overlap: a round that timed out at the barrier is
# counted in rounds and not in overlapped, so overlapped=0, or anything
# short of 12, reads here as the failure it is.
grep -q 'mode=conditional-write rounds=12 overlapped=12 conflicts=12 clobbers=0' <<< "$RACE_TOTAL" \
  || fail "k8srec" "the race's verdict line reads \"$RACE_TOTAL\"; all 12 rounds must overlap on the wire, each must produce one named conflict, and none may end with both writes landed"
grep -qE '^--- PASS: TestKubernetesTwoWritersOneRecord \(' <<< "$RACE_OUT" \
  || fail "k8srec" "the race did not pass: $(grep -E 'round [0-9]+\]|^--- FAIL' <<< "$RACE_OUT" | awk 'NR<=6')"
[ "$RACE_RC" = "0" ] \
  || fail "k8srec" "the race printed a passing verdict and exited $RACE_RC, so something outside the rounds failed: $(tail -10 <<< "$RACE_OUT")"
cmd "kubectl get leases,secrets -n $RACE_NS   # after 12 contended writes, nothing lock-shaped"
# Both listings are captured with their own exit status checked, and the
# Secret listing has to hold the race's own records. A `kubectl | grep -i
# lock && fail` reads clean when the kubectl failed, and a listing of a
# namespace the race never wrote to reads clean for having seen nothing:
# either way the absence below would be nobody's absence.
RACE_LEASES="$(kc get leases -n "$RACE_NS" -o name 2>&1)" \
  || fail "k8srec" "listing Leases in $RACE_NS failed, so whether the race took one was never answered: $RACE_LEASES"
RACE_SECRETS="$(kc get secrets -n "$RACE_NS" -o name 2>&1)" \
  || fail "k8srec" "listing Secrets in $RACE_NS failed, so whether the race left anything lock-shaped was never answered: $RACE_SECRETS"
RACE_RECORDS="$(grep -c '^secret/tofu-record-' <<< "$RACE_SECRETS" || true)"
echo "record Secrets in the race namespace: $RACE_RECORDS; leases: ${RACE_LEASES:-none}" | evidence
# Seven: the one record the update rounds contend over, and the six the
# create rounds each make. The test leaves them there for this listing.
[ "$RACE_RECORDS" = "7" ] \
  || fail "k8srec" "the race's twelve rounds left $RACE_RECORDS record Secrets in $RACE_NS and six created keys plus the one the updates contend over is seven; a listing that cannot see the race's own objects cannot say whether one of them is lock-shaped"
[ -z "$RACE_LEASES" ] \
  || fail "k8srec" "twelve contended writes left a Lease in the race namespace; this store takes no lock: $RACE_LEASES"
grep -qi 'lock' <<< "$RACE_SECRETS" \
  && fail "k8srec" "an object in the race namespace is named like a lock: $RACE_SECRETS"
proof "twelve rounds, every one of them with both requests parked on the wire at once, and every one settled by the API server's own optimistic concurrency: one write landed, the other came back as a version conflict naming the version it planned against and the version the store now holds, and the refused payload is not in the record. No Lease and nothing lock-shaped was taken to do it."

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

  step "BREAK control - step 6's contract passed because the identity was scoped; widen it and the same run must be refused"
  explain \
    "Step 6's first contact went through under a Role holding secrets in" \
    "one namespace. What that proves depends on the contract being able to" \
    "refuse the same run, so here Carol's identity is given cluster-wide" \
    "secret reads and a second estate's records are already in the" \
    "cluster. read_isolation must then refuse her, by name. If it does" \
    "not, step 6 passed because the assertion cannot fail."
  kc create clusterrolebinding carol-reads-everything --clusterrole=secrets-everywhere --serviceaccount=default:carol >/dev/null \
    || fail "k8srec" "BREAK: could not widen Carol's reads"
  # And the ability to SEE the other estates. Cluster-wide secret reads
  # alone are a capability the contract only warns about, because on a
  # cluster holding no other estate's records nothing is exposed by them
  # (#1393). What refuses is another estate's records namespace that exists
  # and this identity can read, and finding one means listing namespaces.
  kc create clusterrole namespaces-everywhere --verb=get,list --resource=namespaces >/dev/null \
    || fail "k8srec" "BREAK: could not create the namespace-listing role"
  kc create clusterrolebinding carol-sees-everything --clusterrole=namespaces-everywhere --serviceaccount=default:carol >/dev/null \
    || fail "k8srec" "BREAK: could not let Carol see the other estates"
  # RBAC takes a moment to reach the authorizer, and the authorizer is what
  # the contract asks. can-i is the same question from the same identity, so
  # it is what waits the window out.
  for _ in $(seq 1 30); do
    [ "$(kubectl --kubeconfig "$CAROL_KC" auth can-i list secrets --all-namespaces 2>/dev/null)" = "yes" ] \
      && [ "$(kubectl --kubeconfig "$CAROL_KC" auth can-i list namespaces 2>/dev/null)" = "yes" ] && break
    sleep 1
  done
  [ "$(kubectl --kubeconfig "$CAROL_KC" auth can-i list secrets --all-namespaces 2>/dev/null)" = "yes" ] \
    || fail "k8srec" "BREAK: Carol's secret reads were never widened, so the refusal below would measure nothing"
  [ "$(kubectl --kubeconfig "$CAROL_KC" auth can-i list namespaces 2>/dev/null)" = "yes" ] \
    || fail "k8srec" "BREAK: Carol still cannot list namespaces, so the other estates' records namespaces are not known to her and the refusal below would measure nothing"
  # A fresh estate, so the run is a first contact and the contract runs. Its
  # name is not a prefix of Carol's: the fixture below is written by
  # substitution, and a name that contains the other one substitutes twice.
  BROKE_ESTATE="k8srec-wide"
  BROKE_NS="tofu-records-$BROKE_ESTATE"
  BROKE="$W/wide"
  kc create namespace "$BROKE_NS" >/dev/null || fail "k8srec" "BREAK: could not create the second records namespace"
  kc create role carol-records-wide -n "$BROKE_NS" --verb=get,list,create,update,delete --resource=secrets >/dev/null \
    || fail "k8srec" "BREAK: could not create Carol's second Role"
  kc create rolebinding carol-records-wide -n "$BROKE_NS" --role=carol-records-wide --serviceaccount=default:carol >/dev/null \
    || fail "k8srec" "BREAK: could not bind Carol's second Role"
  sed -e "s/ESTATE/$BROKE_ESTATE/g" -e "s/PRINCIPAL_NAMESPACE/default/g" -e "s/PRINCIPAL/carol/g" \
    "$ROOT/live/kubernetes/estate-grant.yaml" | kc apply -f - >/dev/null \
    || fail "k8srec" "BREAK: could not grant the second estate to Carol"
  mkdir -p "$BROKE"
  sed -e "s|$CAROL_NS|$BROKE_NS|g" -e "s/k8srec-carol/$BROKE_ESTATE/g" "$CAROL/versions.tf" > "$BROKE/versions.tf"
  grep -q "namespace = \"$BROKE_NS\"" "$BROKE/versions.tf" \
    || fail "k8srec" "BREAK: the widened estate's fixture does not name $BROKE_NS: $(cat "$BROKE/versions.tf")"
  cp "$CAROL/main.tf" "$BROKE/main.tf"
  ( cd "$BROKE" && as_identity "$CAROL_KC" chdf init -input=false -no-color >/dev/null ) \
    || fail "k8srec" "BREAK: the widened identity's init failed"
  cmd "choudoufu apply   # first contact, identity now reading secrets cluster-wide"
  if BR="$( cd "$BROKE" && as_identity "$CAROL_KC" chdf apply -auto-approve -input=false -no-color 2>&1 )"; then
    fail "k8srec" "BREAK: an identity that can read every estate's records in this cluster was allowed to open a new store, so step 6's pass was not read_isolation holding: $BR"
  fi
  { grep -E 'read_isolation' <<< "$BR" || true; } | head -2 | evidence
  grep -q 'fails its read_isolation assertion' <<< "$BR" \
    || fail "k8srec" "BREAK: the widened identity was refused for some other reason: $BR"
  proof "step 6's contract can refuse the run it let through: the same estate, the same store, the same command, with cluster-wide secret reads added, is refused by name on read_isolation."

  step "BREAK control - a write that reads the record and updates what it read must lose the race step 10 wins"
  explain \
    "Step 10 passes because the version a write carries is the one its" \
    "caller read, and nothing re-reads it inside the call. The stock" \
    "backend's Put does the opposite: it reads the Secret and updates what" \
    "came back (internal/backend/remote-state/kubernetes/client.go, line" \
    "86). This arm runs step 10's rounds again, with each write swapped for" \
    "that one and every assertion left alone. The writer released second" \
    "now reads the winner's object, updates it with its own payload and" \
    "reports success, so both writers \"win\" and the record holds the" \
    "payload of the one that was judged second." \
    "" \
    "The broken write lives in kubernetes_race_live_test.go and is reached" \
    "only through an environment variable that file reads, so no build of" \
    "choudoufu contains it. If this arm were to pass, step 10 would be" \
    "passing for some reason other than the conditional write and would" \
    "prove nothing."
  cmd "CHOUDOUFU_K8S_RECORD_RACE_BREAK=1 go test ./internal/live/staterecord -run TestKubernetesTwoWritersOneRecord"
  BREAK_OUT="$( cd "$ROOT" && CHOUDOUFU_K8S_RECORD_RACE_BREAK=1 CHOUDOUFU_K8S_RECORD_KUBECONFIG="$KUBECONFIG" CHOUDOUFU_K8S_RECORD_NAMESPACE="$RACE_NS" \
    go test ./internal/live/staterecord -run TestKubernetesTwoWritersOneRecord -count=1 -v 2>&1 )" && BREAK_RC=0 || BREAK_RC=$?
  { grep -E 'BOTH writers reported success' <<< "$BREAK_OUT" || true; } | sed 's/^[[:space:]]*//; s/^[^:]*go:[0-9]*: //' | awk 'NR<=2' | evidence
  { grep -E 'RACE-SUMMARY case=total' <<< "$BREAK_OUT" || true; } | sed 's/^[[:space:]]*//; s/^[^:]*go:[0-9]*: //' | evidence
  grep -qE '^--- SKIP: TestKubernetesTwoWritersOneRecord ' <<< "$BREAK_OUT" \
    && fail "k8srec" "BREAK: the race SKIPPED, so this control measured nothing"
  BREAK_TOTAL="$(grep -oE 'RACE-SUMMARY case=total .*' <<< "$BREAK_OUT" | tail -1 || true)"
  [ -n "$BREAK_TOTAL" ] \
    || fail "k8srec" "BREAK: the race printed no verdict line: $BREAK_OUT"
  grep -q 'mode=read-then-update rounds=12 overlapped=12 conflicts=0 clobbers=12' <<< "$BREAK_TOTAL" \
    || fail "k8srec" "BREAK: the verdict line reads \"$BREAK_TOTAL\"; with the write reading the record first, all 12 rounds must end with both writes landed and no conflict named at all"
  grep -q 'BOTH writers reported success' <<< "$BREAK_OUT" \
    || fail "k8srec" "BREAK: no round reported two winners, so step 10's assertion was never made to fire: $BREAK_OUT"
  [ "$BREAK_RC" != "0" ] \
    || fail "k8srec" "BREAK: the read-then-update write PASSED step 10's assertions, so those assertions cannot fail and step 10 proves nothing"
  proof "caught - twelve rounds, twelve clobbers, no conflict named once. The same rounds and the same assertions that step 10 passes are failed by a write that reads the record and updates what it read, which is what the conditional write exists to prevent."
fi
