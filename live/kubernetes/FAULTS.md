# Fault injection on Kubernetes

GitHub issue #1110 asked which faults a real cluster produces that the
kubernetes lane never injected, and whether each should be a gauntlet
stage (a fault between two steps of a run the lane already does) or a
smoke claim with a `BREAK` control (one object, one fault, one honest
plan). This note records the answer for all five and what is left.

| # | Fault | Shape | Status | What injects it |
|---|-------|-------|--------|-----------------|
| 1 | A finalizer holds a delete | claim | done, claim 25 (#1186) | `live/smoke/scenarios/k8s-a-held-delete-is-not-gone.sh` puts a finalizer on the object; `BREAK=1` takes it off before the destroy |
| 2 | An admission webhook rejects or mutates | claim | done, claim 26 (#1193) | `k8s-the-server-gets-the-last-word.sh`: a `ValidatingWebhookConfiguration` with no endpoint, and `MutatingAdmissionPolicy` objects for the mutations |
| 3 | A server-side apply conflict | claim | blocked on #1191 (deferred) | nothing; see below |
| 4a | A kill mid-apply | stage | done, `day2_crash` on kind (#1189) | the gauntlet's crash stage sends SIGTERM between two objects' creates |
| 4b | A kill mid-move | unit test | injector landed, fault is red today | `crashBetweenMarkerWrites` in `internal/live/mv/fault_move_crash_test.go` |
| 5 | The namespace is deleted under the estate | stage | not started | nothing yet; see below |

## 1 and 2: done

Both are claims, measured on kind and written up on the claims
themselves (`live/smoke/claims/`). The findings they turned up were filed
separately: #1184 (a held delete printed as destroyed), #1185 (`timeouts`
dropped on the live path) and #1192 (a mutation that strips `tofu-estate`
goes unnoticed).

## 3: SSA conflict, blocked

The proof #1110 asks for, two estates writing one field with the second
refused by name, cannot be reached for anything choudoufu admits. A whole
object is settled by the `tofu-estate` label before server-side apply
ever runs, and the six field-granular types that could reach a real
conflict are `unadmitted-type` (#1191, ruled out of #1579 on
2026-09-26). Separately, a `field_manager` block is re-planned on every
plan (#1190). Nothing more happens here until #1191 is taken up.

## 4: crash

The apply half is the gauntlet's `day2_crash` on the kind substrate: a
real SIGTERM between one object's create and the next, and the next plan
must propose exactly the remainder. It passes on the kubernetes-lane
estates, 4 of 4 in `live/gauntlet.json`.

The move half is narrower than #1110 describes, because live-mv's
Kubernetes writes changed after it was written:

- A metadata-block object (`kubernetes_config_map_v1` and the other typed
  kinds) is moved or renamed by one provider write that carries the label
  and the address annotation together. There is no second write to be
  killed before, and a cross-estate move re-keys no record.
- A manifest-declared object (`kubernetes_manifest`) is renamed by
  `kubesweep.Client.PatchMarkers`, which is two requests: the merge patch
  that writes the address annotation, then a JSON patch that hands the
  annotation's ownership from the `Terraform` Update entry to the
  provider's `Terraform` Apply entry (#1704). That gap is the only
  two-write window live-mv has on a cluster, so it is where the injector
  goes. A cross-estate move of a manifest object is refused by name and
  has no window.

The injector is a reactor on client-go's fake dynamic client, running
over the field-managed object tracker (the API server's own managedfields
code). It lets the marker patch through and fails the ownership patch
once, which is the state a kill leaves. The test then reruns live-mv and
applies a second rename the way the provider does.

It found a defect. The rerun reads the new address on the object, takes
the already-marked branch, reports the rename verified and writes
nothing, so the Update entry keeps the annotation and the provider's next
rename fails:

    Apply failed with 1 conflict: conflict with "Terraform" using
    stable.example.com/v1: .metadata.annotations.choudoufu.intentius.io/tofu-address

The same rename with no fault applies cleanly (the control test). The
test pins today's behaviour with `rerunRecovers = false`; the fix flips
it to `true`. live-import's manifest adoption has the same
already-stamped branch in front of the same `PatchMarkers` call
(`internal/live/liveimport/manifest.go`) and is expected to behave the
same way; that is read from the code, not measured.

## 5: namespace delete, not started

Stage-shaped: deleting the namespace is a fault injected between two
steps the lane already runs (an apply, then a plan). The questions are
#1110's: after `kubectl delete namespace` under a converged estate, do the
sweep and the plan agree on what is gone, and does the plan propose
exactly the creates, with stock's plan from the same position as the
oracle. A second variant deletes the record store's namespace
(`tofu-records-<estate>`), which `OPERATE.md` says is refused by name.
This is its own unit.
