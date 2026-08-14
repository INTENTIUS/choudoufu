# Reference

The normative specifications live in the repository, next to the code and the
tests that hold them to it. This page is the index into them.

They are written for people integrating with choudoufu or working on it. If you
are trying to get an estate running, the path pages are what you want.

## Specifications

| Document | What it settles |
|---|---|
| [`live/MARKERS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MARKERS.md) | The marker tag spec: key names, the escaping rule, continuation tags, ownership semantics, the rename rule, and what protects the tags. The one surface external tooling can rely on. |
| [`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md) | Every construct the mode bounds or rejects, per rule, each with its lint rule and fixture. |
| [`live/RECEIPTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md) | Recording an effect that leaves nothing in the live system to read back, and the four guards on the pattern. |
| [`live/OUTPUTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md) | Sharing values between estates with no remote state. |

## Coverage and evidence

| Document | What it settles |
|---|---|
| [`live/COVERAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/COVERAGE.md) | Which AWS resource types are covered, in layers, and what each layer means. |
| [`live/SURVEY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/SURVEY.md) | How admission is decided per type, the method, and the raw signals behind it. |

## The demo, which is also the test suite

[`live/e2e/README.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/README.md)
documents the harness: what each step proves, the environment knobs, and what
each exit code means.

```
bash live/e2e/run.sh --expect 5
```

## Commands

`choudoufu <command> -help` is authoritative for flags. The live-specific
commands:

| Command | What it does |
|---|---|
| `choudoufu plan` / `apply` | Ordinary plan and apply. With a `live` block present, these run against markers. |
| `choudoufu live-mv <old> <new>` | Rewrites the `tofu-address` tag. The replacement for `moved` blocks. |
| `choudoufu live-import` | Bulk migration: reads an existing state file once, verifies each entry, stamps markers on what verifies. |
| `choudoufu live-plan` | The live plan, invoked directly. |

## The `live` block

Not documented here yet, deliberately. Two open changes alter it:
[#109](https://github.com/INTENTIUS/choudoufu/issues/109) removes `snapshots`
and `snapshot_path`, and [#72](https://github.com/INTENTIUS/choudoufu/issues/72)
adds a sidecar file that becomes the form the docs lead with. Writing the
argument reference before both land would mean rewriting it.

Until then, `internal/configs/live.go` carries the schema, and the path pages
show the forms in use.

## Everything else is OpenTofu

The language, the CLI, providers, backends: all unmodified. Use
[opentofu.org/docs](https://opentofu.org/docs/).
