# the-recommended-secure-configuration
# CLAIM 37 - The recommended secure configuration works end to end, including recovering a deleted record (REAL AWS, maintainer-run). ~10 min.

SMOKE_WORK="$SMOKE_WORKROOT/secureconfig"
mkdir -p "$SMOKE_WORK/est"; export SMOKE_WORK
PROJECT="$ROOT/examples/record-store-bucket"
# shellcheck source=../bucket-iam.sh
. "$SMOKE_DIR/bucket-iam.sh"

step "the claim"
explain \
  "The documentation recommends a customer managed key, because it is the" \
  "one kind of encryption at rest where you write the key policy and can" \
  "cut access by revoking the key. choudoufu asserts nothing about it, so" \
  "nothing in the repository would notice the day that advice stopped" \
  "working. A recommendation nobody measures is folklore. This stands the" \
  "whole recommended stack up FROM WHAT SHIPS - the runnable bucket" \
  "project and the published IAM policy, no hand-written one-offs - runs" \
  "an estate's life against it as a scoped role, and then does the thing" \
  "the retention window exists for: brings back a record that was" \
  "destroyed by mistake."

step "0. real AWS"
[ "${SMOKE_REAL_AWS:-0}" = "1" ] \
  || fail "secureconfig" "this scenario runs against real AWS: it deploys a CloudFormation stack (an S3 bucket), creates an IAM role and, unless SMOKE_KMS_KEY_ARN names one, a KMS key, and removes them. It is maintainer-run and never starts by itself: set SMOKE_REAL_AWS=1 to run it."
for bin in jq just node npm; do command -v "$bin" >/dev/null 2>&1 || fail "secureconfig" "$bin is not installed; the runnable bucket project needs it"; done
real_aws_begin secureconfig
BUCKET="chdf-smoke-secure-$SUFFIX"
ROLE="$(role_name smoke-secure-estate)" || fail "secureconfig" "could not name this run's role"
OPERATOR_ARN="$(aws sts get-caller-identity --query Arn --output text)"
# A key policy names IAM principals. Credentials that are themselves an
# assumed role arrive as an STS session ARN, so name the role behind it.
OPERATOR_ARN="$(sed -E 's#^(arn:aws[a-z-]*):sts::([0-9]+):assumed-role/([^/]+)/.*$#\1:iam::\2:role/\3#' <<< "$OPERATOR_ARN")"
[ -d "$PROJECT/node_modules" ] || ( cd "$PROJECT" && npm ci >/dev/null 2>&1 ) || fail "secureconfig" "npm ci failed in $PROJECT"

# Teardown on top of bucket-iam.sh's: the stack, the key policy and the key.
#
# This is the body #1378 was filed about. It runs from an EXIT trap under
# smoke.sh's `set -euo pipefail`, so one throttled list-object-versions
# used to end it there and skip `just down`, the key policy of a key that
# is somebody else's, the key deletion and the role deletion, printing
# nothing at all. errexit and nounset go off first, each step prints its
# own line naming the resource, and the last step is reached whatever the
# steps before it did.
CREATED_KEY=""; ORIGINAL_KEY_POLICY=""; KEY_POLICY_FILE=""; STACK_UP=0
secure_teardown() {
  set +e
  set +u
  if [ "$STACK_UP" = "1" ]; then
    # Deliberate emptying, which is what `just down` tells an operator to do.
    if aws s3api head-bucket --bucket "$BUCKET" >/dev/null 2>&1; then
      empty_bucket "$BUCKET"
    else
      echo "  no bucket $BUCKET to empty"
    fi
    ( cd "$PROJECT" && RECORD_KMS_KEY_ARN="$KEY_ARN" just down "$BUCKET" >/dev/null 2>&1 ) && echo "  removed stack $BUCKET" || echo "  COULD NOT REMOVE stack $BUCKET - remove it by hand" >&2
    # `just down` deletes the stack and the stack retains its bucket (#1382),
    # so the bucket is a step of its own, attempted whatever `down` said.
    remove_retained_bucket "$BUCKET"
  fi
  # A BORROWED key. Its policy was replaced by this run and belongs to
  # whoever lent it, so the copy on disk outlives the work root on purpose.
  if [ -n "$KEY_POLICY_FILE" ] && [ -f "$KEY_POLICY_FILE" ]; then
    if aws kms put-key-policy --key-id "$KEY_ARN" --policy-name default --policy "file://$KEY_POLICY_FILE" >/dev/null 2>&1; then
      echo "  restored the key's original policy from $KEY_POLICY_FILE"
      rm -f "$KEY_POLICY_FILE" && echo "  removed the saved copy of the original policy" \
        || echo "  COULD NOT REMOVE the saved policy copy $KEY_POLICY_FILE - delete it by hand" >&2
    else
      echo "  COULD NOT RESTORE the key policy on $KEY_ARN" >&2
      echo "  the original is still on disk. Restore it with:" >&2
      echo "    aws kms put-key-policy --key-id $KEY_ARN --policy-name default --policy file://$KEY_POLICY_FILE" >&2
    fi
  fi
  if [ -n "$CREATED_KEY" ]; then
    aws kms schedule-key-deletion --key-id "$CREATED_KEY" --pending-window-in-days 7 >/dev/null 2>&1 && echo "  scheduled KMS key $CREATED_KEY for deletion in 7 days" || echo "  COULD NOT SCHEDULE deletion of $CREATED_KEY - do it by hand" >&2
  fi
  real_aws_teardown
}
trap 'set +e; set +u; secure_teardown; cleanup' EXIT

step "1. the key, and a key policy that names who may use it"
explain \
  "The key policy is the point of a customer managed key, so it is" \
  "written the way the recommendation means it: the account may" \
  "ADMINISTER the key, and only the principals named may USE it. It does" \
  "not hand usage to IAM wholesale. The estate's role is named. So is" \
  "whoever is running this, because the operator recovers a record in" \
  "step 5 and that is a read."
if [ -n "${SMOKE_KMS_KEY_ARN:-}" ]; then
  KEY_ARN="$SMOKE_KMS_KEY_ARN"
  ORIGINAL_KEY_POLICY="$(aws kms get-key-policy --key-id "$KEY_ARN" --policy-name default --query Policy --output text)" || fail "secureconfig" "could not read the policy of $KEY_ARN"
  # The policy of a key this run does not own, written to disk BEFORE the
  # first put-key-policy and outside $SMOKE_WORKROOT, which cleanup deletes
  # (#1378). A shell variable is gone the moment the run is killed, and
  # what is lost is somebody else's key policy.
  KEY_POLICY_FILE="${TMPDIR:-/tmp}/choudoufu-smoke-keypolicy-${KEY_ARN##*/}-$(date +%Y%m%d-%H%M%S).json"
  printf '%s\n' "$ORIGINAL_KEY_POLICY" > "$KEY_POLICY_FILE" \
    || fail "secureconfig" "could not save the borrowed key's original policy to $KEY_POLICY_FILE; refusing to touch a key whose policy is not backed up"
  chmod 600 "$KEY_POLICY_FILE" 2>/dev/null || true
  echo "reusing $(mask <<< "$KEY_ARN"); its policy is restored at the end" | evidence
  echo "its original policy is saved at $KEY_POLICY_FILE" | evidence
  echo "if this run dies before restoring it, restore it by hand with:" | evidence
  echo "  aws kms put-key-policy --key-id $KEY_ARN --policy-name default --policy file://$KEY_POLICY_FILE" | evidence
else
  CREATED_KEY="$(aws kms create-key --description "choudoufu smoke claim 37, safe to delete" --query KeyMetadata.KeyId --output text)" || fail "secureconfig" "could not create the KMS key"
  KEY_ARN="$(aws kms describe-key --key-id "$CREATED_KEY" --query KeyMetadata.Arn --output text)"
fi
# The role has to exist before a key policy can name it, so it is created
# here rather than by role_with_policy in step 3. Registering it in
# REAL_ROLES is what tells step 3 this run owns it; a role of that name
# that this run did NOT create is refused, here and there, because its
# policy would be an earlier run's and teardown would leave it behind.
aws iam get-role --role-name "$ROLE" >/dev/null 2>&1 \
  && fail "secureconfig" "the role $ROLE already exists and this run did not create it; remove it by hand and run again"
aws iam create-role --role-name "$ROLE" --assume-role-policy-document "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::$ACCOUNT:root\"},\"Action\":\"sts:AssumeRole\"}]}" >/dev/null || fail "secureconfig" "could not create the role"
REAL_ROLES+=("$ROLE")
ROLE_ARN="arn:aws:iam::$ACCOUNT:role/$ROLE"
# key_policy <principal-arn>...: the account administers the key, and the
# statement that says who may USE it is the one the project ships.
KEY_STATEMENT="$PROJECT/iam/render-key-statement.sh"
key_policy() {
  local users; users="$("$KEY_STATEMENT" "$@")" || return 1
  jq -n --arg root "arn:aws:iam::$ACCOUNT:root" --argjson users "$users" '{
    Version: "2012-10-17",
    Statement: [
      { Sid: "TheAccountAdministersTheKey", Effect: "Allow", Principal: { AWS: $root },
        Action: ["kms:Create*","kms:Describe*","kms:Enable*","kms:List*","kms:Put*","kms:Update*","kms:Revoke*","kms:Disable*","kms:Get*","kms:Delete*","kms:TagResource","kms:UntagResource","kms:ScheduleKeyDeletion","kms:CancelKeyDeletion"],
        Resource: "*" },
      $users
    ]}'
}
cmd "render-key-statement.sh <operator> <the estate's role>   # examples/record-store-bucket/iam"
# A principal just created is not always visible to KMS at once.
KP_OK=0
for i in $(seq 1 20); do
  aws kms put-key-policy --key-id "$KEY_ARN" --policy-name default --policy "$(key_policy "$OPERATOR_ARN" "$ROLE_ARN")" >/dev/null 2>"$SMOKE_WORK/kp.err" && { KP_OK=1; break; }
  sleep 3
done
[ "$KP_OK" = "1" ] || fail "secureconfig" "could not set the key policy: $(cat "$SMOKE_WORK/kp.err")"
aws kms get-key-policy --key-id "$KEY_ARN" --policy-name default --query Policy --output text | jq -c '.Statement[] | {Sid, Principal}' | mask | evidence
proof "usage is granted by name in the key policy, and to nobody else."

step "2. the bucket, from the project that ships"
cmd "RECORD_KMS_KEY_ARN=<key> just up $BUCKET   # examples/record-store-bucket"
# Before the deploy, never after it: a `just up` that creates the stack and
# then fails, or is interrupted, leaves one behind, and a flag set on the
# line after would still read 0 (#1378).
STACK_UP=1
UP_OUT="$(cd "$PROJECT" && RECORD_KMS_KEY_ARN="$KEY_ARN" RECORD_NONCURRENT_DAYS=7 just up "$BUCKET" 2>&1)" || fail "secureconfig" "just up failed: $UP_OUT"
grep -E 'RECORD_STORE_BUCKET=|noncurrent versions expire' <<< "$UP_OUT" | evidence
cmd "just verify $BUCKET"
V_OUT="$(cd "$PROJECT" && CHOUDOUFU_BIN="$TOFU" RECORD_KMS_KEY_ARN="$KEY_ARN" just verify "$BUCKET" 2>&1)" || fail "secureconfig" "just verify says the bucket it just made is not correct: $V_OUT"
grep -E ' OK | ok$|ok \(|: correct' <<< "$V_OUT" | evidence
grep -q "bucket $BUCKET: correct" <<< "$V_OUT" || fail "secureconfig" "verify did not report the bucket correct: $V_OUT"
proof "stood up and verified with the shipped project. Nothing about this bucket was written by hand."

step "3. the estate's role, from the published policy"
cmd "render-policy.sh smoke-secure $BUCKET --kms <key>"
role_with_policy "$ROLE" "$("$POLICY_RENDERER" smoke-secure "$BUCKET" --kms "$KEY_ARN")" "$BUCKET" \
  || fail "secureconfig" "the role's policy never went live"
# The sentence below was printed and not asked (#1379), and it was not quite
# true either: role_with_policy adds a ProofThisPolicyIsLive statement over
# one marker key, which is how it knows IAM has propagated. So the live
# policy is read back, that one statement is dropped, and what is left has to
# be what the renderer produces today, statement for statement.
LIVE_POLICY="$(aws iam get-role-policy --role-name "$ROLE" --policy-name estate --query PolicyDocument --output json)" \
  || fail "secureconfig" "could not read back the role's inline policy"
LIVE_MINUS_MARKER="$(jq -S '.Statement |= map(select(.Sid != "ProofThisPolicyIsLive"))' <<< "$LIVE_POLICY")"
FRESH_RENDER="$("$POLICY_RENDERER" smoke-secure "$BUCKET" --kms "$KEY_ARN" | jq -S .)"
grep -q ProofThisPolicyIsLive <<< "$LIVE_POLICY" \
  || fail "secureconfig" "the live policy carries no ProofThisPolicyIsLive statement, so dropping it below removes nothing and the comparison is not the one this step describes"
[ "$LIVE_MINUS_MARKER" = "$FRESH_RENDER" ] \
  || fail "secureconfig" "the role's live policy is not the renderer's output: $(diff <(echo "$LIVE_MINUS_MARKER") <(echo "$FRESH_RENDER") | head -20 | mask)"
jq -r '.Statement[].Sid' <<< "$LIVE_POLICY" | tr '\n' ' ' | sed 's/^/statements on the role: /' | evidence
proof "the role's policy is the renderer's output statement for statement, plus the one ProofThisPolicyIsLive statement this harness adds over a single marker key to know that IAM has propagated. Nothing else was edited in."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the mistake people actually make"
  explain \
    "A customer managed key whose key policy does not name the estate's" \
    "role. Everything else is right: the bucket, the IAM policy with its" \
    "kms statement, the role. The run must say what is wrong BY NAME. An" \
    "opaque AccessDenied from a GET costs an operator an afternoon." \
    "" \
    "The role was in the key policy until now, because that is the only" \
    "way to prove its IAM policy is live. It is taken out here, and the" \
    "arm waits until the role can no longer read an object it could read" \
    "a moment ago - so what follows is measured under the broken policy" \
    "and not under the one before it."
  aws kms put-key-policy --key-id "$KEY_ARN" --policy-name default --policy "$(key_policy "$OPERATOR_ARN")" >/dev/null || fail "secureconfig" "could not install the key policy that omits the role"
  aws kms get-key-policy --key-id "$KEY_ARN" --policy-name default --query Policy --output text | jq -c '.Statement[] | {Sid, Principal}' | mask | evidence
  CUT=0
  for i in $(seq 1 60); do
    RAW="$(as_role "$ROLE" aws s3api get-object --bucket "$BUCKET" --key "markers/$ROLE-$MARKER_N" "$SMOKE_WORK/marker.out" 2>&1)" || {
      # Any failure used to end this loop and read as "the role can no longer
      # decrypt" (#1379): an assume-role that timed out, a throttle, a
      # network blip. What this arm needs is AWS refusing the read.
      denied "$RAW" \
        || fail "secureconfig" "the role's read of its marker failed, but not on a denial, so nothing here shows the key policy is what cut it: $RAW"
      CUT=1
      break
    }
    sleep 3
  done
  [ "$CUT" = "1" ] || fail "secureconfig" "three minutes after the role was taken out of the key policy it can still decrypt, so this arm has nothing to measure"
  echo "what AWS itself says: $(flat <<< "$RAW" | mask)" | evidence
fi

write_estate() { # instances-literal input
  cat > "$SMOKE_WORK/est/main.tf" <<TFEOF
terraform {
  live {
    estate = "smoke-secure"

    record_store "s3" {
      bucket = "$BUCKET"
      region = "$AWS_REGION"
    }
  }
}

resource "terraform_data" "effect" {
  for_each = toset($1)
  input    = "\${each.key}-$2"
}
TFEOF
}
as_estate() { ( cd "$SMOKE_WORK/est" && as_role "$ROLE" env TF_LOG=debug TF_LOG_PATH="$SMOKE_WORKROOT/logs/secure-$1.log" "$TOFU" "${@:2}" ); }

step "4. an estate's life, as that role, against that stack"
write_estate '["keep", "precious"]' v1
( cd "$SMOKE_WORK/est" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "secureconfig" "init failed"
cmd "choudoufu apply -auto-approve   # as the estate's role"
A_OUT="$(as_estate apply apply -auto-approve -input=false -no-color 2>&1)" && A_RC=0 || A_RC=$?

if [ "${BREAK:-0}" = "1" ]; then
  [ "$A_RC" != "0" ] || fail "secureconfig" "the estate applied although the key policy does not name its role: $A_OUT"
  A_FLAT="$(flat <<< "$A_OUT")"
  # The page says the refusal names the key, the action and the role. It used
  # to be checked with two substrings, "KMS key" and "key policy", both of
  # which a message naming none of the three could carry (#1379). All three
  # are named here, by their values in this run.
  grep -q "key policy" <<< "$A_FLAT" \
    || fail "secureconfig" "the run failed without pointing at the key policy. An operator reading this has no idea where to look: $A_OUT"
  grep -q "${KEY_ARN##*/}" <<< "$A_FLAT" \
    || fail "secureconfig" "the refusal does not name the key that refused ($(mask <<< "$KEY_ARN")): $A_OUT"
  grep -qE 'kms:GenerateDataKey|kms:Decrypt' <<< "$A_FLAT" \
    || fail "secureconfig" "the refusal does not name a KMS action, so it does not say what the role may not do: $A_OUT"
  grep -q "$ROLE" <<< "$A_FLAT" \
    || fail "secureconfig" "the refusal does not name the role it refused ($ROLE): $A_OUT"
  grep -oE 'Error: [^.]*\.' <<< "$A_FLAT" | head -1 | mask | evidence
  grep -oE "The bucket's KMS key[^.]*\.[^.]*\." <<< "$A_FLAT" | head -1 | mask | evidence
  proof "caught - refused by name, and the name is three things: the key $(mask <<< "${KEY_ARN##*/}"), the KMS action, and the role $ROLE that its policy no longer names."
  exit 0
fi

[ "$A_RC" = "0" ] || fail "secureconfig" "the estate could not apply against the recommended stack: $A_OUT"
grep -q "Resources: 2 added, 0 changed, 0 destroyed" <<< "$A_OUT" || fail "secureconfig" "create: $A_OUT"
RECORD="$(aws s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-secure/terraform_data/ --query 'Contents[].Key' --output json | jq -r '.[]' | while read -r k; do aws s3api get-object --bucket "$BUCKET" --key "$k" /dev/stdout 2>/dev/null | grep -q 'precious' && echo "$k" && break; done)"
[ -n "$RECORD" ] || fail "secureconfig" "could not find precious's record in the bucket"
SSE="$(aws s3api head-object --bucket "$BUCKET" --key "$RECORD" --query '[ServerSideEncryption,SSEKMSKeyId]' --output text)"
grep -q "aws:kms" <<< "$SSE" && grep -q "${KEY_ARN##*/}" <<< "$SSE" || fail "secureconfig" "the record is not encrypted under the customer managed key: $SSE"
write_estate '["keep", "precious"]' v2
U_OUT="$(as_estate update apply -auto-approve -input=false -no-color 2>&1)" || fail "secureconfig" "the update under If-Match failed: $U_OUT"
grep -q "Resources: 0 added, 2 changed, 0 destroyed" <<< "$U_OUT" || fail "secureconfig" "update: $U_OUT"
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
P_OUT="$(as_estate plan plan -input=false -no-color 2>&1)" || fail "secureconfig" "plan: $P_OUT"
grep -q "No changes." <<< "$P_OUT" || fail "secureconfig" "the replan from the records alone was not empty: $P_OUT"
BEFORE="$(aws s3api head-object --bucket "$BUCKET" --key "$RECORD" --query '[VersionId,ETag]' --output text)"
echo "2 added; record encrypted with aws:kms under the key; 2 changed under If-Match; replan empty" | evidence
proof "create, read and update as the scoped role, through a key only it and the operator may use."

step "5. a record destroyed by mistake, and brought back"
explain \
  "Someone removes 'precious' from the configuration and applies. The" \
  "record is deleted. In a versioned bucket a delete writes a delete" \
  "marker and the record survives underneath as a noncurrent version for" \
  "as long as the lifecycle allows - that window is what the bucket" \
  "project makes you choose. Recovery is an OPERATOR's act, with the" \
  "operator's credentials: the estate's policy deliberately has no" \
  "s3:DeleteObjectVersion, so a run can never rewrite history."
write_estate '["keep"]' v2
M_OUT="$(as_estate mistake apply -auto-approve -input=false -no-color 2>&1)" || fail "secureconfig" "the mistaken apply failed: $M_OUT"
grep -q "Resources: 0 added, 0 changed, 1 destroyed" <<< "$M_OUT" || fail "secureconfig" "the mistaken apply: $M_OUT"
GONE="$(aws s3api list-objects-v2 --bucket "$BUCKET" --prefix "$RECORD" --query 'length(Contents || `[]`)' --output text)"
[ "$GONE" = "0" ] || fail "secureconfig" "the record is still listed after its instance was destroyed"
MARKER="$(aws s3api list-object-versions --bucket "$BUCKET" --prefix "$RECORD" --query 'DeleteMarkers[?IsLatest==`true`].VersionId | [0]' --output text)"
[ -n "$MARKER" ] && [ "$MARKER" != "None" ] || fail "secureconfig" "no delete marker: the record was removed outright and there is nothing to recover"
echo "the record is gone from a listing; a delete marker and $(aws s3api list-object-versions --bucket "$BUCKET" --prefix "$RECORD" --query 'length(Versions || `[]`)' --output text) earlier version(s) remain" | evidence
cmd "aws s3api delete-object --version-id <the delete marker>   # as the operator"
D_ROLE="$(as_role "$ROLE" aws s3api delete-object --bucket "$BUCKET" --key "$RECORD" --version-id "$MARKER" 2>&1)" && fail "secureconfig" "the ESTATE'S role removed a delete marker; it must not be able to rewrite history: $D_ROLE"
# The call failing is not the claim. An assume-role that timed out, or a
# key that is no longer readable, fails here too and would read as a
# refusal (#1378). What the claim rests on is AWS refusing the delete.
denied "$D_ROLE" || fail "secureconfig" "the estate's role did not remove the delete marker, but the failure is not a denial, so nothing here shows the policy refused it: $D_ROLE"
aws s3api delete-object --bucket "$BUCKET" --key "$RECORD" --version-id "$MARKER" >/dev/null || fail "secureconfig" "the operator could not remove the delete marker"
write_estate '["keep", "precious"]' v2
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # as the estate's role, 'precious' back in the configuration"
R_OUT="$(as_estate recovered plan -input=false -no-color 2>&1)" || fail "secureconfig" "the plan after recovery failed: $R_OUT"
grep -q "No changes." <<< "$R_OUT" || fail "secureconfig" "after recovery the plan was not empty, so the record that came back is not the record that was lost: $R_OUT"
AFTER="$(aws s3api head-object --bucket "$BUCKET" --key "$RECORD" --query '[VersionId,ETag]' --output text)"
[ -n "$BEFORE" ] && [ "$BEFORE" = "$AFTER" ] || fail "secureconfig" "the record current after recovery is version '$AFTER'; before the mistake it was '$BEFORE'"
echo "precious's record: version $(cut -f1 <<< "$BEFORE") before the mistake, $(cut -f1 <<< "$AFTER") after recovery; plan empty" | evidence
proof "recovered from a noncurrent version: the very object version that was current before the mistake, and a plan that proposes no re-creation. The estate's role was refused the same act."

step "6. what the run actually used, against what the policy grants"
explain \
  "Every S3 operation the estate's role made is in the request log. A" \
  "policy that grants something the run never used, or a run that needed" \
  "something the policy does not grant, is a defect in the policy."
USED="$(cat "$SMOKE_WORKROOT"/logs/secure-*.log | grep 'stateless/recordstore: HTTP Request Sent' | grep -oE 'rpc.method=[A-Za-z0-9]+' | cut -d= -f2 | sort -u)"
[ -n "$USED" ] || fail "secureconfig" "the request log holds no record store requests, so nothing was reconciled"
to_action() { case "$1" in
  ListObjectsV2) echo s3:ListBucket ;; GetObject) echo s3:GetObject ;; PutObject) echo "s3:PutObject s3:PutObjectTagging" ;;
  DeleteObject) echo s3:DeleteObject ;; GetBucketVersioning) echo s3:GetBucketVersioning ;;
  GetBucketLifecycleConfiguration) echo s3:GetLifecycleConfiguration ;; GetPublicAccessBlock) echo s3:GetBucketPublicAccessBlock ;;
  *) echo "UNMAPPED:$1" ;; esac; }
USED_ACTIONS="$(for op in $USED; do to_action "$op"; done | tr ' ' '\n' | sort -u)"
GRANTED="$("$POLICY_RENDERER" smoke-secure "$BUCKET" --kms "$KEY_ARN" | jq -r '.Statement[] | select(.Effect == "Allow") | .Action | if type == "array" then .[] else . end' | grep '^s3:' | sort -u)"
grep -q UNMAPPED <<< "$USED_ACTIONS" && fail "secureconfig" "the run made an S3 call this scenario cannot map to an IAM action: $USED_ACTIONS"
ONLY_USED="$(comm -23 <(echo "$USED_ACTIONS") <(echo "$GRANTED"))"
ONLY_GRANTED="$(comm -13 <(echo "$USED_ACTIONS") <(echo "$GRANTED"))"
[ -z "$ONLY_USED" ] || fail "secureconfig" "the run used what the policy does not name: $ONLY_USED"
[ -z "$ONLY_GRANTED" ] || fail "secureconfig" "the policy grants what no step of an estate's life used: $ONLY_GRANTED"
echo "$USED_ACTIONS" | tr '\n' ' ' | evidence
proof "the S3 actions exercised and the S3 actions granted are the same set. (kms:Decrypt and kms:GenerateDataKey are made by S3 on the role's behalf and never appear as the role's own requests; step 4 could not have passed without them.)"

step "7. teardown, and the project refusing to help until it is safe"
T_OUT="$(as_estate destroy apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "secureconfig" "destroy: $T_OUT"
grep -q "Resources: 0 added, 0 changed, 2 destroyed" <<< "$T_OUT" || fail "secureconfig" "the destroy did not remove both instances: $T_OUT"
cmd "just down $BUCKET   # the estate is destroyed, its recoverable versions are not"
DN_OUT="$(cd "$PROJECT" && RECORD_KMS_KEY_ARN="$KEY_ARN" just down "$BUCKET" 2>&1)" && fail "secureconfig" "just down tore the bucket down while it still held recoverable record versions: $DN_OUT"
grep -q "REFUSING" <<< "$DN_OUT" || fail "secureconfig" "just down failed, but not by refusing: $DN_OUT"
grep -E 'REFUSING|It holds' <<< "$DN_OUT" | head -2 | evidence
proof "both instances destroyed by the role, and the project will not remove a bucket that still holds what step 5 just relied on. The exit handler empties it deliberately and runs just down again."

echo "  What you watched: the recommended stack built from the shipped project"
echo "  and the published policy, an estate living in it as a scoped role"
echo "  under a key only named principals may use, a destroyed record brought"
echo "  back with its identity intact, and the policy's grants matching what"
echo "  the run used."
