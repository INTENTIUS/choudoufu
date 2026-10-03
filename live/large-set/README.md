# Large-set fixture (#1750)

The fixture epic #1749 is measured on: N estate roots, each with its own
`estate.chdf.hcl`, all calling one shared module, and a module bump from
version A to version B. The generator is `tools/largeset-gen`; the shape and
the reason for a new tool are in `internal/live/largeset/gen.go` and the tool's
header.

| File | What it is |
|---|---|
| `apply.sh` | Applies every root of a generated fixture at A against a scratch floci (`cdfa-largeset-floci`, port 4880 by default) in the fixture's apply order and leaves them applied. One verdict line, `LARGESET-APPLY: green|red - ...`. |
| `oci.sh` | The OCI variant: a local registry (`cdfa-oci-registry`, :4890), the module published at 1.0.0 and 1.1.0, every root pinned at 1.0.0 and applied, then a pin bump across a subset of roots. One verdict line, `LARGESET-OCI: ...`. Removes its containers on exit. |
| `baseline-n5.json` | The gated baseline record at N=5: per estate, `live-plan` at A after its own apply (the steady-state control, which must be empty) and `live-plan` of the bump, each with API calls through the counting proxy, wall clock and plan summary. Written by `TestLargeSetBaselineAgainstFloci`, never by hand; read it with `largeset.ReadBaseline`, which refuses an unknown schema. |
| `setplan-n5.json` | #1752's comparison at N=5: one `live-plan-set` of the bump against `live-plan -out` in each root in turn, three repeats each, alternating. Every root's `resource_changes` agree both ways and match `baseline-n5.json`'s bump, or the record is refused (`GateSetPlan`). Calls, wall clock and peak resident memory (the process group, sampled from `ps`) for both arms. Written by `TestLargeSetSetPlanAgainstFloci`, never by hand. |

Regenerate the record (about six minutes):

    LARGESET_BASELINE=1 LARGESET_RECORD=$PWD/live/large-set/baseline-n5.json \
      env -u PWD go test ./internal/live/largeset/ -run TestLargeSetBaselineAgainstFloci -v -timeout 30m

Regenerate the set-plan record (about five minutes; `LARGESET_FLOCI_PORT` pins floci's host port):

    LARGESET_SETPLAN=1 LARGESET_SETPLAN_RECORD=$PWD/live/large-set/setplan-n5.json \
      env -u PWD go test ./internal/live/largeset/ -run TestLargeSetSetPlanAgainstFloci -v -timeout 40m

Read calls, not seconds, when comparing: the calls are identical across
repeats, and the seconds are whatever the machine was doing.

N=5 is the developer loop. N=20 runs only on the maintainer's go
(`LARGESET_MAINTAINER_GO=1`), and nothing larger. None of this is a gauntlet
estate: it writes nothing to `live/gauntlet.json` or the cohort artifacts.
On CI it runs only when `.github/workflows/large-set.yml` is dispatched.
