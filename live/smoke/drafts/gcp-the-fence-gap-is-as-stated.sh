# gcp-the-fence-gap-is-as-stated
# CLAIM 52 (gcp) - The fence over derived types is what the docs say. ~5 min.
#
# This proof: live/MARKERS.md, "What a GCP grant cannot reach", says a
# declaration-carried type is fenced at project scope only, the same as a
# labelled one, because an IAM condition cannot read a label and these
# types have none to read. This scenario measures that row on a derived
# type, google_storage_bucket_iam_member. A principal granted by
# live/gcp/estate-grant.sh creates one in its own estate's project; the
# same principal is refused writing one in another project; and a second
# estate's principal granted on the same project rewrites the first
# estate's member unopposed, which is the gap the row states.
#
# Claim D5 of #2119. BREAK=1 removes Bob's storage binding on his own
# project and requires his apply of the derived member to be refused, so
# the write that went through went through because of the grant.
#
# The emulator's IAM evaluation is opt-in (FLOCI_GCP_IAM_ENFORCEMENT=enforce
# here) and covers Cloud Storage among others but not Compute
# (lex00/floci-gcp's docs/services/iam.md), so the derived type measured is
# a bucket's IAM member, not a firewall. Each principal is a service
# account the emulator mints a token for. Not yet run, so its cell reads
# unproven. Unconfirmed from the fork's docs: that a bucket's setIamPolicy
# is evaluated against the project's policy, and the generateAccessToken
# path, the same two gcp-the-label-is-the-boundary rests on.

SMOKE_WORK="$SMOKE_WORKROOT/gcp-the-fence-gap-is-as-stated"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
PA="chdf-smoke-a"     # Bob's and Carol's estates' project
PB="chdf-smoke-b"     # Alice's estate's project
BOB="bob@${PA}.iam.gserviceaccount.com"
CAROL="carol@${PA}.iam.gserviceaccount.com"
ALICE="alice@${PB}.iam.gserviceaccount.com"
ROLE="roles/storage.objectViewer"
BOB_MEMBER="serviceAccount:$CAROL"
ALICE_MEMBER="serviceAccount:$ALICE"
BOB_WORK="$SMOKE_WORK/bob"; ALICE_WORK="$SMOKE_WORK/alice"

export FLOCI_GCP_IAM_ENFORCEMENT=enforce
gcp_up

# mint <service account email> prints a token the emulator minted for it.
mint() {
  gcpl POST "v1/projects/-/serviceAccounts/$1:generateAccessToken" \
    '{"scope":["https://www.googleapis.com/auth/cloud-platform"],"lifetime":"3600s"}' \
    | gcp_field 'd.get("accessToken", "")'
}
# grant <project> <storage member...> installs live/gcp/estate-grant.sh
# for every estate in <project>, its gcloud lines spelled as the REST calls
# they make: the Cloud Asset API enabled, roles/cloudasset.viewer for each
# estate's principal, and roles/storage.admin, the ROLE the estates' types
# need, for each one named. Two estates sharing a project each hold the
# same two bindings on it; that sharing is what the stated gap is about.
grant() {
  local project="$1"; shift
  local members
  members="$(printf '"serviceAccount:%s",' "$@")"; members="[${members%,}]"
  gcpl POST "v1/projects/$project/services/cloudasset.googleapis.com:enable" '{}' >/dev/null || return 1
  gcpl POST "v1/projects/$project:setIamPolicy" \
    "{\"policy\":{\"bindings\":[{\"role\":\"roles/cloudasset.viewer\",\"members\":$members},{\"role\":\"roles/storage.admin\",\"members\":$members}]}}" >/dev/null
}
as() { # <token> <dir> <chdf args...>
  local token="$1" dir="$2"; shift 2
  ( cd "$dir" && GOOGLE_OAUTH_ACCESS_TOKEN="$token" chdf "$@" )
}
bucket_block() { # <project> <bucket name>
  cat <<TF
resource "google_storage_bucket" "data" {
  project                     = "$1"
  name                        = "$2"
  location                    = "US"
  uniform_bucket_level_access = true
  force_destroy               = true
}
TF
}
member_block() { # <member>
  cat <<TF

resource "google_storage_bucket_iam_member" "reader" {
  bucket = google_storage_bucket.data.name
  role   = "$ROLE"
  member = "$1"
}
TF
}

step "1. three principals, each granted on its estate's project"
explain \
  "Bob's and Carol's estates both live in $PA; Alice's lives in $PB." \
  "Each principal gets the bindings live/gcp/estate-grant.sh installs on" \
  "its own estate's project, installed here through the REST API by the" \
  "run's own token, which Resource Manager does not evaluate."
for sa in "$PA bob" "$PA carol" "$PB alice"; do
  read -r sa_project sa_id <<< "$sa"
  gcpl POST "v1/projects/$sa_project/serviceAccounts" "{\"accountId\":\"$sa_id\"}" >/dev/null \
    || fail "gcp-the-fence-gap-is-as-stated" "could not create $sa_id's service account in $sa_project"
done
grant "$PA" "$BOB" "$CAROL" || fail "gcp-the-fence-gap-is-as-stated" "could not grant Bob and Carol on $PA"
grant "$PB" "$ALICE" || fail "gcp-the-fence-gap-is-as-stated" "could not grant Alice on $PB"
gcpl POST "v1/projects/$PA:getIamPolicy" '{}' | gcp_field 'd.get("bindings", [])' | evidence
BOB_TOKEN="$(mint "$BOB")"; CAROL_TOKEN="$(mint "$CAROL")"; ALICE_TOKEN="$(mint "$ALICE")"
[ -n "$BOB_TOKEN" ] && [ -n "$CAROL_TOKEN" ] && [ -n "$ALICE_TOKEN" ] \
  || fail "gcp-the-fence-gap-is-as-stated" "the emulator minted no token for one of the three, so nothing below would be evaluated"
proof "three principals, each holding the estate grant on its own estate's project, each with a token the emulator evaluates."

step "2. each estate applies its bucket"
gcp_write_root "$ALICE_WORK" smoke-gcp-alice "$PB" || fail "gcp-the-fence-gap-is-as-stated" "could not write Alice's root"
{ bucket_block "$PB" "${PB}-gap-data"; member_block "$ALICE_MEMBER"; } > "$ALICE_WORK/main.tf"
gcp_write_root "$BOB_WORK" smoke-gcp-bob "$PA" || fail "gcp-the-fence-gap-is-as-stated" "could not write Bob's root"
bucket_block "$PA" "${PA}-gap-data" > "$BOB_WORK/main.tf"
logged gcp-gap-init-alice "gcp-the-fence-gap-is-as-stated" "Alice's init failed" -- in_dir "$ALICE_WORK" chdf init -input=false -no-color
logged gcp-gap-init-bob "gcp-the-fence-gap-is-as-stated" "Bob's init failed" -- in_dir "$BOB_WORK" chdf init -input=false -no-color
cmd "choudoufu apply -auto-approve   (as Alice in $PB: a bucket and its IAM member; as Bob in $PA: a bucket)"
A_OUT="$(as "$ALICE_TOKEN" "$ALICE_WORK" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-fence-gap-is-as-stated" "Alice's apply in her own project failed: $A_OUT"
grep -qE 'Apply complete! Resources: 2 added' <<< "$A_OUT" \
  || fail "gcp-the-fence-gap-is-as-stated" "Alice's apply did not add her bucket and its member: $A_OUT"
B_OUT="$(as "$BOB_TOKEN" "$BOB_WORK" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-fence-gap-is-as-stated" "Bob's apply in his own project failed: $B_OUT"
{ grep -E 'Apply complete!' <<< "$A_OUT$B_OUT" || true; } | evidence
proof "Alice holds a bucket and a derived IAM member in $PB, Bob a bucket in $PA."

member_block "$BOB_MEMBER" >> "$BOB_WORK/main.tf"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - remove Bob's storage binding; his derived member must be refused"
  explain \
    "You asked for proof the assertions can fail. Bob keeps only the" \
    "sweep's binding on $PA. If his apply of the IAM member still goes" \
    "through, the grant is not what reaches the derived type."
  cmd "POST v1/projects/$PA:setIamPolicy {Bob: roles/cloudasset.viewer only}"
  gcpl POST "v1/projects/$PA:setIamPolicy" \
    "{\"policy\":{\"bindings\":[{\"role\":\"roles/cloudasset.viewer\",\"members\":[\"serviceAccount:$BOB\",\"serviceAccount:$CAROL\"]},{\"role\":\"roles/storage.admin\",\"members\":[\"serviceAccount:$CAROL\"]}]}}" >/dev/null \
    || fail "gcp-the-fence-gap-is-as-stated" "BREAK: could not remove Bob's binding"
  BRC=0
  BOUT="$(as "$BOB_TOKEN" "$BOB_WORK" apply -auto-approve -input=false -no-color 2>&1)" || BRC=$?
  { grep -E '^Error|PERMISSION_DENIED|403|Apply complete' <<< "$BOUT" || true; } | head -3 | evidence
  [ "$BRC" != "0" ] \
    || fail "gcp-the-fence-gap-is-as-stated" "BREAK: with no storage binding Bob's derived member still applied - the allow is not the grant's"
  grep -qE 'PERMISSION_DENIED|Error 403|403' <<< "$BOUT" \
    || fail "gcp-the-fence-gap-is-as-stated" "BREAK: Bob's apply failed, but not on a permission denial: $BOUT"
  if gcp_bucket_has_member "${PA}-gap-data" "$ROLE" "$BOB_MEMBER"; then
    fail "gcp-the-fence-gap-is-as-stated" "BREAK: the refused apply left the member in the bucket's policy"
  fi
  proof "caught. Without the grant Bob cannot write the derived member even in his own project, so the write below goes through because of the grant."
  exit 0
fi

step "3. the grant reaches the derived type in the estate's own project"
explain \
  "Bob adds a google_storage_bucket_iam_member to his bucket. It has no" \
  "labels argument; the estate grant is a project binding, so it reaches" \
  "this type exactly as it reaches the labelled bucket."
cmd "choudoufu apply -auto-approve   (as Bob, the IAM member in $PA)"
M_OUT="$(as "$BOB_TOKEN" "$BOB_WORK" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-fence-gap-is-as-stated" "Bob's apply of the derived member in his own project failed: $M_OUT"
{ grep -E 'Apply complete!' <<< "$M_OUT" || true; } | evidence
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$M_OUT" \
  || fail "gcp-the-fence-gap-is-as-stated" "Bob's apply did not add exactly the member: $M_OUT"
gcp_bucket_has_member "${PA}-gap-data" "$ROLE" "$BOB_MEMBER" \
  || fail "gcp-the-fence-gap-is-as-stated" "the member is not in Bob's bucket's policy after the apply"
proof "applied by Bob's principal in his estate's project, and present in the policy."

step "4. outside the project, the fence holds"
explain \
  "Bob takes Alice's member out of her bucket's policy with a REST PUT and" \
  "his own token. He holds nothing on $PB, so the write is refused there."
cmd "PUT storage/v1/b/${PB}-gap-data/iam without $ALICE_MEMBER   (as Bob)"
if GCPL_TOKEN="$BOB_TOKEN" gcp_bucket_drop_member "${PB}-gap-data" "$ROLE" "$ALICE_MEMBER" 2>/dev/null; then
  fail "gcp-the-fence-gap-is-as-stated" "Bob rewrote the IAM policy of a bucket in $PB, a project he holds no binding on"
fi
gcp_bucket_has_member "${PB}-gap-data" "$ROLE" "$ALICE_MEMBER" \
  || fail "gcp-the-fence-gap-is-as-stated" "Alice's member is gone after Bob's refused write"
echo "${PB}-gap-data still grants $ROLE to $ALICE_MEMBER" | evidence
proof "refused, and Alice's derived member stands."

step "5. inside the project, the gap is what the docs say"
explain \
  "Carol runs a different estate in the same project and holds the same" \
  "grant. Nothing on Bob's IAM member says whose it is, and nothing in" \
  "IAM could read it if it did, so her token rewrites his bucket's policy" \
  "unopposed. live/MARKERS.md states exactly this: project only. The" \
  "next plan of Bob's estate sees the member gone and proposes it back."
cmd "PUT storage/v1/b/${PA}-gap-data/iam without $BOB_MEMBER   (as Carol)"
GCPL_TOKEN="$CAROL_TOKEN" gcp_bucket_drop_member "${PA}-gap-data" "$ROLE" "$BOB_MEMBER" \
  || fail "gcp-the-fence-gap-is-as-stated" "Carol's write to Bob's bucket's policy was refused; the stated gap says a principal granted on the project reaches it, so either the docs or the grant are wrong"
if gcp_bucket_has_member "${PA}-gap-data" "$ROLE" "$BOB_MEMBER"; then
  fail "gcp-the-fence-gap-is-as-stated" "Carol's write answered, but Bob's member is still there"
fi
G_PLAN="$(as "$BOB_TOKEN" "$BOB_WORK" plan -input=false -no-color 2>&1)" \
  || fail "gcp-the-fence-gap-is-as-stated" "Bob's replan failed: $G_PLAN"
{ grep -E '^Plan:|will be created' <<< "$G_PLAN" || true; } | evidence
grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$G_PLAN" \
  || fail "gcp-the-fence-gap-is-as-stated" "Bob's replan does not propose his member back: $G_PLAN"
proof "allowed, as stated: a second estate's principal in the same project reaches the first estate's derived type, and the first estate's next plan shows it."

step "6. tear the estates down, each as its own principal"
R_OUT="$(as "$BOB_TOKEN" "$BOB_WORK" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-fence-gap-is-as-stated" "Bob's re-apply failed: $R_OUT"
B_D="$(as "$BOB_TOKEN" "$BOB_WORK" apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-fence-gap-is-as-stated" "Bob's destroy failed: $B_D"
destroyed_exactly "gcp-the-fence-gap-is-as-stated" 2 "$B_D"
A_D="$(as "$ALICE_TOKEN" "$ALICE_WORK" apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-fence-gap-is-as-stated" "Alice's destroy failed: $A_D"
destroyed_exactly "gcp-the-fence-gap-is-as-stated" 2 "$A_D"
proof "each estate destroyed by its own principal, a bucket and its member each."

echo "  What you watched: a derived GCP type fenced exactly as the docs say."
echo "  The estate grant reaches it in the estate's own project and nowhere"
echo "  else, and any principal granted on that project reaches it too, which"
echo "  is why the ruling is one estate per project."
