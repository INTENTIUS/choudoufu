# the-estate-answers-in-the-present-tense
# CLAIM 41 - The estate answers in the present tense: after a change made out of band, its own tags and a live describe give the answer as it is now, and the state cache gives it as of the last apply. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/present"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-present"

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
AMI="$(awsl ec2 describe-images --query 'Images[0].ImageId' --output text)"
[ -n "$AMI" ] && [ "$AMI" != "None" ] || fail "present" "the emulator offers no AMI to launch an instance from"

cat > "$SMOKE_WORK/main.tf" <<TFEOF
terraform {
  live {
    estate = "$ESTATE"
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

resource "aws_vpc" "main" {
  cidr_block = "10.77.0.0/16"
}

resource "aws_subnet" "app" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.77.1.0/24"
}

resource "aws_security_group" "web" {
  name        = "$ESTATE-web"
  description = "attached to the estate's instance"
  vpc_id      = aws_vpc.main.id
}

resource "aws_security_group" "db" {
  name        = "$ESTATE-db"
  description = "attached to nothing until the world moves"
  vpc_id      = aws_vpc.main.id
}

resource "aws_security_group" "spare" {
  name        = "$ESTATE-spare"
  description = "attached to nothing throughout"
  vpc_id      = aws_vpc.main.id
}

resource "aws_instance" "app" {
  ami                    = "$AMI"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.app.id
  vpc_security_group_ids = [aws_security_group.web.id]
}
TFEOF
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"

# ask_live prints, one group name per line and sorted, which of this
# estate's security groups nothing uses. No choudoufu runs: the estate's
# groups come from the tagging API by the tofu-estate tag, and what uses
# them comes from a describe, made now, of every instance that is not
# terminated and every network interface in the account.
ask_live() {
  local ids used id name
  ids="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" \
    --query 'ResourceTagMappingList[].ResourceARN' --output text | tr '\t' '\n' \
    | sed -n 's|.*:security-group/||p' | sort -u)"
  [ -n "$ids" ] || { echo "ERROR: the tagging API lists no security group tagged $ESTATE"; return 0; }
  used="$( { awsl ec2 describe-instances --filters Name=instance-state-name,Values=pending,running,stopping,stopped \
      --query 'Reservations[].Instances[].SecurityGroups[].GroupId' --output text
    awsl ec2 describe-network-interfaces --query 'NetworkInterfaces[].Groups[].GroupId' --output text; } \
    | tr '\t' '\n' | grep -v '^None$' | sort -u)"
  for id in $ids; do
    grep -qxF -- "$id" <<< "$used" && continue
    name="$(awsl ec2 describe-security-groups --group-ids "$id" --query 'SecurityGroups[0].GroupName' --output text)"
    echo "$name"
  done | sort
}

# ask_cache prints the same answer from the state cache alone: the
# security groups it holds, minus every group an instance it holds names. It reads a file and calls nothing.
ask_cache() {
  python3 - "$CACHE" <<'PYEOF'
import json, sys
st = json.load(open(sys.argv[1]))
groups, used = {}, set()
for r in st.get("resources", []):
    if r.get("mode") != "managed":
        continue
    for inst in r.get("instances", []):
        a = inst.get("attributes", {})
        if r["type"] == "aws_security_group":
            groups[a["id"]] = a["name"]
        elif r["type"] == "aws_instance":
            used.update(a.get("vpc_security_group_ids") or [])
            used.update(a.get("security_groups") or [])
for name in sorted(groups[g] for g in groups if g not in used):
    print(name)
PYEOF
}
oneline() { tr '\n' ' ' <<< "$1" | sed 's/ $//'; }

step "1. stand the estate up and ask the question"
explain \
  "Three security groups and one instance. The instance uses" \
  "web, so the answer to \"which of this estate's security groups are" \
  "attached to nothing\" is db and spare. It is asked two ways. Live:" \
  "the tagging API for the groups carrying tofu-estate=$ESTATE, and a" \
  "describe of every instance and interface, with no choudoufu in the loop." \
  "Stored: the state cache the apply just wrote, read as a file. Right" \
  "after the apply the two must agree, or the queries themselves differ."
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "present" "init failed"
APPLY="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "present" "apply failed: $APPLY"
grep -q 'Resources: 6 added' <<< "$APPLY" || fail "present" "the apply did not create the six resources: $APPLY"
[ -f "$CACHE" ] || fail "present" "the apply wrote no state cache at $CACHE, so there is no stored answer to compare"
LIVE0="$(ask_live)"; CACHE0="$(ask_cache)"
echo "live:  $(oneline "$LIVE0")" | evidence
echo "cache: $(oneline "$CACHE0")" | evidence
[ "$LIVE0" = "$(printf '%s\n' "$ESTATE-db" "$ESTATE-spare")" ] || fail "present" "the live answer after the apply is not db and spare: $LIVE0"
[ "$CACHE0" = "$LIVE0" ] || fail "present" "the cache and the live answer disagree before anything moved, so the two queries are not asking the same question: cache [$CACHE0], live [$LIVE0]"
proof "both ways answer db and spare."

MOVE=1
if [ "${BREAK:-0}" = "1" ]; then
  MOVE=0
  step "BREAK control - leave the world where it is"
  explain \
    "The divergence below means something only if the comparison can come" \
    "out equal. So this arm skips the out-of-band move and asks both ways" \
    "again. They must agree. If the scenario still reports a divergence," \
    "the two queries disagree by construction and step 3 proves nothing."
fi

if [ "$MOVE" = "1" ]; then
  step "2. move reality out of band"
  explain \
    "Someone swaps the instance's security group from web to db with the" \
    "AWS CLI." \
    "No choudoufu runs and no apply happens, so the cache is untouched. The" \
    "right answer is now web and spare."
  INST="$(python3 -c "import json,sys;st=json.load(open(sys.argv[1]));print([i['attributes']['id'] for r in st['resources'] if r['type']=='aws_instance' for i in r['instances']][0])" "$CACHE")"
  DBSG="$(awsl ec2 describe-security-groups --filters "Name=group-name,Values=$ESTATE-db" --query 'SecurityGroups[0].GroupId' --output text)"
  cmd "aws ec2 modify-instance-attribute --instance-id $INST --groups $DBSG"
  awsl ec2 modify-instance-attribute --instance-id "$INST" --groups "$DBSG" || fail "present" "the out-of-band move failed"
  NOW="$(awsl ec2 describe-instances --instance-ids "$INST" --query 'Reservations[0].Instances[0].SecurityGroups[].GroupName' --output text)"
  echo "the instance's groups now: $NOW" | evidence
  [ "$NOW" = "$ESTATE-db" ] || fail "present" "the instance does not use exactly db after the move: $NOW"
  proof "the instance uses db, and nothing choudoufu keeps has been told."
fi

step "3. ask again, both ways"
explain \
  "The same two queries, unchanged. The live one reads the platform now;" \
  "the cache answers as of the apply. The estate needs no stored copy to" \
  "answer this, because its tags are on the resources and the platform" \
  "describes them as they are."
cmd "aws resourcegroupstaggingapi get-resources --tag-filters Key=tofu-estate,Values=$ESTATE ; aws ec2 describe-instances"
LIVE1="$(ask_live)"; CACHE1="$(ask_cache)"
echo "live:  $(oneline "$LIVE1")" | evidence
echo "cache: $(oneline "$CACHE1")" | evidence
if [ "$MOVE" = "0" ]; then
  [ "$LIVE1" = "$CACHE1" ] || fail "present" "BREAK: nothing moved and the scenario still reports a divergence: cache [$CACHE1], live [$LIVE1]"
  [ "$LIVE1" = "$LIVE0" ] || fail "present" "BREAK: nothing moved and the live answer changed: [$LIVE0] then [$LIVE1]"
  proof "caught - with the world unmoved both ways answer $(oneline "$LIVE1"), so the divergence the main arm reports comes from the world moving, not from two queries that differ by construction."
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi
[ "$LIVE1" = "$(printf '%s\n' "$ESTATE-spare" "$ESTATE-web")" ] || fail "present" "the live answer after the move is not spare and web: $LIVE1"
[ "$CACHE1" = "$CACHE0" ] || fail "present" "the cache's answer changed with no choudoufu run, so it is not the stored copy this step claims to read: [$CACHE0] then [$CACHE1]"
[ "$LIVE1" != "$CACHE1" ] || fail "present" "the live answer and the cache agree after the world moved: [$LIVE1]"
proof "live says spare and web, the answer as it is. The cache still says db and spare, the answer as of the apply."

step "4. the next plan reads the present too"
explain \
  "The estate's own run does not consult the cache for this. The next" \
  "plan reads the instance live, sees db where the configuration says" \
  "web, and proposes the one in-place update that puts it back."
cmd "choudoufu plan"
PLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "present" "the plan failed: $PLAN"
{ grep -E 'aws_instance.app will be updated|^Plan:' <<< "$PLAN" || true; } | evidence
grep -q '# aws_instance.app will be updated in-place' <<< "$PLAN" || fail "present" "the plan does not propose updating the moved instance: $PLAN"
grep -q 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$PLAN" || fail "present" "the plan is not the one in-place change: $PLAN"
proof "the plan names the move, from the live read."

step "5. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "present" "teardown failed: $DOUT"
destroyed_exactly "present" 6 "$DOUT"
proof "the estate is gone."

echo "  What you watched: one question about an estate - which of its"
echo "  security groups are attached to nothing - asked from its tags and a"
echo "  live describe, and from the state cache. They agreed after the"
echo "  apply. After one out-of-band change, the live answer moved with the"
echo "  world and the cache kept the answer as of the apply; the estate's"
echo "  next plan read the present as well. This is about the estate's own"
echo "  resources. It is not account-wide gap analysis."
