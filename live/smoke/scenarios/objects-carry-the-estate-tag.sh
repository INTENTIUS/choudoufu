# objects-carry-the-estate-tag
# CLAIM 36 - Every record store object carries its estate's tag, and the tag is load-bearing (REAL AWS, maintainer-run). ~5 min.

SMOKE_WORK="$SMOKE_WORKROOT/objecttags"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
# shellcheck source=../bucket-iam.sh
. "$SMOKE_DIR/bucket-iam.sh"

step "the claim"
explain \
  "Every object choudoufu writes to the bucket carries the estate's own" \
  "markers, tofu-estate on all of them and tofu-address on a record, the" \
  "same pair every managed resource carries. That is half of the" \
  "isolation model, so it has to be more than a habit. Two things make it" \
  "load-bearing, and this measures both. The published policy will not" \
  "accept an untagged write at all. And an object tagged as another" \
  "estate's is refused to this estate's role, even under its own prefix."

step "0. real AWS"
[ "${SMOKE_REAL_AWS:-0}" = "1" ] \
  || fail "objecttags" "this scenario runs against real AWS: it creates an S3 bucket and an IAM role in the account your credentials name, and removes them. It is maintainer-run and never starts by itself: set SMOKE_REAL_AWS=1 to run it."
command -v jq >/dev/null 2>&1 || fail "objecttags" "jq is not installed; the policy renderer needs it"
real_aws_begin objecttags
BUCKET="chdf-smoke-objecttags-$SUFFIX"

# The binary under test. BREAK=1 swaps in one that sends no tags.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a store that does not tag must not be able to write"
  explain \
    "The corruption is in the binary: the S3 store stops sending tags." \
    "Built with go build -overlay, so the source tree is never touched." \
    "If that binary can still write under the published policy, then" \
    "tagging is a convention the policy does not enforce."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "objecttags" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION"
  command -v go >/dev/null 2>&1 || fail "objecttags" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/staterecord/s3.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/s3.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
old = '\tif tagging := encodeObjectTagging(s.baseTags, ObjectTags(ctx)); tagging != "" {\n'
assert src.count(old) == 1, "the break patch no longer matches S3Store.PutIfVersion"
open(sys.argv[2], "w").write(src.replace(old, '\tif tagging := encodeObjectTagging(s.baseTags, ObjectTags(ctx)); false && tagging != "" {\n'))
PYEOF
  [ -s "$SMOKE_WORK/break/s3.go" ] || fail "objecttags" "the break patch did not apply, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/s3.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # PutObject with no Tagging"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "objecttags" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi
run_as_estate() { ( cd "$SMOKE_WORK/est" && as_role smoke-tagged-estate "$RUN_BIN" "$@" ); }

step "1. an estate applies under the published policy"
bucket_up "$BUCKET" || fail "objecttags" "could not create the bucket"
role_with_policy smoke-tagged-estate "$("$POLICY_RENDERER" smoke-tagged "$BUCKET")" "$BUCKET" || fail "objecttags" "could not create the estate's role"
mkdir -p "$SMOKE_WORK/est"
cat > "$SMOKE_WORK/est/main.tf" <<TFEOF
terraform {
  live {
    estate = "smoke-tagged"

    record_store "s3" {
      bucket = "$BUCKET"
      region = "$AWS_REGION"
    }
  }
}

resource "terraform_data" "effect" {
  for_each = toset(["a.b", "plain"])
  input    = each.key
}

output "shared" {
  value = "v"
}
TFEOF
( cd "$SMOKE_WORK/est" && "$RUN_BIN" init -input=false -no-color >/dev/null 2>&1 ) || fail "objecttags" "init failed"
cmd "choudoufu apply -auto-approve   # as the estate's role"
A_OUT="$(run_as_estate apply -auto-approve -input=false -no-color 2>&1)" && A_RC=0 || A_RC=$?

if [ "${BREAK:-0}" = "1" ]; then
  [ "$A_RC" != "0" ] || fail "objecttags" "a binary that sends no tags applied under the published policy, so the policy does not enforce tagging: $A_OUT"
  denied "$(flat <<< "$A_OUT")" || fail "objecttags" "the untagged apply failed, but not on a denial: $A_OUT"
  LEFT="$(aws s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu- --query 'length(Contents || `[]`)' --output text)"
  [ "$LEFT" = "0" ] || fail "objecttags" "the untagged binary still wrote $LEFT object(s)"
  flat <<< "$A_OUT" | grep -oE 'AccessDenied[^"]*s3:PutObject' | head -1 | mask | evidence
  proof "caught - without its tags the store cannot write a single object. Under this policy tagging is not a convention."
  exit 0
fi

[ "$A_RC" = "0" ] || fail "objecttags" "the estate could not apply under the published policy: $A_OUT"
grep -q "Resources: 2 added" <<< "$A_OUT" || fail "objecttags" "the apply: $A_OUT"
N=0
while read -r key; do
  [ -n "$key" ] || continue
  TAGS="$(aws s3api get-object-tagging --bucket "$BUCKET" --key "$key" --query 'TagSet[].[Key,Value]' --output text | sort | tr '\t' '=' | tr '\n' ' ')"
  grep -q "tofu-estate=smoke-tagged" <<< "$TAGS" || fail "objecttags" "$key does not carry tofu-estate=smoke-tagged: [$TAGS]"
  case "$key" in
    tofu-records/*/terraform_data/*) grep -q "tofu-address=terraform_data.effect:" <<< "$TAGS" || fail "objecttags" "record $key carries no tofu-address: [$TAGS]" ;;
  esac
  echo "$key   $TAGS" | evidence
  N=$((N+1))
done < <(aws s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu- --query 'Contents[].Key' --output json | jq -r '.[]')
[ "$N" -ge 5 ] || fail "objecttags" "only $N objects were checked; expected the sentinel, the hint, an output and two records"
proof "$N objects, every one tagged with its estate, and each record with the marker form of its address (a.b is a@db, the same escape the resource's own tag uses)."

step "2. a record tagged as another estate's is refused to this one"
explain \
  "One record is retagged out of band as tofu-estate=someone-else, under" \
  "this estate's own prefix. The role's prefix scope still reaches it. The" \
  "tag is now the only thing that says it is not this estate's, and the" \
  "run must stop and name the record rather than plan as if it were gone."
VICTIM="$(grep 'terraform_data/' <<< "$(aws s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-tagged/terraform_data/ --query 'Contents[].Key' --output json | jq -r '.[]')" | head -1)"
aws s3api put-object-tagging --bucket "$BUCKET" --key "$VICTIM" --tagging 'TagSet=[{Key=tofu-estate,Value=someone-else}]' >/dev/null || fail "objecttags" "could not retag the record"
cmd "aws s3api get-object ...   # as the estate's role, the retagged record"
G_OUT="$(as_role smoke-tagged-estate aws s3api get-object --bucket "$BUCKET" --key "$VICTIM" "$SMOKE_WORK/o" 2>&1)" && fail "objecttags" "the estate's role read a record tagged as another estate's: $G_OUT"
denied "$G_OUT" || fail "objecttags" "the read failed, but not on a denial: $G_OUT"
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # as the estate's role"
P_OUT="$(run_as_estate plan -input=false -no-color 2>&1)" && fail "objecttags" "the plan SUCCEEDED with a record the role may not read: $P_OUT"
grep -qE 'Plan: [1-9][0-9]* to add' <<< "$P_OUT" && fail "objecttags" "the run rendered a plan that re-creates the resource whose record it could not read: $P_OUT"
grep -q "$(basename "$VICTIM")" <<< "$(flat <<< "$P_OUT")" || fail "objecttags" "the refusal does not name the record it could not read: $P_OUT"
grep -E '^.?Error:' <<< "$P_OUT" | head -1 | evidence
proof "denied by the tag under its own prefix, and the run refused by naming the record. A record it may not read is not a record that is gone."

step "3. the tag put back, and the estate is whole"
aws s3api put-object-tagging --bucket "$BUCKET" --key "$VICTIM" --tagging 'TagSet=[{Key=tofu-estate,Value=smoke-tagged}]' >/dev/null
R_OUT="$(run_as_estate plan -input=false -no-color 2>&1)" || fail "objecttags" "the plan failed after the tag was restored: $R_OUT"
grep -q "No changes." <<< "$R_OUT" || fail "objecttags" "the plan was not empty after the tag was restored: $R_OUT"
D_OUT="$(run_as_estate apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "objecttags" "teardown failed: $D_OUT"
grep -q "Resources: 0 added, 0 changed, 2 destroyed" <<< "$D_OUT" || fail "objecttags" "teardown did not destroy both instances: $D_OUT"
proof "plan empty again, and both instances destroyed by the role."

echo "  What you watched: every object the estate wrote carrying its tag, a"
echo "  record retagged as someone else's refused to the estate's own role,"
echo "  and the run naming that record instead of planning around it."
