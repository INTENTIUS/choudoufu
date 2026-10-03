# Fault injection on Kubernetes

GitHub issue #1110 asked which faults a real cluster produces that the
kubernetes lane never injected, and whether each should be a gauntlet
stage (a fault between two steps of a run the lane already does) or a
smoke claim with a `BREAK` control (one object, one fault, one honest
plan). This note records the answer for all five and what is left.

| # | Fault | Shape | Status | What injects it |
|---|-------|-------|--------|-----------------|
| 1 | A finalizer holds a delete | claim | done, claim 1 (#1186) | `live/smoke/scenarios/k8s-a-held-delete-is-not-gone.sh` puts a finalizer on the object; `BREAK=1` takes it off before the destroy |
| 2 | An admission webhook rejects or mutates | claim | done, claim 15 (#1193) | `k8s-the-server-gets-the-last-word.sh`: a `ValidatingWebhookConfiguration` with no endpoint, and `MutatingAdmissionPolicy` objects for the mutations |
| 3 | A server-side apply conflict | claim | unblocked by #1191; proof written, not run | `live/kubernetes/proof-ssa-conflict.sh` puts two estates on one ConfigMap, one label each; `BREAK=1` forces the same write through the stock oracle |
| 4a | A kill mid-apply | stage | done, `day2_crash` on kind (#1189) | the gauntlet's crash stage sends SIGTERM between two objects' creates |
| 4b | A kill mid-move | unit test | injector landed, fault is red today | `crashBetweenMarkerWrites` in `internal/live/mv/fault_move_crash_test.go` |
| 5 | The namespace is deleted under the estate | claim | done, claim 1 (#1765) | `k8s-a-deleted-namespace-is-gone.sh` runs `kubectl delete namespace` under two estates and their stock twins; `BREAK=1` asserts an empty plan after the delete, which must fail |

## 1 and 2: done

Both are claims, measured on kind and written up on the claims
themselves (`live/smoke/claims/`). The findings they turned up were filed
separately: #1184 (a held delete printed as destroyed), #1185 (`timeouts`
dropped on the live path) and #1192 (a mutation that strips `tofu-estate`
goes unnoticed).

## 3: SSA conflict, unblocked, proof not yet run

The six field-granular types are admitted
([#1191](https://github.com/INTENTIUS/choudoufu/issues/1191), ruled
2026-10-03, superseding the 2026-09-26 ruling that kept them out of
#1579). Their ownership marker is the server-side-apply field manager,
`choudoufu:<estate>`, and the patched object's own estate label is
irrelevant, so two estates can now meet on one object - one field each -
which a whole-object type never can: there the `tofu-estate` label
settles the object before server-side apply runs.

That gives #1106 section 3's second control its subject, and it is
built: a planned write with `force = true` over a field another estate's
manager owns is refused by the plan, naming that estate, with nothing
applied (`Force refused over another estate's field`). Without force the
plan warns by name and the API server refuses the apply with a 409 that
names `choudoufu:<other>`, the first estate's name rather than the
provider's `Terraform`. Force against any manager that is not an
estate's keeps its ordinary meaning.

The proof #1110 asks for is `live/kubernetes/proof-ssa-conflict.sh`. It
is a manual script against a kind cluster the caller creates from
`live/kind-node-image`, and **it has not been run**: it was written with
the unit and is not a claim, because `live/smoke/claims.json` admits a
proof only once it has run and its control has caught. Its five steps:
estate `ssa-a` labels a ConfigMap neither estate owns and replans empty;
estate `ssa-b` declares the same label and is warned, then refused by
the server's 409, both by name; the same block with `force = true` is
refused by the plan; force over a label kubectl wrote goes through; and
two field-granular blocks of one estate on one object are refused.
`BREAK=1` applies the forced write with the stock oracle and requires
the label to move, so the plan's refusal is shown to be this fork's and
not the server's.

Still open: running it, and then filing it as a smoke claim's Kubernetes
proof. The `field_manager` block on `kubernetes_manifest` is a separate
question (#1190) and is unchanged.

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

## 5: namespace delete, done as a claim

#1110 put this down as stage-shaped, a fault between an apply and a plan
the lane already runs. #1765 took the fallback the issue allowed and made
it claim 1, `live/smoke/scenarios/k8s-a-deleted-namespace-is-gone.sh`. A
stage is one more headline column: it would have had to read n/a on every
floci estate and re-measure the four kubernetes-lane estates, when what
the fault measures does not depend on an estate's shape beyond whether it
declares its namespace. The claim carries both variants and its own stock
oracle, so nothing was given up for the smaller shape.

What it found, on kind, against stock's plan from the same position:

- Declared namespace: `live-ls` lists nothing, the plan is stock's three
  creates with no orphan and nothing read as present, the namespace is
  created first, and one apply converges to `No changes.`.
- Undeclared namespace: the plan is stock's two creates, and both tools'
  applies fail with `namespaces "<ns>" not found`, once per object. Once
  the namespace is back, one apply converges.
- Still terminating (a finalizer on an object inside it, fault 1's shape):
  the plan reads the namespace and the held object as present and proposes
  only the object the delete already took, as stock's does.
- The record store's namespace deleted: the plan and the apply are both
  refused with `Cannot open the record store`, naming the namespace and
  the `kubectl create namespace` line, and the refused apply creates
  nothing.

No defect turned up.
