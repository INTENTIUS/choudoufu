# Draft proofs

A scenario here is written and has never run. It is the proof a
`live/smoke/claims.json` cell names in its note while that cell still reads
`open`.

It lives here and not under `scenarios/` because `live/smoke_claims_test.go`
admits no unregistered scenario. Every scenario under `scenarios/` must be
the proof of a cell, and `proven` means every proof in the cell has a
dated run on record. An unrun script registered there would turn a cell
from `open` to `unproven`, which says a proof exists when none has ever
run; a draft keeps the cell `open` and the script out of the table.

`just smoke <name>` runs a draft the same way it runs a scenario, `BREAK=1`
included.

Promoting a draft after its first green run, both arms:

1. `git mv live/smoke/drafts/<name>.sh live/smoke/scenarios/`.
2. Add a proof to the cell in `live/smoke/claims.json` (scenario, command,
   minutes as measured, break_mode, needs_go/needs_emulator as the script
   needs them) and the evidence file of its first run, set the status to
   `proven`, rewrite the note, and run `go run ./tools/gauntlet render` to
   refresh `live/CLAIMS.md`.
3. Add a `## On Kubernetes` section with the command to the claim's page
   under `live/smoke/claims/`. The Azure drafts already have their
   `## On Azure` section, which says the proof is a draft; rewrite it to
   describe the run.
4. These drafts are named `k8s-<claim slug>` or `az-<name>`, because the
   bare slug is already the AWS proof's file. `TestSmokeClaimsMatchScenarios`
   accepts any file name a cell registers since #1504, so this step needs
   nothing more.

The Azure drafts (#1120) run against floci-az, which `azure_up` in
`live/smoke/lib.sh` starts. Each says in its header what the emulator
cannot answer yet (live/FLOCI.md, Azure), and its cell's note says the
same.

`TestSmokeDraftsNameAnOpenCell` (`live/smoke_claims_test.go`) holds a
draft to its cell: the header names a claim and a provider, states that
claim's title, the cell reads `open`, and its note names the draft's path.

The scenario lints read drafts as well as scenarios (#1876), so a draft is
held to them before it is promoted: no bare `kubectl` in a `k8s-*` draft
(`bash live/smoke/selftest-bounds.sh --only kubectl-roster`), no bare `wait`
(`live/smoke_bare_wait_test.go`), and an EXIT-trap teardown that turns
errexit off (`live/smoke_teardown_test.go`).
