# a-role-holds-an-address-pattern
# CLAIM 13 (aws) - The tag is the boundary. ~3 min.
#
# This proof: one IAM condition on the tofu-address tag scopes a role to an
# address pattern. In one estate the role changes what module.app.* owns,
# and the platform refuses it on any other address, with no estate
# condition, no second estate and no state split doing the fencing (#1955).
#
# the-tag-is-the-boundary fences two roles to two halves and then carves
# one half out. This scenario is the narrower grant on its own: one role,
# one statement, one StringLike on aws:ResourceTag/tofu-address, so a
# refusal here can only have come from that condition.

W="$SMOKE_WORKROOT/addrrole"; ROOTDIR="$W/shop"; MODS="$W/modules"
mkdir -p "$ROOTDIR" "$MODS/app" "$MODS/db"
# The compose file interpolates the oracle service, so SMOKE_WORK must be set
# even though this scenario never uses the oracle leg.
SMOKE_WORK="$W"; export SMOKE_WORK
LOGS="$SMOKE_WORKROOT/addrrole-logs"; mkdir -p "$LOGS"

# The emulator's IAM enforcement filter is off by default. The harness's own
# "test" key keeps bypassing it, so the account steps below are ungoverned
# and only the role this scenario creates and assumes is evaluated.
export FLOCI_IAM_ENFORCEMENT=true

cat > "$ROOTDIR/versions.tf" <<'EOF'
terraform {
  required_version = ">= 1.5.0"
  live {
    estate = "shop"
  }
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}
provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
EOF
cat > "$MODS/app/main.tf" <<'TFEOF'
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  tags          = { Name = "web" }
}
TFEOF
cat > "$MODS/db/main.tf" <<'TFEOF'
resource "aws_instance" "primary" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  tags          = { Name = "primary" }
}
TFEOF
cat > "$ROOTDIR/main.tf" <<'TFEOF'
module "app" { source = "../modules/app" }

module "db" { source = "../modules/db" }
TFEOF

APP_ADDR="module.app.aws_instance.web"
DB_ADDR="module.db.aws_instance.primary"

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

ACCT="$(awsl sts get-caller-identity --query Account --output text)" || fail "addrrole" "the emulator did not answer sts get-caller-identity"
TRUST="{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::$ACCT:root\"},\"Action\":\"sts:AssumeRole\"}]}"

# grant is the within-estate grant live/MARKERS.md describes under "What this
# grant cannot reach" and the scope-a-role guide prints under "Part of an
# estate": reads any plan needs, and the mutating actions conditioned on the
# ownership address alone. $1 is the condition block, or empty for none, so
# the BREAK arm drops exactly the condition and keeps every action.
grant() {
  local cond="${1:+,\"Condition\":$1}"
  cat <<EOF
{"Version":"2012-10-17","Statement":[
 {"Sid":"ReadTheAccount","Effect":"Allow",
  "Action":["ec2:Describe*","tag:GetResources","sts:GetCallerIdentity"],"Resource":"*"},
 {"Sid":"ChangeModuleApp","Effect":"Allow",
  "Action":["ec2:CreateTags","ec2:DeleteTags","ec2:TerminateInstances"],"Resource":"*"$cond}
]}
EOF
}
COND='{"StringLike":{"aws:ResourceTag/tofu-address":"module.app.*"}}'

# as_role runs one command under the role's session credentials, in a
# subshell so nothing leaks back into the account-level steps.
as_role() {
  local c
  c="$(awsl sts assume-role --role-arn "arn:aws:iam::$ACCT:role/app-deployer" --role-session-name app-deployer \
        --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]' --output text)" || { echo "  could not assume app-deployer" >&2; return 1; }
  ( export AWS_ACCESS_KEY_ID="$(cut -f1 <<< "$c")" AWS_SECRET_ACCESS_KEY="$(cut -f2 <<< "$c")" AWS_SESSION_TOKEN="$(cut -f3 <<< "$c")"
    "$@" )
}
# tags_of prints Name, tofu-estate and tofu-address for the instance whose
# ownership address is $1, read as the account.
tags_of() {
  awsl ec2 describe-instances --filters "Name=tag:tofu-address,Values=$1" \
    --query 'Reservations[].Instances[].[Tags[?Key==`Name`]|[0].Value,Tags[?Key==`tofu-estate`]|[0].Value,Tags[?Key==`tofu-address`]|[0].Value]' \
    --output text
}
name_of() { tags_of "$1" | awk '{print $1}'; }
id_of() {
  awsl ec2 describe-instances --filters "Name=tag:tofu-address,Values=$1" \
    --query 'Reservations[].Instances[].InstanceId' --output text
}
# rename_tag rewrites one Name tag value in a module file without sed -i.
rename_tag() { local f="$1" from="$2" to="$3" t; t="$(mktemp)"
  sed "s/Name = \"$from\"/Name = \"$to\"/" "$f" > "$t" && mv "$t" "$f" || fail "addrrole" "could not rewrite $f"
  grep -qF "Name = \"$to\"" "$f" || fail "addrrole" "the rename $from -> $to did not land in $f"; }
# denied: the refusal is the platform's, read as the-tag-is-the-boundary
# reads it, with the sweep's own gap warning stripped first (#1636) so a
# denied read the role was never granted cannot pass for the refusal.
denied() { strip_incomplete_sweep_warning <<< "$1" | grep -qE 'UnauthorizedOperation|AccessDenied|not authorized to perform|StatusCode: 403'; }
refusal_line() { { strip_incomplete_sweep_warning <<< "$1" | sed 's/\x1b\[[0-9;]*m//g' | grep -E 'UnauthorizedOperation|AccessDenied|not authorized to perform|StatusCode: 403' || true; } | head -1 | sed -e 's/^[[:space:]]*//' -e 's/^[^A-Za-z]*//' -e 's/, RequestID: [0-9a-f-]*//'; }

step "the claim"
explain \
  "In stock, a role that may apply a root may change everything in it:" \
  "the state file is one object and the cloud never sees which block a" \
  "resource came from. Here every resource carries its configuration" \
  "address as the tofu-address tag, so IAM can scope a role below the" \
  "estate. One StringLike on aws:ResourceTag/tofu-address, module.app.*," \
  "lets a role change what module.app declares and nothing else in the" \
  "same estate. The refusal is AWS's, so it holds for a plain AWS CLI" \
  "call as well as for this tool."

step "1. the account stands up one estate with two modules in it"
explain \
  "One root, estate shop, declares module.app and module.db, an instance" \
  "each. Both carry tofu-estate=shop; only tofu-address tells them apart."
cmd "choudoufu apply -auto-approve   # in shop/, as the account"
logged a-role-holds-an-address-pattern-init "addrrole" "init failed in shop" -- in_dir "$ROOTDIR" chdf init -input=false -no-color
( cd "$ROOTDIR" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "addrrole" "apply failed in shop"
{ tags_of "$APP_ADDR"; tags_of "$DB_ADDR"; } | evidence
[ "$(tags_of "$APP_ADDR" | awk '{print $2}')" = "shop" ] || fail "addrrole" "$APP_ADDR does not carry tofu-estate=shop"
[ "$(tags_of "$DB_ADDR"  | awk '{print $2}')" = "shop" ] || fail "addrrole" "$DB_ADDR does not carry tofu-estate=shop"
proof "two instances, one estate, and the address on each is the only tag that differs."

step "2. one role, one condition on the address"
explain \
  "app-deployer may read the account, and may tag, untag and terminate" \
  "what carries a tofu-address matching module.app.*. There is no" \
  "tofu-estate condition at all, so nothing but the address can fence it."
cmd "aws iam create-role app-deployer ; aws iam put-role-policy ... StringLike aws:ResourceTag/tofu-address = module.app.*"
awsl iam create-role --role-name app-deployer --assume-role-policy-document "$TRUST" >/dev/null || fail "addrrole" "could not create app-deployer"
awsl iam put-role-policy --role-name app-deployer --policy-name scope --policy-document "$(grant "$COND")" || fail "addrrole" "could not grant app-deployer"
GOT="$(awsl iam get-role-policy --role-name app-deployer --policy-name scope --query 'PolicyDocument.Statement[1].Condition' --output json)" || fail "addrrole" "could not read the grant back"
printf '%s\n' "$(tr -d ' \n' <<< "$GOT")" | evidence
grep -qF 'module.app.*' <<< "$GOT" || fail "addrrole" "the grant read back does not carry the module.app.* condition"
WHO="$(as_role awsl sts get-caller-identity --query Arn --output text)" || fail "addrrole" "app-deployer's session cannot identify itself"
echo "$WHO" | evidence
proof "the role holds one address pattern, and the pattern is a condition on a tag this tool wrote."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the condition is removed; the change on module.db must land"
  explain \
    "The claim is that the condition is the fence. Rewrite the grant with" \
    "the same actions and no Condition, then have the role change" \
    "module.db. If AWS still refuses, something other than the condition" \
    "was fencing it and the main run proves nothing. It must succeed."
  cmd "aws iam put-role-policy app-deployer (no Condition) ; sed Name=primary-v2 ; choudoufu apply -auto-approve   # in shop/, as app-deployer"
  awsl iam put-role-policy --role-name app-deployer --policy-name scope --policy-document "$(grant '')" || fail "addrrole" "BREAK: could not rewrite the grant"
  rename_tag "$MODS/db/main.tf" primary primary-v2
  OUT="$(cd "$ROOTDIR" && as_role chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "addrrole" "BREAK: with no condition, the change on module.db was still refused: $(refusal_line "$OUT")"
  denied "$OUT" && fail "addrrole" "BREAK: the apply succeeded but its output still carries a refusal: $(refusal_line "$OUT")"
  tags_of "$DB_ADDR" | evidence
  [ "$(name_of "$DB_ADDR")" = "primary-v2" ] || fail "addrrole" "BREAK: the change on module.db did not land"
  proof "caught - with the condition removed, the role changed module.db. The address condition was the whole fence."
  ( cd "$ROOTDIR" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "3. the role changes module.app"
explain \
  "A tag on module.app's instance changes in configuration and the role" \
  "applies it. The CreateTags lands on a resource whose tofu-address is" \
  "module.app.aws_instance.web, the pattern matches, and AWS allows it."
cmd "sed Name=web-v2 ; choudoufu apply -auto-approve   # in shop/, as app-deployer"
rename_tag "$MODS/app/main.tf" web web-v2
OUT="$(cd "$ROOTDIR" && as_role chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "addrrole" "the role's apply on module.app failed: $(refusal_line "$OUT")"
printf '%s\n' "$OUT" > "$LOGS/app.apply"
tags_of "$APP_ADDR" | evidence
[ "$(name_of "$APP_ADDR")" = "web-v2" ] || fail "addrrole" "the change on module.app did not land"
[ "$(name_of "$DB_ADDR")" = "primary" ] || fail "addrrole" "module.db changed although nothing asked it to"
proof "the change on module.app landed under the role, and module.db is as it was."

step "4. the same role, the same estate, another address: refused by AWS"
explain \
  "The same kind of change on module.db, in the same estate, under the" \
  "same session. Its tofu-address is module.db.aws_instance.primary, the" \
  "pattern does not match, and CreateTags comes back denied. choudoufu" \
  "reports the platform's refusal and the instance keeps its tag."
cmd "sed Name=primary-v2 ; choudoufu apply -auto-approve   # in shop/, as app-deployer"
rename_tag "$MODS/db/main.tf" primary primary-v2
OUT="$(cd "$ROOTDIR" && as_role chdf apply -auto-approve -input=false -no-color 2>&1 || true)"
printf '%s\n' "$OUT" > "$LOGS/db-denied.apply"
denied "$OUT" || fail "addrrole" "the role's change on module.db was not refused by the platform (full output in $LOGS/db-denied.apply): $(grep -E '^Plan:|Apply complete|Error' <<< "$OUT" | head -3)"
refusal_line "$OUT" | evidence
[ "$(name_of "$DB_ADDR")" = "primary" ] || fail "addrrole" "module.db changed despite the refusal"
[ "$(tags_of "$DB_ADDR" | awk '{print $2}')" = "shop" ] || fail "addrrole" "module.db is no longer in estate shop"
proof "refused on module.db, in estate shop, by the condition that let module.app through."

step "5. the refusal binds the credential, not the binary"
explain \
  "With no choudoufu in the call, the role's plain aws ec2 create-tags" \
  "and terminate-instances on module.db's instance are refused by the" \
  "same condition. A create-tags on module.app's instance goes through."
DB_ID="$(id_of "$DB_ADDR")" || fail "addrrole" "could not read module.db's instance id"
APP_ID="$(id_of "$APP_ADDR")" || fail "addrrole" "could not read module.app's instance id"
cmd "aws ec2 create-tags --resources $DB_ID --tags Key=Name,Value=primary-cli   # as app-deployer"
OUT="$(as_role awsl ec2 create-tags --resources "$DB_ID" --tags 'Key=Name,Value=primary-cli' 2>&1 || true)"
denied "$OUT" || fail "addrrole" "the role's plain create-tags on module.db was not refused: $OUT"
refusal_line "$OUT" | evidence
cmd "aws ec2 terminate-instances --instance-ids $DB_ID   # as app-deployer"
OUT="$(as_role awsl ec2 terminate-instances --instance-ids "$DB_ID" 2>&1 || true)"
denied "$OUT" || fail "addrrole" "the role's plain terminate-instances on module.db was not refused: $OUT"
refusal_line "$OUT" | evidence
STATE="$(awsl ec2 describe-instances --instance-ids "$DB_ID" --query 'Reservations[].Instances[].State.Name' --output text)"
[ "$STATE" = "running" ] || fail "addrrole" "module.db's instance was terminated despite the refusal (state: $STATE)"
[ "$(name_of "$DB_ADDR")" = "primary" ] || fail "addrrole" "module.db's Name changed despite the refusal"
cmd "aws ec2 create-tags --resources $APP_ID --tags Key=Name,Value=web-v2   # as app-deployer"
as_role awsl ec2 create-tags --resources "$APP_ID" --tags 'Key=Name,Value=web-v2' >/dev/null \
  || fail "addrrole" "the role's plain create-tags on module.app was refused"
echo "create-tags on $APP_ADDR: allowed" | evidence
proof "two plain AWS CLI calls refused on module.db and one allowed on module.app, each by the address condition."

step "6. teardown"
( cd "$ROOTDIR" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "addrrole" "teardown of shop failed"
proof "the estate is gone, destroyed as the account."

echo "  What you watched: one role, one StringLike on the tofu-address tag."
echo "  In a single estate it changed module.app and was refused by AWS on"
echo "  module.db, through choudoufu and through the plain AWS CLI alike."
echo "  With the condition removed, the same change on module.db landed."
