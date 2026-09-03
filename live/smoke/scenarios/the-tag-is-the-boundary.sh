# the-tag-is-the-boundary
# CLAIM 13 - The tag is the boundary: ownership is a tag, so the cloud's own policy engine governs who may act on what, per resource, and a carve is a governed tag write instead of state surgery nothing can gate. ~4 min.
#
# Two roles on one estate, each fenced to its half by a condition on the
# ownership tag. Then one of them carves her half out into an estate of its
# own, and the carve itself is governed: the other role's attempt at the same
# move is denied by the platform, not by this tool.

W="$SMOKE_WORKROOT/boundary"; APP="$W/app"; DATA="$W/data"; MODS="$W/modules"
mkdir -p "$APP" "$DATA" "$MODS/net" "$MODS/data"
# The compose file interpolates the oracle service, so SMOKE_WORK must be set
# even though this scenario never uses the oracle leg.
SMOKE_WORK="$W"; export SMOKE_WORK
LOGS="$SMOKE_WORKROOT/boundary-logs"; mkdir -p "$LOGS"

# The emulator's IAM enforcement filter is off by default, and this is the
# scenario that needs it on. The harness's own "test" key keeps bypassing the
# filter, so the platform steps below run as the account and only the two
# roles this scenario creates and assumes are governed.
export FLOCI_IAM_ENFORCEMENT=true

versions() { cat <<EOF
terraform {
  required_version = ">= 1.5.0"
  live {
    estate = "$1"
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
}
cat > "$MODS/net/main.tf" <<'TFEOF'
resource "aws_instance" "gateway" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  tags          = { Name = "gateway" }
}
TFEOF
cat > "$MODS/data/main.tf" <<'TFEOF'
resource "aws_instance" "database" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  tags          = { Name = "database" }
}
TFEOF
versions app > "$APP/versions.tf"
cat > "$APP/main.tf" <<'TFEOF'
module "net" { source = "../modules/net" }

module "data" { source = "../modules/data" }
TFEOF
versions data > "$DATA/versions.tf"
: > "$DATA/main.tf"

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

ACCT="$(awsl sts get-caller-identity --query Account --output text)"
TRUST="{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::$ACCT:root\"},\"Action\":\"sts:AssumeRole\"}]}"

# grant is the two-statement grant live/MARKERS.md publishes under "Granting
# an estate", plus the reads any plan needs. $1 is the half of the shared
# estate the role may act on, by ownership address; $2 is the estate it may
# create into or move a resource into, by ownership estate.
grant() { cat <<EOF
{"Version":"2012-10-17","Statement":[
 {"Sid":"ReadTheAccount","Effect":"Allow",
  "Action":["ec2:Describe*","tag:GetResources","sts:GetCallerIdentity"],"Resource":"*"},
 {"Sid":"ActOnMyHalf","Effect":"Allow",
  "Action":["ec2:CreateTags","ec2:DeleteTags","ec2:TerminateInstances"],"Resource":"*",
  "Condition":{"StringLike":{"aws:ResourceTag/tofu-address":"$1"}}},
 {"Sid":"CreateIntoMyEstate","Effect":"Allow",
  "Action":["ec2:RunInstances","ec2:CreateTags"],"Resource":"*",
  "Condition":{"StringEquals":{"aws:RequestTag/tofu-estate":"$2"}}}
]}
EOF
}
# ungoverned is the same reach with every condition dropped: the BREAK control.
ungoverned() { echo '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["ec2:*","tag:*","sts:GetCallerIdentity"],"Resource":"*"}]}'; }

# as_role runs one command under a role's session credentials, in a subshell
# so nothing leaks back into the account-level steps.
as_role() {
  local role="$1" c; shift
  c="$(awsl sts assume-role --role-arn "arn:aws:iam::$ACCT:role/$role" --role-session-name "$role" \
        --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]' --output text)" || fail "boundary" "could not assume $role"
  ( export AWS_ACCESS_KEY_ID="$(cut -f1 <<< "$c")" AWS_SECRET_ACCESS_KEY="$(cut -f2 <<< "$c")" AWS_SESSION_TOKEN="$(cut -f3 <<< "$c")"
    "$@" )
}
# tags_of prints Name, tofu-estate and tofu-address for the instance whose
# ownership address is $1, read through the plain CLI as the account.
tags_of() {
  awsl ec2 describe-instances --filters "Name=tag:tofu-address,Values=$1" \
    --query 'Reservations[].Instances[].[Tags[?Key==`Name`]|[0].Value,Tags[?Key==`tofu-estate`]|[0].Value,Tags[?Key==`tofu-address`]|[0].Value]' \
    --output text
}
# settle waits until the tagging index the sweep reads reflects a tag write
# (the #756 lesson): $1 is the estate to look under, $2 the address expected.
settle() { local i; for i in $(seq 1 30); do
  if awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$1" \
       --query 'ResourceTagMappingList[].Tags[?Key==`tofu-address`].Value' --output text 2>/dev/null | grep -qF "$2"; then return 0; fi
  sleep 1; done; return 0; }
# denied fails the scenario unless $1 (a captured command output) carries the
# platform's own refusal. The tool never says no here; AWS does. Real EC2
# answers UnauthorizedOperation; the emulator answers 403 with a body the EC2
# SDK cannot parse (lex00/floci: EC2 deny envelope), which the provider
# reports as a bare 403 (lex00/floci#189).
denied() { grep -qE 'UnauthorizedOperation|AccessDenied|not authorized to perform|StatusCode: 403' <<< "$1"; }
refusal_line() { sed 's/\x1b\[[0-9;]*m//g' <<< "$1" | grep -E 'UnauthorizedOperation|AccessDenied|not authorized to perform|StatusCode: 403' | head -1 | sed -e 's/^[[:space:]]*//' -e 's/^[^A-Za-z]*//' -e 's/, RequestID: [0-9a-f-]*//'; }

step "the claim"
explain \
  "In stock, who owns a resource is a line in a state file. Changing that" \
  "line is state surgery. No IAM policy can gate it, because the cloud" \
  "never sees it, and no log in the account records it. Here ownership" \
  "is a tag on the resource, and a tag write is an API call the cloud's" \
  "own policy engine evaluates per resource. So a role can be fenced to" \
  "half an estate by a condition on the ownership tag, and a carve, one" \
  "half moving into an estate of its own, is a governed write rather" \
  "than an edit nobody can refuse. Two roles share one estate here. Each" \
  "converges its half and is denied on the other's, by AWS. Then Alice" \
  "carves her half out and Bob's attempt at the same carve is denied." \
  "Where there was one estate there are two, with no state split, and" \
  "every step of it was something a policy could say no to."

step "1. the platform stands one estate up, two halves in it"
explain \
  "A shared estate, app, holds a network half and a data half, each a" \
  "module. The account stands it up; the markers it stamps are what the" \
  "policies below will condition on."
cmd "choudoufu apply -auto-approve   # in app/, as the account"
( cd "$APP" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "init failed in app"
( cd "$APP" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "apply failed in app"
settle app "module.data.aws_instance.database"
{ tags_of "module.net.aws_instance.gateway"; tags_of "module.data.aws_instance.database"; } | evidence
[ "$(tags_of 'module.data.aws_instance.database' | awk '{print $2}')" = "app" ] || fail "boundary" "the database does not carry tofu-estate=app"
[ "$(tags_of 'module.net.aws_instance.gateway' | awk '{print $2}')" = "app" ] || fail "boundary" "the gateway does not carry tofu-estate=app"
proof "two instances in one estate, and the ownership address on each names its half, module.net.* or module.data.*."

step "2. two roles, two halves, one grant shape"
explain \
  "Alice's role may act on module.data.* and create into the estate" \
  "data; Bob's may act on module.net.* and create into net. The grant is" \
  "the one live/MARKERS.md publishes: aws:ResourceTag on the ownership" \
  "tag for what the role already owns, aws:RequestTag for what it may" \
  "bring into its estate. Reads are open to both. Nothing here is a" \
  "choudoufu feature; it is IAM reading a tag."
cmd "aws iam create-role alice ; aws iam put-role-policy ... aws:ResourceTag/tofu-address = module.data.*   # and bob, module.net.*"
awsl iam create-role --role-name alice --assume-role-policy-document "$TRUST" >/dev/null || fail "boundary" "could not create alice"
awsl iam create-role --role-name bob   --assume-role-policy-document "$TRUST" >/dev/null || fail "boundary" "could not create bob"
awsl iam put-role-policy --role-name alice --policy-name estate --policy-document "$(grant 'module.data.*' data)" || fail "boundary" "could not grant alice"
awsl iam put-role-policy --role-name bob   --policy-name estate --policy-document "$(grant 'module.net.*'  net)"  || fail "boundary" "could not grant bob"
awsl iam get-role-policy --role-name alice --policy-name estate --query 'PolicyDocument.Statement[1:].Condition' --output json | tr -d ' \n' | evidence
as_role alice awsl sts get-caller-identity --query Arn --output text | evidence
proof "two roles hold two halves of one estate, and the fence is a condition on the tag this tool wrote."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - drop Bob's conditions; the cross-half denial must vanish"
  explain \
    "The claim is that the CONDITION is the boundary, not the credentials." \
    "Replace Bob's grant with the same reach and no conditions, then have" \
    "Bob change a tag on Alice's half. If AWS still refuses, something other" \
    "than the condition was fencing him and the claim proves nothing. It" \
    "must succeed."
  cmd "aws iam put-role-policy bob (no Condition) ; sed Name=database-v2 ; choudoufu apply -auto-approve   # in app/, as bob"
  awsl iam put-role-policy --role-name bob --policy-name estate --policy-document "$(ungoverned)" || fail "boundary" "BREAK: could not rewrite bob's grant"
  sed -i '' 's/Name = "database"/Name = "database-v2"/' "$MODS/data/main.tf"
  OUT="$(cd "$APP" && as_role bob chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "BREAK: with no condition, Bob's write on Alice's half was still refused: $(grep -E 'AccessDenied|not authorized|Error' <<< "$OUT" | head -3)"
  denied "$OUT" && fail "boundary" "BREAK: the apply succeeded but the output still carries a refusal: $OUT"
  tags_of "module.data.aws_instance.database" | evidence
  [ "$(tags_of 'module.data.aws_instance.database' | awk '{print $1}')" = "database-v2" ] || fail "boundary" "BREAK: Bob's write did not land"
  proof "caught - with the condition gone, Bob wrote Alice's half. The condition on the tag was the whole boundary, and it can be taken down as well as put up."
  ( cd "$APP" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "3. Alice converges her half"
explain \
  "A tag on the database changes in configuration. Alice applies it. The" \
  "provider's CreateTags carries the resource's ownership address, the" \
  "policy engine matches module.data.*, and the write goes through."
cmd "sed Name=database-v2 ; choudoufu apply -auto-approve   # in app/, as alice"
sed -i '' 's/Name = "database"/Name = "database-v2"/' "$MODS/data/main.tf"
OUT="$(cd "$APP" && as_role alice chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "Alice's apply on her own half failed: $(grep -E 'Error|AccessDenied|not authorized' <<< "$OUT" | head -3)"
tags_of "module.data.aws_instance.database" | evidence
[ "$(tags_of 'module.data.aws_instance.database' | awk '{print $1}')" = "database-v2" ] || fail "boundary" "Alice's write did not land"
proof "Alice's write landed on her half. The policy read the ownership tag off the resource and said yes."

step "4. Alice is denied on Bob's half - by AWS, not by this tool"
explain \
  "The same kind of change, on the gateway. Alice applies it and the" \
  "platform refuses: her grant's condition names module.data.*, the" \
  "gateway carries module.net.*, and CreateTags comes back AccessDenied." \
  "choudoufu surfaces the refusal and changes nothing. In stock the" \
  "equivalent move is a state edit, and there is no API call to refuse."
cmd "sed Name=gateway-v2 ; choudoufu apply -auto-approve   # in app/, as alice"
sed -i '' 's/Name = "gateway"/Name = "gateway-v2"/' "$MODS/net/main.tf"
OUT="$(cd "$APP" && as_role alice chdf apply -auto-approve -input=false -no-color 2>&1 || true)"
printf '%s\n' "$OUT" > "$LOGS/alice-denied.apply"
denied "$OUT" || fail "boundary" "Alice's write on Bob's half was not refused by the platform (full output in $LOGS/alice-denied.apply): $(grep -E '^Plan:|Apply complete|Error' <<< "$OUT" | head -3)"
refusal_line "$OUT" | evidence
[ "$(tags_of 'module.net.aws_instance.gateway' | awk '{print $1}')" = "gateway" ] || fail "boundary" "the gateway changed despite the refusal"
proof "a 403 on ec2:CreateTags, from the platform. The gateway is untouched, and the refusal is the account's own."

step "5. Bob converges the same change"
explain \
  "The pending change is Bob's to make. Same configuration under his" \
  "session, and the condition matches module.net.*, so the write lands."
cmd "choudoufu apply -auto-approve   # in app/, as bob"
OUT="$(cd "$APP" && as_role bob chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "boundary" "Bob's apply on his own half failed: $(grep -E 'Error|AccessDenied|not authorized' <<< "$OUT" | head -3)"
tags_of "module.net.aws_instance.gateway" | evidence
[ "$(tags_of 'module.net.aws_instance.gateway' | awk '{print $1}')" = "gateway-v2" ] || fail "boundary" "Bob's write did not land"
proof "one estate with two halves and two roles, and every write went to the role the tag says it belongs to."

step "6. the carve begins with a git move, and Bob's attempt at the retag is denied"
explain \
  "The data module moves from app's configuration into a new root, data," \
  "the way any split starts. The ownership write that completes it is" \
  "live-mv -from-estate=app, run in data/: one CreateTags carrying" \
  "tofu-estate=data. Bob runs it first. His grant may create into net" \
  "rather than data, and may act on module.net.* rather than the" \
  "database, so the platform refuses the retag before anything moves."
cmd "mv module \"data\" from app/main.tf to data/main.tf ; choudoufu live-mv -from-estate=app module.data.aws_instance.database module.data.aws_instance.database   # in data/, as bob"
python3 - "$APP/main.tf" "$DATA/main.tf" <<'PYEOF'
import sys
a, d = sys.argv[1], sys.argv[2]
s = open(a).read()
blk = 'module "data" { source = "../modules/data" }\n'
assert blk in s, "no data module block in app"
open(a, 'w').write(s.replace(blk, '').rstrip() + '\n')
open(d, 'w').write(blk)
PYEOF
( cd "$DATA" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "init failed in data"
OUT="$(cd "$DATA" && as_role bob chdf live-mv -from-estate=app module.data.aws_instance.database module.data.aws_instance.database 2>&1 || true)"
printf '%s\n' "$OUT" > "$LOGS/bob-denied.mv"
denied "$OUT" || fail "boundary" "Bob's retag into data was not refused by the platform (full output in $LOGS/bob-denied.mv): $(grep -E 'Error|rewrote|Moved' <<< "$OUT" | head -3)"
refusal_line "$OUT" | evidence
[ "$(tags_of 'module.data.aws_instance.database' | awk '{print $2}')" = "app" ] || fail "boundary" "the database left the estate despite the refusal"
proof "the carve itself was refused, per resource, by the account. A state mv has no such moment; nothing evaluates it."

step "7. Alice completes the carve: one governed tag write"
explain \
  "Same command, Alice's session. Her grant may create into data and" \
  "the request carries tofu-estate=data, so the retag goes through. The" \
  "database never moves; its owner does."
cmd "choudoufu live-mv -from-estate=app module.data.aws_instance.database module.data.aws_instance.database   # in data/, as alice"
OUT="$(cd "$DATA" && as_role alice chdf live-mv -from-estate=app module.data.aws_instance.database module.data.aws_instance.database 2>&1)" || fail "boundary" "Alice's retag into data failed: $(grep -E 'Error|AccessDenied|not authorized' <<< "$OUT" | head -3)"
settle data "module.data.aws_instance.database"
tags_of "module.data.aws_instance.database" | evidence
[ "$(tags_of 'module.data.aws_instance.database' | awk '{print $2}')" = "data" ] || fail "boundary" "the database does not carry tofu-estate=data after Alice's move"
proof "tofu-estate=data, written by the one role a policy lets write it. Where there was one estate there are two, and no state was split."

step "8. both estates plan clean, each under its own role"
explain \
  "Alice plans data; Bob plans app. Each reads only its own estate and" \
  "finds nothing to do. The record app kept for the database is not" \
  "consulted, because its live tag now names another estate."
cmd "choudoufu plan   # in data/ as alice, then in app/ as bob"
for pair in "alice:$DATA:data" "bob:$APP:app"; do
  role="${pair%%:*}"; rest="${pair#*:}"; dir="${rest%%:*}"; label="${rest#*:}"
  OUT="$(cd "$dir" && as_role "$role" chdf plan -input=false -no-color 2>&1)" || fail "boundary" "$label does not plan under $role: $(grep -E 'Error|AccessDenied|not authorized' <<< "$OUT" | head -3)"
  printf '%s\n' "$OUT" > "$LOGS/$label.plan"
  grep -q "No changes." <<< "$OUT" || fail "boundary" "$label does not plan clean under $role (full plan in $LOGS/$label.plan): $(grep -E '^Plan:|will be|Owned and undeclared|UNOWNED' <<< "$OUT" | head -4)"
  echo "$label under $role: No changes." | evidence
done
proof "No changes, twice, each under the role that owns the estate. The boundary moved with a tag write, and both sides agree where it is."

step "9. teardown - each estate by its own destroy"
( cd "$DATA" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "teardown of data failed"
( cd "$APP"  && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "boundary" "teardown of app failed"
proof "both estates are gone, each through its own configuration."

echo "  What you watched: two roles share one estate and are fenced to their"
echo "  halves by a condition on the ownership tag, refused by AWS when they"
echo "  reach across; then one of them carves her half into a new estate with"
echo "  a single governed tag write that the other role's attempt at could not"
echo "  make. In stock every one of those moves is a state edit, and nothing"
echo "  in the account can say no to a state edit or knows it happened."
