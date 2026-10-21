# Draft proofs

A script here has never run green on both arms, so it proves no claim.
Its promise is a GitHub issue until it does. It lives here and not under
`scenarios/` because `live/smoke_claims_test.go` admits no unregistered
scenario there, and every proof in a cell names a dated run.

`just smoke <name>` runs a draft the same way it runs a scenario, `BREAK=1`
included.

Promoting a draft after its first green run, both arms:

1. `git mv live/smoke/drafts/<name>.sh live/smoke/scenarios/`.
2. Add a proof to the cell in `live/smoke/claims.json` (scenario, command,
   minutes as measured, break_mode, needs_go/needs_emulator as the script
   needs them) and the evidence file of its first run, with the claim if it
   is new (a reserved number comes back as itself), and run
   `go run ./tools/gauntlet render` to refresh `live/CLAIMS.md`.
3. Add a `## On Kubernetes` section with the command to the claim's page
   under `live/smoke/claims/`.
4. These drafts are named `k8s-<claim slug>`, because the bare slug is
   already the AWS proof's file. `TestSmokeClaimsMatchScenarios` accepts a
   readability prefix put on the slug since the first promotions
   (2026-10-04), so this step needs nothing more.

The Azure scripts (`az-*.sh`, #1120) live here too. They were registered
under `scenarios/` on the azure branch with cells reading `unproven`; since
cells read only `proven` or `n/a`, they wait here for their first dated run
against floci-az, and claims 48 to 52 (#2119's D1 to D5) are reserved
numbers until then. An Azure promotion adds a `## On Azure` section in
step 3, and the `az-` prefix plays the part `k8s-` does in step 4.

The scenario lints read drafts as well as scenarios (#1876), so a draft is
held to them before it is promoted: no bare `kubectl` in a `k8s-*` draft
(`bash live/smoke/selftest-bounds.sh --only kubectl-roster`), no bare `wait`
(`live/smoke_bare_wait_test.go`), and an EXIT-trap teardown that turns
errexit off (`live/smoke_teardown_test.go`).
