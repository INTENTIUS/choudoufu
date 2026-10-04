# shellcheck shell=bash
# live/e2e/reference-eks/stages.sh: the day-2 stage bodies of reference-eks
# (#1113), written once and run by both scripts that run the estate:
#
#   live/e2e/reference-eks/run.sh         the gauntlet crossing, on floci-eks
#   live/live-cert/reference-eks.sh       the real-AWS certification
#
# estate.sh is the configuration; this file is what the stages do to it. A
# stage that needs to know which target it is on asks a hook, never a
# variable naming the target, so the assertion text is the same on both.
#
# Written under the maintainer's no-testing ruling and NOT run by the change
# that added it: the first emulator run of live/e2e/reference-eks/run.sh is
# its first measurement.
#
# The caller defines, before calling any function here:
#
#   fail <msg>, log <msg>    the script's own (fail records CURRENT_STAGE)
#   ref_kubectl <args>       kubectl against the estate's cluster
#   ref_aws <args>           the AWS CLI against the estate's account
#   ref_tofu <dir> <args>    choudoufu in <dir>, against the target
#   ref_tofu_crash <dir> <args>
#                            the e2eTestingFeatures build (day2_crash's
#                            engine-delivered interrupt), same environment
#   ref_stock <dir> <args>   the stock binary in <dir>, against the target
#   ref_root <dir> <estate-or-empty> [variant...]
#                            writes <dir>/main.tf from estate.sh, with the
#                            target's provider block and record store
#   ref_aws_provider         prints the target's aws provider block
#   ref_labelled             how many cluster-leg objects carry
#                            tofu-estate=$ESTATE (namespaces, service
#                            accounts, config maps, deployments, secrets)
#   ref_aws_marked           how many AWS-leg objects carry the estate's
#                            markers, by the target's own listing
#   ref_crash_rename         day2_crash's create_before_destroy rename window
#                            (#1768): runs it and sets CRASH_RENAME_DETAIL,
#                            or returns 3 when this target cannot read the
#                            record it asserts on, leaving the reason in
#                            CRASH_RENAME_DETAIL
#
# and these variables: ESTATE, ADOPTED (the migrated root), STOCK (stock's
# cold_deploy root, its state the oracles' starting point), WORK, CLUSTER,
# REGION, AWS_N and CLUSTER_N (cold_deploy's two leg counts), and
# REF_RECORDS, the adopted root's local record store directory, or empty
# when its records are not local (the live-cert cycle keeps them in the
# cluster, record_store "kubernetes", #1524).
#
# The cluster leg's namespace is always app; estate.sh declares it.
REF_NS="app"

# ── small readers ──────────────────────────────────────────────────────────
_ref_plan_line() { grep -E '^Plan:|^No changes' <<< "$1" | head -1 | sed 's/\.$//'; }
_ref_noop() {
  grep -qF 'No changes.' <<< "$1" && return 0
  grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$1"
}
# _ref_addrs <plan> <verb>: the addresses a plan says "will be <verb>", sorted.
_ref_addrs() {
  grep -E "^[[:space:]]*# \\S+ will be $2" <<< "$1" | sed -E 's/^[[:space:]#]*//; s/ will be .*$//' | sort -u
}
_ref_exists() { ref_kubectl get "$1" "$2" -n "$REF_NS" >/dev/null 2>&1; }

# ══════════════════════════════════════════════════════════════════════
# The stock oracles, taken before migrate marks anything.
# ══════════════════════════════════════════════════════════════════════
#
# reference-ec2-vpc's B1.5 shape: plan-only, each on a copy of cold_deploy's
# own root and state, while the cloud is still exactly what stock left. A
# stock plan after migrate would carry an update per marked object (stock
# does not declare the markers), so this is the one moment its plan for a
# day-2 change is that change and nothing else. Nothing here applies.
#
# The two oracles a plan cannot give - a create_before_destroy replace's
# apply order, and a half-applied graph's remainder - are applied for real
# in a stock root of their own (reference_eks_oracle_root), inside their own
# stages.
#
# A failure here is not this run's to blame on a stage: each oracle's exit
# status is kept beside its output, and the stage that reads it fails on it.
REF_ORACLES=""
reference_eks_stock_oracles() {
  REF_ORACLES="$WORK/oracles"
  mkdir -p "$REF_ORACLES"
  local name

  # drift: the same tamper drift_reconverge makes, planned by stock, then
  # put back exactly.
  _ref_oracle_copy drift
  if ref_kubectl patch configmap app-config -n "$REF_NS" --type merge -p '{"data":{"greeting":"tampered"}}' >/dev/null 2>&1; then
    ref_root "$REF_ORACLES/drift" "" || true
    ref_stock "$REF_ORACLES/drift" plan -input=false -no-color > "$REF_ORACLES/drift.out" 2>&1
    echo $? > "$REF_ORACLES/drift.rc"
    ref_kubectl patch configmap app-config -n "$REF_NS" --type merge -p '{"data":{"greeting":"hello from reference-eks"}}' >/dev/null 2>&1 \
      || echo "could not put app-config's greeting back after the oracle's tamper" > "$REF_ORACLES/drift.restore-failed"
  else
    echo "could not tamper app-config with kubectl for the drift oracle" > "$REF_ORACLES/drift.out"
    echo 98 > "$REF_ORACLES/drift.rc"
  fi

  _ref_oracle_plan reviewed reviewed
  _ref_oracle_plan rename team moved
  _ref_oracle_plan remove drop_cm
  _ref_oracle_plan count_down shards=1

  # count_up: stock at one shard, scaling back to two. The state copy drops
  # shard[1] locally (no API call), which is the position stock's own
  # scale-down apply would have left it in.
  _ref_oracle_copy count_up
  if ref_stock "$REF_ORACLES/count_up" state rm 'kubernetes_config_map_v1.shard[1]' > "$REF_ORACLES/count_up.staterm" 2>&1; then
    ref_root "$REF_ORACLES/count_up" "" || true
    ref_stock "$REF_ORACLES/count_up" plan -input=false -no-color > "$REF_ORACLES/count_up.out" 2>&1
    echo $? > "$REF_ORACLES/count_up.rc"
  else
    cp "$REF_ORACLES/count_up.staterm" "$REF_ORACLES/count_up.out"
    echo 97 > "$REF_ORACLES/count_up.rc"
  fi

  # teardown: stock's whole-estate destroy, planned.
  _ref_oracle_copy teardown
  ref_stock "$REF_ORACLES/teardown" plan -destroy -input=false -no-color > "$REF_ORACLES/teardown.out" 2>&1
  echo $? > "$REF_ORACLES/teardown.rc"

  for name in drift reviewed rename remove count_down count_up teardown; do
    log "  stock oracle $name: exit $(cat "$REF_ORACLES/$name.rc"), $(_ref_plan_line "$(cat "$REF_ORACLES/$name.out")")"
  done
}
# _ref_oracle_copy <name>: cold_deploy's configuration, lock file and state
# copied, its .terraform linked rather than copied - the providers there are
# hundreds of megabytes each and a plan only reads them. The state is the
# copy's own, so a `state rm` in one oracle touches no other.
_ref_oracle_copy() {
  local d="${REF_ORACLES:?}/$1"
  rm -rf "$d"; mkdir -p "$d"
  cp "$STOCK/main.tf" "$STOCK/terraform.tfstate" "$d/"
  [ -f "$STOCK/.terraform.lock.hcl" ] && cp "$STOCK/.terraform.lock.hcl" "$d/"
  ln -s "$(cd "$STOCK" && pwd)/.terraform" "$d/.terraform"
}
_ref_oracle_plan() { # <name> <variant...>
  local name="$1"; shift
  _ref_oracle_copy "$name"
  if ref_root "$REF_ORACLES/$name" "" "$@"; then
    ref_stock "$REF_ORACLES/$name" plan -input=false -no-color > "$REF_ORACLES/$name.out" 2>&1
    echo $? > "$REF_ORACLES/$name.rc"
  else
    echo "could not write the $name oracle's root" > "$REF_ORACLES/$name.out"
    echo 96 > "$REF_ORACLES/$name.rc"
  fi
}
# _ref_oracle <name>: sets REF_ORACLE_OUT to the oracle's plan text, or
# fails naming why there is none. Never called inside $(...): fail has to
# print its GAUNTLET line on the script's own stdout.
REF_ORACLE_OUT=""
_ref_oracle() {
  [ -n "$REF_ORACLES" ] && [ -f "$REF_ORACLES/$1.rc" ] || fail "stock's $1 oracle was never taken (reference_eks_stock_oracles runs between cold_deploy and migrate)"
  [ "$(cat "$REF_ORACLES/$1.rc")" = "0" ] || { tail -20 "$REF_ORACLES/$1.out"; fail "stock's $1 oracle plan, on a copy of cold_deploy's state, exited $(cat "$REF_ORACLES/$1.rc")"; }
  REF_ORACLE_OUT="$(cat "$REF_ORACLES/$1.out")"
}

# ══════════════════════════════════════════════════════════════════════
# drift_reconverge: one cluster-leg object tampered out of band
# ══════════════════════════════════════════════════════════════════════
reference_eks_stage_drift_reconverge() {
  gauntlet_begin_stage drift_reconverge
  log "=== drift_reconverge: kubectl patch app-config; one change proposed, stock's oracle the same ==="
  local oracle plan reconv greeting
  _ref_oracle drift; oracle="$REF_ORACLE_OUT"
  [ ! -f "$REF_ORACLES/drift.restore-failed" ] || fail "$(cat "$REF_ORACLES/drift.restore-failed") - the estate did not start this stage where cold_deploy left it"
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$oracle" || { printf '%s\n' "$oracle" | tail -10; fail "stock's plan for the same tamper is not exactly one change: $(_ref_plan_line "$oracle")"; }
  grep -qE '# kubernetes_config_map_v1\.app will be updated in-place' <<< "$oracle" || fail "stock's plan for the same tamper does not name kubernetes_config_map_v1.app"

  ref_kubectl patch configmap app-config -n "$REF_NS" --type merge -p '{"data":{"greeting":"tampered"}}' >/dev/null || fail "could not tamper app-config with kubectl"
  if [ "${BREAK:-}" = "1" ]; then
    ref_kubectl patch configmap shard-0 -n "$REF_NS" --type merge -p '{"data":{"shard":"tampered"}}' >/dev/null || fail "BREAK: could not tamper shard-0"
  fi
  plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$plan" | tail -30; fail "the plan after the tamper failed"; }
  if [ "${BREAK:-}" = "1" ]; then
    grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$plan" \
      && fail "BREAK=1: two objects were tampered but the plan still proposes exactly one change - the single-object assertion is not load-bearing"
    ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 || fail "BREAK: could not reconverge"
    gauntlet_stage drift_reconverge pass "BREAK=1 control: with two objects tampered the single-object assertion correctly fails to hold ($(_ref_plan_line "$plan")); reconverged afterwards"
    return 0
  fi
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$plan" || { printf '%s\n' "$plan" | tail -30; fail "the plan after one tamper does not propose exactly one change: $(_ref_plan_line "$plan")"; }
  [ "$(_ref_addrs "$plan" 'updated in-place')" = "kubernetes_config_map_v1.app" ] \
    || { printf '%s\n' "$plan" | grep -E '# .* will be'; fail "the one change is not kubernetes_config_map_v1.app, the object tampered"; }
  reconv="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$reconv" | tail -30; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$reconv" || fail "the reconverging apply did not change exactly one object"
  greeting="$(ref_kubectl get configmap app-config -n "$REF_NS" -o jsonpath='{.data.greeting}')"
  [ "$greeting" = "hello from reference-eks" ] || fail "app-config's greeting reads '$greeting' after reconverging"
  gauntlet_stage drift_reconverge pass "one ConfigMap tampered with kubectl patch, out of band on the cluster leg; choudoufu, with its kubernetes provider configured from aws_eks_cluster.this, proposed exactly kubernetes_config_map_v1.app (0 add, 1 change, 0 destroy) - stock's own plan for the same tamper on a copy of cold_deploy's state, before any marker existed, is the same single change; apply changed 1 and the value reads back as configured, with kubectl. BREAK=1 tampers a second object and the single-object assertion correctly fails"
}

# ══════════════════════════════════════════════════════════════════════
# plan_approval: plan -out, the world moves, apply of the file refuses
# ══════════════════════════════════════════════════════════════════════
reference_eks_stage_plan_approval() {
  gauntlet_begin_stage plan_approval
  log "=== plan_approval: a saved plan, an out-of-band label, a refusal; the same file applies once the world is back ==="
  local oracle p_plan p_apply p_rc p_apply2 reviewed
  _ref_oracle reviewed; oracle="$REF_ORACLE_OUT"
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$oracle" && grep -qE '# kubernetes_config_map_v1\.app will be updated in-place' <<< "$oracle" \
    || { printf '%s\n' "$oracle" | tail -10; fail "stock's plan for the reviewed change is not one update of kubernetes_config_map_v1.app: $(_ref_plan_line "$oracle")"; }
  ref_root "$ADOPTED" "$ESTATE" reviewed || fail "could not write the reviewed configuration"
  rm -f "$ADOPTED/approved.tfplan"
  p_plan="$(ref_tofu "$ADOPTED" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$p_plan" | tail -30; fail "plan -out failed"; }
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$p_plan" || { printf '%s\n' "$p_plan" | tail -10; fail "the saved plan is not exactly one change"; }
  [ -s "$ADOPTED/approved.tfplan" ] || fail "plan -out wrote no file"
  ref_kubectl label configmap shard-0 -n "$REF_NS" stray=yes >/dev/null || fail "could not move the world (a label on shard-0)"
  p_apply="$(ref_tofu "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; p_rc=$?
  if [ "${BREAK_APPROVAL:-}" = "1" ]; then
    [ "$p_rc" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
    ref_kubectl label configmap shard-0 -n "$REF_NS" stray- >/dev/null
    ref_tofu "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
    rm -f "$ADOPTED/approved.tfplan"
    gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $p_rc (refused), so the stage's own Break line correctly fails; applied once the world was put back"
    return 0
  fi
  [ "$p_rc" -eq 3 ] || { printf '%s\n' "$p_apply" | tail -30; fail "apply of the saved plan after the world moved exited $p_rc, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$p_apply" || { printf '%s\n' "$p_apply" | tail -30; fail "the refusal does not carry its documented sentence"; }
  grep -q "Apply complete!" <<< "$p_apply" && fail "the apply ran anyway after refusing"
  reviewed="$(ref_kubectl get configmap app-config -n "$REF_NS" -o jsonpath='{.data.reviewed}')"
  [ -z "$reviewed" ] || fail "app-config gained reviewed=$reviewed despite the refusal"
  ref_kubectl label configmap shard-0 -n "$REF_NS" stray- >/dev/null || fail "could not put the world back"
  p_apply2="$(ref_tofu "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$p_apply2" | tail -30; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$p_apply2" || fail "the saved plan's apply did not change exactly one object"
  [ "$(ref_kubectl get configmap app-config -n "$REF_NS" -o jsonpath='{.data.reviewed}')" = "yes" ] || fail "app-config does not read reviewed=yes after the saved plan applied"
  rm -f "$ADOPTED/approved.tfplan"
  gauntlet_stage plan_approval pass "plan -out wrote one update (app-config gains reviewed=yes), the change stock's own plan proposes for the same edit on a copy of cold_deploy's state; the world then moved out of band (a stray label on shard-0, kubectl, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied (kubectl reads no reviewed key); with the label removed the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back. The saved plan carries both legs and its apply configured the kubernetes provider from the same cluster the plan read. BREAK_APPROVAL=1 expects success after the move and correctly fails"
}

# ══════════════════════════════════════════════════════════════════════
# day2_rename: kubernetes_service_account_v1.app -> .team, a moved block
# ══════════════════════════════════════════════════════════════════════
reference_eks_stage_day2_rename() {
  gauntlet_begin_stage day2_rename
  log "=== day2_rename: kubernetes_service_account_v1.app becomes .team through a moved block ==="
  local oracle r_plan r_apply before
  _ref_oracle rename; oracle="$REF_ORACLE_OUT"
  _ref_noop "$oracle" || { printf '%s\n' "$oracle" | tail -10; fail "stock's plan for the moved block, on a copy of cold_deploy's state, is not zero churn: $(_ref_plan_line "$oracle")"; }
  grep -qE '# kubernetes_service_account_v1\.app has moved to kubernetes_service_account_v1\.team' <<< "$oracle" \
    || { printf '%s\n' "$oracle" | tail -10; fail "stock's plan does not report the ServiceAccount's move"; }
  before="$(ref_labelled)" || fail "could not count the cluster leg before the rename"
  if [ "${BREAK:-}" = "1" ]; then
    # The bare-rename Break line cannot fire on this leg: the block name is
    # not part of a Kubernetes object's identity, so a rename without a
    # moved block plans the same annotation rewrite (#1639). The control
    # that can fire renames the object itself.
    ref_root "$ADOPTED" "$ESTATE" reviewed sa_renamed || fail "BREAK: could not write the renamed-object configuration"
    r_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$r_plan" | tail -30; fail "BREAK: the plan after renaming the object failed"; }
    grep -q "1 to add" <<< "$r_plan" && grep -q "1 to destroy" <<< "$r_plan" \
      || { printf '%s\n' "$r_plan" | tail -30; fail "BREAK=1: renaming the ServiceAccount's own name did not plan a destroy and a create - the marker-rewritten-in-place assertion is not load-bearing: $(_ref_plan_line "$r_plan")"; }
    ref_root "$ADOPTED" "$ESTATE" reviewed team moved || fail "BREAK: could not write the moved-block configuration"
    ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 || fail "BREAK: the moved-block apply failed"
    gauntlet_stage day2_rename pass "BREAK=1 control: renaming the ServiceAccount's own metadata.name plans a replace ($(_ref_plan_line "$r_plan")), so the marker-rewritten-in-place assertion correctly fails to hold; the moved block then applied"
    return 0
  fi
  ref_root "$ADOPTED" "$ESTATE" reviewed team moved || fail "could not write the moved-block configuration"
  r_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$r_plan" | tail -30; fail "the moved-block plan failed"; }
  grep -qE 'will be (created|destroyed)|must be replaced' <<< "$r_plan" \
    && { printf '%s\n' "$r_plan" | grep -E '# .+ (will|must) be'; fail "the moved-block rename proposes a create, a destroy or a replace - not the marker rewritten in place"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$r_plan" \
    || { printf '%s\n' "$r_plan" | tail -30; fail "the moved-block plan is not exactly one in-place change (the address annotation rewrite): $(_ref_plan_line "$r_plan")"; }
  [ "$(_ref_addrs "$r_plan" 'updated in-place')" = "kubernetes_service_account_v1.team" ] \
    || { printf '%s\n' "$r_plan" | grep -E '# .* will be'; fail "the one in-place change is not on kubernetes_service_account_v1.team"; }
  grep -qE '~ +"choudoufu\.intentius\.io/tofu-address" = ".*" -> ".*"' <<< "$r_plan" \
    || { printf '%s\n' "$r_plan"; fail "the moved-block plan does not show the tofu-address annotation being rewritten"; }
  r_apply="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$r_apply" | tail -30; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$r_apply" || fail "the moved-block apply was not exactly one in-place change"
  _ref_exists serviceaccount app || fail "the ServiceAccount is gone after the rename"
  [ "$(ref_kubectl get serviceaccount app -n "$REF_NS" -o jsonpath='{.metadata.annotations.choudoufu\.intentius\.io/tofu-address}')" = "kubernetes_service_account_v1.team" ] \
    || fail "the ServiceAccount's address annotation does not read kubernetes_service_account_v1.team after the rename"
  [ "$(ref_labelled)" = "$before" ] || fail "$(ref_labelled) labelled cluster-leg objects after the rename, $before before"
  # The move is in both states now; the moved block leaves, as the kind
  # estates' does, and the plan must stay empty without it.
  ref_root "$ADOPTED" "$ESTATE" reviewed team || fail "could not write the post-rename configuration"
  r_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$r_plan" | tail -30; fail "the plan without the moved block failed"; }
  _ref_noop "$r_plan" || { printf '%s\n' "$r_plan" | grep -E '# .+ (will|must) be'; fail "with the moved block gone the plan is not empty: $(_ref_plan_line "$r_plan")"; }
  gauntlet_stage day2_rename pass "moved block: kubernetes_service_account_v1.app -> .team (the Deployment's and the pod identity association's references following it), no add and no destroy, one in-place change confined to the address annotation rewrite (0 add, 1 change, 0 destroy) - the marker rewritten in place; the live object untouched and still labelled, its annotation reading the new address, read with kubectl; stock's plan for the same moved block on a copy of cold_deploy's state is zero churn, since stock never writes this annotation; with the moved block then dropped the plan stays empty. The moved-block half on the cluster leg only; live-mv is not exercised by this stage. BREAK=1 renames the object's own metadata.name instead, a genuine identity change, and plans a destroy and a create"
}

# ══════════════════════════════════════════════════════════════════════
# day2_remove: kubernetes_config_map_v1.app's block leaves the configuration
# ══════════════════════════════════════════════════════════════════════
reference_eks_stage_day2_remove() {
  gauntlet_begin_stage day2_remove
  log "=== day2_remove: kubernetes_config_map_v1.app's block leaves the configuration ==="
  local oracle k_plan d_plan d_addr d_apply d_replan before
  _ref_oracle remove; oracle="$REF_ORACLE_OUT"
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$oracle" || { printf '%s\n' "$oracle" | tail -10; fail "stock's plan for the removal is not exactly one destroy: $(_ref_plan_line "$oracle")"; }
  [ "$(_ref_addrs "$oracle" destroyed)" = "kubernetes_config_map_v1.app" ] || fail "stock's one destroy is not kubernetes_config_map_v1.app"
  before="$(ref_labelled)" || fail "could not count the cluster leg before the removal"
  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    k_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "BREAK_REMOVE: the plan with the block kept failed"
    grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$k_plan" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed - the destroy below would not be the block removal's doing"
    ref_root "$ADOPTED" "$ESTATE" reviewed team drop_cm || fail "BREAK_REMOVE: could not write the removal"
    ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 || fail "BREAK_REMOVE: the removal apply failed afterwards"
    gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the block kept, no destroy is proposed ($(_ref_plan_line "$k_plan")); the real check is skipped"
    return 0
  fi
  ref_root "$ADOPTED" "$ESTATE" reviewed team drop_cm || fail "could not write the removal"
  grep -q 'kubernetes_config_map_v1" "app"' "$ADOPTED/main.tf" && fail "kubernetes_config_map_v1.app's block is still in the adopted root"
  d_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$d_plan" | tail -30; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$d_plan" || { printf '%s\n' "$d_plan" | tail -30; fail "the remove plan is not exactly one destroy: $(_ref_plan_line "$d_plan")"; }
  d_addr="$(_ref_addrs "$d_plan" destroyed)"
  # With no address bearing on the object's identity, the sweep finds it by
  # its label and plans it at its orphan address; kubectl below says it is
  # the object stock destroys.
  grep -qE "^kubernetes_config_map(_v1)?\\.orphan_${REF_NS}_app-config\$" <<< "$d_addr" \
    || { printf '%s\n' "$d_plan" | grep -E 'destroyed|^Plan:'; fail "the one destroy is '${d_addr:-unnamed}', not app-config at the orphan address the sweep plans a label-found object at"; }
  grep -q "Owned and undeclared: 1 live resource will be destroyed" <<< "$d_plan" || fail "the plan does not say the destroy is an owned, undeclared object"
  d_apply="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$d_apply" | tail -30; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$d_apply" || fail "the remove apply did not destroy exactly one object"
  _ref_exists configmap app-config && fail "app-config still exists after the remove apply"
  d_replan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the remove failed"
  _ref_noop "$d_replan" || { printf '%s\n' "$d_replan" | tail -30; fail "the replan after the remove is not empty"; }
  [ "$(ref_labelled)" = "$((before - 1))" ] || fail "$(ref_labelled) labelled cluster-leg objects after the remove, want $((before - 1))"
  gauntlet_stage day2_remove pass "deleting kubernetes_config_map_v1.app's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at the sweep's orphan address $d_addr (\"Owned and undeclared: 1 live resource will be destroyed\"), found by its tofu-estate label - stock's plan for the same removal on a copy of cold_deploy's state is the same single destroy, of kubernetes_config_map_v1.app; applied cleanly, app-config gone from the cluster (kubectl), the next plan empty and the labelled count down by exactly one. The cluster leg's removal only; the AWS leg's untaggable children (policy attachments, route-table associations) are not removed by this stage. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
}

# ══════════════════════════════════════════════════════════════════════
# day2_count: kubernetes_config_map_v1.shard 2 -> 1 -> 2
# ══════════════════════════════════════════════════════════════════════
reference_eks_stage_day2_count() {
  gauntlet_begin_stage day2_count
  log "=== day2_count: kubernetes_config_map_v1.shard scales 2 -> 1 -> 2 ==="
  local down up c_plan c_addr c_apply u_plan u_apply u_replan before
  _ref_oracle count_down; down="$REF_ORACLE_OUT"
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$down" && [ "$(_ref_addrs "$down" destroyed)" = "kubernetes_config_map_v1.shard[1]" ] \
    || { printf '%s\n' "$down" | tail -10; fail "stock's scale-down plan is not exactly one destroy of shard[1]: $(_ref_plan_line "$down")"; }
  _ref_oracle count_up; up="$REF_ORACLE_OUT"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$up" && [ "$(_ref_addrs "$up" created)" = "kubernetes_config_map_v1.shard[1]" ] \
    || { printf '%s\n' "$up" | tail -10; fail "stock's scale-up plan is not exactly one create of shard[1]: $(_ref_plan_line "$up")"; }
  before="$(ref_labelled)" || fail "could not count the cluster leg before the count change"

  ref_root "$ADOPTED" "$ESTATE" reviewed team drop_cm shards=1 || fail "could not write shards=1"
  c_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$c_plan" | tail -30; fail "the scale-down plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$c_plan" || { printf '%s\n' "$c_plan" | tail -30; fail "the scale-down plan is not exactly one destroy: $(_ref_plan_line "$c_plan")"; }
  c_addr="$(_ref_addrs "$c_plan" destroyed)"
  grep -qE "^kubernetes_config_map(_v1)?\\.orphan_${REF_NS}_shard-1\$" <<< "$c_addr" \
    || { printf '%s\n' "$c_plan" | grep -E 'destroyed|^Plan:'; fail "the scale-down destroys '${c_addr:-nothing named}', not shard-1 at its orphan address"; }
  c_apply="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$c_apply" | tail -30; fail "the scale-down apply failed"; }
  grep -qF "0 added, 0 changed, 1 destroyed" <<< "$c_apply" || fail "the scale-down apply did not destroy exactly one object"
  if [ "${BREAK_COUNT:-}" = "1" ]; then
    _ref_exists configmap shard-0 || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold, so the check is not load-bearing"
    ref_root "$ADOPTED" "$ESTATE" reviewed team drop_cm || fail "BREAK_COUNT: could not write shards=2"
    ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 || fail "BREAK_COUNT: the scale-up apply failed"
    gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (shard-0) was destroyed correctly fails to hold; the real check is skipped"
    return 0
  fi
  _ref_exists configmap shard-0 || fail "shard-0 was destroyed on the scale-down"
  _ref_exists configmap shard-1 && fail "shard-1 still exists after the scale-down"

  ref_root "$ADOPTED" "$ESTATE" reviewed team drop_cm || fail "could not write shards=2"
  u_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$u_plan" | tail -30; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$u_plan" || { printf '%s\n' "$u_plan" | tail -30; fail "the scale-up plan is not exactly one add: $(_ref_plan_line "$u_plan")"; }
  [ "$(_ref_addrs "$u_plan" created)" = "kubernetes_config_map_v1.shard[1]" ] || fail "the scale-up does not create kubernetes_config_map_v1.shard[1]"
  u_apply="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$u_apply" | tail -30; fail "the scale-up apply failed"; }
  grep -qF "1 added, 0 changed, 0 destroyed" <<< "$u_apply" || fail "the scale-up apply did not create exactly one object"
  _ref_exists configmap shard-0 && _ref_exists configmap shard-1 || fail "both shards do not exist after the scale-up"
  u_replan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up failed"
  _ref_noop "$u_replan" || { printf '%s\n' "$u_replan" | tail -30; fail "the replan after the scale-up is not empty"; }
  [ "$(ref_labelled)" = "$before" ] || fail "$(ref_labelled) labelled cluster-leg objects after the count cycle, $before before"
  gauntlet_stage day2_count pass "scaling kubernetes_config_map_v1.shard from 2 to 1 destroyed exactly shard-1, planned at the sweep's orphan address $c_addr since the label carries no index (shard-0 untouched, both read with kubectl); back to 2 created exactly kubernetes_config_map_v1.shard[1] under the same name; the next plan is empty and the labelled count is back to $before. Stock's plans for the same two changes, on copies of cold_deploy's state (the second with shard[1] dropped from the state copy, the position stock's own scale-down leaves), are one destroy of shard[1] and one create of shard[1]. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
}

# _ref_oracle_root <dir>: a fresh stock root of its own (reference_eks_oracle_root),
# initialised, its namespace applied. Used by day2_replace and day2_crash.
_ref_oracle_root() {
  local dir="$1" ns="$2" out
  rm -rf "$dir"; mkdir -p "$dir"
  reference_eks_oracle_root "$(ref_aws_provider)" "$CLUSTER" "$REGION" "$ns" > "$dir/main.tf" || fail "could not write the oracle root"
  gauntlet_locked_init ref_stock "$dir" init -input=false -no-color > "$dir/init.out" 2>&1 || { tail -20 "$dir/init.out"; fail "stock init of the oracle root failed"; }
  out="$(ref_stock "$dir" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$out" | tail -20; fail "stock could not create the oracle namespace $ns"; }
}
_ref_oracle_destroy() {
  local out
  out="$(ref_stock "$1" destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$out" | tail -20; fail "stock could not destroy the oracle root $1"; }
}

# ══════════════════════════════════════════════════════════════════════
# day2_replace: a content-hashed ConfigMap renamed under create_before_destroy
# ══════════════════════════════════════════════════════════════════════
#
# The kind lane's shape (#1541, gauntlet_kind_day2_replace), on _v1 and with
# stock's oracle in a root of its own on the same cluster, in its own
# namespace, applied for real: the apply ORDER is the oracle here and only
# an apply has one.
reference_eks_stage_day2_replace() {
  gauntlet_begin_stage day2_replace
  log "=== day2_replace: cfg-a -> cfg-b under create_before_destroy ==="
  local block="kubernetes_config_map_v1.hashed" oracle="$WORK/oracle-replace" ons="refeks-oracle-replace"
  local o_plan o_apply r_apply r_plan r_replan r_rm ann b_plan created destroying
  _ref_replace_order() { # <log> <where>
    created="$(grep -nE "${block//./\\.}: Creation complete" <<< "$1" | head -1 | cut -d: -f1)"
    destroying="$(grep -nE "${block//./\\.} \\(deposed object [^)]*\\): Destroying" <<< "$1" | head -1 | cut -d: -f1)"
    [ -n "$created" ] && [ -n "$destroying" ] || { printf '%s\n' "$1" | tail -20; fail "$2: the apply log has no create complete (${created:-none}) or no deposed destroy (${destroying:-none}) for $block"; }
    [ "$created" -lt "$destroying" ] || { printf '%s\n' "$1" | tail -20; fail "$2: the old object's destroy started (log line $destroying) before the new object's create completed (line $created)"; }
  }
  _ref_is_replace() { # <plan> <where>
    grep -qF "Plan: 1 to add, 0 to change, 1 to destroy." <<< "$1" || { printf '%s\n' "$1" | tail -20; fail "$2: the rename is not one add and one destroy: $(_ref_plan_line "$1")"; }
    grep -qE "# ${block//./\\.} must be replaced" <<< "$1" || fail "$2: $block is not planned as a replace"
    grep -qF "+/- create replacement and then destroy" <<< "$1" || fail "$2: the replace is not create-first"
    grep -q "orphan_" <<< "$1" && fail "$2: the old object is planned as an orphan beside a create (#1541) rather than as the replace's deposed half"
    return 0
  }

  # The oracle: stock, its own root, its own namespace, for real.
  _ref_oracle_root "$oracle" "$ons"
  reference_eks_replace_block "$ons" a >> "$oracle/main.tf"
  o_apply="$(ref_stock "$oracle" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$o_apply" | tail -20; fail "stock's apply of cfg-a in the oracle root failed"; }
  reference_eks_oracle_root "$(ref_aws_provider)" "$CLUSTER" "$REGION" "$ons" > "$oracle/main.tf"
  reference_eks_replace_block "$ons" b >> "$oracle/main.tf"
  o_plan="$(ref_stock "$oracle" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$o_plan" | tail -10; fail "stock's rename plan failed in the oracle root"; }
  _ref_is_replace "$o_plan" "stock (the oracle)"
  o_apply="$(ref_stock "$oracle" apply -auto-approve -input=false -no-color -parallelism=1 2>&1)" || { printf '%s\n' "$o_apply" | tail -10; fail "stock's rename apply failed in the oracle root"; }
  _ref_replace_order "$o_apply" "stock (the oracle)"
  _ref_oracle_destroy "$oracle"

  reference_eks_replace_block "$REF_NS" a > "$ADOPTED/day2_replace.tf"
  r_apply="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$r_apply" | tail -20; fail "the apply of cfg-a failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed." <<< "$r_apply" || fail "the apply of cfg-a did not add exactly one object"
  ann="$(ref_kubectl get configmap cfg-a -n "$REF_NS" -o jsonpath='{.metadata.annotations.choudoufu\.intentius\.io/tofu-address}' 2>&1)"
  [ "$ann" = "$block" ] || fail "cfg-a carries the address annotation '$ann', want $block (#1639): nothing would bind it on the rename"
  reference_eks_replace_block "$REF_NS" b > "$ADOPTED/day2_replace.tf"
  r_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$r_plan" | tail -20; fail "the rename plan failed"; }
  _ref_is_replace "$r_plan" "choudoufu"
  r_apply="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color -parallelism=1 2>&1)" || { printf '%s\n' "$r_apply" | tail -20; fail "the rename apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 1 destroyed." <<< "$r_apply" || fail "the rename apply did not add one object and destroy one"
  _ref_replace_order "$r_apply" "choudoufu"
  _ref_exists configmap cfg-b || fail "cfg-b does not exist after the replace"
  _ref_exists configmap cfg-a && fail "cfg-a still exists after the replace"
  if [ "${BREAK_REPLACE:-}" = "1" ]; then
    ref_kubectl create configmap cfg-a -n "$REF_NS" --from-literal=v=a >/dev/null || fail "BREAK_REPLACE: could not recreate cfg-a"
    ref_kubectl label configmap cfg-a -n "$REF_NS" "tofu-estate=$ESTATE" >/dev/null || fail "BREAK_REPLACE: could not label cfg-a"
    ref_kubectl annotate configmap cfg-a -n "$REF_NS" "choudoufu.intentius.io/tofu-address=$block" >/dev/null || fail "BREAK_REPLACE: could not annotate cfg-a"
    b_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$b_plan" | tail -20; fail "BREAK_REPLACE: the plan over the recreated cfg-a failed"; }
    _ref_noop "$b_plan" && fail "BREAK_REPLACE=1: the old object is back, carrying the block's address, and the plan proposes nothing - the empty-plan assertion is not load-bearing"
  else
    r_replan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$r_replan" | tail -20; fail "the replan after the replace failed"; }
    _ref_noop "$r_replan" || { printf '%s\n' "$r_replan" | tail -20; fail "the replan after the replace is not empty"; }
  fi
  rm -f "$ADOPTED/day2_replace.tf"
  r_rm="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$r_rm" | tail -20; fail "removing the replace block failed"; }
  _ref_exists configmap cfg-b && fail "cfg-b still exists after its block was removed"
  _ref_exists configmap cfg-a && fail "cfg-a still exists after the replace block was removed"
  if [ "${BREAK_REPLACE:-}" = "1" ]; then
    gauntlet_stage day2_replace pass "BREAK_REPLACE=1 control: after the create_before_destroy rename cfg-a -> cfg-b, cfg-a was recreated by hand carrying tofu-estate=$ESTATE and the annotation naming $block, and the next plan proposes work ($(_ref_plan_line "$b_plan")) rather than nothing, so the empty-plan assertion is load-bearing; the real replan check is skipped"
  else
    gauntlet_stage day2_replace pass "a create_before_destroy ConfigMap whose content-hashed name changes (cfg-a -> cfg-b) plans as stock's replace: '$block must be replaced', +/- create replacement and then destroy, 1 add and 1 destroy, with no orphan destroy beside it, because cfg-a carries the block's address annotation (#1640). At -parallelism=1 the apply log shows cfg-b's creation complete (line $created) before cfg-a's deposed destroy starts (line $destroying), the order stock's own apply of the same rename shows in a root of its own on the same cluster; kubectl confirms cfg-b alone remains and the next plan is empty. The block is removed afterwards. BREAK_REPLACE=1 recreates cfg-a carrying the block's annotation and the next plan correctly proposes work"
  fi
}

# ══════════════════════════════════════════════════════════════════════
# day2_crash: SIGTERM between two creates, and inside a cbd rename
# ══════════════════════════════════════════════════════════════════════
reference_eks_stage_day2_crash() {
  gauntlet_begin_stage day2_crash
  log "=== day2_crash: the engine's own SIGTERM between two creates, and inside a create_before_destroy rename ==="
  local oracle="$WORK/oracle-crash" ons="refeks-oracle-crash" o_plan o_rem x_plan x_out x_rc r_plan r_rc r_line
  local x_rec="" x_residue="" n_plan n_line b_plan r_apply r_replan rec_detail rename_rc

  # The rename window first, so the two-object window starts converged.
  CRASH_RENAME_DETAIL=""
  ref_crash_rename; rename_rc=$?
  case "$rename_rc" in
    0|3) ;;
    *) fail "the create_before_destroy rename window's leg returned $rename_rc without a verdict" ;;
  esac

  # The oracle: stock, its own root, walked into the same position by
  # applying the first object alone, then asked for the remainder.
  _ref_oracle_root "$oracle" "$ons"
  reference_eks_crash_pair "$ons" first >> "$oracle/main.tf"
  o_plan="$(ref_stock "$oracle" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$o_plan" | tail -10; fail "stock's crash-first apply failed in the oracle root"; }
  reference_eks_crash_pair "$ons" second >> "$oracle/main.tf"
  o_rem="$(ref_stock "$oracle" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$o_rem" | tail -10; fail "stock's remainder plan failed in the oracle root"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$o_rem" && [ "$(_ref_addrs "$o_rem" created)" = "kubernetes_config_map_v1.crash_second" ] \
    || { printf '%s\n' "$o_rem" | tail -10; fail "stock's remainder plan is not exactly one add of crash_second - the oracle for this stage is not what it should be"; }
  _ref_oracle_destroy "$oracle"

  reference_eks_crash_pair "$REF_NS" both > "$ADOPTED/day2_crash.tf"
  x_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$x_plan" | tail -30; fail "the pre-crash plan failed"; }
  grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$x_plan" || { printf '%s\n' "$x_plan" | tail -30; fail "the pre-crash plan is not exactly two adds - there is no two-object apply to interrupt"; }
  # A non-zero exit is the normal outcome: the process was killed.
  x_out="$(export TOFU_E2E_APPLY_RESOURCE_INTERRUPT="kubernetes_secret_v1.crash_first"; ref_tofu_crash "$ADOPTED" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; x_rc=$?
  printf '%s\n' "$x_out" > "$WORK/day2_crash.log"
  [ "$x_rc" -ne 0 ] || { printf '%s\n' "$x_out" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted"; }
  _ref_exists secret crash-first || { printf '%s\n' "$x_out" | tail -20; fail "crash-first does not exist after the interrupted apply - the kill landed before the create committed"; }
  _ref_exists configmap crash-second && { printf '%s\n' "$x_out" | tail -20; fail "crash-second exists after the interrupted apply - the kill landed after both creates"; }
  ref_kubectl get secret -n "$REF_NS" -l "tofu-estate=$ESTATE" -o name 2>/dev/null | grep -qx "secret/crash-first" \
    || fail "crash-first was created by the interrupted apply but does not come back under tofu-estate=$ESTATE"

  # What the interrupted apply wrote, read off a local store by the
  # envelope's own address (#1235); a store in the cluster is not read here.
  if [ -n "${REF_RECORDS:-}" ]; then
    x_rec="$(gauntlet_record_file "$REF_RECORDS" "kubernetes_secret_v1.crash_first")" \
      || fail "the interrupted apply created crash-first but wrote no record for kubernetes_secret_v1.crash_first"
    x_residue="$(gauntlet_record_residue "$x_rec" | tr '\n' ' ' | sed 's/ $//')"
    [ "$x_residue" = "wait_for_service_account_token" ] || fail "the record for kubernetes_secret_v1.crash_first carries residue [${x_residue:-none}], want wait_for_service_account_token"
  fi

  if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
    ref_kubectl label secret crash-first -n "$REF_NS" tofu-estate- >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not strip the label off crash-first"
  fi
  r_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)"; r_rc=$?
  r_line="$(_ref_plan_line "$r_plan")"
  _ref_recovered() {
    [ "$r_rc" -eq 0 ] || return 1
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$r_plan" || return 1
    [ "$(_ref_addrs "$r_plan" created)" = "kubernetes_config_map_v1.crash_second" ] || return 1
    grep -E '^[[:space:]]*# .* (will|must) be' <<< "$r_plan" | grep -q 'crash_first\|crash-first' && return 1
    return 0
  }
  if [ "${BREAK_CRASH:-}" = "1" ]; then
    _ref_noop "$r_plan" && fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty, so this stage's own check is not load-bearing"
    ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 || fail "BREAK_CRASH: the recovery apply failed afterwards"
    rm -f "$ADOPTED/day2_crash.tf"
    ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 || fail "BREAK_CRASH: removing the crash pair failed afterwards"
    gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($r_line), so the stage's own Break line - interrupt and then assert nothing is proposed - correctly fails to hold; the real check is skipped. $CRASH_RENAME_DETAIL"
    return 0
  fi
  if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
    _ref_recovered && fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash-first carrying no tofu-estate label - it is not measuring whether the crashed-out object was bound"
    ref_kubectl delete secret crash-first -n "$REF_NS" >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: could not delete the unlabelled crash-first"
    rm -f "$ADOPTED/day2_crash.tf"
    ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 || fail "BREAK_CRASH_UNBOUND: the cleanup apply failed"
    gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the tofu-estate label stripped off the object the interrupted apply created, the recovery check correctly fails to hold ($r_line); the real check is skipped. $CRASH_RENAME_DETAIL"
    return 0
  fi
  _ref_recovered || { printf '%s\n' "$r_plan" | grep -E '^Plan:|^No changes|will be|must be' | head -20; fail "the plan after a real interrupt between the create of crash_first and the create of crash_second is not exactly the remainder: ${r_line:-no plan line} (exit $r_rc); stock, walked into the same position in a root of its own, plans exactly one add (crash_second)"; }

  rec_detail="the record store is in the cluster on this target, so the record the interrupted apply wrote is not read here (the emulator crossing reads it)"
  if [ -n "$x_rec" ]; then
    # The record's contribution, measured by taking it away (#1235).
    mv "$x_rec" "$WORK/crash_first.record" || fail "could not move the crash record aside"
    n_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$n_plan" | tail -20; fail "the replan with the crash record taken out of the store failed"; }
    n_line="$(_ref_plan_line "$n_plan")"
    grep -qF "Plan: 1 to add, 1 to change, 0 to destroy." <<< "$n_plan" \
      || { printf '%s\n' "$n_plan" | grep -E '^Plan:|will be|^ +[+~-] ' | head -20; fail "with the crash record taken out of the store the recovery plan is $n_line, not the remainder plus one in-place update - the record contributed nothing this stage can read"; }
    grep -qE '^[[:space:]]+\+ wait_for_service_account_token +=' <<< "$n_plan" \
      || fail "the extra in-place update the missing record produces does not propose wait_for_service_account_token back"
    mv "$WORK/crash_first.record" "$x_rec" || fail "could not put the crash record back"
    b_plan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)"
    grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$b_plan" || fail "putting the record back does not restore the exact-remainder plan"
    rec_detail="the interrupted apply wrote one record for kubernetes_secret_v1.crash_first carrying residue $x_residue, and taking that one file out of the store turns the recovery plan from $r_line into $n_line, proposing wait_for_service_account_token back; putting it back restores the exact remainder"
  fi

  r_apply="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$r_apply" | tail -20; fail "the recovery apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$r_apply" || fail "the recovery apply did not add exactly the one remaining object"
  _ref_exists configmap crash-second || fail "crash-second does not exist after the recovery apply"
  _ref_exists secret crash-first || fail "crash-first is gone after the recovery apply - the recovery replaced the object the crash created instead of binding it"
  r_replan="$(ref_tofu "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$r_replan" | tail -30; fail "the replan after the recovery failed"; }
  _ref_noop "$r_replan" || { printf '%s\n' "$r_replan" | tail -30; fail "the replan after the recovery is not empty"; }
  rm -f "$ADOPTED/day2_crash.tf"
  r_apply="$(ref_tofu "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$r_apply" | tail -20; fail "removing the crash pair failed"; }
  grep -qF "0 added, 0 changed, 2 destroyed" <<< "$r_apply" || fail "removing the crash pair did not destroy exactly its two objects"
  gauntlet_stage day2_crash pass "an apply creating two cluster-leg objects was interrupted by a real SIGTERM (exit $x_rc), delivered by the engine itself inside the -parallelism=1 graph walker the instant kubernetes_secret_v1.crash_first's create committed; crash_second reads crash_first's name, so the walker cannot have reached it - kubectl confirms crash-first exists carrying tofu-estate=$ESTATE and crash-second does not. The next plan proposed exactly the remainder ($r_line, kubernetes_config_map_v1.crash_second) and nothing for crash-first, which it bound by its label - matching stock's own plan from the same position, reached by applying the first object alone in a root of its own on the same cluster; the recovery apply added exactly one object and the plan after it is empty. $rec_detail. The pair is removed afterwards. $CRASH_RENAME_DETAIL BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 strips the label off crash-first and the same recovery check correctly fails"
}

# ══════════════════════════════════════════════════════════════════════
# strict: every toggle on, against a scratch estate
# ══════════════════════════════════════════════════════════════════════
reference_eks_stage_strict() {
  gauntlet_begin_stage strict
  log "=== strict: every strict toggle on ==="
  local dir="$WORK/strict" on on_rc off off_rc
  rm -rf "$dir"; mkdir -p "$dir"
  reference_eks_strict_root "$ESTATE-strict" refuse > "$dir/main.tf"
  ref_tofu "$dir" init -input=false -no-color >"$dir/init.out" 2>&1 || { tail -20 "$dir/init.out"; fail "choudoufu init for the strict-stage scratch estate failed"; }
  on="$(ref_tofu "$dir" plan -input=false -no-color 2>&1)"; on_rc=$?
  if [ "${BREAK_STRICT:-}" = "1" ]; then
    reference_eks_strict_root "$ESTATE-strict" store > "$dir/main.tf"
    off="$(ref_tofu "$dir" plan -input=false -no-color 2>&1)"; off_rc=$?
    [ "$off_rc" -eq 0 ] || { printf '%s\n' "$off" | tail -30; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $off_rc"; }
    grep -q "^Error:" <<< "$off" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
    grep -qF 'random_password.db will be created' <<< "$off" || fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"
    gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create; the real check is skipped"
    return 0
  fi
  [ "$on_rc" -eq 1 ] || { printf '%s\n' "$on" | tail -30; fail "the every-toggle-on plan exited $on_rc, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$on")" -eq 1 ] || { printf '%s\n' "$on"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$on" || { printf '%s\n' "$on"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$on" || { printf '%s\n' "$on"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming kubernetes_config_map_v1) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. The scratch estate reaches neither leg. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
}

# ══════════════════════════════════════════════════════════════════════
# no_local_state: the record store and the state cache both gone
# ══════════════════════════════════════════════════════════════════════
#
# #1098's ruling: "local state" is BOTH local artifacts. The cache-serving
# plan is taken first and counted; then both are deleted and the plan with
# only the account left must find every declared object - nothing created,
# destroyed or replaced. An in-place update is not a failure: the record
# store is also the residue store, and deleting it deletes the applied
# values of config-only arguments the provider's Read never returns
# (kubernetes_deployment_v1's wait_for_rollout among them); each one is
# listed in the verdict. The call count is AWS SDK requests by the rule
# terralith-scale and live/costs/what-you-pay.md use; the kubernetes
# provider's own requests are not in that log line.
_ref_api_calls() {
  [ -f "$1" ] || return 1
  awk '
    function flush() { if (entry ~ /HTTP Request Sent/) total++; entry = "" }
    /^20[0-9][0-9]-[0-9][0-9]-[0-9][0-9]T/ { flush(); entry = $0; next }
    { entry = entry " " $0 }
    END { flush(); printf "%d\n", total + 0 }
  ' "$1"
}
reference_eks_stage_no_local_state() {
  gauntlet_begin_stage no_local_state
  log "=== no_local_state: delete the local record store and the state cache, then plan ==="
  local cache="$ADOPTED/.terraform/choudoufu-cache.tfstate" base base_rc base_calls plan rc calls igw ratio upd what
  base="$(export TF_LOG=DEBUG TF_LOG_PATH="$WORK/nls.base.log"; ref_tofu "$ADOPTED" plan -refresh=false -input=false -no-color 2>&1)"; base_rc=$?
  base_calls="$(_ref_api_calls "$WORK/nls.base.log" || true)"
  [ "$base_rc" -eq 0 ] && _ref_noop "$base" || { printf '%s\n' "$base" | tail -30; fail "the cache-serving plan (exit $base_rc) is not empty, so it is no baseline"; }
  [ -s "$cache" ] || fail "no state cache at $cache before the baseline plan, so deleting it below would delete nothing"
  [ -n "$base_calls" ] && [ "$base_calls" -gt 0 ] || fail "the cache-serving plan left no countable debug log"
  rm -f "$cache"
  what="the state cache"
  if [ -n "${REF_RECORDS:-}" ]; then
    rm -rf "$REF_RECORDS"
    [ ! -e "$REF_RECORDS" ] || fail "the local record store survived deletion"
    what="the local record store and the state cache"
  else
    what="the state cache (this target's records live in the cluster, record_store \"kubernetes\", so a fresh clone has them and there is no local record store to delete)"
  fi
  [ ! -e "$cache" ] || fail "the state cache survived deletion"
  if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
    igw="$(ref_aws ec2 describe-internet-gateways --filters "Name=tag:tofu-estate,Values=$ESTATE" --output json | jq -r '.InternetGateways[0].InternetGatewayId // empty')"
    [ -n "$igw" ] || fail "BREAK_NO_LOCAL_STATE: no internet gateway carries tofu-estate=$ESTATE"
    ref_aws ec2 delete-tags --resources "$igw" --tags Key=tofu-estate Key=tofu-address >/dev/null || fail "BREAK_NO_LOCAL_STATE: could not strip $igw's markers"
  fi
  plan="$(export TF_LOG=DEBUG TF_LOG_PATH="$WORK/nls.log"; ref_tofu "$ADOPTED" plan -refresh=false -input=false -no-color 2>&1)"; rc=$?
  calls="$(_ref_api_calls "$WORK/nls.log" || true)"
  _ref_nls_violation() {
    [ "$rc" -eq 0 ] || { echo "the plan with no local state exited $rc: $(grep -m1 '^Error' <<< "$plan")"; return; }
    grep -qE '# .+ will be created' <<< "$plan" && { echo "with no local state the plan proposes CREATING something that already exists: $(_ref_addrs "$plan" created | tr '\n' ' ')"; return; }
    grep -qE '# .+ will be destroyed' <<< "$plan" && { echo "with no local state the plan proposes destroying: $(_ref_addrs "$plan" destroyed | tr '\n' ' ')"; return; }
    grep -qE '# .+ must be replaced' <<< "$plan" && { echo "with no local state the plan proposes a replace"; return; }
  }
  local bad
  bad="$(_ref_nls_violation)"
  if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
    [ -n "$bad" ] || fail "BREAK_NO_LOCAL_STATE=1: with the internet gateway's markers stripped and no local state, the plan still found everything - this stage's check is not load-bearing"
    ref_aws ec2 create-tags --resources "$igw" --tags "Key=tofu-estate,Value=$ESTATE" Key=tofu-address,Value=aws_internet_gateway.main >/dev/null \
      || fail "BREAK_NO_LOCAL_STATE: could not put $igw's markers back"
    gauntlet_stage no_local_state pass "BREAK_NO_LOCAL_STATE=1 control: with the internet gateway's markers stripped and $what deleted, the check correctly failed ($bad); the markers were put back"
    return 0
  fi
  [ -z "$bad" ] || { grep -E '# .+ (will|must) be' <<< "$plan" | head -20; fail "$bad"; }
  [ -n "$calls" ] && [ "$calls" -gt 0 ] || fail "the plan with no local state left no countable debug log"
  upd="$(_ref_addrs "$plan" 'updated in-place' | tr '\n' ' ' | sed 's/ $//')"
  ratio="$(awk -v a="$calls" -v b="$base_calls" 'BEGIN { printf "%.2f", a / b }')"
  gauntlet_stage no_local_state pass "with $what deleted - a fresh clone with only the account left - the plan found every declared object on both legs: nothing created, destroyed or replaced; the AWS leg by its markers and the untaggable children from stamped parents, the cluster leg by its tofu-estate label through a kubernetes provider configured from the cluster this plan read live. In-place updates putting back residue the store held: ${upd:-none}. plan_calls_no_local_state=${calls} plan_calls_cache_serving=${base_calls} ratio=${ratio}x (both plan -refresh=false, AWS SDK requests counted from TF_LOG=DEBUG); stock in this position has no plan at all, only one import block per object. BREAK_NO_LOCAL_STATE=1 strips the internet gateway's markers and the check correctly fails"
}

# ══════════════════════════════════════════════════════════════════════
# day2_teardown: choudoufu apply -destroy, both legs
# ══════════════════════════════════════════════════════════════════════
#
# Last on the adopted estate: it destroys the cluster the cluster leg lives
# on, so nothing after it can read that leg. The cluster-leg objects must
# be destroyed before the cluster, the order the configuration's own graph
# gives; the apply log is the evidence. Afterwards the AWS leg's markers
# count zero and the cluster is gone.
reference_eks_stage_day2_teardown() {
  gauntlet_begin_stage day2_teardown
  log "=== day2_teardown: choudoufu apply -destroy on the adopted estate ==="
  local oracle want before_aws before_k8s out rc n last_k8s cluster_destroy left targets a
  _ref_oracle teardown; oracle="$REF_ORACLE_OUT"
  n="$(grep -oE 'Plan: 0 to add, 0 to change, [0-9]+ to destroy' <<< "$oracle" | grep -oE '[0-9]+ to destroy' | grep -oE '[0-9]+')"
  [ "$n" = "$((AWS_N + CLUSTER_N))" ] || { printf '%s\n' "$oracle" | tail -5; fail "stock's planned destroy of cold_deploy's state is ${n:-no} objects, not the ${AWS_N} + ${CLUSTER_N} it applied"; }
  before_aws="$(ref_aws_marked)" || fail "could not count the AWS leg before the teardown"
  before_k8s="$(ref_labelled)" || fail "could not count the cluster leg before the teardown"
  # day2_remove took one cluster-leg object out of the estate for good;
  # every other stage put back what it added.
  want=$((AWS_N + CLUSTER_N - 1))
  [ "$before_k8s" = "$((CLUSTER_N - 1))" ] || fail "$before_k8s labelled cluster-leg objects before the teardown, want $((CLUSTER_N - 1)) - an earlier stage left something behind"
  if [ "${BREAK_TEARDOWN:-}" = "1" ]; then
    # Leave one resource: target everything but the VPC (a -target takes its
    # dependencies' dependents with it, never the VPC itself).
    targets=()
    for a in aws_internet_gateway.main aws_route_table.public aws_route_table_association.a aws_route_table_association.b \
             aws_eks_node_group.default aws_eks_cluster.this aws_subnet.a aws_subnet.b; do
      targets+=("-target=$a")
    done
    out="$(ref_tofu "$ADOPTED" apply -destroy -auto-approve -input=false -no-color "${targets[@]}" 2>&1)"; rc=$?
  else
    out="$(ref_tofu "$ADOPTED" apply -destroy -auto-approve -input=false -no-color 2>&1)"; rc=$?
  fi
  printf '%s\n' "$out" > "$WORK/day2_teardown.log"
  [ "$rc" -eq 0 ] || { printf '%s\n' "$out" | tail -30; fail "choudoufu apply -destroy exited $rc"; }
  # The cluster leg goes first: the last kubernetes_* destroy completes
  # before aws_eks_cluster.this starts destroying.
  last_k8s="$(grep -nE '^kubernetes_[a-z0-9_]+\.[^:]+: Destruction complete' <<< "$out" | tail -1 | cut -d: -f1)"
  cluster_destroy="$(grep -nE '^aws_eks_cluster\.this: Destroying' <<< "$out" | head -1 | cut -d: -f1)"
  [ -n "$cluster_destroy" ] || fail "the teardown never destroyed aws_eks_cluster.this"
  if [ "${BREAK_TEARDOWN:-}" != "1" ]; then
    [ -n "$last_k8s" ] && [ "$last_k8s" -lt "$cluster_destroy" ] \
      || fail "the cluster leg was not destroyed before the cluster it lives on (last kubernetes_* destroy at log line ${last_k8s:-none}, the cluster's at $cluster_destroy)"
  fi
  if ref_aws eks describe-cluster --name "$CLUSTER" --output json >/dev/null 2>&1; then
    fail "cluster $CLUSTER still answers DescribeCluster after the teardown"
  fi
  left="$(ref_aws_marked)" || fail "could not count the AWS leg after the teardown"
  if [ "${BREAK_TEARDOWN:-}" = "1" ]; then
    [ "$left" = "0" ] && fail "BREAK_TEARDOWN=1: the VPC was left out of the destroy, yet the estate counts empty - the emptiness assertion is not load-bearing"
    gauntlet_stage day2_teardown pass "BREAK_TEARDOWN=1 control: with the VPC left out of the destroy, $left marked AWS object(s) remain and the emptiness assertion correctly fails"
    return 0
  fi
  [ "$left" = "0" ] || fail "$left AWS object(s) still carry tofu-estate=$ESTATE after the teardown"
  grep -qE "Resources: (0 added, 0 changed, )?${want} destroyed" <<< "$out" \
    || { grep -E 'Destroy complete|Apply complete' <<< "$out"; fail "the teardown did not destroy exactly the ${want} objects the estate held (${AWS_N} AWS, $((CLUSTER_N - 1)) cluster)"; }
  gauntlet_stage day2_teardown pass "choudoufu apply -destroy removed exactly the ${want} objects the estate held in one apply (${AWS_N} AWS, $((CLUSTER_N - 1)) on the cluster; stock's planned destroy of cold_deploy's state is ${n}, the difference being the config map day2_remove destroyed); the cluster leg's last destroy completed (log line $last_k8s) before aws_eks_cluster.this started destroying (line $cluster_destroy), so nothing on the cluster outlived it; afterwards nothing on the AWS leg carries the estate's markers ($before_aws before) and the cluster no longer answers DescribeCluster. BREAK_TEARDOWN=1 leaves the VPC out and the emptiness assertion correctly fails"
}

# reference_eks_inventory <aws-fn> <kubectl-fn> <prefix>: the structural
# inventory greenfield compares, one line per object, markers never part of
# it and every name with its run's prefix taken off, so two estates under
# two prefixes in one account compare by shape. The AWS leg by the AWS CLI,
# the cluster leg by kubectl.
reference_eks_inventory() {
  local awsf="$1" kcf="$2" prefix="$3" vpc
  # Whole responses through jq, never a reducing CLI query (#1042).
  vpc="$("$awsf" ec2 describe-vpcs --filters "Name=tag:Name,Values=${prefix}-vpc" --output json | jq -r '.Vpcs[0].VpcId // empty')" || return 1
  [ -n "$vpc" ] || return 1
  "$awsf" ec2 describe-vpcs --vpc-ids "$vpc" --output json | jq -r '.Vpcs[] | ["vpc", .CidrBlock] | @tsv'
  "$awsf" ec2 describe-subnets --filters "Name=vpc-id,Values=$vpc" --output json \
    | jq -r '.Subnets[] | ["subnet", .CidrBlock, (.AvailabilityZone[-1:]), (.MapPublicIpOnLaunch|tostring)] | @tsv'
  "$awsf" ec2 describe-internet-gateways --filters "Name=attachment.vpc-id,Values=$vpc" --output json \
    | jq -r '.InternetGateways[] | ["igw", .Attachments[0].State] | @tsv'
  "$awsf" ec2 describe-route-tables --filters "Name=vpc-id,Values=$vpc" "Name=tag:Name,Values=${prefix}-public" --output json \
    | jq -r '.RouteTables[] | ["rtb", (.Associations|length|tostring)] | @tsv'
  "$awsf" iam get-role --role-name "${prefix}-cluster" --output json | jq -r --arg p "${prefix}-" '["role", (.Role.RoleName|ltrimstr($p))] | @tsv'
  "$awsf" iam get-role --role-name "${prefix}-node" --output json | jq -r --arg p "${prefix}-" '["role", (.Role.RoleName|ltrimstr($p))] | @tsv'
  "$awsf" eks describe-cluster --name "${prefix}-eks" --output json \
    | jq -r --arg p "${prefix}-" '["cluster", (.cluster.name|ltrimstr($p)), (.cluster.resourcesVpcConfig.subnetIds|length|tostring)] | @tsv'
  "$awsf" eks describe-nodegroup --cluster-name "${prefix}-eks" --nodegroup-name "${prefix}-default" --output json \
    | jq -r --arg p "${prefix}-" '.nodegroup | ["nodegroup", (.nodegroupName|ltrimstr($p)), (.scalingConfig.desiredSize|tostring), (.scalingConfig.minSize|tostring), (.scalingConfig.maxSize|tostring)] | @tsv'
  "$kcf" get namespace app -o jsonpath='{"namespace\t"}{.metadata.name}{"\n"}'
  "$kcf" get serviceaccount app -n app -o jsonpath='{"serviceaccount\t"}{.metadata.name}{"\n"}'
  "$kcf" get configmap app-config -n app -o jsonpath='{"configmap\t"}{.metadata.name}{"\t"}{.data.greeting}{"\n"}'
  "$kcf" get configmap shard-0 -n app -o jsonpath='{"configmap\t"}{.metadata.name}{"\t"}{.data.shard}{"\n"}'
  "$kcf" get configmap shard-1 -n app -o jsonpath='{"configmap\t"}{.metadata.name}{"\t"}{.data.shard}{"\n"}'
  "$kcf" get deployment app -n app -o jsonpath='{"deployment\t"}{.metadata.name}{"\t"}{.spec.template.spec.serviceAccountName}{"\t"}{.spec.template.spec.containers[0].image}{"\n"}'
}
