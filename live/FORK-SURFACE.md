# Fork surface

How far this fork diverges from stock OpenTofu, counted file by file against
the fork point. The per-file detail is in
[fork-surface.json](fork-surface.json).

Rendered from `live/fork-surface.json` by `go run ./tools/forkdiff-gen -render`
(or `go run ./tools/gauntlet render`); `go run ./tools/forkdiff-gen` recounts
against the current HEAD. Edit the text around the generated markers, never
inside them.

<!-- forkdiff-gen:begin fork-surface -->
Counted at choudoufu commit `7354086d91` on 2026-11-06T20:15:37Z, against stock OpenTofu `1.13.2` at fork point [`765062032c`](https://github.com/opentofu/opentofu/commit/765062032c46cacab116b36d714db098b806dede) ("Prepare v1.13.2 release (#4753)").

5020 files diverge. 3348 of them sit under the 5 fork-owned roots below; the other 1672 are outside those roots, each named with its own reason in [`live/forkdiff_test.go`](forkdiff_test.go). A further 1145 files, not counted here, change only the Go module import path from `github.com/opentofu/opentofu` to `github.com/intentius/choudoufu`.

| Root | Files |
|---|---|
| `internal/live/` | 1905 |
| `tools/` | 583 |
| `live/` | 741 |
| `site/` | 94 |
| `.github/` | 25 |
| other (each justified) | 1672 |
| Total | 5020 |
<!-- forkdiff-gen:end fork-surface -->
