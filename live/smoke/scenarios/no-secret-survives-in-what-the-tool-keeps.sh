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
( cd "$REFUSE" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "secrets" "init of the refuse estate failed"
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
  ( cd "$BSTORE" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "secrets" "BREAK: init of the store estate failed"
  BAPPLY="$(cd "$BSTORE" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "secrets" "BREAK: apply under store failed: $BAPPLY"
  BHITS="$(scan_kept "$BSTORE")"
  echo "files holding the password: $(tr '\n' ' ' <<< "${BHITS:-none}")" | evidence
  if [ -z "$BHITS" ]; then
    fail "secrets" "BREAK: the store run kept the password nowhere the scan can see, so the zero hits in step 3 were never evidence of anything"
  fi
  proof "caught - the same scan finds the password in $(grep -c . <<< "$BHITS") file(s) under secrets = \"store\", so the zero under refuse was a real zero."
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
( cd "$FLAGS" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "secrets" "init of the flags estate failed"
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

step "4b. the replan under refuse, as measured"
explain \
  "use/secrets.md says a sensitive argument left out of its record shows" \
  "as a change on every plan. Measured, it does not: the plan seeds the" \
  "prior from the configuration's own value, so the replan reads No" \
  "changes. The same seeding means a password changed in the" \
  "configuration is not proposed under refuse; that is a defect, filed" \
  "as #1503, and this step prints the replan without asserting its shape" \
  "so the scenario does not pin it. What it does assert is the claim:" \
  "the replan leaves nothing behind."
cmd "choudoufu plan"
RPLAN="$(cd "$REFUSE" && chdf plan -input=false -no-color 2>&1)" || fail "secrets" "the replan under refuse failed: $RPLAN"
{ grep -E '^Plan:|^No changes' <<< "$RPLAN" || true; } | head -1 | evidence
grep -qF -- "$SECRET" <<< "$RPLAN" && fail "secrets" "the replan printed the password"
[ -z "$(scan_kept "$REFUSE")" ] || fail "secrets" "the replan left the password in what the tool keeps"
proof "observed, not asserted: the replan headline above. Asserted: it printed no password and left none on disk."

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

step "6. teardown"
cmd "choudoufu apply -destroy -auto-approve   # all three estates"
for d in "$REFUSE" "$FLAGS" "$STORE"; do
  DOUT="$(cd "$d" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
    || fail "secrets" "teardown of $(basename "$d") failed: $DOUT"
  destroyed_exactly "secrets" 1 "$DOUT"
done
proof "all three databases gone."

echo "  What you watched: a random_password and an access key refused by"
echo "  name under strict { secrets = \"refuse\" }, with nothing written;"
echo "  a database's master password set under the same setting and then"
echo "  searched for, by value, in every file the run kept, and found in"
echo "  none, with no cache file to hold it; the cost of that on the next"
echo "  plan, and the two files you can ask for by flag, a saved plan and a"
echo "  debug log, read for what they hold; and the default run keeping the"
echo "  same value in its record and its cache, the way a stock state file"
echo "  does."
