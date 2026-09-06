# the-boundary-holds-across-accounts
# CLAIM 19 - The boundary holds across accounts: two AWS accounts under one estate, one client-chosen name in both, and every answer names the account it is about. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/twoaccounts"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
LOGDIR="$SMOKE_WORKROOT/twoaccounts-logs"; mkdir -p "$LOGDIR"

LG_NAME="/smoke-two-accounts/app"
HOME_ACCT="000000000000"
OTHER_ACCT="111111111111"

# The estate. It is claim 16's estate with the second axis swapped: there
# the two aliased provider configurations differ by REGION and share an
# account, here they differ by ACCOUNT and share a region. Everything else
# is deliberately identical, because the claim is that the mechanism does
# not care which of the two it is.
#
#   aws.home / aws.other_account   two aliased provider configurations, one
#                                  account each, same region, against
#                                  floci's single endpoint. The only thing
#                                  that differs between the blocks is the
#                                  credential.
#   the log group pair             ONE client-chosen name declared in both
#                                  accounts. A log group's import identity
#                                  IS its name and DescribeLogGroups is
#                                  scoped to the calling account, so these
#                                  are two distinct live objects answering
#                                  to one import identity - issue #745's
#                                  shape, one account over instead of one
#                                  region over.
#   the vpc pair                   server-assigned: nothing in the
#                                  configuration says which vpc- id it is,
#                                  so each is found in its own account.
#                                  This is the pair the state cache serves
#                                  on -refresh=false.
#
# The account id comes from the access key id: floci reads a 12-digit
# access key id as the account id itself (AccountResolver.resolve, "matching
# LocalStack's multi-account convention"), and a session from
# sts:AssumeRole into another account's role resolves the same way through
# SessionAccountLookup. So an ordinary static-credential provider block is
# all it takes to put one estate in two accounts, and the account id is
# then visible on the wire in SigV4's own credential scope.
write_estate() {
  local other_akid="${1:-$OTHER_ACCT}"
  cat > "$SMOKE_WORK/main.tf" <<TFEOF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "smoke-two-accounts"
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  alias                       = "home"
  region                      = "us-east-1"
  access_key                  = "${HOME_ACCT}"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

provider "aws" {
  alias                       = "other_account"
  region                      = "us-east-1"
  access_key                  = "${other_akid}"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

resource "aws_cloudwatch_log_group" "home" {
  provider          = aws.home
  name              = "${LG_NAME}"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "other_account" {
  provider          = aws.other_account
  name              = "${LG_NAME}"
  retention_in_days = 1
}

resource "aws_vpc" "home" {
  provider   = aws.home
  cidr_block = "10.72.0.0/16"
}

resource "aws_vpc" "other_account" {
  provider   = aws.other_account
  cidr_block = "10.73.0.0/16"
}
TFEOF
}

# awsa runs the AWS CLI AS one of the two accounts. The account id is the
# access key id, so this is the whole of "log in to the other account".
awsa() { local acct="$1"; shift; AWS_ACCESS_KEY_ID="$acct" AWS_SECRET_ACCESS_KEY=test awsl "$@"; }

plan_rf() {
  (cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGDIR/$1.log" "$TOFU" plan -refresh=false -input=false -no-color 2>&1 | grep -v '^discovering:')
}
plan_full() {
  (cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGDIR/$1.log" "$TOFU" plan -input=false -no-color 2>&1 | grep -v '^discovering:')
}
# requests_as counts the signed requests in one debug stream that were
# signed BY one account's credentials. SigV4's credential scope opens with
# the access key id, which here IS the account id, so this attributes work
# to the provider configuration that did it without trusting any counter of
# ours - the same wire-read attribution claim 16 makes by region.
requests_as() { grep -oE "Credential=$2/[0-9]{8}/us-east-1/" "$LOGDIR/$1.log" 2>/dev/null | wc -l | tr -d ' '; }

step "the claim"
explain \
  "The boundary holds across accounts. Claim 16 proves it for two" \
  "regions; this is the same estate with the other axis swapped - two" \
  "ACCOUNTS, one region, one tofu-estate marker, one record store - and" \
  "the reason to run it separately is that a cross-account alias is only" \
  "the same mechanism as a cross-region one if it is measured to be." \
  "The same client-chosen name is declared in both accounts, so two" \
  "distinct live objects answer to one import identity, and neither may" \
  "answer for the other: delete one account's object and that account's" \
  "instance is named, while the other account keeps being served from" \
  "cache. Then lose the cache and the record store outright, and both" \
  "accounts come back from what the cloud itself carries."

write_estate
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID="$HOME_ACCT" AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "1. two accounts, one estate"
explain \
  "First, that there really are two accounts here: sts:GetCallerIdentity" \
  "under each credential, with no choudoufu in the loop. Then one apply" \
  "over both provider configurations, and the AWS CLI reads each account" \
  "directly: two log groups with the SAME name in the SAME region and" \
  "DIFFERENT account-qualified ARNs, each carrying the same tofu-estate" \
  "and its own tofu-address, and each invisible to the other account's" \
  "own listing. That is the answer to 'is this one estate or two' - it is" \
  "one, and the marker says so in both accounts."
cmd "aws sts get-caller-identity (each credential) ; choudoufu apply -auto-approve ; aws logs describe-log-groups (each account)"
ID_HOME="$(awsa "$HOME_ACCT" sts get-caller-identity --query Account --output text)"
ID_OTHER="$(awsa "$OTHER_ACCT" sts get-caller-identity --query Account --output text)"
[ "$ID_HOME" = "$HOME_ACCT" ] || fail "accounts" "aws.home's credential does not answer for account $HOME_ACCT: got [$ID_HOME]"
[ "$ID_OTHER" = "$OTHER_ACCT" ] || fail "accounts" "aws.other_account's credential does not answer for account $OTHER_ACCT: got [$ID_OTHER] - this emulator cannot present two account ids and nothing below would mean anything"
{ echo "aws.home          sts:GetCallerIdentity -> $ID_HOME"; echo "aws.other_account sts:GetCallerIdentity -> $ID_OTHER"; } | evidence

( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "accounts" "init failed"
A1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "accounts" "apply failed: $A1"
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$A1" | grep -oE '[0-9]+')"
[ "$ADDED" = "4" ] || fail "accounts" "the apply built $ADDED resources, not the fixture's 4: $A1"

H_ARN="$(awsa "$HOME_ACCT" logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
O_ARN="$(awsa "$OTHER_ACCT" logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
[ -n "$H_ARN" ] || fail "accounts" "account $HOME_ACCT holds no log group named $LG_NAME"
[ -n "$O_ARN" ] || fail "accounts" "account $OTHER_ACCT holds no log group named $LG_NAME"
[ "$H_ARN" != "$O_ARN" ] || fail "accounts" "both provider configurations returned the same ARN ($H_ARN) - this is one object, not a mirrored pair, and nothing below would mean anything"
case "$H_ARN" in *":$HOME_ACCT:"*) ;; *) fail "accounts" "aws.home's log group is not in account $HOME_ACCT: $H_ARN" ;; esac
case "$O_ARN" in *":$OTHER_ACCT:"*) ;; *) fail "accounts" "aws.other_account's log group is not in account $OTHER_ACCT: $O_ARN" ;; esac
# Both objects are in us-east-1: the region is NOT what keeps them apart.
case "$H_ARN" in *:us-east-1:*) ;; *) fail "accounts" "aws.home's log group is not in us-east-1: $H_ARN" ;; esac
case "$O_ARN" in *:us-east-1:*) ;; *) fail "accounts" "aws.other_account's log group is not in us-east-1: $O_ARN - the two objects have to share a region or this measures claim 16 again" ;; esac
# Each account's own listing sees its own object and nothing of the other's.
if grep -q "$O_ARN" <<< "$(awsa "$HOME_ACCT" logs describe-log-groups --output text)"; then
  fail "accounts" "account $HOME_ACCT's own listing returned the OTHER account's log group ($O_ARN) - the accounts are not separate populations here"
fi
if grep -q "$H_ARN" <<< "$(awsa "$OTHER_ACCT" logs describe-log-groups --output text)"; then
  fail "accounts" "account $OTHER_ACCT's own listing returned the HOME account's log group ($H_ARN) - the accounts are not separate populations here"
fi
H_TAGS="$(awsa "$HOME_ACCT" logs list-tags-for-resource --resource-arn "$H_ARN" --output json)"
O_TAGS="$(awsa "$OTHER_ACCT" logs list-tags-for-resource --resource-arn "$O_ARN" --output json)"
grep -q '"tofu-estate": "smoke-two-accounts"' <<< "$H_TAGS" || fail "accounts" "account $HOME_ACCT's object does not carry the estate marker: $H_TAGS"
grep -q '"tofu-estate": "smoke-two-accounts"' <<< "$O_TAGS" || fail "accounts" "account $OTHER_ACCT's object does not carry the estate marker: $O_TAGS"
grep -q '"tofu-address": "aws_cloudwatch_log_group.home"' <<< "$H_TAGS" || fail "accounts" "account $HOME_ACCT's object does not carry its own address marker: $H_TAGS"
grep -q '"tofu-address": "aws_cloudwatch_log_group.other_account"' <<< "$O_TAGS" || fail "accounts" "account $OTHER_ACCT's object does not carry its own address marker: $O_TAGS"
RECDIR="$SMOKE_WORK/.tofu-records/tofu-records/smoke-two-accounts"
[ -d "$RECDIR" ] || fail "accounts" "no record store appeared for the estate at $RECDIR"
RECS="$(find "$RECDIR" -type f ! -name '.store-sentinel' | wc -l | tr -d ' ')"
{ echo "aws.home          (account $HOME_ACCT) $H_ARN"; echo "aws.other_account (account $OTHER_ACCT) $O_ARN"; echo "one record store, $RECS record(s), both accounts' instances in it"; } | evidence
proof "one estate, two accounts: aws.home and aws.other_account hold two distinct objects answering to the same name $LG_NAME in the same region us-east-1, told apart by account alone, both marked tofu-estate=smoke-two-accounts, both recorded in the one store beside the module."

explain \
  "Now the plan. -refresh=false is the path that serves converged" \
  "instances from the state cache, so it is also the path where one" \
  "account answering for another would be invisible. Both accounts must" \
  "bind, the plan must be empty, and the work must be attributable:" \
  "SigV4's credential scope opens with the access key id, which here is" \
  "the account id itself, so the request count per account comes off the" \
  "wire rather than from a counter of ours."
cmd "choudoufu plan -refresh=false (TF_LOG=debug)"
P_BIND="$(plan_rf bind)" || fail "accounts" "the two-account -refresh=false plan failed"
grep -q "No changes." <<< "$P_BIND" || fail "accounts" "the two-account estate did not plan clean: $P_BIND"
REQ_HOME="$(requests_as bind "$HOME_ACCT")"
REQ_OTHER="$(requests_as bind "$OTHER_ACCT")"
[ "$REQ_HOME" -gt 0 ] || fail "accounts" "no request was signed as account $HOME_ACCT - the aws.home pass did no work"
[ "$REQ_OTHER" -gt 0 ] || fail "accounts" "no request was signed as account $OTHER_ACCT - the aws.other_account pass never reached the account it names, and every account claim below would be vacuous"
HITS_BIND="$(grep -c 'state cache hit' "$LOGDIR/bind.log" || true)"
echo "per-pass requests: aws.home (account $HOME_ACCT) $REQ_HOME, aws.other_account (account $OTHER_ACCT) $REQ_OTHER; $HITS_BIND instance(s) served from the cache" | evidence
grep -E 'No changes\.' <<< "$P_BIND" | head -1 | evidence
proof "both provider configurations bound their own half and the estate planned empty, at $REQ_HOME requests signed as account $HOME_ACCT and $REQ_OTHER signed as account $OTHER_ACCT."

step "2. a delete in one account is seen in that account"
explain \
  "This is issue #745's step across the account axis. The log group in" \
  "account $OTHER_ACCT is deleted out of band with the AWS CLI. Its twin" \
  "in account $HOME_ACCT - same name, same region, same service - is" \
  "untouched and still answers to the same import identity, $LG_NAME. A" \
  "run that keyed its evidence by identity alone, with no notion of" \
  "which provider configuration saw it, would take the home account's" \
  "object as proof that the other account's still exists and report a" \
  "dead instance as unchanged. The next -refresh=false plan must name" \
  "aws_cloudwatch_log_group.other_account, and only it, while the home" \
  "account's half keeps being served from the cache."
cmd "aws --profile other logs delete-log-group ; choudoufu plan -refresh=false"
awsa "$OTHER_ACCT" logs delete-log-group --log-group-name "$LG_NAME" \
  || fail "accounts" "could not delete account $OTHER_ACCT's log group out of band"
GONE="$(awsa "$OTHER_ACCT" logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
[ -z "$GONE" ] || fail "accounts" "account $OTHER_ACCT's log group survived the delete ($GONE) - the drift is not real"
STILL="$(awsa "$HOME_ACCT" logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
[ "$STILL" = "$H_ARN" ] || fail "accounts" "account $HOME_ACCT's object did not survive the delete in the other account: got [$STILL]"
echo "account $OTHER_ACCT: $LG_NAME is gone; account $HOME_ACCT: $STILL still there, same name, same region" | evidence

if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control. The assertion below is that the other account's dead" \
    "instance is NAMED, and #745's symptom was silence - so the direction" \
    "that has to be provable is that this run CAN report the dead" \
    "instance as unchanged. Two changes manufacture exactly that world." \
    "The home account's object loses its ownership markers, because an" \
    "unmarked sighting is the only shape the cache's envelope-vouch arm" \
    "consumes. And aws.other_account's credential is swapped for the home" \
    "account's, so the second pass lists the account the home object" \
    "lives in and sights the same client-chosen name. The record still" \
    "attests the other account's identity, so the cache serves its dead" \
    "instance and the plan reports it unchanged. If the check below is" \
    "scenery, this run passes it anyway."
  cmd "aws logs untag-resource (home) ; point aws.other_account at account $HOME_ACCT ; choudoufu plan -refresh=false"
  awsa "$HOME_ACCT" logs untag-resource --resource-arn "$H_ARN" --tag-keys tofu-estate tofu-address \
    || fail "accounts" "BREAK: could not strip account $HOME_ACCT's markers"
  write_estate "$HOME_ACCT"
  B_OUT="$(plan_rf break)" || fail "accounts" "BREAK: the plan failed outright instead of reporting a stale answer: $B_OUT"
  if grep -q 'aws_cloudwatch_log_group.other_account will be created' <<< "$B_OUT"; then
    fail "accounts" "BREAK: the other account's dead instance was still named, so this control manufactured nothing and step 2's check proves nothing: $B_OUT"
  fi
  grep 'state cache hit for aws_cloudwatch_log_group.other_account' "$LOGDIR/break.log" | sed 's/.*projection: //' | head -1 | evidence
  { echo "the whole change set this plan proposes:"; grep -E '^  # .* will be ' <<< "$B_OUT" || echo "  (nothing)"; } | evidence
  echo "aws_cloudwatch_log_group.other_account is not in it, and its object in account $OTHER_ACCT is gone" | evidence
  proof "caught - with aws.other_account pointed at the account the home object lives in, the deleted instance in account $OTHER_ACCT reads as unchanged and no plan line mentions it. Step 2's check is what stands between that and a silent, dead instance one account over."
  exit 0
fi

P_DEL="$(plan_rf delete)" || fail "accounts" "the post-delete -refresh=false plan failed"
grep -q 'aws_cloudwatch_log_group.other_account will be created' <<< "$P_DEL" \
  || fail "accounts" "the deleted instance in account $OTHER_ACCT ($LG_NAME) did not surface - account $HOME_ACCT's same-named object answered for it, which is the #745 defect across the account axis: $P_DEL"
grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$P_DEL" \
  || fail "accounts" "the plan proposes more than the other account's one instance: $P_DEL"
if grep -q 'aws_cloudwatch_log_group.home will be' <<< "$P_DEL"; then
  fail "accounts" "the home account's instance was dragged into a change by a delete in the other account: $P_DEL"
fi
HITS_VPC_HOME="$(grep -c 'state cache hit for aws_vpc.home' "$LOGDIR/delete.log" || true)"
[ "$HITS_VPC_HOME" -gt 0 ] \
  || fail "accounts" "the home account's converged instances stopped being served from the cache because something was deleted in the other account: $(grep 'state cache hit' "$LOGDIR/delete.log" || echo 'no cache hits at all')"
HITS_LG_HOME="$(grep -c 'state cache hit for aws_cloudwatch_log_group.home' "$LOGDIR/delete.log" || true)"
[ "$HITS_LG_HOME" -gt 0 ] \
  || fail "accounts" "the home account's CONCRETE log group stopped being served from the cache because something was deleted in the other account: $(grep 'state cache hit' "$LOGDIR/delete.log" || echo 'no cache hits at all')"
grep -E 'aws_cloudwatch_log_group.other_account will be created' <<< "$P_DEL" | head -1 | evidence
grep -E 'Plan: 1 to add' <<< "$P_DEL" | head -1 | evidence
grep 'state cache hit for aws_vpc.home' "$LOGDIR/delete.log" | sed 's/.*projection: //' | head -1 | evidence
grep 'state cache hit for aws_cloudwatch_log_group.home' "$LOGDIR/delete.log" | sed 's/.*projection: //' | head -1 | evidence
proof "account $OTHER_ACCT lost $LG_NAME and the plan named that instance, and only that instance; account $HOME_ACCT's identical name in the same region never answered for it, and the home account's own instances - the needs-discovery vpc AND the CONCRETE log group alike - stayed served from the cache."

explain \
  "One ordinary apply puts the other account's half back. Nothing about" \
  "the recovery is account-specific: the run re-creates what its own" \
  "provider configuration could not find."
cmd "choudoufu apply -auto-approve"
A2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "accounts" "the reconverging apply failed: $A2"
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$A2" \
  || fail "accounts" "reconvergence was not exactly one create in the other account: $A2"
BACK="$(awsa "$OTHER_ACCT" logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
[ "$BACK" = "$O_ARN" ] || fail "accounts" "the recreated object is not account $OTHER_ACCT's: got [$BACK], wanted [$O_ARN]"
echo "$BACK" | evidence
proof "one create in account $OTHER_ACCT, nothing in account $HOME_ACCT - the estate is whole again, and the recreated object carries the other account's ARN."

step "3. recovery is a re-run in both accounts at once"
explain \
  "Claim 5 with two accounts. The state cache and the whole record store" \
  "are deleted and the same plan runs again. The change set must be" \
  "empty, because both accounts come back from what the cloud itself" \
  "carries - markers for the server-assigned vpcs, the configuration's" \
  "own names for the client-named log groups - and the sweep has to have" \
  "reached BOTH accounts to do it. If either account's half were" \
  "unreachable with the record gone, this plan would propose creating a" \
  "resource that already exists."
cmd "rm .terraform/choudoufu-cache.tfstate .tofu-records ; choudoufu plan"
rm -f "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate" || fail "accounts" "could not delete the state cache"
rm -rf "$SMOKE_WORK/.tofu-records" || fail "accounts" "could not delete the record store"
if [ -e "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate" ]; then
  fail "accounts" "the state cache is still there; this step would prove nothing"
fi
if [ -e "$SMOKE_WORK/.tofu-records" ]; then
  fail "accounts" "the record store is still there; this step would prove nothing"
fi
P_REC="$(plan_full recover)" || fail "accounts" "the plan after cache and record loss failed"
grep -q "No changes." <<< "$P_REC" \
  || fail "accounts" "with the cache and the record store gone, the plan is not empty - at least one account's instances were not rediscovered: $(grep -E '^  # .* will be ' <<< "$P_REC" || echo "$P_REC")"
REC_HOME="$(requests_as recover "$HOME_ACCT")"
REC_OTHER="$(requests_as recover "$OTHER_ACCT")"
[ "$REC_HOME" -gt 0 ] || fail "accounts" "recovery signed no request as account $HOME_ACCT"
[ "$REC_OTHER" -gt 0 ] || fail "accounts" "recovery signed no request as account $OTHER_ACCT - the other account was never read, so an empty plan here would be luck, not recovery"
echo "cache deleted, record store deleted; recovery read both accounts: $REC_HOME request(s) as $HOME_ACCT, $REC_OTHER as $OTHER_ACCT" | evidence
grep -E 'No changes\.' <<< "$P_REC" | head -1 | evidence
proof "with nothing local left, both accounts were re-derived from the cloud in one run and the plan proposed nothing - no duplicate create in either account."

step "4. teardown"
explain \
  "One destroy removes exactly what the two provider configurations hold" \
  "between them, and each account is then read directly to confirm its" \
  "own half is gone."
cmd "choudoufu destroy -auto-approve ; aws logs describe-log-groups (each account)"
D="$(cd "$SMOKE_WORK" && chdf destroy -auto-approve -input=false -no-color 2>&1)" || fail "accounts" "destroy failed: $D"
grep -qE 'Resources: 4 destroyed' <<< "$D" || fail "accounts" "destroy did not remove exactly the fixture's 4 resources: $(grep -E 'Resources: ' <<< "$D")"
LEFT_HOME="$(awsa "$HOME_ACCT" logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
LEFT_OTHER="$(awsa "$OTHER_ACCT" logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
[ -z "$LEFT_HOME" ] || fail "accounts" "account $HOME_ACCT still holds $LG_NAME after destroy: $LEFT_HOME"
[ -z "$LEFT_OTHER" ] || fail "accounts" "account $OTHER_ACCT still holds $LG_NAME after destroy: $LEFT_OTHER"
grep -E 'Resources: 4 destroyed' <<< "$D" | head -1 | evidence
echo "account $HOME_ACCT: no $LG_NAME; account $OTHER_ACCT: no $LG_NAME" | evidence
proof "one destroy, both accounts emptied of this estate's objects - the estate spanned two accounts on the way out as well as on the way in."
