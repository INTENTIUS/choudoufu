# k8s-a-deleted-namespace-is-gone
# CLAIM 46 (kubernetes) - A namespace deleted under a converged estate takes its objects with it and the plan reads them as gone: nothing in it is listed as present or swept as an orphan, the plan proposes exactly the creates stock's plan proposes from the same position, the namespace's own create first when the estate declares it, and one apply converges to an empty plan; a deleted record-store namespace is refused by name on the plan and on the apply, and nothing is written. ~5 min.
#
# Fault 5 of #1110 (#1765), the last of the five. `kubectl delete
# namespace` is the bluntest out-of-band write a cluster has: the API
# server deletes every object in the namespace, whoever owns it, and an
# estate whose objects lived there is left with a configuration that names
# them and a cluster that has none of them. Stock learns that from its
# refresh, its state file still listing every object. This fork has no
# state to refresh from - it lists the estate by its label - so the
# question is whether the listing and the plan agree that the objects are
# gone, and whether the plan is then the one stock would make.
#
# Two estates and their stock twins, each in a namespace of its own, so
# every plan below is compared with stock's plan from the same position on
# the same cluster rather than with a quoted answer:
#
#   smoke-nsd  declares its namespace (kubernetes_namespace.app) and keeps
#              its records in the cluster, record_store "kubernetes", in
#              tofu-records-smoke-nsd. A ConfigMap and a ServiceAccount
#              live in the namespace, named through a reference to it.
#   smoke-nsu  does not: the platform team made its namespace with
#              kubectl, and the estate is the two objects inside it, on
#              the implied local store.
#
# Steps 4 and 5 are the claim's first half: the sweep lists nothing, the
# plan proposes exactly stock's creates, and - declared - the apply creates
# the namespace first and converges. Undeclared, neither tool can create
# into a namespace that is not there; both applies fail in the provider's
# words, and once the namespace is back one apply converges.
#
# Step 6 is #1765's item 4, measured and reported rather than folded in: a
# namespace still terminating because a finalizer holds an object inside
# it is fault 1's shape (claim 25). What the plan says in that window is
# what stock's plan says: the namespace and the held object are still
# there, so they are not proposed, and the object the namespace's delete
# already took is proposed as a create.
#
# Step 7 is the second variant: the record store's own namespace deleted.
# A list in an absent namespace answers empty, and an empty listing would
# read as an estate with no records, so live/kubernetes/OPERATE.md says the
# store refuses it by name with the kubectl line that makes it. This step
# holds the plan and the apply to that and checks that the refused apply
# created nothing - not the namespace it could have recreated, not an
# object, not a record.
#
# BREAK=1 runs the same delete and then asserts what a sweep that never
# saw the delete would say: an empty plan. That assertion must fail; the
# control exits 0 only when the plan proposes the creates.

SCEN="k8s-a-deleted-namespace-is-gone"

SMOKE_WORK="$SMOKE_WORKROOT/$SCEN"
mkdir -p "$SMOKE_WORK/nsd" "$SMOKE_WORK/nsu" "$SMOKE_WORK/stock-nsd" "$SMOKE_WORK/stock-nsu"; export SMOKE_WORK

NSD="smoke-nsd"; NSU="smoke-nsu"
STOCK_NSD="smoke-nsd-stock"; STOCK_NSU="smoke-nsu-stock"
STORE_NS="tofu-records-$NSD"
FINALIZER="smoke.choudoufu.io/hold"

command -v terraform >/dev/null 2>&1 \
  || fail "$SCEN" "the terraform binary is not on PATH - every plan here is compared with stock's"

# versions_tf <dir> <estate|""> <store>: the terraform block. An empty
# estate is a stock root; store is "kubernetes" or "local".
versions_tf() {
  local dir="$1" estate="$2" store="$3"
  {
    echo 'terraform {'
    echo '  required_version = ">= 1.5.0"'
    if [ -n "$estate" ]; then
      echo '  live {'
      echo "    estate = \"$estate\""
      if [ "$store" = "kubernetes" ]; then
        # kind's API server has no encryption provider and a fresh cluster
        # has no admission policy or Roles, so the three assertions it
        # fails are waived and said so on every run (claim 30, on claim
        # 39's store). This claim is about the namespace, not the store's
        # cluster contract.
        echo '    record_store "kubernetes" {'
        echo '      allow_insecure = ["read_isolation", "encryption_at_rest", "estate_boundary"]'
        echo '    }'
      fi
      echo '  }'
    fi
    echo '  required_providers {'
    echo '    kubernetes = {'
    echo '      source  = "hashicorp/kubernetes"'
    echo '      version = "= 3.2.1"'
    echo '    }'
    echo '  }'
    echo '}'
    echo
    echo 'provider "kubernetes" {}'
  } > "$dir/versions.tf"
}

# declared_tf <dir> <namespace>: the namespace is the estate's, and its two
# objects name it through a reference, the way a real root does.
declared_tf() {
  cat > "$1/main.tf" <<TF
resource "kubernetes_namespace" "app" {
  metadata {
    name = "$2"
  }
}

resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = kubernetes_namespace.app.metadata[0].name
  }
  data = {
    greeting = "hello"
  }
}

resource "kubernetes_service_account" "app" {
  metadata {
    name      = "app"
    namespace = kubernetes_namespace.app.metadata[0].name
  }
}
TF
}

# undeclared_tf <dir> <namespace>: the namespace is the platform team's.
undeclared_tf() {
  cat > "$1/main.tf" <<TF
resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = "$2"
  }
  data = {
    greeting = "hello"
  }
}

resource "kubernetes_service_account" "app" {
  metadata {
    name      = "app"
    namespace = "$2"
  }
}
TF
}

versions_tf "$SMOKE_WORK/nsd" "$NSD" kubernetes;  declared_tf "$SMOKE_WORK/nsd" "$NSD"
versions_tf "$SMOKE_WORK/nsu" "$NSU" local;       undeclared_tf "$SMOKE_WORK/nsu" "$NSU"
versions_tf "$SMOKE_WORK/stock-nsd" "" "";        declared_tf "$SMOKE_WORK/stock-nsd" "$STOCK_NSD"
versions_tf "$SMOKE_WORK/stock-nsu" "" "";        undeclared_tf "$SMOKE_WORK/stock-nsu" "$STOCK_NSU"

run_chdf() { local d="$1"; shift; ( cd "$SMOKE_WORK/$d" && chdf "$@" -input=false -no-color 2>&1 ); }
run_stock() { local d="$1"; shift; ( cd "$SMOKE_WORK/$d" && terraform "$@" -input=false -no-color 2>&1 ); }

# creates <plan>: the plan's proposals, one address per line, sorted, with
# the action that follows it. Every proposal, not only the creates, so a
# destroy or an orphan would show up as a difference.
proposals() { grep -E '^  # [^ ]+ (will|must) be ' <<< "$1" | sed -E 's/^  # //' | sort; }

# same_as_stock <what> <live-plan> <stock-plan>: the two plans propose the
# same actions on the same addresses, and their Plan: lines agree.
same_as_stock() {
  local what="$1" lp="$2" sp="$3" l s
  l="$(proposals "$lp")"; s="$(proposals "$sp")"
  { echo "choudoufu:"; sed 's/^/  /' <<< "$l"; grep -E '^Plan:|^No changes' <<< "$lp" | head -1 | sed 's/^/  /'
    echo "stock:";     sed 's/^/  /' <<< "$s"; grep -E '^Plan:|^No changes' <<< "$sp" | head -1 | sed 's/^/  /'; } | evidence
  [ "$l" = "$s" ] \
    || fail "$SCEN" "$what: choudoufu's plan proposes [$(tr '\n' ';' <<< "$l")], stock's from the same position proposes [$(tr '\n' ';' <<< "$s")]"
  [ "$(grep -E '^Plan:|^No changes' <<< "$lp" | head -1)" = "$(grep -E '^Plan:|^No changes' <<< "$sp" | head -1)" ] \
    || fail "$SCEN" "$what: the Plan lines differ: $(grep -E '^Plan:|^No changes' <<< "$lp" | head -1) / $(grep -E '^Plan:|^No changes' <<< "$sp" | head -1)"
  if grep -qE 'orphan_' <<< "$lp"; then
    fail "$SCEN" "$what: the plan names an orphan, so something in the deleted namespace was swept: $(grep -E 'orphan_' <<< "$lp" | head -3)"
  fi
}

# listed <estate> <dir>: how many objects live-ls finds carrying the label.
listed() {
  ( cd "$SMOKE_WORK/$2" && chdf live-ls -estate="$1" -no-color . 2>&1 ) | sed -nE 's/^Estate "[^"]+": ([0-9]+) resource\(s\) carry its marker\./\1/p'
}

empty_plan() { # <dir> <what>
  local p
  p="$(run_chdf "$1" plan)" || fail "$SCEN" "$2: the plan failed: $(tail -15 <<< "$p")"
  grep -E '^No changes|^Plan:' <<< "$p" | head -1 | evidence
  grep -q 'No changes.' <<< "$p" || fail "$SCEN" "$2: the plan is not empty: $(proposals "$p" | tr '\n' ';')"
}

gone() { # <namespace>: wait for the namespace to finish terminating
  local i
  for i in $(seq 1 90); do
    kc get namespace "$1" >/dev/null 2>&1 || return 0
    sleep 1
  done
  return 1
}

cluster_up

step "1. two estates and their stock twins apply"
explain \
  "smoke-nsd declares its namespace and keeps its records in the cluster;" \
  "smoke-nsu lives in a namespace the platform team made with kubectl." \
  "Stock applies the identical shapes into namespaces of its own, with a" \
  "state file, so every later plan has stock's answer beside it."
cmd "kubectl create namespace $STORE_NS $NSU $STOCK_NSU && choudoufu apply && terraform apply   # x2"
kc create namespace "$STORE_NS" >/dev/null || fail "$SCEN" "could not create the record store's namespace"
kc create namespace "$NSU" >/dev/null || fail "$SCEN" "could not create $NSU"
kc create namespace "$STOCK_NSU" >/dev/null || fail "$SCEN" "could not create $STOCK_NSU"
for d in nsd nsu; do
  logged "k8s-a-deleted-namespace-is-gone-$d-init" "$SCEN" "choudoufu init failed in $d" -- run_chdf "$d" init
  A="$(run_chdf "$d" apply -auto-approve)" || fail "$SCEN" "choudoufu apply failed in $d: $(tail -15 <<< "$A")"
  grep -E 'Apply complete!' <<< "$A" | sed "s/^/$d: /" | evidence
done
for d in stock-nsd stock-nsu; do
  logged "k8s-a-deleted-namespace-is-gone-$d-init" "$SCEN" "stock init failed in $d" -- run_stock "$d" init
  A="$(run_stock "$d" apply -auto-approve)" || fail "$SCEN" "stock apply failed in $d: $(tail -15 <<< "$A")"
  grep -E 'Apply complete!' <<< "$A" | sed "s/^/$d: /" | evidence
done
[ "$(listed "$NSD" nsd)" = "3" ] || fail "$SCEN" "live-ls does not list smoke-nsd's three objects: $(cd "$SMOKE_WORK/nsd" && chdf live-ls -estate="$NSD" -no-color . 2>&1 | head -3)"
[ "$(listed "$NSU" nsu)" = "2" ] || fail "$SCEN" "live-ls does not list smoke-nsu's two objects"
[ -n "$(kc get secrets -n "$STORE_NS" -l tofu-estate="$NSD" -o name)" ] \
  || fail "$SCEN" "smoke-nsd's apply wrote no record into $STORE_NS"
proof "three objects carry tofu-estate=$NSD and two carry tofu-estate=$NSU; stock holds the same five in a state file."

step "2. before the fault, every plan is empty"
cmd "choudoufu plan   # in both"
empty_plan nsd "smoke-nsd before the fault"
empty_plan nsu "smoke-nsu before the fault"
proof "converged. Whatever the next plan proposes, the delete in step 3 is what put it there."

step "3. kubectl delete namespace, under all four"
explain \
  "The API server deletes every object in a namespace it is deleting," \
  "whoever owns it. The step waits for each namespace to finish" \
  "terminating: a namespace still terminating is step 6's question."
cmd "kubectl delete namespace $NSD $NSU $STOCK_NSD $STOCK_NSU"
kc delete namespace "$NSD" "$NSU" "$STOCK_NSD" "$STOCK_NSU" --wait=false >/dev/null \
  || fail "$SCEN" "kubectl delete namespace failed"
for n in "$NSD" "$NSU" "$STOCK_NSD" "$STOCK_NSU"; do
  gone "$n" || fail "$SCEN" "namespace $n was still terminating after 90s, so there is no deleted namespace to measure"
done
{ kc get namespace "$NSD" "$NSU" 2>&1 || true; } | evidence
proof "all four namespaces are gone, and everything in them with them."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - assert the plan a blind sweep would make: nothing"
  explain \
    "You asked for proof the assertions can fail. A sweep that never saw" \
    "the delete - one that read a cache, or took a NotFound for presence -" \
    "would plan nothing here. Assert exactly that, over the real plan. It" \
    "must not hold: the plan has to propose the creates."
  cmd "choudoufu plan   # expected: No changes."
  BPLAN="$(run_chdf nsd plan)" || fail "$SCEN" "BREAK: the plan failed: $(tail -15 <<< "$BPLAN")"
  grep -E '^No changes|^Plan:' <<< "$BPLAN" | head -1 | evidence
  if grep -q 'No changes.' <<< "$BPLAN"; then
    fail "$SCEN" "BREAK: the plan after the namespace delete is empty, so the assertion this control exists to defeat held"
  fi
  grep -qE '^Plan: 3 to add, 0 to change, 0 to destroy\.' <<< "$BPLAN" \
    || fail "$SCEN" "BREAK: the plan is neither empty nor the three creates: $(proposals "$BPLAN" | tr '\n' ';')"
  proof "caught. \"No changes.\" does not hold after the delete: the plan proposes the three creates, so the empty plans of step 2 and the creates of step 4 are the cluster's answer and not the assertion's."
  exit 0
fi

step "4. declared namespace - the sweep and the plan agree it is all gone, and the plan is stock's"
explain \
  "live-ls lists the estate by its label, and there is no object left to" \
  "carry it. The plan has to say the same: three creates, nothing read as" \
  "present, nothing swept as an orphan - exactly what stock's refresh" \
  "makes of its state file over the same delete."
cmd "choudoufu live-ls && choudoufu plan   # beside: terraform plan"
LISTED="$(listed "$NSD" nsd)"
echo "live-ls: $LISTED object(s) carry tofu-estate=$NSD" | evidence
[ "$LISTED" = "0" ] || fail "$SCEN" "live-ls still lists $LISTED object(s) of smoke-nsd after its namespace was deleted"
LP4="$(run_chdf nsd plan)" || fail "$SCEN" "the plan after the delete failed: $(tail -15 <<< "$LP4")"
SP4="$(run_stock stock-nsd plan)" || fail "$SCEN" "stock's plan after the delete failed: $(tail -15 <<< "$SP4")"
same_as_stock "declared namespace" "$LP4" "$SP4"
grep -qE '^Plan: 3 to add, 0 to change, 0 to destroy\.' <<< "$LP4" \
  || fail "$SCEN" "the plan does not propose the three creates: $(grep -E '^Plan:|No changes' <<< "$LP4")"
cmd "choudoufu apply -auto-approve -parallelism=1   # beside: terraform apply"
LA4="$(run_chdf nsd apply -auto-approve -parallelism=1)" || fail "$SCEN" "the apply after the delete failed: $(tail -15 <<< "$LA4")"
SA4="$(run_stock stock-nsd apply -auto-approve -parallelism=1)" || fail "$SCEN" "stock's apply after the delete failed: $(tail -15 <<< "$SA4")"
grep -E ': Creat|Apply complete' <<< "$LA4" | evidence
for side in LA4 SA4; do
  out="${!side}"
  first="$(grep -nE '^[a-z_]+\.[a-z_]+: Creating\.\.\.' <<< "$out" | head -1 | cut -d: -f2-)"
  ns_done="$(grep -nE '^kubernetes_namespace\.app: Creation complete' <<< "$out" | head -1 | cut -d: -f1)"
  other="$(grep -nE '^kubernetes_(config_map|service_account)\.app: Creating\.\.\.' <<< "$out" | head -1 | cut -d: -f1)"
  [ "$first" = "kubernetes_namespace.app: Creating..." ] && [ -n "$ns_done" ] && [ -n "$other" ] && [ "$ns_done" -lt "$other" ] \
    || fail "$SCEN" "$side: the namespace's create did not complete before the objects inside it were dispatched: $(grep -E ': Creat' <<< "$out" | tr '\n' ';')"
done
grep -qE 'Apply complete! Resources: 3 added, 0 changed, 0 destroyed\.' <<< "$LA4" \
  || fail "$SCEN" "the apply did not add the three objects back: $(grep 'Apply complete' <<< "$LA4")"
empty_plan nsd "smoke-nsd after the apply"
proof "the listing and the plan agreed the namespace and both objects were gone, the plan was stock's three creates, the namespace was created first on both sides, and one apply converged."

step "5. undeclared namespace - the same plan as stock, and the same refusal from the API server"
explain \
  "Nothing in this estate can make the namespace back, and stock cannot" \
  "either: both plans propose the two creates, and both applies fail in" \
  "the provider's words, because the namespace is not there to create" \
  "into. When the platform team puts it back, one apply converges."
cmd "choudoufu live-ls && choudoufu plan && choudoufu apply   # beside: terraform"
LISTED="$(listed "$NSU" nsu)"
echo "live-ls: $LISTED object(s) carry tofu-estate=$NSU" | evidence
[ "$LISTED" = "0" ] || fail "$SCEN" "live-ls still lists $LISTED object(s) of smoke-nsu after its namespace was deleted"
LP5="$(run_chdf nsu plan)" || fail "$SCEN" "the plan after the delete failed: $(tail -15 <<< "$LP5")"
SP5="$(run_stock stock-nsu plan)" || fail "$SCEN" "stock's plan after the delete failed: $(tail -15 <<< "$SP5")"
same_as_stock "undeclared namespace" "$LP5" "$SP5"
grep -qE '^Plan: 2 to add, 0 to change, 0 to destroy\.' <<< "$LP5" \
  || fail "$SCEN" "the plan does not propose the two creates: $(grep -E '^Plan:|No changes' <<< "$LP5")"
LA5_RC=0; LA5="$(run_chdf nsu apply -auto-approve)" || LA5_RC=$?
SA5_RC=0; SA5="$(run_stock stock-nsu apply -auto-approve)" || SA5_RC=$?
{ grep -E '^Error:' <<< "$LA5" | sed 's/^/choudoufu: /'; grep -E '^Error:' <<< "$SA5" | sed 's/^/stock:     /'; } | evidence
[ "$LA5_RC" != "0" ] && [ "$SA5_RC" != "0" ] \
  || fail "$SCEN" "an apply into a deleted namespace succeeded (choudoufu rc=$LA5_RC, stock rc=$SA5_RC)"
[ "$(grep -cE "^Error: namespaces \"$NSU\" not found" <<< "$LA5")" = "2" ] \
  || fail "$SCEN" "choudoufu's apply did not fail on both objects with the API server's not-found: $(grep -E '^Error' <<< "$LA5" | head -4)"
[ "$(grep -cE "^Error: namespaces \"$STOCK_NSU\" not found" <<< "$SA5")" = "2" ] \
  || fail "$SCEN" "stock's apply did not fail the same way, so the comparison has no oracle: $(grep -E '^Error' <<< "$SA5" | head -4)"
cmd "kubectl create namespace $NSU && choudoufu apply -auto-approve"
kc create namespace "$NSU" >/dev/null || fail "$SCEN" "could not put $NSU back"
LA5b="$(run_chdf nsu apply -auto-approve)" || fail "$SCEN" "the apply once the namespace was back failed: $(tail -15 <<< "$LA5b")"
grep -E 'Apply complete' <<< "$LA5b" | evidence
grep -qE 'Apply complete! Resources: 2 added, 0 changed, 0 destroyed\.' <<< "$LA5b" \
  || fail "$SCEN" "the apply did not add the two objects back: $(grep 'Apply complete' <<< "$LA5b")"
empty_plan nsu "smoke-nsu after the apply"
proof "the same two creates as stock, the same two not-found errors as stock, and once the namespace was back one apply converged."

step "6. a namespace still terminating - fault 1's shape, reported rather than folded in"
explain \
  "A finalizer on the ConfigMap holds the namespace in Terminating: the" \
  "namespace and the held ConfigMap are still there, the ServiceAccount" \
  "is not. Stock's refresh reads the first two as present and the third" \
  "as gone; the plan here has to read them the same way. An apply in this" \
  "window would be refused by the API server, which creates nothing in a" \
  "terminating namespace; that is the platform's answer, not either tool's."
cmd "kubectl patch configmap app-config --finalizers && kubectl delete namespace --wait=false && choudoufu plan   # beside: terraform plan"
for n in "$NSD" "$STOCK_NSD"; do
  kc patch configmap app-config -n "$n" --type merge -p "{\"metadata\":{\"finalizers\":[\"$FINALIZER\"]}}" >/dev/null \
    || fail "$SCEN" "could not put the finalizer on $n/app-config"
  kc delete namespace "$n" --wait=false >/dev/null || fail "$SCEN" "could not delete $n"
done
for n in "$NSD" "$STOCK_NSD"; do
  for i in $(seq 1 60); do
    kc get serviceaccount app -n "$n" >/dev/null 2>&1 || break
    sleep 1
  done
  kc get serviceaccount app -n "$n" >/dev/null 2>&1 && fail "$SCEN" "$n/app outlived its namespace's delete by 60s"
done
kc get namespace "$NSD" -o jsonpath='{.metadata.name} {.status.phase}{"\n"}' | evidence
LP6="$(run_chdf nsd plan)" || fail "$SCEN" "the plan in the terminating window failed: $(tail -15 <<< "$LP6")"
SP6="$(run_stock stock-nsd plan)" || fail "$SCEN" "stock's plan in the terminating window failed: $(tail -15 <<< "$SP6")"
same_as_stock "terminating namespace" "$LP6" "$SP6"
grep -qE '^Plan: 1 to add, 0 to change, 0 to destroy\.' <<< "$LP6" \
  || fail "$SCEN" "the plan in the window is not the one create stock makes: $(grep -E '^Plan:|No changes' <<< "$LP6")"
grep -qE '^  # kubernetes_service_account\.app will be created' <<< "$LP6" \
  || fail "$SCEN" "the one create is not the ServiceAccount the delete took: $(proposals "$LP6" | tr '\n' ';')"
for n in "$NSD" "$STOCK_NSD"; do
  kc patch configmap app-config -n "$n" --type merge -p '{"metadata":{"finalizers":null}}' >/dev/null \
    || fail "$SCEN" "could not clear the finalizer on $n/app-config"
done
gone "$NSD" || fail "$SCEN" "$NSD did not finish terminating once the finalizer cleared"
gone "$STOCK_NSD" || fail "$SCEN" "$STOCK_NSD did not finish terminating once the finalizer cleared"
proof "in the window the plan is stock's: the terminating namespace and the held ConfigMap read as present and are not proposed, and the ServiceAccount the delete already took is the one create. Once the finalizer cleared, the namespace finished going."

step "7. the record store's namespace deleted - refused by name, and nothing written"
explain \
  "smoke-nsd's objects are gone again from step 6, so a run that went" \
  "ahead would have three creates to make. The record store's namespace" \
  "is deleted too. A list there answers empty, which would read as an" \
  "estate with no records; the store refuses instead, on the plan and on" \
  "the apply, with the kubectl line that makes the namespace, and the" \
  "refused apply makes nothing: no namespace, no object, no record."
cmd "kubectl delete namespace $STORE_NS && choudoufu plan && choudoufu apply -auto-approve"
kc delete namespace "$STORE_NS" --wait=false >/dev/null || fail "$SCEN" "could not delete $STORE_NS"
gone "$STORE_NS" || fail "$SCEN" "$STORE_NS did not finish terminating"
for verb in plan apply; do
  RC=0
  if [ "$verb" = "plan" ]; then OUT="$(run_chdf nsd plan)" || RC=$?; else OUT="$(run_chdf nsd apply -auto-approve)" || RC=$?; fi
  { grep -E '^Error:' <<< "$OUT"; grep -oE "namespace \"$STORE_NS\" does not exist" <<< "$OUT" | head -1; grep -oE "\`kubectl create namespace $STORE_NS\`" <<< "$OUT" | head -1; } | sed "s/^/$verb: /" | evidence
  [ "$RC" != "0" ] || fail "$SCEN" "the $verb went ahead with the record store's namespace deleted: $(grep -E '^Plan:|Apply complete|No changes' <<< "$OUT" | head -2)"
  grep -q 'Error: Cannot open the record store' <<< "$OUT" \
    || fail "$SCEN" "the $verb failed, but not because the record store could not be opened: $(grep -E '^Error' <<< "$OUT" | head -3)"
  grep -qF "namespace \"$STORE_NS\" does not exist" <<< "$OUT" \
    || fail "$SCEN" "the $verb's refusal does not name the namespace that is missing: $(tail -12 <<< "$OUT")"
  grep -qF "\`kubectl create namespace $STORE_NS\`" <<< "$OUT" \
    || fail "$SCEN" "the $verb's refusal does not give the kubectl line that makes the namespace: $(tail -12 <<< "$OUT")"
  if grep -qE ': Creating\.\.\.|Apply complete' <<< "$OUT"; then
    fail "$SCEN" "the $verb dispatched a write while refusing: $(grep -E ': Creating|Apply complete' <<< "$OUT" | head -3)"
  fi
done
if kc get namespace "$STORE_NS" >/dev/null 2>&1; then fail "$SCEN" "the refused run created $STORE_NS itself"; fi
if kc get namespace "$NSD" >/dev/null 2>&1; then fail "$SCEN" "the refused apply created $NSD"; fi
WROTE="$(kc get namespaces,configmaps,serviceaccounts,secrets -A -l tofu-estate="$NSD" -o name 2>&1)"
echo "labelled tofu-estate=$NSD: ${WROTE:-nothing}" | evidence
[ -z "$WROTE" ] || fail "$SCEN" "the refused runs left objects carrying the estate's label: $WROTE"
cmd "kubectl create namespace $STORE_NS && choudoufu apply -auto-approve && choudoufu plan"
kc create namespace "$STORE_NS" >/dev/null || fail "$SCEN" "could not put $STORE_NS back"
LA7="$(run_chdf nsd apply -auto-approve)" || fail "$SCEN" "the apply once the store's namespace was back failed: $(tail -15 <<< "$LA7")"
grep -E 'Apply complete' <<< "$LA7" | evidence
grep -qE 'Apply complete! Resources: 3 added, 0 changed, 0 destroyed\.' <<< "$LA7" \
  || fail "$SCEN" "the apply did not add the three objects back: $(grep 'Apply complete' <<< "$LA7")"
empty_plan nsd "smoke-nsd after the store's namespace came back"
proof "refused by name twice, with the namespace and the kubectl line; nothing was created while it was missing; once it was back, one apply converged."

step "8. teardown"
for d in nsd nsu; do
  D="$(run_chdf "$d" apply -destroy -auto-approve)" || fail "$SCEN" "apply -destroy failed in $d: $(tail -10 <<< "$D")"
done
LEFT="$(kc get namespaces,configmaps,serviceaccounts -A -l "tofu-estate in ($NSD,$NSU)" -o name 2>&1)"
echo "left: ${LEFT:-none}" | evidence
[ -z "$LEFT" ] || fail "$SCEN" "objects carrying either estate's label survived the destroy: $LEFT"
proof "nothing carrying either estate's label is left in the cluster."

echo "  What you watched: kubectl delete namespace took an estate's objects"
echo "  with it, and the next plan read them as gone - nothing listed,"
echo "  nothing swept as an orphan - and proposed exactly what stock proposed"
echo "  from the same position, the namespace first when the estate declares"
echo "  it. A namespace still terminating read as stock reads it. A deleted"
echo "  record-store namespace was refused by name, and nothing was written."
