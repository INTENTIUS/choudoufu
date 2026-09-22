#!/usr/bin/env bash
set -uo pipefail

# examples/record-store-cluster/selftest.sh: the shipped recipes and
# manifests, run against a real API server, and read back with the API
# server's own authorizer. GitHub issue #1442.
#
# `just up` is applied to a kind cluster, and then `kubectl auth can-i --as`
# asks the authorizer, per identity and per verb, exactly what
# internal/live/staterecord/kubernetescontract.go asks it with a
# SelfSubjectAccessReview: the plan identity may get and list Secrets in the
# records namespace and nothing else, the apply identity may do all five
# verbs the store uses and nothing else, neither may read Secrets outside
# the namespace, and the apply identity holds the estate grant the boundary
# policy asks for. Then `just down` is run against a namespace holding a
# record Secret, which must refuse, and against an empty one, which must
# delete it.
#
# The verdict is the last line, never the exit code. Every property prints
# "  ok:" or "  FAIL:", and the run ends with one of
#
#   PASS: record-store-cluster selftest - <summary>
#   FAIL: record-store-cluster selftest - <what was missing>
#
# BREAK=1 is the control. The apply Role is applied with `update` removed
# from its verbs, the same checks run, and the run passes only if they FAIL
# on exactly that: the line "-> caught" is printed with what caught it, and
# the verdict reads "PASS: record-store-cluster selftest (BREAK=1) - ...". A
# control that found nothing is a FAIL.
#
# Usage:
#   bash examples/record-store-cluster/selftest.sh
#       creates a kind cluster (needs kind, kubectl, just), deletes it on exit
#   SELFTEST_KUBECONFIG=<path> bash examples/record-store-cluster/selftest.sh
#       runs against that cluster as its current context, creates and deletes
#       no cluster; it must be a throwaway cluster you may create namespaces
#       and ClusterRoles in
#   SELFTEST_CLUSTER=<name>   the kind cluster's name (default chdf-rsc-<id>)
#   BREAK=1                   the control

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BREAK="${BREAK:-0}"
PASS=1
FAILS=()

log() { printf '%s\n' "$*"; }
ok() { log "  ok: $*"; }
bad() { log "  FAIL: $*"; PASS=0; FAILS+=("$*"); }
verdict_fail() {
  log "FAIL: record-store-cluster selftest - $*"
  exit 1
}

for tool in kubectl just; do
  command -v "$tool" >/dev/null 2>&1 || verdict_fail "$tool is not on PATH"
done

# --- the cluster ---
CLUSTER=""
if [ -n "${SELFTEST_KUBECONFIG:-}" ]; then
  export KUBECONFIG="$SELFTEST_KUBECONFIG"
  log "cluster: the one at $KUBECONFIG (not created here, not deleted here)"
else
  command -v kind >/dev/null 2>&1 || verdict_fail "kind is not on PATH and SELFTEST_KUBECONFIG is not set"
  WORK="$(mktemp -d)"
  CLUSTER="${SELFTEST_CLUSTER:-chdf-rsc-$(date +%s | tail -c 6)-$RANDOM}"
  export KUBECONFIG="$WORK/kubeconfig"
  log "cluster: kind create cluster --name $CLUSTER"
  if ! kind create cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG" --wait 120s >"$WORK/kind.log" 2>&1; then
    tail -5 "$WORK/kind.log"
    verdict_fail "kind create cluster failed"
  fi
fi
cleanup() {
  if [ -n "$CLUSTER" ]; then
    kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT

kc() { kubectl --request-timeout=60s "$@"; }
j() { just --justfile "$HERE/justfile" --working-directory "$HERE" "$@"; }

ESTATE="selftest-$RANDOM"
NS="tofu-records-$ESTATE"
PLAN="system:serviceaccount:$NS:choudoufu-plan"
APPLY="system:serviceaccount:$NS:choudoufu-apply"

# --- just up ---
log "step 1: just up $ESTATE"
if ! up_out="$(j up "$ESTATE" 2>&1)"; then
  printf '%s\n' "$up_out" | sed 's/^/    /'
  verdict_fail "just up $ESTATE exited non-zero"
fi
printf '%s\n' "$up_out" | grep -q "^RECORD_STORE_NAMESPACE=$NS\$" \
  || bad "just up did not print RECORD_STORE_NAMESPACE=$NS"
phase="$(kc get namespace "$NS" -o jsonpath='{.status.phase}' 2>&1)"
if [ "$phase" = "Active" ]; then ok "namespace $NS is Active"; else bad "namespace $NS: phase '$phase'"; fi

# --- the control's mutation ---
if [ "$BREAK" = "1" ]; then
  log "control: re-applying the apply Role with 'update' removed from its verbs"
  mutant="$(j _render "$ESTATE" | sed 's/\["get", "list", "create", "update", "delete"\]/["get", "list", "create", "delete"]/')"
  if printf '%s\n' "$mutant" | grep -q '"update"'; then
    verdict_fail "the control's sed did not remove 'update' from the rendered manifest, so there is no mutant to catch"
  fi
  printf '%s\n' "$mutant" | kc apply -f - >/dev/null 2>&1 || verdict_fail "applying the mutant manifest failed"
fi

# --- the authorizer's answers ---
# can <expect> <identity> <verb> <resource> [kubectl args...]: asks the API
# server's authorizer as that identity. The answer is read from stdout
# ("yes"/"no"), never from the exit code, and anything else fails the check:
# a kubectl that could not reach the server prints neither.
can() {
  local expect="$1" who="$2" verb="$3" res="$4"; shift 4
  local got
  got="$(kc auth can-i "$verb" "$res" --as="$who" "$@" 2>/dev/null | tr -d '[:space:]')"
  local where="$*"
  case "$got" in
    yes|no) ;;
    *) bad "$who: can-i $verb $res $where answered '$got', neither yes nor no"; return ;;
  esac
  if [ "$got" = "$expect" ]; then
    ok "$who may$( [ "$expect" = no ] && printf ' not') $verb $res $where"
  else
    bad "$who: can-i $verb $res $where answered $got, want $expect"
  fi
}

log "step 2: the plan identity, get and list on Secrets in $NS and nothing else"
for v in get list; do can yes "$PLAN" "$v" secrets -n "$NS"; done
for v in create update delete patch watch; do can no "$PLAN" "$v" secrets -n "$NS"; done

log "step 3: the apply identity, the five verbs the store uses and nothing else"
for v in get list create update delete; do can yes "$APPLY" "$v" secrets -n "$NS"; done
for v in patch watch; do can no "$APPLY" "$v" secrets -n "$NS"; done

log "step 4: neither identity reads Secrets outside $NS (read_isolation)"
for who in "$PLAN" "$APPLY"; do
  can no "$who" get secrets -n default
  can no "$who" list secrets -n kube-system
  can no "$who" list secrets --all-namespaces
done

# holds <expect> <identity> <estate>: asks the authorizer whether identity
# holds `use` on estates.choudoufu.intentius.io/<estate>, with a
# SubjectAccessReview and not `kubectl auth can-i`. The resource is virtual
# (it exists only in RBAC and in the boundary policy's CEL), and measured on
# kind v1.36.1 with kubectl v1.36.1, `auth can-i use
# estates.choudoufu.intentius.io/<estate>` answers "no" for an identity the
# same question as a SubjectAccessReview answers "allowed: true" for, because
# kubectl cannot resolve a resource type discovery does not list. The
# review is what the policy's authorizer and the store's contract ask, so
# it is what this asks.
holds() {
  local expect="$1" who="$2" estate="$3" got
  got="$(kc create -f - -o jsonpath='{.status.allowed}' 2>/dev/null <<EOF
apiVersion: authorization.k8s.io/v1
kind: SubjectAccessReview
spec:
  user: $who
  groups: ["system:serviceaccounts", "system:authenticated"]
  resourceAttributes:
    group: choudoufu.intentius.io
    resource: estates
    name: $estate
    verb: use
EOF
)"
  case "$got" in
    true) got=yes ;;
    false) got=no ;;
    *) bad "$who: SubjectAccessReview for use on estates.choudoufu.intentius.io/$estate answered '$got', neither true nor false"; return ;;
  esac
  if [ "$got" = "$expect" ]; then
    ok "$who may$( [ "$expect" = no ] && printf ' not') use estates.choudoufu.intentius.io/$estate"
  else
    bad "$who: use on estates.choudoufu.intentius.io/$estate answered $got, want $expect"
  fi
}

log "step 5: the estate grant (estate_boundary): the apply identity holds use on the estate, the plan identity does not"
holds yes "$APPLY" "$ESTATE"
holds no "$PLAN" "$ESTATE"
holds no "$APPLY" "some-other-estate"

# --- the control's verdict ---
if [ "$BREAK" = "1" ]; then
  if [ "$PASS" = "1" ]; then
    verdict_fail "(BREAK=1) the apply Role lost 'update' and every check above passed; the checks cannot see the verbs they claim to"
  fi
  caught=0; other=0
  for f in "${FAILS[@]}"; do
    case "$f" in
      *choudoufu-apply*"can-i update secrets"*) caught=$((caught+1)) ;;
      *) other=$((other+1)) ;;
    esac
  done
  if [ "$caught" -ne 1 ] || [ "$other" -ne 0 ]; then
    verdict_fail "(BREAK=1) $caught check(s) failed on the apply identity's update and $other on something else; the mutant removed exactly one verb and exactly one check must name it"
  fi
  log "  -> caught: the apply identity's Role without 'update' is refused by name (${FAILS[0]})"
  j down "$ESTATE" >/dev/null 2>&1 || log "  (just down after the control: non-zero, the cluster is deleted anyway)"
  log "PASS: record-store-cluster selftest (BREAK=1) - the one verb the mutant removed was the one check that failed"
  exit 0
fi

# --- just down ---
log "step 6: just down refuses while a record Secret is held"
kc create secret generic tofu-record-planted -n "$NS" --from-literal=record=planted >/dev/null 2>&1 \
  || bad "could not plant a Secret in $NS"
kc label secret tofu-record-planted -n "$NS" app.kubernetes.io/managed-by=choudoufu tofu-estate="$ESTATE" >/dev/null 2>&1 \
  || bad "could not label the planted Secret"
down_out="$(j down "$ESTATE" 2>&1)" && down_rc=0 || down_rc=$?
if [ "$down_rc" -ne 0 ] && printf '%s\n' "$down_out" | grep -q "REFUSING"; then
  ok "just down exited $down_rc and said REFUSING over 1 held record"
else
  bad "just down over a held record exited $down_rc without REFUSING: $(printf '%s' "$down_out" | tail -3 | tr '\n' ' ')"
fi
if kc get namespace "$NS" >/dev/null 2>&1; then ok "namespace $NS is still there after the refusal"; else bad "namespace $NS was deleted despite the refusal"; fi

log "step 7: just down deletes an empty namespace and the grant"
kc delete secret tofu-record-planted -n "$NS" >/dev/null 2>&1 || bad "could not remove the planted Secret"
down_out="$(j down "$ESTATE" 2>&1)" && down_rc=0 || down_rc=$?
if [ "$down_rc" -eq 0 ]; then ok "just down exited 0 over an empty namespace"; else bad "just down over an empty namespace exited $down_rc: $(printf '%s' "$down_out" | tail -3 | tr '\n' ' ')"; fi
if kc get namespace "$NS" >/dev/null 2>&1; then bad "namespace $NS is still there after just down"; else ok "namespace $NS is gone"; fi
if kc get clusterrole "choudoufu-estate-$ESTATE" >/dev/null 2>&1; then bad "the estate grant ClusterRole is still there after just down"; else ok "the estate grant for $ESTATE is gone"; fi

if [ "$PASS" = "1" ]; then
  log "PASS: record-store-cluster selftest - plan gets and lists, apply holds all five verbs and the estate grant, neither reads outside $NS, down refuses over a held record and deletes an empty namespace"
  exit 0
fi
verdict_fail "${#FAILS[@]} check(s) failed; read the FAIL lines above"
