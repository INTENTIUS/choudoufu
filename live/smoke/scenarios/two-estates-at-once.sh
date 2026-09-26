# two-estates-at-once
# CLAIM 43 - Two estates in one account apply at the same moment and both finish clean: the estate boundary is what keeps them apart, not a queue. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/twoestates"
mkdir -p "$SMOKE_WORK/a" "$SMOKE_WORK/b"; export SMOKE_WORK

# write_estate <dir> <estate> <role-name>. Both copies declare the SAME
# resource address (aws_iam_role.svc) and read the SAME data source (the
# account identity) - only the estate differs between them, which is the
# whole point: the address and the shared read are not what keeps two
# estates apart.
write_estate() {
  cat > "$1/main.tf" <<TFEOF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "$2"
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

data "aws_caller_identity" "current" {}

resource "aws_iam_role" "svc" {
  name = "$3"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17",
    Statement = [{ Effect = "Allow", Principal = { Service = "ec2.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

output "account_id" {
  value = data.aws_caller_identity.current.account_id
}
TFEOF
}

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

ESTATE_A=smoke-concurrent-a
ESTATE_B=smoke-concurrent-b
if [ "${BREAK:-0}" = "1" ]; then
  # BREAK control: collapse the one thing that told the two apart. The
  # address, the shared data-source read and the account all stay identical -
  # only the estate value moves.
  ESTATE_B="$ESTATE_A"
fi

step "the claim"
explain \
  "Two estates in one account are not two writers of one thing: they own" \
  "disjoint resources, so there is nothing between them to serialize." \
  "What actually keeps them apart is the tofu-estate value each root" \
  "carries - not a queue, not a lock, not which one started first. Both" \
  "roots below declare the identical resource address (aws_iam_role.svc)" \
  "and read the identical data source (the account's own identity);" \
  "only the estate differs, and that difference is doing all the work."

write_estate "$SMOKE_WORK/a" "$ESTATE_A" "smoke-concurrent-a-role"
write_estate "$SMOKE_WORK/b" "$ESTATE_B" "smoke-concurrent-b-role"
( cd "$SMOKE_WORK/a" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "twoestates" "init failed in a"
( cd "$SMOKE_WORK/b" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "twoestates" "init failed in b"

step "1. both estates apply at the same moment"
cmd "(apply in a &) ; (apply in b &) ; wait"
WALL_START="$(date +%s)"
( cd "$SMOKE_WORK/a" && { date +%s > "$SMOKE_WORK/a.start"; chdf apply -auto-approve -input=false -no-color > "$SMOKE_WORK/a.out" 2>&1; echo $? > "$SMOKE_WORK/a.rc"; date +%s > "$SMOKE_WORK/a.end"; } ) &
( cd "$SMOKE_WORK/b" && { date +%s > "$SMOKE_WORK/b.start"; chdf apply -auto-approve -input=false -no-color > "$SMOKE_WORK/b.out" 2>&1; echo $? > "$SMOKE_WORK/b.rc"; date +%s > "$SMOKE_WORK/b.end"; } ) &
wait
WALL_END="$(date +%s)"

A_START="$(cat "$SMOKE_WORK/a.start")"; B_START="$(cat "$SMOKE_WORK/b.start")"
A_END="$(cat "$SMOKE_WORK/a.end")"; B_END="$(cat "$SMOKE_WORK/b.end")"
A_RC="$(cat "$SMOKE_WORK/a.rc")"; B_RC="$(cat "$SMOKE_WORK/b.rc")"
A_OUT="$(cat "$SMOKE_WORK/a.out")"; B_OUT="$(cat "$SMOKE_WORK/b.out")"
START_SKEW=$(( A_START > B_START ? A_START - B_START : B_START - A_START ))
[ "$START_SKEW" -le 1 ] \
  || fail "twoestates" "estate a started at $A_START and estate b at $B_START, more than a second apart - this did not race"
WALL=$(( WALL_END - WALL_START ))
SERIAL_SUM=$(( (A_END - A_START) + (B_END - B_START) ))
echo "start skew: ${START_SKEW}s (a=$A_START b=$B_START)   wall clock: ${WALL}s   sum of each apply's own time: ${SERIAL_SUM}s" | evidence

if [ "${BREAK:-0}" != "1" ]; then
  [ "$A_RC" -eq 0 ] || fail "twoestates" "estate a's apply failed: $A_OUT"
  [ "$B_RC" -eq 0 ] || fail "twoestates" "estate b's apply failed: $B_OUT"
  grep -q "Resources: 1 added" <<< "$A_OUT" || fail "twoestates" "estate a did not create exactly its own role: $A_OUT"
  grep -q "Resources: 1 added" <<< "$B_OUT" || fail "twoestates" "estate b did not create exactly its own role: $B_OUT"
  grep -q "smoke-concurrent-b-role" <<< "$A_OUT" && fail "twoestates" "estate a's output names estate b's role - the boundary leaked"
  grep -q "smoke-concurrent-a-role" <<< "$B_OUT" && fail "twoestates" "estate b's output names estate a's role - the boundary leaked"
  [ "$WALL" -le "$SERIAL_SUM" ] \
    || fail "twoestates" "wall clock (${WALL}s) exceeded the sum of the two applies' own time (${SERIAL_SUM}s) - they did not overlap"
  proof "both applies exited 0, each named only its own role, and the wall clock (${WALL}s) is well under the serial sum (${SERIAL_SUM}s) - they ran together, not queued."

  step "2. each estate re-plans clean"
  cmd "choudoufu plan   # in a, then in b"
  PA="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1)" || fail "twoestates" "estate a's replan failed: $PA"
  grep -q "No changes." <<< "$PA" || fail "twoestates" "estate a did not converge: $PA"
  PB="$(cd "$SMOKE_WORK/b" && chdf plan -input=false -no-color 2>&1)" || fail "twoestates" "estate b's replan failed: $PB"
  grep -q "No changes." <<< "$PB" || fail "twoestates" "estate b did not converge: $PB"
  grep -E 'No changes\.' <<< "$PA" | head -1 | evidence
  grep -E 'No changes\.' <<< "$PB" | head -1 | evidence
  proof "neither estate has anything left to do. The other estate's role, its data-source read, and the shared account were never in question."

  step "3. teardown"
  cmd "choudoufu apply -destroy -auto-approve   # in a, then in b"
  DA="$(cd "$SMOKE_WORK/a" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "twoestates" "estate a's teardown failed: $DA"
  destroyed_exactly twoestates 1 "$DA"
  DB="$(cd "$SMOKE_WORK/b" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "twoestates" "estate b's teardown failed: $DB"
  destroyed_exactly twoestates 1 "$DB"
  proof "both estates gone, each by its own apply."

  echo "  What you watched: two estates, one account, one shared data-source"
  echo "  read and one identical resource address between them, applied at"
  echo "  the same moment - both finished clean, each blind to the other,"
  echo "  in well under the time either would have taken run twice in a row."
else
  step "BREAK control - the same address, the same account, now the same estate too"
  explain \
    "Everything else is unchanged: two roots, two applies started together," \
    "one shared data-source read, one identical resource address. Only the" \
    "estate value now matches. The two live roles still exist separately -" \
    "AWS gave them different names - but they now carry the identical" \
    "ownership marker (estate + address), which is exactly the ambiguity" \
    "live/MARKERS.md refuses rather than guesses at. If a plan reading" \
    "reality back stays quiet about that, the estate value was never the" \
    "boundary the claim above rests on."
  echo "estate a's apply exited $A_RC, estate b's apply exited $B_RC" | evidence
  [ "$A_RC" -eq 0 ] && [ "$B_RC" -eq 0 ] \
    || echo "  (at least one apply itself refused or failed under the shared estate - see its output below)" | evidence

  # named_disagreement <output> <this-role> <other-role>: true when the plan
  # names BOTH live roles rather than reading clean, whether that is
  # discovery's own two-live-claimants collision (live/MARKERS.md) or a
  # displaced-object warning naming the foreign role by its live identity
  # beside the identity this root's own config computes. Either is a refusal
  # to guess; only silence would mean the estate value was never load-bearing.
  named_disagreement() {
    local out="$1" mine="$2" other="$3"
    grep -qiE "collision|displaced from the address" <<< "$out" || return 1
    grep -q "$mine" <<< "$out" && grep -q "$other" <<< "$out"
  }

  step "the disagreement, read back by a plan"
  cmd "choudoufu plan   # in a, with b's role now carrying a's estate and address"
  COLL_A="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1 || true)"
  cmd "choudoufu plan   # in b, with a's role now carrying b's estate and address"
  COLL_B="$(cd "$SMOKE_WORK/b" && chdf plan -input=false -no-color 2>&1 || true)"

  grep -E "collision|Warning: Live resource displaced|carries estate|identity this configuration computes" <<< "$COLL_A" | head -4 | evidence

  if ! named_disagreement "$COLL_A" "smoke-concurrent-a-role" "smoke-concurrent-b-role" \
    && ! named_disagreement "$COLL_B" "smoke-concurrent-b-role" "smoke-concurrent-a-role"; then
    fail "twoestates" "BREAK: with both roots sharing one tofu-estate, neither plan named the other estate's role - the disagreement was never detected, so the boundary claim above proves nothing: a=$COLL_A / b=$COLL_B"
  fi
  proof "caught - with one tofu-estate value shared by both roots, the next plan names both live roles rather than reading clean. The address and the shared read were never the boundary; the estate value was."
  exit 0
fi
