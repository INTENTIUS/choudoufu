# k8s-no-secret-survives-in-what-the-tool-keeps
# CLAIM 40 (kubernetes) - No secret survives unless you let it. ~3 min.
#
# This proof: under strict { secrets = "refuse" } on a Kubernetes estate, a
# random_password is refused by name with nothing written, and a
# kubernetes_secret_v1 whose data comes from the environment applies, after
# which no file the run kept holds the value and no state cache exists;
# the replan is empty, because the API server gives the data back.
#
# First run 2026-10-04 failed step 4: the unchanged replan proposed
# + wait_for_service_account_token on the Secret, because under refuse
# projection.residueCandidates recorded nothing at all for a type holding a
# sensitive attribute. Since #1873 refuse drops each sensitive argument and
# records the rest, and the second run, both arms, was green on kind
# (Kubernetes v1.37.0): 6 kept files read, none holding the value, and the
# store arm's scan found it in the state cache.
#
# Where this differs from the AWS proof, on purpose. RDS never returns a
# master password, so under refuse the AWS replan proposes the password
# again on every plan. A Secret's data is returned by the API server, so
# the prior is read from the cluster and the replan here must be empty.
# The value stays in the cluster, which is where the configuration put it;
# the claim is about what the tool keeps.
#
# BREAK=1 applies the identical estate under secrets = "store" and runs the
# identical scan, which must find the value (in the state cache, which store
# writes). Without it, zero hits under refuse would read the same from a
# scan that could not find the value anywhere.

W="$SMOKE_WORKROOT/k8s-secrets"
mkdir -p "$W"
SMOKE_WORK="$W"; export SMOKE_WORK
SECRET="smoke-K8s-Pl41ntext-$RANDOM$RANDOM"
export TF_VAR_app_password="$SECRET"

# scan_kept and files_kept are the AWS proof's: every file under <dir> the
# run wrote on its own, minus the provider plugins init downloaded before
# the value reached any run.
scan_kept() {
  local dir="$1"
  find "$dir" -type f -not -path '*/.terraform/providers/*' -print0 \
    | xargs -0 grep -l -- "$SECRET" 2>/dev/null | sed "s|^$dir/||" || true
}
files_kept() {
  find "$1" -type f -not -path '*/.terraform/providers/*' | wc -l | tr -d ' '
}

# write_secret_estate <dir> <estate> <namespace> <strict-lines>
write_secret_estate() {
  local dir="$1" estate="$2" ns="$3" strict="$4"
  mkdir -p "$dir"
  {
    echo 'terraform {'
    echo '  live {'
    echo "    estate = \"$estate\""
    [ -z "$strict" ] || printf '%s\n' "$strict"
    echo '  }'
    cat <<'TFEOF'
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}

variable "app_password" {
  type      = string
  sensitive = true
}

resource "kubernetes_namespace" "app" {
  metadata {
    name = "NAMESPACE"
  }
}

resource "kubernetes_secret_v1" "app" {
  metadata {
    name      = "app-credentials"
    namespace = "NAMESPACE"
  }
  data = {
    password = var.app_password
  }
  depends_on = [kubernetes_namespace.app]
}
TFEOF
  } > "$dir/main.tf"
  sed_i "$dir/main.tf" "s/NAMESPACE/$ns/g"
}

REFUSE_STRICT='    strict {
      secrets = "refuse"
    }'
STORE_STRICT='    strict {
      secrets = "store"
    }'

cluster_up

step "the claim"
explain \
  "No secrets stored by the tool. Under strict { secrets = \"refuse\" }" \
  "a type that generates a secret is refused by name, and the one" \
  "artifact allowed to hold attribute material, the disposable state" \
  "cache, is not written. This applies a Secret under that setting and" \
  "greps everything the run kept for the value, by value."

step "1. a secret-generating type is refused by name"
explain \
  "A random_password's whole value exists only in whatever the tool" \
  "keeps. Under refuse the plan must stop before anything is created," \
  "naming the resource and the setting, and write nothing."
GEN="$W/generated"; mkdir -p "$GEN"
cat > "$GEN/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-k8s-secrets-gen"
    strict {
      secrets = "refuse"
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

resource "random_password" "app" {
  length = 16
}

resource "kubernetes_secret_v1" "generated" {
  metadata {
    name      = "generated"
    namespace = "default"
  }
  data = {
    password = random_password.app.result
  }
}
TFEOF
cmd "choudoufu init && choudoufu plan   # strict { secrets = \"refuse\" }"
logged k8s-secrets-gen-init "k8s-secrets" "init of the generating estate failed" -- in_dir "$GEN" chdf init -input=false -no-color
GCODE=0
GOUT="$(cd "$GEN" && chdf plan -input=false -no-color 2>&1)" || GCODE=$?
GFLAT="$(tr '\n' ' ' <<< "$GOUT" | tr -s ' ')"
[ "$GCODE" != "0" ] || fail "k8s-secrets" "the plan went through with a random_password under secrets = \"refuse\": $GOUT"
grep -qF -- 'random_password.app: "random_password" is a logical resource, classified SECRET_REFUSED' <<< "$GFLAT" \
  || fail "k8s-secrets" "the random_password is not refused as SECRET_REFUSED by name: $GOUT"
grep -qF -- 'strict { secrets = "refuse" }' <<< "$GFLAT" \
  || fail "k8s-secrets" "the refusal does not name the setting: $GOUT"
grep -E '^random_password\.app: ' <<< "$GOUT" | cut -c1-110 | evidence
echo "exit status: $GCODE" | evidence
[ ! -e "$GEN/.terraform/choudoufu-cache.tfstate" ] || fail "k8s-secrets" "a refused plan wrote the state cache"
if kc get secret generated -n default >/dev/null 2>&1; then
  fail "k8s-secrets" "the refused plan created the Secret anyway"
fi
proof "refused by name, the setting named, exit $GCODE, nothing written and nothing created."

step "2. a Secret whose data comes from the environment, under refuse"
explain \
  "The value reaches the configuration through TF_VAR_app_password alone" \
  "and is spelled nowhere on disk. One plain apply, so what it leaves" \
  "behind is what the tool keeps on its own."
REFUSE="$W/refuse"
write_secret_estate "$REFUSE" smoke-k8s-secrets-refuse smoke-k8s-secrets-refuse "$REFUSE_STRICT"
cmd "choudoufu init && choudoufu apply -auto-approve   # TF_VAR_app_password set"
logged k8s-secrets-refuse-init "k8s-secrets" "init of the refuse estate failed" -- in_dir "$REFUSE" chdf init -input=false -no-color
RAPPLY="$(cd "$REFUSE" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-secrets" "the apply under refuse failed: $RAPPLY"
grep -E 'Apply complete!' <<< "$RAPPLY" | evidence
grep -q 'Resources: 2 added' <<< "$RAPPLY" || fail "k8s-secrets" "the apply did not add the namespace and the Secret: $RAPPLY"
grep -qF -- "$SECRET" <<< "$RAPPLY" && fail "k8s-secrets" "the apply printed the value"
IN_CLUSTER="$(kc get secret app-credentials -n smoke-k8s-secrets-refuse -o jsonpath='{.data.password}' | base64 -d)"
[ "$IN_CLUSTER" = "$SECRET" ] || fail "k8s-secrets" "the Secret in the cluster does not hold the value, so the scan below would be looking for something never set"
proof "the Secret holds the value in the cluster, where the configuration put it, and the run never printed it."

step "3. grep everything the run kept for the value"
cmd "grep -rl \"\$TF_VAR_app_password\" .   # minus .terraform/providers"
[ ! -e "$REFUSE/.terraform/choudoufu-cache.tfstate" ] || fail "k8s-secrets" "the state cache was written under refuse"
HITS="$(scan_kept "$REFUSE")"
NKEPT="$(files_kept "$REFUSE")"
echo "files read: $NKEPT; no cache file exists" | evidence
echo "files holding the value: ${HITS:-none}" | evidence
[ "$NKEPT" -gt 1 ] || fail "k8s-secrets" "the scan read $NKEPT file(s), so a zero below would be a grep over nothing"
[ -z "$HITS" ] || fail "k8s-secrets" "the value is in what the run kept under refuse:
$HITS"
proof "$NKEPT files read, none holds the value, and no cache file exists to hold it."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the same scan over a run that keeps the value"
  explain \
    "Zero hits proves nothing if the scan could not find the value in a" \
    "file that has it. The identical estate under secrets = \"store\"," \
    "applied the same way; the identical scan MUST find the value."
  BSTORE="$W/store-break"
  write_secret_estate "$BSTORE" smoke-k8s-secrets-store smoke-k8s-secrets-store "$STORE_STRICT"
  cmd "choudoufu apply -auto-approve   # strict { secrets = \"store\" } ; then the same scan"
  logged k8s-secrets-bstore-init "k8s-secrets" "BREAK: init of the store estate failed" -- in_dir "$BSTORE" chdf init -input=false -no-color
  BAPPLY="$(cd "$BSTORE" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "k8s-secrets" "BREAK: the apply under store failed: $BAPPLY"
  BHITS="$(scan_kept "$BSTORE")"
  echo "files holding the value: $(tr '\n' ' ' <<< "${BHITS:-none}")" | evidence
  [ -n "$BHITS" ] || fail "k8s-secrets" "BREAK: the store run kept the value nowhere the scan can see, so the zero in step 3 was never evidence of anything"
  proof "caught: the same scan finds the value in $(grep -c . <<< "$BHITS") file(s) under secrets = \"store\", so the zero under refuse was a real zero."
  exit 0
fi

step "4. the replan under refuse is empty"
explain \
  "Nothing the run keeps remembers the value, and the plan does not need" \
  "it to: the prior is read from the cluster, which returns the Secret's" \
  "data. So the replan is empty. A rotated value in the environment must" \
  "still be proposed, or a rotation would never be sent (#1503's shape)."
cmd "choudoufu plan   # unchanged, then with TF_VAR_app_password rotated"
RPLAN="$(cd "$REFUSE" && chdf plan -input=false -no-color 2>&1)" || fail "k8s-secrets" "the replan under refuse failed: $RPLAN"
grep -E '^Plan:|No changes' <<< "$RPLAN" | head -1 | evidence
grep -q 'No changes.' <<< "$RPLAN" || fail "k8s-secrets" "the replan under refuse is not empty: $RPLAN"
ROTATED="$SECRET-rotated"
RROT="$(cd "$REFUSE" && TF_VAR_app_password="$ROTATED" chdf plan -input=false -no-color 2>&1)" \
  || fail "k8s-secrets" "the rotated replan failed: $RROT"
grep -E '^Plan:|No changes' <<< "$RROT" | head -1 | sed 's/^/rotated: /' | evidence
grep -q 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$RROT" \
  || fail "k8s-secrets" "a rotated value is not proposed under refuse: $RROT"
grep -q 'kubernetes_secret_v1.app will be updated in-place' <<< "$RROT" \
  || fail "k8s-secrets" "the change proposed is not the Secret's: $RROT"
grep -qF -- "$ROTATED" <<< "$RROT" && fail "k8s-secrets" "the rotated plan printed the new value"
[ -z "$(scan_kept "$REFUSE")" ] || fail "k8s-secrets" "a replan left the value in what the tool keeps"
proof "the unchanged replan is empty and the rotated one proposes the Secret; neither printed the value or left it on disk."

step "5. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$REFUSE" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "k8s-secrets" "teardown failed: $DOUT"
destroyed_exactly "k8s-secrets" 2 "$DOUT"
proof "the namespace and the Secret are gone."

echo "  What you watched: a random_password refused by name under"
echo "  strict { secrets = \"refuse\" } on a Kubernetes estate, and a Secret's"
echo "  data set under the same setting and then searched for, by value, in"
echo "  every file the run kept, and found in none."
