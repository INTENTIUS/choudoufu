# k8s-a-label-is-a-change
# CLAIM 27 - An edit to a Kubernetes object's labels or annotations is an ordinary change: a label edited in the configuration plans one in-place update and the apply writes it, stock's own answer for the same edit alongside it, an annotation added and then changed does the same, a label or an annotation DELETED from the configuration is removed from the object and the estate then settles, a key the configuration never declared - the API server's own kubernetes.io/metadata.name, a controller's annotation - stays the server's and churns nothing, and a second working directory holding no record of its own removes the same label when the estate shares its records, as Secrets in the cluster or as objects in a bucket. ~10 min.
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
# Step 6 is the last thing a state file was doing for this type, and
# GitHub issue #1211: a key DELETED from the configuration. It is not the
# same question as an edited one and it cannot be answered from the
# object - "this configuration used to declare this key" is a fact about
# the estate's history, and the object holds no copy of it. What stock
# reads out of its last-applied manifest, a stateless run has to record:
# the estate's residue record carries the label and annotation keys each
# apply declared, and the removal set is (recorded) minus (currently
# declared). metadata.managedFields was tried as the source first and
# refuted on this cluster - the provider resends every key it read back
# on apply, so server-side apply records THIS estate as the writer of
# keys nobody declared, and one apply later it claims
# kubernetes.io/metadata.name. It survives as a safety rail, never as the
# source.
#
# The second half of step 6 is what makes it evidence rather than
# scenery, and it is the half that caught the refuted design: the estate
# has to SETTLE. The removal apply leaves one metadata map unchanged,
# which is when the provider resends it wholesale and the laundering
# happens, so the replans after it run against exactly the world that
# broke the other route.
#
# The one difference from stock this leaves, step 7: a DECLARED label
# changed out of band now plans, where stock's computed_fields swallows it.
# "The configuration was edited" and "the live object drifted" are the same
# observation without a last-applied value to tell them apart, so making
# the first visible necessarily makes the second visible. Step 7 runs the
# same kubectl command against both runners and prints both answers, so the
# difference stays measured rather than worked around. It is deliberately
# last: it leaves the object drifted on purpose.
#
# Steps 8 and 9 are GitHub issue #1394, and they are step 6 asked on behalf
# of the second operator. Everything above them runs on the implied local
# store, where the record step 6 reads is a file beside the module, so the
# removal works for whoever holds that directory and nobody else. Each of
# the two runs the same removal from a second working directory that never
# applied anything and carries nothing of the first's, over a store the two
# directories share: step 8 over record_store "kubernetes" (#1392), which
# keeps the records as Secrets in the cluster the claim already runs on,
# and step 9 over record_store "s3" on the pinned floci emulator, which is
# what a Kubernetes estate had before that store existed. Step 9 is why
# this claim needs both substrates at once and why claims.json carries
# needs_floci for it.
#
# Both read what had never been read for a Kubernetes address: what the
# record itself carries - the estate marker, the address, the record key -
# and whether the write that landed was conditional. On the cluster store
# that is resourceVersion, shown by the version moving across B's write and
# by a write carrying the version from before it being refused; on the
# bucket it is If-None-Match and If-Match, read off the wire through
# live/smoke/s3proxy.py. Step 9b is #1344's case, an update whose object
# was deleted underneath it.
#
# BREAK=1 runs the identical kubectl command against a key the
# configuration does NOT declare - same object, same --overwrite, one key
# different - and requires the opposite outcome: "No changes.". Without
# that control every plan in this scenario would read the same if
# choudoufu simply planned on any difference between the configuration and
# the live object, and the whole of step 5 would be scenery. It then runs
# one control for steps 8 and 9 together: the same two directories on
# record_store "local", where the second one has no record to read and must
# propose nothing.

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

# --- steps 8 and 9, a SHARED record store (GitHub issue #1394) ------------
#
# Steps 1 to 7 run on the implied local store, where the record that says
# which metadata keys this estate declared is a file beside the module. Step
# 6's removal reads that record, and a missing or unreadable one proposes
# removing nothing, with at most a warning (projection/residue.go,
# SummaryResidueUnreadable). So the removal only works for whoever holds
# that directory, and every other operator - a second checkout, a CI runner,
# a fresh clone - gets the quiet answer.
#
# No Kubernetes claim measured a shared store before this. Claims 21 to 27
# were all local, and claims 28 to 38, the bucket backend's, are all AWS.
# The path a Kubernetes estate writes its records to a shared store by had
# never been run for a kubernetes_manifest address.
#
# The same two working directories run it twice, on the two stores a
# Kubernetes estate can actually share. A applies both labels. B has never
# applied anything and carries nothing of A's; it deletes one label from its
# configuration and plans. What B proposes comes from the shared record or
# from nothing at all.
#
#   step 8, record_store "kubernetes": records as Secrets in this cluster
#           (#1392, claim 39). It is first because it needs nothing but the
#           cluster the claim already runs on.
#   step 9, record_store "s3": records as objects in a bucket on the pinned
#           floci emulator. It needs both substrates at once, which is why
#           claims.json carries needs_floci for this claim.
#
# Each reads the record object's own metadata, which no Kubernetes claim had
# read before: the estate marker and the address on the record, and whether
# the write that landed was conditional. Step 9b is #1344's case, an update
# whose object has been deleted underneath it.
#
# The BREAK arm is the identical pair of directories on record_store
# "local": A's record is a file under A, B cannot see it, and B proposes
# nothing. It is one control for both steps, because what it takes away -
# the sharing - is the one thing they have in common. It is not a
# hypothetical failure: it is what a Kubernetes estate on the implied store
# gives its second directory today.
SH_WORK="$SMOKE_WORK/shared"
SH_PROXY_PID=""

# shared_config <dir> <squad: yes|no>. SH_LIVE_BODY is the live block's
# body, which is where the record store under test is named. The two
# directories differ in one thing only, the second declared label, so what
# B proposes cannot be an artefact of any other difference between them.
SH_LIVE_BODY=""
shared_config() {
  local dir="$1" squad="$2" sq=""
  [ "$squad" = "yes" ] && sq='
        "squad" = "blue"'
  mkdir -p "$dir"
  cat > "$dir/main.tf" <<TF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "$SH_ESTATE"

$SH_LIVE_BODY
  }

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}

resource "kubernetes_manifest" "ns" {
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "Namespace"
    "metadata" = {
      "name" = "$SH_NS"
    }
  }
}

resource "kubernetes_manifest" "cm" {
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "ConfigMap"
    "metadata" = {
      "labels" = {
        "tier" = "one"$sq
      }
      "name"      = "shared-config"
      "namespace" = "$SH_NS"
    }
    "data" = {
      "greeting" = "hello"
    }
  }
  depends_on = [kubernetes_manifest.ns]
}
TF
}

# sh_run <dir> <chdf args...> - one command in one of the two directories,
# with the record store's endpoint pointed at the proxy when there is one,
# so every object write step 9 makes is on the proxy's log.
sh_run() {
  local dir="$1"; shift
  (
    cd "$dir" || exit 1
    if [ -n "${SH_PROXY_URL:-}" ]; then export AWS_ENDPOINT_URL="$SH_PROXY_URL"; fi
    chdf "$@"
  )
}

# sh_record_bucket, and not bucket_up: live/smoke_claims_test.go refuses that
# name in a scenario whose row is not real_aws, because bucket-iam.sh's
# bucket_up makes a bucket in the account whose credentials are in the
# environment. This one only ever talks to the emulator.
#
# It makes a bucket the bucket contract accepts (claim 29):
# versioning, a lifecycle that expires noncurrent versions, and the
# public-access block. None of the three is what this step measures; an
# estate's first contact refuses a bucket without them.
sh_record_bucket() {
  awsl s3api create-bucket --bucket "$SH_BUCKET" >/dev/null \
    || fail "$SCEN" "could not create the record bucket on the emulator"
  awsl s3api put-bucket-versioning --bucket "$SH_BUCKET" --versioning-configuration Status=Enabled >/dev/null \
    || fail "$SCEN" "could not enable versioning on the record bucket"
  awsl s3api put-bucket-lifecycle-configuration --bucket "$SH_BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null \
    || fail "$SCEN" "could not set the lifecycle rule on the record bucket"
  awsl s3api put-public-access-block --bucket "$SH_BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null \
    || fail "$SCEN" "could not set the public-access block on the record bucket"
}

# sh_cm_record_key finds the record OBJECT for kubernetes_manifest.cm by the
# tofu-address tag on the object, never by decoding a key. If the tags are
# not there this prints nothing and the caller fails, which is the point:
# the tags are half of what step 9 measures.
sh_cm_record_key() {
  local key addr
  for key in $(awsl s3api list-objects-v2 --bucket "$SH_BUCKET" --prefix "$SH_RECORD_PATH" --query 'Contents[].Key' --output text | tr '\t' '\n' | grep -v '^None$'); do
    addr="$(awsl s3api get-object-tagging --bucket "$SH_BUCKET" --key "$key" --query 'TagSet[?Key==`tofu-address`].Value | [0]' --output text 2>/dev/null)"
    [ "$addr" = "kubernetes_manifest.cm" ] && { echo "$key"; return 0; }
  done
  return 0
}

# sh_cm_record_secret is the same lookup for the cluster store: the record
# SECRET for kubernetes_manifest.cm, found by the estate label the store
# writes and the address annotation, never by recomputing the name's hash.
sh_cm_record_secret() {
  local name
  for name in $(kc get secrets -n "$SH_RECORDS_NS" -l "tofu-estate=$SH_ESTATE" -o name 2>/dev/null | sed 's|^secret/||'); do
    if kc get secret "$name" -n "$SH_RECORDS_NS" -o jsonpath='{.metadata.annotations}' 2>/dev/null \
      | grep -q '"tofu-address":"kubernetes_manifest.cm"'; then
      echo "$name"; return 0
    fi
  done
  return 0
}

sh_secret_version() { kc get secret "$SH_CM_SECRET" -n "$SH_RECORDS_NS" -o jsonpath='{.metadata.resourceVersion}'; }

# shared_store_step <kubernetes|s3|local>. One body, three stores: the only
# thing that changes between the two real arms and the control is the
# record_store block, which is the point.
shared_store_step() {
  local store_kind="$1" A B
  case "$store_kind" in
    # A store per estate name, so the three arms never meet: each brings up
    # its own namespace and its own objects on the one cluster.
    kubernetes) SH_ESTATE="smoke-label-cluster" ;;
    s3)         SH_ESTATE="smoke-label-shared" ;;
    local)      SH_ESTATE="smoke-label-local" ;;
    *) fail "$SCEN" "shared_store_step: no such record store: $store_kind" ;;
  esac
  SH_NS="$SH_ESTATE"
  SH_BUCKET="$SH_ESTATE-records"
  SH_RECORD_PATH="tofu-records/$SH_ESTATE/kubernetes_manifest/"
  # What projection.KubernetesRecordNamespace derives when the block names no
  # namespace. The step never spells it into the configuration, so a change
  # to that derivation shows up here.
  SH_RECORDS_NS="tofu-records-$SH_ESTATE"
  A="$SH_WORK/$store_kind/a"; B="$SH_WORK/$store_kind/b"
  mkdir -p "$SH_WORK/$store_kind"

  case "$store_kind" in
  kubernetes)
    step "8. the same removal from a SECOND directory, over records in the cluster"
    explain \
      "Step 6's removal reads the estate's residue record, and on the" \
      "implied local store that record is a file beside the module. Every" \
      "operator who is not holding that directory has no record, and a" \
      "missing one proposes removing nothing. So a shared store is not an" \
      "extra here: it is what makes step 6 true for a second person." \
      "record_store \"kubernetes\" (#1392) keeps the records as Secrets in" \
      "the cluster this estate already runs on, which is the arrangement a" \
      "Kubernetes team can have without an AWS account. Two working" \
      "directories, one estate, one cluster: A applies both labels, B has" \
      "never applied anything and carries no cache and no file of A's, and" \
      "B deletes one label from its configuration and plans."
    cmd "kubectl create namespace $SH_RECORDS_NS   # the store never creates it"
    explain \
      "The records namespace is the read boundary (claim 39 step 4), so" \
      "creating one is an operator's act and not a side effect of a first" \
      "write. Nothing in this fork creates it. The block below names no" \
      "namespace at all, so what is used is what the estate name derives:" \
      "tofu-records-<estate>."
    kc create namespace "$SH_RECORDS_NS" >/dev/null \
      || fail "$SCEN" "could not create the records namespace $SH_RECORDS_NS"
    SH_LIVE_BODY='    record_store "kubernetes" {}'
    ;;
  s3)
    step "9. the same removal again, over records in a bucket"
    explain \
      "The other store a Kubernetes estate can share, and the one it had" \
      "before #1392: the records are objects in a bucket, here the pinned" \
      "floci emulator. Everything else is step 8 exactly - same two" \
      "directories, same label deleted from B - so what changes is the" \
      "store and nothing else. This is the step that makes the claim need" \
      "both substrates at once, and it does not skip when the emulator is" \
      "absent."
    command -v aws >/dev/null 2>&1 \
      || fail "$SCEN" "the AWS CLI is not installed. This step reads the record object's tags with it and does not skip: a shared record store is the path #1394 says has never been measured for a Kubernetes address, and a run that quietly left it out would print PASS having measured nothing."
    docker info >/dev/null 2>&1 \
      || fail "$SCEN" "Docker is not running. This step needs the pinned floci emulator for the estate's record bucket and does not skip; see #1394."
    stack_up
    export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
    export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
    sh_record_bucket
    # Every record store request from both directories goes through the
    # proxy, which logs each PUT's conditional-write header. Without it a
    # write that landed says nothing about whether it was conditional.
    python3 "$SMOKE_DIR/s3proxy.py" "$FLOCI_PORT" "$SH_WORK" 2>"$SMOKE_WORKROOT/logs/shared-proxy.err" &
    SH_PROXY_PID=$!
    trap 'kill $SH_PROXY_PID 2>/dev/null || true; cleanup' EXIT
    for _ in $(seq 1 50); do [ -s "$SH_WORK/proxy.port" ] && break; sleep 0.1; done
    [ -s "$SH_WORK/proxy.port" ] || fail "$SCEN" "the record store proxy never started"
    SH_PROXY_URL="http://localhost:$(cat "$SH_WORK/proxy.port")"
    SH_LIVE_BODY="    record_store \"s3\" {
      bucket = \"$SH_BUCKET\"
    }"
    ;;
  local)
    step "BREAK control 2 - the same two directories on record_store \"local\": B holds no record and must propose nothing"
    explain \
      "You asked for proof that steps 8 and 9 can fail. Everything about" \
      "them is kept - the same two directories, the same label deleted" \
      "from B's configuration - and one thing is changed: the record store" \
      "is the local one, so A's record is a file under A and B cannot see" \
      "it. B must then propose nothing. Without this control both steps" \
      "would read the same if B's plan proposed the removal from something" \
      "it could work out locally, and the sharing would be scenery. One" \
      "control for both, because the sharing is the one thing they have in" \
      "common."
    SH_LIVE_BODY='    record_store "local" {}'
    ;;
  esac

  cmd "choudoufu apply -auto-approve   # directory A: tier=one and squad=blue, estate $SH_ESTATE"
  shared_config "$A" yes
  sh_run "$A" init -input=false -no-color >/dev/null 2>&1 \
    || fail "$SCEN" "init failed in directory A"
  SH_APPLY_A="$(sh_run "$A" apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "$SCEN" "directory A could not apply: $(tail -20 <<< "$SH_APPLY_A")"
  { grep -E 'Apply complete!' <<< "$SH_APPLY_A" || true; } | evidence
  grep -qE 'Resources: 2 added' <<< "$SH_APPLY_A" \
    || fail "$SCEN" "directory A did not create the namespace and the ConfigMap: $(grep -E 'Apply complete' <<< "$SH_APPLY_A")"
  SH_LABELS_A="$(kc get configmap shared-config -n "$SH_NS" -o jsonpath='{.metadata.labels}')"
  echo "$SH_LABELS_A" | evidence
  grep -q '"squad":"blue"' <<< "$SH_LABELS_A" \
    || fail "$SCEN" "directory A's second declared label is not on the object, so there is nothing for B to remove: $SH_LABELS_A"

  # What A's write left in the store, read from the store and not inferred.
  if [ "$store_kind" = "kubernetes" ]; then
    SH_CM_SECRET="$(sh_cm_record_secret)"
    [ -n "$SH_CM_SECRET" ] \
      || fail "$SCEN" "no Secret in $SH_RECORDS_NS carries tofu-estate=$SH_ESTATE and a tofu-address annotation naming kubernetes_manifest.cm, so this estate's Kubernetes record is either not in the cluster or not marked: $(kc get secrets -n "$SH_RECORDS_NS" -o name | tr '\n' ' ')"
    cmd "kubectl get secret <the cm record> -n $SH_RECORDS_NS -o jsonpath='{.metadata.labels}' and '{.metadata.annotations}'"
    echo "$SH_CM_SECRET" | evidence
    SH_SEC_LABELS="$(kc get secret "$SH_CM_SECRET" -n "$SH_RECORDS_NS" -o jsonpath='{.metadata.labels}')"
    SH_SEC_ANN="$(kc get secret "$SH_CM_SECRET" -n "$SH_RECORDS_NS" -o jsonpath='{.metadata.annotations}')"
    echo "$SH_SEC_LABELS" | evidence
    echo "$SH_SEC_ANN" | evidence
    grep -q "^tofu-record-" <<< "$SH_CM_SECRET" \
      || fail "$SCEN" "the record Secret is not named tofu-record-<hash>: $SH_CM_SECRET"
    grep -q "\"tofu-estate\":\"$SH_ESTATE\"" <<< "$SH_SEC_LABELS" \
      || fail "$SCEN" "the record Secret for a kubernetes_manifest address carries no tofu-estate label naming this estate, so live/kubernetes/estate-boundary.yaml does not fence it: $SH_SEC_LABELS"
    grep -q '"tofu-address":"kubernetes_manifest.cm"' <<< "$SH_SEC_ANN" \
      || fail "$SCEN" "the record Secret carries no tofu-address annotation naming the address it records: $SH_SEC_ANN"
    grep -q "\"choudoufu.intentius.io/record-key\":\"$SH_RECORD_PATH" <<< "$SH_SEC_ANN" \
      || fail "$SCEN" "the record Secret's record-key annotation does not hold a key under $SH_RECORD_PATH, so the Secret's name is the only thing saying which record it is: $SH_SEC_ANN"
    SH_RV_BEFORE="$(sh_secret_version)"
    kc get secret "$SH_CM_SECRET" -n "$SH_RECORDS_NS" -o json > "$SH_WORK/$store_kind/stale.json"
  fi
  if [ "$store_kind" = "s3" ]; then
    SH_CM_KEY="$(sh_cm_record_key)"
    [ -n "$SH_CM_KEY" ] \
      || fail "$SCEN" "no object under $SH_RECORD_PATH carries a tofu-address tag naming kubernetes_manifest.cm, so this estate's Kubernetes record is either not in the bucket or not tagged: $(awsl s3api list-objects-v2 --bucket "$SH_BUCKET" --prefix tofu- --query 'Contents[].Key' --output text)"
    cmd "aws s3api get-object-tagging --bucket $SH_BUCKET --key <the cm record>"
    echo "$SH_CM_KEY" | evidence
    awsl s3api get-object-tagging --bucket "$SH_BUCKET" --key "$SH_CM_KEY" --query 'TagSet[].[Key,Value]' --output text | evidence
    SH_ESTATE_TAG="$(awsl s3api get-object-tagging --bucket "$SH_BUCKET" --key "$SH_CM_KEY" --query 'TagSet[?Key==`tofu-estate`].Value | [0]' --output text)"
    [ "$SH_ESTATE_TAG" = "$SH_ESTATE" ] \
      || fail "$SCEN" "the record object for a kubernetes_manifest address carries tofu-estate = '$SH_ESTATE_TAG', want '$SH_ESTATE'"
    # The proxy logs the request line as the client sent it, query string
    # and all ("...?x-id=PutObject"), so the status is not the third word of
    # a fixed-width line; match the key, then whatever the SDK appended.
    SH_PUT_RE="^PUT /$SH_CM_KEY(\\?[^ ]*)? "
    SH_CREATES="$(grep -cE "${SH_PUT_RE}20[0-9] if-none-match: " "$SH_WORK/proxy.log" || true)"
    [ "$SH_CREATES" -ge 1 ] \
      || fail "$SCEN" "directory A's write of the Kubernetes record was not a conditional create: $(grep -E "$SH_PUT_RE" "$SH_WORK/proxy.log" || echo '<no PUT to that key at all>')"
  fi

  cmd "choudoufu init && choudoufu plan   # directory B, the IDENTICAL configuration"
  shared_config "$B" yes
  sh_run "$B" init -input=false -no-color >/dev/null 2>&1 \
    || fail "$SCEN" "init failed in directory B"
  [ ! -e "$B/.terraform/choudoufu-cache.tfstate" ] \
    || fail "$SCEN" "directory B has a state cache before it has planned, so it is not the fresh directory this step claims"
  SH_PLAN_B0="$(sh_run "$B" plan -input=false -no-color 2>&1)" \
    || fail "$SCEN" "directory B's first plan failed: $(tail -20 <<< "$SH_PLAN_B0")"
  { grep -E '^No changes|^Plan:' <<< "$SH_PLAN_B0" | head -1 || true; } | evidence
  grep -q 'No changes.' <<< "$SH_PLAN_B0" \
    || fail "$SCEN" "directory B proposes a change for the configuration A just applied, so anything it proposes after the edit below would be about B being a different directory rather than about the edit: $(grep -E '^Plan:|will be' <<< "$SH_PLAN_B0" | head -3)"

  cmd "delete squad from B's configuration && choudoufu plan"
  shared_config "$B" no
  SH_PLAN_B="$(sh_run "$B" plan -input=false -no-color 2>&1)" \
    || fail "$SCEN" "directory B's plan after deleting the label failed: $(tail -20 <<< "$SH_PLAN_B")"
  { grep -E 'squad|^Plan:|^No changes' <<< "$SH_PLAN_B" | head -3 || true; } | evidence

  if [ "$store_kind" = "local" ]; then
    [ -d "$A/.tofu-records" ] \
      || fail "$SCEN" "directory A wrote no local record directory, so this control would pass with nothing recorded anywhere and prove nothing about where B reads from"
    echo "A's records: $(find "$A/.tofu-records" -type f | wc -l | tr -d ' ') file(s) under $A/.tofu-records; B's: $(find "$B/.tofu-records" -type f 2>/dev/null | wc -l | tr -d ' ')" | evidence
    grep -q 'No changes.' <<< "$SH_PLAN_B" \
      || fail "$SCEN" "on the local store directory B proposed the removal anyway, so steps 8 and 9 are not evidence that B read a shared record: $(grep -E '^Plan:|squad' <<< "$SH_PLAN_B" | head -3)"
    grep -q '"squad":"blue"' <<< "$(kc get configmap shared-config -n "$SH_NS" -o jsonpath='{.metadata.labels}')" \
      || fail "$SCEN" "the label B was supposed to leave alone is gone from the object"
    sh_run "$A" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 || true
    proof "caught. With A's record in a file only A can see, the second directory deletes the same label from the same configuration and plans \"No changes.\" - the removal is silently not proposed. That is what steps 8 and 9 measure a shared store fixing, and it is what every Kubernetes estate on the implied store gives its second operator today."
    return 0
  fi

  grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$SH_PLAN_B" \
    || fail "$SCEN" "the second directory did not propose the removal over the shared record store - this is #1394: $(grep -E '^Plan:|No changes' <<< "$SH_PLAN_B" | head -2)"
  grep -qE '^ +- squad +=.*"blue"' <<< "$SH_PLAN_B" \
    || fail "$SCEN" "directory B plans something, but it is not the deleted label: $(grep -E 'will be|squad' <<< "$SH_PLAN_B" | head -5)"

  cmd "choudoufu apply -auto-approve   # from B, then read the object back"
  SH_APPLY_B="$(sh_run "$B" apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "$SCEN" "directory B's apply failed: $(tail -20 <<< "$SH_APPLY_B")"
  { grep -E 'Apply complete!' <<< "$SH_APPLY_B" || true; } | evidence
  SH_AFTER="$(kc get configmap shared-config -n "$SH_NS" -o jsonpath='{.metadata.labels}')"
  echo "$SH_AFTER" | evidence
  grep -q '"squad"' <<< "$SH_AFTER" \
    && fail "$SCEN" "B's apply reported success and the deleted label is still on the object: $SH_AFTER"
  grep -q '"tier":"one"' <<< "$SH_AFTER" \
    || fail "$SCEN" "the removal took the still-declared label with it: $SH_AFTER"
  grep -q "\"tofu-estate\":\"$SH_ESTATE\"" <<< "$SH_AFTER" \
    || fail "$SCEN" "the removal took the estate's own marker with it: $SH_AFTER"

  # Whether the write that landed was CONDITIONAL. An apply that succeeded
  # looks the same either way, and a store that overwrote whatever it found
  # would pass every assertion above.
  if [ "$store_kind" = "kubernetes" ]; then
    cmd "kubectl get secret <the cm record> -o jsonpath='{.metadata.resourceVersion}'   # before and after B's apply"
    SH_RV_AFTER="$(sh_secret_version)"
    echo "resourceVersion before B's apply: $SH_RV_BEFORE   after: $SH_RV_AFTER" | evidence
    [ -n "$SH_RV_BEFORE" ] && [ -n "$SH_RV_AFTER" ] && [ "$SH_RV_BEFORE" != "$SH_RV_AFTER" ] \
      || fail "$SCEN" "the record Secret's resourceVersion did not move across B's apply ($SH_RV_BEFORE -> $SH_RV_AFTER), so B did not write the record it read and the removal above came from somewhere else"
    cmd "kubectl replace -f <the record Secret AS IT WAS BEFORE>   # a write carrying the stale version"
    explain \
      "resourceVersion is what this store conditions a write on, which is" \
      "the API server's own optimistic concurrency and not something the" \
      "store implements (claim 39 step 1 runs the Store suite's" \
      "stale-version case against this same cluster). The copy taken" \
      "before B's apply still carries the old version, so replacing it now" \
      "is exactly the write B would have made had it not re-read - and the" \
      "server has to refuse it."
    if SH_STALE="$(kc replace -f "$SH_WORK/$store_kind/stale.json" 2>&1)"; then
      fail "$SCEN" "a write carrying the record Secret's PREVIOUS resourceVersion was accepted, so a version is not what a write to this store is conditional on and B could have clobbered a concurrent writer: $SH_STALE"
    fi
    { head -2 <<< "$SH_STALE" || true; } | evidence
    grep -qiE 'conflict|has been modified' <<< "$SH_STALE" \
      || fail "$SCEN" "the stale write failed for some reason other than a version conflict, so this measured nothing about the conditional: $SH_STALE"
  else
    cmd "grep PUT <the cm record> proxy.log   # what the write that landed carried"
    SH_UPDATE="$(grep -E "${SH_PUT_RE}20[0-9] if-match: " "$SH_WORK/proxy.log" | tail -1 || true)"
    { grep -E "$SH_PUT_RE" "$SH_WORK/proxy.log" || true; } | evidence
    [ -n "$SH_UPDATE" ] \
      || fail "$SCEN" "B's write of the Kubernetes record was not a conditional update that succeeded; a store that overwrote unconditionally would look the same to the apply: $(grep -E "$SH_PUT_RE" "$SH_WORK/proxy.log" || echo '<no PUT to that key at all>')"
  fi

  cmd "choudoufu plan   # from B, twice: the estate has to SETTLE"
  SH_REPLAN1="$(sh_run "$B" plan -input=false -no-color 2>&1)" \
    || fail "$SCEN" "B's first replan failed: $(tail -20 <<< "$SH_REPLAN1")"
  SH_REPLAN2="$(sh_run "$B" plan -input=false -no-color 2>&1)" \
    || fail "$SCEN" "B's second replan failed: $(tail -20 <<< "$SH_REPLAN2")"
  {
    echo "replan 1:   $(grep -E '^No changes|^Plan:' <<< "$SH_REPLAN1" | head -1)"
    echo "replan 2:   $(grep -E '^No changes|^Plan:' <<< "$SH_REPLAN2" | head -1)"
  } | evidence
  grep -q 'No changes.' <<< "$SH_REPLAN1" \
    || fail "$SCEN" "B's replan after the removal is not empty: $(grep -E '^Plan:|will be' <<< "$SH_REPLAN1" | head -3)"
  grep -q 'No changes.' <<< "$SH_REPLAN2" \
    || fail "$SCEN" "B's SECOND replan is not empty: $(grep -E '^Plan:|will be' <<< "$SH_REPLAN2" | head -3)"
  SH_REPLAN_A="$(sh_run "$A" plan -input=false -no-color 2>&1)" \
    || fail "$SCEN" "A's replan after B's removal failed: $(tail -20 <<< "$SH_REPLAN_A")"
  { grep -E '^No changes|^Plan:' <<< "$SH_REPLAN_A" | head -1 || true; } | evidence
  grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$SH_REPLAN_A" \
    || fail "$SCEN" "directory A, which still declares squad, does not propose putting it back after B removed it; the two directories are not sharing one estate: $(grep -E '^Plan:|No changes' <<< "$SH_REPLAN_A" | head -2)"

  if [ "$store_kind" = "s3" ]; then
    sh_1344_case
  fi
  sh_run "$B" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 || true
  kc delete namespace "$SH_NS" --wait=false >/dev/null 2>&1 || true
  [ "$store_kind" = "kubernetes" ] && { kc delete namespace "$SH_RECORDS_NS" --wait=false >/dev/null 2>&1 || true; }
  if [ "$store_kind" = "kubernetes" ]; then
    proof "one estate, one cluster, two directories and no bucket: the directory that never applied anything proposed the removal the first one's record made possible, applied it, and settled on two replans, while the directory that still declares the label proposed putting it back. The record is a Secret in $SH_RECORDS_NS, the namespace the estate name derives, carrying this estate's tofu-estate label, the address's own tofu-address annotation and the record key; its resourceVersion moved across B's write, and a write carrying the version from before it is refused."
  else
    proof "one estate, one bucket, two directories: the same removal again with the records in a bucket instead of the cluster. The record object for a kubernetes_manifest address carries tofu-estate and tofu-address, its create was an If-None-Match and the update that landed was an If-Match, all read off the wire."
  fi
}

# sh_1344_case manufactures #1344's shape for a Kubernetes address: an
# update whose If-Match meets a key that is no longer there. The proxy holds
# B's record PUT, the object is deleted underneath it, and the PUT is then
# released. Real S3 answers such a PutObject 404 rather than 412 (measured
# on #1344); either way it is the same conflict from the caller's side and
# the run must be refused rather than silently overwrite. The status the
# emulator actually answered is printed, so the difference from real S3
# stays measured instead of assumed.
sh_1344_case() {
  step "9b. the update whose record is GONE (#1344), for a Kubernetes address"
  explain \
    "A conditional update carries the version it read. If the object has" \
    "been deleted in between there is no version to match, and the store" \
    "has to report that as the conflict it is rather than create the" \
    "record afresh over whatever else happened. The proxy holds B's write" \
    "of the record, the object is deleted while the write waits, and the" \
    "write is then let through."
  rm -f "$SH_WORK/held" "$SH_WORK/release"
  : > "$SH_WORK/proxy.log"
  echo "$SH_CM_KEY" > "$SH_WORK/hold"
  SH_LIVE_BODY="    record_store \"s3\" {
      bucket = \"$SH_BUCKET\"
    }

    retry {
      max_attempts = 1
    }"
  shared_config "$SH_WORK/s3/b" yes
  # `&& rc=0 || rc=$?`, never `; echo $?`: this scenario runs under set -e
  # and the apply under test is expected to fail, which would end the
  # subshell before the exit code was written.
  rm -f "$SH_WORK/b1344.rc"
  ( { sh_run "$SH_WORK/s3/b" apply -auto-approve -input=false -no-color > "$SH_WORK/b1344.out" 2>&1 && rc=0 || rc=$?; echo "$rc" > "$SH_WORK/b1344.rc"; } ) &
  local pid=$!
  # The proxy releases a held PUT only after every PUT listed before it has
  # been judged, so listing more turns than the writer can take is free and
  # a retry of the same write cannot wait for a turn that never comes.
  for _ in $(seq 1 1200); do [ -s "$SH_WORK/held" ] && break; sleep 0.05; done
  if [ ! -s "$SH_WORK/held" ]; then
    kill $pid 2>/dev/null || true
    rm -f "$SH_WORK/hold"
    fail "$SCEN" "B's apply never wrote the record for kubernetes_manifest.cm within 60s, so nothing was held and this case was not manufactured: $(tail -5 "$SH_WORK/b1344.out" 2>/dev/null)"
  fi
  cmd "aws s3api delete-object --key <the cm record>   # while B's conditional write waits"
  awsl s3api delete-object --bucket "$SH_BUCKET" --key "$SH_CM_KEY" >/dev/null \
    || { kill $pid 2>/dev/null || true; rm -f "$SH_WORK/hold"; fail "$SCEN" "could not delete the record object underneath the held write"; }
  echo "1 2 3 4 5 6 7 8" > "$SH_WORK/release"
  for _ in $(seq 1 1200); do [ -s "$SH_WORK/b1344.rc" ] && break; sleep 0.05; done
  rm -f "$SH_WORK/hold" "$SH_WORK/release"
  if [ ! -s "$SH_WORK/b1344.rc" ]; then
    kill $pid 2>/dev/null || true
    fail "$SCEN" "B was still running 60s after its held write was released"
  fi
  wait $pid 2>/dev/null || true
  { grep -E "$SH_PUT_RE" "$SH_WORK/proxy.log" || true; } | evidence
  { grep -iE 'Record store write conflict|expected version' "$SH_WORK/b1344.out" | head -2 || true; } | evidence
  [ "$(cat "$SH_WORK/b1344.rc")" != "0" ] \
    || fail "$SCEN" "the apply whose record was deleted under its conditional write reported success, so the update was not conditional on anything: $(tail -5 "$SH_WORK/b1344.out")"
  grep -qi 'Record store write conflict' "$SH_WORK/b1344.out" \
    || fail "$SCEN" "the apply failed, but not on a named record store write conflict, so what an update over a deleted record does is still unstated: $(tail -20 "$SH_WORK/b1344.out")"
  proof "an If-Match update whose object had been deleted underneath it was refused and named as a write conflict, for a kubernetes_manifest address, answered $(grep -E "$SH_PUT_RE" "$SH_WORK/proxy.log" | tail -1 | awk '{print $3}') by the emulator (real S3 answers 404 here, #1344)."
}

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
    "You asked for proof the assertions can fail. Steps 3 and 7 below" \
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
  # The control for steps 8 and 9. It is a different assertion from the one
  # above - that the removal a second directory proposes comes from the
  # SHARED record and from nowhere else - so it gets its own arm rather
  # than riding on this one.
  shared_store_step local
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

step "6. a label DELETED from the configuration is removed - and the estate then settles"
explain \
  "The last thing a state file was doing for this type. \"Removed from" \
  "the configuration\" is not visible in the configuration - the key is" \
  "gone from it - and it is not visible on the object either, which" \
  "holds the label and no memory of who asked for it. The estate's own" \
  "record carries the keys each apply declared, and the removal set is" \
  "(recorded) minus (currently declared)." \
  "" \
  "The shape matters. Only the LABELS map is edited here; the" \
  "annotations map is left exactly as it was, which is when the" \
  "provider's computed_fields rule resends it wholesale and the API" \
  "server records this estate as the writer of the annotation kubectl" \
  "wrote in step 5. A removal rule sourced from managedFields passes the" \
  "plan-and-apply half of this step and then churns for ever on the" \
  "replans below. That is why both halves are here."
cmd "add a second declared label squad=blue && choudoufu apply -auto-approve"
# A second declared label, so the removal below takes one of two rather
# than emptying the map - the map-emptying shape is the annotation half,
# further down.
sed_i "$SMOKE_WORK/live/main.tf" 's/"tier" = "two"/"tier" = "two"\n        "squad" = "blue"/'
( cd "$SMOKE_WORK/live" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "$SCEN" "could not add the second declared label"
LABELS6="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels}')"
echo "$LABELS6" | evidence
grep -q '"squad":"blue"' <<< "$LABELS6" \
  || fail "$SCEN" "the second declared label is not on the object, so there is nothing to remove: $LABELS6"
grep -q '"owner":"payments-team"' <<< "$LABELS6" \
  || fail "$SCEN" "step 5's undeclared label is gone, so this step cannot show a removal sparing it: $LABELS6"

cmd "delete squad from the configuration && choudoufu plan"
sed_i "$SMOKE_WORK/live/main.tf" '/"squad" = "blue"/d'
PLAN6="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the plan after deleting the label failed: $(tail -20 <<< "$PLAN6")"
{ grep -E 'squad|^Plan:|^No changes' <<< "$PLAN6" | head -3 || true; } | evidence
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$PLAN6" \
  || fail "$SCEN" "deleting a label from the configuration did not plan - this is #1211: $(grep -E '^Plan:|No changes' <<< "$PLAN6" | head -2)"
grep -qE '^ +- squad +=.*"blue"' <<< "$PLAN6" \
  || fail "$SCEN" "the plan changes something, but it is not the deleted label: $(grep -E 'will be|squad' <<< "$PLAN6" | head -5)"

cmd "choudoufu apply -auto-approve   # then read the object back"
APPLY6="$(cd "$SMOKE_WORK/live" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the apply after deleting the label failed: $(tail -20 <<< "$APPLY6")"
{ grep -E 'Apply complete!' <<< "$APPLY6" || true; } | evidence
AFTER6="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels}')"
echo "$AFTER6" | evidence
grep -q '"squad"' <<< "$AFTER6" \
  && fail "$SCEN" "the apply reported success and the deleted label is still on the object: $AFTER6"
grep -q '"owner":"payments-team"' <<< "$AFTER6" \
  || fail "$SCEN" "the removal took a key another field manager wrote with it: $AFTER6"
grep -q '"tier":"two"' <<< "$AFTER6" \
  || fail "$SCEN" "the removal took a still-declared label with it: $AFTER6"

cmd "choudoufu plan   # twice - the estate has to SETTLE, not just move"
PLAN6B="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the first replan failed: $(tail -20 <<< "$PLAN6B")"
PLAN6C="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the second replan failed: $(tail -20 <<< "$PLAN6C")"
{
  echo "replan 1:   $(grep -E '^No changes|^Plan:' <<< "$PLAN6B" | head -1)"
  echo "replan 2:   $(grep -E '^No changes|^Plan:' <<< "$PLAN6C" | head -1)"
} | evidence
grep -q 'No changes.' <<< "$PLAN6B" \
  || fail "$SCEN" "the replan after the removal is not empty, so the removal rule churns: $(grep -E '^Plan:|will be|metadata.name|scraped|owner' <<< "$PLAN6B" | head -5)"
grep -q 'No changes.' <<< "$PLAN6C" \
  || fail "$SCEN" "the SECOND replan is not empty: a removal rule sourced from the object's own managedFields fails exactly here, one apply later than the first: $(grep -E '^Plan:|will be|metadata.name' <<< "$PLAN6C" | head -5)"
NSLABELS6="$(kc get namespace "$NS" -o jsonpath='{.metadata.labels}')"
echo "$NSLABELS6" | evidence
grep -q '"kubernetes.io/metadata.name"' <<< "$NSLABELS6" \
  || fail "$SCEN" "the API server's own namespace label is gone, so the two empty replans above were measured against the wrong world"

cmd "delete the LAST annotation - the map goes with it - && choudoufu plan"
explain \
  "One annotation, then none, is the commonest shape there is, and it" \
  "is a different code path: deleting the last key deletes the" \
  "annotations map from the configuration too, so there is no prior map" \
  "left to carry the removed key."
live_config two
PLAN6D="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the plan after deleting the last annotation failed: $(tail -20 <<< "$PLAN6D")"
{ grep -E 'reviewed|^Plan:|^No changes' <<< "$PLAN6D" | head -3 || true; } | evidence
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$PLAN6D" \
  || fail "$SCEN" "deleting the only annotation did not plan: $(grep -E '^Plan:|No changes' <<< "$PLAN6D" | head -2)"
( cd "$SMOKE_WORK/live" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "$SCEN" "the apply after deleting the last annotation failed"
ANN6="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.annotations}')"
echo "${ANN6:-<nothing>}" | evidence
grep -q '"reviewed"' <<< "$ANN6" \
  && fail "$SCEN" "the declared annotation is still on the object after the apply: $ANN6"
grep -q '"scraped":"true"' <<< "$ANN6" \
  || fail "$SCEN" "the removal took the annotation kubectl wrote with it: ${ANN6:-<nothing>}"
PLAN6E="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the replan after the annotation removal failed: $(tail -20 <<< "$PLAN6E")"
{ grep -E '^No changes|^Plan:' <<< "$PLAN6E" | head -1 || true; } | evidence
grep -q 'No changes.' <<< "$PLAN6E" \
  || fail "$SCEN" "the replan after emptying the annotations map is not empty: $(grep -E '^Plan:|will be' <<< "$PLAN6E" | head -3)"
proof "a label and an annotation deleted from the configuration are each proposed for removal and written, the keys kubectl wrote and the API server's own are left exactly where they were, and the plan is empty afterwards - twice, against the world in which the provider has already resent the untouched map and the server has recorded this estate as its writer."

step "7. the one difference from stock, measured on both runners"
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
STOCK_PLAN7="$(cd "$SMOKE_WORK/stock" && terraform plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "stock's plan after the out-of-band change failed: $(tail -10 <<< "$STOCK_PLAN7")"
PLAN7="$(cd "$SMOKE_WORK/live" && chdf plan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the plan after the out-of-band change failed: $(tail -20 <<< "$PLAN7")"
{
  echo "stock:      $(grep -E '^No changes|^Plan:' <<< "$STOCK_PLAN7" | head -1)"
  echo "choudoufu:  $(grep -E '^No changes|^Plan:' <<< "$PLAN7" | head -1)"
} | evidence
grep -q 'No changes.' <<< "$STOCK_PLAN7" \
  || fail "$SCEN" "stock proposed something for the out-of-band change, so the difference this step records is not the difference it says it is: $(grep -E '^Plan:|No changes' <<< "$STOCK_PLAN7" | head -2)"
grep -qE '^Plan: 0 to add, 1 to change, 0 to destroy\.' <<< "$PLAN7" \
  || fail "$SCEN" "choudoufu did not propose restoring the declared label: $(grep -E '^Plan:|No changes' <<< "$PLAN7" | head -2)"
grep -qE 'tier +=.*"zzz".*->.*"two"' <<< "$PLAN7" \
  || fail "$SCEN" "choudoufu plans something, but not the label restore: $(grep -E 'will be|tier' <<< "$PLAN7" | head -5)"
proof "stock says \"No changes.\" and choudoufu proposes restoring the declared label. That difference is the price of having no last-applied value and it is recorded in live/LIMITATIONS.md, not hidden: a saved plan's staleness check can see an out-of-band kubectl label here, and stock's cannot."

# Steps 8 and 9 run after step 7 because step 7 leaves its object drifted
# on purpose. Each uses its own estate, namespace and objects on the same
# cluster, so nothing above them is disturbed and nothing above them is what
# they measure. The cluster store comes first: it needs nothing the claim
# does not already have.
shared_store_step kubernetes
shared_store_step s3

( cd "$SMOKE_WORK/live" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
( cd "$SMOKE_WORK/stock" && terraform destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
kc delete namespace "$STOCK_NS" --wait=false >/dev/null 2>&1 || true

echo "  What you watched: a label edited in the configuration proposed as one"
echo "  in-place update and written to the object, the same answer stock gave"
echo "  for the same edit with a state file behind it; an annotation added and"
echo "  changed doing the same; a label and an annotation DELETED from the"
echo "  configuration proposed for removal, written, and then quiet on two"
echo "  successive replans; and the API server's own label, a controller's"
echo "  label and a controller's annotation left alone by every plan. The"
echo "  prior this fork rebuilds on each run carries the server's value for"
echo "  the keys the configuration names plus the keys its own record says"
echo "  it used to name, and nothing else - which is what a state file's"
echo "  last-applied manifest says, except for a declared key someone moved"
echo "  by hand. Then the same removal twice more from a SECOND working"
echo "  directory, over the two stores two directories can share, which is"
echo "  the only way the removal is true for anyone but the operator who"
echo "  applied it: as a Secret in the cluster, carrying the estate label,"
echo "  the address annotation and the record key, whose resourceVersion"
echo "  moved across that directory's write and refuses a write still"
echo "  carrying the version from before it; and as an object in a bucket,"
echo "  carrying tofu-estate and tofu-address, created with an If-None-Match"
echo "  and updated with an If-Match read off the wire, refused by name when"
echo "  the object it named had been deleted underneath it."
