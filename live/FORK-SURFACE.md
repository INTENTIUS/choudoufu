# Fork surface

How far this fork diverges from stock OpenTofu, counted file by file against
the fork point. The per-file detail is in
[fork-surface.json](fork-surface.json).

Rendered from `live/fork-surface.json` by `go run ./tools/forkdiff-gen -render`
(or `go run ./tools/gauntlet render`); `go run ./tools/forkdiff-gen` recounts
against the current HEAD. Edit the text around the generated markers, never
inside them.

<!-- forkdiff-gen:begin fork-surface -->
Counted at choudoufu commit `84a2fa6abf` on 2026-10-04T01:29:24Z, against stock OpenTofu `1.13.0` at fork point [`2b6193043d`](https://github.com/opentofu/opentofu/commit/2b6193043d500dcfef1f3b4f4819b938b667099f) ("Bump version v1.13.0").

4194 files diverge. 2896 of them sit under the 5 fork-owned roots below; the other 1298 are outside those roots, each named with its own reason in [`live/forkdiff_test.go`](forkdiff_test.go). A further 1176 files, not counted here, change only the Go module import path from `github.com/opentofu/opentofu` to `github.com/intentius/choudoufu`.

| Root | Files |
|---|---|
| `internal/live/` | 1692 |
| `tools/` | 488 |
| `live/` | 551 |
| `site/` | 129 |
| `.github/` | 36 |
| other (each justified) | 1298 |
| Total | 4194 |
<!-- forkdiff-gen:end fork-surface -->
