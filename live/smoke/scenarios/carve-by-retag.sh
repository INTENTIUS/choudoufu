# carve-by-retag
# CLAIM 12 - Carve by retag: a stock terralith is adopted with one command, then carved into estates by tag writes, and every side plans clean with nothing rebuilt. Needs Go. ~6 min.
#
# The estate is the blog's own fixture: tools/terralith-gen at -scale 1, one
# stock state file, 79 resources across IAM, ECS, Route 53 and EC2, with
# count, for_each and a module-nested pod. Nothing in it carries a marker
# until live-import writes one.

W="$SMOKE_WORKROOT/carve"
MONO="$W/monolith"; TEAM="$W/team1"; IAM="$W/iam"
mkdir -p "$MONO"
# The oracle mounts SMOKE_WORK at /work; the monolith is /work/monolith.
SMOKE_WORK="$W"; export SMOKE_WORK
LOGS="${CARVE_LOGS:-/tmp/carve-logs-$$}"; mkdir -p "$LOGS"

MONO_ESTATE="tl-terralith"; TEAM_ESTATE="tl-team-1"; IAM_ESTATE="tl-iam"

command -v go >/dev/null 2>&1 || fail "carve" "this scenario generates its terralith with go run ./tools/terralith-gen and needs Go on PATH"
( cd "$ROOT" && env -u PWD go run ./tools/terralith-gen -scale 1 -prefix tl -out "$MONO" -fmt-bin choudoufu >/dev/null 2>&1 ) \
  || fail "carve" "terralith-gen failed"
[ -f "$MONO/iam.tf" ] && [ -f "$MONO/modules/team_pod/main.tf" ] || fail "carve" "the generator wrote no estate at $MONO"

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

step "1. stock stands the terralith up: one state file, no markers"
explain \
  "The pinned stock OpenTofu, in its own container, applies the" \
  "generated terralith the ordinary way. Nothing here carries a marker;" \
  "identity exists only in terraform.tfstate."
cmd "docker compose run opentofu -chdir=/work/monolith apply -auto-approve"
stock -chdir=/work/monolith init -input=false -no-color >/dev/null 2>&1 || fail "carve" "stock init failed"
SOUT="$(stock -chdir=/work/monolith apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "carve" "stock apply failed: $(tail -20 <<< "$SOUT")"
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$SOUT" | grep -oE '[0-9]+')"
[ "$ADDED" = "79" ] || fail "carve" "stock added $ADDED resources, want 79"
[ -f "$MONO/terraform.tfstate" ] || fail "carve" "no terraform.tfstate after the stock apply"
MARKED_IN_STATE="$(grep -c '"tofu-address"' "$MONO/terraform.tfstate" || true)"
echo "tofu-address markers in the state file: $MARKED_IN_STATE" | evidence
[ "$MARKED_IN_STATE" = "0" ] || fail "carve" "stock's estate already carries markers"
proof "79 resources, one state file, zero markers. A stranger's terralith."

step "2. one command adopts it, and the state file is deleted"
explain \
  "Add the live block. live-import then reads the state file once and" \
  "stamps the two ownership markers on every resource that verifies." \
  "Half the estate is untaggable and needs no marker at all: its identity" \
  "composes from an already-stamped parent. Then the file goes, and the" \
  "plan reads the estate back off the tags."
cmd "choudoufu live-import -state=terraform.tfstate -estate=$MONO_ESTATE -approve ; rm terraform.tfstate ; choudoufu plan"
inject_live "$MONO/versions.tf" "$MONO_ESTATE"
( cd "$MONO" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "carve" "choudoufu init failed in the monolith"
IOUT="$(cd "$MONO" && chdf live-import -state=terraform.tfstate -estate="$MONO_ESTATE" -approve -no-color 2>&1)" \
  || fail "carve" "live-import failed: $(tail -20 <<< "$IOUT")"
pick "eligible for stamping|stamped" <<< "$IOUT" | head -2 | evidence
grep -qF "38 of 79 resource instance(s) are eligible for stamping" <<< "$IOUT" \
  || fail "carve" "live-import did not ratify 38 of 79: $(grep -E 'eligible' <<< "$IOUT" | head -2)"
rm -f "$MONO/terraform.tfstate" "$MONO/terraform.tfstate.backup"
plan_clean "$MONO" "the adopted monolith"
MONO_CALLS="$(plan_calls "$MONO" mono-plan)"
echo "the monolith plans clean with the state file gone: $MONO_CALLS requests" | evidence
proof "38 stamped, 41 composed from a parent, 0 written by hand. The state file has done its last job."

# ── the carve ────────────────────────────────────────────────────────────
TEAM_TAGGABLE="aws_iam_role.team_0001_role aws_iam_policy.team_0001_policy aws_iam_instance_profile.team_0001_profile"
carve_team_config() {
  new_estate "$TEAM" "$TEAM_ESTATE"
  local b
  for b in aws_iam_role:team_0001_role aws_iam_role_policy:team_0001_inline aws_iam_policy:team_0001_policy \
           aws_iam_role_policy_attachment:team_0001_managed_attach aws_iam_role_policy_attachment:team_0001_custom_attach \
           aws_iam_instance_profile:team_0001_profile; do
    move_block "$MONO/iam.tf" "${b%%:*}" "${b#*:}" "$TEAM/iam.tf"
  done
  ( cd "$TEAM" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "carve" "choudoufu init failed in the team estate"
}

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - move the blocks, skip the retag; both sides must show the window"
  explain \
    "The claim rests on the tag write being the move. So defeat it. Move" \
    "the six blocks to a new root and rewrite no tag. That is the" \
    "two-ledger window stock lives in, manufactured. The monolith must" \
    "propose destroying what it still owns and no longer declares, and" \
    "the new estate must propose creating what it declares and does not" \
    "own. A clean plan on either side would mean the retag never" \
    "mattered."
  cmd "move six blocks into team1/ ; choudoufu plan   # in both, no live-mv"
  carve_team_config
  BM="$(cd "$MONO" && chdf plan -input=false -no-color 2>&1 || true)"
  BT="$(cd "$TEAM" && chdf plan -input=false -no-color 2>&1 || true)"
  pick '^Plan:|Owned and undeclared' <<< "$BM" | head -2 | sed 's/^/monolith: /' | evidence
  pick '^Plan:|UNOWNED|ADOPTABLE' <<< "$BT" | head -2 | sed 's/^/team1:    /' | evidence
  if grep -q "No changes." <<< "$BM"; then
    fail "carve" "BREAK: the monolith plans clean with three of its marked resources undeclared - the retag proves nothing"
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

step "3. carve a team out: six blocks move, three tags are rewritten"
explain \
  "Team 1 is six resources. Three carry markers. The other three cannot." \
  "An inline policy and two attachments compose their identity from the" \
  "role." \
  "The git half is the same as any tool's: the blocks move to a new" \
  "root with its own live block. The state half is three runs of" \
  "live-mv -from-estate, one tag write each, made in the destination" \
  "so the tool can check the address is declared there and free there." \
  "The three children need no write at all; they follow their parent."
cmd "choudoufu live-mv -from-estate=$MONO_ESTATE aws_iam_role.team_0001_role aws_iam_role.team_0001_role   # in team1/, and two more"
carve_team_config
for addr in $TEAM_TAGGABLE; do
  MV="$(cd "$TEAM" && chdf live-mv -no-color -from-estate="$MONO_ESTATE" "$addr" "$addr" 2>&1)" \
    || fail "carve" "live-mv refused $addr: $(tail -6 <<< "$MV")"
  grep -q "Moved one live resource into this estate. This was a cloud write." <<< "$MV" \
    || fail "carve" "live-mv did not report a cloud write for $addr: $MV"
done
pick "tofu-estate|live ID" <<< "$MV" | head -2 | evidence
TEAM_ROLE_ESTATE="$(role_estate tl-team-0001-role)"
[ "$TEAM_ROLE_ESTATE" = "$TEAM_ESTATE" ] || fail "carve" "the role's live tofu-estate reads '$TEAM_ROLE_ESTATE', want $TEAM_ESTATE"
INLINE="$(awsl iam list-role-policies --role-name tl-team-0001-role --query 'PolicyNames' --output text 2>/dev/null || echo none)"
echo "aws iam list-role-tags tl-team-0001-role: tofu-estate=$TEAM_ROLE_ESTATE; inline policies still attached: $INLINE" | evidence
[ "$INLINE" = "tl-team-0001-inline" ] || fail "carve" "the inline policy did not stay attached across the move: '$INLINE'"
proof "three tag writes, read back through the plain CLI. The children were never touched and never needed to be."

step "4. both sides plan clean"
explain \
  "The monolith no longer declares the six and no longer owns the three" \
  "it could see; the team estate declares all six and owns the three." \
  "Neither side has anything to propose. No state was split, no window" \
  "was left, nothing was rebuilt."
cmd "choudoufu plan   # in monolith/, then in team1/"
plan_clean "$MONO" "the monolith after the carve"
plan_clean "$TEAM" "the carved team estate"
proof "No changes, twice. Where there was one boundary there are two, and the split cost three tag writes."

step "5. carve across a reference: the execution role leaves, the task definition keeps pointing at it"
explain \
  "The hard split is the resource on the line. The ECS task definition" \
  "reads its execution role's ARN from the role block. The role moves to" \
  "an IAM estate; the task definition stays and reads the same ARN" \
  "through a data source, the cross-estate pattern the docs give. One" \
  "tag write, and the attachment that composes from the role follows."
cmd "choudoufu live-mv -from-estate=$MONO_ESTATE aws_iam_role.svc_0000_exec_role aws_iam_role.svc_0000_exec_role   # in iam/"
new_estate "$IAM" "$IAM_ESTATE"
move_block "$MONO/iam.tf" aws_iam_role svc_0000_exec_role "$IAM/iam.tf"
move_block "$MONO/iam.tf" aws_iam_role_policy_attachment svc_0000_exec_attach "$IAM/iam.tf"
python3 - "$MONO/ecs.tf" <<'PYEOF'
import sys
p = sys.argv[1]; s = open(p).read()
old = "execution_role_arn       = aws_iam_role.svc_0000_exec_role.arn"
assert s.count(old) == 1, "the task definition's execution_role_arn line was not found once"
s = s.replace(old, "execution_role_arn       = data.aws_iam_role.svc_0000_exec_role.arn")
s += '\n# The execution role now lives in the tl-iam estate; read it by the name\n# both sides already know (live/OUTPUTS.md, the cross-estate pattern).\ndata "aws_iam_role" "svc_0000_exec_role" {\n  name = "tl-svc-0000-exec-role"\n}\n'
open(p, 'w').write(s)
PYEOF
( cd "$IAM" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "carve" "choudoufu init failed in the iam estate"
MV="$(cd "$IAM" && chdf live-mv -no-color -from-estate="$MONO_ESTATE" aws_iam_role.svc_0000_exec_role aws_iam_role.svc_0000_exec_role 2>&1)" \
  || fail "carve" "live-mv refused the execution role: $(tail -6 <<< "$MV")"
EXEC_ESTATE="$(role_estate tl-svc-0000-exec-role)"
echo "aws iam list-role-tags tl-svc-0000-exec-role: tofu-estate=$EXEC_ESTATE" | evidence
[ "$EXEC_ESTATE" = "$IAM_ESTATE" ] || fail "carve" "the execution role's tofu-estate reads '$EXEC_ESTATE', want $IAM_ESTATE"
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
proof "the six-resource estate plans for $TEAM_CALLS requests against the monolith's $MONO_CALLS. Cost tracks the estate."

step "7. teardown - three estates, each by its own destroy"
cmd "choudoufu apply -destroy -auto-approve   # monolith/, then iam/, then team1/"
for pair in "$MONO:71" "$IAM:2" "$TEAM:6"; do
  d="${pair%%:*}"; want="${pair#*:}"
  DOUT="$(cd "$d" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
    || fail "carve" "destroy failed in $d: $(tail -10 <<< "$DOUT")"
  grep -qE "Resources: 0 added, 0 changed, $want destroyed" <<< "$DOUT" \
    || fail "carve" "$d did not destroy exactly $want: $(grep -E 'Apply complete|Destroy complete' <<< "$DOUT")"
  echo "$(basename "$d"): $want destroyed" | evidence
done
proof "71 + 2 + 6 = 79. Every resource left through the estate that owned it."

echo "  What you watched: a stock terralith adopted with one command, then"
echo "  carved into three estates by four tag writes, with the untaggable"
echo "  children following their parents, a reference crossing the new"
echo "  boundary through a data source, every side planning clean, and the"
echo "  carved estate planning for a fraction of the monolith's requests."
