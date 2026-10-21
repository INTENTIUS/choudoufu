# The claims, one page each

One page per promise: what it promises, each proof's command, what it
checks, and what its `BREAK=1` run breaks. The index is `../claims.json`,
rendered as `live/CLAIMS.md`.

Proof is per provider (#1112). Each provider cell in `claims.json` lists its
proofs: a scenario under `../scenarios/`, some steps of another claim's
scenario, or a gauntlet stage cited as `<estate>:<stage id>`. Every proof
names a dated run: an evidence file under `../evidence/`, a carried
`last_run`, or a stage that reads pass in `live/gauntlet.json`.
`live/smoke_claims_test.go` holds each page to its cells.

An AWS proof needs Docker and the AWS CLI for the pinned floci emulator. A
Kubernetes proof needs Docker, kind and kubectl, and the emulator where its
cell says so. A real AWS proof is maintainer-run with `SMOKE_REAL_AWS=1`.
Without Go, set `CHOUDOUFU_VERSION` to a release tag; a proof whose
`BREAK=1` rebuilds choudoufu needs Go.

Numbers are never reused. A retired number's page points at the claim
that carries its proof now.
