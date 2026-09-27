Closes #1582. Part of #1579.

Ruling: issue #1582's body (comments: 0; the "Before you start" section carries the ruling text, read at origin/main f2b7f012a2 by a read-only audit on 2026-09-26). Scope: "A Forbidden from the cluster becomes the same denied class as AWS: one warning per run naming the verb, resource and namespace the grant lacks, pointing at live/kubernetes/estate-\*.yaml. Non-403 failures stay LIST_FAILED. Share the aggregation shape with sweepdenied.go rather than copying it."

## Operator-facing text (verbatim)

The one warning, raised once per run when any Kubernetes list call was denied:

> The Kubernetes sweep's own list call was refused by the cluster's RBAC for N kind(s) (Kind1, Kind2, ...), so a resource of any of those kinds this estate owns but no longer declares WILL NOT be proposed for destruction by this run. Each denial names the verb, resource and scope the server's own message named. Grant \<verb\> \<resource\> at the cluster scope (or in namespace "\<ns\>") to the identity running this estate - see live/kubernetes/estate-grant.yaml, whose comment names the ordinary RBAC an estate's principal needs beside the estate fence - then re-run. Every denied kind is one [WARN] line in the log: run with TF_LOG=WARN, or TF_LOG_PATH to write it to a file.

Log line per denied kind:

> [WARN] stateless/discovery: Kubernetes sweep denied: listing \<Kind\> (for \<type\>) needs \<verb\> on \<resource\>, at the cluster scope (or in namespace "\<ns\>"): \<err\>

## What changed

- `internal/live/kubesweep/forbidden.go` (new): `kubesweep.Forbidden(err)` classifies a cluster error as RBAC-Forbidden (403) and parses the RBAC authorizer's own message for the verb, resource and namespace/cluster-scope it named. This is "kubesweep error typing" from the issue's file list, alongside `connect.go`'s existing `Diagnose` (401/unreachable/exec-plugin classification) - 403 was previously left `CauseUnclassified` on purpose (see connect.go's comment), and stays that way; this adds a second, independent classifier for it rather than folding it into `Diagnose`.
- `internal/live/discovery/kubernetes.go`: in `sweepKubernetes`'s per-kind `List` failure branch, a Forbidden error now routes through the new `sweepGapKubeDenied` instead of being appended to `res.SweepGaps` directly. The gap's `Reason` is unchanged (`SweepGapListFailed`), matching how AWS's denied gaps keep `LIST_FAILED` too - the difference is bookkeeping, not classification. A non-Forbidden failure is byte-for-byte the same path as before.
- `internal/live/discovery/sweepdenied.go`: new `kubeDenial` struct (kind, verb, resource, namespace) and `sweepGapKubeDenied` / `kubeDeniedSweepDiag` / `kubeGrantLine`, in a clearly separated block. These call the *same* `nameFirst`, `joinAnd` and `plural` helpers the AWS aggregation above them uses - the "share the aggregation shape... rather than copying it" instruction, taken literally: no per-provider-family reimplementation of the dedup/sort/"first five and N more" logic. `kubeDenial` itself is a separate struct from `sweepDenial` because the two denials carry different fields (Cloud Control type + IAM action vs. Kubernetes kind + RBAC verb + resource); forcing them through one struct would give each side fields meaningless to it. `DeniedSweepWarning` (the multi-pass, #1513 path) now aggregates and raises both.
- `internal/live/discovery/discovery.go`: `Discover` raises `kubeDeniedSweepDiag(res.kubeSweepDenied)` alongside the existing `deniedSweepDiag` call, gated the same way (`!req.DeferDeniedSweepWarning`).
- `internal/live/discovery/result.go`: `Result.kubeSweepDenied []kubeDenial`, the Kubernetes counterpart of `sweepDenied`.
- `internal/live/discovery/refusals.go`: new registry entry `"Kubernetes sweep denied"` (`SummaryKubernetesSweepDenied`), severity Warning (a coverage gap, never a wrong plan - same class as `SummaryIncompleteSweep`).
- `internal/live/discovery/severity_test.go`: registers the new summary's severity in `TestEveryRegisteredRefusalHasAStatedSeverity`'s switch (this test fails closed on an unregistered severity, so it had to be updated for the suite to build green).
- `internal/live/discovery/kubernetes_test.go`: `stubSweeper` gains `listErrs map[string]error` (kind -> error) so a test can inject a real `apierrors.NewForbidden` rather than only the pre-existing plain `errors.New("forbidden")` (which correctly stays *not* denied - it isn't a real API status error).

## Note on #1580/#1613

PR #1613 (`live/sweep-interface-1580`) was still open, unmerged, at HEAD b1b2ea5381 when this was written - checked again just before pushing. This branch is built directly on `sweepKubernetes` in `kubernetes.go` as it exists today. If #1613 lands first, this rebases onto the `discovery.Sweeper`/`KubernetesSweep` shape it introduces; the aggregation in sweepdenied.go and the `kubesweep.Forbidden` classifier are independent of that refactor and should carry over unchanged.

## Red proof

Built the test (`TestKubernetesSweepDeniedListIsClassedAndNamed`) against unfixed `kubernetes.go`/`sweepdenied.go`/`result.go`/`refusals.go`/`discovery.go` (git-checked-out to HEAD in the worktree, source reverted, test kept):

```
# github.com/intentius/choudoufu/internal/live/discovery [github.com/intentius/choudoufu/internal/live/discovery.test]
internal/live/discovery/kubernetes_test.go:227:13: res.kubeSweepDenied undefined (type *Result has no field or method kubeSweepDenied)
internal/live/discovery/kubernetes_test.go:228:75: res.kubeSweepDenied undefined (type *Result has no field or method kubeSweepDenied)
internal/live/discovery/kubernetes_test.go:230:16: res.kubeSweepDenied undefined (type *Result has no field or method kubeSweepDenied)
internal/live/discovery/kubernetes_test.go:234:10: undefined: kubeDeniedSweepDiag
internal/live/discovery/kubernetes_test.go:234:34: res.kubeSweepDenied undefined (type *Result has no field or method kubeSweepDenied)
internal/live/discovery/kubernetes_test.go:239:18: undefined: SummaryKubernetesSweepDenied
internal/live/discovery/kubernetes_test.go:239:48: undefined: SummaryKubernetesSweepDenied
FAIL	github.com/intentius/choudoufu/internal/live/discovery [build failed]
FAIL
```

Confirms the issue's own premise more strongly than a runtime mismatch would: the denial mechanism did not exist at all, so today's code has literally no path from a Forbidden `List` to anything but the bare `LIST_FAILED` gap with the raw error text.

## Green result

```
=== RUN   TestKubernetesSweepDeniedListIsClassedAndNamed
--- PASS: TestKubernetesSweepDeniedListIsClassedAndNamed (0.00s)
```

Full runs (uncached, `-count=1`):

```
ok  	github.com/intentius/choudoufu/internal/live/discovery	9.182s
ok  	github.com/intentius/choudoufu/internal/live/kubesweep	4.315s
```

`env -u PWD go build ./...` clean. `gofmt -l` clean on every touched file. `go vet` clean on both packages.

## Left undone / notes

- Only `List`'s per-kind Forbidden is routed through the denied path, matching the issue's "Done when" criterion exactly (a role lacking `list` on one kind). `Kinds` (the cluster-wide API-discovery call) failing with Forbidden still raises the pre-existing `SummaryKubernetesSweepUnavailable` (whole-sweep-unavailable) warning, unchanged - that failure mode already has its own warning and isn't what the issue's scope or Done-when criterion names.
- The RBAC authorizer's message format (`cannot <verb> resource "<resource>" in API group "<group>" [in the namespace "<ns>"|at the cluster scope]`) is not vendored/pinned anywhere in this repo (the `apiserver` module isn't a dependency); `kubesweep.Forbidden` degrades gracefully if it doesn't match (still classes as denied, with empty Detail fields, and the caller falls back to the raw error and `err.Error()` in the log line), so a wording change upstream loses parsed detail rather than breaking the classification.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
