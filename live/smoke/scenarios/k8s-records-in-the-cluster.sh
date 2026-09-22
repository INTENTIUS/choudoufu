# k8s-records-in-the-cluster
# CLAIM 39 - Records live in the cluster: a Kubernetes-only estate keeps its records as Secrets under resourceVersion with no AWS in the environment, two writers held on the wire with one resourceVersion between them settle with one winner and one named conflict, a waiver names what it waives on every run and live-cluster ignores it, a listing that fails after its first page fails the plan and never reads as a short estate, an apply killed with SIGKILL leaves no lock behind, a role scoped to one records namespace cannot read another estate's records, and the store checks that namespace, its RBAC scope, encryption at rest and the estate boundary once, on first contact, before it writes a record. ~20 min.
#
# GitHub issue #1392, under the #1398 ruling. Until this, a Kubernetes-only
# estate had two choices for its records: "local", which is one machine's
# disk, or "s3", which is an AWS account, a bucket and a role for a team
# that runs nothing on AWS. record_store "kubernetes" is the third: Secrets
# in a namespace, metadata.resourceVersion as the conditional write, and no
# lock and no Lease.
#
# Twelve steps, each measuring one of the things that would make the store
# a bad idea if it were not true. Steps 1 to 5 are the store (#1392); steps
# 6 to 9 are what it checks about the cluster before it writes a record
# (#1393), which is the bucket contract's shape sized for a cluster; steps
# 10 to 12 are claims 32, 30 and 31 on this store (#1441), each proven on
# the bucket by a scenario of its own and each needing the cluster steps 1
# to 9 already stood up, so they come last rather than beside the other
# store steps.
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
#  11. Claim 30 on this store (#1441): a fresh estate under allow_insecure
#      names every waived setting, with its cost, on its first apply, on a
#      plan and on a second apply alike, exactly three times per run; and
#      `choudoufu live-cluster`, reading the same block, ignores the waiver
#      - the two assertions this cluster fails stay FAIL, the verdict stays
#      NOT correct, and the waiver is named apart from the verdict as hiding
#      two failures.
#  12. Claim 31 on this store (#1441): the store's bulk read is one paged
#      LIST, and a proxy (live/smoke/k8sproxy.py) answers its second page
#      with the API server's own 410 Expired, once on the listing the store
#      opens with and twice on the bulk read after it. Each run must fail,
#      naming the listing and the reason, and print no plan at all - never
#      one over the records it did see. The namespace is padded so the first
#      page holds the store's sentinel and nothing of the estate, and the
#      second page every record.
#
# BREAK=1 takes the three fences away and requires what they refused to go
# through: the plan role is given cluster-wide secret reads and must then
# read Bob's records, the admission policy is removed and Bob's write into
# Alice's record Secret must then land, and the scoped identity step 6's
# contract passed is widened to read secrets cluster-wide, after which the
# same first contact must be refused on read_isolation. It adds a fourth
# control for step 10: each write becomes the stock backend's read-then-update
# (client.go:86) and the same twelve rounds must then end with both writes
# landed and nothing named. Steps 1, 2, 3, 8 and 9 each have a control of
# their own, run against the step's own check function (#1448): the suite
# skipped and the suite short one case, the record Secrets stripped of their
# annotation and then their label, a planted Lease and a lock-named Secret,
# Dan's Role given the verb it lacked, and a write between step 9's two
# dumps; each must fail the check by name, and the listings behind steps 3,
# 8 and 9 are also run through a kubeconfig that reaches no server and must
# fail rather than read as empty. Steps 11 and 12 each rebuild choudoufu
# with go build -overlay, the way claims 30 and 31 do on the bucket: a
# binary whose waiver warning goes quiet once the estate has a cache, whose
# second run step 11's check must refuse; and a binary whose paged LIST
# keeps its first page and drops the error, whose plan - creating every
# resource that exists - step 12's check must refuse.

SMOKE_WORK="$SMOKE_WORKROOT/k8s-records-in-the-cluster"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
W="$SMOKE_WORK"

cluster_up

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
# CONFORMANCE_CASES is how many cases the shared Store contract suite has:
# one t.Run per case in runConformance, internal/live/staterecord/
# conformance_test.go. It is written here, once, and compared EXACTLY rather
# than as a floor, so that a case added to that suite without this number
# moving fails this step and says so. Deriving it from the same `go test`
# output this step is checking would make it agree with itself whatever
# happened, which is the failure #1448 found in the `-ge 17` this replaces.
CONFORMANCE_CASES=18
# conformance_verdict <go test output> is step 1's check, and the BREAK arm
# runs the same function against a suite that never reached the cluster.
#
# The guards come BEFORE the evidence line, and every capture carries its own
# `|| true`. This step used to open with `grep -c ... | evidence`, which exits
# 1 when nothing matched: under set -euo pipefail a whole-suite SKIP therefore
# ended the run on that line, with no verdict line at all, and the SKIP guard
# written to catch exactly that sat on the next line, unreached. #1448.
conformance_verdict() {
  local out="$1" cases
  ! grep -qE '^--- SKIP: TestKubernetesStoreConformance ' <<< "$out" \
    || fail "k8srec" "the conformance suite SKIPPED; a skip is not a pass and this step measured nothing"
  ! grep -q 'no tests to run' <<< "$out" \
    || fail "k8srec" "go test matched no test at all, so this step measured nothing: $out"
  # Anchored the way step 10 anchors its own PASS line. `go test -v` indents a
  # subtest's result by four spaces and writes what a test LOGS behind a
  # file:line prefix, so an unanchored --- PASS also counts a log line quoting
  # one.
  cases="$( { grep -cE '^    --- PASS: TestKubernetesStoreConformance/[A-Za-z][A-Za-z0-9]* \(' <<< "$out" || true; } )"
  echo "conformance cases passed: $cases of $CONFORMANCE_CASES" | evidence
  [ "$cases" = "$CONFORMANCE_CASES" ] \
    || fail "k8srec" "$cases conformance cases passed against this cluster and the shared suite has $CONFORMANCE_CASES (runConformance in internal/live/staterecord/conformance_test.go). Either a case was added or removed there without this scenario's CONFORMANCE_CASES moving with it, or a case did not run against this cluster."
  { grep -E -- '--- PASS: TestKubernetesStore(VersionIsTheResourceVersion|RefusesAMissingNamespaceByName)' <<< "$out" || true; } | evidence
}
cmd "go test ./internal/live/staterecord -run TestKubernetesStore   # against the kind cluster"
CONF_OUT="$( cd "$ROOT" && CHOUDOUFU_K8S_RECORD_KUBECONFIG="$KUBECONFIG" CHOUDOUFU_K8S_RECORD_NAMESPACE="$RECORDS_NS" \
  go test ./internal/live/staterecord -run TestKubernetesStore -count=1 -v 2>&1 )" \
  || fail "k8srec" "the conformance suite failed against the cluster: $( { grep -E '^\s+--- FAIL|FAIL' <<< "$CONF_OUT" || true; } | awk 'NR<=10' )"
conformance_verdict "$CONF_OUT"
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
{ grep -E 'Apply complete!' <<< "$A_OUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 3 added' <<< "$A_OUT" \
  || fail "k8srec" "the apply did not report 3 added: $A_OUT"
grep -qiE 'aws|credential' <<< "$A_OUT" \
  && fail "k8srec" "the apply output mentions AWS or credentials: $A_OUT"
# records_are_secrets <namespace> <estate> is step 2's check on the objects:
# the estate's records are Secrets carrying its label, named tofu-record-,
# and the first of them says which record it is. It leaves the listing in
# REC_NAMES. The BREAK arm runs the same function after stripping the
# annotation and then the label.
records_are_secrets() {
  local ns="$1" estate="$2" one ann
  cmd "kubectl get secrets -n $ns -l tofu-estate=$estate"
  REC_NAMES="$(kc get secrets -n "$ns" -l "tofu-estate=$estate" -o name 2>&1)" \
    || fail "k8srec" "could not list the record Secrets: $REC_NAMES"
  echo "$REC_NAMES" | evidence
  [ -n "$REC_NAMES" ] || fail "k8srec" "no record Secret carries tofu-estate=$estate; the estate wrote its records somewhere else, or nowhere"
  grep -q 'secret/tofu-record-' <<< "$REC_NAMES" \
    || fail "k8srec" "a record Secret is not named tofu-record-<hash>: $REC_NAMES"
  one="$(awk 'NR<=1' <<< "$REC_NAMES" | sed 's|^secret/||')"
  cmd "kubectl get secret $one -n $ns -o jsonpath='{.metadata.annotations}'"
  ann="$(kc get secret "$one" -n "$ns" -o jsonpath='{.metadata.annotations}' 2>&1)" \
    || fail "k8srec" "reading the record Secret's annotations failed: $ann"
  echo "$ann" | evidence
  grep -q 'choudoufu.intentius.io/record-key' <<< "$ann" \
    || fail "k8srec" "the record Secret carries no record-key annotation, so nothing says which record it is: $ann"
}
records_are_secrets "$RECORDS_NS" k8srec-alice
# How many record Secrets this estate has, read here where they are checked
# by name. Step 3 requires a listing of the same namespace to still hold at
# least this many before it says nothing in it is named like a lock.
ALICE_RECORDS="$( { grep -c '^secret/tofu-record-' <<< "$REC_NAMES" || true; } )"
# The apply is idempotent and the record is what makes it so: a second plan
# reads the record back and proposes nothing.
P_OUT="$( cd "$APP" && no_aws chdf plan -input=false -no-color 2>&1 )" || fail "k8srec" "the replan failed: $P_OUT"
{ grep -E 'No changes|Plan:' <<< "$P_OUT" || true; } | awk 'NR<=1' | evidence
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
# lock_free <kubeconfig> <namespace> <records> is step 3's check: nothing in
# the namespace is a Lease or named like a lock. Both listings are captured
# with their own exit status checked, and the Secret listing has to still
# hold the <records> step 2 read back by name. A `kc get secrets | grep -i
# lock && fail` reads clean when the kubectl crashed, and a listing that saw
# nothing reads clean for having seen nothing: either way the absence below
# would be nobody's absence. Step 10 does exactly this; this is the same
# shape. #1448. The kubeconfig is a parameter so the BREAK arm can hand it
# one that reaches no server.
lock_free() {
  local cfg="$1" ns="$2" records="$3" locks secrets now
  locks="$(kc_as "$cfg" get leases -n "$ns" -o name 2>&1)" \
    || fail "k8srec" "listing Leases in $ns failed, so whether the killed apply left one behind was never answered: $locks"
  secrets="$(kc_as "$cfg" get secrets -n "$ns" -o name 2>&1)" \
    || fail "k8srec" "listing Secrets in $ns failed, so whether anything in it is named like a lock was never answered: $secrets"
  now="$( { grep -c '^secret/tofu-record-' <<< "$secrets" || true; } )"
  echo "record Secrets in the records namespace: $now; leases: ${locks:-none}" | evidence
  [ "$now" -ge "$records" ] \
    || fail "k8srec" "this listing of $ns holds $now record Secrets and step 2 read $records of them back by name; a listing that cannot see the estate's own objects cannot say whether one of them is named like a lock: $secrets"
  [ -z "$locks" ] || fail "k8srec" "a Lease exists in the records namespace; this store takes no lock: $locks"
  ! grep -qi 'lock' <<< "$secrets" \
    || fail "k8srec" "an object in the records namespace is named like a lock: $secrets"
}
lock_free "$KUBECONFIG" "$RECORDS_NS" "$ALICE_RECORDS"
cmd "choudoufu apply -auto-approve   # the very next run, nothing done in between"
N_OUT="$( cd "$APP" && no_aws chdf apply -auto-approve -input=false -no-color 2>&1 )" \
  || fail "k8srec" "the run after the killed one failed: $N_OUT"
grep -qiE "state lock|acquiring the lock|force-unlock" <<< "$N_OUT" \
  && fail "k8srec" "the run after the killed one talks about a lock: $N_OUT"
{ grep -E 'Apply complete!' <<< "$N_OUT" || true; } | awk 'NR<=1' | evidence
grep -q 'Apply complete!' <<< "$N_OUT" || fail "k8srec" "the run after the killed one did not complete: $N_OUT"
# And it converged, which is the other half of carrying on: the estate the
# killed run left half-built is whole again.
C_OUT="$( cd "$APP" && no_aws chdf plan -input=false -no-color 2>&1 )" || fail "k8srec" "the plan after the recovery run failed: $C_OUT"
{ grep -E 'No changes|Plan:' <<< "$C_OUT" || true; } | awk 'NR<=1' | evidence
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
CTRL="$(kc get secrets -n "$BOB_NS" -l tofu-estate=k8srec-bob -o name 2>&1)" \
  || fail "k8srec" "the control listing of Bob's records failed, so the refusal below would be compared against nothing: $CTRL"
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
OWN="$(kc_as "$PLANNER_KC" get secrets -n "$RECORDS_NS" -l tofu-estate=k8srec-alice -o name 2>&1)" \
  || fail "k8srec" "the scoped identity's read of its OWN estate's records failed, so the refusal below would be a role that reads nothing at all: $OWN"
echo "$OWN" | evidence
grep -q 'secret/tofu-record-' <<< "$OWN" \
  || fail "k8srec" "the scoped role cannot read its OWN estate's records, so the refusal below would be a role that reads nothing at all: $OWN"

cmd "kubectl --as the planner get secrets -n $BOB_NS   # the other estate's"
if CROSS="$(kc_as "$PLANNER_KC" get secrets -n "$BOB_NS" -o name 2>&1)"; then
  fail "k8srec" "the scoped role READ another estate's records: $CROSS"
fi
awk 'NR<=2' <<< "$CROSS" | evidence
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
TC="$(kc get validatingadmissionpolicy choudoufu-estate-boundary -o jsonpath='{.status.typeChecking.expressionWarnings}' 2>&1)" \
  || fail "k8srec" "reading the policy's type-check status failed, so whether its CEL type-checks was never answered: $TC"
[ -z "$TC" ] || fail "k8srec" "the policy's CEL has type-check warnings: $TC"
sed -e "s/ESTATE/k8srec-bob/g" -e "s/PRINCIPAL_NAMESPACE/default/g" -e "s/PRINCIPAL/planner/g" \
  "$ROOT/live/kubernetes/estate-grant.yaml" | kc apply -f - >/dev/null \
  || fail "k8srec" "could not grant estate k8srec-bob to the planner"
# `kc | head -1` under pipefail: head closes the pipe after one line, the
# kubectl writing the rest dies of EPIPE, and the whole substitution fails -
# which under set -e ends the run here, with no verdict line, and the
# emptiness guard below never runs. Captured whole, then narrowed with awk.
ALICE_NOW="$(kc get secrets -n "$RECORDS_NS" -l tofu-estate=k8srec-alice -o name 2>&1)" \
  || fail "k8srec" "listing Alice's record Secrets failed, so there is no object for the fenced write to be refused on: $ALICE_NOW"
TARGET="$( { grep '^secret/tofu-record-' <<< "$ALICE_NOW" || true; } | awk 'NR<=1' | sed 's|^secret/||')"
[ -n "$TARGET" ] || fail "k8srec" "no record Secret of Alice's to write to: $ALICE_NOW"
# The write is an UPDATE and not a patch, on purpose. `kubectl annotate` and
# `kubectl label` both send a PATCH, and the Role the docs recommend - get,
# list, create, update, delete, which is what the store itself uses - does not
# carry patch. A patch here is refused by RBAC before admission is ever
# consulted, so it would read as the boundary holding while measuring nothing
# about it. That is exactly what the first version of this step did.
kc get secret "$TARGET" -n "$RECORDS_NS" -o json > "$W/target.json" \
  || fail "k8srec" "reading Alice's record Secret $TARGET failed, so there is nothing for the fenced write to send"
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
  if ! kc_as "$PLANNER_KC" replace --dry-run=server -f "$W/target.json" >/dev/null 2>&1; then
    FENCED=yes; break
  fi
  sleep 1
done
[ -n "$FENCED" ] || fail "k8srec" "30s after the policy was observed, a dry-run cross-estate write on a record Secret was still accepted; the fence is not in force and the assertion below would measure nothing"
cmd "kubectl --as the planner (bound to estate k8srec-bob) replace -f <Alice's record Secret>"
if WROTE="$(kc_as "$PLANNER_KC" replace -f "$W/target.json" 2>&1)"; then
  fail "k8srec" "an identity bound to estate k8srec-bob wrote estate k8srec-alice's record object: $WROTE"
fi
awk 'NR<=3' <<< "$WROTE" | evidence
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
#
# A FAILED READ FAILS THE STEP, by name, every time the counter is read. This
# used to end `|| true`, which turned a refused, crashed or empty /metrics
# call into a count of 0 - and a BEFORE reading of 0 makes "first contact
# asked the authorizer something" true of any AFTER reading at all, which is
# the whole measurement. #1448.
#
# The status is still captured separately from the awk rather than piped into
# it, which is what the `|| true` was there for: this scenario runs under set
# -euo pipefail, and a pipeline whose first stage fails would end the run with
# no verdict line. Capturing the status explicitly keeps that property and
# answers the question the `|| true` threw away.
ssar_count() {
  local raw rc
  raw="$(kc get --raw /metrics 2>&1)" && rc=0 || rc=$?
  [ "$rc" = "0" ] \
    || fail "k8srec" "reading the API server's /metrics exited $rc, so the SelfSubjectAccessReview counter this step is measured with was never read and a count of zero here would be the read failing rather than nothing being asked: $raw"
  [ -n "$raw" ] \
    || fail "k8srec" "the API server's /metrics answered nothing at all, so the SelfSubjectAccessReview counter this step is measured with is empty"
  grep -q '^apiserver_request_total' <<< "$raw" \
    || fail "k8srec" "the API server's /metrics carries no apiserver_request_total series, so a count of zero would mean the metric is gone rather than that the authorizer was asked nothing"
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
  "They are asked on the estate's FIRST contact with the store, which is the" \
  "run that created the sentinel, and again before every apply, because an" \
  "apply is the run that writes records and a policy somebody uninstalled" \
  "last week is worth catching. They are NOT asked on an ordinary plan. The" \
  "API server's own request counter measures the plans, and the plans'" \
  "output measures it a second way: the warnings for what could not be" \
  "checked come from the paths that run the contract, so a plan prints none."
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
{ grep -E 'Apply complete!' <<< "$C_OUT" || true; } | awk 'NR<=1' | evidence
echo "SelfSubjectAccessReviews on first contact: $((FIRST-BEFORE))" | evidence
[ "$((FIRST-BEFORE))" -gt 0 ] \
  || fail "k8srec" "first contact asked the authorizer nothing, so the contract did not run and the count below would measure nothing"
# What a scoped identity cannot read, it says, by name, rather than passing.
# This is the run that says it: the contract ran here, so the warnings are
# here. The two plans below must carry none.
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
# The plans' output is KEPT. It used to go to /dev/null, so the half of this
# step that does not need the metrics endpoint - that a plan prints no
# contract finding, because the contract did not run - was never measured at
# all. #1448.
for n in 1 2; do
  PL_OUT="$( cd "$CAROL" && as_identity "$CAROL_KC" chdf plan -input=false -no-color 2>&1 )" \
    || fail "k8srec" "plan $n after first contact failed: $PL_OUT"
  grep -q 'could not be checked' <<< "$PL_OUT" \
    && fail "k8srec" "plan $n printed a contract warning, so the contract ran on an ordinary plan: $PL_OUT"
  grep -q 'not readable from here, not checked' <<< "$PL_OUT" \
    && fail "k8srec" "plan $n printed a contract finding, so the contract ran on an ordinary plan: $PL_OUT"
done
echo "contract findings printed by the two plans: none" | evidence
AFTER="$(ssar_count)"
echo "SelfSubjectAccessReviews across two further plans: $((AFTER-FIRST))" | evidence
[ "$((AFTER-FIRST))" -eq 0 ] \
  || fail "k8srec" "the contract ran again on a plan: $((AFTER-FIRST)) more SelfSubjectAccessReviews across two plans, and a plan is not supposed to ask it at all"
proof "the estate's first contact asked the authorizer $((FIRST-BEFORE)) questions; the two plans after it asked none and printed no contract finding. The three properties this scoped identity cannot read - read_isolation, encryption_at_rest and estate_boundary - were each named on the run that asked them, and none of them was reported as a pass."

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
# refused_short_one_verb <kubeconfig> <dir> is step 8's first check: the
# apply is refused before it starts, naming the assertion and the verb.
# left_nothing_behind <kubeconfig> <namespace> is its second: the refusal
# wrote nothing that would make the next run something other than a first
# contact. The BREAK arm gives the same identity the verb and runs both
# again, and each must then come out the other way.
refused_short_one_verb() {
  local cfg="$1" dir="$2" out
  if out="$( cd "$dir" && as_identity "$cfg" chdf apply -auto-approve -input=false -no-color 2>&1 )"; then
    fail "k8srec" "an apply under a Role that cannot update a record Secret was allowed to start: $out"
  fi
  { grep -E 'namespace_access|may not update' <<< "$out" || true; } | awk 'NR<=3' | evidence
  grep -q 'fails its namespace_access assertion' <<< "$out" \
    || fail "k8srec" "the refusal does not name the assertion: $out"
  grep -q 'may not update secrets' <<< "$out" \
    || fail "k8srec" "the refusal does not name the verb that is missing, so nobody could act on it: $out"
}
left_nothing_behind() {
  local cfg="$1" ns="$2" left
  # The listing's own status is checked: an assignment from a kubectl that
  # crashed ends the run under set -e with no verdict line, the shape #1448
  # found in step 3.
  left="$(kc_as "$cfg" get secrets -n "$ns" -o name 2>&1)" \
    || fail "k8srec" "listing Secrets in $ns failed, so whether the refused first contact left anything behind was never answered: $left"
  echo "secrets in Dan's records namespace after the refusal: ${left:-none}" | evidence
  [ -z "$left" ] || fail "k8srec" "the refused first contact left $left behind; the next run would not be a first contact and would proceed against the cluster this one refused"
}
cmd "choudoufu apply   # as a Role with get, list, create and delete, and no update"
refused_short_one_verb "$DAN_KC" "$DAN"
cmd "kubectl get secrets -n $DAN_NS   # the refused first contact left nothing behind"
left_nothing_behind "$KUBECONFIG" "$DAN_NS"
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
  ANS="$(kc_as "$PLAN_KC" auth can-i "$verb" secrets -n "$CAROL_NS" 2>&1 || true)"
  case "$verb:$ANS" in
    get:yes|list:yes|create:no|update:no|delete:no) ;;
    *) fail "k8srec" "the plan identity answers $ANS to $verb on secrets in $CAROL_NS; it is supposed to hold get and list and nothing else" ;;
  esac
done
# The before/after pair is guarded, both halves, the way the sibling scenario
# k8s-a-label-is-a-change.sh guards its own. Two EMPTY files diff clean, so an
# unguarded pair reads "nothing moved" when the kubectl crashed, when the
# namespace emptied out and when the jsonpath stopped matching - three ways of
# measuring nothing and calling it a pass. #1448.
#
# rv_snapshot <kubeconfig> <namespace> <file> before|after <records> takes
# one half of the pair and requires it to hold at least <records> lines,
# leaving the count in RV_N; rv_unchanged <before> <after> is the diff. The
# BREAK arm runs the same two functions around a write, against an empty
# namespace, and through a kubeconfig that reaches no server.
rv_snapshot() {
  local cfg="$1" ns="$2" file="$3" which="$4" records="$5"
  if ! kc_as "$cfg" get secrets -n "$ns" -o jsonpath='{range .items[*]}{.metadata.name}={.metadata.resourceVersion}{"\n"}{end}' > "$file"; then
    case "$which" in
      before) fail "k8srec" "reading the record Secrets' resourceVersions before the plan failed, so there is nothing for the reading after it to be compared against" ;;
      *) fail "k8srec" "reading the record Secrets' resourceVersions after the plan failed, so whether one of them moved was never answered" ;;
    esac
  fi
  RV_N="$( { grep -c '=' "$file" || true; } )"
  [ "$RV_N" -ge "$records" ] \
    || fail "k8srec" "the $which dump holds $RV_N resourceVersions and this namespace holds $records record Secrets; a short or empty dump diffs clean against anything"
}
rv_unchanged() {
  local before="$1" after="$2"
  if ! diff "$before" "$after" > "$before.diff" 2>&1; then
    fail "k8srec" "a record Secret's resourceVersion moved across a plan, so the plan wrote something: $(cat "$before.diff")"
  fi
}
CAROL_SECRETS="$(kc get secrets -n "$CAROL_NS" -o name 2>&1)" \
  || fail "k8srec" "listing Carol's records namespace failed, so the pair of resourceVersion dumps below would be two empty files, and two empty files diff clean: $CAROL_SECRETS"
CAROL_RECORDS="$( { grep -c '^secret/tofu-record-' <<< "$CAROL_SECRETS" || true; } )"
[ "$CAROL_RECORDS" -ge 1 ] \
  || fail "k8srec" "Carol's records namespace holds no tofu-record- Secret although step 6 applied her estate into it; there is no record Secret here whose resourceVersion could move or stay put: $CAROL_SECRETS"
rv_snapshot "$KUBECONFIG" "$CAROL_NS" "$W/rv-before" before "$CAROL_RECORDS"
cmd "choudoufu plan   # as an identity with get and list on secrets, and no create, update or delete"
P2="$( cd "$CAROL" && as_identity "$PLAN_KC" chdf plan -input=false -no-color 2>&1 )" \
  || fail "k8srec" "a plan under a get/list-only identity was refused: $P2"
{ grep -E 'No changes' <<< "$P2" || true; } | awk 'NR<=1' | evidence
grep -q 'No changes' <<< "$P2" \
  || fail "k8srec" "the read-only plan did not read the records back, so it proposed changes: $P2"
rv_snapshot "$KUBECONFIG" "$CAROL_NS" "$W/rv-after" after "$CAROL_RECORDS"
cmd "diff <(resourceVersions before) <(resourceVersions after)"
rv_unchanged "$W/rv-before" "$W/rv-after"
echo "$RV_N record Secret resourceVersions, $CAROL_RECORDS of them records, unchanged across the plan" | evidence
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

### Claims 30 and 31 on this store (#1441). Each is proven on the bucket by
### a scenario of its own; on the cluster each is a step here, because one
### promise keeps one claim number and gets a proof per platform (#1112).

# flat undoes the CLI's word wrap, which breaks a diagnostic's sentences
# across lines and prefixes them with a box-drawing bar wherever the
# terminal width falls. The bucket scenarios have the same function.
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }

step "11. a waiver names what it waives on every run, and live-cluster ignores it"
explain \
  "Claim 30 on this store. Steps 2 to 5 ran under allow_insecure naming" \
  "read_isolation, encryption_at_rest and estate_boundary, and nothing" \
  "above asserted that a run SAYS so. This does, on a fresh estate: its" \
  "first apply, a plan and a second apply must each name all three waived" \
  "settings with what each one costs, and name exactly three. A waiver" \
  "that goes quiet after the first run looks exactly like a cluster that" \
  "passes. Then choudoufu live-cluster, run from the same directory so it" \
  "reads the same block, must leave read_isolation and encryption_at_rest" \
  "as FAIL and exit non-zero whatever the waiver says, and name the waiver" \
  "apart from the verdict as hiding a failure. The run does not say" \
  "whether the cluster really fails what it waives; the report is where" \
  "that is said, and step 7 measured it saying so."
WAIVED_NS="tofu-records-k8srec-waived"
WAIVED="$W/waived"
kc create namespace "$WAIVED_NS" >/dev/null || fail "k8srec" "could not create the waived estate's records namespace"
mkdir -p "$WAIVED"
versions k8srec-waived "$WAIVED_NS" "$WAIVED"
cat > "$WAIVED/main.tf" <<'TF'
resource "terraform_data" "effect" {
  input = "v1"
}
TF
# waiver_cost <setting> is a phrase from the cost clause the warning carries
# for that setting (staterecord.ClusterWaiverCost), so a warning that names
# the setting and not what waiving it gives up is caught.
waiver_cost() {
  case "$1" in
    read_isolation) echo "reading another estate's records" ;;
    encryption_at_rest) echo "encrypt the records in etcd" ;;
    estate_boundary) echo "overwriting or deleting this estate's records" ;;
    *) fail "k8srec" "waiver_cost knows no cost for $1" ;;
  esac
}
# waived_named <label> <output> is step 11's check on one run: each of the
# three waived settings is named as waived, each with its own cost, and the
# run names exactly three - not the two it would name if one had gone quiet.
# The BREAK arm runs it against the second run of a binary whose warning
# goes quiet once the estate has a cache.
waived_named() {
  local label="$1" text n setting
  text="$(flat <<< "$2")"
  for setting in read_isolation encryption_at_rest estate_boundary; do
    grep -q "The record store cluster's $setting assertion is waived" <<< "$text" \
      || fail "k8srec" "[$label] the run said nothing about the $setting waiver it is running under: $2"
    grep -q "$(waiver_cost "$setting")" <<< "$text" \
      || fail "k8srec" "[$label] the run names the $setting waiver but not what it costs: $2"
  done
  n="$( { grep -o "assertion is waived" <<< "$text" || true; } | wc -l | tr -d ' ')"
  [ "$n" = "3" ] \
    || fail "k8srec" "[$label] the run names $n waived assertion(s) and the configuration waives three: $2"
  echo "$label: 3 waived assertions named, each with its cost" | evidence
}
( cd "$WAIVED" && no_aws chdf init -input=false -no-color >/dev/null ) || fail "k8srec" "the waived estate's init failed"
cmd "choudoufu apply -auto-approve   # allow_insecure = [read_isolation, encryption_at_rest, estate_boundary]; first contact"
WV1="$( cd "$WAIVED" && no_aws chdf apply -auto-approve -input=false -no-color 2>&1 )" \
  || fail "k8srec" "the first apply under the waiver was refused, although every assertion this cluster fails is waived: $WV1"
grep -q 'Apply complete! Resources: 1 added' <<< "$WV1" || fail "k8srec" "the waived estate's first apply did not report 1 added: $WV1"
{ grep -E 'assertion is waived' <<< "$WV1" || true; } | sed 's/^[^A-Za-z]*//' | awk 'NR<=3' | evidence
waived_named "run 1, the first apply" "$WV1"
cmd "choudoufu plan   # run 2"
WV2="$( cd "$WAIVED" && no_aws chdf plan -input=false -no-color 2>&1 )" || fail "k8srec" "the plan under the waiver failed: $WV2"
grep -q 'No changes' <<< "$WV2" || fail "k8srec" "the plan under the waiver proposes changes, so the records were not read back: $WV2"
waived_named "run 2, a plan" "$WV2"
sed -i.bak 's/input = "v1"/input = "v2"/' "$WAIVED/main.tf" && rm -f "$WAIVED/main.tf.bak"
grep -q 'input = "v2"' "$WAIVED/main.tf" || fail "k8srec" "the waived estate's input was not changed, so the second apply below would apply nothing"
cmd "choudoufu apply -auto-approve   # run 3, a second apply"
WV3="$( cd "$WAIVED" && no_aws chdf apply -auto-approve -input=false -no-color 2>&1 )" || fail "k8srec" "the second apply under the waiver failed: $WV3"
grep -q 'Apply complete!' <<< "$WV3" || fail "k8srec" "the second apply under the waiver did not complete: $WV3"
waived_named "run 3, a second apply" "$WV3"
cmd "choudoufu live-cluster   # from the estate's directory: the same block is read, and its waiver ignored"
LC_OUT="$( cd "$WAIVED" && no_aws chdf live-cluster -no-color 2>&1 )" && LC_RC=0 || LC_RC=$?
{ grep -E '^  (read_isolation|encryption_at_rest|estate_boundary|waiver:)' <<< "$LC_OUT" || true; } | cut -c1-150 | evidence
grep -q 'reached through the record_store "kubernetes" block' <<< "$LC_OUT" \
  || fail "k8srec" "live-cluster did not read the estate's record_store block, so the waiver it is supposed to ignore was never in front of it: $LC_OUT"
[ "$LC_RC" != "0" ] \
  || fail "k8srec" "live-cluster exited 0 under a waiver, on a cluster that fails two of the waived assertions; the waiver reached the verdict: $LC_OUT"
grep -q 'NOT correct' <<< "$LC_OUT" \
  || fail "k8srec" "live-cluster's verdict line does not read NOT correct under the waiver: $LC_OUT"
for setting in read_isolation encryption_at_rest; do
  grep -qE "^  $setting +FAIL " <<< "$LC_OUT" \
    || fail "k8srec" "live-cluster did not report $setting as FAIL although the block it read waives it; the waiver reached the report: $LC_OUT"
  grep -qF "waiver: allow_insecure names \"$setting\", and the cluster DOES fail it" <<< "$LC_OUT" \
    || fail "k8srec" "live-cluster does not say that the $setting waiver is hiding a failure: $LC_OUT"
done
grep -qF 'waiver: allow_insecure names "estate_boundary". The cluster passes it today' <<< "$LC_OUT" \
  || fail "k8srec" "live-cluster does not say that the estate_boundary waiver, on a cluster with the policy in force, is hiding nothing: $LC_OUT"
proof "three runs, three warnings each: a first apply, a plan and a second apply all named read_isolation, encryption_at_rest and estate_boundary as waived, each with what it costs. live-cluster, reading the same block, ignored the waiver: read_isolation and encryption_at_rest are FAIL, the verdict is NOT correct and the exit is non-zero, and the waiver is named apart from the verdict as hiding two failures and, for estate_boundary, nothing."

step "12. a listing that fails after its first page fails the plan, and never reads as a short estate"
explain \
  "Claim 31 on this store. The bucket store's bulk read is a LIST and a" \
  "fan-out of GETs, and claim 31 fails one GET. This store's bulk read is" \
  "one paged LIST - every Secret's payload rides along with its metadata," \
  "so there is nothing to fan out - and the page is where it can come" \
  "back short: the API server hands out a continue token per page, and a" \
  "token that has outlived the watch cache is answered 410 Gone. A read" \
  "that kept the first page and dropped the error would read as an" \
  "estate with fewer records than it has, and a plan over that proposes" \
  "creating every record it did not see." \
  "" \
  "So the LIST has to page. A run lists 200 a page and the store lists" \
  "the whole namespace with no selector, so 199 plain Secrets in the" \
  "records namespace, named to sort before every record, make the first" \
  "page hold the store's sentinel and nothing of the estate, and the" \
  "second page every record. The estate's name is chosen so its sentinel" \
  "sorts first: the sentinel (#693) is read back through List when the" \
  "store opens, and a sentinel on page two would have the BREAK binary" \
  "refused there, by the sentinel guard, before its short listing ever" \
  "reached a plan. A proxy (live/smoke/k8sproxy.py) sits between the run" \
  "and the API server, re-terminating TLS - the run's kubeconfig points" \
  "at it with insecure-skip-tls-verify, and it speaks to the real API" \
  "server as the admin - and answers the second page with the API" \
  "server's own 410 Expired on cue: first on the listing the store opens" \
  "with, then, with that one relayed whole, on the bulk read after it."
# The estate whose sentinel Secret sorts before its six records and its hint.
# Names are SHA-256 of the key (staterecord.KubernetesStore.SecretName), the
# keys are projection's (RecordKeyPrefix, SentinelKey, HintKey), and what
# this arithmetic produces is checked against the cluster below rather than
# trusted.
PAGED_ESTATE="$(python3 - <<'PYEOF'
import base64, hashlib
def name(key): return "tofu-record-" + hashlib.sha256(key.encode()).hexdigest()
addrs = ['terraform_data.effect["%s"]' % k for k in ("a", "b", "c", "d", "e", "f")]
for n in range(10000):
    estate = "k8srec-paged-%d" % n
    sentinel = name("tofu-records/%s/.store-sentinel" % estate)
    others = [name("tofu-records/%s/terraform_data/%s" % (estate, base64.urlsafe_b64encode(a.encode()).decode().rstrip("="))) for a in addrs]
    others.append(name("tofu-hints/%s/guided" % estate))
    if all(sentinel < o for o in others):
        print(estate)
        break
PYEOF
)"
[ -n "$PAGED_ESTATE" ] || fail "k8srec" "no estate name in ten thousand put the sentinel's Secret first; the naming arithmetic above is wrong"
PAGED_NS="tofu-records-$PAGED_ESTATE"
PAGED="$W/paged"
kc create namespace "$PAGED_NS" >/dev/null || fail "k8srec" "could not create the paged estate's records namespace"
mkdir -p "$PAGED"
versions "$PAGED_ESTATE" "$PAGED_NS" "$PAGED"
cat > "$PAGED/main.tf" <<'TF'
resource "terraform_data" "effect" {
  for_each = toset(["a", "b", "c", "d", "e", "f"])
  input    = each.key
}
TF
( cd "$PAGED" && no_aws chdf init -input=false -no-color >/dev/null ) || fail "k8srec" "the paged estate's init failed"
cmd "choudoufu apply -auto-approve   # six record-backed resources, straight to the cluster"
PG_OUT="$( cd "$PAGED" && no_aws chdf apply -auto-approve -input=false -no-color 2>&1 )" \
  || fail "k8srec" "the paged estate's apply failed: $PG_OUT"
grep -q 'Apply complete! Resources: 6 added' <<< "$PG_OUT" || fail "k8srec" "the paged estate's apply did not report 6 added: $PG_OUT"
# The layout the fault needs, read back from the cluster: six record Secrets,
# and the sentinel's Secret first of everything named tofu-record- in name
# order, which is the order a LIST pages in.
PAGED_NAMES="$(kc get secrets -n "$PAGED_NS" -o jsonpath='{range .items[*]}{.metadata.name} {.metadata.annotations.choudoufu\.intentius\.io/record-key}{"\n"}{end}' 2>&1)" \
  || fail "k8srec" "listing the paged estate's Secrets failed, so the page layout below is unknown: $PAGED_NAMES"
PAGED_RECORDS="$( { grep -c '^tofu-record-.* tofu-records/.*/terraform_data/' <<< "$PAGED_NAMES" || true; } )"
[ "$PAGED_RECORDS" = "6" ] || fail "k8srec" "the paged estate holds $PAGED_RECORDS record Secrets for its six resources: $PAGED_NAMES"
FIRST_NAMED="$( { grep '^tofu-record-' <<< "$PAGED_NAMES" || true; } | LC_ALL=C sort | awk 'NR==1')"
grep -q '/.store-sentinel$' <<< "$FIRST_NAMED" \
  || fail "k8srec" "the first record-named Secret in name order is not the store's sentinel but $FIRST_NAMED; the estate was named so that it would be, so the store's naming has moved. Page one would then hold a record, or not hold the sentinel, and the control below could be refused by the sentinel guard instead of caught at the plan: $PAGED_NAMES"
echo "sentinel first in name order: $(cut -d' ' -f1 <<< "$FIRST_NAMED"); records after it: $PAGED_RECORDS" | evidence
# 199 padding Secrets named to sort before every tofu-record-: page one is
# then exactly these and the sentinel. They carry no record-key annotation,
# so the store attributes them to nobody and skips them, the way it skips
# anything else an operator keeps in the namespace.
python3 - 199 "$PAGED_NS" > "$W/pad.yaml" <<'PYEOF'
import sys
n, ns = int(sys.argv[1]), sys.argv[2]
docs = ["apiVersion: v1\nkind: Secret\nmetadata:\n  name: pad-%03d\n  namespace: %s\ntype: Opaque\nstringData:\n  pad: \"%d\"\n" % (i, ns, i) for i in range(n)]
sys.stdout.write("---\n".join(docs))
PYEOF
cmd "kubectl create -f pad.yaml   # 199 Secrets named pad-000 to pad-198"
kc create -f "$W/pad.yaml" >/dev/null || fail "k8srec" "could not create the padding Secrets"
PAGED_ALL="$(kc get secrets -n "$PAGED_NS" -o name 2>&1)" \
  || fail "k8srec" "listing the padded namespace failed: $PAGED_ALL"
PAD_N="$( { grep -c '^secret/pad-' <<< "$PAGED_ALL" || true; } )"
ALL_N="$( { grep -c '^secret/' <<< "$PAGED_ALL" || true; } )"
[ "$PAD_N" = "199" ] || fail "k8srec" "$PAD_N padding Secrets exist, want 199: $PAGED_ALL"
[ "$ALL_N" -gt 200 ] || fail "k8srec" "the namespace holds $ALL_N Secrets, which one page of 200 lists whole; there is no second page to fail"
echo "Secrets in the namespace: $ALL_N ($PAD_N padding); a LIST of 200 a page needs two pages, and the estate's records are on the second" | evidence

# The proxy. Its upstream identity and trust are the kind admin's, taken out
# of the kubeconfig; its own certificate is self-signed and the run's
# kubeconfig skips verifying it.
PROXY_WORK="$W/proxy"
mkdir -p "$PROXY_WORK"
API_SERVER="$(kubectl --kubeconfig "$KUBECONFIG" config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')"
kubectl --kubeconfig "$KUBECONFIG" config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d > "$PROXY_WORK/upstream-ca.crt"
kubectl --kubeconfig "$KUBECONFIG" config view --raw --minify -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > "$PROXY_WORK/upstream-client.crt"
kubectl --kubeconfig "$KUBECONFIG" config view --raw --minify -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > "$PROXY_WORK/upstream-client.key"
for f in upstream-ca.crt upstream-client.crt upstream-client.key; do
  [ -s "$PROXY_WORK/$f" ] || fail "k8srec" "the kind kubeconfig yielded no $f, so the proxy has nothing to reach the API server with"
done
[ -n "$API_SERVER" ] || fail "k8srec" "the kind kubeconfig names no server"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$PROXY_WORK/proxy.key" -out "$PROXY_WORK/proxy.crt" -days 1 \
  -subj /CN=smoke-proxy -addext subjectAltName=IP:127.0.0.1 >/dev/null 2>&1 \
  || fail "k8srec" "openssl could not write the proxy's certificate"
python3 "$SMOKE_DIR/k8sproxy.py" "$API_SERVER" "$PROXY_WORK" 2>"$SMOKE_WORKROOT/logs/k8srec-proxy.err" &
PROXY_PID=$!
trap 'kill "$PROXY_PID" 2>/dev/null || true; cleanup' EXIT
for _ in $(seq 1 50); do [ -s "$PROXY_WORK/proxy.port" ] && break; sleep 0.1; done
[ -s "$PROXY_WORK/proxy.port" ] || fail "k8srec" "the proxy never started: $(cat "$SMOKE_WORKROOT/logs/k8srec-proxy.err")"
PROXY_KC="$W/proxy.kubeconfig"
cp "$KUBECONFIG" "$PROXY_KC"
# set-cluster with the insecure flag drops certificate-authority-data as
# well, which it has to: clientcmd refuses a cluster that carries both.
kubectl --kubeconfig "$PROXY_KC" config set-cluster "kind-$CLUSTER_NAME" \
  --server="https://127.0.0.1:$(cat "$PROXY_WORK/proxy.port")" --insecure-skip-tls-verify=true >/dev/null
[ -z "$(kubectl --kubeconfig "$PROXY_KC" config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')" ] \
  || fail "k8srec" "the proxy kubeconfig still carries the kind CA beside insecure-skip-tls-verify, which clientcmd refuses; every run through the proxy below would fail before it reached it"
cmd "kubectl --kubeconfig proxy.kubeconfig get secrets -n $PAGED_NS   # the proxy relays, as the admin"
VIA="$(kc_as "$PROXY_KC" get secrets -n "$PAGED_NS" -o name 2>&1)" \
  || fail "k8srec" "a listing through the proxy failed, so the proxy does not relay: $VIA. Proxy stderr: $(cat "$SMOKE_WORKROOT/logs/k8srec-proxy.err")"
[ "$( { grep -c '^secret/' <<< "$VIA" || true; } )" = "$ALL_N" ] \
  || fail "k8srec" "the listing through the proxy holds $( { grep -c '^secret/' <<< "$VIA" || true; } ) Secrets and the direct one $ALL_N; the proxy changes the answer"

# The control: the plan through the proxy with nothing failing is empty, and
# the wire shows the listing paging. The state cache would answer the plan
# without asking the store at all, so it goes first.
rm -f "$PAGED/.terraform/choudoufu-cache.tfstate" "$PROXY_WORK/expire"
: > "$PROXY_WORK/proxy.log"
cmd "choudoufu plan   # through the proxy, nothing failing"
PC_OUT="$( cd "$PAGED" && as_identity "$PROXY_KC" chdf plan -input=false -no-color 2>&1 )" \
  || fail "k8srec" "the control plan through the proxy failed, so the proxy itself changes the answer: $PC_OUT. Proxy stderr: $(cat "$SMOKE_WORKROOT/logs/k8srec-proxy.err")"
grep -q 'No changes' <<< "$PC_OUT" || fail "k8srec" "the control plan through the proxy is not empty, so the proxy itself changes the answer: $PC_OUT"
LIST_LINES="$( { grep -E "^GET /api/v1/namespaces/$PAGED_NS/secrets\?" "$PROXY_WORK/proxy.log" || true; } )"
LISTS="$( { grep -c . <<< "$LIST_LINES" || true; } )"
PAGES="$( { grep -c 'continue=' <<< "$LIST_LINES" || true; } )"
[ "$PAGES" -ge 1 ] \
  || fail "k8srec" "the proxy saw $LISTS Secrets LIST(s) in $PAGED_NS and none carried a continue token, so the listing never paged and a failed second page would be a failure of nothing: $LIST_LINES"
{ grep -v ' 200$' <<< "$LIST_LINES" || true; } | grep -q . \
  && fail "k8srec" "a Secrets LIST through the proxy was answered something other than 200 with no fault armed: $LIST_LINES"
echo "Secrets LISTs through the proxy: $LISTS, of which $PAGES carried a continue token, all answered 200; the plan is empty" | evidence

# listing_failed_whole <rc> <output> is step 12's check on a run made with
# the second page answering 410: the run failed, it named the listing and
# carried the API server's reason, and it printed no plan at all - not an
# empty one, not one creating anything, not one destroying anything. The
# BREAK arm runs the same function against a binary that keeps its first
# page.
listing_failed_whole() {
  local rc="$1" out="$2" text
  text="$(flat <<< "$out")"
  [ "$rc" != "0" ] \
    || fail "k8srec" "the run exited 0 with the second page of its listing answered 410, so a listing that failed part-way read as a listing: $out"
  grep -qE 'Plan: [0-9]+ to add|will be created|will be destroyed|No changes' <<< "$out" \
    && fail "k8srec" "the run printed a plan over a listing whose second page failed; whatever that plan says, it says it over fewer records than the estate has: $out"
  grep -q 'continue parameter is too old' <<< "$text" \
    || fail "k8srec" "the run failed without carrying the API server's own reason, that the continue parameter is too old: $out"
  grep -q 'listing' <<< "$text" \
    || fail "k8srec" "the refusal does not say that a listing failed: $out"
  grep -q "$PAGED_NS" <<< "$text" \
    || fail "k8srec" "the refusal does not name the namespace whose listing failed: $out"
}
# Three runs. A run lists the records namespace twice: when the store opens,
# to read its sentinel back, and for the bulk read the plan is built on. With
# no skip the 410 lands on the first; with skip 1 the proxy relays the open's
# second page and answers the bulk read's, which is the read claim 31 is
# about, so those two runs must get past opening the store and be refused
# after it.
echo "-1" > "$PROXY_WORK/expire"
for CASE in "0 plan" "1 plan" "1 plan -destroy"; do
  SKIP="${CASE%% *}"; MODE="${CASE#* }"
  if [ "$SKIP" = "0" ]; then WHERE="the store's open"; else WHERE="the bulk read"; fi
  : > "$PROXY_WORK/proxy.log"
  echo "$SKIP" > "$PROXY_WORK/skip"
  rm -f "$PAGED/.terraform/choudoufu-cache.tfstate"
  cmd "choudoufu $MODE   # the second page of $WHERE's Secrets LIST answers 410 Expired"
  # MODE is a command and its flags, so it is meant to split.
  # shellcheck disable=SC2086
  PX_OUT="$( cd "$PAGED" && as_identity "$PROXY_KC" chdf $MODE -input=false -no-color 2>&1 )" && PX_RC=0 || PX_RC=$?
  printf '%s\n' "$PX_OUT" > "$SMOKE_WORKROOT/logs/k8srec-paged-skip$SKIP-${MODE// /_}.out"
  PAGE_LINES="$( { grep -E "^GET /api/v1/namespaces/$PAGED_NS/secrets\?.*continue=" "$PROXY_WORK/proxy.log" || true; } )"
  EXPIRED="$( { grep -c ' 410$' <<< "$PAGE_LINES" || true; } )"
  RELAYED="$( { grep -c ' 200$' <<< "$PAGE_LINES" || true; } )"
  [ "$EXPIRED" -ge 1 ] \
    || fail "k8srec" "the proxy answered 410 to nothing during choudoufu $MODE, so this run measured an ordinary plan: $(cat "$PROXY_WORK/proxy.log")"
  [ "$RELAYED" = "$SKIP" ] \
    || fail "k8srec" "the proxy relayed $RELAYED later page(s) before answering 410 during choudoufu $MODE, want $SKIP: $PAGE_LINES"
  listing_failed_whole "$PX_RC" "$PX_OUT"
  if [ "$SKIP" = "1" ]; then
    grep -q 'Cannot open the record store' <<< "$PX_OUT" \
      && fail "k8srec" "with the open's listing relayed whole, choudoufu $MODE was still refused at opening the store, so the bulk read never met the 410 and this run measured the open again: $PX_OUT"
  fi
  { grep -E 'Error:' <<< "$PX_OUT" || true; } | awk 'NR<=1' | sed 's/^[^A-Za-z]*//' | evidence
  { flat <<< "$PX_OUT" | grep -oE 'staterecord: kubernetes: listing "[^"]*" in namespace "[^"]*": The provided continue parameter is too old' || true; } \
    | awk 'NR<=1' | evidence
  echo "choudoufu $MODE, 410 on $WHERE: exit $PX_RC, no plan printed; later pages relayed $RELAYED, answered 410 $EXPIRED" | evidence
done
rm -f "$PROXY_WORK/skip"
rm -f "$PROXY_WORK/expire"
# And the estate is whole: the same plan with the fault lifted is empty, so
# the refusals above were over a store holding every record.
rm -f "$PAGED/.terraform/choudoufu-cache.tfstate"
PW_OUT="$( cd "$PAGED" && as_identity "$PROXY_KC" chdf plan -input=false -no-color 2>&1 )" \
  || fail "k8srec" "the plan after the fault was lifted failed: $PW_OUT"
grep -q 'No changes' <<< "$PW_OUT" || fail "k8srec" "the plan after the fault was lifted is not empty, so a refused run above changed something: $PW_OUT"
echo "the fault lifted: the plan is empty again" | evidence
proof "three runs with the second page of a record listing answered 410 Expired: a plan whose store-open listing failed, and a plan and a destroy plan whose bulk read failed after the store had opened. Each exited non-zero naming the listing, the namespace and the API server's reason, and none printed a plan: not an empty one over the records on page one, not a create for the six it never saw. With the fault lifted the plan is empty, so the store held every record the whole time."

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
    kc_as "$PLANNER_KC" get secrets -n "$BOB_NS" >/dev/null 2>&1 && break
    sleep 1
  done
  cmd "kubectl --as the planner, now cluster-wide, get secrets -n $BOB_NS"
  CROSS="$(kc_as "$PLANNER_KC" get secrets -n "$BOB_NS" -l tofu-estate=k8srec-bob -o name 2>&1)" \
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
    kc_as "$PLANNER_KC" replace --dry-run=server -f "$W/target.json" >/dev/null 2>&1 && break
    sleep 1
  done
  cmd "kubectl --as the planner replace -f <Alice's record Secret>   # policy gone"
  WROTE="$(kc_as "$PLANNER_KC" replace -f "$W/target.json" 2>&1)" \
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
    [ "$(kc_as "$CAROL_KC" auth can-i list secrets --all-namespaces 2>/dev/null)" = "yes" ] \
      && [ "$(kc_as "$CAROL_KC" auth can-i list namespaces 2>/dev/null)" = "yes" ] && break
    sleep 1
  done
  [ "$(kc_as "$CAROL_KC" auth can-i list secrets --all-namespaces 2>/dev/null)" = "yes" ] \
    || fail "k8srec" "BREAK: Carol's secret reads were never widened, so the refusal below would measure nothing"
  [ "$(kc_as "$CAROL_KC" auth can-i list namespaces 2>/dev/null)" = "yes" ] \
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
  { grep -E 'read_isolation' <<< "$BR" || true; } | awk 'NR<=2' | evidence
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

  ### Steps 1, 2, 3, 8 and 9 (#1448, section E). Each of these arms breaks
  ### the world one way and runs the step's OWN check function against it,
  ### so what is proved is that the line the step stands on can fail. They
  ### come after the three arms above because the first of those took the
  ### boundary policy down and wrote to Alice's record Secret from a copy
  ### read in step 5; the strips below would move that object's
  ### resourceVersion out from under it.

  # must_fail_naming <words> <check> <args...> runs one of the step checks
  # in a subshell against the broken world, requires it to fail, and
  # requires its FAIL line to carry <words>: a check that failed for some
  # other reason proves nothing about the break. Its own FAIL line is the
  # evidence.
  must_fail_naming() {
    local words="$1" out; shift
    if out="$( "$@" 2>&1 )"; then
      fail "k8srec" "BREAK: $1 passed against a world built to fail it, so the step it belongs to cannot fail: $out"
    fi
    { grep -E '^FAIL \[' <<< "$out" || true; } | cut -c1-220 | evidence
    grep -qF -- "$words" <<< "$out" \
      || fail "k8srec" "BREAK: $1 failed, but not by the name expected (\"$words\"): $out"
  }
  # A kubeconfig whose server is a port nothing listens on, for the arms
  # that need a kubectl to crash. `kubectl config` only edits the file.
  NOWHERE_KC="$W/nowhere.kubeconfig"
  cp "$KUBECONFIG" "$NOWHERE_KC"
  kubectl --kubeconfig "$NOWHERE_KC" config set-cluster "kind-$CLUSTER_NAME" --server=https://127.0.0.1:1 >/dev/null

  step "BREAK control - step 1's suite run against no cluster, and short one case, must fail step 1's check"
  explain \
    "Step 1 counts the conformance cases that passed against the cluster." \
    "Here the same go test is run with the kubeconfig variable unset, so" \
    "the suite skips, and again with -run narrowed so one case never runs." \
    "Step 1's own check must refuse each by name. If it passed either," \
    "step 1 would pass on a suite that measured nothing."
  cmd "go test ./internal/live/staterecord -run TestKubernetesStore   # CHOUDOUFU_K8S_RECORD_KUBECONFIG unset"
  SKIP_OUT="$( cd "$ROOT" && env -u CHOUDOUFU_K8S_RECORD_KUBECONFIG -u CHOUDOUFU_K8S_RECORD_NAMESPACE \
    go test ./internal/live/staterecord -run TestKubernetesStore -count=1 -v 2>&1 )" \
    || fail "k8srec" "BREAK: the suite with no cluster to reach did not exit 0, so this is not the skip the control was built to catch: $SKIP_OUT"
  must_fail_naming "the conformance suite SKIPPED" conformance_verdict "$SKIP_OUT"
  cmd "go test ./internal/live/staterecord -run 'TestKubernetesStoreConformance/^[A-CE-Z]'   # the cases named D... left out"
  SHORT_OUT="$( cd "$ROOT" && CHOUDOUFU_K8S_RECORD_KUBECONFIG="$KUBECONFIG" CHOUDOUFU_K8S_RECORD_NAMESPACE="$RECORDS_NS" \
    go test ./internal/live/staterecord -run 'TestKubernetesStoreConformance/^[A-CE-Z]' -count=1 -v 2>&1 )" \
    || fail "k8srec" "BREAK: the narrowed suite failed, so this is not the short run the control was built to catch: $SHORT_OUT"
  must_fail_naming "conformance cases passed against this cluster and the shared suite has $CONFORMANCE_CASES" conformance_verdict "$SHORT_OUT"
  proof "caught, twice. A suite that skipped and a suite short of its cases each fail step 1's check by name, so the count step 1 prints is a count of cases that ran against this cluster."

  step "BREAK control - step 2's record Secrets stripped of their annotation, then their label, must fail step 2's check"
  explain \
    "Step 2 reads the estate's records back as Secrets: listed by the" \
    "tofu-estate label, named tofu-record-, and each saying which record" \
    "it is in an annotation. Here the record-key annotation is stripped" \
    "from every one of them and step 2's check must say so; then the" \
    "label is stripped and the same check must find no record at all."
  cmd "kubectl annotate secrets -n $RECORDS_NS -l tofu-estate=k8srec-alice choudoufu.intentius.io/record-key-"
  kc annotate secrets -n "$RECORDS_NS" -l tofu-estate=k8srec-alice choudoufu.intentius.io/record-key- >/dev/null \
    || fail "k8srec" "BREAK: could not strip the record-key annotation"
  must_fail_naming "carries no record-key annotation" records_are_secrets "$RECORDS_NS" k8srec-alice
  cmd "kubectl label secrets -n $RECORDS_NS -l tofu-estate=k8srec-alice tofu-estate-"
  kc label secrets -n "$RECORDS_NS" -l tofu-estate=k8srec-alice tofu-estate- >/dev/null \
    || fail "k8srec" "BREAK: could not strip the estate label"
  must_fail_naming "no record Secret carries tofu-estate=k8srec-alice" records_are_secrets "$RECORDS_NS" k8srec-alice
  proof "caught, twice. Without the annotation the Secret no longer says which record it is, and without the label it is no longer the estate's; step 2 notices each, so its reading of the records is a reading of these objects."

  step "BREAK control - a Lease, a Secret named like a lock, and a kubectl that cannot connect must each fail step 3's check"
  explain \
    "Step 3 says nothing in the records namespace is a Lease or named like" \
    "a lock. Here a Lease is put there, then a Secret whose name says lock," \
    "and step 3's check must find each. Then the same check is run through" \
    "a kubeconfig that reaches no server and must fail rather than read the" \
    "listing it never got as clean."
  cmd "kubectl apply -f - <<< 'kind: Lease ... name: tofu-lock'   # in $RECORDS_NS"
  printf 'apiVersion: coordination.k8s.io/v1\nkind: Lease\nmetadata:\n  name: tofu-lock\n  namespace: %s\nspec:\n  holderIdentity: break\n' "$RECORDS_NS" \
    | kc apply -f - >/dev/null || fail "k8srec" "BREAK: could not plant the Lease"
  must_fail_naming "a Lease exists in the records namespace" lock_free "$KUBECONFIG" "$RECORDS_NS" "$ALICE_RECORDS"
  kc delete lease tofu-lock -n "$RECORDS_NS" >/dev/null || fail "k8srec" "BREAK: could not remove the planted Lease"
  cmd "kubectl create secret generic tofu-state-lock -n $RECORDS_NS"
  kc create secret generic tofu-state-lock -n "$RECORDS_NS" >/dev/null || fail "k8srec" "BREAK: could not plant the lock-named Secret"
  must_fail_naming "is named like a lock" lock_free "$KUBECONFIG" "$RECORDS_NS" "$ALICE_RECORDS"
  kc delete secret tofu-state-lock -n "$RECORDS_NS" >/dev/null || fail "k8srec" "BREAK: could not remove the lock-named Secret"
  cmd "kubectl --kubeconfig <server: 127.0.0.1:1> get leases -n $RECORDS_NS"
  must_fail_naming "listing Leases in $RECORDS_NS failed" lock_free "$NOWHERE_KC" "$RECORDS_NS" "$ALICE_RECORDS"
  proof "caught, three times. A planted Lease and a planted lock-named Secret are each found by name, and a listing that never reached the server fails instead of reading as an empty namespace; step 3's absence is an absence somebody looked for."

  step "BREAK control - give step 8's identity the verb it lacked, and the same apply must go through and leave records behind"
  explain \
    "Step 8's refusal names update as the missing verb, and its second half" \
    "says the refusal left nothing behind. Here Dan's Role gains update and" \
    "nothing else changes: step 8's refusal check must find the apply" \
    "allowed to start, and its left-nothing-behind check must then find the" \
    "records the apply wrote. If the apply were still refused, the verb was" \
    "never what refused it. The same listing is then run through a" \
    "kubeconfig that reaches no server and must fail rather than read" \
    "nothing as nothing left behind."
  kc create role dan-records-update -n "$DAN_NS" --verb=update --resource=secrets >/dev/null \
    || fail "k8srec" "BREAK: could not create Dan's update Role"
  kc create rolebinding dan-records-update -n "$DAN_NS" --role=dan-records-update --serviceaccount=default:dan >/dev/null \
    || fail "k8srec" "BREAK: could not bind Dan's update Role"
  for _ in $(seq 1 30); do
    [ "$(kc_as "$DAN_KC" auth can-i update secrets -n "$DAN_NS" 2>/dev/null)" = "yes" ] && break
    sleep 1
  done
  [ "$(kc_as "$DAN_KC" auth can-i update secrets -n "$DAN_NS" 2>/dev/null)" = "yes" ] \
    || fail "k8srec" "BREAK: Dan still may not update secrets, so the apply below would be refused for the reason step 8 already measured"
  cmd "choudoufu apply   # as Dan, now holding all five verbs"
  must_fail_naming "was allowed to start" refused_short_one_verb "$DAN_KC" "$DAN"
  cmd "kubectl get secrets -n $DAN_NS   # the apply that went through left its records"
  must_fail_naming "the refused first contact left secret/" left_nothing_behind "$KUBECONFIG" "$DAN_NS"
  cmd "kubectl --kubeconfig <server: 127.0.0.1:1> get secrets -n $DAN_NS"
  must_fail_naming "listing Secrets in $DAN_NS failed" left_nothing_behind "$NOWHERE_KC" "$DAN_NS"
  proof "caught, three times. With update granted the identical apply is allowed to start and leaves record Secrets in Dan's namespace, which step 8's two checks each refuse; and a listing that never reached the server fails by name. Step 8's refusal was the missing verb and its empty namespace was an emptiness somebody read."

  step "BREAK control - a write between step 9's two dumps, an empty namespace, and a kubectl that cannot connect must each fail step 9's check"
  explain \
    "Step 9 diffs the record Secrets' resourceVersions before and after a" \
    "plan and requires both dumps to be as long as the namespace's record" \
    "count. Here a record is annotated between the two dumps and the diff" \
    "must fail; a namespace holding no record is dumped and the length" \
    "guard must fail by name; and the dump is taken through a kubeconfig" \
    "that reaches no server and must fail rather than dump nothing."
  rv_snapshot "$KUBECONFIG" "$CAROL_NS" "$W/rv-break-before" before "$CAROL_RECORDS"
  cmd "kubectl annotate secrets -n $CAROL_NS -l tofu-estate=k8srec-carol moved=between-the-dumps   # then dump again"
  kc annotate secrets -n "$CAROL_NS" -l tofu-estate=k8srec-carol moved=between-the-dumps --overwrite >/dev/null \
    || fail "k8srec" "BREAK: could not write to Carol's record Secrets"
  rv_snapshot "$KUBECONFIG" "$CAROL_NS" "$W/rv-break-after" after "$CAROL_RECORDS"
  must_fail_naming "resourceVersion moved across a plan" rv_unchanged "$W/rv-break-before" "$W/rv-break-after"
  EMPTY_NS="tofu-records-k8srec-empty"
  kc create namespace "$EMPTY_NS" >/dev/null || fail "k8srec" "BREAK: could not create the empty records namespace"
  cmd "kubectl get secrets -n $EMPTY_NS -o jsonpath=...   # a namespace holding no record"
  must_fail_naming "the before dump holds 0 resourceVersions" rv_snapshot "$KUBECONFIG" "$EMPTY_NS" "$W/rv-empty" before "$CAROL_RECORDS"
  cmd "kubectl --kubeconfig <server: 127.0.0.1:1> get secrets -n $CAROL_NS -o jsonpath=..."
  must_fail_naming "resourceVersions before the plan failed" rv_snapshot "$NOWHERE_KC" "$CAROL_NS" "$W/rv-nowhere" before "$CAROL_RECORDS"
  proof "caught, three times. One write between the dumps is a moved resourceVersion the diff refuses, a namespace holding no record is a dump too short to diff, and a dump that never reached the server fails by name. Step 9's unchanged pair is a pair that was read."

  ### Steps 11 and 12 (#1441). Each rebuilds choudoufu with go build
  ### -overlay, the way claims 30 and 31 do on the bucket, and runs the
  ### step's own check function against what the broken binary does.
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "k8srec" "BREAK=1 rebuilds choudoufu from this checkout for steps 11 and 12; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "k8srec" "BREAK=1 needs Go to build the broken binaries for steps 11 and 12"

  step "BREAK control - a waiver warning that goes quiet once the estate has a cache must fail step 11's check"
  explain \
    "The corruption is in the binary, so it is built with go build" \
    "-overlay and the source tree is never touched: the same edit claim" \
    "30's bucket scenario makes, the warning emitted only while the estate" \
    "has no state cache yet, which is to say on its first run and never" \
    "again. A fresh estate's first apply under that binary must still" \
    "warn, and its plan must then be refused by step 11's check for" \
    "saying nothing. A check that looked at one run would pass it."
  WSRC="$ROOT/internal/command/live_mode.go"
  mkdir -p "$W/break30"
  sed 's|if !r.waiverWarned {|if _, quietErr := os.Stat(".terraform/choudoufu-cache.tfstate"); !r.waiverWarned \&\& quietErr != nil {|' "$WSRC" > "$W/break30/live_mode.go"
  cmp -s "$WSRC" "$W/break30/live_mode.go" \
    && fail "k8srec" "BREAK: the break patch changed nothing in $WSRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$WSRC" "$W/break30/live_mode.go" > "$W/break30/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # the waiver warning, first run only"
  ( cd "$ROOT" && go build -overlay "$W/break30/overlay.json" -o "$W/break30/choudoufu" ./cmd/choudoufu ) \
    || fail "k8srec" "BREAK: the broken binary for step 11 did not build"
  QUIET_NS="tofu-records-k8srec-quiet"
  QUIET="$W/quiet"
  kc create namespace "$QUIET_NS" >/dev/null || fail "k8srec" "BREAK: could not create the quiet estate's records namespace"
  mkdir -p "$QUIET"
  versions k8srec-quiet "$QUIET_NS" "$QUIET"
  cp "$WAIVED/main.tf" "$QUIET/main.tf"
  ( cd "$QUIET" && no_aws "$W/break30/choudoufu" init -input=false -no-color >/dev/null ) \
    || fail "k8srec" "BREAK: the quiet estate's init failed"
  cmd "choudoufu apply -auto-approve   # the broken binary's first run: no cache yet, so it still warns"
  BQ1="$( cd "$QUIET" && no_aws "$W/break30/choudoufu" apply -auto-approve -input=false -no-color 2>&1 )" \
    || fail "k8srec" "BREAK: the broken binary's first apply failed: $BQ1"
  waived_named "BREAK run 1, the first apply" "$BQ1"
  [ -f "$QUIET/.terraform/choudoufu-cache.tfstate" ] \
    || fail "k8srec" "BREAK: the first apply left no state cache at .terraform/choudoufu-cache.tfstate, which is what the broken binary goes quiet on; whatever the plan below says, it says for a reason that is not the break"
  cmd "choudoufu plan   # the broken binary's second run"
  BQ2="$( cd "$QUIET" && no_aws "$W/break30/choudoufu" plan -input=false -no-color 2>&1 )" \
    || fail "k8srec" "BREAK: the broken binary's plan failed: $BQ2"
  grep -q 'No changes' <<< "$BQ2" || fail "k8srec" "BREAK: the broken binary's plan is not empty: $BQ2"
  must_fail_naming "said nothing about the read_isolation waiver" waived_named "BREAK run 2, a plan" "$BQ2"
  proof "caught - the same binary named all three waivers on the estate's first run and none on its second, and step 11's check refused the second run by name. A waiver that goes quiet is what the every-run check exists to see."

  step "BREAK control - a listing that keeps its first page and drops the error must be refused at the plan by step 12's check"
  explain \
    "The corruption is in the binary: the store's paged LIST, on a page" \
    "that fails after the first, breaks out of its loop with the pages it" \
    "has instead of failing the call. Built with go build -overlay, so the" \
    "source tree is never touched. With the fault re-armed, that binary's" \
    "first page holds the sentinel and the 199 padding Secrets, so the" \
    "sentinel guard is satisfied, and the estate reads as holding no" \
    "record at all: the plan it prints proposes creating all six" \
    "resources, which exist. Step 12's check must refuse that run by" \
    "name; if it did not, step 12 would be passing for a reason other" \
    "than the listing failing whole."
  SRC="$ROOT/internal/live/staterecord/kubernetes.go"
  mkdir -p "$W/break31"
  python3 - "$SRC" "$W/break31/kubernetes.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
refuse = """\t\tif err != nil {
\t\t\tif notFoundIsNamespace(err) {
\t\t\t\treturn nil, nil, &NamespaceMissingError{Namespace: s.namespace, Err: err}
\t\t\t}
\t\t\treturn nil, nil, s.classify("listing", keyPrefix, err)
\t\t}
"""
assert src.count(refuse) == 1, "the break patch no longer matches KubernetesStore.list's failed-page branch"
keep = """\t\tif err != nil {
\t\t\tif cont != "" {
\t\t\t\tbreak
\t\t\t}
\t\t\tif notFoundIsNamespace(err) {
\t\t\t\treturn nil, nil, &NamespaceMissingError{Namespace: s.namespace, Err: err}
\t\t\t}
\t\t\treturn nil, nil, s.classify("listing", keyPrefix, err)
\t\t}
"""
open(sys.argv[2], "w").write(src.replace(refuse, keep))
PYEOF
  [ -s "$W/break31/kubernetes.go" ] || fail "k8srec" "BREAK: the break patch did not apply to $SRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$W/break31/kubernetes.go" > "$W/break31/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # a failed second page keeps the first"
  ( cd "$ROOT" && go build -overlay "$W/break31/overlay.json" -o "$W/break31/choudoufu" ./cmd/choudoufu ) \
    || fail "k8srec" "BREAK: the broken binary for step 12 did not build"
  echo "-1" > "$PROXY_WORK/expire"
  : > "$PROXY_WORK/proxy.log"
  rm -f "$PAGED/.terraform/choudoufu-cache.tfstate"
  cmd "choudoufu plan   # the broken binary, through the proxy, every second page answering 410"
  BP_OUT="$( cd "$PAGED" && as_identity "$PROXY_KC" "$W/break31/choudoufu" plan -input=false -no-color 2>&1 )" && BP_RC=0 || BP_RC=$?
  BP_EXPIRED="$( { grep -c ' 410$' "$PROXY_WORK/proxy.log" || true; } )"
  [ "$BP_EXPIRED" -ge 1 ] \
    || fail "k8srec" "BREAK: the proxy answered 410 to nothing, so the broken binary's run measured an ordinary plan: $(cat "$PROXY_WORK/proxy.log")"
  { grep -E 'will be created|^Plan:' <<< "$BP_OUT" || true; } | awk 'NR<=3' | evidence
  grep -q 'Plan: 6 to add' <<< "$BP_OUT" \
    || fail "k8srec" "BREAK: the binary built to keep its first page did not propose creating all six resources (exit $BP_RC), so the break did not take and this control proves nothing: $BP_OUT"
  must_fail_naming "the run exited 0 with the second page of its listing answered 410" listing_failed_whole "$BP_RC" "$BP_OUT"
  rm -f "$PROXY_WORK/expire"
  proof "caught - with the error dropped, the first page read as the whole estate: the sentinel was on it, no record was, and the plan proposed creating all six resources that exist. Step 12's check refused that run by name, and that plan is the harm the claim is about."

  echo
  echo "BREAK [k8srec]: every control held; each break was caught by the check it was built for"
fi
