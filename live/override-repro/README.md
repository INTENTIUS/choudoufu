# Provider override reproductions

One root module per upstream provider bug that #2309 considers, each the shape
that reproduced (or failed to reproduce) on 2026-11-04 against provider 6.71.0.
`drive.sh floci <dir>` or `drive.sh aws <dir>` applies it, plans again or
applies the mutation in `v2/`, and destroys. The results are in #2309's
comment of that date.

Run without overrides (`CHOUDOUFU_PROVIDER_OVERRIDES=off`) for the bug, and
with them for the fix. A real-AWS run spends money and is the maintainer's
call, per run.
