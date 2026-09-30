# #1355 reproduction harness (floci only, manual)

GitHub issue #1355: on real AWS, one `apply -destroy` of a two-instance
record-backed estate destroyed one instance and reported success. It has
never been reproduced. This directory is the loop that tried, kept so the
next attempt starts from it instead of rebuilding it.

Nothing here runs in CI, and it is not a smoke claim. It never talks to real
AWS: every endpoint is the local emulator pinned in `live/floci-image`.

```
live/repro/1355/run.sh <name> <iters-per-loop> <loops> [jitter-ms] [maxkeys] [cpu-hogs]
```

The estate is the issue's own: `terraform_data.effect` over
`toset(["a.b", "plain"])`, an S3 record store in a versioned bucket that
meets the bucket contract. Each iteration is a fresh estate and runs apply,
an out-of-band `put-object-tagging` on `a.b`'s record (replace, add, replace
plus deleting the state cache, or none, rotating), an apply that changes
`input`, and `apply -destroy`. It passes only if all three exit 0, report
2 added, 2 changed and 2 destroyed, and no record object is left.

`run.sh`'s header lists the stressors (a jitter proxy, forced ListObjectsV2
pagination, CPU load, one shared bucket, continuous re-taggers, state cache
removal, read parallelism). `CONTROL=1` deletes `plain`'s record before the
destroy and must be reported as an anomaly; that is how to check the loop
can fail.

The store and projection layers have in-process loops of the same shape,
skipped unless `CHOUDOUFU_STRESS_1355` names an iteration count:

```
CHOUDOUFU_STRESS_1355=1000 go test -race -run TestStressRunCacheOverS3SeesBothRecords ./internal/live/staterecord/
CHOUDOUFU_STRESS_1355=3000 go test -race -run TestStressTwoRecordBackedInstancesBothMaterialize ./internal/live/projection/
```

`CHOUDOUFU_STRESS_1355_CONTROL=1` makes each of them fail on purpose.
