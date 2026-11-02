# carve-by-retag
# CLAIM 7 (aws) - Identity is a tag you can read, move and carve by. ~6 min.
#
# This proof: Carve by retag: a stock estate is adopted with one command,
# then carved into estates by tag writes, and every side plans clean with
# nothing rebuilt.
#
# The estate is a scenario estate (choudoufu#2303, ruling A on
# INTENTIUS/chant#4586: the generated terralith is no longer test
# evidence): go run ./tools/scenario-estate -scale 2 -prefix tl, two copies
# of live/e2e/estate in one stock state file, 86 resources across VPC, EC2,
# ALB, ACM, Route 53, IAM, ECS, KMS, S3, DynamoDB, SNS, Step Functions,
# CloudWatch and SSM, with count and for_each. Nothing in it carries a
# marker until live-import writes one. The render prints how many
# resources it declares and how many carry tags, and the counts below are
# read from that line rather than written out.

W="$SMOKE_WORKROOT/carve"
MONO="$W/monolith"; TEAM="$W/team1"; IAM="$W/iam"
mkdir -p "$MONO"
# The oracle mounts SMOKE_WORK at /work; the monolith is /work/monolith.
SMOKE_WORK="$W"; export SMOKE_WORK
LOGS="${CARVE_LOGS:-/tmp/carve-logs-$$}"; mkdir -p "$LOGS"

MONO_ESTATE="tl-monolith"; TEAM_ESTATE="tl-team-1"; IAM_ESTATE="tl-iam"

command -v go >/dev/null 2>&1 || fail "carve" "this scenario renders its estate with go run ./tools/scenario-estate and needs Go on PATH"
GEN="$( cd "$ROOT" && env -u PWD go run ./tools/scenario-estate -scale 2 -prefix tl -out "$MONO" 2>&1 )" \
  || fail "carve" "scenario-estate failed: $GEN"
[ -f "$MONO/iam.tf" ] && [ -f "$MONO/receipts.tf" ] || fail "carve" "the render wrote no estate at $MONO"
RES="$(jq -r .resources <<< "$GEN")"; TAGGABLE="$(jq -r .taggable <<< "$GEN")"
[ "$RES" -gt 0 ] 2>/dev/null && [ "$TAGGABLE" -gt 0 ] 2>/dev/null || fail "carve" "the render's summary has no counts: $GEN"
COMPOSED=$((RES - TAGGABLE))

# inject_live adds a live block naming an estate to a versions.tf, with a
# local record store beside the module, the way the terralith crossing does.
inject_live() {
  local file="$1" estate="$2" t; t="$(mktemp)"
  awk -v estate="$estate" '
    !inserted && /^}$/ {
      print ""
      print "  live {"
      print "    estate = \"" estate "\""
      print "    record_store \"local\" {"
      print "      path = \".tofu-records\""
      print "    }"
      print "  }"
      inserted = 1
    }
    { print }
  ' "$file" > "$t" && mv "$t" "$file"
  grep -qF "estate = \"$estate\"" "$file" || fail "carve" "no live block landed in $file"
}

# move_block cuts one top-level resource block out of a file and appends it
# to another: the git move a split needs in any tool. Top-level blocks in
# the generated estate close with a bare "}" on its own line.
move_block() {
  local from="$1" type="$2" name="$3" to="$4" hdr t
  hdr="resource \"$type\" \"$name\" {"
  grep -qF "$hdr" "$from" || fail "carve" "no block $type.$name in $from"
  awk -v hdr="$hdr" '$0 == hdr { p = 1 } p { print } p && $0 == "}" { p = 0; print "" }' "$from" >> "$to"
  t="$(mktemp)"
  awk -v hdr="$hdr" '$0 == hdr { d = 1; next } d && $0 == "}" { d = 0; next } !d { print }' "$from" > "$t" && mv "$t" "$from"
  grep -qF "$hdr" "$from" && fail "carve" "block $type.$name is still in $from"
  return 0
}

# new_estate lays a thin root for a carved estate: the generator's own
# provider wiring plus a live block naming the new estate, and nothing else
# until blocks move in.
new_estate() {
  local dir="$1" estate="$2"
  mkdir -p "$dir"
  cp "$MONO/versions.tf" "$dir/versions.tf"
  python3 - "$dir/versions.tf" <<'PYEOF'
import re, sys
p = sys.argv[1]; s = open(p).read()
s = re.sub(r'\n  live \{.*?\n  \}\n', '\n', s, flags=re.S)
open(p, 'w').write(s)
PYEOF
  inject_live "$dir/versions.tf" "$estate"
  : > "$dir/iam.tf"
}

# read_role_by_data points every reader of copy $1's app role in the
# monolith at a data source instead of the resource: the bucket policy's
# principal and the hash receipt's inputs. The role is about to leave this
# estate, and the cross-estate pattern is to read it by the name both sides
# already know (live/OUTPUTS.md). The values do not change, so neither
# reader proposes anything.
read_role_by_data() {
  local k="$1"
  python3 - "$MONO" "$k" <<'PYEOF'
import glob, os, sys
mono, k = sys.argv[1], sys.argv[2]
ref = "aws_iam_role.app_%s." % k
n = 0
for p in sorted(glob.glob(os.path.join(mono, "*.tf"))):
    s = open(p).read()
    lines = s.split("\n")
    out = []
    for line in lines:
        # The role's own block and its two children have already moved out.
        if ref in line and not line.lstrip().startswith("#"):
            line = line.replace(ref, "data." + ref)
            n += 1
        out.append(line)
    open(p, "w").write("\n".join(out))
assert n >= 2, "expected the bucket policy and the receipt to read aws_iam_role.app_%s, found %d" % (k, n)
with open(os.path.join(mono, "iam.tf"), "a") as f:
    f.write('\n# The app role of copy %s lives in another estate now; read it by name\n# (live/OUTPUTS.md, the cross-estate pattern).\ndata "aws_iam_role" "app_%s" {\n  name = "tofu-tl-%s-app"\n}\n' % (k, k, k))
PYEOF
  [ $? -eq 0 ] || fail "carve" "could not point the monolith's readers of copy $k's role at a data source"
}

plan_clean() { # $1=dir $2=label -> fails unless the plan is empty
  local out slug
  slug="$(tr -c 'a-z0-9' '-' <<< "$2" | sed 's/-*$//')"
  out="$(cd "$1" && chdf plan -input=false -no-color 2>&1)" || fail "carve" "$2: plan failed: $out"
  printf '%s\n' "$out" > "$LOGS/$slug.plan"
  if ! grep -q "No changes." <<< "$out"; then
    # The whole plan and the store's keys land in the log so a failure
    # here says which leg surfaced what, not merely that one did.
    echo "--- records in $1/.tofu-records ---" >> "$LOGS/$slug.plan"
    ( cd "$1/.tofu-records" 2>/dev/null && find . -type f | sort ) >> "$LOGS/$slug.plan" 2>/dev/null || true
    cp -R "$1/.tofu-records" "$LOGS/$slug.records" 2>/dev/null || true
    fail "carve" "$2 does not plan clean (full plan and record listing in $LOGS/$slug.plan): $(grep -E '^Plan:|will be|Owned and undeclared|UNOWNED|ABSENT|NOT_SCANNED|Not swept' <<< "$out" | head -12)"
  fi
}

plan_calls() { # $1=dir $2=logname -> request count of one plan
  ( cd "$1" && TF_LOG=debug TF_LOG_PATH="$LOGS/$2.log" "$TOFU" plan -input=false -no-color >/dev/null 2>&1 || true )
  grep -c "HTTP Request Sent" "$LOGS/$2.log" 2>/dev/null || echo 0
}

role_estate() { awsl iam list-role-tags --role-name "$1" --query 'Tags[?Key==`tofu-estate`].Value' --output text 2>/dev/null || echo none; }

# pick shows matching lines as evidence and never fails the run: a grep
# that matches nothing exits 1, and under pipefail that would end the
# scenario with no FAIL line at all. Assertions grep separately.
pick() { grep -E "$1" || true; }

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "Carve by retag. In stock tooling every ownership boundary is a state" \
  "file, so splitting a monolith into team estates is state surgery." \
  "Each resource is moved between files by hand, and for a moment it" \
  "sits in two ledgers or in none. Here the boundary is a tag. A" \
  "resource leaves one estate for another by having its tofu-estate tag" \
  "rewritten. The tool refuses a write that would leave either side" \
  "dirty, and afterwards each side plans clean and pays only for what" \
  "it holds."

step "1. stock stands the estate up: one state file, no markers"
explain \
  "The pinned stock OpenTofu, in its own container, applies the" \
  "rendered estate the ordinary way. Nothing here carries a marker;" \
  "identity exists only in terraform.tfstate."
cmd "docker compose run opentofu -chdir=/work/monolith apply -auto-approve"
logged carve-by-retag-stock-monolith-init "carve" "stock init failed" -- stock -chdir=/work/monolith init -input=false -no-color
SOUT="$(stock -chdir=/work/monolith apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "carve" "stock apply failed: $(tail -20 <<< "$SOUT")"
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$SOUT" | grep -oE '[0-9]+')"
[ "$ADDED" = "$RES" ] || fail "carve" "stock added $ADDED resources, want $RES"
[ -f "$MONO/terraform.tfstate" ] || fail "carve" "no terraform.tfstate after the stock apply"
MARKED_IN_STATE="$(grep -c '"tofu-address"' "$MONO/terraform.tfstate" || true)"
echo "tofu-address markers in the state file: $MARKED_IN_STATE" | evidence
[ "$MARKED_IN_STATE" = "0" ] || fail "carve" "stock's estate already carries markers"
proof "$RES resources, one state file, zero markers. A stranger's estate."

step "2. one command adopts it, and the state file is deleted"
explain \
  "Add the live block. live-import then reads the state file once and" \
  "stamps the two ownership markers on every resource that verifies." \
  "Half the estate is untaggable and needs no marker at all: its identity" \
  "composes from an already-stamped parent. Then the file goes, and the" \
  "plan reads the estate back off the tags."
cmd "choudoufu live-import -state=terraform.tfstate -estate=$MONO_ESTATE -approve ; rm terraform.tfstate ; choudoufu plan"
inject_live "$MONO/versions.tf" "$MONO_ESTATE"
logged carve-by-retag-mono-init "carve" "choudoufu init failed in the monolith" -- in_dir "$MONO" chdf init -input=false -no-color
IOUT="$(cd "$MONO" && chdf live-import -state=terraform.tfstate -estate="$MONO_ESTATE" -approve -no-color 2>&1)" \
  || fail "carve" "live-import failed: $(tail -20 <<< "$IOUT")"
pick "eligible for stamping|stamped" <<< "$IOUT" | head -2 | evidence
grep -qF "$TAGGABLE of $RES resource instance(s) are eligible for stamping" <<< "$IOUT" \
  || fail "carve" "live-import did not ratify $TAGGABLE of $RES: $(grep -E 'eligible' <<< "$IOUT" | head -2)"
rm -f "$MONO/terraform.tfstate" "$MONO/terraform.tfstate.backup"
plan_clean "$MONO" "the adopted monolith"
MONO_CALLS="$(plan_calls "$MONO" mono-plan)"
echo "the monolith plans clean with the state file gone: $MONO_CALLS requests" | evidence
proof "$TAGGABLE stamped, $COMPOSED composed from a parent, 0 written by hand. The state file has done its last job."

# ── the carve ────────────────────────────────────────────────────────────
TEAM_TAGGABLE="aws_iam_role.app_0001"
carve_team_config() {
  new_estate "$TEAM" "$TEAM_ESTATE"
  local b
  for b in aws_iam_role:app_0001 aws_iam_role_policy:app_0001 aws_iam_role_policy_attachment:app_0001; do
    move_block "$MONO/iam.tf" "${b%%:*}" "${b#*:}" "$TEAM/iam.tf"
  done
  read_role_by_data 0001
  logged carve-by-retag-team-init "carve" "choudoufu init failed in the team estate" -- in_dir "$TEAM" chdf init -input=false -no-color
}

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - move the blocks, skip the retag; both sides must show the window"
  explain \
    "The claim rests on the tag write being the move. So defeat it. Move" \
    "the three blocks to a new root and rewrite no tag. That is the" \
    "two-ledger window stock lives in, manufactured. The monolith must" \
    "propose destroying what it still owns and no longer declares, and" \
    "the new estate must propose creating what it declares and does not" \
    "own. A clean plan on either side would mean the retag never" \
    "mattered."
  cmd "move three blocks into team1/ ; choudoufu plan   # in both, no live-mv"
  carve_team_config
  BM="$(cd "$MONO" && chdf plan -input=false -no-color 2>&1 || true)"
  BT="$(cd "$TEAM" && chdf plan -input=false -no-color 2>&1 || true)"
  pick '^Plan:|Owned and undeclared' <<< "$BM" | head -2 | sed 's/^/monolith: /' | evidence
  pick '^Plan:|UNOWNED|ADOPTABLE' <<< "$BT" | head -2 | sed 's/^/team1:    /' | evidence
  if grep -q "No changes." <<< "$BM"; then
    fail "carve" "BREAK: the monolith plans clean with a marked role it no longer declares - the retag proves nothing"
  fi
  grep -q "Owned and undeclared" <<< "$BM" \
    || fail "carve" "BREAK: the monolith does not name the leavers as owned and undeclared: $(grep -E '^Plan:' <<< "$BM")"
  if grep -q "No changes." <<< "$BT"; then
    fail "carve" "BREAK: the new estate plans clean owning nothing - the retag proves nothing"
  fi
  proof "caught - without the retag the monolith wants to destroy the leavers and the new estate wants to build them again. The tag is the boundary, and the write is the move."
  ( cd "$MONO" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "3. carve a team out: three blocks move, one tag is rewritten"
explain \
  "Team 1 is the second copy's app role and its two children. The role" \
  "carries a marker. The inline policy and the attachment cannot: they" \
  "compose their identity from the role. The bucket policy and the hash" \
  "receipt that stay behind read the role through a data source." \
  "The git half is the same as any tool's: the blocks move to a new" \
  "root with its own live block. The state half is three runs of" \
  "live-mv -from-estate, one tag write, made in the destination" \
  "so the tool can check the address is declared there and free there." \
  "The two children need no write at all; they follow their parent." \
  "-json (issue #791) turns each run into a document instead of a" \
  "sentence: the dry run previews exactly what the real run then does," \
  "and the role's own document names the inline policy as a follower" \
  "before either run writes anything."
cmd "choudoufu live-mv -json -from-estate=$MONO_ESTATE aws_iam_role.app_0001 aws_iam_role.app_0001   # in team1/"
carve_team_config
MVERR="$(mktemp)"
ROLE_JSON=""
for addr in $TEAM_TAGGABLE; do
  DRYJSON="$(cd "$TEAM" && chdf live-mv -no-color -json -dry-run -from-estate="$MONO_ESTATE" "$addr" "$addr" 2>"$MVERR")" \
    || fail "carve" "live-mv -json -dry-run refused $addr: $(tail -c 2000 "$MVERR")"
  jq -e . >/dev/null 2>&1 <<< "$DRYJSON" || fail "carve" "$addr: -json -dry-run did not print valid JSON: $DRYJSON"

  MV="$(cd "$TEAM" && chdf live-mv -no-color -json -from-estate="$MONO_ESTATE" "$addr" "$addr" 2>"$MVERR")" \
    || fail "carve" "live-mv -json refused $addr: $(tail -c 2000 "$MVERR")"
  jq -e . >/dev/null 2>&1 <<< "$MV" || fail "carve" "$addr: -json did not print valid JSON: $MV"

  # The dry run previewed exactly the move the real run then made: same
  # resource, same endpoints, same followers. dry_run/written/verified are
  # the only fields the two documents are allowed to disagree on - the rest
  # is the claim behold or the workbench would read from a -dry-run preview,
  # asserted here against what actually landed.
  for field in resource from to followers; do
    d="$(jq -cS ".$field" <<< "$DRYJSON")"
    r="$(jq -cS ".$field" <<< "$MV")"
    [ "$d" = "$r" ] || fail "carve" "$addr: the dry run and the real run disagree on .$field: dry=$d real=$r"
  done
  [ "$(jq -r '.dry_run' <<< "$DRYJSON")" = "true" ]  || fail "carve" "$addr: the dry run's own document does not say dry_run: $DRYJSON"
  [ "$(jq -r '.written' <<< "$DRYJSON")" = "false" ] || fail "carve" "$addr: the dry run's own document claims a write: $DRYJSON"
  [ "$(jq -r '.dry_run' <<< "$MV")" = "false" ]      || fail "carve" "$addr: the real run's own document still claims dry_run: $MV"
  [ "$(jq -r '.written' <<< "$MV")" = "true" ]       || fail "carve" "$addr: the real run's own document says nothing was written: $MV"
  [ "$(jq -r '.refusal' <<< "$MV")" = "null" ]       || fail "carve" "$addr: the real run reports a refusal: $(jq -c '.refusal' <<< "$MV")"

  if [ "$addr" = "aws_iam_role.app_0001" ]; then
    # aws_iam_role_policy's identity composes from "role" alone - the
    # admission table has no other component it could reference - so the
    # inline policy is deterministically the role's follower, dry run or
    # real, every time.
    jq -e '.followers | map(.address) | index("aws_iam_role_policy.app_0001")' <<< "$DRYJSON" >/dev/null \
      || fail "carve" "the role's dry run does not list the inline policy as a follower: $(jq -c '.followers' <<< "$DRYJSON")"
    jq -e '.followers | map(.address) | index("aws_iam_role_policy.app_0001")' <<< "$MV" >/dev/null \
      || fail "carve" "the role's real run does not list the inline policy as a follower: $(jq -c '.followers' <<< "$MV")"
    ROLE_JSON="$MV"
    echo "role's followers (-json, both runs agree): $(jq -c '.followers' <<< "$MV")" | evidence
  fi
done
JSON_TOESTATE="$(jq -r '.to.estate' <<< "$ROLE_JSON")"
JSON_LIVEID="$(jq -r '.resource.live_id' <<< "$ROLE_JSON")"
echo "-json (role): resource.live_id=$JSON_LIVEID  to.estate=$JSON_TOESTATE" | evidence
TEAM_ROLE_ESTATE="$(role_estate tofu-tl-0001-app)"
[ "$TEAM_ROLE_ESTATE" = "$TEAM_ESTATE" ] || fail "carve" "the role's live tofu-estate reads '$TEAM_ROLE_ESTATE', want $TEAM_ESTATE"
# The document's own claim has to agree with the same fact read back
# independently through the plain AWS CLI - a receipt that only ever
# confirms itself proves nothing.
[ "$JSON_TOESTATE" = "$TEAM_ROLE_ESTATE" ] \
  || fail "carve" "the role's -json document claims to.estate=$JSON_TOESTATE, but the AWS CLI reads tofu-estate=$TEAM_ROLE_ESTATE"
INLINE="$(awsl iam list-role-policies --role-name tofu-tl-0001-app --query 'PolicyNames' --output text 2>/dev/null || echo none)"
echo "aws iam list-role-tags tofu-tl-0001-app: tofu-estate=$TEAM_ROLE_ESTATE; inline policies still attached: $INLINE" | evidence
[ "$INLINE" = "tofu-tl-0001-app-inline" ] || fail "carve" "the inline policy did not stay attached across the move: '$INLINE'"
proof "one tag write, its dry run and real run agreeing on paper, both matched against the inventory read back through the plain CLI. The children were never touched and never needed to be."

step "4. both sides plan clean"
explain \
  "The monolith no longer declares the three and no longer owns the role;" \
  "the team estate declares all three and owns the role." \
  "Neither side has anything to propose. No state was split, no window" \
  "was left, nothing was rebuilt."
cmd "choudoufu plan   # in monolith/, then in team1/"
plan_clean "$MONO" "the monolith after the carve"
plan_clean "$TEAM" "the carved team estate"
proof "No changes, twice. Where there was one boundary there are two, and the split cost one tag write."

step "5. carve across a reference: the first copy's role leaves, the state machine keeps pointing at it"
explain \
  "The hard split is the resource on the line. The first copy's Step" \
  "Functions state machine names its role, and its bucket policy and" \
  "hash receipt read the role block. The role moves to an IAM estate;" \
  "all three stay and read the same role through a data source, the" \
  "cross-estate pattern the docs give. One tag write, and the inline" \
  "policy and attachment that compose from the role follow."
cmd "choudoufu live-mv -from-estate=$MONO_ESTATE aws_iam_role.app_0000 aws_iam_role.app_0000   # in iam/"
new_estate "$IAM" "$IAM_ESTATE"
for b in aws_iam_role:app_0000 aws_iam_role_policy:app_0000 aws_iam_role_policy_attachment:app_0000; do
  move_block "$MONO/iam.tf" "${b%%:*}" "${b#*:}" "$IAM/iam.tf"
done
read_role_by_data 0000
python3 - "$MONO/workflows.tf" <<'PYEOF'
import re, sys
p = sys.argv[1]; s = open(p).read()
old = re.compile(r'role_arn = "arn:aws:iam::000000000000:role/tofu-tl-0000-app"')
assert len(old.findall(s)) == 1, "the first copy's state machine role_arn line was not found once"
s = old.sub("role_arn = data.aws_iam_role.app_0000.arn", s)
open(p, 'w').write(s)
PYEOF
logged carve-by-retag-iam-init "carve" "choudoufu init failed in the iam estate" -- in_dir "$IAM" chdf init -input=false -no-color
MV="$(cd "$IAM" && chdf live-mv -no-color -from-estate="$MONO_ESTATE" aws_iam_role.app_0000 aws_iam_role.app_0000 2>&1)" \
  || fail "carve" "live-mv refused the first copy's role: $(tail -6 <<< "$MV")"
EXEC_ESTATE="$(role_estate tofu-tl-0000-app)"
echo "aws iam list-role-tags tofu-tl-0000-app: tofu-estate=$EXEC_ESTATE" | evidence
[ "$EXEC_ESTATE" = "$IAM_ESTATE" ] || fail "carve" "the first copy's role's tofu-estate reads '$EXEC_ESTATE', want $IAM_ESTATE"
plan_clean "$MONO" "the monolith reading the role through a data source"
plan_clean "$IAM" "the iam estate"
proof "the reference crossed the boundary and nothing moved but a tag. Three estates, three clean plans."

step "6. a plan costs what its estate holds"
explain \
  "A smaller plan was the point of the decomposition. Planning the carved" \
  "team estate reads that estate and nothing else, however much the rest" \
  "of the account holds."
cmd "TF_LOG=debug choudoufu plan   # count HTTP Request Sent, team1/ against the monolith"
TEAM_CALLS="$(plan_calls "$TEAM" team-plan)"
echo "monolith plan: $MONO_CALLS requests   ·   team estate plan: $TEAM_CALLS requests" | evidence
[ "$TEAM_CALLS" -gt 0 ] 2>/dev/null || fail "carve" "the team plan made no measurable requests"
[ "$TEAM_CALLS" -lt "$MONO_CALLS" ] 2>/dev/null || fail "carve" "the carved estate did not plan cheaper than the monolith ($TEAM_CALLS vs $MONO_CALLS)"
proof "the three-resource estate plans for $TEAM_CALLS requests against the monolith's $MONO_CALLS. Cost tracks the estate."

step "7. teardown - three estates, each by its own destroy"
cmd "choudoufu apply -destroy -auto-approve   # monolith/, then iam/, then team1/"
MONO_LEFT=$((RES - 6))
for pair in "$MONO:$MONO_LEFT" "$IAM:3" "$TEAM:3"; do
  d="${pair%%:*}"; want="${pair#*:}"
  DOUT="$(cd "$d" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
    || fail "carve" "destroy failed in $d: $(tail -10 <<< "$DOUT")"
  grep -qE "Resources: 0 added, 0 changed, $want destroyed" <<< "$DOUT" \
    || fail "carve" "$d did not destroy exactly $want: $(grep -E 'Apply complete|Destroy complete' <<< "$DOUT")"
  echo "$(basename "$d"): $want destroyed" | evidence
done
proof "$MONO_LEFT + 3 + 3 = $RES. Every resource left through the estate that owned it."

echo "  What you watched: a stock estate adopted with one command, then"
echo "  carved into three estates by two tag writes, with the untaggable"
echo "  children following their parents, a reference crossing the new"
echo "  boundary through a data source, every side planning clean, and the"
echo "  carved estate planning for a fraction of the monolith's requests."
