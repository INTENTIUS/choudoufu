# Fork surface

How far this fork diverges from stock OpenTofu, counted file by file against
the fork point. The per-file detail is in
[fork-surface.json](fork-surface.json).

Rendered from `live/fork-surface.json` by `go run ./tools/forkdiff-gen -render`
(or `go run ./tools/gauntlet render`); `go run ./tools/forkdiff-gen` recounts
against the current HEAD. Edit the text around the generated markers, never
inside them.

<!-- forkdiff-gen:begin fork-surface -->
Counted at choudoufu commit `8ffa461f0b` on 2026-11-11T17:50:06Z, against stock OpenTofu `1.14.0-dev` at fork point [`aa3eb66a0c`](https://github.com/opentofu/opentofu/commit/aa3eb66a0c1c792b7751e6ffeef7ca0cd8693f66) ("[Govulncheck][GO-2026-6859][main] Upgrade Go toolchain to 1.27.4 (#4831)").

5013 files diverge. 3347 of them sit under the 5 fork-owned roots below; the other 1666 are outside those roots, each named with its own reason in [`live/forkdiff_test.go`](forkdiff_test.go). A further 1167 files, not counted here, change only the Go module import path from `github.com/opentofu/opentofu` to `github.com/intentius/choudoufu`.

| Root | Files |
|---|---|
| `internal/live/` | 1905 |
| `tools/` | 583 |
| `live/` | 741 |
| `site/` | 94 |
| `.github/` | 24 |
| other (each justified) | 1666 |
| Total | 5013 |
<!-- forkdiff-gen:end fork-surface -->
