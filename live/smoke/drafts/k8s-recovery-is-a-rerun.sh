# k8s-recovery-is-a-rerun
# CLAIM 5 (kubernetes) - A crash is fixed by re-running. ~3 min.
#
# This proof: an apply killed with SIGKILL after one ConfigMap's create
# returned and before the next object's create was sent leaves an object the
# next plan binds by its label, namespace and name; the re-run creates only
# what the killed run never reached, with no duplicate and no orphan, and
# replans empty.
#
# DRAFT: written and never run (live/smoke/drafts/README.md). Claim 5's
# Kubernetes cell stays open until both arms have run green. The property is
# already measured by the gauntlet's day2_crash stage on the kind lane
# (live/GAUNTLET.md stage 10); this is the BREAK-controlled smoke form of it.
#
# The kill lands on a file, never on a timer. terraform_data.pause sits
# between the two ConfigMaps in one dependency chain and its provisioner
# touches pause-started and sleeps, so when that file exists ConfigMap
# first has been created and ConfigMap second has not been sent.
#
# terraform_data.pause is record-carried, through the record store a live
# block implies (.tofu-records). Its record is written once, after the
# walk, so the killed run leaves none, and the re-run creates it again and
# runs its provisioner again. That is the AWS proof's record window, stated
# rather than hidden: the plan below is required to propose it.
#
# BREAK=1 strips the tofu-estate label from ConfigMap first after the kill
# and requires the next plan to refuse it by name, exit 1. Without that
# control the empty "first is not created again" assertion would read the
# same if the plan found first by some route other than its label.

W="$SMOKE_WORKROOT/k8s-killed"
mkdir -p "$W"
SMOKE_WORK="$W"; export SMOKE_WORK
ESTATE="smoke-k8s-killed"
NS="smoke-k8s-killed"
cat > "$W/main.tf" <<TFEOF
terraform {
  live {
    estate = "$ESTATE"
  }
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}

resource "kubernetes_namespace" "app" {
  metadata {
    name = "$NS"
  }
}

resource "kubernetes_config_map" "first" {
  metadata {
    name      = "first"
    namespace = "$NS"
  }
  data       = { order = "1" }
  depends_on = [kubernetes_namespace.app]
}

resource "terraform_data" "pause" {
  input      = "pause"
  depends_on = [kubernetes_config_map.first]

  provisioner "local-exec" {
    command = "if [ -f '$W/pause-done' ]; then exit 0; fi; touch '$W/pause-started'; exec sleep 120"
  }
}

resource "kubernetes_config_map" "second" {
  metadata {
    name      = "second"
    namespace = "$NS"
  }
  data       = { order = "2" }
  depends_on = [terraform_data.pause]
}
TFEOF

cluster_up

step "the claim"
explain \
  "A crash is fixed by re-running. An apply that dies part-way leaves" \
  "objects it created and objects it never reached. Stock keeps the" \
  "first kind in a state file written as it goes; this run keeps no" \
  "state file, so the only thing that can tie a created object back to" \
  "its block is what rode the create: the tofu-estate label. This kills" \
  "an apply in between two creates, with SIGKILL, so nothing cleans up," \
  "and requires the next run to finish the job."

step "1. an apply killed between two creates"
explain \
  "One chain: a namespace, ConfigMap first, a pause, ConfigMap second." \
  "The pause's provisioner touches a file and sleeps. When the file" \
  "exists, first has been created and second has not been sent, and the" \
  "whole process tree is killed with SIGKILL."
cmd "choudoufu apply -auto-approve &   # then kill -9 once pause has started"
logged k8s-recovery-init "k8s-killed" "init failed" -- in_dir "$W" chdf init -input=false -no-color
rm -f "$W/pause-started" "$W/pause-done"
( cd "$W" && "$TOFU" apply -auto-approve -input=false -no-color >"$W/killed.out" 2>&1 ) &
APPLY_PID=$!
for _ in $(seq 1 120); do [ -f "$W/pause-started" ] && break; sleep 1; done
[ -f "$W/pause-started" ] || { kill -9 "$APPLY_PID" 2>/dev/null; fail "k8s-killed" "the pause never started, so there was no apply in flight between the two creates: $(cat "$W/killed.out")"; }
kill -0 "$APPLY_PID" 2>/dev/null || fail "k8s-killed" "the apply had already exited before it could be killed: $(cat "$W/killed.out")"
# The background subshell is not choudoufu; the whole tree is killed, as
# k8s-records-in-the-cluster's step 3 does (#1783), and checked dead.
KIDS="$(smoke_descendants "$APPLY_PID")" || KIDS=""
[ -n "$KIDS" ] || fail "k8s-killed" "found no process under the background apply's subshell, so there was no choudoufu to kill"
# shellcheck disable=SC2086  # a list of pids, split on purpose
kill -9 "$APPLY_PID" $KIDS 2>/dev/null || true
wait "$APPLY_PID" 2>/dev/null && fail "k8s-killed" "the killed apply exited 0"
ALIVE=""
for _ in $(seq 1 10); do
  ALIVE=""
  for pid in $KIDS; do kill -0 "$pid" 2>/dev/null && ALIVE="$ALIVE $pid"; done
  [ -z "$ALIVE" ] && break
  sleep 1
done
[ -z "$ALIVE" ] || fail "k8s-killed" "processes of the killed apply are still running after SIGKILL (pids$ALIVE)"
grep -q "Apply complete" "$W/killed.out" && fail "k8s-killed" "the apply completed; it was not killed in flight"
FIRST_LABEL="$(kc get configmap first -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}' 2>&1)" \
  || fail "k8s-killed" "ConfigMap first is not in the cluster after the kill, so the kill landed before the create this claim is about: $FIRST_LABEL"
[ "$FIRST_LABEL" = "$ESTATE" ] || fail "k8s-killed" "ConfigMap first carries tofu-estate='$FIRST_LABEL', want $ESTATE: the label did not ride the create"
if kc get configmap second -n "$NS" >/dev/null 2>&1; then
  fail "k8s-killed" "ConfigMap second exists, so the kill landed after the chain finished"
fi
FIRST_UID="$(kc get configmap first -n "$NS" -o jsonpath='{.metadata.uid}')"
[ ! -f "$W/.terraform/choudoufu-cache.tfstate" ] || note "a cache file exists from the killed run; the plan below never consults it for ownership"
echo "first: created, tofu-estate=$FIRST_LABEL, uid $FIRST_UID; second: absent; no state file" | evidence
[ ! -f "$W/terraform.tfstate" ] || fail "k8s-killed" "a terraform.tfstate exists; a live-block apply must never write one"
proof "killed with SIGKILL between the two creates: first exists and carries the label, second was never sent, and nothing was written that remembers either."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip first's label; the next plan must refuse it by name"
  explain \
    "If the plan below binds first by its label, removing the label must" \
    "change what it says. An object holding a declared name and carrying" \
    "no label is nobody's (#1108): the plan stops with an error naming it" \
    "rather than proposing a create the API server would refuse with 409" \
    "(#1546)."
  cmd "kubectl label configmap first -n $NS tofu-estate- ; choudoufu plan"
  kc label configmap first -n "$NS" tofu-estate- >/dev/null || fail "k8s-killed" "BREAK: could not strip the label"
  touch "$W/pause-done"
  BRC=0
  BOUT="$(cd "$W" && chdf plan -input=false -no-color 2>&1)" || BRC=$?
  BFLAT="$(tr '\n' ' ' <<< "$BOUT" | tr -s ' ')"
  grep -E '^Error: |^Plan:' <<< "$BOUT" | head -2 | evidence
  echo "plan exit: $BRC" | evidence
  [ "$BRC" = "1" ] || fail "k8s-killed" "BREAK: the plan exited $BRC with first's label stripped, want 1: $BOUT"
  grep -q 'Error: Unlabelled live object holds the declared name' <<< "$BOUT" \
    || fail "k8s-killed" "BREAK: the plan does not refuse the unlabelled ConfigMap by name: $BOUT"
  grep -q 'kubernetes_config_map.first declares, and carries no tofu-estate label' <<< "$BFLAT" \
    || fail "k8s-killed" "BREAK: the refusal does not name kubernetes_config_map.first: $BOUT"
  proof "caught: with the label gone the plan refuses first by name, so the binding the main arm relies on is the label and nothing else."
  exit 0
fi

step "2. the next plan names what the killed run left, and binds it"
explain \
  "The plan reads the cluster. first carries the label and is found by" \
  "its kind, namespace and name, so it is not created again. pause has" \
  "no record (records are written after the walk, and the walk never" \
  "finished), so it is created again: that is the record window, and it" \
  "is required here rather than hidden. second was never sent."
cmd "choudoufu plan"
touch "$W/pause-done"
P1="$(cd "$W" && chdf plan -input=false -no-color 2>&1)" || fail "k8s-killed" "the plan after the kill failed: $P1"
grep -E '^Plan:|will be created' <<< "$P1" | evidence
grep -qE 'Plan: 2 to add, 0 to change, 0 to destroy' <<< "$P1" \
  || fail "k8s-killed" "the plan after the kill does not propose exactly pause and second: $P1"
grep -q 'kubernetes_config_map.second will be created' <<< "$P1" \
  || fail "k8s-killed" "the plan does not propose second, the object the killed run never reached: $P1"
grep -q 'terraform_data.pause will be created' <<< "$P1" \
  || fail "k8s-killed" "the plan does not propose pause, whose record the killed run never wrote: $P1"
if grep -qE 'kubernetes_config_map.first will be|kubernetes_namespace.app will be' <<< "$P1"; then
  fail "k8s-killed" "the plan proposes a change to an object the killed run created; it was not bound by its label: $P1"
fi
proof "first and the namespace are bound by their label; the plan proposes exactly the two things the killed run left undone."

step "3. the re-run finishes the job"
cmd "choudoufu apply -auto-approve ; choudoufu plan"
A2="$(cd "$W" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-killed" "the re-run failed: $A2"
grep -E 'Apply complete!' <<< "$A2" | evidence
grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$A2" \
  || fail "k8s-killed" "the re-run did not add exactly pause and second: $A2"
P2="$(cd "$W" && chdf plan -input=false -no-color 2>&1)" || fail "k8s-killed" "the replan failed: $P2"
grep -q "No changes." <<< "$P2" || fail "k8s-killed" "the replan after the re-run is not empty: $P2"
AFTER_UID="$(kc get configmap first -n "$NS" -o jsonpath='{.metadata.uid}')"
[ "$AFTER_UID" = "$FIRST_UID" ] \
  || fail "k8s-killed" "ConfigMap first's uid moved from $FIRST_UID to $AFTER_UID: the re-run replaced the object the killed run made"
LABELLED="$(kc get configmaps -n "$NS" -l "tofu-estate=$ESTATE" -o name 2>&1)" \
  || fail "k8s-killed" "could not list the estate's ConfigMaps: $LABELLED"
echo "$LABELLED" | evidence
[ "$(grep -c '^configmap/' <<< "$LABELLED")" = "2" ] \
  || fail "k8s-killed" "the namespace holds $(grep -c '^configmap/' <<< "$LABELLED") ConfigMaps carrying the label, want 2 (first and second): $LABELLED"
proof "one re-run: 2 added, first kept with the uid the killed run gave it, an empty replan, and exactly the two labelled ConfigMaps the configuration declares."

step "4. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$W" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "k8s-killed" "teardown failed: $DOUT"
destroyed_exactly "k8s-killed" 4 "$DOUT"
proof "4 destroyed: the namespace, both ConfigMaps and the pause."

echo "  What you watched: an apply killed with SIGKILL between two creates on"
echo "  a real cluster, and the next run finishing it from the label alone,"
echo "  with no state file, no duplicate and no orphan."
