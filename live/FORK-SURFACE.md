# Fork surface

How far this fork diverges from stock OpenTofu, counted file by file against
the fork point. The per-file detail is in
[fork-surface.json](fork-surface.json).

Rendered from `live/fork-surface.json` by `go run ./tools/forkdiff-gen -render`
(or `go run ./tools/gauntlet render`); `go run ./tools/forkdiff-gen` recounts
against the current HEAD. Edit the text around the generated markers, never
inside them.

<!-- forkdiff-gen:begin fork-surface -->
Counted at choudoufu commit `1c86dd56a1` on 2026-10-28T05:27:36Z, against stock OpenTofu `1.13.0` at fork point [`2b6193043d`](https://github.com/opentofu/opentofu/commit/2b6193043d500dcfef1f3b4f4819b938b667099f) ("Bump version v1.13.0").

4969 files diverge. 3253 of them sit under the 5 fork-owned roots below; the other 1716 are outside those roots, each named with its own reason in [`live/forkdiff_test.go`](forkdiff_test.go). A further 1145 files, not counted here, change only the Go module import path from `github.com/opentofu/opentofu` to `github.com/intentius/choudoufu`.

| Root | Files |
|---|---|
| `internal/live/` | 1875 |
| `tools/` | 580 |
| `live/` | 677 |
| `site/` | 96 |
| `.github/` | 25 |
| other (each justified) | 1716 |
| Total | 4969 |
<!-- forkdiff-gen:end fork-surface -->
