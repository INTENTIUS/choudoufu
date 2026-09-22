# no-secret-survives-in-what-the-tool-keeps
# CLAIM 40 - No secret the run generates or sets survives in anything the tool keeps. ~3 min.

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

# scan_for_secret <dir> <label> prints every file under <dir> that holds the
# value, one per line, and the count of files it read. Provider plugins
# under .terraform/providers are the one exclusion: they are downloaded by
# init, before the value ever reaches a run. Everything else the run wrote -
# records, cache, plan file, debug log, the configuration - is read.
scan_for_secret() {
  local dir="$1"
  find "$dir" -type f -not -path '*/.terraform/providers/*' -print0 \
    | xargs -0 grep -l -- "$SECRET" 2>/dev/null | sed "s|^$dir/||" || true
}
files_scanned() {
  find "$1" -type f -not -path '*/.terraform/providers/*' | wc -l | tr -d ' '
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
( cd "$GEN" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "secrets" "init of the generating estate failed"
GCODE=0
GOUT="$(cd "$GEN" && chdf plan -input=false -no-color 2>&1)" || GCODE=$?
[ "$GCODE" != "0" ] || fail "secrets" "the plan went through with a random_password and an aws_iam_access_key under secrets = \"refuse\": $GOUT"
GFLAT="$(tr '\n' ' ' <<< "$GOUT" | tr -s ' ')"
for want in 'random_password.db' 'aws_iam_access_key.deploy' 'secrets = "refuse"'; do
  grep -qF -- "$want" <<< "$GFLAT" || fail "secrets" "the refusal does not name $want: $GOUT"
done
grep -E 'random_password.db|aws_iam_access_key.deploy' <<< "$GOUT" | grep -E '^Error|^│ Error|with |is a' | head -4 | evidence
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
  "is spelled nowhere on disk. The run is made as noisy as a run can be:" \
  "a saved plan file (-out), the apply consuming it, and TF_LOG=debug" \
  "capturing every request the provider sent - so the grep afterwards" \
  "has the most to find."
REFUSE="$SMOKE_WORK/refuse"
write_db_estate "$REFUSE" "smoke-secrets-refuse" '    strict {
      secrets = "refuse"
    }'
cmd "choudoufu plan -out=change.tfplan && choudoufu apply change.tfplan   # TF_LOG=debug, TF_VAR_db_password set"
( cd "$REFUSE" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "secrets" "init of the refuse estate failed"
( cd "$REFUSE" && TF_LOG=debug TF_LOG_PATH="$REFUSE/plan-debug.log" "$TOFU" plan -input=false -no-color -out=change.tfplan >/dev/null 2>&1 ) \
  || fail "secrets" "plan -out under refuse failed"
RAPPLY="$(cd "$REFUSE" && TF_LOG=debug TF_LOG_PATH="$REFUSE/apply-debug.log" "$TOFU" apply -input=false -no-color change.tfplan 2>&1)" \
  || fail "secrets" "apply under refuse failed: $RAPPLY"
grep -E 'Apply complete!' <<< "$RAPPLY" | evidence
grep -q 'Resources: 1 added' <<< "$RAPPLY" || fail "secrets" "the apply did not add exactly the database: $RAPPLY"
DBSTATUS="$(awsl rds describe-db-instances --db-instance-identifier smoke-secrets-refuse-app --query 'DBInstances[0].DBInstanceStatus' --output text 2>/dev/null || echo none)"
[ "$DBSTATUS" = "available" ] || fail "secrets" "the database is not there after the apply: $DBSTATUS"
grep -qF -- "$SECRET" <<< "$RAPPLY" && fail "secrets" "the apply printed the password to the terminal"
proof "one database created with the password set, and the run never printed it."

step "3. grep everything the run wrote for the plaintext"
explain \
  "Every file under the working directory except the provider plugins" \
  "init downloaded: the record store, the data dir, the saved plan file," \
  "both debug logs, the configuration itself. The grep is by value, for" \
  "the exact string in TF_VAR_db_password. The record for the database" \
  "must exist and the logs must be non-trivial, or a grep over nothing" \
  "would pass forever."
cmd "grep -rl \"\$TF_VAR_db_password\" .   # minus .terraform/providers"
RREC="$REFUSE/.tofu-records/tofu-records/smoke-secrets-refuse/aws_db_instance/$(python3 -c "import base64; print(base64.urlsafe_b64encode(b'aws_db_instance.app').decode().rstrip('='))")"
[ -f "$RREC" ] || fail "secrets" "no record was written for aws_db_instance.app under refuse: $(find "$REFUSE/.tofu-records" -type f 2>/dev/null)"
[ -s "$REFUSE/change.tfplan" ] || fail "secrets" "the saved plan file is missing or empty"
APPLY_REQS="$(grep -c 'HTTP Request Sent' "$REFUSE/apply-debug.log" || true)"
[ "$APPLY_REQS" -gt 0 ] || fail "secrets" "the apply's debug log recorded no requests, so it is not the log this step claims to have read"
grep -q 'CreateDBInstance' "$REFUSE/apply-debug.log" || fail "secrets" "the apply's debug log never mentions CreateDBInstance, the one request that carried the password"
[ -e "$REFUSE/.terraform/choudoufu-cache.tfstate" ] && fail "secrets" "the state cache was written under refuse"
HITS="$(scan_for_secret "$REFUSE")"
NSCANNED="$(files_scanned "$REFUSE")"
echo "files read: $NSCANNED (record $(wc -c < "$RREC" | tr -d ' ') bytes, plan file $(wc -c < "$REFUSE/change.tfplan" | tr -d ' ') bytes, apply log $APPLY_REQS requests, no cache file)" | evidence
echo "files holding the password: ${HITS:-none}" | evidence
[ -z "$HITS" ] || fail "secrets" "the plaintext password is in what the run kept under refuse:
$HITS"
grep -q '"password"' "$RREC" && fail "secrets" "the record names the password attribute at all: $(cat "$RREC")"
proof "$NSCANNED files read, zero hold the password: not the record, not the plan file, not the debug logs, and no cache file exists to hold it."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the same grep over a run that keeps the value"
  explain \
    "Zero hits proves nothing if the grep could not find the value in a" \
    "file that has it. So the control is the inverse: the identical" \
    "estate under secrets = \"store\", the default, applied the same way," \
    "and the identical scan MUST find the password. If it finds nothing," \
    "the zero above was scenery and this run fails."
  STORE="$SMOKE_WORK/store-break"
  write_db_estate "$STORE" "smoke-secrets-store" ""
  cmd "choudoufu apply -auto-approve   # secrets = \"store\" ; then the same grep"
  ( cd "$STORE" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "secrets" "BREAK: init of the store estate failed"
  ( cd "$STORE" && TF_LOG=debug TF_LOG_PATH="$STORE/apply-debug.log" "$TOFU" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
    || fail "secrets" "BREAK: apply under store failed"
  BHITS="$(scan_for_secret "$STORE")"
  echo "files holding the password: ${BHITS:-none}" | evidence
  if [ -z "$BHITS" ]; then
    fail "secrets" "BREAK: the default run kept the password nowhere the scan can see, so the zero hits in step 3 were never evidence of anything"
  fi
  proof "caught - the same scan finds the password in $(grep -c . <<< "$BHITS") file(s) under the default, so the zero under refuse was a real zero."
  ( cd "$STORE" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  ( cd "$REFUSE" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "4. what refuse costs: the password is proposed on every plan"
explain \
  "A value that is neither returned by the API nor remembered has no" \
  "prior side. So every plan under refuse shows the password as a change" \
  "to an otherwise unchanged database, and the apply that follows sends" \
  "it again. That is the price of keeping nothing, and the page says so." \
  "It is visible, and it is the same value, so it is harmless; but a" \
  "reader who wants No changes. on a converged estate is choosing store."
cmd "choudoufu plan"
RPLAN="$(cd "$REFUSE" && chdf plan -input=false -no-color 2>&1 | grep -v '^discovering:')" || fail "secrets" "the replan under refuse failed"
grep -E 'password|to change' <<< "$RPLAN" | head -3 | evidence
grep -q '1 to change' <<< "$RPLAN" || fail "secrets" "the replan under refuse is not exactly one in-place change: $RPLAN"
grep -q 'password' <<< "$RPLAN" || fail "secrets" "the replan's one change is not the password: $RPLAN"
grep -qF -- "$SECRET" <<< "$RPLAN" && fail "secrets" "the plan printed the password"
proof "one change, the password, rendered as a sensitive value: the cost of remembering nothing, stated rather than hidden."

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
( cd "$STORE" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "secrets" "init of the store estate failed"
SAPPLY="$(cd "$STORE" && TF_LOG=debug TF_LOG_PATH="$STORE/apply-debug.log" "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "secrets" "apply under store failed: $SAPPLY"
grep -q 'Resources: 1 added' <<< "$SAPPLY" || fail "secrets" "the store apply did not add exactly the database: $SAPPLY"
SREC="$STORE/.tofu-records/tofu-records/smoke-secrets-store/aws_db_instance/$(python3 -c "import base64; print(base64.urlsafe_b64encode(b'aws_db_instance.app').decode().rstrip('='))")"
SCACHE="$STORE/.terraform/choudoufu-cache.tfstate"
[ -f "$SREC" ] || fail "secrets" "no record for aws_db_instance.app under store"
[ -f "$SCACHE" ] || fail "secrets" "no state cache under store"
SHITS="$(scan_for_secret "$STORE")"
echo "files holding the password: ${SHITS:-none}" | evidence
grep -qF -- "$SECRET" "$SREC" || fail "secrets" "the default did not record the password, which contradicts what the docs say the default keeps: $(cat "$SREC")"
grep -qF -- "$SECRET" "$SCACHE" || fail "secrets" "the default did not put the password in the state cache, which contradicts what the docs say the cache holds"
rm -f "$SCACHE"
SPLAN="$(cd "$STORE" && chdf plan -input=false -no-color 2>&1 | grep -v '^discovering:')" || fail "secrets" "the store replan failed"
grep -q 'No changes.' <<< "$SPLAN" || fail "secrets" "with the cache deleted the store estate does not plan clean: $SPLAN"
grep -E 'No changes\.' <<< "$SPLAN" | head -1 | evidence
proof "under the default the password is in the record and was in the cache, exactly what a stock state file keeps; the cache deleted, the plan is still No changes. The setting is the difference, and it is yours to make."

step "6. teardown"
cmd "choudoufu apply -destroy -auto-approve   # both estates"
for d in "$REFUSE" "$STORE"; do
  DOUT="$(cd "$d" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
    || fail "secrets" "teardown of $(basename "$d") failed: $DOUT"
  destroyed_exactly "secrets" 1 "$DOUT"
done
proof "both databases gone."

echo "  What you watched: a random_password and an access key refused by"
echo "  name under strict { secrets = \"refuse\" }, with nothing written;"
echo "  a database's master password set through the same setting and then"
echo "  searched for, by value, in every file the run wrote - record, plan"
echo "  file, debug logs - and found in none, with no cache file to hold"
echo "  it; the cost of that stated on the next plan; and the default run"
echo "  keeping the same value in its record and its cache, the way a stock"
echo "  state file does."
