# secrets-in-ssm-on-real-aws
# CLAIM 40 (aws) - No secret survives unless you let it, or keep it in SSM (REAL AWS, maintainer-run). ~5 min.
#
# This proof: claim 40's secrets = "ssm" steps (6 to 12 of
# no-secret-survives-in-what-the-tool-keeps, which run on the emulator)
# against a real account, where SSM, KMS and S3 are AWS's own and not an
# emulation of them (GitHub issue #2393).
#
# The resource is an aws_elasticache_user, not the emulator scenario's
# aws_db_instance. Its `passwords` argument is sensitive and DescribeUsers
# never returns it (only a password count), so the value exists in exactly
# two places once the apply is done: the user itself, which cannot be asked
# for it, and whatever the tool keeps. That is the property the database's
# master password has. An ElastiCache user costs nothing, needs no cache
# cluster, and is created, modified and deleted in seconds, where the
# smallest database costs money by the hour and takes minutes each way.
#
# Everything the run creates carries the run id: the KMS key and the bucket
# by tag, the user by tag and by name, and the parameters by path
# (/choudoufu/smoke/<run id>). The EXIT trap tears all of it down on any
# exit and ends on one `teardown:` line naming what it removed and anything
# it could not.

SMOKE_WORK="$SMOKE_WORKROOT/secrets-ssm-real"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

[ "${SMOKE_REAL_AWS:-0}" = "1" ] \
  || fail "ssmreal" "this scenario runs against real AWS: it creates a KMS key (scheduled for deletion at the end, 7-day minimum), an S3 bucket, ElastiCache users and SSM parameters in the account your credentials name, and removes them. It is maintainer-run and never starts by itself: set SMOKE_REAL_AWS=1 to run it."
command -v python3 >/dev/null 2>&1 || fail "ssmreal" "python3 is not installed; the record reader needs it"
# shellcheck source=../bucket-iam.sh
. "$SMOKE_DIR/bucket-iam.sh"
real_aws_begin ssmreal

RUN="ssm-$SUFFIX"
SSM_PATH="/choudoufu/smoke/$RUN"
SSM_BUCKET="chdf-smoke-ssm-$SUFFIX"
SSM_KEY=""
BUCKET_MADE=0
RUN_TAG_KEY="choudoufu-smoke-run"
# What the trap has to look at: every estate directory that got as far as
# init, and every ElastiCache user id a configuration in this run names,
# whether or not an apply was ever expected to create it.
ESTATE_DIRS=()
USER_IDS=()

# The value under test, set once, reaching the configuration only through
# TF_VAR_app_password and spelled nowhere on disk, so a hit anywhere under
# the work directory is a file the run wrote it into. ElastiCache wants 16
# to 128 printable characters.
SECRET="smoke-Pl41ntext-EcP4ss-$RANDOM$RANDOM"
export TF_VAR_app_password="$SECRET"

# ssm_real_teardown runs from the EXIT trap, on every exit. It starts with
# errexit and nounset off (#1378), and every step prints its own line and
# records its outcome, so the last line is reached and names what is left.
# Order matters: the estates are destroyed while the key and the bucket
# they read still exist, then anything a failed destroy left is deleted
# directly, then the parameters, the key and the bucket.
ssm_real_teardown() {
  set +e
  set +u
  local d uid names name cleaned="" left="" n batch
  for d in ${ESTATE_DIRS[@]+"${ESTATE_DIRS[@]}"}; do
    if ( cd "$d" && chdf apply -destroy -auto-approve -input=false -no-color ) > "$d.teardown.log" 2>&1; then
      echo "  destroyed estate $(basename "$d")"
      cleaned="$cleaned estate:$(basename "$d")"
    else
      echo "  COULD NOT DESTROY estate $(basename "$d") - its output is in $d.teardown.log; the users are deleted directly below" >&2
    fi
  done
  for uid in ${USER_IDS[@]+"${USER_IDS[@]}"}; do
    if ! aws elasticache describe-users --user-id "$uid" >/dev/null 2>&1; then
      echo "  no ElastiCache user $uid left"
      continue
    fi
    if aws elasticache delete-user --user-id "$uid" >/dev/null 2>&1; then
      echo "  deleted ElastiCache user $uid directly"
      cleaned="$cleaned user:$uid"
    else
      echo "  COULD NOT DELETE ElastiCache user $uid - aws elasticache delete-user --user-id $uid" >&2
      left="$left user:$uid"
    fi
  done
  if ! names="$(aws ssm get-parameters-by-path --path "$SSM_PATH" --recursive --query 'Parameters[].Name' --output text 2>/dev/null)"; then
    echo "  COULD NOT LIST the parameters under $SSM_PATH - list and delete them by hand" >&2
    left="$left parameters:$SSM_PATH"
  else
    n=0; batch=()
    for name in $names; do
      [ "$name" != "None" ] || continue
      batch+=("$name"); n=$((n+1))
      if [ "${#batch[@]}" -ge 10 ]; then
        aws ssm delete-parameters --names "${batch[@]}" >/dev/null 2>&1 \
          || { echo "  COULD NOT DELETE parameters ${batch[*]}" >&2; left="$left parameters:$SSM_PATH"; }
        batch=()
      fi
    done
    if [ "${#batch[@]}" -gt 0 ]; then
      aws ssm delete-parameters --names "${batch[@]}" >/dev/null 2>&1 \
        || { echo "  COULD NOT DELETE parameters ${batch[*]}" >&2; left="$left parameters:$SSM_PATH"; }
    fi
    echo "  $n parameter(s) were left under $SSM_PATH, deletion attempted for each"
    cleaned="$cleaned parameters:$SSM_PATH($n)"
  fi
  if [ -n "$SSM_KEY" ]; then
    if aws kms schedule-key-deletion --key-id "$SSM_KEY" --pending-window-in-days 7 >/dev/null 2>&1; then
      echo "  scheduled KMS key $SSM_KEY for deletion in 7 days"
      cleaned="$cleaned key:$SSM_KEY(pending-deletion)"
    else
      echo "  COULD NOT SCHEDULE deletion of KMS key $SSM_KEY - aws kms schedule-key-deletion --key-id $SSM_KEY --pending-window-in-days 7" >&2
      left="$left key:$SSM_KEY"
    fi
  fi
  if [ "$BUCKET_MADE" = "1" ]; then
    if ! aws s3api head-bucket --bucket "$SSM_BUCKET" >/dev/null 2>&1; then
      echo "  no bucket $SSM_BUCKET to remove"
    else
      empty_bucket "$SSM_BUCKET"
      if aws s3api delete-bucket --bucket "$SSM_BUCKET" >/dev/null 2>&1; then
        echo "  removed bucket $SSM_BUCKET"
        cleaned="$cleaned bucket:$SSM_BUCKET"
      else
        echo "  COULD NOT REMOVE bucket $SSM_BUCKET - empty and delete it by hand" >&2
        left="$left bucket:$SSM_BUCKET"
      fi
    fi
  fi
  echo "teardown: cleaned${cleaned:- nothing}; could not clean${left:- nothing}"
}
trap 'set +e; set +u; ssm_real_teardown; cleanup' EXIT

# ssm_real_up creates this run's customer managed key and its record bucket,
# each tagged with the run id. The bucket satisfies the bucket contract
# (versioning, noncurrent-version expiry, public access blocked), so nothing
# is refused for a reason that has nothing to do with secrets.
ssm_real_up() {
  SSM_KEY="$(aws kms create-key --description "choudoufu smoke $RUN, safe to delete" \
    --tags "TagKey=$RUN_TAG_KEY,TagValue=$RUN" --query KeyMetadata.KeyId --output text)" \
    || fail "ssmreal" "could not create the run's customer managed KMS key"
  [ -n "$SSM_KEY" ] && [ "$SSM_KEY" != "None" ] || fail "ssmreal" "kms create-key returned no key id"
  if [ "$AWS_REGION" = "us-east-1" ]; then
    aws s3api create-bucket --bucket "$SSM_BUCKET" >/dev/null || fail "ssmreal" "could not create the record bucket $SSM_BUCKET"
  else
    aws s3api create-bucket --bucket "$SSM_BUCKET" --create-bucket-configuration "LocationConstraint=$AWS_REGION" >/dev/null \
      || fail "ssmreal" "could not create the record bucket $SSM_BUCKET"
  fi
  BUCKET_MADE=1
  aws s3api put-bucket-tagging --bucket "$SSM_BUCKET" --tagging "TagSet=[{Key=$RUN_TAG_KEY,Value=$RUN}]" \
    || fail "ssmreal" "could not tag $SSM_BUCKET with the run id"
  aws s3api put-bucket-versioning --bucket "$SSM_BUCKET" --versioning-configuration Status=Enabled \
    || fail "ssmreal" "could not turn on versioning on $SSM_BUCKET"
  aws s3api put-bucket-lifecycle-configuration --bucket "$SSM_BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":1}}]}' >/dev/null \
    || fail "ssmreal" "could not set the noncurrent-version lifecycle on $SSM_BUCKET"
  aws s3api put-public-access-block --bucket "$SSM_BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true \
    || fail "ssmreal" "could not block public access on $SSM_BUCKET"
}

# write_user_estate <dir> <estate> <kms_key_id> writes an estate under
# strict { secrets = "ssm" } whose one resource is an ElastiCache user with
# a password from TF_VAR_app_password, and registers the user id with the
# trap before anything can create it.
write_user_estate() {
  local dir="$1" estate="$2" key="$3" uid
  uid="$(user_id_of "$estate")"
  USER_IDS+=("$uid")
  mkdir -p "$dir"
  cat > "$dir/main.tf" <<TFEOF
terraform {
  live {
    estate = "$estate"
    record_store "s3" {
      bucket = "$SSM_BUCKET"
      region = "$AWS_REGION"
    }
    strict {
      secrets = "ssm"
      ssm {
        kms_key_id = "$key"
        path       = "$SSM_PATH"
      }
    }
  }
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {}

variable "app_password" {
  type = string
}

resource "aws_elasticache_user" "app" {
  user_id       = "$uid"
  user_name     = "$uid"
  engine        = "redis"
  access_string = "on ~smoke:* -@all +get"
  passwords     = [var.app_password]
  tags = {
    "$RUN_TAG_KEY" = "$RUN"
  }
}
TFEOF
}

# user_id_of <estate>: the ElastiCache user id an estate's configuration
# names. ElastiCache wants a letter first, then letters, digits and hyphens.
user_id_of() { printf 'chdf-%s-%s\n' "$1" "$SUFFIX"; }

# user_status <user id>: the user's status, or "none" when there is no such
# user.
user_status() {
  aws elasticache describe-users --user-id "$1" --query 'Users[0].Status' --output text 2>/dev/null || echo none
}

# init_estate <dir> <log name>: init, kept in a log, and registered with
# the trap only once init has succeeded, because a destroy needs it.
init_estate() {
  logged "$2" "ssmreal" "init of $(basename "$1") failed" -- in_dir "$1" chdf init -input=false -no-color
  ESTATE_DIRS+=("$1")
}

# scan_kept <dir> prints every file under <dir> that holds the value, one
# per line, relative to <dir>: the copies of the bucket's records, the data
# dir and any cache in it, the lock file, the configuration. The provider
# plugins init downloaded before the value reached any run are left out.
scan_kept() {
  local dir="$1" v="${2:-$SECRET}"
  find "$dir" -type f -not -path '*/.terraform/providers/*' -print0 \
    | xargs -0 grep -l -- "$v" 2>/dev/null | sed "s|^$dir/||" || true
}
files_kept() {
  find "$1" -type f -not -path '*/.terraform/providers/*' | wc -l | tr -d ' '
}
record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }

# bucket_copy <estate> <dir> downloads every version of every object the
# bucket holds under the estate's prefix into <dir>, one file per version,
# and prints how many. A superseded record version that held a value would
# be a copy the tool kept, so noncurrent versions are read too.
bucket_copy() {
  local estate="$1" out="$2" n=0 key ver listing
  mkdir -p "$out"
  listing="$(aws s3api list-object-versions --bucket "$SSM_BUCKET" --prefix "tofu-records/$estate/" \
    --query 'Versions[].[Key,VersionId]' --output text)" || fail "ssmreal" "could not list the object versions in $SSM_BUCKET"
  while IFS=$'\t' read -r key ver; do
    [ -n "$key" ] && [ "$key" != "None" ] || continue
    n=$((n+1))
    aws s3api get-object --bucket "$SSM_BUCKET" --key "$key" --version-id "$ver" "$out/$n-$(basename "$key")" >/dev/null \
      || fail "ssmreal" "could not read $key version $ver from $SSM_BUCKET"
  done <<< "$listing"
  echo "$n"
}

# current_record <estate> <file> downloads the current version of the
# estate's record for aws_elasticache_user.app into <file>, or fails.
current_record() {
  aws s3api get-object --bucket "$SSM_BUCKET" \
    --key "tofu-records/$1/aws_elasticache_user/$(record_key aws_elasticache_user.app)" "$2" >/dev/null 2>&1 \
    || fail "ssmreal" "no record for aws_elasticache_user.app of $1 in $SSM_BUCKET"
}

# record_refs <record file> prints one line per secret_refs entry:
# "<in> <attribute or -> <parameter>".
record_refs() {
  python3 - "$1" <<'PYEOF'
import json, sys
env = json.load(open(sys.argv[1]))
for r in env.get("secret_refs") or []:
    print(r.get("in", "?"), r.get("attribute") or "-", r.get("parameter", ""))
PYEOF
}

# param_is <decrypted value> <want>: the parameter holds the attribute's
# value as JSON, which for `passwords` is a set of one string. Succeeds
# when it is exactly that set, or exactly that string.
param_is() {
  python3 - "$1" "$2" <<'PYEOF'
import json, sys
v = json.loads(sys.argv[1])
sys.exit(0 if v == [sys.argv[2]] or v == sys.argv[2] else 1)
PYEOF
}

# ssm_params prints every parameter name under the run's path.
ssm_params() {
  aws ssm get-parameters-by-path --path "$SSM_PATH" --recursive --query 'Parameters[].Name' --output text \
    | tr '\t' '\n' | grep -v '^None$' | grep . || true
}

step "the claim"
explain \
  "Claim 40's ssm steps run on the emulator in" \
  "no-secret-survives-in-what-the-tool-keeps. This is the same arc on a" \
  "real account: SSM's own key refused by name before anything is" \
  "created, then the estate applied under strict { secrets = \"ssm\" }" \
  "with a customer managed key the run creates, every version of every" \
  "record in the bucket grepped for the plaintext, the parameter read back" \
  "as a SecureString under that key, a clean replan, and a rotation that" \
  "deletes the parameter it superseded. The secret is an ElastiCache" \
  "user's password, which AWS never returns."

step "1. a key, a bucket and a path of this run's own"
ssm_real_up
aws kms describe-key --key-id "$SSM_KEY" --query 'KeyMetadata.[KeyManager,KeySpec,KeyState]' --output text \
  | sed "s/^/key ...${SSM_KEY: -8}: /" | evidence
echo "run id: $RUN, parameter path: $SSM_PATH, record bucket: $SSM_BUCKET, region: $AWS_REGION" | evidence
proof "a customer managed key and a record bucket, both tagged $RUN_TAG_KEY=$RUN."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the same grep over an ssm record that also holds the value"
  explain \
    "Zero hits in step 4 proves nothing unless the same scan finds the" \
    "value in a record that has it. The estate applies under" \
    "secrets = \"ssm\" with the run's key and with" \
    "CHOUDOUFU_TEST_SSM_LEAK_PLAINTEXT=1, a test-only switch that makes the" \
    "store write each value into the record as well as into its parameter." \
    "The record still carries its secret_refs, so this is the ssm write" \
    "path, and the step 4 scan over the same files MUST find the password" \
    "in the bucket's copy."
  BSSM="$SMOKE_WORK/ssm-break"
  write_user_estate "$BSSM" "smoke-ssm-break" "$SSM_KEY"
  init_estate "$BSSM" secrets-in-ssm-on-real-aws-break-init
  cmd "CHOUDOUFU_TEST_SSM_LEAK_PLAINTEXT=1 choudoufu apply -auto-approve   # then the same grep"
  BSAPPLY="$(cd "$BSSM" && CHOUDOUFU_TEST_SSM_LEAK_PLAINTEXT=1 chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "ssmreal" "BREAK: apply under ssm with the leak switch failed: $BSAPPLY"
  BSN="$(bucket_copy "smoke-ssm-break" "$BSSM/bucket-copy")"
  BSREC="$SMOKE_WORK/ssm-break-record.json"
  current_record "smoke-ssm-break" "$BSREC"
  [ -n "$(record_refs "$BSREC")" ] || fail "ssmreal" "BREAK: the record carries no secret_refs, so it was not written by the ssm store and the control is about a different write path: $(cat "$BSREC")"
  BSHITS="$(scan_kept "$BSSM")"
  echo "object versions copied from the bucket: $BSN" | evidence
  echo "files holding the password: $(tr '\n' ' ' <<< "${BSHITS:-none}")" | evidence
  grep -q '^bucket-copy/' <<< "$BSHITS" \
    || fail "ssmreal" "BREAK: the ssm record held the password and the scan did not find it in the bucket's copy, so the zero hits in step 4 were never evidence of anything"
  proof "caught - the same scan finds the password in the bucket's copy of an ssm record that holds it, so the zero in step 4 is a real zero."
  exit 0
fi

step "2. an AWS managed key is refused by name, before anything is created"
explain \
  "SSM's own key, alias/aws/ssm, can be used by every principal holding" \
  "ssm:GetParameter. The lint refuses it before the plan reaches AWS, so" \
  "afterwards there must be no user, no record and no parameter."
SSMD="$SMOKE_WORK/ssm"
write_user_estate "$SSMD" "smoke-ssm" alias/aws/ssm
init_estate "$SSMD" secrets-in-ssm-on-real-aws-init
cmd "choudoufu apply -auto-approve   # ssm { kms_key_id = \"alias/aws/ssm\" }"
ACODE=0
AOUT="$(cd "$SSMD" && chdf apply -auto-approve -input=false -no-color 2>&1)" || ACODE=$?
[ "$ACODE" != "0" ] || fail "ssmreal" "the apply went through with kms_key_id = \"alias/aws/ssm\": $AOUT"
AFLAT="$(tr '\n' ' ' <<< "$AOUT" | tr -s ' ')"
grep -qF -- 'kms_key_id "alias/aws/ssm" is an AWS managed key by its alias' <<< "$AFLAT" \
  || fail "ssmreal" "the AWS managed key is not refused by name: $AOUT"
{ grep -oE 'kms_key_id "alias/aws/ssm" is an AWS managed key by its alias' <<< "$AFLAT" || true; } | head -1 | evidence
echo "exit status: $ACODE" | evidence
UID_MAIN="$(user_id_of smoke-ssm)"
[ "$(user_status "$UID_MAIN")" = "none" ] || fail "ssmreal" "the refused apply created the ElastiCache user $UID_MAIN anyway"
[ -z "$(ssm_params)" ] || fail "ssmreal" "the refused apply left parameters under $SSM_PATH: $(ssm_params | tr '\n' ' ')"
aws s3api head-object --bucket "$SSM_BUCKET" --key "tofu-records/smoke-ssm/aws_elasticache_user/$(record_key aws_elasticache_user.app)" >/dev/null 2>&1 \
  && fail "ssmreal" "the refused apply wrote a record for aws_elasticache_user.app"
proof "alias/aws/ssm refused by name, exit $ACODE, with no user, no record and no parameter."

step "3. the same estate applies with the run's customer managed key"
explain \
  "The one line changed: kms_key_id names the key this run created. The" \
  "first write checks the key is customer managed and that the caller can" \
  "put, read back and delete a parameter under the path, then creates the" \
  "password's parameter before it commits the record that references it."
write_user_estate "$SSMD" "smoke-ssm" "$SSM_KEY"
cmd "choudoufu apply -auto-approve   # ssm { kms_key_id = \"<the run's key>\" }"
SAPPLY="$(cd "$SSMD" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "ssmreal" "apply under ssm failed: $SAPPLY"
{ grep -E 'Apply complete!' <<< "$SAPPLY" || true; } | evidence
grep -q 'Resources: 1 added' <<< "$SAPPLY" || fail "ssmreal" "the ssm apply did not add exactly the user: $SAPPLY"
USTATUS="$(user_status "$UID_MAIN")"
[ "$USTATUS" = "active" ] || fail "ssmreal" "the ElastiCache user is not active after the apply: $USTATUS"
aws elasticache describe-users --user-id "$UID_MAIN" --query 'Users[0].Authentication.[Type,PasswordCount]' --output text \
  | sed 's/^/user authentication: /' | evidence
grep -qF -- "$SECRET" <<< "$SAPPLY" && fail "ssmreal" "the ssm apply printed the password to the terminal"
proof "one ElastiCache user created with the password set, and the run never printed it."

step "4. grep everything the run kept, every version in the bucket included"
explain \
  "Every version of every object under the estate's prefix is downloaded" \
  "into the estate's directory and grepped by value with the data dir, the" \
  "lock file and the configuration. The current record must exist, carry" \
  "the user's id and hold secret_refs, or a grep over nothing would pass" \
  "forever."
cmd "aws s3api get-object ... (every version under tofu-records/smoke-ssm/) ; grep -rl \"\$TF_VAR_app_password\" ."
SREC="$SMOKE_WORK/ssm-record.json"
current_record "smoke-ssm" "$SREC"
grep -qF -- "$UID_MAIN" "$SREC" || fail "ssmreal" "the record for aws_elasticache_user.app does not carry the user's id, so it is not the record this step claims to have read: $(cat "$SREC")"
SREFS="$(record_refs "$SREC")"
[ -n "$SREFS" ] || fail "ssmreal" "the record holds no secret_refs, so nothing was moved to Parameter Store: $(cat "$SREC")"
[ -e "$SSMD/.terraform/choudoufu-cache.tfstate" ] && fail "ssmreal" "the state cache was written under ssm"
SVERS="$(bucket_copy "smoke-ssm" "$SSMD/bucket-copy")"
[ "$SVERS" -ge 1 ] || fail "ssmreal" "no object version was copied from the bucket, so the scan would read no record"
SHITS="$(scan_kept "$SSMD")"
echo "files read: $(files_kept "$SSMD"), among them $SVERS object version(s) from the bucket; no cache file exists" | evidence
awk '{print "secret_refs: " $1 " " $2 " -> " $3}' <<< "$SREFS" | sed "s|$SSM_PATH|<path>|" | evidence
echo "files holding the password: ${SHITS:-none}" | evidence
[ -z "$SHITS" ] || fail "ssmreal" "the plaintext password is in what the run kept under ssm:
$SHITS"
proof "zero copies of the password in the bucket's records, the data dir, the lock file or the configuration; the record holds secret_refs and no cache file exists."

step "5. the parameters are SecureString under the run's key, and decrypt to the value"
explain \
  "Each reference names a parameter under the run's path. Each must be a" \
  "SecureString encrypted under the run's customer managed key, not SSM's" \
  "own, and the one for passwords must decrypt to the value the run set." \
  "Nothing else may be under the path: the first write's probe parameter" \
  "is deleted once it has been read back."
cmd "aws ssm describe-parameters ... ; aws ssm get-parameter --with-decryption ..."
PWPARAM=""
while read -r rin rattr rparam; do
  [ -n "$rparam" ] || continue
  case "$rparam" in "$SSM_PATH"/*) ;; *) fail "ssmreal" "the reference $rparam is not under the run's path $SSM_PATH" ;; esac
  PMETA="$(aws ssm describe-parameters --parameter-filters "Key=Name,Values=$rparam" --query 'Parameters[0].[Type,KeyId]' --output text 2>/dev/null || true)"
  PTYPE="$(cut -f1 <<< "$PMETA")"; PKEY="$(cut -f2 <<< "$PMETA")"
  [ "$PTYPE" = "SecureString" ] || fail "ssmreal" "the parameter $rparam is $PTYPE, not a SecureString"
  case "$PKEY" in "$SSM_KEY"|*"key/$SSM_KEY") ;; *) fail "ssmreal" "the parameter $rparam is encrypted under $PKEY, not the run's key $SSM_KEY" ;; esac
  echo "$rin $rattr: $PTYPE under key ...${SSM_KEY: -8}" | evidence
  if [ "$rin" = "residue.attributes" ] && [ "$rattr" = "passwords" ]; then PWPARAM="$rparam"; fi
done <<< "$SREFS"
[ -n "$PWPARAM" ] || fail "ssmreal" "no reference is for the passwords attribute: $SREFS"
PWVAL="$(aws ssm get-parameter --name "$PWPARAM" --with-decryption --query Parameter.Value --output text)" \
  || fail "ssmreal" "could not read back the password's parameter $PWPARAM"
param_is "$PWVAL" "$SECRET" || fail "ssmreal" "the password's parameter does not decrypt to the value the run set"
echo "passwords parameter decrypts to the value set in TF_VAR_app_password" | evidence
NPARAMS="$(ssm_params | grep -c . || true)"
[ "$NPARAMS" = "$(grep -c . <<< "$SREFS")" ] || fail "ssmreal" "$NPARAMS parameter(s) under $SSM_PATH and $(grep -c . <<< "$SREFS") reference(s); one of them is an orphan or a probe left behind: $(ssm_params | tr '\n' ' ')"
proof "$NPARAMS SecureString parameter(s) under the run's key, one per reference, and the password's decrypts to the value."

step "6. the replan reads the password back"
explain \
  "AWS never returns the password, so without a kept value the plan would" \
  "have nothing to compare against. Under ssm it reads the value back from" \
  "Parameter Store, so the unchanged estate plans No changes."
cmd "choudoufu plan"
SPLAN="$(cd "$SSMD" && chdf plan -input=false -no-color 2>&1)" || fail "ssmreal" "the replan under ssm failed: $SPLAN"
grep -q 'No changes.' <<< "$SPLAN" || fail "ssmreal" "the unchanged estate under ssm does not plan clean, so the password was not read back from its parameter: $SPLAN"
grep -E 'No changes\.' <<< "$SPLAN" | head -1 | evidence
grep -qF -- "$SECRET" <<< "$SPLAN" && fail "ssmreal" "the replan printed the password"
proof "No changes.: the password came back from its parameter, and the plan did not print it."

step "7. a rotation replaces the parameter and deletes the one it superseded"
explain \
  "The password changes in TF_VAR_app_password. The apply sends it with" \
  "ModifyUser, writes the new value to a new parameter, commits the record" \
  "that references it, and only then deletes the parameter the record used" \
  "to reference. The old name must be gone, the new one must decrypt to" \
  "the new value, and neither value may be in what the run kept."
ROTATED="$SECRET-rotated"
cmd "TF_VAR_app_password=<rotated> choudoufu apply -auto-approve"
RAPPLY="$(cd "$SSMD" && TF_VAR_app_password="$ROTATED" chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "ssmreal" "the rotation apply under ssm failed: $RAPPLY"
grep -q 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RAPPLY" || fail "ssmreal" "the rotation did not update the user in place: $RAPPLY"
current_record "smoke-ssm" "$SREC"
SREFS2="$(record_refs "$SREC")"
NEWPW="$(awk '$1 == "residue.attributes" && $2 == "passwords" {print $3}' <<< "$SREFS2")"
[ -n "$NEWPW" ] || fail "ssmreal" "after the rotation the record has no reference for passwords: $SREFS2"
[ "$NEWPW" != "$PWPARAM" ] || fail "ssmreal" "the rotated password kept the old parameter name $PWPARAM"
aws ssm get-parameter --name "$PWPARAM" >/dev/null 2>&1 \
  && fail "ssmreal" "the superseded parameter $PWPARAM is still there after the record moved to $NEWPW"
NEWVAL="$(aws ssm get-parameter --name "$NEWPW" --with-decryption --query Parameter.Value --output text)" \
  || fail "ssmreal" "could not read back the rotated password's parameter $NEWPW"
param_is "$NEWVAL" "$ROTATED" || fail "ssmreal" "the new parameter does not decrypt to the rotated value"
NPARAMS2="$(ssm_params | grep -c . || true)"
[ "$NPARAMS2" = "$(grep -c . <<< "$SREFS2")" ] || fail "ssmreal" "$NPARAMS2 parameter(s) under $SSM_PATH after the rotation and $(grep -c . <<< "$SREFS2") reference(s): $(ssm_params | tr '\n' ' ')"
echo "passwords parameter: ...${PWPARAM: -12} deleted, ...${NEWPW: -12} decrypts to the rotated value" | evidence
echo "parameters under the path: $NPARAMS2, references: $(grep -c . <<< "$SREFS2")" | evidence
rm -rf "$SSMD/bucket-copy"
bucket_copy "smoke-ssm" "$SSMD/bucket-copy" >/dev/null
for v in "$SECRET" "$ROTATED"; do
  VHITS="$(scan_kept "$SSMD" "$v")"
  [ -z "$VHITS" ] || fail "ssmreal" "after the rotation a password is in what the run kept under ssm:
$VHITS"
done
RPLAN="$(cd "$SSMD" && TF_VAR_app_password="$ROTATED" chdf plan -input=false -no-color 2>&1)" || fail "ssmreal" "the replan after the rotation failed: $RPLAN"
grep -q 'No changes.' <<< "$RPLAN" || fail "ssmreal" "after the rotation the estate does not plan clean with the rotated value: $RPLAN"
grep -E 'No changes\.' <<< "$RPLAN" | head -1 | sed 's/^/rotated: /' | evidence
proof "the rotated password went to a new parameter, the superseded one is deleted, the next plan reads the new value back, and neither value is in the bucket or on disk."

step "8. teardown"
explain \
  "Destroying the estate deletes the user, its record and then the" \
  "parameters the record referenced, so nothing may be left under the" \
  "run's path. The EXIT trap then schedules the key for deletion, removes" \
  "the bucket, and prints one teardown line."
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SSMD" && TF_VAR_app_password="$ROTATED" chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "ssmreal" "teardown of the estate failed: $DOUT"
destroyed_exactly "ssmreal" 1 "$DOUT"
ESTATE_DIRS=()
[ "$(user_status "$UID_MAIN")" = "none" ] || fail "ssmreal" "the ElastiCache user $UID_MAIN is still there after the destroy"
LEFT="$(ssm_params)"
echo "parameters left under the run's path: $(grep -c . <<< "$LEFT" || true)" | evidence
[ -z "$LEFT" ] || fail "ssmreal" "the destroy left parameters under $SSM_PATH: $(tr '\n' ' ' <<< "$LEFT")"
proof "the user is gone and no parameter is left under the run's path; the key and the bucket go in the trap."

echo "  What you watched, on a real account: SSM's own key refused by name"
echo "  with nothing created; an ElastiCache user's password applied under"
echo "  strict { secrets = \"ssm\" } with the run's customer managed key; every"
echo "  record version in the bucket holding references and no copy of it; a"
echo "  SecureString under that key that decrypts to it; a clean replan; and a"
echo "  rotation that deleted the parameter it superseded."
