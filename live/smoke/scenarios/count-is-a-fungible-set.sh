# count-is-a-fungible-set
# CLAIM 11 - A count pool is a fungible set: slot markers hold it together, so it scales down by removing one member and rebuilding nothing; a count block the configuration NAMES is the other kind and carries no slot at all; stripping a slot that belongs makes the run refuse rather than guess, and stamping one that does not fails the read. ~3 min.

SMOKE_WORK="$SMOKE_WORKROOT/count"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp "$ROOT/live/e2e/estate-block/versions.tf" "$SMOKE_WORK/"
cat > "$SMOKE_WORK/pool.tf" <<'TFEOF'
resource "aws_eip" "pool" {
  count  = 3
  domain = "vpc"
}
TFEOF
pool() { awsl ec2 describe-addresses \
  --query 'Addresses[?Tags[?Key==`tofu-estate`&&Value==`stateless-e2e-block`]].[AllocationId,Tags[?Key==`tofu-slot`]|[0].Value,Tags[?Key==`tofu-address`]|[0].Value]' \
  --output text | sort -k2; }
# settle waits until the tagging index the sweep uses reflects a manufactured
# tag change - floci's tagging API lags a raw create-tags/delete-tags, and a
# plan that raced it would read stale markers (the #756 lesson).
settle() { local want="$1" i; for i in $(seq 1 20); do
  if awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=stateless-e2e-block" \
    --query 'ResourceTagMappingList[].Tags' --output text 2>/dev/null | grep -q "$want"; then return 0; fi
  sleep 1; done; return 0; }

# The step-5 read (#976) goes through the plain AWS CLI and nothing else:
# describe-log-groups to find a group, list-tags-for-resource to read its
# whole tag set. Real AWS returns a log group arn with a trailing ":*" and
# floci returns it without, so the suffix is stripped either way.
lg_arn() { awsl logs describe-log-groups --log-group-name-prefix "$1" \
  --query "logGroups[?logGroupName=='$1']|[0].arn" --output text; }
lg_keys() { awsl logs list-tags-for-resource --resource-arn "${1%:\*}" \
  --query 'keys(tags)' --output text | tr '\t' ' ' | tr ' ' '\n' | sort | tr '\n' ' ' | sed 's/ $//'; }
lg_tag() { awsl logs list-tags-for-resource --resource-arn "${1%:\*}" \
  --query "tags.\"$2\"" --output text; }

# named_tag_check is step 5's assertion about the count block the
# configuration NAMES: the whole tag key set by literal value, then
# tofu-estate and tofu-address by value. It is a function because BOTH arms
# run THIS code - the ordinary arm expects it to hold, and the BREAK_SLOT
# arm expects the identical check to catch a slot stamped out of band. A
# mirrored copy in the break arm would prove nothing about this one. On a
# mismatch it prints the reason and returns non-zero.
named_tag_check() {
  local i arn keys
  for i in 0 1; do
    arn="$(lg_arn "/svc/$i")"
    if [ -z "$arn" ] || [ "$arn" = "None" ]; then
      echo "/svc/$i is not there at all - the named count member was never created"; return 1
    fi
    keys="$(lg_keys "$arn")"
    if [ "$keys" != "purpose tofu-address tofu-estate" ]; then
      echo "/svc/$i carries tag keys [$keys], not exactly [purpose tofu-address tofu-estate] - the configuration names this instance, so nothing is left for a slot to decide and it must carry no tofu-slot at all; the name_prefix log groups beside it, read through the identical call, do carry one"
      return 1
    fi
    if [ "$(lg_tag "$arn" tofu-estate)" != "stateless-e2e-block" ]; then
      echo "/svc/$i does not carry tofu-estate=stateless-e2e-block"; return 1
    fi
    if [ "$(lg_tag "$arn" tofu-address)" != "aws_cloudwatch_log_group.named:$i" ]; then
      echo "/svc/$i does not carry tofu-address=aws_cloudwatch_log_group.named:$i - the index in the address is what says which instance it is when there is no slot"; return 1
    fi
  done
  return 0
}

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "A count pool is a fungible SET when nothing in the configuration says" \
  "which live resource is which. Its members are then interchangeable:" \
  "nothing about instance 2 distinguishes it from instance 0. The" \
  "positional index aws_eip.pool[1] is where a member sits, not what it" \
  "is. What it is, is a tofu-slot marker: a stable id minted once and" \
  "never reused. The slot holds the set together across a scale change." \
  "Shrinking the pool therefore removes one member and rebuilds nothing," \
  "where stock renumbers and recreates the tail. Strip a slot where no" \
  "local record vouches for the member, and the set has two rules for" \
  "naming its members, so the run refuses rather than guess. A count" \
  "block whose members the configuration itself NAMES is the other kind" \
  "and carries no slot at all; step 5 reads both kinds back off the" \
  "cloud with the plain AWS CLI."

step "1. stand up a pool of three"
cmd "choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "count" "init failed"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "count" "apply failed"
pool | evidence
[ "$(pool | grep -c .)" = "3" ] || fail "count" "expected three members"
[ "$(pool | awk '{print $2}' | sort -u | grep -c .)" = "3" ] || fail "count" "the three members do not carry three distinct slots"
proof "three interchangeable members, three distinct slots. The slot is the name; the index is just today's seat."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - lose the local record, strip one member's slot; the set loses its name"
  explain \
    "Beside a configuration that has applied, a missing slot is a repair," \
    "not a guess: the local record already names every member, so the" \
    "plan re-stamps the slot from it. The stock condition is the one" \
    "where nothing but the tags names a member. To manufacture it, delete" \
    "the local files, cache and record store both, which the storage page" \
    "calls churn and never a lost estate, then delete the tofu-slot tag" \
    "from one member. Now two members answer 'which" \
    "instance am I?' by slot and one has no answer at all. That is two" \
    "rules for one set, and the run must REFUSE naming the disagreement" \
    "rather than bind the odd member by a guess. A clean plan here would" \
    "mean the slot was never what bound the set."
  cmd "rm -rf .terraform* terraform.tfstate* .tofu-records ; choudoufu init ; aws ec2 delete-tags --tags Key=tofu-slot ; choudoufu plan"
  [ -d "$SMOKE_WORK/.tofu-records" ] || fail "count" "BREAK: expected the record store beside the module before the wipe"
  rm -rf "$SMOKE_WORK"/.terraform "$SMOKE_WORK"/.terraform.lock.hcl "$SMOKE_WORK"/terraform.tfstate* "$SMOKE_WORK"/.tofu-records
  ( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "count" "BREAK: init after the wipe failed"
  VICTIM="$(pool | awk 'NR==2{print $1}')"; VSLOT="$(pool | awk 'NR==2{print $2}')"
  awsl ec2 delete-tags --resources "$VICTIM" --tags Key=tofu-slot >/dev/null 2>&1 || fail "count" "BREAK: could not strip a slot"
  # settle: the sweep reads the tagging index, which lags a raw delete-tags
  # (the #756 lesson), so wait until the index shows two slots, not three.
  for i in $(seq 1 30); do
    SLOTS_NOW="$(awsl resourcegroupstaggingapi get-resources --tag-filters Key=tofu-estate,Values=stateless-e2e-block \
      --query 'length(ResourceTagMappingList[].Tags[?Key==`tofu-slot`][])' --output text 2>/dev/null || echo 3)"
    [ "$SLOTS_NOW" = "2" ] && break; sleep 1
  done
  BP="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if ! grep -qiE "partial slot markers|disagree about slot" <<< "$BP"; then
    fail "count" "BREAK: the plan did not refuse the half-slotted set by name: $BP"
  fi
  grep -iE "partial slot markers|disagree about slot" <<< "$BP" | head -1 | evidence
  proof "caught - with no record to vouch for it, a set that carries slots on some members and not others has two answers, and the run stops. Slots are what bind the set."
  awsl ec2 create-tags --resources "$VICTIM" --tags "Key=tofu-slot,Value=$VSLOT" >/dev/null 2>&1 || true
  ( cd "$SMOKE_WORK" && sed_i pool.tf 's/count  = 3/count  = 0/'; chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "2. capture the survivor at the middle seat"
MID="$(pool | awk '$2=="1"{print $1}')"
[ -n "$MID" ] || fail "count" "no member holds slot 1"
echo "slot 1 lives on $MID" | evidence
proof "watch $MID - its seat is about to change and its identity must not."

step "3. scale to two - one removed, nothing rebuilt"
explain \
  "count drops to 2. The set matcher keeps the lowest slots and drops" \
  "the highest, which is the only rule that leaves every survivor on the" \
  "seat it already had. Expect exactly one destroy and zero creates - a" \
  "fungible shrink, not a renumber-and-rebuild."
cmd "count = 2 ; choudoufu plan ; choudoufu apply -auto-approve"
sed_i "$SMOKE_WORK/pool.tf" 's/count  = 3/count  = 2/'
P="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "count" "scale-down plan failed: $P"
D="$(grep -cE '^[[:space:]]*# .*will be destroyed' <<< "$P" || true)"
C="$(grep -cE '^[[:space:]]*# .*will be created' <<< "$P" || true)"
echo "plan: $D to destroy, $C to create" | evidence
[ "$D" = "1" ] || fail "count" "scale-down proposed $D destroys, not 1 - the pool renumbered: $P"
[ "$C" = "0" ] || fail "count" "scale-down proposed $C creates - a fungible shrink creates nothing: $P"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "count" "scale-down apply failed"
proof "one destroy, zero creates. The set shrank by exactly one member."

step "4. the middle survivor is the same live object"
STILL="$(pool | awk -v a="$MID" '$1==a{print $1}')"
[ "$STILL" = "$MID" ] || fail "count" "$MID did not survive - it was rebuilt, the stock failure this claim excludes"
pool | evidence
proof "$MID is still here. Its seat moved and its identity did not - the whole difference between a slot and a subscript."

step "5. both kinds of count instance, read back with the plain AWS CLI"
explain \
  "Not every count block declares a fungible set, and which kind it is" \
  "is visible in the tags. Two count blocks of the SAME type go up side" \
  "by side, differing in exactly one property. One names its members -" \
  "name = \"/svc/\${count.index}\", the shape lint admits because every" \
  "instance renders a distinct name - so the configuration already says" \
  "which live resource is which, nothing is left for a slot to decide," \
  "and none is written: that member binds by tofu-address, the index in" \
  "the address being what says which instance it is. The other leaves" \
  "the name to the provider (name_prefix), so nothing in the" \
  "configuration tells its members apart and the slot is the only thing" \
  "that does. Every tag is read back off the live log groups with the" \
  "plain AWS CLI, no choudoufu in the read, through the identical call -" \
  "so a tofu-slot coming back missing on one pair cannot be a broken" \
  "query when the same query answers on the pair beside it."
cmd "choudoufu apply -auto-approve ; aws logs list-tags-for-resource --resource-arn <each log group>"
cat > "$SMOKE_WORK/named.tf" <<'TFEOF'
resource "aws_cloudwatch_log_group" "named" {
  count = 2

  # The configuration itself names each member, so instance k is the log
  # group called /svc/k and can be nothing else. The purpose tag is here so
  # the assertion below is a whole-key-set comparison and not a subset
  # check: a set with an extra tag in it still has to match exactly.
  name = "/svc/${count.index}"
  tags = { purpose = "count-member" }
}

resource "aws_cloudwatch_log_group" "fungible" {
  count = 2

  # The same type, one property different: the provider mints the name, so
  # nothing in the configuration says which of these two is instance 0.
  name_prefix = "/svc-pool-"
}
TFEOF
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "count" "the two-kinds apply failed"

FUNG=""
for LG in $(awsl logs describe-log-groups --log-group-name-prefix "/svc-pool-" --query 'logGroups[].logGroupName' --output text); do
  LGA="$(lg_arn "$LG")"
  LGK="$(lg_keys "$LGA")"
  [ "$LGK" = "tofu-address tofu-estate tofu-slot" ] || fail "count" "$LG carries tag keys [$LGK], not exactly [tofu-address tofu-estate tofu-slot] - nothing in the configuration names this member, so a slot is the only thing that can say which instance it is"
  FUNG="$FUNG$(lg_tag "$LGA" tofu-address)=$(lg_tag "$LGA" tofu-slot)
"
done
FUNG="$(printf '%s' "$FUNG" | sort | tr '\n' ' ' | sed 's/ $//')"
[ "$FUNG" = "aws_cloudwatch_log_group.fungible:0=0 aws_cloudwatch_log_group.fungible:1=1" ] || fail "count" "the name_prefix pair reads [$FUNG], not slots 0 and 1 off the two live log groups"
echo "name_prefix pair (nothing names them): $FUNG" | evidence

if [ "${BREAK_SLOT:-0}" = "1" ]; then
  step "BREAK_SLOT control - stamp a tofu-slot onto the named member; the absence assertion must catch it"
  explain \
    "This claim's other control (BREAK=1) strips a slot that belongs." \
    "This one is its mirror, because the assertion it tests is an" \
    "ABSENCE: the only corruption that can test an absence is a tag that" \
    "should not be there. tofu-slot=0 is stamped onto /svc/0 out of band" \
    "with the AWS CLI, and then the IDENTICAL check runs - the same" \
    "function, not a mirrored copy of it. If it still passes, it was" \
    "reading nothing."
  cmd "aws logs tag-resource --resource-arn <.../log-group:/svc/0> --tags tofu-slot=0"
  A0="$(lg_arn "/svc/0")"
  awsl logs tag-resource --resource-arn "${A0%:\*}" --tags tofu-slot=0 >/dev/null 2>&1 \
    || awsl logs tag-log-group --log-group-name "/svc/0" --tags tofu-slot=0 >/dev/null 2>&1 \
    || fail "count" "BREAK_SLOT: could not stamp a slot out of band"
  # No settle wait here: this reads the log group's own tags with
  # list-tags-for-resource, not the tagging index a plan sweeps, so there
  # is no lagging index to race (the #756 lesson is about the index).
  [ "$(lg_keys "$A0")" = "purpose tofu-address tofu-estate tofu-slot" ] \
    || fail "count" "BREAK_SLOT: the out-of-band stamp did not land, so the check below would pass for the wrong reason"
  if REASON="$(named_tag_check)"; then
    fail "count" "BREAK_SLOT: /svc/0 carries a tofu-slot it must not and the check passed anyway - it asserts nothing"
  fi
  echo "$REASON" | evidence
  proof "caught - the absence is asserted by literal value, so a slot that should not be there fails the very check that passes without it."
else
  if ! REASON="$(named_tag_check)"; then
    fail "count" "$REASON"
  fi
  echo "named pair: /svc/0 and /svc/1 carry [purpose tofu-address tofu-estate] - tofu-estate and tofu-address by value, and no tofu-slot" | evidence
  P2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "count" "the replan over both kinds failed: $P2"
  grep -q "No changes" <<< "$P2" || fail "count" "the replan over both kinds was not empty, so one of the two kinds did not bind: $P2"
  proof "one type, two kinds of set: the named pair binds by tofu-address carrying no slot, the name_prefix pair binds by slots 0 and 1, and the next plan is empty, so both bound."
fi

step "6. teardown"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "count" "teardown failed"
proof "the pool is gone."

echo "  What you watched: a count pool shrink by removing one member and"
echo "  keeping the rest as the exact same live objects, because a stable"
echo "  slot marker names each member of a fungible set. Stock renumbers"
echo "  and rebuilds the tail; here the tail does not exist. And then the"
echo "  boundary of that: two count blocks of one type, read back through"
echo "  one AWS CLI call - the one whose members the configuration names"
echo "  carries tofu-estate and tofu-address and no slot, the one whose"
echo "  names the provider mints carries slots 0 and 1, and both bind."
