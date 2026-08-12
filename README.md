# choudoufu

Plenty of state. None of it binding.
Your config says what should exist, the cloud says what does — no file gets a vote.

choudoufu is a fork of OpenTofu that adds a stateless mode: no state file, no
backend, no lock. Ownership of a resource lives on the resource itself, as
tags, and every plan rebuilds prior state from the live system instead of
trusting a file. Everything else here is stock OpenTofu. The binary is
`choudoufu`.

## See the demo

Paste this to a coding agent (Claude Code or similar) and let it run the
demo end to end.

```
Clone https://github.com/INTENTIUS/choudoufu, then:

1. Confirm Docker is running (`docker info` must succeed).
2. From the repo root, run: bash stateless/e2e/run.sh --expect 5
3. Report each step's result as the script prints it, and the final exit code.

Exit code 0 means every claim the script makes about stateless mode held.
Non-zero means one of them didn't — report which step failed.

This takes about two minutes. It stands up a real estate of resources
against a local AWS emulator, deletes the state file partway through, and
proves the plans stay exact anyway.
```

Prefer to run it yourself: `bash stateless/e2e/run.sh --expect 5` from the
repo root, Docker running.

## Docs

The docs unique to this fork:

- [`website/docs/language/stateless-mode.mdx`](website/docs/language/stateless-mode.mdx) — what stateless mode is and how to use it
- [`stateless/MARKERS.md`](stateless/MARKERS.md) — the ownership tag format, the integration surface for external tooling
- [`stateless/LIMITATIONS.md`](stateless/LIMITATIONS.md) — every construct stateless mode bounds or rejects
- [`stateless/RECEIPTS.md`](stateless/RECEIPTS.md) — recording an effect that leaves nothing in the live system to read back
- [`stateless/e2e/README.md`](stateless/e2e/README.md) — running the demo/test harness, reading its output

These also render as a small docs site at
https://intentius.io/choudoufu/. All stock OpenTofu documentation
lives at
[opentofu.org](https://opentofu.org/docs/).

## Building and testing

```
go build ./cmd/choudoufu
go test ./...
```

The integration tier needs Docker and `TF_FLOCI_TEST=1`.

## License

MPL-2.0. Forked from [opentofu/opentofu](https://github.com/opentofu/opentofu)
at `03743ce6e8`. LICENSE and all copyright headers are unchanged from
upstream.

choudoufu is not affiliated with or endorsed by OpenTofu or the Linux
Foundation. OpenTofu is a registered trademark of the Linux Foundation.
