# Fork surface

How far this fork diverges from stock OpenTofu, counted file by file against
the fork point. The per-file detail is in
[fork-surface.json](fork-surface.json).

Rendered from `live/fork-surface.json` by `go run ./tools/forkdiff-gen -render`
(or `go run ./tools/gauntlet render`); `go run ./tools/forkdiff-gen` recounts
against the current HEAD. Edit the text around the generated markers, never
inside them.

<!-- forkdiff-gen:begin fork-surface -->
Counted at choudoufu commit `d8b3da2a3c` on 2026-11-04T21:37:24Z, against stock OpenTofu `1.13.2` at fork point [`765062032c`](https://github.com/opentofu/opentofu/commit/765062032c46cacab116b36d714db098b806dede) ("Prepare v1.13.2 release (#4753)").

5066 files diverge. 3349 of them sit under the 5 fork-owned roots below; the other 1717 are outside those roots, each named with its own reason in [`live/forkdiff_test.go`](forkdiff_test.go). A further 1145 files, not counted here, change only the Go module import path from `github.com/opentofu/opentofu` to `github.com/intentius/choudoufu`.

| Root | Files |
|---|---|
| `internal/live/` | 1903 |
| `tools/` | 583 |
| `live/` | 744 |
| `site/` | 94 |
| `.github/` | 25 |
| other (each justified) | 1717 |
| Total | 5066 |
<!-- forkdiff-gen:end fork-surface -->
