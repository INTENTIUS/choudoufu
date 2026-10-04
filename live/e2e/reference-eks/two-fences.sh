#!/usr/bin/env bash
# Two fences, one estate (#1113, build-plan step 6) - the scenario behind
# live/kubernetes/COMPATIBILITY.md's "Two fences, one estate" paragraph, on
# the floci-eks substrate. ~15 min.
#
# NOT REGISTERED AS A PROOF. Written under the maintainer's no-testing ruling
# for #1113 and never run. live/smoke/claims.json's guard makes any
# registered proof read `proven`, so this lives beside the estate it uses
# until it has run and its BREAK control has caught; claim 13's cells name it
# in their notes. Moving it to live/smoke/scenarios/ and registering it is
# the step after its first green run.
#
# What it shows. One estate, `app`, spans an EKS cluster's AWS leg and the
# cluster it creates. Two AWS roles, alice and bob, reach both legs through
# their own credential only:
#
#   - the AWS leg is fenced by IAM conditions on the ownership tags
#     (live/MARKERS.md's grant), judged by the emulator's IAM enforcement;
#   - the cluster leg is fenced by live/kubernetes/estate-boundary.yaml, a
#     ValidatingAdmissionPolicy judged per Kubernetes user, and each role
#     reaches the cluster as its own user through an EKS access entry -
#     which needs floci's principal identity (lex00/floci#220,
#     FLOCI_SERVICES_EKS_PRINCIPAL_IDENTITY=true). On an image without it,
#     every role is system:masters and step 3 fails loudly saying so.
#
# Then: each fence refuses the role it should and admits the one it should;
# a role granted the AWS leg but not the cluster leg is admitted on one and
# refused on the other (the fences do not agree for you); and a carve into a
# new estate is one live-mv -from-estate per leg, each judged by its own
# fence, run by choudoufu under the role's own credential, with the
# kubernetes provider's exec token minting the role's own cluster identity.
#
#   FLOCI_IMAGE=<an image with lex00/floci#220> bash live/e2e/reference-eks/two-fences.sh
#
# Needs Docker with a socket floci can reach, the AWS CLI, jq and python3.
#
# BREAK=1: the control. Bob's IAM grant loses its conditions and the
# admission policy is removed; Bob's writes on both legs must then LAND. If
# either is still refused, something other than the fence was refusing him
# and the main arm proves nothing.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
# shellcheck source=live/e2e/reference-eks/estate.sh
source "$ROOT/live/e2e/reference-eks/estate.sh"

gauntlet_plugin_cache

WORK="$(mktemp -d)"
REGION="us-east-1"
PREFIX="fences"
CLUSTER="${PREFIX}-eks"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
FLOCI_PORT="${FLOCI_PORT:-4751}"
FLOCI_NAME="choudoufu-two-fences-$$"
FLOCI_NS="fences$$"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
APP="$WORK/app"
DATA="$WORK/data"

cleanup() {
  local children
  children="$(docker ps -a --filter "name=floci-${FLOCI_NS}-" --format '{{.Names}}' 2>/dev/null)"
  # shellcheck disable=SC2086  # docker container names, never globs
  [ -n "$children" ] && docker rm -f $children >/dev/null 2>&1
  gauntlet_floci_teardown "$FLOCI_NAME"
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
step() { printf '\n=== %s ===\n' "$*"; }
proof() { printf '  PROOF: %s\n' "$*"; }

# The account: the emulator's static key bypasses IAM enforcement, the same
# arrangement live/smoke/scenarios/the-tag-is-the-boundary.sh uses.
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

k3s_of() { docker ps --filter "name=floci-${FLOCI_NS}-eks-" --format '{{.Names}}' | head -n1; }
# admin_kc: kubectl as the cluster's own admin, inside the k3s container.
admin_kc() {
  local c; c="$(k3s_of)"; [ -n "$c" ] || { echo "no k3s container" >&2; return 1; }
  docker exec -i "$c" kubectl "$@" 2>/dev/null || docker exec -i "$c" k3s kubectl "$@"
}

# as_role <role> <cmd...>: one command under the role's session, in a
# subshell. Everything inside - the AWS CLI, choudoufu, the kubernetes
# provider's exec plugin, kubectl's - signs with the role's credential.
as_role() {
  local role="$1" c; shift
  c="$(awsl sts assume-role --role-arn "arn:aws:iam::$ACCT:role/$role" --role-session-name "$role" \
        --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]' --output text)" || { echo "could not assume $role" >&2; return 1; }
  ( AWS_ACCESS_KEY_ID="$(cut -f1 <<< "$c")"; AWS_SECRET_ACCESS_KEY="$(cut -f2 <<< "$c")"; AWS_SESSION_TOKEN="$(cut -f3 <<< "$c")"
    export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
    "$@" )
}
# role_kc <role> <kubectl args...>: kubectl as the role, through the same
# exec credential an EKS root's provider block uses.
role_kc() {
  local role="$1"; shift
  as_role "$role" kubectl --kubeconfig "$WORK/kubeconfig" "$@"
}

iam_denied() { grep -qE 'UnauthorizedOperation|AccessDenied|not authorized to perform|StatusCode: 403' <<< "$1"; }
vap_denied() { grep -q "choudoufu-estate-boundary" <<< "$1"; }

# grant <act-on-estate> <create-into-estate>: live/MARKERS.md's grant shape,
# plus the reads a plan and a kubeconfig need.
grant() { cat <<EOF
{"Version":"2012-10-17","Statement":[
 {"Sid":"Read","Effect":"Allow","Action":["ec2:Describe*","eks:Describe*","eks:List*","tag:GetResources","sts:GetCallerIdentity","iam:Get*","iam:List*"],"Resource":"*"},
 {"Sid":"ActOnWhatTheEstateAlreadyOwns","Effect":"Allow","Action":["ec2:CreateTags","ec2:DeleteTags"],"Resource":"*",
  "Condition":{"StringEquals":{"aws:ResourceTag/tofu-estate":"$1"}}},
 {"Sid":"CreateIntoTheEstate","Effect":"Allow","Action":["ec2:CreateTags"],"Resource":"*",
  "Condition":{"StringEquals":{"aws:RequestTag/tofu-estate":"$2"}}}
]}
EOF
}
ungoverned() { echo '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["ec2:*","eks:*","tag:*","iam:Get*","iam:List*","sts:GetCallerIdentity"],"Resource":"*"}]}'; }

aws_provider_block() {
  cat <<EOF
provider "aws" {
  region                      = "$REGION"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
EOF
}

# The carve's two objects, one per leg, declared in app first and moved to
# data in step 7. Neither has a dependent, so app plans without them.
carve_blocks() {
  cat <<'HCL'

resource "aws_vpc" "carve" {
  cidr_block = "10.99.0.0/16"
  tags = {
    Name = "fences-carve"
  }
}

resource "kubernetes_config_map_v1" "carve" {
  metadata {
    name      = "carve"
    namespace = "default"
  }
  data = {
    leg = "cluster"
  }
}
HCL
}

# data's own root: the carve's two blocks, with the kubernetes provider
# configured from the cluster app owns, read through a data source.
data_root() {
  reference_eks_terraform_block data || return 1
  printf '\n%s\n' "$(aws_provider_block)"
  cat <<EOF

data "aws_eks_cluster" "app" {
  name = "$CLUSTER"
}

provider "kubernetes" {
  host                   = data.aws_eks_cluster.app.endpoint
  cluster_ca_certificate = base64decode(data.aws_eks_cluster.app.certificate_authority[0].data)

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", data.aws_eks_cluster.app.name, "--region", "$REGION"]
  }
}
EOF
  carve_blocks
}

# ── 0. tools, emulator ──────────────────────────────────────────────────
step "0. tools and floci (EKS real mode, IAM enforcement, principal identity)"
for t in docker aws jq kubectl python3; do command -v "$t" >/dev/null 2>&1 || fail "$t is not on PATH"; done
[ -S /var/run/docker.sock ] || fail "no /var/run/docker.sock - floci's EKS real mode needs it"
if [ -n "${TOFU_BIN:-}" ]; then TOFU="$TOFU_BIN"; else
  TOFU="$WORK/bin/choudoufu"; mkdir -p "$WORK/bin"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
fi
gauntlet_floci_start "$FLOCI_NAME" -p "${FLOCI_PORT}:4566" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e FLOCI_SERVICES_EKS_ENDPOINT_MODE=host \
  -e FLOCI_SERVICES_EKS_PRINCIPAL_IDENTITY=true \
  -e FLOCI_IAM_ENFORCEMENT=true \
  -e "FLOCI_DOCKER_RESOURCE_NAMESPACE=$FLOCI_NS" \
  "$FLOCI_IMAGE" || fail "floci did not start"
for _ in $(seq 1 45); do curl -fs "$ENDPOINT/_localstack/health" 2>/dev/null | grep -q '"eks"' && break; sleep 2; done
ACCT="$(awsl sts get-caller-identity --query Account --output text)" || fail "the emulator did not answer sts get-caller-identity"

# ── 1. the account stands one estate up across both legs ────────────────
step "1. the account applies estate app: the reference-eks shape plus the carve's two objects"
mkdir -p "$APP"
{ reference_eks_main_tf app "$(aws_provider_block)" "$PREFIX" false "$REGION" && carve_blocks; } > "$APP/main.tf" || fail "could not write app"
( cd "$APP" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "init failed in app"
( cd "$APP" && "$TOFU" apply -input=false -auto-approve -no-color > "$WORK/app-apply.out" 2>&1 ) || { tail -30 "$WORK/app-apply.out"; fail "the account's apply of app failed"; }
CARVE_VPC="$(awsl ec2 describe-vpcs --filters Name=tag:tofu-address,Values=aws_vpc.carve --query 'Vpcs[0].VpcId' --output text)"
MAIN_VPC="$(awsl ec2 describe-vpcs --filters Name=tag:tofu-address,Values=aws_vpc.main --query 'Vpcs[0].VpcId' --output text)"
[ "$(admin_kc get configmap app-config -n app -o jsonpath='{.metadata.labels.tofu-estate}')" = "app" ] || fail "app-config carries no tofu-estate=app"
proof "one estate, app, on both legs: $MAIN_VPC and $CARVE_VPC tagged, app/app-config and default/carve labelled."

# ── 2. two roles, two fences each ───────────────────────────────────────
step "2. alice holds app on both legs (and may carve into data); bob holds neither"
TRUST="{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::$ACCT:root\"},\"Action\":\"sts:AssumeRole\"}]}"
for r in alice bob; do
  awsl iam create-role --role-name "$r" --assume-role-policy-document "$TRUST" >/dev/null || fail "could not create role $r"
  awsl eks create-access-entry --cluster-name "$CLUSTER" --principal-arn "arn:aws:iam::$ACCT:role/$r" \
    --username "$r" --kubernetes-groups estate-writers >/dev/null || fail "could not create $r's access entry"
done
awsl iam put-role-policy --role-name alice --policy-name estate --policy-document "$(grant app data)" || fail "could not grant alice"
awsl iam put-role-policy --role-name bob --policy-name estate --policy-document "$(grant other other)" || fail "could not grant bob"
admin_kc apply -f - < "$ROOT/live/kubernetes/estate-boundary.yaml" >/dev/null || fail "could not install the estate boundary policy"
admin_kc create clusterrolebinding estate-writers-edit --clusterrole=edit --group=estate-writers >/dev/null || fail "could not give the writers edit"
for e in app data; do
  sed -e "s/PRINCIPAL_NAMESPACE//" -e "s/ESTATE/$e/g" -e "s/PRINCIPAL/alice/g" "$ROOT/live/kubernetes/estate-grant.yaml" \
    | python3 -c 'import sys
s=sys.stdin.read()
s=s.replace("  - kind: ServiceAccount\n    name: alice\n    namespace: \n","  - kind: User\n    name: alice\n    apiGroup: rbac.authorization.k8s.io\n")
assert "kind: User" in s, "estate-grant.yaml no longer has the subject shape this rewrite expects"
sys.stdout.write(s)' | admin_kc apply -f - >/dev/null || fail "could not bind alice to estate $e"
done
awsl eks update-kubeconfig --name "$CLUSTER" --kubeconfig "$WORK/kubeconfig" >/dev/null || fail "could not write a kubeconfig"
proof "IAM: alice may act on tofu-estate=app and create into data; bob on other only. Cluster: one admission policy; alice bound to estates app and data; both may edit."

# ── 3. each role is its own user on the cluster ─────────────────────────
step "3. the exec token is the role: kubectl auth whoami as each"
for r in alice bob; do
  WHO="$(role_kc "$r" auth whoami -o jsonpath='{.status.userInfo.username}' 2>&1)" || fail "$r cannot reach the cluster: $WHO"
  [ "$WHO" = "$r" ] || fail "$r reaches the cluster as \"$WHO\", not as $r. On floci:aws-iam the image lacks principal identity (lex00/floci#220): every role is system:masters and the cluster fence cannot tell the two apart"
done
proof "alice is alice and bob is bob on the cluster, through the same aws eks get-token an EKS root's provider block runs."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control: drop bob's IAM conditions and the admission policy; both of bob's writes must land"
  awsl iam put-role-policy --role-name bob --policy-name estate --policy-document "$(ungoverned)" || fail "BREAK: could not rewrite bob's grant"
  admin_kc delete validatingadmissionpolicybinding choudoufu-estate-boundary >/dev/null || fail "BREAK: could not remove the binding"
  admin_kc delete validatingadmissionpolicy choudoufu-estate-boundary >/dev/null || fail "BREAK: could not remove the policy"
  OUT="$(as_role bob awsl ec2 create-tags --resources "$MAIN_VPC" --tags Key=note,Value=bob 2>&1)" || fail "BREAK: with no condition, bob's AWS write was still refused: $OUT"
  OUT="$(role_kc bob label configmap app-config -n app note=bob --overwrite 2>&1)" || fail "BREAK: with no policy, bob's cluster write was still refused: $OUT"
  proof "caught - with both fences down, bob wrote both legs. The conditions and the policy were the whole of what refused him."
  exit 0
fi

# ── 4. the AWS fence ────────────────────────────────────────────────────
step "4. AWS leg: bob is refused by IAM, alice is admitted"
OUT="$(as_role bob awsl ec2 create-tags --resources "$MAIN_VPC" --tags Key=note,Value=bob 2>&1 || true)"
iam_denied "$OUT" || fail "bob's create-tags on app's VPC was not refused by IAM: $OUT"
as_role alice awsl ec2 create-tags --resources "$MAIN_VPC" --tags Key=note,Value=alice >/dev/null || fail "alice's create-tags on app's VPC was refused"
proof "one tag write each, no choudoufu in the call: IAM refused bob and admitted alice on the ownership tag."

# ── 5. the cluster fence ────────────────────────────────────────────────
step "5. cluster leg: bob is refused by the admission policy, alice is admitted"
OUT="$(role_kc bob label configmap app-config -n app note=bob --overwrite 2>&1 || true)"
vap_denied "$OUT" || fail "bob's label write on app/app-config was not refused by the estate boundary policy: $OUT"
role_kc alice label configmap app-config -n app note=alice --overwrite >/dev/null || fail "alice's label write on app/app-config was refused"
proof "one label write each: the policy refused bob, who is not bound to estate app, and admitted alice, who is."

# ── 6. the fences do not agree for you ──────────────────────────────────
step "6. bob is given app on the AWS leg only: admitted there, still refused on the cluster"
awsl iam put-role-policy --role-name bob --policy-name estate --policy-document "$(grant app other)" || fail "could not widen bob's IAM grant"
as_role bob awsl ec2 create-tags --resources "$MAIN_VPC" --tags Key=note,Value=bob >/dev/null || fail "bob's widened IAM grant did not admit his AWS write"
OUT="$(role_kc bob label configmap app-config -n app note=bob --overwrite 2>&1 || true)"
vap_denied "$OUT" || fail "bob's cluster write was admitted though only his IAM grant changed: $OUT"
awsl iam put-role-policy --role-name bob --policy-name estate --policy-document "$(grant other other)" || fail "could not narrow bob's grant back"
proof "two fences, two credentials, two answers: an IAM grant moved and the cluster's answer did not."

# ── 7. a carve is one move per leg, each judged by its own fence ────────
step "7. the carve: aws_vpc.carve and kubernetes_config_map_v1.carve move from app into data"
python3 - "$APP/main.tf" <<'PY' || fail "could not remove the carve's blocks from app"
import re, sys
p = sys.argv[1]
s = open(p).read()
for head in ('resource "aws_vpc" "carve" {', 'resource "kubernetes_config_map_v1" "carve" {'):
    i = s.index(head)
    depth, j = 0, i
    while True:
        if s[j] == '{': depth += 1
        elif s[j] == '}':
            depth -= 1
            if depth == 0: break
        j += 1
    s = s[:i] + s[j + 1:]
open(p, 'w').write(s)
PY
mkdir -p "$DATA"
data_root > "$DATA/main.tf" || fail "could not write data"
( cd "$DATA" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "init failed in data"

for addr in aws_vpc.carve kubernetes_config_map_v1.carve; do
  OUT="$(cd "$DATA" && as_role bob "$TOFU" live-mv -from-estate=app "$addr" "$addr" 2>&1 || true)"
  case "$addr" in
    aws_*) iam_denied "$OUT" || fail "bob's carve of $addr was not refused by IAM: $OUT" ;;
    *) vap_denied "$OUT" || fail "bob's carve of $addr was not refused by the admission policy: $OUT" ;;
  esac
done
[ "$(awsl ec2 describe-tags --filters Name=resource-id,Values="$CARVE_VPC" Name=key,Values=tofu-estate --query 'Tags[0].Value' --output text)" = "app" ] \
  || fail "the carve VPC left app despite bob's refusal"
[ "$(admin_kc get configmap carve -n default -o jsonpath='{.metadata.labels.tofu-estate}')" = "app" ] \
  || fail "default/carve left app despite bob's refusal"
for addr in aws_vpc.carve kubernetes_config_map_v1.carve; do
  OUT="$(cd "$DATA" && as_role alice "$TOFU" live-mv -from-estate=app "$addr" "$addr" 2>&1)" || fail "alice's carve of $addr failed: $OUT"
done
[ "$(awsl ec2 describe-tags --filters Name=resource-id,Values="$CARVE_VPC" Name=key,Values=tofu-estate --query 'Tags[0].Value' --output text)" = "data" ] \
  || fail "the carve VPC does not carry tofu-estate=data after alice's move"
[ "$(admin_kc get configmap carve -n default -o jsonpath='{.metadata.labels.tofu-estate}')" = "data" ] \
  || fail "default/carve does not carry tofu-estate=data after alice's move"
proof "two moves, one per leg, run by choudoufu under each role's own credential: IAM refused bob's AWS move, the admission policy refused his cluster move, and both of alice's landed. No move spanned both legs."

# ── 8. both estates plan clean ──────────────────────────────────────────
step "8. data plans clean as alice; app plans clean as the account"
OUT="$(cd "$DATA" && as_role alice "$TOFU" plan -input=false -no-color 2>&1)" || fail "data does not plan as alice: $(tail -5 <<< "$OUT")"
grep -q "No changes." <<< "$OUT" || fail "data does not plan clean as alice: $(grep -m3 -E '^Plan:|#' <<< "$OUT")"
OUT="$(cd "$APP" && "$TOFU" plan -input=false -no-color 2>&1)" || fail "app does not plan: $(tail -5 <<< "$OUT")"
grep -q "No changes." <<< "$OUT" || fail "app does not plan clean: $(grep -m3 -E '^Plan:|#' <<< "$OUT")"
proof "where there was one estate on two legs there are two, each clean, and every write on the way was judged by the fence of the leg it touched."
