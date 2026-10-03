# an-ignored-tag-is-not-drift
# CLAIM 45 (aws) - Drift you ignore isn't drift. ~3 min.
#
# This proof: A tag another account's automation or an AWS service adds to a
# resource out of band does not churn the plan once the estate tells its
# provider to ignore that key, the same knob stock gives every AWS user for
# exactly this, and an edit to a tag the estate DOES declare still plans.
#
# GitHub issue #1624. Claim 27's own AWS cell (#1597, then #1598) names this
# the genuinely open question and files it separately rather than restating
# it there: "whether a tag another account's automation, or an AWS service,
# adds to a resource out of band churns an AWS plan."
#
# Kubernetes' claim 27 answers its own version of this for free: a key the
# configuration never declared - the API server's own
# kubernetes.io/metadata.name, a controller's annotation - stays the
# server's and churns nothing, because kubernetes_manifest's computed_fields
# reads the PRIOR manifest to decide what to keep, and choudoufu mirrors the
# live object into that prior for every key the configuration itself
# declares (internal/live/projection/nodestamp_manifest.go). It is
# automatic; no estate configuration switches it on.
#
# AWS's "tags" argument has no such branch, and this is not a state-file
# question: tftags.TagsSchema() makes "tags" Optional but not Computed on
# nearly every taggable type, so Terraform's own core plan mechanics -
# common to every provider, not an AWS or a choudoufu choice - propose the
# CONFIGURED value verbatim as the new state on every single plan. A
# stateless run's rebuilt prior (importAndRead) carries the live object's
# real tags, because there is no state file to prefer instead, so a tag
# present live and absent from configuration reads as a difference and
# plans an update removing it - the exact answer a stock, state-backed run
# gives a real AWS account for the identical drift. This is a well-known,
# long-documented AWS provider behaviour (it is why the provider ships an
# `ignore_tags` block at all), and telling the provider to ignore a key
# another system owns is the ordinary, stock-compatible way an operator
# gets AWS's version of what Kubernetes gives for free: opt-in rather than
# automatic, because the mechanism it rides is the provider's own tag
# interceptor, not a Kubernetes admission chain choudoufu instruments.
#
# choudoufu has no tag-diffing of its own to defeat with a build overlay
# the way most BREAK controls here do: a resource's "tags" argument is
# planned entirely by the real hashicorp/aws provider process over the plan
# RPC, the same subprocess a stock run talks to. There is nothing in this
# repository to patch. BREAK=1 instead removes the one piece of THIS
# estate's own configuration the claim is actually about - the
# ignore_tags declaration - and requires the opposite outcome: the
# identical out-of-band tag write now plans an update, so step 2's "No
# changes." is provably the declaration at work, not choudoufu shrugging
# at every tag difference.
#
# Step 3 keeps the claim from proving too much: with ignore_tags naming
# only CostCenter, an edit to "team" - a tag the estate DOES declare - must
# still plan, or the claim would really be "this estate never notices a tag
# difference," which is false and would be a defect, not a feature.
#
# What this scenario does not measure: the record-store half of #1624's
# question, a tag added out of band to an object choudoufu itself wrote for
# a record-backed resource. That is not a plan-time question at all -
# S3Store.PutIfVersion (internal/live/staterecord/s3.go) REPLACES an
# object's whole tag set on every write, and no code path here ever reads a
# record object's own tags to compute a diff; they are read once, on
# access, only to confirm the object is this estate's (checkOwnershipAt),
# never compared against a configured value. An unrelated tag surviving
# there between writes is not a coincidence needing an ignore_tags
# equivalent - it is a consequence of a record object's tags never being a
# planned attribute in the first place, established by reading the store's
# own write path rather than by a scenario step.

SMOKE_WORK="$SMOKE_WORKROOT/oobtag"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

ESTATE="smoke-oob-tag"
LG_NAME="smoke-oob-tag-app"

# render writes main.tf. $1=1 declares the estate's ignore_tags block
# (CostCenter only); anything else leaves it out. $2 is the "team" tag's
# value, so step 3 can edit it without rewriting the whole function.
render() { # $1=ignore_tags(0/1) $2=team-tag-value
  local ignore=""
  if [ "$1" = "1" ]; then
    ignore='

  ignore_tags {
    keys = ["CostCenter"]
  }'
  fi
  cat > "$SMOKE_WORK/main.tf" <<TF
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

provider "aws" {$ignore
}

resource "aws_cloudwatch_log_group" "app" {
  name = "$LG_NAME"

  tags = {
    team = "$2"
  }
}
TF
}

lg_arn() {
  awsl logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text
}

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "A tag another account's automation, or an AWS service, adds to a" \
  "resource out of band should not force this estate to keep proposing" \
  "its removal. AWS gives every user one knob for exactly this - the" \
  "provider's own ignore_tags block - and it works here with no code of" \
  "choudoufu's own: the estate declares which keys are not its business," \
  "and a tag matching one of them, however it arrived, never enters the" \
  "plan. A tag the estate DOES declare still plans when it changes."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the same out-of-band tag, no ignore_tags to catch it"
  explain \
    "Rebuild nothing: remove the one line of THIS estate's configuration" \
    "the claim is about. With no ignore_tags naming CostCenter, the" \
    "identical AWS CLI tag write must now read as drift and plan an" \
    "update, or step 2's clean plan proved nothing at all."
  render 0 platform
  cmd "choudoufu init ; choudoufu apply -auto-approve   # no ignore_tags"
  logged an-ignored-tag-is-not-drift-break-init "oobtag" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
  ( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "oobtag" "apply failed"
  ARN="$(lg_arn)"
  [ -n "$ARN" ] && [ "$ARN" != "None" ] || fail "oobtag" "no log group named $LG_NAME after apply"
  cmd "aws logs tag-resource --resource-arn ... --tags CostCenter=finance-added-this   # not choudoufu"
  awsl logs tag-resource --resource-arn "$ARN" --tags CostCenter=finance-added-this >/dev/null \
    || fail "oobtag" "could not tag the log group out of band"
  cmd "choudoufu plan"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "oobtag" "the BREAK plan errored outright: $BOUT"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "oobtag" "BREAK: the plan still read \"No changes.\" with no ignore_tags declared - the control proves nothing: $BOUT"
  fi
  grep -q "CostCenter" <<< "$BOUT" || fail "oobtag" "BREAK: the plan changed but never names CostCenter: $BOUT"
  grep -E '~ resource|will be updated in-place|CostCenter' <<< "$BOUT" | head -3 | evidence
  proof "caught - without ignore_tags naming the key, the identical out-of-band tag plans an update. The declaration in step 2 was doing real work, not scenery."
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "1. an estate applies, one tag declared and one key told to the provider to ignore"
cmd "choudoufu init ; choudoufu apply -auto-approve   # ignore_tags { keys = [\"CostCenter\"] }"
render 1 platform
logged an-ignored-tag-is-not-drift-init "oobtag" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "oobtag" "apply failed: $AOUT"
grep -q "Resources: 1 added" <<< "$AOUT" || fail "oobtag" "the apply: $AOUT"
ARN="$(lg_arn)"
[ -n "$ARN" ] && [ "$ARN" != "None" ] || fail "oobtag" "no log group named $LG_NAME after apply"
proof "one log group, tagged team=platform, and a provider that already knows CostCenter is not this estate's tag to manage."

step "2. the same key, added out of band, does not churn the plan"
explain \
  "Not through choudoufu - the plain AWS CLI, the way a cost-allocation" \
  "job or another team's automation would reach this object."
cmd "aws logs tag-resource --resource-arn ... --tags CostCenter=finance-added-this   # not choudoufu"
awsl logs tag-resource --resource-arn "$ARN" --tags CostCenter=finance-added-this >/dev/null \
  || fail "oobtag" "could not tag the log group out of band"
TAGS_NOW="$(awsl logs list-tags-for-resource --resource-arn "$ARN" --output json)"
grep -q "CostCenter" <<< "$TAGS_NOW" || fail "oobtag" "the out-of-band tag never landed: $TAGS_NOW"
cmd "choudoufu plan"
P1="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "oobtag" "plan failed after the out-of-band tag: $P1"
grep -q "No changes." <<< "$P1" || fail "oobtag" "a tag ignore_tags names still churned the plan: $P1"
echo "aws logs list-tags-for-resource: $(grep -o '"CostCenter"[^}]*' <<< "$TAGS_NOW" || echo present); choudoufu plan: No changes." | evidence
proof "the tag another job wrote is still on the object, and the plan never saw it as this estate's business."

step "3. a tag the estate DOES declare still plans when it changes"
explain \
  "ignore_tags names CostCenter alone. \"team\" stays this estate's, and" \
  "editing it in the configuration must still plan an update - proving" \
  "step 2's silence is about CostCenter specifically, not the estate" \
  "going blind to its own tags."
cmd "edit tags.team in main.tf ; choudoufu plan"
render 1 platform-2
P2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "oobtag" "plan failed after the declared edit: $P2"
grep -q "will be updated in-place" <<< "$P2" || fail "oobtag" "the declared tag edit did not plan: $P2"
grep -q "CostCenter" <<< "$P2" && fail "oobtag" "the declared edit's plan also proposes touching CostCenter, which ignore_tags should keep out of it entirely: $P2"
grep -E '~ tags|"team"' <<< "$P2" | head -3 | evidence
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "oobtag" "apply of the declared edit failed"
proof "the declared tag's edit planned and applied on its own; the ignored key never entered it."

step "4. teardown"
cmd "choudoufu apply -destroy -auto-approve"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "oobtag" "teardown failed"
proof "gone."

echo "  What you watched: a tag another job wrote out of band surviving on"
echo "  the object while the plan stayed silent about it, because the"
echo "  estate's own provider block named it out of scope - the same knob"
echo "  every AWS user has - and a tag the estate does manage still"
echo "  planning cleanly when it changes."
