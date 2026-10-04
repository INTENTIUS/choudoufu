# Draft proofs

A scenario here is written and has never run. It is the proof a
`live/smoke/claims.json` cell names in its note while that cell still reads
`open`.

It lives here and not under `scenarios/` because `live/smoke_claims_test.go`
admits no middle state. Every scenario under `scenarios/` must be the proof
of a cell. A cell with a proof must read `proven` or `restated`, and
`proven` means the proof runs and its BREAK control catches. Putting an
unrun script there would make a cell read `proven` on nobody's run, which
is the false green #1379 and #1591 exist to stop.

`just smoke <name>` runs a draft the same way it runs a scenario, `BREAK=1`
included.

Promoting a draft after its first green run, both arms:

1. `git mv live/smoke/drafts/<name>.sh live/smoke/scenarios/`.
2. Add a proof to the cell in `live/smoke/claims.json` (scenario, command,
   minutes as measured, break_mode, needs_go/needs_emulator as the script
   needs them), set the status to `proven`, rewrite the note, and copy the
   file to `site/data/claims.json`.
3. Add the name to `.github/workflows/k8s-smoke.yml`'s matrix
   (`live/k8s_ci_test.go` requires it for a `k8s-*` scenario).
4. Add a `## On Kubernetes` section with the command to the claim's page
   under `live/smoke/claims/`.
5. These drafts are named `k8s-<claim slug>`, because the bare slug is
   already the AWS proof's file. `TestSmokeClaimsMatchScenarios` accepts a
   readability prefix put on the slug since the first promotions
   (2026-10-04), so this step needs nothing more.
