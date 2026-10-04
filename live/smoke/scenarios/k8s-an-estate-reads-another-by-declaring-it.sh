# k8s-an-estate-reads-another-by-declaring-it
# CLAIM 44 (kubernetes) - Reading another estate is declared. ~1 min.
#
# This proof: on record_store "kubernetes", an estate reads another estate's
# outputs only by declaring the read in its record_store block
# (reads_outputs_of "<estate>" {}); an undeclared read is refused before
# anything is sent, even under cluster-admin; a declared read under an
# identity granted get on the producer's output Secret plans with the value
# and says how old it is, and writes nothing to the producer's namespace; a
# destroyed producer's values are gone.
#
# First run 2026-10-04, both arms green on kind (Kubernetes v1.37.0). The behaviour it
# proves is internal/live/projection/estateoutputs_kubernetes.go, whose unit
# tests (estateoutputs_kubernetes_test.go) are the same claim against a fake
# API server; this is the real one, with real RBAC answering.
#
# Two things the first run settles, both by reading only so far:
#   - The cluster contract (#1393) on the consumer. The consumer's identity
#     is scoped to its own records namespace plus get-by-name on one Secret
#     of network's. The contract's read_isolation check asks the authorizer
#     about get and list on secrets in another records namespace WITHOUT a
#     resource name, which a resourceNames-scoped Role does not satisfy, so
#     the declared grant should not read as a breach. It is waived here all
#     the same, with encryption_at_rest and estate_boundary (kind has
#     neither), so a contract finding cannot be mistaken for this claim's
#     refusal. Step 4 asks the authorizer the narrow question directly.
#   - The producer runs as kind's cluster-admin, which is why its own
#     record_store waives the same three.
#
# BREAK=1 leaves the consumer's configuration declaring the read and does
# NOT grant its identity get on network's output Secret. The plan must then
# refuse with "This estate may not read another estate's outputs", naming
# estate network, its namespace, and the kubectl Role line that grants the
# read, and a plan that shows network's value fails the control.

W="$SMOKE_WORKROOT/k8s-estateoutputs"; PRODUCER="$W/network"; CONSUMER="$W/app"
mkdir -p "$PRODUCER" "$CONSUMER"
SMOKE_WORK="$W"; export SMOKE_WORK
NET_NS="tofu-records-network"; APP_NS="tofu-records-app"
APP_KC="$W/app.kubeconfig"
# flat undoes the CLI's word wrap before a sentence is matched.
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }

# live_block <estate> [extra record_store body]: the terraform block for one
# estate, its records in its default namespace. No provider is needed: the
# record store reads KUBE_CONFIG_PATH itself, and terraform_data is builtin.
live_block() {
  cat <<TFEOF
terraform {
  live {
    estate = "$1"

    record_store "kubernetes" {
      allow_insecure = ["read_isolation", "encryption_at_rest", "estate_boundary"]
$2
    }
  }
}
TFEOF
}

{ live_block network ""; cat <<'TFEOF'

resource "terraform_data" "anchor" {
  input = "network"
}

# A value no live resource holds: a name this estate chose.
output "cluster_services_namespace" {
  value = "cluster-services"
}
TFEOF
} > "$PRODUCER/main.tf"

# consumer_config <record_store body>: app, reading network's output.
consumer_config() {
  { live_block app "$1"; cat <<'TFEOF'

data "terraform_estate_outputs" "network" {
  estate = "network"
  names  = ["cluster_services_namespace"]
}

resource "terraform_data" "service" {
  input = data.terraform_estate_outputs.network.values.cluster_services_namespace
}
TFEOF
  } > "$CONSUMER/main.tf"
}

step "the claim"
explain \
  "On Kubernetes each estate's records live in a namespace of their own," \
  "and that namespace is the read boundary. An estate reads another's" \
  "root outputs only by naming it in its record_store block with" \
  "reads_outputs_of; the read goes to that estate's namespace, can only" \
  "get, and needs a Role granting get on the producer's output Secrets." \
  "An undeclared read is refused before anything is sent. A missing" \
  "grant is refused naming the Role to create. The value is a copy as" \
  "of the producer's last apply, and the plan says so with the time."

step "1. one cluster, two records namespaces, a scoped identity for app"
cluster_up
cmd "kubectl create namespace $NET_NS ; kubectl create namespace $APP_NS"
kc create namespace "$NET_NS" >/dev/null || fail "k8s-estateoutputs" "could not create $NET_NS"
kc create namespace "$APP_NS" >/dev/null || fail "k8s-estateoutputs" "could not create $APP_NS"
# app's identity: every verb on its own records, nothing anywhere else.
kc create serviceaccount app -n default >/dev/null || fail "k8s-estateoutputs" "could not create the app ServiceAccount"
kc create role app-records -n "$APP_NS" --verb=get,list,create,update,delete --resource=secrets >/dev/null \
  || fail "k8s-estateoutputs" "could not create app's records Role"
kc create rolebinding app-records -n "$APP_NS" --role=app-records --serviceaccount=default:app >/dev/null \
  || fail "k8s-estateoutputs" "could not bind app's records Role"
TOK="$(kc create token app -n default --duration=2h)" || fail "k8s-estateoutputs" "could not mint a token for app"
cp "$KUBECONFIG" "$APP_KC"
kubectl --kubeconfig "$APP_KC" config set-credentials app --token="$TOK" >/dev/null
kubectl --kubeconfig "$APP_KC" config set-context --current --user=app >/dev/null
# as_app runs one command under app's own kubeconfig. KUBE_CONFIG_PATH is
# what the record store's connection loader reads.
as_app() ( export KUBECONFIG="$APP_KC" KUBE_CONFIG_PATH="$APP_KC"; "$@" )
proof "two records namespaces, and an identity for app that reaches only its own."

step "2. the producer applies and records its output"
cmd "choudoufu apply -auto-approve   # in network/, as the cluster admin"
logged k8s-estateoutputs-producer-init "k8s-estateoutputs" "init failed in the producer" -- in_dir "$PRODUCER" chdf init -input=false -no-color
P_OUT="$(cd "$PRODUCER" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-estateoutputs" "the producer could not apply: $P_OUT"
grep -q "Resources: 1 added" <<< "$P_OUT" || fail "k8s-estateoutputs" "the producer's apply: $P_OUT"
SECRETS="$(kc get secrets -n "$NET_NS" -l tofu-estate=network,choudoufu.intentius.io/record-namespace=tofu-outputs -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')" \
  || fail "k8s-estateoutputs" "could not list network's output Secrets"
[ "$(grep -c . <<< "$SECRETS")" = "1" ] || fail "k8s-estateoutputs" "network should hold exactly one recorded output Secret, and holds: [$SECRETS]"
SECRET="$(head -1 <<< "$SECRETS")"
echo "$NET_NS/$SECRET" | evidence
proof "network's root output is one Secret in $NET_NS."

step "3. an undeclared read is refused, even for an identity that may read everything"
explain \
  "The consumer's data block names network, and its record_store block" \
  "declares nothing. It runs as the cluster admin, who can read every" \
  "Secret in the cluster, so a refusal here is the configuration's and" \
  "not RBAC's."
consumer_config ""
logged k8s-estateoutputs-consumer-init "k8s-estateoutputs" "init failed in the consumer" -- in_dir "$CONSUMER" chdf init -input=false -no-color
cmd "choudoufu plan   # in app/, as the cluster admin, no reads_outputs_of"
U_OUT="$(cd "$CONSUMER" && chdf plan -input=false -no-color 2>&1)" \
  && fail "k8s-estateoutputs" "an undeclared read planned: $U_OUT"
U_FLAT="$(flat <<< "$U_OUT")"
grep -q '"cluster-services"' <<< "$U_OUT" && fail "k8s-estateoutputs" "the undeclared plan shows network's value: $U_OUT"
grep -q "This estate does not declare that it reads another estate's outputs" <<< "$U_FLAT" \
  || fail "k8s-estateoutputs" "the plan failed, but not with the undeclared-read refusal: $U_OUT"
grep -q 'reads_outputs_of "network" {}' <<< "$U_FLAT" \
  || fail "k8s-estateoutputs" "the refusal does not give the block that declares the read: $U_OUT"
grep -oE "Error: This estate does not declare that it reads another estate's outputs|reads_outputs_of \"network\" \{\}" <<< "$U_FLAT" | evidence
proof "with nothing declared, the read is refused before it is sent, under an identity RBAC would have let through."

step "4. the read, declared"
consumer_config '      reads_outputs_of "network" {}'
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: the consumer declares the read, and app's identity is" \
    "NOT granted get on network's output Secret. The plan must stop," \
    "naming estate network and the Role that grants the read. A plan that" \
    "reads the value anyway means something other than this grant is" \
    "letting it through."
  cmd "# no Role in $NET_NS for app"
else
  explain \
    "app's identity gets one Role in network's namespace: get, on the one" \
    "output Secret by name. No list, so no other record of network's is" \
    "readable through it."
  cmd "kubectl -n $NET_NS create role tofu-reads-outputs-of-network --verb=get --resource=secrets --resource-name=$SECRET"
  kc create role tofu-reads-outputs-of-network -n "$NET_NS" --verb=get --resource=secrets --resource-name="$SECRET" >/dev/null \
    || fail "k8s-estateoutputs" "could not create the read Role"
  kc create rolebinding tofu-reads-outputs-of-network-app -n "$NET_NS" --role=tofu-reads-outputs-of-network --serviceaccount=default:app >/dev/null \
    || fail "k8s-estateoutputs" "could not bind the read Role"
  # The authorizer's own answers about the grant's width.
  SA="system:serviceaccount:default:app"
  ONE="$(kc auth can-i get "secrets/$SECRET" -n "$NET_NS" --as="$SA" 2>&1 || true)"
  ANY="$(kc auth can-i get secrets -n "$NET_NS" --as="$SA" 2>&1 || true)"
  LST="$(kc auth can-i list secrets -n "$NET_NS" --as="$SA" 2>&1 || true)"
  printf 'app may get secrets/%s: %s\napp may get any secret in %s: %s\napp may list secrets in %s: %s\n' "$SECRET" "$ONE" "$NET_NS" "$ANY" "$NET_NS" "$LST" | evidence
  [ "$ONE" = "yes" ] || fail "k8s-estateoutputs" "the authorizer says app may not get the output Secret it was just granted: $ONE"
  [ "$ANY" = "no" ] && [ "$LST" = "no" ] || fail "k8s-estateoutputs" "the grant is wider than one Secret: get-any=$ANY list=$LST"
fi
BEFORE="$(kc get secrets -n "$NET_NS" -o jsonpath='{range .items[*]}{.metadata.name}={.metadata.resourceVersion}{"\n"}{end}' | sort)"
cmd "choudoufu plan   # in app/, as app"
C_OUT="$(as_app in_dir "$CONSUMER" chdf plan -input=false -no-color 2>&1)" && C_RC=0 || C_RC=$?
C_FLAT="$(flat <<< "$C_OUT")"
AFTER="$(kc get secrets -n "$NET_NS" -o jsonpath='{range .items[*]}{.metadata.name}={.metadata.resourceVersion}{"\n"}{end}' | sort)"
[ "$BEFORE" = "$AFTER" ] || fail "k8s-estateoutputs" "the consumer's plan changed network's namespace: before [$BEFORE] after [$AFTER]"

if [ "${BREAK:-0}" = "1" ]; then
  [ "$C_RC" != "0" ] || fail "k8s-estateoutputs" "the consumer planned without the grant, so the grant is not what this claim measures: $C_OUT"
  grep -q '"cluster-services"' <<< "$C_OUT" && fail "k8s-estateoutputs" "the consumer's plan shows network's value without the grant: $C_OUT"
  grep -q "This estate may not read another estate's outputs" <<< "$C_FLAT" \
    || fail "k8s-estateoutputs" "the plan failed, but not with the denial refusal, so nothing here shows the missing grant stopped it: $C_OUT"
  grep -q "reading estate \"network\"'s output" <<< "$C_FLAT" \
    || fail "k8s-estateoutputs" "the refusal does not name estate network as the one it may not read: $C_OUT"
  grep -q "in namespace \"$NET_NS\"" <<< "$C_FLAT" \
    || fail "k8s-estateoutputs" "the refusal does not name network's namespace: $C_OUT"
  grep -q "create role tofu-reads-outputs-of-network --verb=get --resource=secrets --resource-name=$SECRET" <<< "$C_FLAT" \
    || fail "k8s-estateoutputs" "the refusal does not give the Role that grants exactly this read: $C_OUT"
  grep -oE "Error: This estate may not read another estate's outputs|kubectl -n $NET_NS create role [^ ]+ --verb=get --resource=secrets --resource-name=[a-z0-9-]+" <<< "$C_FLAT" | evidence
  proof "caught - with the read declared and the grant missing, the plan refuses and names estate network, its namespace and the Role that grants get on its output Secret."
  exit 0
fi

[ "$C_RC" = "0" ] || fail "k8s-estateoutputs" "the consumer could not plan with the read declared and granted: $C_OUT"
grep -qE '\+ input += "cluster-services"' <<< "$C_OUT" \
  || fail "k8s-estateoutputs" "the consumer's plan does not use the value network recorded: $C_OUT"
grep -q "Values from another estate are as of its last apply" <<< "$C_FLAT" \
  || fail "k8s-estateoutputs" "the plan does not say the value is as of network's last apply: $C_OUT"
grep -qE "read from estate \"network\" as recorded by its last apply, at [0-9]{4}-[0-9]{2}-[0-9]{2}T" <<< "$C_FLAT" \
  || fail "k8s-estateoutputs" "the as-of warning does not name estate network and the time of its record: $C_OUT"
grep -E '# terraform_data.service will be created|\+ input += "cluster-services"' <<< "$C_OUT" | sed 's/^ *//' | evidence
echo "objects in $NET_NS unchanged by the consumer's plan: $(grep -c . <<< "$AFTER")" | evidence
proof "the consumer plans with network's value through a grant of one get, says how old it is, and changed nothing of network's."

step "5. the producer is destroyed, and its values go with it"
cmd "choudoufu apply -destroy -auto-approve   # in network/, as the cluster admin"
D_OUT="$(cd "$PRODUCER" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-estateoutputs" "the producer's destroy failed: $D_OUT"
destroyed_exactly k8s-estateoutputs 1 "$D_OUT"
LEFT="$(kc get secrets -n "$NET_NS" -l tofu-estate=network,choudoufu.intentius.io/record-namespace=tofu-outputs -o name | grep -c . || true)"
[ "$LEFT" = "0" ] || fail "k8s-estateoutputs" "network was destroyed and $LEFT output Secret(s) remain in $NET_NS"
cmd "choudoufu plan   # in app/, as app"
G_OUT="$(as_app in_dir "$CONSUMER" chdf plan -input=false -no-color 2>&1)" \
  && fail "k8s-estateoutputs" "the consumer planned against a destroyed estate's outputs: $G_OUT"
G_FLAT="$(flat <<< "$G_OUT")"
grep -q "Another estate has not recorded this output" <<< "$G_FLAT" \
  || fail "k8s-estateoutputs" "the consumer's plan failed, but not because network's output is no longer recorded: $G_OUT"
echo "output Secrets in $NET_NS after the destroy: $LEFT" | evidence
grep -oE "Error: Another estate has not recorded this output|Estate \"network\" has no recorded value for its output \"[a-z_]+\"" <<< "$G_FLAT" | evidence
proof "network's destroy deleted its recorded output, and the consumer is told so by name."

echo "  What you watched: on Kubernetes, a read of another estate's outputs"
echo "  refused until the record_store block declared it, then served through"
echo "  a Role granting get on one Secret, with the plan saying how old the"
echo "  value is, and the producer's destroy taking the value with it."
