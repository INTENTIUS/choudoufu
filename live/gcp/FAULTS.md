# Fault injection on GCP

The faults a GCP estate can meet between two steps of a run, and what
injects each. None has run yet: the harness is floci-gcp, pinned in
`live/floci-gcp-image`, and the scenarios that inject these are built for
the maintainer to run.

| # | Fault | Shape | What injects it |
|---|-------|-------|-----------------|
| 1 | A label stripped out of band | claim 7 | `gcp-greenfield.sh` under `BREAK=1` removes `tofu-estate`; the replan must restore it |
| 2 | An orphan's label stripped | claim 1 | `gcp-no-silent-orphans.sh` under `BREAK=1`; the replan must leave the object alone |
| 3 | A write from a principal without the project binding | claim 13 | `gcp-the-label-is-the-boundary.sh`; `BREAK=1` adds the binding and the denied write must go through |
| 4 | A record bucket that cannot be trusted | claim 29 | no scenario: `gcp-records-in-a-bucket.sh` was deleted under #2288, since `TestGCSStoreConformance` (CI) and plunge's `TestPlungeGSStoreConformance` (floci-gcp) hold the store |
| 5 | The asset index lagging a create or a delete | sweep | injectable on the emulator through floci-gcp's configurable search lag (lex00/floci-gcp#7); not yet measured |
| 6 | A kill mid-apply | stage | the gauntlet's `day2_crash`, once a GCP lane exists |

Fault 5 is the one GCP shares with AWS rather than Kubernetes. The family
answers `MarkerOutlivesObject` true, so the plan reads the record
tombstone AWS's tagging index already needs, and a lingering hit is not
taken for a second claimant.
