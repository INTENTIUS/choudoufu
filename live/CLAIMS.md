# Claims

A claim is a short promise with a number that never changes. Each one is
proven per provider by a scenario you can run, with a BREAK arm that breaks it
on purpose. A cell is proven when every proof in it names a dated run,
unproven when one does not, restated when the promise holds in a weaker form,
n/a when the substrate has no such concept, and open when there is no proof
yet. Each claim's page has its steps.

Rendered from `live/smoke/claims.json` and the evidence files under
`live/smoke/evidence/` by `go run ./tools/gauntlet render`. Edit the text
around the generated markers, never inside them.

<!-- claims-gen:begin claims -->
| # | Claim | Theme | AWS | KUBERNETES | GCP |
|---|---|---|---|---|---|
| 1 | [Owned resources never fall out of a plan](smoke/claims/no-silent-orphans.md) | adopt | unproven<br>`just smoke no-silent-orphans` ~2 min, ran 2026-09-17 at `161369754a`<br>`just smoke a-shadow-is-not-a-claimant` ~3 min<br>`just smoke a-held-delete-is-not-gone` ~3 min | unproven<br>`just smoke k8s-no-silent-orphans` ~3 min<br>`just smoke k8s-a-held-delete-is-not-gone` ~3 min<br>`just smoke k8s-a-deleted-namespace-is-gone` ~5 min | open |
| 2 | [Nothing is held](smoke/claims/no-self-managed-locks.md) | operate | unproven<br>`just smoke no-self-managed-locks` ~2 min<br>`just smoke two-writers-one-record` ~3 min, needs Go<br>`just smoke cas-holds-under-every-sse-flavour` ~6 min, needs Go, real AWS, maintainer-run, ran 2026-09-19 at `7720a46df5` | proven<br>proven by claim 29: `just smoke k8s-records-in-the-cluster` | open |
| 3 | [The cache changes the cost, never the answer](smoke/claims/staleness-costs-reads.md) | cache | unproven<br>`just smoke staleness-costs-reads` ~3 min<br>`just smoke unchanged-is-free` ~3 min<br>`just smoke cache-serves-the-whole-estate` ~2 min | proven<br>`just smoke k8s-staleness-costs-reads` ~3 min, ran 2026-10-04 at `20bc8ccbcf`<br>`just smoke k8s-unchanged-is-free` ~2 min, ran 2026-10-04 at `20bc8ccbcf` | open |
| 5 | [A crash is fixed by re-running](smoke/claims/recovery-is-a-rerun.md) | adopt | unproven<br>`just smoke a-killed-apply-hides-nothing` ~4 min, needs Go | proven<br>gauntlet stage `reference-k8s:day2_crash`, pass 2026-10-12 at `2f02038dd1` | open |
| 6 | [Stock in, stock out, whenever you need it](smoke/claims/roundtrip.md) | cache | unproven<br>`just smoke roundtrip` ~3 min<br>`just smoke stock-when-you-need-it` ~3 min | proven<br>`just smoke k8s-roundtrip` ~2 min, ran 2026-10-04 at `9a46ebe205`<br>`just smoke k8s-stock-when-you-need-it` ~1 min, ran 2026-10-04 at `20bc8ccbcf` | open |
| 7 | [Identity is a tag you can read, move and carve by](smoke/claims/identity-is-a-tag.md) | adopt | unproven<br>`just smoke carve-by-retag` ~6 min, needs Go<br>`just smoke count-is-a-fungible-set` ~2 min | unproven<br>`just smoke k8s-greenfield` ~2 min<br>`just smoke k8s-custom-resource` ~5 min<br>proven by claim 13: `just smoke k8s-the-label-is-the-boundary` | open |
| 13 | [The tag is the boundary](smoke/claims/the-tag-is-the-boundary.md) | gate | unproven<br>`just smoke the-tag-is-the-boundary` ~4 min<br>`just smoke a-role-holds-an-address-pattern` ~3 min<br>`just smoke the-boundary-holds-across-regions` ~2 min<br>`just smoke the-boundary-holds-across-accounts` ~2 min<br>`just smoke two-estates-at-once` ~2 min | unproven<br>`just smoke k8s-the-label-is-the-boundary` ~5 min | open |
| 14 | [A plan costs its estate](smoke/claims/plan-cost-tracks-the-estate.md) | scale | unproven<br>`just smoke plan-cost-under-foreign-load` ~5 min, needs Go | proven<br>`just smoke k8s-plan-cost-tracks-the-estate` ~2 min, ran 2026-10-04 at `20bc8ccbcf` | open |
| 47 | [A plan costs its change](smoke/claims/plan-cost-tracks-the-change.md) | scale | unproven<br>`just smoke plan-cost-tracks-the-change` ~15 min, needs Go | open | open |
| 15 | [Apply exactly what was approved](smoke/claims/apply-what-was-approved.md) | gate | unproven<br>`just smoke apply-what-was-approved` ~7 min, needs Go<br>`just smoke the-server-gets-the-last-word` ~4 min | unproven<br>`just smoke k8s-the-server-gets-the-last-word` ~4 min | open |
| 28 | [An estate reaches only its own records, and what it declares](smoke/claims/a-name-prefix-shares-no-keys.md) | operate | unproven<br>`just smoke a-name-prefix-shares-no-keys` ~1 min, needs Go<br>`just smoke one-bucket-many-estates` ~8 min, needs Go, real AWS, maintainer-run, ran 2026-09-19 at `5ad067f82c`, ran 2026-09-19 at `7720a46df5`<br>`just smoke a-read-only-role-can-plan` ~6 min, needs Go, real AWS, maintainer-run, ran 2026-09-19 at `130e24d22f`<br>`just smoke an-estate-reads-another-by-declaring-it` ~3 min | proven<br>proven by claim 29: `just smoke k8s-records-in-the-cluster`<br>`just smoke k8s-an-estate-reads-another-by-declaring-it` ~1 min, ran 2026-10-04 at `20bc8ccbcf` | open |
| 29 | [The record store refuses what it can't trust](smoke/claims/a-wrong-bucket-is-refused.md) | operate | unproven<br>`just smoke a-wrong-bucket-is-refused` ~2 min, needs Go<br>`just smoke a-waiver-names-what-it-waives` ~2 min, needs Go<br>`just smoke a-bulk-read-is-complete-or-it-fails` ~2 min, needs Go | unproven<br>`just smoke k8s-records-in-the-cluster` ~20 min, needs Go, ran 2026-09-21 at `a30eb7cb0d`<br>proven by claim 1: `just smoke k8s-a-deleted-namespace-is-gone` | open |
| 40 | [No secret survives unless you let it](smoke/claims/no-secret-survives-in-what-the-tool-keeps.md) | operate | unproven<br>`just smoke no-secret-survives-in-what-the-tool-keeps` ~3 min | proven<br>`just smoke k8s-no-secret-survives-in-what-the-tool-keeps` ~3 min, ran 2026-10-04 at `85d3d8afb5` | open |
| 45 | [Drift you ignore isn't drift](smoke/claims/an-ignored-tag-is-not-drift.md) | operate | unproven<br>`just smoke an-ignored-tag-is-not-drift` ~3 min | proven<br>`just smoke k8s-a-label-is-a-change` ~10 min, needs the emulator, ran 2026-09-21 at `a30eb7cb0d` | open |
<!-- claims-gen:end claims -->
