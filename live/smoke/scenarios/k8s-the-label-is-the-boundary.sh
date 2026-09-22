# k8s-the-label-is-the-boundary
# CLAIM 23 - The label is the boundary: the plan refuses a block that declares another estate's object before any cluster is consulted, and one admission policy on the estate label fences every write to an estate's objects, cluster-wide, so a principal is refused on another estate's object by the API server itself, a carve is one governed relabel (live-mv -from-estate, the same command as the AWS retag), a rename is a config edit with nothing to write, and handover is an RBAC change. ~5 min.
#
# The Kubernetes sibling of claim 13 (#1066, under #1016's ruling). RBAC has
# no attribute predicate, so the label is advisory until something fences on
# it; that something is live/kubernetes/estate-boundary.yaml, one
# ValidatingAdmissionPolicy a cluster admin installs once, whose CEL reads
# tofu-estate off oldObject and object and asks the authorizer whether the
# caller holds "use" on a virtual resource named after the estate. The
# grant is then an ordinary ClusterRole (live/kubernetes/estate-grant.yaml)
# and handover is a binding moving, not an edit to the policy.
#
# Said in the headline, not a caveat: admission sees create, update and
# delete and never get or list, so this fence is write-only where an IAM
# condition can fence a describe; it fences the object and not its
# subresources; and the policy is one cluster-wide object with a wider
# blast radius than two IAM changes. BREAK=1 removes the policy and requires
# the same writes it refused to go through - Bob's apply and plain kubectl
# on Alice's estate, and Alice's live-mv into an estate she was never
# granted: if the API server still said no, something other than the
# policy was the fence.
#
# Step 4 is the half that is not the cluster's (#1108). A block declaring
# an object another estate owns is refused by the PLAN, by name, in the
# sentence a declared AWS resource carrying another estate's tofu-estate
# tag gets - and the BREAK arm runs that same step again with the policy
# deleted, where it must still hold. "Never write a wrong marker" is a
# property of the plan; the policy is what stops everything that never
# went through choudoufu at all.
#
# Steps 8, 9 and 10 are #1449's ruling: owned objects keep their estate.
# The policy used to skip itself entirely for any object carrying an
# ownerReference, and ownerReferences is a field the caller writes, so
# Alice could add an owner to her own app object and then relabel it into
# net, or create an object already labelled net and already owned. Both
# arms are here beside the refused twin the issue's table puts them next
# to, and beside the write the ruling does allow: an update to an owned
# object that leaves its tofu-estate label alone goes through with no
# grant at all, which is what a third-party operator's writes on a
# labelled child need. Step 10 is the cost of that, measured rather than
# assumed: a Deployment whose pod template carries the label still gets
# its ReplicaSet and its Pods, because those copies are written by
# kube-system controllers, which the first match condition exempts.
#
# The carve is live-mv's Kubernetes leg (#1081's fifth item): with no
# address on the object a rename within one estate has nothing governed to
# write and live-mv says so, exit 0 (step 12); a move between estates is the
# one tofu-estate label write, which live-mv -from-estate makes through the
# provider under the caller's own ServiceAccount, so the policy judges it
# exactly as it judges a plain kubectl label (steps 13 and 14).

W="$SMOKE_WORKROOT/k8s-boundary"; APP="$W/app"; NET="$W/net"; DATA="$W/data"; LOGS="$W/logs"
mkdir -p "$APP" "$NET" "$DATA" "$LOGS"
SMOKE_WORK="$W"; export SMOKE_WORK
PNS="choudoufu-smoke"   # the namespace the two ServiceAccounts live in

versions() { cat <<EOF
terraform {
  required_version = ">= 1.5.0"
  live {
    estate = "$1"
  }
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}
provider "kubernetes" {}
EOF
}
versions app > "$APP/versions.tf"
cat > "$APP/main.tf" <<'TF'
resource "kubernetes_namespace" "boundary" {
  metadata {
    name = "boundary"
  }
}

resource "kubernetes_config_map" "gateway" {
  metadata {
    name      = "gateway"
    namespace = "boundary"
  }
  data = { greeting = "gateway" }
  depends_on = [kubernetes_namespace.boundary]
}
TF
# The object that will be carved out, in a file of its own so that moving
# the block between roots is moving the file.
cat > "$APP/database.tf" <<'TF'
resource "kubernetes_config_map" "database" {
  metadata {
    name      = "database"
    namespace = "boundary"
  }
  data = { greeting = "database" }
  depends_on = [kubernetes_namespace.boundary]
}
TF
versions net > "$NET/versions.tf"
cat > "$NET/main.tf" <<'TF'
resource "kubernetes_namespace" "net" {
  metadata {
    name = "net"
  }
}

resource "kubernetes_config_map" "router" {
  metadata {
    name      = "router"
    namespace = "net"
  }
  data = { greeting = "router" }
  depends_on = [kubernetes_namespace.net]
}
TF
versions data > "$DATA/versions.tf"
: > "$DATA/main.tf"

cluster_up

# principal makes one ServiceAccount, mints it a token, and writes it a
# kubeconfig of its own beside the admin's, so a step can run as it.
principal() {
  kc create serviceaccount "$1" -n "$PNS" >/dev/null || fail "boundary" "could not create ServiceAccount $1"
  local tok
  tok="$(kc create token "$1" -n "$PNS" --duration=2h)" || fail "boundary" "could not mint a token for $1"
  cp "$KUBECONFIG" "$W/$1.kubeconfig"
  kubectl --kubeconfig "$W/$1.kubeconfig" config set-credentials "$1" --token="$tok" >/dev/null
  kubectl --kubeconfig "$W/$1.kubeconfig" config set-context --current --user="$1" >/dev/null
}
# as_role runs one command under a principal's own kubeconfig, in a
# subshell so nothing leaks back into the admin-level steps. KUBECONFIG is
# what kubectl reads and KUBE_CONFIG_PATH is what the provider and the
# sweep read; both point at the same file.
as_role() {
  local who="$1"; shift
  ( export KUBECONFIG="$W/$who.kubeconfig" KUBE_CONFIG_PATH="$W/$who.kubeconfig"; "$@" )
}
# grant applies live/kubernetes/estate-grant.yaml for estate $1 to
# ServiceAccount $2: the ClusterRole naming the estate and the binding.
grant() {
  sed -e "s/ESTATE/$1/g" -e "s/PRINCIPAL_NAMESPACE/$PNS/g" -e "s/PRINCIPAL/$2/g" "$ROOT/live/kubernetes/estate-grant.yaml" \
    | kc apply -f - >/dev/null || fail "boundary" "could not grant estate $1 to $2"
}
# can_use asks the API server's own authorizer the exact question the
# policy asks: may principal $1 "use" estate $2. Prints true or false.
can_use() {
  kc create -o jsonpath='{.status.allowed}' -f - <<EOF
apiVersion: authorization.k8s.io/v1
kind: SubjectAccessReview
spec:
  user: system:serviceaccount:$PNS:$1
  resourceAttributes:
    group: choudoufu.intentius.io
    resource: estates
    name: $2
    verb: use
EOF
}
labels_of() { kc get configmap "$1" -n "$2" -o jsonpath='{.metadata.labels}{"\n"}'; }
greeting_of() { kc get configmap "$1" -n "$2" -o jsonpath='{.data.greeting}{"\n"}'; }
# set_greeting rewrites one greeting value in a root's file without sed -i,
# whose in-place flag differs between BSD and GNU sed.
set_greeting() { local f="$1" from="$2" to="$3" t; t="$(mktemp)"
  sed "s/greeting = \"$from\"/greeting = \"$to\"/" "$f" > "$t" && mv "$t" "$f" || fail "boundary" "could not rewrite $f"
  grep -qF "greeting = \"$to\"" "$f" || fail "boundary" "the edit $from -> $to did not land in $f"; }
# wrong_estate_plan manufactures the shape GitHub issue #1108 is about and
# asserts the PLAN's own verdict on it: a block in net/ declaring, by
# namespace and name, the ConfigMap that already exists carrying
# tofu-estate=app. The plan must refuse it by name, say which estate does
# own it, propose only the create the block declares, and leave the live
# object alone - and it must do all of that with nothing in the cluster
# consulted, which is why it is called twice: once with the policy
# installed and once, in the BREAK arm, with the policy gone. $1 names the
# arm, for the failure message and the saved plan only; the verdict must be
# identical either way. The block is removed again before anything is
# asserted, so a failure leaves net/ as it was found.
wrong_estate_plan() {
  local arm="$1" out flat
  cat > "$NET/foreign.tf" <<'TF'
resource "kubernetes_config_map" "foreign" {
  metadata {
    name      = "gateway"
    namespace = "boundary"
  }
  data = { greeting = "hijacked" }
}
TF
  out="$(cd "$NET" && as_role bob chdf plan -input=false -no-color 2>&1 || true)"
  printf '%s\n' "$out" > "$LOGS/wrong-estate-$arm.plan"
  rm -f "$NET/foreign.tf"
  flat="$(tr '\n' ' ' <<< "$out" | tr -s ' ')"
  grep -q 'kubernetes_config_map.foreign' <<< "$out" \
    || fail "boundary" "$arm: the plan never mentions the block that declares another estate's object (full plan in $LOGS/wrong-estate-$arm.plan)"
  grep -q 'carries tofu-estate="app"' <<< "$flat" \
    || fail "boundary" "$arm: the plan does not refuse the object by the estate it carries - it was bound by name and the plan would relabel it (full plan in $LOGS/wrong-estate-$arm.plan): $(grep -E '^Plan:|~ |will be' <<< "$out" | head -5)"
  grep -q 'belongs to another estate and nothing in this plan reads, changes or destroys it' <<< "$flat" \
    || fail "boundary" "$arm: the refusal is not the sentence a declared AWS resource carrying another estate's tag gets: $(grep -i 'another estate' <<< "$out" | head -3)"
  grep -qE '^Plan: 1 to add, 0 to change, 0 to destroy' <<< "$out" \
    || fail "boundary" "$arm: the plan is not a single create of what the block declares (full plan in $LOGS/wrong-estate-$arm.plan): $(grep -E '^Plan:' <<< "$out")"
  grep -q '"tofu-estate":"app"' <<< "$(labels_of gateway boundary)" \
    || fail "boundary" "$arm: the gateway ConfigMap no longer carries app's label: $(labels_of gateway boundary)"
  [ "$(greeting_of gateway boundary)" != "hijacked" ] \
    || fail "boundary" "$arm: the refused block's own data reached the live object"
  grep -E 'IN_THE_WAY|^Plan:' <<< "$out" | head -2 | evidence
  refusal_sentence "$out" | evidence
}
# refusal_sentence pulls the one wrapped sentence the plan prints about an
# object another estate owns back onto a single line, for the evidence.
refusal_sentence() { tr '\n' ' ' <<< "$1" | tr -s ' ' | grep -o 'A live kubernetes_config_map[^.]*carries tofu-estate="app"[^.]*\.' | head -1; }

# denied fails the scenario unless $1 (a captured command output) carries
# the API server's own refusal from this policy. The tool never says no
# here; admission does, and it names the policy in the message.
denied() { grep -q "ValidatingAdmissionPolicy 'choudoufu-estate-boundary'" <<< "$1"; }
refusal_line() { { sed 's/\x1b\[[0-9;]*m//g' <<< "$1" | grep -o "denied request: .*" || true; } | head -1; }

# refused_by_policy fails the scenario unless $2 is THIS policy refusing
# $1, in the policy's own words. Both halves are the assertion. A bad
# kubeconfig, an expired token, a missing namespace or an RBAC gap also
# make a kubectl call fail, and "the call failed" would be satisfied by
# every one of them, so $3 is the sentence the policy itself writes:
# "is not bound to it" for the estate a write would enter,
# "is not bound to that estate" for the estate it is leaving.
refused_by_policy() {
  local what="$1" out="$2" phrase="$3"
  denied "$out" \
    || fail "boundary" "$what: nothing in the output is a refusal from ValidatingAdmissionPolicy 'choudoufu-estate-boundary': $out"
  grep -qF "$phrase" <<< "$out" \
    || fail "boundary" "$what: refused, but not with the policy's own words (\"$phrase\"), so what said no was not the check this step is about: $out"
  refusal_line "$out" | evidence
}

# OWNER_UID is the boundary Namespace's uid, read once in step 8 and
# asserted non-empty there. Every ownerReference below names that
# Namespace, which is what #1449's reproduction used: a namespaced object
# owned by a cluster-scoped one is legal and the garbage collector leaves
# it alone.
OWNER_UID=""
owner_lines() { cat <<EOF
  ownerReferences:
    - apiVersion: v1
      kind: Namespace
      name: boundary
      uid: $OWNER_UID
EOF
}
# cm_yaml prints a ConfigMap manifest for the boundary namespace: $1 name,
# $2 estate label, and "owned" as $3 to carry the ownerReference.
cm_yaml() {
  cat <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: $1
  namespace: boundary
  labels:
    tofu-estate: $2
EOF
  if [ "${3:-}" = "owned" ]; then owner_lines; fi
  cat <<EOF
data:
  greeting: $1
EOF
}
# record_secret_yaml prints a Secret shaped like one the Kubernetes record
# store writes for estate $1 - the name prefix, the managed-by and
# record-namespace labels, the record-key annotation - carrying an
# ownerReference. This is #1449's third measured row: a planted record.
record_secret_yaml() { cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: tofu-record-0e4d1c9a7f2b5a6c8d3e1f0a9b8c7d6e
  namespace: boundary
  labels:
    tofu-estate: $1
    app.kubernetes.io/managed-by: choudoufu
    choudoufu.intentius.io/record-namespace: tofu-records
  annotations:
    choudoufu.intentius.io/record-key: tofu-records/$1/default.tfstate
$(owner_lines)
type: Opaque
stringData:
  tfstate: planted
EOF
}
# obj_labels prints one object's whole label map: $1 kind, $2 name, $3
# namespace.
obj_labels() { kc get "$1" "$2" -n "$3" -o jsonpath='{.metadata.labels}{"\n"}'; }

step "the claim"
explain \
  "In stock, who owns an object is a line in a state file, and no policy" \
  "in the cluster can gate an edit to it. Here ownership is one label on" \
  "the object, and every write to that object passes through admission," \
  "where a policy can read the label and ask the API server's own" \
  "authorizer whether the caller holds the estate. RBAC alone cannot say" \
  "that: a PolicyRule has verbs, groups, resources and names, and no" \
  "predicate on a label. So the fence is one ValidatingAdmissionPolicy," \
  "installed once, cluster-wide, by a cluster admin, and the grant is an" \
  "ordinary ClusterRole naming the estate. The fence binds the credential," \
  "not the binary: a plain kubectl call under the same ServiceAccount is" \
  "refused or let through by the identical policy, with no choudoufu" \
  "anywhere in the call. Two ServiceAccounts hold two estates here. Each" \
  "converges its own; each is refused on the other's, by the API server." \
  "Then one object is carved from one estate into a new one by a relabel" \
  "the policy governs, and handover is a binding moving. What this fence" \
  "is not, said now: it is write-only, admission never sees a get or a" \
  "list; it fences the object and not its scale or status subresource;" \
  "and it is one shared cluster object rather than two IAM changes."

step "1. the fence, installed once by a cluster admin"
explain \
  "live/kubernetes/estate-boundary.yaml is the whole gate: a policy whose" \
  "CEL reads tofu-estate off the object a write is about to change and off" \
  "the object it would produce, and asks the authorizer whether the caller" \
  "may \"use\" estates.choudoufu.intentius.io/<that estate>. No such" \
  "resource exists; the verb lives only in RBAC, which is the point. The" \
  "control plane is exempt - nodes, the kube-system controllers, the" \
  "scheduler and the API server itself - and an object that already" \
  "carries an ownerReference may be updated with no grant while its" \
  "tofu-estate label stays as it was. Nothing else is exempt."
cmd "kubectl apply -f live/kubernetes/estate-boundary.yaml   # as the cluster admin"
kc apply -f "$ROOT/live/kubernetes/estate-boundary.yaml" >/dev/null || fail "boundary" "could not install the policy"
for i in $(seq 1 30); do
  OBS="$(kc get validatingadmissionpolicy choudoufu-estate-boundary -o jsonpath='{.status.observedGeneration}' 2>/dev/null || true)"
  [ -n "$OBS" ] && break; sleep 1
done
[ -n "$OBS" ] || fail "boundary" "the API server never observed the policy"
TC="$(kc get validatingadmissionpolicy choudoufu-estate-boundary -o jsonpath='{.status.typeChecking.expressionWarnings}')"
[ -z "$TC" ] || fail "boundary" "the policy's CEL has type-check warnings: $TC"
GEN="$(kc get validatingadmissionpolicy choudoufu-estate-boundary -o jsonpath='{.metadata.generation}')" \
  || fail "boundary" "could not read the policy's generation back"
[ -n "$GEN" ] && [ "$OBS" = "$GEN" ] \
  || fail "boundary" "the API server has observed generation '$OBS' of the policy and its current generation is '$GEN'; the record store's cluster contract reads exactly these two fields, and a policy whose status lags its spec is not yet judging anything"
kc get validatingadmissionpolicy,validatingadmissionpolicybinding choudoufu-estate-boundary -o name | evidence
echo "observedGeneration $OBS == generation $GEN; status.typeChecking.expressionWarnings: none" | evidence
proof "one policy and one binding, cluster-wide, with no type-check warning. Nothing about an estate is in them; the estate comes from the label and the grant."

step "2. two ServiceAccounts, two estates, one grant shape"
explain \
  "Alice holds app; Bob holds net. Each grant is" \
  "live/kubernetes/estate-grant.yaml with the estate name filled in: a" \
  "ClusterRole allowing \"use\" on estates.choudoufu.intentius.io named" \
  "<estate>, bound to the ServiceAccount. Both also hold ordinary RBAC for" \
  "the kinds their estates declare, and reads on everything, which is what" \
  "a plan's sweep needs. Nothing here is a choudoufu feature; it is RBAC" \
  "answering a question admission asks."
cmd "kubectl create serviceaccount alice ; sed s/ESTATE/app/ estate-grant.yaml | kubectl apply -f -   # and bob, net"
kc create namespace "$PNS" >/dev/null || fail "boundary" "could not create $PNS"
principal alice; principal bob
cat <<'EOF' | kc apply -f - >/dev/null || fail "boundary" "could not create the operator role"
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: choudoufu-estate-operator
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["namespaces", "configmaps", "secrets"]
    verbs: ["create", "update", "patch", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["create", "update", "patch", "delete"]
EOF
for who in alice bob; do
  kc create clusterrolebinding "choudoufu-estate-operator-$who" --clusterrole=choudoufu-estate-operator --serviceaccount="$PNS:$who" >/dev/null \
    || fail "boundary" "could not bind the operator role to $who"
done
grant app alice
grant net bob
kc get clusterrole choudoufu-estate-app -o jsonpath='{.rules[0]}{"\n"}' | evidence
for spec in "alice app" "bob app" "bob net" "alice data"; do
  read -r who est <<< "$spec"
  echo "may $who use estate $est: $(can_use "$who" "$est")" | evidence
done
[ "$(can_use alice app)" = "true" ] || fail "boundary" "alice does not hold app"
[ "$(can_use bob app)" = "false" ] || fail "boundary" "bob holds app; the fence below would prove nothing"
[ "$(can_use bob net)" = "true" ] || fail "boundary" "bob does not hold net"
[ "$(can_use alice data)" = "false" ] || fail "boundary" "alice already holds data; the carve below would prove nothing"
WHO="$(as_role alice kc auth whoami -o jsonpath='{.status.userInfo.username}')" || fail "boundary" "alice's token cannot identify itself"
echo "$WHO" | evidence
proof "two ServiceAccounts hold two estates, and the authorizer answers the exact question the policy will ask: alice may use app, bob may not."

step "3. each principal stands its own estate up"
explain \
  "Alice applies app: a namespace and two ConfigMaps. Bob applies net: a" \
  "namespace and one ConfigMap. Every create carries tofu-estate on the" \
  "object, so admission reads it and asks whether the caller holds that" \
  "estate; each does, and the writes go through."
cmd "choudoufu apply -auto-approve   # in app/ as alice, then in net/ as bob"
( cd "$APP" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "init failed in app"
( cd "$NET" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "init failed in net"
OUT="$(cd "$APP" && as_role alice chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "Alice's apply of app failed: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
grep -E 'Apply complete!' <<< "$OUT" | evidence
grep -q 'Apply complete! Resources: 3 added' <<< "$OUT" || fail "boundary" "app did not report 3 added: $OUT"
OUT="$(cd "$NET" && as_role bob chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "Bob's apply of net failed: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
grep -E 'Apply complete!' <<< "$OUT" | evidence
grep -q 'Apply complete! Resources: 2 added' <<< "$OUT" || fail "boundary" "net did not report 2 added: $OUT"
labels_of database boundary | evidence
grep -q '"tofu-estate":"app"' <<< "$(labels_of database boundary)" || fail "boundary" "the database ConfigMap does not carry tofu-estate=app"
grep -q '"tofu-estate":"net"' <<< "$(labels_of router net)" || fail "boundary" "the router ConfigMap does not carry tofu-estate=net"
proof "two estates on one cluster, each stood up by the principal that holds it, and the label the policy reads is on every object."

step "4. a block declaring an object another estate owns - the PLAN refuses it, with nothing in the cluster consulted"
explain \
  "Everything else in this scenario is the API server saying no. This" \
  "step is the half that has to hold when no policy is installed at all." \
  "A block in net/ declares the ConfigMap Alice's estate owns, by its" \
  "namespace and name, which is how every Kubernetes object binds. The" \
  "plan must refuse it by name, say which estate does own it, propose" \
  "only the create the block declares, and leave the live object alone -" \
  "the same verdict, in the same sentence, that a declared AWS resource" \
  "carrying another estate's tofu-estate tag gets, and it must not lean" \
  "on the policy to get there. Before #1108 the projection read no label" \
  "at all on this substrate: the plan bound the object by name and" \
  "proposed relabelling it to net, and admission was the only thing" \
  "between that plan and a wrong marker written over Alice's."
cmd "(a block in net/ naming boundary/gateway) ; choudoufu plan   # in net/, as bob"
wrong_estate_plan with-policy
proof "the plan refuses the object by name, names app as its owner, and proposes one create and nothing else. No write was attempted, so nothing in the cluster was asked."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - remove the policy; the cross-estate writes must go through"
  explain \
    "The claim is that the POLICY is the boundary, not the credentials or" \
    "RBAC. Delete it, then have Bob change Alice's ConfigMap through" \
    "choudoufu and, tool-less, with a plain kubectl label. If the API" \
    "server still refuses either, something other than the policy was" \
    "fencing him and the claim proves nothing. Both must succeed."
  cmd "kubectl delete validatingadmissionpolicy choudoufu-estate-boundary ; sed greeting=gateway-v2 ; choudoufu apply -auto-approve   # in app/, as bob"
  kc delete validatingadmissionpolicy choudoufu-estate-boundary >/dev/null || fail "boundary" "BREAK: could not remove the policy"
  kc delete validatingadmissionpolicybinding choudoufu-estate-boundary >/dev/null || fail "boundary" "BREAK: could not remove the binding"
  sleep 3

  step "BREAK control - the plan's own refusal must NOT go through: step 4 again, policy gone"
  explain \
    "Every other assertion in this arm requires a write the main run" \
    "refused to succeed. This one is the exception, and it is the point of" \
    "step 4: the plan's refusal of another estate's object is the plan's," \
    "not the cluster's, so removing the policy must change nothing about" \
    "it. If the refusal disappeared with the policy, the plan side was" \
    "leaning on admission and #1108 is not fixed."
  cmd "(the same block in net/) ; choudoufu plan   # in net/, as bob, policy gone"
  wrong_estate_plan no-policy
  proof "identical to step 4 with the policy installed: refused by name, one create proposed, Alice's object untouched. The plan never asked the cluster who may write it."

  set_greeting "$APP/main.tf" gateway gateway-v2
  OUT="$(cd "$APP" && as_role bob chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "BREAK: with no policy, Bob's write on Alice's estate was still refused: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
  denied "$OUT" && fail "boundary" "BREAK: the apply succeeded but the output still carries a refusal: $OUT"
  [ "$(greeting_of gateway boundary)" = "gateway-v2" ] || fail "boundary" "BREAK: Bob's write did not land"
  greeting_of gateway boundary | evidence
  proof "caught - with the policy gone, Bob wrote Alice's estate through choudoufu. The policy was the whole boundary, and it can be taken down as well as put up."

  step "BREAK control (cont'd) - the tool-less write goes through too, with no choudoufu in the call"
  cmd "kubectl label configmap database -n boundary owner=bob   # no choudoufu, as bob, policy gone"
  OUT="$(as_role bob kc label configmap database -n boundary owner=bob 2>&1)" || fail "boundary" "BREAK: with no policy, Bob's tool-less write on Alice's estate was still refused: $OUT"
  denied "$OUT" && fail "boundary" "BREAK: the tool-less write succeeded but the output still carries a refusal: $OUT"
  grep -q '"owner":"bob"' <<< "$(labels_of database boundary)" || fail "boundary" "BREAK: Bob's tool-less write did not land"
  labels_of database boundary | evidence
  proof "caught - with the policy gone, plain kubectl wrote Alice's object with no choudoufu anywhere in the call. The policy was the boundary, for the tool and for a script alike."

  step "BREAK control (cont'd) - live-mv into an estate the caller was never granted goes through too"
  explain \
    "The carve's refusal in the main run is the policy reading the label the" \
    "write would produce. With the policy gone, Alice - who holds app and" \
    "was never granted data - moves the database ConfigMap into data with" \
    "live-mv -from-estate=app, and the write must land: the tool never says" \
    "no here, and nothing but the policy did."
  cmd "git mv app/database.tf data/database.tf ; choudoufu live-mv -from-estate=app kubernetes_config_map.database kubernetes_config_map.database   # in data/, as alice, no grant on data, policy gone"
  sed '/depends_on/d' "$APP/database.tf" > "$DATA/database.tf" && rm "$APP/database.tf" || fail "boundary" "BREAK: the database block did not move from app to data"
  ( cd "$DATA" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "BREAK: init failed in data"
  [ "$(can_use alice data)" = "false" ] || fail "boundary" "BREAK: alice holds data; the control would prove nothing"
  OUT="$(cd "$DATA" && as_role alice chdf live-mv -no-color -from-estate=app kubernetes_config_map.database kubernetes_config_map.database 2>&1)" || fail "boundary" "BREAK: with no policy, Alice's live-mv into an estate she does not hold was still refused: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
  denied "$OUT" && fail "boundary" "BREAK: the live-mv succeeded but the output still carries a refusal: $OUT"
  grep -q 'Relabelled one live object into this estate' <<< "$OUT" || fail "boundary" "BREAK: live-mv did not report the relabel: $OUT"
  grep -E 'Relabelled|tofu-estate' <<< "$OUT" | head -2 | evidence
  labels_of database boundary | evidence
  grep -q '"tofu-estate":"data"' <<< "$(labels_of database boundary)" || fail "boundary" "BREAK: Alice's live-mv did not land"
  proof "caught - with the policy gone, live-mv relabelled the object into an estate Alice was never granted. The refusal the main run shows at this step is the policy's, not the tool's."

  ( cd "$DATA" && as_role alice chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  ( cd "$APP" && as_role alice chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  ( cd "$NET" && as_role bob chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "5. Alice converges her estate"
explain \
  "A value in the gateway ConfigMap changes in configuration. Alice" \
  "applies it. The provider's update carries the object as it is and as" \
  "it will be, both labelled app; the policy asks whether Alice holds" \
  "app, and the write goes through."
cmd "sed greeting=gateway-v2 ; choudoufu apply -auto-approve   # in app/, as alice"
set_greeting "$APP/main.tf" gateway gateway-v2
OUT="$(cd "$APP" && as_role alice chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "Alice's apply on her own estate failed: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
[ "$(greeting_of gateway boundary)" = "gateway-v2" ] || fail "boundary" "Alice's write did not land"
greeting_of gateway boundary | evidence
proof "Alice's write landed on her estate. Admission read the label off the object and the authorizer said yes."

step "6. Bob, through choudoufu, is refused on Alice's estate - by the API server, not by this tool"
explain \
  "Bob has a checkout of the same configuration. The next change is his" \
  "to attempt, under his own ServiceAccount. The plan binds every object" \
  "by name, as it would for Alice, and finds one update to make; the" \
  "apply sends it, and the API server refuses: tofu-estate=app fences" \
  "the object and Bob is not bound to app. choudoufu surfaces the refusal" \
  "and changes nothing. In stock the equivalent move is a state edit, and" \
  "there is no admission to refuse it."
cmd "sed greeting=gateway-v3 ; choudoufu apply -auto-approve   # in app/, as bob"
set_greeting "$APP/main.tf" gateway-v2 gateway-v3
OUT="$(cd "$APP" && as_role bob chdf apply -auto-approve -input=false -no-color 2>&1 || true)"
printf '%s\n' "$OUT" > "$LOGS/bob-denied.apply"
denied "$OUT" || fail "boundary" "Bob's write on Alice's estate was not refused by the policy (full output in $LOGS/bob-denied.apply): $(grep -E '^Plan:|Apply complete|Error' <<< "$OUT" | head -3)"
refusal_line "$OUT" | evidence
[ "$(greeting_of gateway boundary)" = "gateway-v2" ] || fail "boundary" "the gateway changed despite the refusal"
proof "a Forbidden from admission, naming the policy and the estate. The ConfigMap is untouched, and the refusal is the cluster's own."
cmd "choudoufu apply -auto-approve   # in app/, as alice - the pending change is hers to make"
OUT="$(cd "$APP" && as_role alice chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "Alice could not apply the pending change: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
[ "$(greeting_of gateway boundary)" = "gateway-v3" ] || fail "boundary" "Alice's second write did not land"
proof "the same change under Alice's ServiceAccount lands. The configuration is identical; the credential is what the policy read."

step "7. Bob, tool-less, is refused on Alice's object - with no choudoufu in the call path"
explain \
  "Every write so far went through choudoufu. The fence is admission, not" \
  "the binary, so under Bob's ServiceAccount, with nothing of this tool" \
  "anywhere in the call, a plain kubectl label on the database ConfigMap" \
  "must be refused, a plain kubectl delete of it must be refused, and so" \
  "must stripping the label itself - the write that would make the object" \
  "nobody's. What is fenced is exactly a create, update or delete of an" \
  "object carrying tofu-estate; not a get, not a list, not a subresource," \
  "and not an object outside any estate. The policy names what it governs" \
  "and nothing wider."
cmd "kubectl label configmap database -n boundary owner=bob   # no choudoufu, as bob"
OUT="$(as_role bob kc label configmap database -n boundary owner=bob 2>&1 || true)"
denied "$OUT" || fail "boundary" "Bob's tool-less label on Alice's object was not refused by the policy: $OUT"
refusal_line "$OUT" | evidence
cmd "kubectl delete configmap database -n boundary   # no choudoufu, as bob"
OUT="$(as_role bob kc delete configmap database -n boundary 2>&1 || true)"
denied "$OUT" || fail "boundary" "Bob's tool-less delete of Alice's object was not refused by the policy: $OUT"
refusal_line "$OUT" | evidence
cmd "kubectl label configmap database -n boundary tofu-estate-   # no choudoufu, as bob: strip the marker"
OUT="$(as_role bob kc label configmap database -n boundary tofu-estate- 2>&1 || true)"
denied "$OUT" || fail "boundary" "Bob's tool-less strip of the marker was not refused by the policy: $OUT"
refusal_line "$OUT" | evidence
L="$(labels_of database boundary)"
grep -q '"tofu-estate":"app"' <<< "$L" || fail "boundary" "the database's label changed despite the refusals: $L"
grep -q 'owner' <<< "$L" && fail "boundary" "Bob's label landed despite the refusal: $L"
cmd "kubectl get configmap database -n boundary   # reads are not admission's to fence"
as_role bob kc get configmap database -n boundary -o name >/dev/null || fail "boundary" "Bob's read of Alice's object was refused; admission does not fence reads, so something else did"
proof "three plain kubectl writes, no choudoufu anywhere in the process, all refused by the same policy that fences choudoufu's own writes - and a plain read went through, because admission never sees one. The fence binds the credential, not the tool, and it is write-only."

step "8. an owned object keeps its estate: the owner field is no way out of one"
explain \
  "metadata.ownerReferences is an ordinary field the caller writes, so it" \
  "cannot be what decides whether the fence applies. Alice holds app and" \
  "not net. She is refused when she relabels an app object into net, so" \
  "the control is on the table first. Then she puts an ownerReference on" \
  "one of her own objects, which is allowed because the label does not" \
  "change, and tries the same relabel again: under the ruling on #1449" \
  "that is refused too, with the same sentence. Adding the owner and" \
  "changing the label in one request is refused as well, because the" \
  "exemption is read off the object as it already is. What the ruling does" \
  "allow is the last command here: an update to an owned object that" \
  "leaves tofu-estate exactly as it found it goes through under a" \
  "ServiceAccount holding no grant on that estate at all, which is what a" \
  "third-party operator writing to a labelled child needs. Stripping the" \
  "label off an owned object and deleting one are neither of them keeping" \
  "its estate, so both still need the grant."
OWNER_UID="$(kc get namespace boundary -o jsonpath='{.metadata.uid}')" || fail "boundary" "could not read the boundary Namespace's uid"
[ -n "$OWNER_UID" ] || fail "boundary" "the boundary Namespace's uid read back empty, and an ownerReference built from an empty uid would prove nothing"
cmd "kubectl apply -f - <<< (two ConfigMaps labelled tofu-estate=app)   # as alice"
for n in unowned owned; do
  cm_yaml "$n" app | as_role alice kc apply -f - >/dev/null \
    || fail "boundary" "Alice could not create the $n ConfigMap on her own estate; every arm below would prove nothing"
done
grep -q '"tofu-estate":"app"' <<< "$(labels_of unowned boundary)" || fail "boundary" "the unowned ConfigMap does not carry tofu-estate=app"
grep -q '"tofu-estate":"app"' <<< "$(labels_of owned boundary)" || fail "boundary" "the owned ConfigMap does not carry tofu-estate=app"
echo "unowned, owned: both tofu-estate=app, neither with an ownerReference yet" | evidence

cmd "kubectl label configmap unowned -n boundary tofu-estate=net --overwrite   # as alice - the control, no ownerReference"
OUT="$(as_role alice kc label configmap unowned -n boundary tofu-estate=net --overwrite 2>&1 || true)"
refused_by_policy "the control relabel of an object with no ownerReference" "$OUT" 'is not bound to it'

cmd "kubectl patch configmap owned -n boundary --type=merge -p '{\"metadata\":{\"ownerReferences\":[{... Namespace boundary ...}]}}'   # as alice"
as_role alice kc patch configmap owned -n boundary --type=merge \
  -p "{\"metadata\":{\"ownerReferences\":[{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"name\":\"boundary\",\"uid\":\"$OWNER_UID\",\"controller\":false}]}}" >/dev/null \
  || fail "boundary" "Alice could not put an ownerReference on her own object; the bypass arm below would prove nothing"
OWNED_BY="$(kc get configmap owned -n boundary -o jsonpath='{.metadata.ownerReferences[0].kind}/{.metadata.ownerReferences[0].name}')" \
  || fail "boundary" "could not read the ownerReferences back off the owned ConfigMap"
[ "$OWNED_BY" = "Namespace/boundary" ] || fail "boundary" "the ownerReference did not land on the object: '$OWNED_BY'"
echo "owned: ownerReferences[0] = $OWNED_BY, tofu-estate still app" | evidence
proof "the owner write itself is allowed, and it must be: the label did not change. That is the whole of what an owner buys."

cmd "kubectl label configmap owned -n boundary tofu-estate=net --overwrite   # as alice - the same relabel, now with an owner"
OUT="$(as_role alice kc label configmap owned -n boundary tofu-estate=net --overwrite 2>&1 || true)"
refused_by_policy "the relabel of an OWNED object into an estate Alice does not hold (#1449's update arm)" "$OUT" 'is not bound to it'
grep -q '"tofu-estate":"app"' <<< "$(labels_of owned boundary)" \
  || fail "boundary" "the owned ConfigMap left estate app despite the refusal: $(labels_of owned boundary)"

cmd "kubectl patch configmap unowned -n boundary --type=merge -p '{... tofu-estate: net AND an ownerReference ...}'   # as alice, one request"
OUT="$(as_role alice kc patch configmap unowned -n boundary --type=merge \
  -p "{\"metadata\":{\"labels\":{\"tofu-estate\":\"net\"},\"ownerReferences\":[{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"name\":\"boundary\",\"uid\":\"$OWNER_UID\",\"controller\":false}]}}" 2>&1 || true)"
refused_by_policy "adding an ownerReference and changing the label in one request" "$OUT" 'is not bound to it'
grep -q '"tofu-estate":"app"' <<< "$(labels_of unowned boundary)" \
  || fail "boundary" "the one-request move landed: $(labels_of unowned boundary)"
STILL_UNOWNED="$(kc get configmap unowned -n boundary -o jsonpath='{.metadata.ownerReferences}')" \
  || fail "boundary" "could not read the unowned ConfigMap's ownerReferences back"
[ -z "$STILL_UNOWNED" ] || fail "boundary" "the refused request still planted an ownerReference: $STILL_UNOWNED"
proof "the exemption is read off the object as it already is, so a request cannot hand itself one. Reading it off the object the write would produce would also let Bob patch Alice's object by adding an owner in the same request, which is step 7 undone."

cmd "kubectl label configmap owned -n boundary tofu-estate-   # as bob: strip the marker off an OWNED object"
OUT="$(as_role bob kc label configmap owned -n boundary tofu-estate- 2>&1 || true)"
refused_by_policy "stripping the label off an owned object, by a ServiceAccount that does not hold app" "$OUT" 'is not bound to that estate'
cmd "kubectl delete configmap owned -n boundary   # as bob: delete an OWNED object"
OUT="$(as_role bob kc delete configmap owned -n boundary 2>&1 || true)"
refused_by_policy "deleting an owned object, by a ServiceAccount that does not hold app" "$OUT" 'is not bound to that estate'
grep -q '"tofu-estate":"app"' <<< "$(labels_of owned boundary)" \
  || fail "boundary" "the owned ConfigMap lost its label or its life despite the refusals: $(labels_of owned boundary)"

cmd "kubectl patch configmap owned -n boundary --type=merge -p '{\"data\":{\"greeting\":\"operator\"}}'   # as bob, who holds no grant on app"
as_role bob kc patch configmap owned -n boundary --type=merge -p '{"data":{"greeting":"operator"}}' >/dev/null \
  || fail "boundary" "the ruling's allowance does not work: an update to an owned object that leaves tofu-estate alone was refused for a caller with no grant on the estate"
[ "$(greeting_of owned boundary)" = "operator" ] || fail "boundary" "Bob's permitted update of the owned object did not land"
greeting_of owned boundary | evidence
grep -q '"tofu-estate":"app"' <<< "$(labels_of owned boundary)" || fail "boundary" "the owned object lost its label to the permitted update"
proof "one object, one caller with no grant on app: the relabel, the strip and the delete are all refused by name, and the plain update goes through. That is the ruling - owned objects keep their estate - and the estate is what the caller has to hold to move it."

step "9. the owner field is no way INTO an estate either: the create arm"
explain \
  "On a create there is no old object, so the earlier rule read" \
  "ownerReferences off the object being created, and one manifest" \
  "carrying both a label and an owner was admitted. Alice holds app and" \
  "not net. The control goes first again: a ConfigMap labelled net with no" \
  "owner, refused. Then the same manifest with an ownerReference, which is" \
  "#1449's create arm and must now be refused in the same words. Then the" \
  "one that reaches furthest: a Secret shaped exactly like a record the" \
  "Kubernetes record store writes for net - the tofu-record- name, the" \
  "managed-by and record-namespace labels, the record-key annotation - and" \
  "owned. The store's list does not skip owned Secrets, so an admitted one" \
  "would be read back as a genuine record."
cmd "kubectl apply -f - <<< (ConfigMap planted-unowned, tofu-estate=net)   # as alice - the control"
OUT="$(cm_yaml planted-unowned net | as_role alice kc apply -f - 2>&1 || true)"
refused_by_policy "creating a net-labelled ConfigMap with no ownerReference" "$OUT" 'is not bound to it'
cmd "kubectl apply -f - <<< (ConfigMap planted, tofu-estate=net, ownerReferences: Namespace/boundary)   # as alice"
OUT="$(cm_yaml planted net owned | as_role alice kc apply -f - 2>&1 || true)"
refused_by_policy "creating a net-labelled ConfigMap that is already owned (#1449's create arm)" "$OUT" 'is not bound to it'
cmd "kubectl apply -f - <<< (Secret tofu-record-..., tofu-estate=net, managed-by=choudoufu, owned)   # as alice"
OUT="$(record_secret_yaml net | as_role alice kc apply -f - 2>&1 || true)"
refused_by_policy "planting a record-shaped Secret for an estate Alice does not hold" "$OUT" 'is not bound to it'
LEFT="$(kc get configmap,secret -n boundary -l tofu-estate=net -o name)" \
  || fail "boundary" "could not list the boundary namespace for net-labelled objects"
[ -z "$LEFT" ] || fail "boundary" "something labelled net was planted in the boundary namespace despite the refusals: $LEFT"
echo "objects labelled tofu-estate=net in namespace boundary: none" | evidence
proof "three creates refused by the same policy and the same sentence, the owned ones with the unowned control beside them. An ownerReference in a manifest is a field the author typed, and the fence reads the label, not the author's claim about who made the object."
cmd "kubectl delete configmap owned unowned -n boundary   # as alice, who holds app"
as_role alice kc delete configmap owned unowned -n boundary >/dev/null \
  || fail "boundary" "Alice could not delete her own objects, owned and unowned alike, at the end of the step"
GONE="$(kc get configmap -n boundary -o name)" || fail "boundary" "could not list the boundary namespace after the cleanup"
grep -qE 'configmap/(owned|unowned)$' <<< "$GONE" && fail "boundary" "the step's scratch objects survived the cleanup: $GONE"
proof "and the same deletes under the ServiceAccount that does hold app go through, owned object included. What the fence reads is the estate, never the owner."

step "10. what the exemption costs: a Deployment's ReplicaSet and Pods are still made"
explain \
  "The first match condition exempts the control plane by who is asking," \
  "and that is what keeps a controller's copies out of the fence. Alice" \
  "applies a Deployment whose pod template carries tofu-estate=app. She" \
  "never creates the ReplicaSet or the Pod; the kube-system deployment and" \
  "replicaset controllers do, under their own ServiceAccounts, and both" \
  "copies carry the label. If the owner exemption had been what let them" \
  "through, tightening it would have broken every labelled workload on the" \
  "cluster. Deleting the Deployment then has the garbage collector remove" \
  "the labelled ReplicaSet and Pod, which it may do for the same reason."
cmd "kubectl apply -f - <<< (Deployment fanout, tofu-estate=app on the object and on the pod template)   # as alice"
cat <<EOF | as_role alice kc apply -f - >/dev/null || fail "boundary" "Alice could not create the fanout Deployment on her own estate"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: fanout
  namespace: boundary
  labels:
    tofu-estate: app
spec:
  replicas: 1
  selector:
    matchLabels:
      app: fanout
  template:
    metadata:
      labels:
        app: fanout
        tofu-estate: app
    spec:
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.10
EOF
# The jsonpath errors while items is empty, so the || true here can only
# leave RS or POD empty, and empty is what the assertions below refuse.
RS=""; POD=""
for i in $(seq 1 60); do
  RS="$(kc get replicaset -n boundary -l app=fanout -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
  POD="$(kc get pod -n boundary -l app=fanout -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
  if [ -n "$RS" ] && [ -n "$POD" ]; then break; fi
  sleep 1
done
[ -n "$RS" ] || fail "boundary" "no ReplicaSet for the labelled Deployment after 60s: the fence is now refusing the deployment controller's own copy of a labelled pod template"
[ -n "$POD" ] || fail "boundary" "the ReplicaSet $RS made no Pod after 60s: the fence is now refusing the replicaset controller's copy of a labelled pod template"
echo "replicaset/$RS pod/$POD" | evidence
grep -q '"tofu-estate":"app"' <<< "$(obj_labels replicaset "$RS" boundary)" \
  || fail "boundary" "the ReplicaSet does not carry the pod template's label, so this step would not be measuring the fence at all: $(obj_labels replicaset "$RS" boundary)"
grep -q '"tofu-estate":"app"' <<< "$(obj_labels pod "$POD" boundary)" \
  || fail "boundary" "the Pod does not carry the label, so this step would not be measuring the fence at all: $(obj_labels pod "$POD" boundary)"
obj_labels pod "$POD" boundary | evidence
cmd "kubectl delete deployment fanout -n boundary   # as alice; the collector removes the labelled copies"
as_role alice kc delete deployment fanout -n boundary >/dev/null || fail "boundary" "Alice could not delete her own Deployment"
COLLECTED=no
for i in $(seq 1 60); do
  REMAINING="$(kc get replicaset,pod -n boundary -l app=fanout -o name)" || fail "boundary" "could not list the Deployment's copies while waiting for the garbage collector"
  if [ -z "$REMAINING" ]; then COLLECTED=yes; break; fi
  sleep 1
done
[ "$COLLECTED" = "yes" ] || fail "boundary" "the garbage collector still had not removed the labelled ReplicaSet and Pod after 60s ($REMAINING); a delete of a labelled object needs the estate, and the collector holds none - it is exempt as a kube-system ServiceAccount, and that is what this waits on"
echo "replicasets and pods labelled app=fanout after the delete: none" | evidence
proof "a labelled pod template still fans out into a labelled ReplicaSet and a labelled Pod, and the collector still cleans them up. Those writes are the control plane's, exempt by who is asking, which is the only exemption a caller cannot forge."

step "11. Bob's own estate, tool-less, and the API server lets it through - the next plan sees it"
explain \
  "The same policy that refused step 7 permits what the grant names on" \
  "what Bob holds. A plain kubectl label on the router ConfigMap (estate" \
  "net) needs no choudoufu to succeed, and nothing about it is hidden" \
  "from the tool afterward: the next plan reads the live object, not a" \
  "log of who wrote it, so it sees the drift and proposes reconciling it" \
  "with what the configuration declares."
cmd "kubectl label configmap router -n net owner=bob   # no choudoufu, as bob"
as_role bob kc label configmap router -n net owner=bob >/dev/null || fail "boundary" "Bob's tool-less write on his own estate was refused"
grep -q '"owner":"bob"' <<< "$(labels_of router net)" || fail "boundary" "Bob's permitted write did not land"
labels_of router net | evidence
cmd "choudoufu plan   # in net/, as bob"
OUT="$(cd "$NET" && as_role bob chdf plan -input=false -no-color 2>&1)" || fail "boundary" "Bob's plan after his own tool-less write failed: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
printf '%s\n' "$OUT" > "$LOGS/bob-toolless.plan"
grep -q "No changes." <<< "$OUT" && fail "boundary" "the next plan reported No changes. after Bob's tool-less write (full plan in $LOGS/bob-toolless.plan)"
grep -q '"owner"' <<< "$OUT" || fail "boundary" "the next plan did not surface Bob's tool-less write (full plan in $LOGS/bob-toolless.plan): $(grep -E '^Plan:|~ ' <<< "$OUT" | head -5)"
grep -E '^Plan:|"owner"' <<< "$OUT" | head -3 | evidence
proof "a write that never touched choudoufu is still visible to it: the plan names the label and proposes removing it. What the fence permits is not hidden from the tool."
cmd "choudoufu apply -auto-approve   # in net/, as bob - reconciling his own tool-less write"
( cd "$NET" && as_role bob chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "Bob could not reconcile his own tool-less write"
grep -q 'owner' <<< "$(labels_of router net)" && fail "boundary" "the reconciling apply did not remove the stray label"
proof "reconciled, still under Bob's own ServiceAccount. The estate is clean again before the carve begins."

step "12. a rename is a configuration edit: live-mv has nothing governed to write"
explain \
  "On AWS a rename ends with live-mv rewriting tofu-address on the live" \
  "object. Here the object carries no address: it is bound to its block by" \
  "its own kind, namespace and name, all authored in configuration. Bob" \
  "renames the router block, runs the same live-mv an AWS runbook would," \
  "and it reports that there is nothing governed to write and exits 0." \
  "The next plan binds the object at its new address with no change."
cmd "sed router=router_renamed net/main.tf ; choudoufu live-mv kubernetes_config_map.router kubernetes_config_map.router_renamed ; choudoufu plan   # in net/, as bob"
sed_i "$NET/main.tf" 's/"kubernetes_config_map" "router"/"kubernetes_config_map" "router_renamed"/'
grep -q '"router_renamed"' "$NET/main.tf" || fail "boundary" "the router block was not renamed in net"
OUT="$(cd "$NET" && as_role bob chdf live-mv -no-color kubernetes_config_map.router kubernetes_config_map.router_renamed 2>&1)" || fail "boundary" "live-mv on a same-estate Kubernetes rename did not exit 0: $OUT"
grep -q 'Nothing to write' <<< "$OUT" || fail "boundary" "live-mv did not say there is nothing to write: $OUT"
grep -E 'Nothing to write' <<< "$OUT" | evidence
OUT="$(cd "$NET" && as_role bob chdf plan -input=false -no-color 2>&1)" || fail "boundary" "net does not plan after the rename: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
printf '%s\n' "$OUT" > "$LOGS/net-renamed.plan"
grep -q "No changes." <<< "$OUT" || fail "boundary" "net does not plan clean after the rename (full plan in $LOGS/net-renamed.plan): $(grep -E '^Plan:|will be|orphan|UNOWNED' <<< "$OUT" | head -4)"
echo "net under bob, block renamed: No changes." | evidence
proof "exit 0 and one sentence: nothing to write. The block is renamed, the object is untouched, and the plan is empty - the rename was the edit."

step "13. the carve begins with a git move, and the relabel is refused from both sides"
explain \
  "The database block moves from app's configuration into a new root," \
  "data, the way any split starts. The ownership write that completes it" \
  "is a relabel: tofu-estate app -> data on one object, and live-mv" \
  "-from-estate=app in data/ is that write, made through the provider" \
  "under the caller's own ServiceAccount - the same command that retags on" \
  "AWS. The policy reads both sides of it. Alice holds app but not data, so" \
  "it refuses her on the estate the object would enter, through live-mv" \
  "and through plain kubectl alike; Bob holds neither, so it refuses him on" \
  "the estate the object is leaving. On AWS the same two checks are" \
  "aws:ResourceTag and aws:RequestTag."
cmd "git mv app/database.tf data/database.tf ; choudoufu live-mv -from-estate=app kubernetes_config_map.database kubernetes_config_map.database   # in data/, as alice"
# The block leaves without its depends_on: the namespace it named is
# declared by app, not by the root the block is moving into.
sed '/depends_on/d' "$APP/database.tf" > "$DATA/database.tf" && rm "$APP/database.tf" || fail "boundary" "the database block did not move from app to data"
grep -q 'name      = "database"' "$DATA/database.tf" || fail "boundary" "the database block did not land in data"
( cd "$DATA" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "init failed in data"
OUT="$(cd "$DATA" && as_role alice chdf live-mv -no-color -from-estate=app kubernetes_config_map.database kubernetes_config_map.database 2>&1 || true)"
printf '%s\n' "$OUT" > "$LOGS/alice-denied.live-mv"
denied "$OUT" || fail "boundary" "Alice's live-mv into data, which she does not hold, was not refused by the policy (full output in $LOGS/alice-denied.live-mv): $(grep -E 'Error|Relabelled' <<< "$OUT" | head -3)"
refusal_line "$OUT" | evidence
cmd "kubectl label configmap database -n boundary tofu-estate=data --overwrite   # as alice, then as bob"
OUT="$(as_role alice kc label configmap database -n boundary tofu-estate=data --overwrite 2>&1 || true)"
denied "$OUT" || fail "boundary" "Alice's tool-less relabel into data, which she does not hold, was not refused by the policy: $OUT"
refusal_line "$OUT" | evidence
OUT="$(as_role bob kc label configmap database -n boundary tofu-estate=data --overwrite 2>&1 || true)"
denied "$OUT" || fail "boundary" "Bob's relabel out of app, which he does not hold, was not refused by the policy: $OUT"
refusal_line "$OUT" | evidence
grep -q '"tofu-estate":"app"' <<< "$(labels_of database boundary)" || fail "boundary" "the database left the estate despite the refusals"
proof "the carve itself was refused, per object, from both sides: the estate being left and the estate being entered - and live-mv met the same refusal a plain kubectl did, because the write it makes is the same write. A state mv has no such moment; nothing evaluates it."

step "14. handover is an RBAC change: grant Alice data, and the same live-mv goes through"
explain \
  "Nothing on the object and nothing in the policy changes. The cluster" \
  "admin applies the same grant template for estate data to Alice, and" \
  "the live-mv she was just refused now passes: the authorizer says yes on" \
  "both estates. kubectl reads the new label back. The object never" \
  "moves; its owner does."
cmd "sed s/ESTATE/data/ estate-grant.yaml | kubectl apply -f -   # as the cluster admin"
grant data alice
[ "$(can_use alice data)" = "true" ] || fail "boundary" "the grant did not take: alice still cannot use data"
echo "may alice use estate data: true" | evidence
cmd "choudoufu live-mv -from-estate=app kubernetes_config_map.database kubernetes_config_map.database   # in data/, as alice"
OUT="$(cd "$DATA" && as_role alice chdf live-mv -no-color -from-estate=app kubernetes_config_map.database kubernetes_config_map.database 2>&1)" || fail "boundary" "Alice's live-mv into data failed after the grant: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
grep -q 'Relabelled one live object into this estate' <<< "$OUT" || fail "boundary" "live-mv did not report the relabel: $OUT"
grep -E 'Relabelled|tofu-estate' <<< "$OUT" | head -2 | evidence
cmd "kubectl get configmap database -n boundary -o jsonpath='{.metadata.labels}'"
labels_of database boundary | evidence
grep -q '"tofu-estate":"data"' <<< "$(labels_of database boundary)" || fail "boundary" "the database does not carry tofu-estate=data after Alice's live-mv"
proof "tofu-estate=data, written by live-mv under the one principal a policy lets write it, and read back by kubectl. Where there was one estate there are two, and no state was split."

step "15. every estate plans clean, each under its own principal"
explain \
  "Alice plans data and app; Bob plans net. Each sweep lists its own" \
  "estate by label and finds nothing to do. app no longer declares the" \
  "database block and the live object no longer carries app's label, so" \
  "app's sweep does not see it and its plan is honestly empty; data binds" \
  "it by namespace and name; net binds the renamed router the same way."
cmd "choudoufu plan   # in data/ and app/ as alice, in net/ as bob"
for spec in "alice $DATA data" "alice $APP app" "bob $NET net"; do
  read -r who dir label <<< "$spec"
  OUT="$(cd "$dir" && as_role "$who" chdf plan -input=false -no-color 2>&1)" || fail "boundary" "$label does not plan under $who: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
  printf '%s\n' "$OUT" > "$LOGS/$label.plan"
  grep -q "No changes." <<< "$OUT" || fail "boundary" "$label does not plan clean under $who (full plan in $LOGS/$label.plan): $(grep -E '^Plan:|will be|orphan|UNOWNED' <<< "$OUT" | head -4)"
  echo "$label under $who: No changes." | evidence
done
proof "No changes, three times, each under the principal that holds the estate. The boundary moved with one label write, and every side agrees where it is."

step "16. teardown - each estate by its own destroy, under its own principal"
OUT="$(cd "$DATA" && as_role alice chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "teardown of data failed: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
grep -q 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$OUT" || fail "boundary" "data's destroy did not remove exactly one object: $OUT"
OUT="$(cd "$APP" && as_role alice chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "teardown of app failed: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
grep -q 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$OUT" || fail "boundary" "app's destroy did not remove exactly two objects: $OUT"
OUT="$(cd "$NET" && as_role bob chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "teardown of net failed: $(grep -E 'Error|Forbidden|denied' <<< "$OUT" | head -3)"
grep -q 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$OUT" || fail "boundary" "net's destroy did not remove exactly two objects: $OUT"
proof "1, 2 and 2 destroyed, each through its own configuration and under the principal that holds it. Every delete passed the same policy."

echo "  What you watched: two ServiceAccounts hold two estates on one cluster"
echo "  and are fenced by one admission policy reading the estate label,"
echo "  refused by the API server when they reach across - through choudoufu"
echo "  and through plain kubectl alike, with a plain read untouched because"
echo "  admission never sees one. An ownerReference is no way past it: an"
echo "  owned object keeps its estate, so the relabel, the strip, the delete"
echo "  and the owned create are all refused beside their unowned twins,"
echo "  while a plain update that leaves the label alone goes through with no"
echo "  grant, and a labelled pod template still fans out into a labelled"
echo "  ReplicaSet and Pod. A rename is a config edit: live-mv has"
echo "  nothing governed to write and says so. Then one object is carved into"
echo "  a new estate by live-mv -from-estate, one label write the policy"
echo "  refused from both sides until a binding moved."
echo "  In stock every one of those moves is a state edit, and nothing in the"
echo "  cluster can say no to a state edit or knows it happened."
