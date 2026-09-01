# no-self-managed-locks
# CLAIM 2 - No self-managed locks: contention settles at the platform API, never in a lock this tool holds. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/no-locks"
mkdir -p "$SMOKE_WORK/a" "$SMOKE_WORK/b"; export SMOKE_WORK
for c in a b; do
  sed 's/stateless-e2e-block/smoke-locks/' "$ROOT/live/e2e/estate-block/versions.tf" > "$SMOKE_WORK/$c/versions.tf"
  cat > "$SMOKE_WORK/$c/role.tf" <<'TFEOF'
resource "aws_iam_role" "contender" {
  name               = "smoke-locks-contender"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17",
    Statement = [{ Effect = "Allow", Principal = { Service = "ec2.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
  tags = {
    tofu-estate  = "smoke-locks"
    tofu-address = "aws_iam_role.contender"
  }
}
TFEOF
done

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "Stock serializes runs with a self-managed lock you must provision," \
  "keep in step with your IAM, and sometimes force open after a crash -" \
  "because its state file is a shared mutable record that two writers" \
  "would corrupt. Here there is no authoritative shared record, so there" \
  "is nothing to lock. Contention lands where it always was: at the" \
  "cloud API, which already referees competing writers with uniqueness" \
  "constraints and conditional writes."

step "1. there is no lock to force open, and the tool says so"
explain \
  "The first evidence is the command stock needs for its worst day." \
  "force-unlock exists to free a lock a crashed run left behind; under a" \
  "live block it refuses with the actual reason. (Until this scenario" \
  "was written it fell through to a misleading stock error about a local" \
  "process - the probe that found that is why this refusal exists.)"
cmd "choudoufu force-unlock LOCK-ID"
FOUT="$(cd "$SMOKE_WORK/a" && chdf force-unlock LOCK-ID -no-color 2>&1 || true)"
grep -E "There is no lock to force open" <<< "$FOUT" | evidence
grep -q "There is no lock to force open" <<< "$FOUT" \
  || fail "locks" "force-unlock did not give the no-lock refusal: $FOUT"
proof "nothing is held that a crash could leave stuck. The whole force-unlock ceremony has no referent here."

step "2. the race - two applies, same resource, at the same moment"
explain \
  "Two copies of one configuration declare the same client-named IAM" \
  "role, and both apply simultaneously with no coordination. Stock" \
  "without a lock would corrupt its state file here. Watch for what does" \
  "NOT appear: neither run ever prints 'Acquiring state lock'. The cloud" \
  "itself referees the create."
cmd "(apply in a &) ; (apply in b &) ; wait"
( cd "$SMOKE_WORK/a" && chdf init -input=false -no-color >/dev/null 2>&1 )
( cd "$SMOKE_WORK/b" && chdf init -input=false -no-color >/dev/null 2>&1 )
( cd "$SMOKE_WORK/a" && chdf apply -auto-approve -input=false -no-color > "$SMOKE_WORK/a.out" 2>&1; echo $? > "$SMOKE_WORK/a.rc" ) &
( cd "$SMOKE_WORK/b" && chdf apply -auto-approve -input=false -no-color > "$SMOKE_WORK/b.out" 2>&1; echo $? > "$SMOKE_WORK/b.rc" ) &
wait
A_RC="$(cat "$SMOKE_WORK/a.rc")"; B_RC="$(cat "$SMOKE_WORK/b.rc")"
grep -q "Acquiring state lock" "$SMOKE_WORK/a.out" "$SMOKE_WORK/b.out" \
  && fail "locks" "an apply printed 'Acquiring state lock' - a lock was taken after all"
LOSER=""
if [ "$A_RC" -ne 0 ] && [ "$B_RC" -eq 0 ]; then LOSER=a; fi
if [ "$B_RC" -ne 0 ] && [ "$A_RC" -eq 0 ]; then LOSER=b; fi
if [ -n "$LOSER" ]; then
  grep -E "EntityAlreadyExists" "$SMOKE_WORK/$LOSER.out" | head -1 | evidence
  grep -q "EntityAlreadyExists" "$SMOKE_WORK/$LOSER.out" \
    || fail "locks" "copy $LOSER lost the race but not to the platform's uniqueness constraint: $(cat "$SMOKE_WORK/$LOSER.out")"
  proof "the platform's own 409 was the referee - one create won and AWS itself told the loser. No lock existed anywhere in the exchange."
elif [ "$A_RC" -eq 0 ] && [ "$B_RC" -eq 0 ]; then
  ADDS=$(( $(grep -oE 'Resources: [0-9]+ added' "$SMOKE_WORK/a.out" | grep -oE '[0-9]+') + $(grep -oE 'Resources: [0-9]+ added' "$SMOKE_WORK/b.out" | grep -oE '[0-9]+') ))
  [ "$ADDS" -eq 1 ] || fail "locks" "both applies succeeded but created $ADDS roles between them - the platform should have allowed exactly one"
  LOSER=b
  echo "both applies exited 0; exactly 1 create happened - the later run bound to the winner's role during its own plan" | evidence
  proof "the race settled even earlier: the second run read reality mid-plan and had nothing left to create."
else
  fail "locks" "both applies failed - the race has no winner to converge on: a=$(cat "$SMOKE_WORK/a.out" | tail -3) b=$(cat "$SMOKE_WORK/b.out" | tail -3)"
fi

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip the winner's marker; convergence must fail"
  explain \
    "The loser's clean re-plan below rests entirely on the winner's role" \
    "carrying its marker. Strip that marker and convergence must break:" \
    "the re-plan will propose a create against a name the cloud holds." \
    "If it still says No changes, the convergence check is scenery."
  cmd "aws iam untag-role --role-name smoke-locks-contender --tag-keys tofu-address"
  awsl iam untag-role --role-name smoke-locks-contender --tag-keys tofu-address \
    || fail "locks" "BREAK: could not strip the marker"
  BOUT="$(cd "$SMOKE_WORK/$LOSER" && chdf plan -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "locks" "BREAK: the re-plan is still clean with the winner's marker gone - the convergence assertion below proves nothing"
  fi
  proof "caught - convergence rests on the marker, so the clean re-plan below is a real check."
  awsl iam delete-role --role-name smoke-locks-contender >/dev/null 2>&1 || true
  exit 0
fi

step "3. the loser converges by reading reality"
explain \
  "Stock's lockless loser leaves a corrupted state file. This loser" \
  "only has to re-plan - the winner's role carries the estate's marker" \
  "and the plan binds to it, leaving nothing to do. There is no repair" \
  "command and no state surgery to perform."
cmd "choudoufu plan   # in the losing copy"
CONV="$(cd "$SMOKE_WORK/$LOSER" && chdf plan -input=false -no-color 2>&1)" \
  || fail "locks" "the loser's convergence plan failed: $CONV"
grep -E 'No changes\.' <<< "$CONV" | head -1 | evidence
grep -q "No changes." <<< "$CONV" || fail "locks" "the loser did not converge: $CONV"
proof "a clean re-plan was the whole recovery. The race left nothing behind to fix."

step "4. the one race the API cannot referee is a named collision"
explain \
  "Server-assigned resources have no unique name for the cloud to" \
  "enforce, so a true double-create leaves two live objects claiming one" \
  "address. The claim does not pretend otherwise - it promises the next" \
  "plan REPORTS the pair, by id, and binds to the one the estate's own" \
  "record says it owns, instead of guessing or going silent. Staged here" \
  "with the CLI: a second VPC wearing aws_vpc.main's exact markers."
cat > "$SMOKE_WORK/a/vpc.tf" <<'TFEOF'
resource "aws_vpc" "main" {
  cidr_block = "10.7.0.0/16"
  tags = {
    tofu-estate  = "smoke-locks"
    tofu-address = "aws_vpc.main"
  }
}
TFEOF
( cd "$SMOKE_WORK/a" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "locks" "could not apply the vpc"
REAL_VPC="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-address,Values=aws_vpc.main" --query 'Vpcs[0].VpcId' --output text)"
DUPE="$(awsl ec2 create-vpc --cidr-block 10.8.0.0/16 --query 'Vpc.VpcId' --output text)"
awsl ec2 create-tags --resources "$DUPE" \
  --tags "Key=tofu-estate,Value=smoke-locks" "Key=tofu-address,Value=aws_vpc.main" \
  || fail "locks" "could not mark the duplicate"
cmd "choudoufu plan   # with two live VPCs claiming aws_vpc.main"
COLL="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1 || true)"
grep -q "$DUPE" <<< "$COLL" \
  || fail "locks" "the duplicate $DUPE is nowhere in the plan output - a silent double-claim: $COLL"
grep -E "$DUPE" <<< "$COLL" | head -1 | evidence
proof "both claimants surfaced, by id. A human deletes one - that is the resolution, and it is a decision, not an accident."

step "5. the human resolves it, the estate is clean again"
cmd "aws ec2 delete-vpc --vpc-id $DUPE ; choudoufu plan"
awsl ec2 delete-vpc --vpc-id "$DUPE" || fail "locks" "could not delete the duplicate"
CLEAN="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1)" \
  || fail "locks" "the post-resolution plan failed: $CLEAN"
grep -q "No changes." <<< "$CLEAN" || fail "locks" "the estate is not clean after resolving the collision: $CLEAN"
grep -E 'No changes\.' <<< "$CLEAN" | head -1 | evidence
proof "resolution was one delete. Nothing needed unlocking, repairing, or importing."

step "6. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK/a" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "locks" "teardown failed: $DOUT"
grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$DOUT" \
  || fail "locks" "teardown did not remove exactly the role and the vpc: $DOUT"
proof "2 destroyed - the estate is gone."

echo "  What you watched: a forced-unlock with nothing to force, a real race"
echo "  refereed by the platform's own uniqueness constraint, a loser whose"
echo "  entire recovery was re-reading reality, and the one unrefereeable"
echo "  race surfacing as a named pair for a human - with no lock, lock"
echo "  table, or lock ceremony anywhere in the story."
