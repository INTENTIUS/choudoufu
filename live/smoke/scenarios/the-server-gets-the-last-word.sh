# the-server-gets-the-last-word
# CLAIM 15 (aws) - Apply exactly what was approved. ~4 min.
#
# This proof: The server gets the last word: the platform decides, after the
# plan, what it accepts and what it stores - a write the plan approved and
# the platform refuses is reported in the platform's own words with nothing
# changed and the approved plan file still applying unchanged once the
# refusal lifts, a rewrite of a declared field reads as the same perpetual
# drift stock reads while the estate keeps its marker, and a rewrite that
# strips the marker on the way in is named by the run that made it - the
# create warns that the marker it sent is not on the object the platform
# stored, and the adopting update that follows fails rather than reporting a
# change nothing kept.
#
# GitHub issue #1599. The AWS form of the first part: a refusal that arrives
# after approval. A plan is saved and approved under a role that may do
# everything; before the apply, an explicit Deny on sqs:SetQueueAttributes
# is attached to that role. On AWS the same refusal comes from a service
# control policy an organization admin rolls out, or a permissions boundary;
# the emulator has no Organizations service, and an SCP's refusal reaches
# the caller as the same AccessDenied an identity-policy Deny does, which is
# the thing this scenario reads. The emulator's IAM enforcement is what makes
# the refusal the platform's: choudoufu is never told about the policy.
#
# The other two parts - a service that stores something other than what it
# was sent, and an AWS Organizations tag policy refusing or rewriting the
# tofu-estate tag on the way in - are not measured here: the emulator has no
# tag-policy enforcement to produce the second, and no service it emulates
# rewrites a declared field on write in a way stock reads as perpetual drift.
# That is why the AWS cell of claim 26 reads "restated", not "proven".
#
# BREAK=1 attaches the identical Deny with one action different -
# sqs:DeleteQueue, which this apply never calls - and requires the opposite
# outcome: the approved plan applies on the first try with no refusal in the
# output. Before that it proves the decoy Deny is live, by having the same
# role try a delete-queue on a throwaway queue and requiring AWS to refuse
# it. Without the control the main arm would read the same if choudoufu
# simply failed every apply of a saved plan after an IAM change, or if the
# role could never write at all.

SCEN="the-server-gets-the-last-word"
SMOKE_WORK="$SMOKE_WORKROOT/$SCEN"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

# The emulator's IAM enforcement filter is off by default. The harness's own
# "test" key keeps bypassing it, so the platform steps below run as the
# account and only the role this scenario creates and assumes is governed.
export FLOCI_IAM_ENFORCEMENT=true

ESTATE="smoke-lastword"
QUEUE="smoke-lastword-work"

cat > "$SMOKE_WORK/versions.tf" <<EOF
terraform {
  required_version = ">= 1.5.0"
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
}
EOF

# queue_block writes the one resource with the visibility timeout $1.
queue_block() {
  cat > "$SMOKE_WORK/main.tf" <<EOF
resource "aws_sqs_queue" "work" {
  name                       = "$QUEUE"
  visibility_timeout_seconds = $1
}
EOF
}

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

ACCT="$(awsl sts get-caller-identity --query Account --output text)" || fail "$SCEN" "the emulator did not answer sts get-caller-identity"
TRUST="{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::$ACCT:root\"},\"Action\":\"sts:AssumeRole\"}]}"
ALLOW_ALL='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}'
# guardrail is the policy attached between approval and apply: a Deny on one
# action. The main arm denies the action the approved plan needs; the BREAK
# arm denies one it never calls. One string differs.
guardrail() { echo "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Sid\":\"Guardrail\",\"Effect\":\"Deny\",\"Action\":\"$1\",\"Resource\":\"*\"}]}"; }

# as_deployer runs one command under the deployer role's session, in a
# subshell so nothing leaks back into the account-level steps.
as_deployer() {
  local c
  c="$(awsl sts assume-role --role-arn "arn:aws:iam::$ACCT:role/deployer" --role-session-name deployer \
        --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]' --output text)" || { echo "  could not assume deployer" >&2; return 1; }
  ( export AWS_ACCESS_KEY_ID="$(cut -f1 <<< "$c")" AWS_SECRET_ACCESS_KEY="$(cut -f2 <<< "$c")" AWS_SESSION_TOKEN="$(cut -f3 <<< "$c")"
    "$@" )
}
queue_url() { awsl sqs get-queue-url --queue-name "$QUEUE" --query QueueUrl --output text; }
visibility() { awsl sqs get-queue-attributes --queue-url "$(queue_url)" --attribute-names VisibilityTimeout --query Attributes.VisibilityTimeout --output text; }
marker() { awsl sqs list-queue-tags --queue-url "$(queue_url)" --query 'Tags."tofu-estate"' --output text; }
# refused reads the one thing the main arm is about: the platform's own
# refusal of the action the approved plan needed. It matches the action
# name, not a bare "AccessDenied", so an unrelated denial elsewhere in the
# output cannot satisfy it (#1636 is that mistake in claim 13's control).
refused() { grep -qF "not authorized to perform: sqs:SetQueueAttributes" <<< "$1"; }

step "the claim"
explain \
  "A plan is a statement about the platform at the moment it was made." \
  "Between approval and apply the platform can change its mind: an" \
  "organization rolls out a guardrail, a permissions boundary lands. The" \
  "platform gets the last word, and the tool's job is to report that" \
  "word as the platform said it, change nothing it was not allowed to," \
  "and leave the approved plan intact so the same approval can be" \
  "applied once the refusal lifts."

step "0. a role that may do anything, and one queue the estate owns"
explain \
  "The deployer role holds Action * on Resource *. It stands the estate" \
  "up: one SQS queue, carrying tofu-estate=$ESTATE."
cmd "aws iam create-role deployer ; aws iam put-role-policy deployer allow-all ; choudoufu init && choudoufu apply -auto-approve   # as deployer"
awsl iam create-role --role-name deployer --assume-role-policy-document "$TRUST" >/dev/null || fail "$SCEN" "could not create the deployer role"
awsl iam put-role-policy --role-name deployer --policy-name allow-all --policy-document "$ALLOW_ALL" || fail "$SCEN" "could not grant the deployer role"
queue_block 30
logged the-server-gets-the-last-word-init "$SCEN" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY0="$(cd "$SMOKE_WORK" && as_deployer chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "$SCEN" "the first apply failed: $APPLY0"
grep -E 'Apply complete!' <<< "$APPLY0" | evidence
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY0" || fail "$SCEN" "the first apply did not report one added: $APPLY0"
[ "$(marker)" = "$ESTATE" ] || fail "$SCEN" "the queue does not carry tofu-estate=$ESTATE; nothing below would mean anything"
echo "VisibilityTimeout=$(visibility) tofu-estate=$(marker)" | evidence
proof "one queue, marked, written by the role that will be refused below."

step "1. a plan is saved and approved - then a guardrail lands"
explain \
  "The visibility timeout goes from 30 to 60 seconds. plan -out writes" \
  "the artifact a reviewer approves. Then, before the apply, an explicit" \
  "Deny lands on the role. Nothing in the plan could have seen it: the" \
  "plan was made before the policy existed."
queue_block 60
cmd "choudoufu plan -out=approved.tfplan   # as deployer"
PLAN1="$(cd "$SMOKE_WORK" && as_deployer chdf plan -out=approved.tfplan -input=false -no-color 2>&1)" \
  || fail "$SCEN" "plan -out failed: $PLAN1"
grep -E '^Plan:' <<< "$PLAN1" | evidence
grep -qE 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$PLAN1" \
  || fail "$SCEN" "the saved plan is not the one change this step needs: $PLAN1"
[ -f "$SMOKE_WORK/approved.tfplan" ] || fail "$SCEN" "plan -out wrote no artifact"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the same guardrail on an action the plan never calls; the approved plan must apply"
  explain \
    "Identical policy, one action different: sqs:DeleteQueue. First the" \
    "decoy is shown live - the role's own delete-queue on a throwaway" \
    "queue is refused. Then the approved plan must apply on the first" \
    "try with no refusal anywhere in the output. If it did not, the main" \
    "arm's failure would be measuring something other than the Deny."
  cmd "aws iam put-role-policy deployer guardrail (Deny sqs:DeleteQueue) ; choudoufu apply approved.tfplan   # as deployer"
  awsl iam put-role-policy --role-name deployer --policy-name guardrail --policy-document "$(guardrail sqs:DeleteQueue)" \
    || fail "$SCEN" "BREAK: could not attach the decoy guardrail"
  DECOY_URL="$(awsl sqs create-queue --queue-name smoke-lastword-decoy --query QueueUrl --output text)" \
    || fail "$SCEN" "BREAK: could not create the throwaway queue"
  DOUT="$(as_deployer awsl sqs delete-queue --queue-url "$DECOY_URL" 2>&1 || true)"
  grep -qF "not authorized to perform: sqs:DeleteQueue" <<< "$DOUT" \
    || fail "$SCEN" "BREAK: the decoy guardrail is not in force - the role deleted the throwaway queue, so this control would prove nothing: $DOUT"
  grep -F "not authorized" <<< "$DOUT" | head -1 | evidence
  BAPPLY="$(cd "$SMOKE_WORK" && as_deployer chdf apply -input=false -no-color approved.tfplan 2>&1)" \
    || fail "$SCEN" "BREAK: with the Deny on an action the plan never calls, the approved plan still failed: $(grep -E '^Error|not authorized' <<< "$BAPPLY" | head -3)"
  grep -qF "not authorized to perform" <<< "$BAPPLY" \
    && fail "$SCEN" "BREAK: the apply succeeded but its output carries a refusal: $BAPPLY"
  grep -E 'Apply complete!' <<< "$BAPPLY" | evidence
  [ "$(visibility)" = "60" ] || fail "$SCEN" "BREAK: the approved change did not land (VisibilityTimeout=$(visibility))"
  proof "caught - with the Deny moved to an action the plan never calls, the same approved plan applied on the first try. The main arm's refusal is the guardrail's, on the action it names."
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

cmd "aws iam put-role-policy deployer guardrail (Deny sqs:SetQueueAttributes)"
awsl iam put-role-policy --role-name deployer --policy-name guardrail --policy-document "$(guardrail sqs:SetQueueAttributes)" \
  || fail "$SCEN" "could not attach the guardrail"
awsl iam get-role-policy --role-name deployer --policy-name guardrail --query 'PolicyDocument.Statement[0].[Effect,Action]' --output text | evidence
proof "an explicit Deny is on the role. The approved plan is on disk and was written before it existed."

step "2. apply the approved plan - the write is refused in the platform's own words"
explain \
  "The apply reaches SetQueueAttributes and AWS refuses it. The error" \
  "must quote the platform's refusal, naming the action; the queue must" \
  "be exactly as it was, marker included; and the plan file must still" \
  "be there, because the approval has not been spent."
cmd "choudoufu apply approved.tfplan   # as deployer"
APPLY_RC=0
APPLY2="$(cd "$SMOKE_WORK" && as_deployer chdf apply -input=false -no-color approved.tfplan 2>&1)" || APPLY_RC=$?
[ "$APPLY_RC" -ne 0 ] || fail "$SCEN" "the apply exited 0 under a Deny on the one action it needed; the write cannot have been refused: $APPLY2"
refused "$APPLY2" || fail "$SCEN" "the error does not quote the platform's refusal of sqs:SetQueueAttributes: $(grep -E '^Error|not authorized' <<< "$APPLY2" | head -3)"
{ sed 's/\x1b\[[0-9;]*m//g' <<< "$APPLY2" | grep -E '^Error|not authorized to perform' || true; } | head -3 | evidence
[ "$(visibility)" = "30" ] || fail "$SCEN" "the refused apply changed the queue anyway (VisibilityTimeout=$(visibility))"
[ "$(marker)" = "$ESTATE" ] || fail "$SCEN" "the refused apply cost the queue its marker (tofu-estate=$(marker))"
[ -f "$SMOKE_WORK/approved.tfplan" ] || fail "$SCEN" "the refused apply consumed the plan artifact; the approval would have to be sought again"
proof "the refusal is AWS's, naming the action. The queue is unchanged, still marked, and the approved plan is still on disk."

step "3. the guardrail lifts - the same approved plan applies unchanged"
explain \
  "The Deny comes off. Nothing was re-planned and nothing re-approved:" \
  "the apply re-reads the queue, finds it exactly as the approval saw" \
  "it, and makes the one approved change."
cmd "aws iam delete-role-policy deployer guardrail ; choudoufu apply approved.tfplan   # as deployer"
awsl iam delete-role-policy --role-name deployer --policy-name guardrail || fail "$SCEN" "could not lift the guardrail"
APPLY3="$(cd "$SMOKE_WORK" && as_deployer chdf apply -input=false -no-color approved.tfplan 2>&1)" \
  || fail "$SCEN" "the retried apply of the approved plan failed: $APPLY3"
grep -E 'Apply complete!' <<< "$APPLY3" | evidence
grep -qE 'Apply complete! Resources: 0 added, 1 changed, 0 destroyed' <<< "$APPLY3" \
  || fail "$SCEN" "the retried apply did not report the one approved change: $APPLY3"
[ "$(visibility)" = "60" ] || fail "$SCEN" "the retried apply did not write the value the approved plan carried (VisibilityTimeout=$(visibility))"
[ "$(marker)" = "$ESTATE" ] || fail "$SCEN" "the queue lost its marker on the retried apply"
echo "VisibilityTimeout=$(visibility) tofu-estate=$(marker)" | evidence
proof "the approval was spent once, on the write it approved, after the platform allowed it."

step "4. the estate settles"
cmd "choudoufu plan   # as deployer"
PLAN4="$(cd "$SMOKE_WORK" && as_deployer chdf plan -input=false -no-color 2>&1)" || fail "$SCEN" "the settling plan failed: $PLAN4"
grep -q "No changes." <<< "$PLAN4" || fail "$SCEN" "the estate does not settle after the approved change: $(grep -E '^Plan:|will be' <<< "$PLAN4" | head -3)"
echo "No changes." | evidence
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "$SCEN" "teardown failed"
proof "No changes. The platform refused, then allowed, and the estate converged on the one change that was approved."
