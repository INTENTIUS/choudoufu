# gcp-the-label-is-the-boundary
# CLAIM 13 (gcp) - The tag is the boundary. ~5 min.
#
# This proof: on GCP the fence is restated, not proven by a label. IAM
# conditions cannot match a resource's labels, so #1121 rules the fence
# project-scoped: one estate per project, and each estate's principal is
# granted on its own project and nowhere else. This scenario installs that
# grant for two estates in two projects and shows it hold: Bob's apply in
# his estate's project goes through, and his write into Alice's project is
# refused by the cloud, both through choudoufu and through a plain REST
# call. BREAK=1 removes Bob's binding on his own project and requires his
# own apply to be refused, so the write that went through went through
# because of the grant.
#
# What the fence does not cover is the headline, as on Kubernetes (writes
# only there): two estates sharing one project are not fenced from each
# other's principals at all, and this scenario does not pretend they are.
#
# The emulator's IAM evaluation is opt-in (FLOCI_GCP_IAM_ENFORCEMENT=enforce
# here) and covers Cloud Storage, Pub/Sub, Resource Manager, Secret Manager
# and Cloud Run, not Compute (lex00/floci-gcp's docs/services/iam.md), so the
# writes measured are Cloud Storage's. Each principal is a service account
# the emulator mints a token for through generateAccessToken; a token it
# did not mint bypasses evaluation, so Alice and Bob each run with their own.
#
# Not yet run; its cell reads restated, the project-scoped form. Unconfirmed from
# the fork's docs: that bucket creation is evaluated against the project's
# policy (the guide says bucket evaluation includes the owning project's
# policy, and lists bucket metadata operations), and the generateAccessToken
# path under <endpoint>/v1/.

SMOKE_WORK="$SMOKE_WORKROOT/gcp-the-label-is-the-boundary"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
PA="chdf-smoke-a"     # Bob's estate's project
PB="chdf-smoke-b"     # Alice's estate's project
BOB="bob@${PA}.iam.gserviceaccount.com"
ALICE="alice@${PB}.iam.gserviceaccount.com"
ALICE_WORK="$SMOKE_WORK/alice"; BOB_WORK="$SMOKE_WORK/bob"

export FLOCI_GCP_IAM_ENFORCEMENT=enforce
gcp_up

# mint <service account email> prints a token the emulator minted for it.
# IAM Credentials is not itself evaluated, so the run's own token can mint.
mint() {
  gcpl POST "v1/projects/-/serviceAccounts/$1:generateAccessToken" \
    '{"scope":["https://www.googleapis.com/auth/cloud-platform"],"lifetime":"3600s"}' \
    | gcp_field 'd.get("accessToken", "")'
}
# grant <project> <member> installs live/gcp/estate-grant.sh for one
# estate, its gcloud lines spelled as the REST calls they make: the Cloud
# Asset API enabled in the project, and the project's policy set to two
# bindings for <member>, roles/cloudasset.viewer for the sweep and
# roles/storage.admin, the one ROLE the estate's buckets need. A policy
# write by the run's own token is an external caller, which Resource
# Manager does not evaluate: this is the administrator installing the
# grant.
grant() {
  gcpl POST "v1/projects/$1/services/cloudasset.googleapis.com:enable" '{}' >/dev/null || return 1
  gcpl POST "v1/projects/$1:setIamPolicy" \
    "{\"policy\":{\"bindings\":[{\"role\":\"roles/cloudasset.viewer\",\"members\":[\"serviceAccount:$2\"]},{\"role\":\"roles/storage.admin\",\"members\":[\"serviceAccount:$2\"]}]}}" >/dev/null
}
# as <token> <dir> <chdf args...> runs choudoufu in <dir> as one principal.
as() {
  local token="$1" dir="$2"; shift 2
  ( cd "$dir" && GOOGLE_OAUTH_ACCESS_TOKEN="$token" chdf "$@" )
}
bucket_block() { # <block name> <project> <bucket name>
  cat <<TF

resource "google_storage_bucket" "$1" {
  project                     = "$2"
  name                        = "$3"
  location                    = "US"
  uniform_bucket_level_access = true
  force_destroy               = true
}
TF
}

step "1. two projects, two principals, one grant each"
explain \
  "Alice's estate lives in $PB and Bob's in $PA. Each gets a service" \
  "account and one binding, roles/storage.admin, on its own estate's" \
  "project, beside the sweep's roles/cloudasset.viewer. Those bindings are" \
  "the whole fence: live/gcp/estate-grant.sh, the template #1121 rules" \
  "for GCP, installed here through the REST API."
cmd "POST v1/projects/$PA/serviceAccounts {accountId: bob}; POST v1/projects/$PB/serviceAccounts {accountId: alice}"
gcpl POST "v1/projects/$PA/serviceAccounts" '{"accountId":"bob"}' >/dev/null \
  || fail "gcp-the-label-is-the-boundary" "could not create Bob's service account"
gcpl POST "v1/projects/$PB/serviceAccounts" '{"accountId":"alice"}' >/dev/null \
  || fail "gcp-the-label-is-the-boundary" "could not create Alice's service account"
grant "$PA" "$BOB" || fail "gcp-the-label-is-the-boundary" "could not grant Bob on $PA"
grant "$PB" "$ALICE" || fail "gcp-the-label-is-the-boundary" "could not grant Alice on $PB"
gcpl POST "v1/projects/$PA:getIamPolicy" '{}' | gcp_field 'd.get("bindings", [])' | evidence
BOB_TOKEN="$(mint "$BOB")"; ALICE_TOKEN="$(mint "$ALICE")"
[ -n "$BOB_TOKEN" ] && [ -n "$ALICE_TOKEN" ] \
  || fail "gcp-the-label-is-the-boundary" "the emulator minted no token for Bob or Alice, so nothing below would be evaluated"
proof "each principal holds one binding on one project, and a token the emulator will evaluate."

step "2. Alice and Bob each apply their own estate"
gcp_write_root "$ALICE_WORK" smoke-gcp-alice "$PB" || fail "gcp-the-label-is-the-boundary" "could not write Alice's root"
bucket_block data "$PB" "${PB}-data" > "$ALICE_WORK/main.tf"
gcp_write_root "$BOB_WORK" smoke-gcp-bob "$PA" || fail "gcp-the-label-is-the-boundary" "could not write Bob's root"
bucket_block data "$PA" "${PA}-data" > "$BOB_WORK/main.tf"
logged gcp-boundary-init-alice "gcp-the-label-is-the-boundary" "Alice's init failed" -- in_dir "$ALICE_WORK" chdf init -input=false -no-color
logged gcp-boundary-init-bob "gcp-the-label-is-the-boundary" "Bob's init failed" -- in_dir "$BOB_WORK" chdf init -input=false -no-color
cmd "choudoufu apply -auto-approve   (as Alice, in $PB)"
A_OUT="$(as "$ALICE_TOKEN" "$ALICE_WORK" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-label-is-the-boundary" "Alice's apply in her own project failed: $A_OUT"
grep -E 'Apply complete!' <<< "$A_OUT" | evidence

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - remove Bob's storage binding; his apply in his own project must be refused"
  explain \
    "You asked for proof the assertions can fail. This removes the one" \
    "binding Bob holds. If his apply in $PA still goes through, the" \
    "allow below did not come from the grant, and the refusal in step 3" \
    "would prove nothing about it."
  cmd "POST v1/projects/$PA:setIamPolicy {bindings: [roles/cloudasset.viewer only]}"
  gcpl POST "v1/projects/$PA:setIamPolicy" "{\"policy\":{\"bindings\":[{\"role\":\"roles/cloudasset.viewer\",\"members\":[\"serviceAccount:$BOB\"]}]}}" >/dev/null \
    || fail "gcp-the-label-is-the-boundary" "BREAK: could not remove Bob's binding"
  BRC=0
  BOUT="$(as "$BOB_TOKEN" "$BOB_WORK" apply -auto-approve -input=false -no-color 2>&1)" || BRC=$?
  grep -E '^Error|PERMISSION_DENIED|403|Apply complete' <<< "$BOUT" | head -3 | evidence
  [ "$BRC" != "0" ] \
    || fail "gcp-the-label-is-the-boundary" "BREAK: with no binding Bob's apply in $PA still succeeded - the allow is not the grant's"
  grep -qE 'PERMISSION_DENIED|Error 403|403' <<< "$BOUT" \
    || fail "gcp-the-label-is-the-boundary" "BREAK: Bob's apply failed, but not on a permission denial: $BOUT"
  if gcpl GET "storage/v1/b/${PA}-data" >/dev/null 2>&1; then
    fail "gcp-the-label-is-the-boundary" "BREAK: the refused apply left a bucket behind"
  fi
  proof "caught. Without the binding Bob cannot write even in his own estate's project, so the write below goes through because of the grant and the refusal is the fence's."
  exit 0
fi

cmd "choudoufu apply -auto-approve   (as Bob, in $PA)"
B_OUT="$(as "$BOB_TOKEN" "$BOB_WORK" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-label-is-the-boundary" "Bob's apply in his own project failed: $B_OUT"
grep -E 'Apply complete!' <<< "$B_OUT" | evidence
proof "both estates applied, each by its own principal in its own project."

step "3. Bob declares a bucket in Alice's project; the cloud refuses it"
explain \
  "Bob adds a block whose project is $PB. Nothing in choudoufu stops" \
  "him from declaring it, and the lint warns that his estate now names" \
  "two projects. The apply reaches Cloud Storage as Bob, and Bob holds" \
  "nothing on $PB, so the create is refused there."
bucket_block intrude "$PB" "${PB}-intrude" >> "$BOB_WORK/main.tf"
cmd "choudoufu apply -auto-approve   (as Bob, a bucket in $PB)"
IRC=0
I_OUT="$(as "$BOB_TOKEN" "$BOB_WORK" apply -auto-approve -input=false -no-color 2>&1)" || IRC=$?
grep -E '^Error|^Warning|PERMISSION_DENIED|403' <<< "$I_OUT" | head -4 | evidence
[ "$IRC" != "0" ] \
  || fail "gcp-the-label-is-the-boundary" "Bob's apply of a bucket in Alice's project succeeded: the project grant is not a fence"
grep -qE 'PERMISSION_DENIED|Error 403|403' <<< "$I_OUT" \
  || fail "gcp-the-label-is-the-boundary" "Bob's apply in Alice's project failed, but not on a permission denial: $I_OUT"
if gcpl GET "storage/v1/b/${PB}-intrude" >/dev/null 2>&1; then
  fail "gcp-the-label-is-the-boundary" "the refused create left ${PB}-intrude behind"
fi
proof "refused by the cloud with a permission denial, and nothing was created in $PB."

step "4. the same refusal without choudoufu: a plain REST write as Bob"
explain \
  "The fence is the cloud's, not the tool's. Bob relabels Alice's bucket" \
  "with a REST PATCH and his own token; it is refused the same way."
cmd "curl -X PATCH -H 'Authorization: Bearer <Bob>' \$GCP_ENDPOINT/storage/v1/b/${PB}-data"
if GCPL_TOKEN="$BOB_TOKEN" gcpl PATCH "storage/v1/b/${PB}-data" '{"labels":{"tofu-estate":"smoke-gcp-bob"}}' >/dev/null 2>&1; then
  fail "gcp-the-label-is-the-boundary" "Bob relabelled Alice's bucket with a plain REST call"
fi
OWNER="$(gcpl GET "storage/v1/b/${PB}-data" | gcp_field 'd.get("labels", {}).get("tofu-estate", "")')"
echo "${PB}-data tofu-estate: $OWNER" | evidence
[ "$OWNER" = "smoke-gcp-alice" ] \
  || fail "gcp-the-label-is-the-boundary" "Alice's bucket reads tofu-estate=$OWNER after Bob's refused write"
proof "refused, and Alice's bucket still carries her estate's label."

step "5. tear both estates down, each as its own principal"
sed_i "$BOB_WORK/main.tf" '/resource "google_storage_bucket" "intrude"/,/^}/d'
A_D="$(as "$ALICE_TOKEN" "$ALICE_WORK" apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-label-is-the-boundary" "Alice's destroy failed: $A_D"
destroyed_exactly "gcp-the-label-is-the-boundary" 1 "$A_D"
B_D="$(as "$BOB_TOKEN" "$BOB_WORK" apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "gcp-the-label-is-the-boundary" "Bob's destroy failed: $B_D"
destroyed_exactly "gcp-the-label-is-the-boundary" 1 "$B_D"
proof "each estate destroyed by its own principal, one bucket each."

echo "  What you watched: the GCP fence is the project. Each estate's"
echo "  principal holds a grant on its own project, and the cloud refuses its"
echo "  writes anywhere else, through choudoufu or not. Two estates sharing"
echo "  one project share that fence, which is why the ruling is one estate"
echo "  per project."
