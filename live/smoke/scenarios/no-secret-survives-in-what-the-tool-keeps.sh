# no-secret-survives-in-what-the-tool-keeps
# CLAIM 40 (aws) - No secret survives unless you let it, or keep it in SSM. ~5 min.
#
# This proof: No secret the run generates or sets survives in what the tool
# keeps: under strict { secrets = "refuse" } a random_password and an
# aws_iam_access_key are refused by name, and a database's master password
# is in no file the run keeps on its own, while the default keeps it in the
# record and the cache the way a stock state file does. Under
# strict { secrets = "ssm" } the same estate keeps its records in a bucket
# and the password in a SecureString parameter under a customer managed key:
# the records hold references, and no copy of the value is anywhere else.

SMOKE_WORK="$SMOKE_WORKROOT/secrets"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

# The value under test. It is set once here, reaches the configuration only
# through the environment (TF_VAR_db_password), and is what every grep below
# looks for BY VALUE. Nothing in the work directory spells it, so a hit
# anywhere under that directory is a file the run wrote it into.
SECRET="smoke-Pl41ntext-DbP4ss-$RANDOM$RANDOM"
export TF_VAR_db_password="$SECRET"

# provider_block writes the provider wiring shared by every estate below.
provider_block() {
  cat <<'TFEOF'
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
TFEOF
}

# write_db_estate <dir> <estate> <strict-lines> writes an estate whose one
# resource is a database with a master password: the settable secret the
# API never returns. `password` is marked sensitive in the provider's own
# schema, so the plan renders it as (sensitive value) under either setting;
# the question is where the VALUE goes after the apply.
write_db_estate() {
  local dir="$1" estate="$2" strict="$3"
  mkdir -p "$dir"
  {
    echo 'terraform {'
    echo '  live {'
    echo "    estate = \"$estate\""
    [ -z "$strict" ] || printf '%s\n' "$strict"
    echo '  }'
    provider_block
    cat <<'TFEOF'

variable "db_password" {
  type = string
}

resource "aws_db_instance" "app" {
  identifier          = "ESTATE-app"
  engine              = "postgres"
  engine_version      = "16.3"
  instance_class      = "db.t3.micro"
  allocated_storage   = 20
  username            = "app"
  password            = var.db_password
  skip_final_snapshot = true
}
TFEOF
  } > "$dir/main.tf"
  sed_i "$dir/main.tf" "s/ESTATE-app/$estate-app/"
}

# scan_kept <dir> prints every file under <dir> that holds the value, one
# per line, relative to <dir>. It reads what a run writes on its own: the
# record store, the data dir with its cache (when there is one), the lock
# file, the configuration. Three things are left out, each by name: the
# provider plugins init downloaded before the value reached any run, and
# the two artifacts an operator asks for by flag - a saved plan (-out) and
# a TF_LOG file - which step 4 reads separately, because what they hold is
# a different claim.
scan_kept() {
  local dir="$1"
  find "$dir" -type f -not -path '*/.terraform/providers/*' \
    -not -name '*.tfplan' -not -name '*-debug.log' -print0 \
    | xargs -0 grep -l -- "$SECRET" 2>/dev/null | sed "s|^$dir/||" || true
}
files_kept() {
  find "$1" -type f -not -path '*/.terraform/providers/*' \
    -not -name '*.tfplan' -not -name '*-debug.log' | wc -l | tr -d ' '
}
# planfile_members <planfile> prints each member of the saved plan (a zip
# archive) that holds the value. A grep over the archive itself reads
# compressed bytes and finds nothing whatever is inside, so the members are
# read one by one.
planfile_members() {
  python3 - "$1" "$SECRET" <<'PYEOF'
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
for n in z.namelist():
    if sys.argv[2].encode() in z.read(n):
        print(n)
PYEOF
}
record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }

# --- strict { secrets = "ssm" } (GitHub issue #2393) ---
#
# Under "ssm" the records live in an S3 bucket and each secret value goes to
# a SecureString parameter under a customer managed key, with the record
# holding a reference. Everything below is named by this run: the key is
# tagged with the run id, the parameters go under a path of the run's own,
# and the bucket carries the run id in its name, so a run never reads or
# deletes another run's.
SSM_RUN="secrets-$SMOKE_ID"
SSM_PATH="/choudoufu/smoke/$SSM_RUN"
SSM_BUCKET="smoke-secrets-ssm-$SMOKE_ID"
SSM_KEY=""

# ssm_up creates this run's customer managed key and its record bucket, once.
# The bucket satisfies the bucket contract (versioning, noncurrent-version
# expiry, public access blocked), so nothing is refused for a reason that has
# nothing to do with secrets.
ssm_up() {
  [ -z "$SSM_KEY" ] || return 0
  SSM_KEY="$(awsl kms create-key --description "choudoufu smoke $SSM_RUN" \
    --tags "TagKey=choudoufu-smoke-run,TagValue=$SSM_RUN" --query KeyMetadata.KeyId --output text)" \
    || fail "secrets" "could not create the run's customer managed KMS key"
  [ -n "$SSM_KEY" ] && [ "$SSM_KEY" != "None" ] || fail "secrets" "kms create-key returned no key id"
  awsl s3api create-bucket --bucket "$SSM_BUCKET" >/dev/null || fail "secrets" "could not create the record bucket $SSM_BUCKET"
  awsl s3api put-bucket-versioning --bucket "$SSM_BUCKET" --versioning-configuration Status=Enabled >/dev/null \
    || fail "secrets" "could not turn on versioning on $SSM_BUCKET"
  awsl s3api put-bucket-lifecycle-configuration --bucket "$SSM_BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null \
    || fail "secrets" "could not set the noncurrent-version lifecycle on $SSM_BUCKET"
  awsl s3api put-public-access-block --bucket "$SSM_BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null \
    || fail "secrets" "could not block public access on $SSM_BUCKET"
}

# ssm_block <kms_key_id> prints the live block lines write_db_estate takes for
# an estate under "ssm": the record bucket and the strict block with its ssm
# block.
ssm_block() {
  cat <<TFEOF
    record_store "s3" {
      bucket = "$SSM_BUCKET"
    }
    strict {
      secrets = "ssm"
      ssm {
        kms_key_id = "$1"
        path       = "$SSM_PATH"
      }
    }
TFEOF
}

# bucket_copy <estate> <dir> downloads every version of every object the
# bucket holds under the estate's prefix into <dir>, one file per version,
# and prints how many. Current and noncurrent alike: a superseded record
# version that held a value would be a copy the tool kept. scan_kept over the
# estate's directory then reads the bucket's records with everything else.
bucket_copy() {
  local estate="$1" out="$2" n=0 key ver listing
  mkdir -p "$out"
  listing="$(awsl s3api list-object-versions --bucket "$SSM_BUCKET" --prefix "tofu-records/$estate/" \
    --query 'Versions[].[Key,VersionId]' --output text)" || fail "secrets" "could not list the object versions in $SSM_BUCKET"
  while IFS=$'\t' read -r key ver; do
    [ -n "$key" ] && [ "$key" != "None" ] || continue
    n=$((n+1))
    awsl s3api get-object --bucket "$SSM_BUCKET" --key "$key" --version-id "$ver" "$out/$n-$(basename "$key")" >/dev/null \
      || fail "secrets" "could not read $key version $ver from $SSM_BUCKET"
  done <<< "$listing"
  echo "$n"
}

# current_record <estate> <file> downloads the current version of the
# estate's record for aws_db_instance.app into <file>, or fails naming it.
current_record() {
  awsl s3api get-object --bucket "$SSM_BUCKET" \
    --key "tofu-records/$1/aws_db_instance/$(record_key aws_db_instance.app)" "$2" >/dev/null 2>&1 \
    || fail "secrets" "no record for aws_db_instance.app of $1 in $SSM_BUCKET"
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

# ssm_params prints every parameter name under the run's path.
ssm_params() {
  awsl ssm get-parameters-by-path --path "$SSM_PATH" --recursive --query 'Parameters[].Name' --output text \
    | tr '\t' '\n' | grep -v '^None$' | grep . || true
}

step "the claim"
explain \
  "HANDOFF's first principle: no secrets stored by the tool. A stock state" \
  "file keeps two kinds of secret in clear - values a resource generates" \
  "(a random_password's result, an access key's secret) and values you set" \
  "that the API never gives back (a database's master password). The" \
  "record store keeps the same two by default, because the default is" \
  "compatible with stock. Under strict { secrets = \"refuse\" } the" \
  "generating types are refused by name and the settable secret is left" \
  "out of its record, and the one artifact allowed to hold attribute" \
  "material, the disposable state cache, is not written. This scenario" \
  "applies under that setting, then greps everything the run wrote for" \
  "the plaintext by value. Then it runs the default and says what that" \
  "keeps, without softening it."

step "1. a secret-generating type is refused by name"
explain \
  "An estate with a random_password and an aws_iam_access_key, the two" \
  "shapes whose whole value exists only in whatever the tool keeps. Under" \
  "refuse the plan must stop before anything is created, naming each" \
  "resource and the setting that refused it - and it must leave nothing" \
  "behind: no record, no cache."
GEN="$SMOKE_WORK/generated"; mkdir -p "$GEN"
{
  cat <<'TFEOF'
terraform {
  live {
    estate = "smoke-secrets-gen"
    strict {
      secrets = "refuse"
    }
  }
TFEOF
  provider_block
  cat <<'TFEOF'

resource "random_password" "db" {
  length = 16
}

resource "aws_iam_user" "deploy" {
  name = "smoke-secrets-deploy"
}

resource "aws_iam_access_key" "deploy" {
  user = aws_iam_user.deploy.name
}
TFEOF
} > "$GEN/main.tf"
cmd "choudoufu init && choudoufu plan   # strict { secrets = \"refuse\" }"
logged no-secret-survives-in-what-the-tool-keeps-gen-init "secrets" "init of the generating estate failed" -- in_dir "$GEN" chdf init -input=false -no-color
GCODE=0
GOUT="$(cd "$GEN" && chdf plan -input=false -no-color 2>&1)" || GCODE=$?
[ "$GCODE" != "0" ] || fail "secrets" "the plan went through with a random_password and an aws_iam_access_key under secrets = \"refuse\": $GOUT"
GFLAT="$(tr '\n' ' ' <<< "$GOUT" | tr -s ' ')"
# Each refusal is checked by its own words, so one resource's message cannot
# stand in for the other's: the random_password comes from the logical-type
# rule, the access key from the record-located route's own refusal.
grep -qF -- 'random_password.db: "random_password" is a logical resource, classified SECRET_REFUSED' <<< "$GFLAT" \
  || fail "secrets" "the random_password is not refused as SECRET_REFUSED by name: $GOUT"
grep -qF -- 'aws_iam_access_key.deploy: "aws_iam_access_key" generates secret material' <<< "$GFLAT" \
  || fail "secrets" "the aws_iam_access_key is not refused by name as generating secret material: $GOUT"
[ "$(grep -oF -- 'strict { secrets = "refuse" }' <<< "$GFLAT" | wc -l | tr -d ' ')" -ge 2 ] \
  || fail "secrets" "the two refusals do not both name the setting: $GOUT"
grep -E '^(random_password\.db|aws_iam_access_key\.deploy): ' <<< "$GOUT" | cut -c1-110 | evidence
echo "exit status: $GCODE" | evidence
[ ! -e "$GEN/.tofu-records" ] || fail "secrets" "a refused plan wrote a record store: $(find "$GEN/.tofu-records" -type f)"
[ ! -e "$GEN/.terraform/choudoufu-cache.tfstate" ] || fail "secrets" "a refused plan wrote the state cache"
awsl iam list-users --query 'Users[?UserName==`smoke-secrets-deploy`].UserName' --output text | grep -q . \
  && fail "secrets" "the refused plan created the IAM user anyway"
proof "both generating types refused by name, the setting named, exit $GCODE, and nothing written or created."

step "2. an estate with a settable secret applies under refuse"
explain \
  "The other shape: aws_db_instance with a master password. RDS never" \
  "returns it, so a stock state file is the only place it would ever be" \
  "read back from. The value enters through TF_VAR_db_password alone and" \
  "is spelled nowhere on disk. This is the ordinary run, one plain apply," \
  "so what it leaves behind is what the tool keeps on its own."
REFUSE="$SMOKE_WORK/refuse"
write_db_estate "$REFUSE" "smoke-secrets-refuse" '    strict {
      secrets = "refuse"
    }'
cmd "choudoufu init && choudoufu apply -auto-approve   # TF_VAR_db_password set"
logged no-secret-survives-in-what-the-tool-keeps-refuse-init "secrets" "init of the refuse estate failed" -- in_dir "$REFUSE" chdf init -input=false -no-color
RAPPLY="$(cd "$REFUSE" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "secrets" "apply under refuse failed: $RAPPLY"
{ grep -E 'Apply complete!' <<< "$RAPPLY" || true; } | evidence
grep -q 'Resources: 1 added' <<< "$RAPPLY" || fail "secrets" "the apply did not add exactly the database: $RAPPLY"
DBSTATUS="$(awsl rds describe-db-instances --db-instance-identifier smoke-secrets-refuse-app --query 'DBInstances[0].DBInstanceStatus' --output text 2>/dev/null || echo none)"
[ "$DBSTATUS" = "available" ] || fail "secrets" "the database is not there after the apply: $DBSTATUS"
grep -qF -- "$SECRET" <<< "$RAPPLY" && fail "secrets" "the apply printed the password to the terminal"
proof "one database created with the password set, and the run never printed it."

step "3. grep everything the run kept for the plaintext"
explain \
  "Every file under the working directory except the provider plugins:" \
  "the record store, the data dir, the lock file, the configuration. The" \
  "grep is by value, for the exact string in TF_VAR_db_password. The" \
  "record for the database must exist, or a grep over nothing would pass" \
  "forever, and it must not name the password attribute at all."
cmd "grep -rl \"\$TF_VAR_db_password\" .   # minus .terraform/providers"
RREC="$REFUSE/.tofu-records/tofu-records/smoke-secrets-refuse/aws_db_instance/$(record_key aws_db_instance.app)"
[ -f "$RREC" ] || fail "secrets" "no record was written for aws_db_instance.app under refuse: $(find "$REFUSE/.tofu-records" -type f 2>/dev/null)"
grep -q 'smoke-secrets-refuse-app' "$RREC" || fail "secrets" "the record for aws_db_instance.app does not carry the database's identifier, so it is not the record this step claims to have read: $(cat "$RREC")"
[ -e "$REFUSE/.terraform/choudoufu-cache.tfstate" ] && fail "secrets" "the state cache was written under refuse"
HITS="$(scan_kept "$REFUSE")"
NKEPT="$(files_kept "$REFUSE")"
echo "files read: $NKEPT, among them the record ($(wc -c < "$RREC" | tr -d ' ') bytes); no cache file exists" | evidence
echo "files holding the password: ${HITS:-none}" | evidence
[ -z "$HITS" ] || fail "secrets" "the plaintext password is in what the run kept under refuse:
$HITS"
grep -q 'password' "$RREC" && fail "secrets" "the record names the password attribute at all: $(cat "$RREC")"
proof "$NKEPT files read, zero hold the password: the record holds the database's identity and not its password, and no cache file exists to hold it."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the same grep over a run that keeps the value"
  explain \
    "Zero hits proves nothing if the grep could not find the value in a" \
    "file that has it. So the control is the inverse: the identical" \
    "estate under secrets = \"store\", applied the same plain way, and the" \
    "identical scan over the identical set of files MUST find the" \
    "password. If it finds nothing, the zero above was scenery and this" \
    "run fails."
  BSTORE="$SMOKE_WORK/store-break"
  write_db_estate "$BSTORE" "smoke-secrets-store" '    strict {
      secrets = "store"
    }'
  cmd "choudoufu apply -auto-approve   # strict { secrets = \"store\" } ; then the same grep"
  logged no-secret-survives-in-what-the-tool-keeps-bstore-init "secrets" "BREAK: init of the store estate failed" -- in_dir "$BSTORE" chdf init -input=false -no-color
  BAPPLY="$(cd "$BSTORE" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "secrets" "BREAK: apply under store failed: $BAPPLY"
  BHITS="$(scan_kept "$BSTORE")"
  echo "files holding the password: $(tr '\n' ' ' <<< "${BHITS:-none}")" | evidence
  if [ -z "$BHITS" ]; then
    fail "secrets" "BREAK: the store run kept the password nowhere the scan can see, so the zero hits in step 3 were never evidence of anything"
  fi
  proof "caught - the same scan finds the password in $(grep -c . <<< "$BHITS") file(s) under secrets = \"store\", so the zero under refuse was a real zero."

  step "BREAK control - the same grep over an ssm record that also holds the value"
  explain \
    "The same question for step 8, where the records are in a bucket and" \
    "the scan reads a downloaded copy of every version of them. The estate" \
    "applies under secrets = \"ssm\" with the run's customer managed key," \
    "and with CHOUDOUFU_TEST_SSM_LEAK_PLAINTEXT=1, a test-only switch that" \
    "makes the store write each value into the record as well as into its" \
    "parameter. The record still carries its secret_refs, so this is the" \
    "ssm write path; the step 8 scan over the same files MUST find the" \
    "password in the bucket's copy, or its zero was never evidence."
  ssm_up
  BSSM="$SMOKE_WORK/ssm-break"
  write_db_estate "$BSSM" "smoke-secrets-ssm-break" "$(ssm_block "$SSM_KEY")"
  cmd "CHOUDOUFU_TEST_SSM_LEAK_PLAINTEXT=1 choudoufu apply -auto-approve   # strict { secrets = \"ssm\" } ; then the same grep"
  logged no-secret-survives-in-what-the-tool-keeps-bssm-init "secrets" "BREAK: init of the ssm estate failed" -- in_dir "$BSSM" chdf init -input=false -no-color
  BSAPPLY="$(cd "$BSSM" && CHOUDOUFU_TEST_SSM_LEAK_PLAINTEXT=1 chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "secrets" "BREAK: apply under ssm with the leak switch failed: $BSAPPLY"
  BSN="$(bucket_copy "smoke-secrets-ssm-break" "$BSSM/bucket-copy")"
  BSREC="$SMOKE_WORK/ssm-break-record.json"
  current_record "smoke-secrets-ssm-break" "$BSREC"
  [ -n "$(record_refs "$BSREC")" ] || fail "secrets" "BREAK: the record carries no secret_refs, so it was not written by the ssm store and the control is about a different write path: $(cat "$BSREC")"
  BSHITS="$(scan_kept "$BSSM")"
  echo "object versions copied from the bucket: $BSN" | evidence
  echo "files holding the password: $(tr '\n' ' ' <<< "${BSHITS:-none}")" | evidence
  grep -q '^bucket-copy/' <<< "$BSHITS" \
    || fail "secrets" "BREAK: the ssm record held the password and the scan did not find it in the bucket's copy, so the zero hits in step 8 were never evidence of anything"
  proof "caught - the same scan finds the password in the bucket's copy of an ssm record that holds it, so the zero in step 8 is a real zero."
  ( cd "$BSSM" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  ( cd "$BSTORE" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  ( cd "$REFUSE" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "4. what you can still ask to be written"
explain \
  "Two flags put more on disk: a saved plan (-out) and a debug log" \
  "(TF_LOG=debug). They are files you name, not what the tool keeps, and" \
  "what they hold is stated here rather than left to be found. A fresh" \
  "estate under the same setting, because only the create sends the" \
  "password: its plan is saved and applied under TF_LOG=debug, then the" \
  "plan's zip members and the log are read for the value."
FLAGS="$SMOKE_WORK/flags"
write_db_estate "$FLAGS" "smoke-secrets-flags" '    strict {
      secrets = "refuse"
    }'
cmd "choudoufu plan -out=change.tfplan && TF_LOG=debug TF_LOG_PATH=apply-debug.log choudoufu apply change.tfplan"
logged no-secret-survives-in-what-the-tool-keeps-flags-init "secrets" "init of the flags estate failed" -- in_dir "$FLAGS" chdf init -input=false -no-color
FPLAN="$(cd "$FLAGS" && chdf plan -input=false -no-color -out=change.tfplan 2>&1)" || fail "secrets" "plan -out under refuse failed: $FPLAN"
grep -qF -- "$SECRET" <<< "$FPLAN" && fail "secrets" "the plan printed the password"
FAPPLY="$(cd "$FLAGS" && TF_LOG=debug TF_LOG_PATH="$FLAGS/apply-debug.log" "$TOFU" apply -input=false -no-color change.tfplan 2>&1)" \
  || fail "secrets" "applying the saved plan failed: $FAPPLY"
grep -q 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$FAPPLY" || fail "secrets" "the saved plan did not create the database: $FAPPLY"
PMEMBERS="$(planfile_members "$FLAGS/change.tfplan")" || fail "secrets" "could not read the saved plan as a zip archive"
echo "saved plan, members holding the password: $(tr '\n' ' ' <<< "${PMEMBERS:-none}")" | evidence
[ -n "$PMEMBERS" ] || fail "secrets" "the saved plan holds no copy of the password, yet the apply that consumed it set one; either the plan file is not being read or the value came from elsewhere"
grep -q 'CreateDBInstance' "$FLAGS/apply-debug.log" || fail "secrets" "the debug log never mentions CreateDBInstance, the request that carried the password, so it is not the log this step claims to have read"
LOGHITS="$(grep -cF -- "$SECRET" "$FLAGS/apply-debug.log" || true)"
# A log entry can span lines: the provider prints a request body as
# continuation lines under one timestamped header. So each line holding the
# value is attributed to the header of the entry it belongs to, and every
# such header must be the aws provider plugin's own.
LOGHDRS="$(awk -v s="$SECRET" '/^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T/ {hdr=$0} index($0, s) {print hdr}' "$FLAGS/apply-debug.log")"
LOGNONPROVIDER="$(grep -vF 'provider.terraform-provider-aws' <<< "$LOGHDRS" || true)"
echo "debug log, lines holding the password: $LOGHITS" | evidence
{ grep -oE '\[DEBUG\] provider[^:]*: [A-Za-z ]+' <<< "$LOGHDRS" || true; } | sort -u | sed 's/$/: .../' | evidence
echo "  | $({ grep -F -- "$SECRET" "$FLAGS/apply-debug.log" || true; } | head -1 | sed "s/$SECRET/<the password>/g" \
  | { grep -oE 'Action=[A-Za-z]+|[A-Za-z]*Password=<the password>' || true; } | tr '\n' ' ')" | evidence
[ "$LOGHITS" -gt 0 ] && [ -n "$LOGHDRS" ] || fail "secrets" "no log entry holds the password, so the attribution below has nothing to attribute"
[ -z "$LOGNONPROVIDER" ] || fail "secrets" "an entry outside the provider plugin wrote the password into the debug log:
$(sed "s/$SECRET/<the password>/g" <<< "$LOGNONPROVIDER" | cut -c1-300)"
FKEPT="$(scan_kept "$FLAGS")"
echo "what the tool kept from this run, files holding the password: ${FKEPT:-none}" | evidence
[ -z "$FKEPT" ] || fail "secrets" "the noisy run left the password in what the tool keeps: $FKEPT"
proof "the saved plan carries the value its apply will send, and the provider's debug lines carry the request it sent, as they do on stock; the record store and data dir from the same run hold none of it."

step "4b. the replan under refuse proposes the argument again"
explain \
  "This is what the refusal costs, and use/secrets.md states it: a" \
  "sensitive argument left out of its record shows as a change on every" \
  "plan, the same perpetual diff stock terraform import leaves. Nothing" \
  "the run keeps holds the last-applied password, so the plan has no" \
  "prior to compare against and proposes sending the value again. Two" \
  "plans are run: the password unchanged, then rotated. Both must" \
  "propose the update - a rotation that planned No changes. would never" \
  "be sent, which is what #1503 was. Neither may print the value or" \
  "leave it on disk."
cmd "choudoufu plan   # unchanged, then with TF_VAR_db_password rotated"
RPLAN="$(cd "$REFUSE" && chdf plan -input=false -no-color 2>&1)" || fail "secrets" "the replan under refuse failed: $RPLAN"
{ grep -E '^Plan:|^No changes' <<< "$RPLAN" || true; } | head -1 | evidence
grep -q 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$RPLAN" \
  || fail "secrets" "the replan under refuse does not propose the password again. use/secrets.md says a sensitive argument left out of its record shows as a change on every plan, and the lint warning on this very attribute promises the same: $RPLAN"
grep -q 'aws_db_instance.app will be updated in-place' <<< "$RPLAN" \
  || fail "secrets" "the one proposed change is not the database's: $RPLAN"
grep -qE '^ *[+~] *password *=' <<< "$RPLAN" \
  || fail "secrets" "the proposed change does not name password, so something other than the refused argument moved: $RPLAN"
{ grep -E '^ *[+~] *password *=' <<< "$RPLAN" || true; } | head -1 | evidence
grep -qF -- "$SECRET" <<< "$RPLAN" && fail "secrets" "the replan printed the password"
[ -z "$(scan_kept "$REFUSE")" ] || fail "secrets" "the replan left the password in what the tool keeps"

# The rotation: #1503's own case. The same estate, the same live database,
# one different value in the environment. Under the seeding this replaced
# it read No changes. and the new password was silently never sent.
ROTATED="$SECRET-rotated"
RROT="$(cd "$REFUSE" && TF_VAR_db_password="$ROTATED" chdf plan -input=false -no-color 2>&1)" \
  || fail "secrets" "the rotated replan under refuse failed: $RROT"
{ grep -E '^Plan:|^No changes' <<< "$RROT" || true; } | head -1 | sed 's/^/rotated: /' | evidence
grep -q 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$RROT" \
  || fail "secrets" "a password changed in the configuration is not proposed under refuse, so the rotation would never be sent (GitHub issue #1503): $RROT"
grep -qF -- "$ROTATED" <<< "$RROT" && fail "secrets" "the rotated replan printed the new password"
ROTHITS="$(find "$REFUSE" -type f -not -path '*/.terraform/providers/*' -not -name '*.tfplan' -not -name '*-debug.log' -print0 \
  | xargs -0 grep -l -- "$ROTATED" 2>/dev/null | sed "s|^$REFUSE/||" || true)"
[ -z "$ROTHITS" ] || fail "secrets" "the rotated plan wrote the new password into what the tool keeps:
$ROTHITS"
[ -z "$(scan_kept "$REFUSE")" ] || fail "secrets" "the rotated plan left the old password in what the tool keeps"
proof "both replans propose the password again, the cost use/secrets.md states; the rotated one proposes it too, so a rotation is sent rather than silently dropped. Neither printed a password or left one on disk."

step "5. the default, honestly"
explain \
  "The same estate with no strict block. The default is compatible with" \
  "stock, and stock keeps this password in its state file; so the record" \
  "holds it, and so does the state cache on the machine that applied." \
  "Neither is a secret manager. What the default buys is a converged" \
  "estate that plans No changes. - and the cache is still disposable:" \
  "deleted, the plan is the same, because the record is what remembers." \
  "Claims 3 and 5 prove the run works with that file gone; this step" \
  "shows what the file held while it was there."
STORE="$SMOKE_WORK/store"
write_db_estate "$STORE" "smoke-secrets-store" ""
cmd "choudoufu apply -auto-approve   # no strict block ; grep ; rm the cache ; choudoufu plan"
logged no-secret-survives-in-what-the-tool-keeps-store-init "secrets" "init of the store estate failed" -- in_dir "$STORE" chdf init -input=false -no-color
SAPPLY="$(cd "$STORE" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "secrets" "apply under store failed: $SAPPLY"
grep -q 'Resources: 1 added' <<< "$SAPPLY" || fail "secrets" "the store apply did not add exactly the database: $SAPPLY"
SREC="$STORE/.tofu-records/tofu-records/smoke-secrets-store/aws_db_instance/$(record_key aws_db_instance.app)"
SCACHE="$STORE/.terraform/choudoufu-cache.tfstate"
[ -f "$SREC" ] || fail "secrets" "no record for aws_db_instance.app under store"
[ -f "$SCACHE" ] || fail "secrets" "no state cache under store"
SHITS="$(scan_kept "$STORE")"
echo "files holding the password: $(tr '\n' ' ' <<< "${SHITS:-none}")" | evidence
grep -qF -- "$SECRET" "$SREC" || fail "secrets" "the default did not record the password, which contradicts what the docs say the default keeps: $(cat "$SREC")"
grep -qF -- "$SECRET" "$SCACHE" || fail "secrets" "the default did not put the password in the state cache, which contradicts what the docs say the cache holds"
rm -f "$SCACHE"
SPLAN="$(cd "$STORE" && chdf plan -input=false -no-color 2>&1)" || fail "secrets" "the store replan failed: $SPLAN"
grep -q 'No changes.' <<< "$SPLAN" || fail "secrets" "with the cache deleted the store estate does not plan clean: $SPLAN"
grep -E 'No changes\.' <<< "$SPLAN" | head -1 | evidence
proof "under the default the password is in the record and was in the cache, which is what a stock state file keeps; with the cache deleted the plan is still No changes. The setting is the difference, and it is yours to make."

step "6. secrets = \"ssm\": an AWS managed key is refused by name"
explain \
  "The third setting keeps the value, somewhere built to hold one: the" \
  "records go to an S3 bucket, and each secret value to a SecureString" \
  "parameter in SSM under a customer managed KMS key, with the record" \
  "holding a reference. The same estate, a database whose password comes" \
  "from TF_VAR_db_password, is applied under it from here to step 11. The" \
  "run creates its own key, tagged with the run id, a parameter path of its" \
  "own and a record bucket. First the mistake the setting exists to stop:" \
  "SSM's own key, alias/aws/ssm, which every principal holding" \
  "ssm:GetParameter can use. It must be refused by name before anything is" \
  "created, recorded or put in Parameter Store."
ssm_up
awsl kms describe-key --key-id "$SSM_KEY" --query 'KeyMetadata.[KeyManager,KeySpec,KeyState]' --output text \
  | sed "s/^/key ...${SSM_KEY: -8}: /" | evidence
echo "parameter path: $SSM_PATH, record bucket: $SSM_BUCKET" | evidence
SSMD="$SMOKE_WORK/ssm"
write_db_estate "$SSMD" "smoke-secrets-ssm" "$(ssm_block alias/aws/ssm)"
cmd "choudoufu init && choudoufu apply -auto-approve   # ssm { kms_key_id = \"alias/aws/ssm\" }"
logged no-secret-survives-in-what-the-tool-keeps-ssm-init "secrets" "init of the ssm estate failed" -- in_dir "$SSMD" chdf init -input=false -no-color
ACODE=0
AOUT="$(cd "$SSMD" && chdf apply -auto-approve -input=false -no-color 2>&1)" || ACODE=$?
[ "$ACODE" != "0" ] || fail "secrets" "the apply went through with kms_key_id = \"alias/aws/ssm\": $AOUT"
AFLAT="$(tr '\n' ' ' <<< "$AOUT" | tr -s ' ')"
grep -qF -- 'kms_key_id "alias/aws/ssm" is an AWS managed key by its alias' <<< "$AFLAT" \
  || fail "secrets" "the AWS managed key is not refused by name: $AOUT"
{ grep -oE 'kms_key_id "alias/aws/ssm" is an AWS managed key by its alias' <<< "$AFLAT" || true; } | head -1 | evidence
echo "exit status: $ACODE" | evidence
ADB="$(awsl rds describe-db-instances --db-instance-identifier smoke-secrets-ssm-app --query 'DBInstances[0].DBInstanceStatus' --output text 2>/dev/null || echo none)"
[ "$ADB" = "none" ] || fail "secrets" "the refused apply created the database anyway: $ADB"
[ -z "$(ssm_params)" ] || fail "secrets" "the refused apply left parameters under $SSM_PATH: $(ssm_params | tr '\n' ' ')"
awsl s3api head-object --bucket "$SSM_BUCKET" --key "tofu-records/smoke-secrets-ssm/aws_db_instance/$(record_key aws_db_instance.app)" >/dev/null 2>&1 \
  && fail "secrets" "the refused apply wrote a record for aws_db_instance.app"
proof "alias/aws/ssm refused by name, exit $ACODE, with no database, no record and no parameter."

step "7. the same estate applies under ssm with the run's customer managed key"
explain \
  "The one line changed: kms_key_id names the key this run created. The" \
  "first write checks the key is customer managed and that the role can" \
  "put, read back and delete a parameter under the path, then creates the" \
  "password's parameter before it commits the record that references it."
write_db_estate "$SSMD" "smoke-secrets-ssm" "$(ssm_block "$SSM_KEY")"
cmd "choudoufu apply -auto-approve   # ssm { kms_key_id = \"<the run's key>\" }"
SAPPLY2="$(cd "$SSMD" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "secrets" "apply under ssm failed: $SAPPLY2"
{ grep -E 'Apply complete!' <<< "$SAPPLY2" || true; } | evidence
grep -q 'Resources: 1 added' <<< "$SAPPLY2" || fail "secrets" "the ssm apply did not add exactly the database: $SAPPLY2"
SDB="$(awsl rds describe-db-instances --db-instance-identifier smoke-secrets-ssm-app --query 'DBInstances[0].DBInstanceStatus' --output text 2>/dev/null || echo none)"
[ "$SDB" = "available" ] || fail "secrets" "the database is not there after the ssm apply: $SDB"
grep -qF -- "$SECRET" <<< "$SAPPLY2" && fail "secrets" "the ssm apply printed the password to the terminal"
proof "one database created with the password set under ssm, and the run never printed it."

step "8. grep everything the run kept, the bucket's records included"
explain \
  "Under ssm the records are in the bucket, so the scan reads a copy of" \
  "every version of every object under the estate's prefix, downloaded" \
  "into the work directory, with the data dir, the lock file and the" \
  "configuration: the step 3 grep over the same kinds of file. The current" \
  "record must exist, carry the database's identifier and hold" \
  "secret_refs, or a grep over nothing would pass forever."
cmd "aws s3api get-object ... (every version under tofu-records/smoke-secrets-ssm/) ; grep -rl \"\$TF_VAR_db_password\" ."
SREC2="$SMOKE_WORK/ssm-record.json"
current_record "smoke-secrets-ssm" "$SREC2"
grep -q 'smoke-secrets-ssm-app' "$SREC2" || fail "secrets" "the ssm record for aws_db_instance.app does not carry the database's identifier, so it is not the record this step claims to have read: $(cat "$SREC2")"
SREFS="$(record_refs "$SREC2")"
[ -n "$SREFS" ] || fail "secrets" "the ssm record holds no secret_refs, so nothing was moved to Parameter Store: $(cat "$SREC2")"
[ -e "$SSMD/.terraform/choudoufu-cache.tfstate" ] && fail "secrets" "the state cache was written under ssm"
SVERS="$(bucket_copy "smoke-secrets-ssm" "$SSMD/bucket-copy")"
SHITS2="$(scan_kept "$SSMD")"
echo "files read: $(files_kept "$SSMD"), among them $SVERS object version(s) from the bucket; no cache file exists" | evidence
awk '{print "secret_refs: " $1 " " $2 " -> " $3}' <<< "$SREFS" | sed "s|$SSM_PATH|<path>|" | evidence
echo "files holding the password: ${SHITS2:-none}" | evidence
[ -z "$SHITS2" ] || fail "secrets" "the plaintext password is in what the run kept under ssm:
$SHITS2"
proof "zero copies of the password in the bucket's records, the data dir, the lock file or the configuration; the record holds secret_refs and no cache file exists."

step "9. the parameters are SecureString under the run's key, and decrypt to the value"
explain \
  "Each reference names a parameter under the run's path. Each must be a" \
  "SecureString encrypted under the run's customer managed key, not SSM's" \
  "own, and the one for password must decrypt to the value the run set." \
  "Nothing else may be under the path: the contract's probe parameter is" \
  "deleted once it has been read back."
cmd "aws ssm describe-parameters ... ; aws ssm get-parameter --with-decryption ..."
PWPARAM=""
while read -r rin rattr rparam; do
  [ -n "$rparam" ] || continue
  case "$rparam" in "$SSM_PATH"/*) ;; *) fail "secrets" "the reference $rparam is not under the run's path $SSM_PATH" ;; esac
  PMETA="$(awsl ssm describe-parameters --parameter-filters "Key=Name,Values=$rparam" --query 'Parameters[0].[Type,KeyId]' --output text 2>/dev/null || true)"
  PTYPE="$(cut -f1 <<< "$PMETA")"; PKEY="$(cut -f2 <<< "$PMETA")"
  [ "$PTYPE" = "SecureString" ] || fail "secrets" "the parameter $rparam is $PTYPE, not a SecureString"
  # The pinned emulator stores no KeyId for SSM and DescribeParameters
  # omits it, so the CLI prints None. Only that exact answer is let
  # through, and the key is then held by secrets-in-ssm-on-real-aws,
  # which makes the same check against AWS. Any KeyId that is present
  # and is not the run's key still fails here.
  case "$PKEY" in
    "$SSM_KEY"|*"key/$SSM_KEY") echo "$rin $rattr: $PTYPE under key ...${SSM_KEY: -8}" | evidence ;;
    None) echo "$rin $rattr: $PTYPE; the emulator reports no KeyId, the key is checked by secrets-in-ssm-on-real-aws" | evidence ;;
    *) fail "secrets" "the parameter $rparam is encrypted under $PKEY, not the run's key $SSM_KEY" ;;
  esac
  if [ "$rin" = "residue.attributes" ] && [ "$rattr" = "password" ]; then PWPARAM="$rparam"; fi
done <<< "$SREFS"
[ -n "$PWPARAM" ] || fail "secrets" "no reference is for the password attribute: $SREFS"
PWVAL="$(awsl ssm get-parameter --name "$PWPARAM" --with-decryption --query Parameter.Value --output text)" \
  || fail "secrets" "could not read back the password's parameter $PWPARAM"
[ "$PWVAL" = "\"$SECRET\"" ] || fail "secrets" "the password's parameter does not decrypt to the value the run set"
echo "password parameter decrypts to the value set in TF_VAR_db_password" | evidence
NPARAMS="$(ssm_params | grep -c . || true)"
[ "$NPARAMS" = "$(grep -c . <<< "$SREFS")" ] || fail "secrets" "$NPARAMS parameter(s) under $SSM_PATH and $(grep -c . <<< "$SREFS") reference(s); one of them is an orphan or a probe left behind: $(ssm_params | tr '\n' ' ')"
proof "$NPARAMS SecureString parameter(s) under the run's key, one per reference, and the password's decrypts to the value."

step "10. the replan under ssm reads the password back"
explain \
  "Under refuse the replan proposed the password on every plan, because" \
  "nothing kept the value to compare against (step 4b). Under ssm the" \
  "plan reads it back from Parameter Store, so the unchanged estate plans" \
  "No changes. and neither prints the value nor leaves it on disk."
cmd "choudoufu plan"
SPLAN2="$(cd "$SSMD" && chdf plan -input=false -no-color 2>&1)" || fail "secrets" "the replan under ssm failed: $SPLAN2"
grep -q 'No changes.' <<< "$SPLAN2" || fail "secrets" "the unchanged estate under ssm does not plan clean, so the password was not read back from its parameter: $SPLAN2"
grep -E 'No changes\.' <<< "$SPLAN2" | head -1 | evidence
grep -qF -- "$SECRET" <<< "$SPLAN2" && fail "secrets" "the replan printed the password"
proof "No changes.: the password came back from its parameter, and the plan did not print it."

step "11. a rotation replaces the parameter and deletes the one it superseded"
explain \
  "The password changes in TF_VAR_db_password. The apply sends it, writes" \
  "the new value to a new parameter, commits the record that references" \
  "it, and only then deletes the parameter the record used to reference." \
  "The old name must be gone, the new one must decrypt to the new value," \
  "and neither value may be in what the run kept."
ROTATED2="$SECRET-rotated"
cmd "TF_VAR_db_password=<rotated> choudoufu apply -auto-approve"
RAPPLY2="$(cd "$SSMD" && TF_VAR_db_password="$ROTATED2" chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "secrets" "the rotation apply under ssm failed: $RAPPLY2"
grep -q 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RAPPLY2" || fail "secrets" "the rotation did not update the database in place: $RAPPLY2"
current_record "smoke-secrets-ssm" "$SREC2"
SREFS2="$(record_refs "$SREC2")"
NEWPW="$(awk '$1 == "residue.attributes" && $2 == "password" {print $3}' <<< "$SREFS2")"
[ -n "$NEWPW" ] || fail "secrets" "after the rotation the record has no reference for password: $SREFS2"
[ "$NEWPW" != "$PWPARAM" ] || fail "secrets" "the rotated password kept the old parameter name $PWPARAM"
awsl ssm get-parameter --name "$PWPARAM" >/dev/null 2>&1 \
  && fail "secrets" "the superseded parameter $PWPARAM is still there after the record moved to $NEWPW"
NEWVAL="$(awsl ssm get-parameter --name "$NEWPW" --with-decryption --query Parameter.Value --output text)" \
  || fail "secrets" "could not read back the rotated password's parameter $NEWPW"
[ "$NEWVAL" = "\"$ROTATED2\"" ] || fail "secrets" "the new parameter does not decrypt to the rotated value"
NPARAMS2="$(ssm_params | grep -c . || true)"
[ "$NPARAMS2" = "$(grep -c . <<< "$SREFS2")" ] || fail "secrets" "$NPARAMS2 parameter(s) under $SSM_PATH after the rotation and $(grep -c . <<< "$SREFS2") reference(s): $(ssm_params | tr '\n' ' ')"
echo "password parameter: ...${PWPARAM: -12} deleted, ...${NEWPW: -12} decrypts to the rotated value" | evidence
echo "parameters under the path: $NPARAMS2, references: $(grep -c . <<< "$SREFS2")" | evidence
rm -rf "$SSMD/bucket-copy"
bucket_copy "smoke-secrets-ssm" "$SSMD/bucket-copy" >/dev/null
for v in "$SECRET" "$ROTATED2"; do
  VHITS="$(find "$SSMD" -type f -not -path '*/.terraform/providers/*' -not -name '*.tfplan' -not -name '*-debug.log' -print0 \
    | xargs -0 grep -l -- "$v" 2>/dev/null | sed "s|^$SSMD/||" || true)"
  [ -z "$VHITS" ] || fail "secrets" "after the rotation a password is in what the run kept under ssm:
$VHITS"
done
RPLAN2="$(cd "$SSMD" && TF_VAR_db_password="$ROTATED2" chdf plan -input=false -no-color 2>&1)" || fail "secrets" "the replan after the rotation failed: $RPLAN2"
grep -q 'No changes.' <<< "$RPLAN2" || fail "secrets" "after the rotation the estate does not plan clean with the rotated value: $RPLAN2"
grep -E 'No changes\.' <<< "$RPLAN2" | head -1 | sed 's/^/rotated: /' | evidence
proof "the rotated password went to a new parameter, the superseded one is deleted, the next plan reads the new value back, and neither value is in the bucket or on disk."

step "12. teardown"
explain \
  "Every estate is destroyed. Destroying the ssm estate deletes its record" \
  "and then the parameters it referenced, so nothing may be left under the" \
  "run's path. The run's key is then scheduled for deletion."
cmd "choudoufu apply -destroy -auto-approve   # all four estates ; aws kms schedule-key-deletion"
for d in "$REFUSE" "$FLAGS" "$STORE" "$SSMD"; do
  note "destroying $(basename "$d") ($(date +%H:%M:%S))"
  DOUT="$(cd "$d" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
    || fail "secrets" "teardown of $(basename "$d") failed: $DOUT"
  destroyed_exactly "secrets" 1 "$DOUT"
done
LEFT="$(ssm_params)"
echo "parameters left under $SSM_PATH: $(grep -c . <<< "$LEFT" || true)" | sed "s|$SSM_PATH|the run's path|" | evidence
[ -z "$LEFT" ] || fail "secrets" "the destroy left parameters under $SSM_PATH: $(tr '\n' ' ' <<< "$LEFT")"
awsl kms schedule-key-deletion --key-id "$SSM_KEY" --pending-window-in-days 7 >/dev/null \
  || fail "secrets" "could not schedule the run's key $SSM_KEY for deletion"
KSTATE="$(awsl kms describe-key --key-id "$SSM_KEY" --query KeyMetadata.KeyState --output text)"
echo "key ...${SSM_KEY: -8}: $KSTATE" | evidence
[ "$KSTATE" = "PendingDeletion" ] || fail "secrets" "the run's key is $KSTATE after schedule-key-deletion"
proof "all four databases gone, no parameter left under the run's path, and the key scheduled for deletion."

echo "  What you watched: a random_password and an access key refused by"
echo "  name under strict { secrets = \"refuse\" }, with nothing written;"
echo "  a database's master password set under the same setting and then"
echo "  searched for, by value, in every file the run kept, and found in"
echo "  none, with no cache file to hold it; the cost of that on the next"
echo "  plan, and the two files you can ask for by flag, a saved plan and a"
echo "  debug log, read for what they hold; and the default run keeping the"
echo "  same value in its record and its cache, the way a stock state file"
echo "  does. Then the same estate under strict { secrets = \"ssm\" }: SSM's"
echo "  own key refused by name, the apply under the run's customer managed"
echo "  key, the bucket's records holding references and no copy of the"
echo "  password, a SecureString under that key that decrypts to it, a clean"
echo "  replan, a rotation that deletes the parameter it superseded, and a"
echo "  teardown that leaves nothing under the run's path."
