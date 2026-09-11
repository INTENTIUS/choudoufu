# converge-operator

The replan-and-converge loop, shown on a choudoufu root: `chant operator`
ticking a `ConvergeOp` against a live/choudoufu terraform root, drift
introduced from the AWS CLI, classified against a typed rule table, dispatched
to a gated apply, and the resulting gate proven to hold as a fact on the
`chant/lifecycle` git ledger across a restart of the operator process itself.

`examples/ci-pipelines` shows Ops on forge triggers; `examples/live-mv-workbench`
shows the tag-based split. Neither shows `chant operator` ticking on its own
interval, off git alone, with nothing watching it fire — that is this project's
whole point (issue #1033, refs chant#2314).

This project pins chant **0.68.1**, which carries the fixes for both
upstream findings below (chant#2395, chant#2396) — `examples/ci-pipelines`
still pins 0.63.0, unrelated to this repin.

## The project

One live root, two Ops:

| File | What it is |
|---|---|
| `terraform/main.tf`, `terraform/estate.chdf.hcl` | the estate: two CloudWatch log groups, `app` and `keepalive` |
| `chant.config.ts` | `terraform.binary: "choudoufu"`, one root (`estate`), one environment (`dev`) |
| `src/dev-apply.op.ts` | `TerraformApplyOp`, `gate: "always"` — the only Op that can change the estate |
| `src/dev-converge.op.ts` | `ConvergeOp`, `env: "dev"`, `dial: "apply"` — the loop |

### The estate

Two resources rather than one, and the second (`keepalive`) is never touched
by the demo — it exists purely so the estate is never left with zero live
resources at once. See `terraform/main.tf`'s own file-level comment for why
that matters: deleting the *only* declared resource leaves nothing for
`chant lifecycle snapshot` to read as present, and that command refuses to
snapshot what it calls an unread environment rather than record what looks
like an empty one (quoted in full under "Two upstream findings" below).

### The rule table

`dev-converge.op.ts`'s `ConvergeOp` carries two rules:

```typescript
when<ConvergeSymptom>(gt("createCount", 0), run("dev-apply"), {
  id: "recreate-deleted",
  why: "A declared resource is missing from the live account, most likely " +
       "deleted out of band; re-apply recreates it, converging the account " +
       "back to what dev-apply.op.ts's root declares.",
  flapThreshold: 20,
}),
when<ConvergeSymptom>(gt("adoptCount", 0), report("an undeclared resource is present in the account"), {
  id: "adopt-report",
  why: "An unowned live resource is reported for a human to review and " +
       "adopt by hand; a converge tick never claims ownership on its own.",
}),
```

`recreate-deleted` is the rule this example's own demo run exercises live.
`adopt-report` is declared, build-checked (OPS014 passes with it in the
table, and refuses if it dispatched a mutation instead — see the test suite),
and **not** exercised live in this run — `dev-converge.op.ts`'s own doc
comment explains exactly why `adoptCount` never moved for anything this
example's demo script tried, measured against a live floci while building it.

Both the rule table's own shape and *why* it reads `createCount`/`adoptCount`
rather than chant's own doc example's `status` field are explained at length
in `src/dev-converge.op.ts`'s doc comment — read that file for the full
reasoning; this README states the conclusions.

## Running it

```bash
npm install
npm test              # config/build/lint assertions — no floci needed
bash scripts/demo.sh   # the live run against floci — needs docker
```

`scripts/demo.sh` starts floci (container `wt-1033-floci`, port 4671, the
image `live/floci-image` pins), inits and applies the root, starts
`chant operator`, introduces drift from the AWS CLI, and drives the whole
loop through to convergence — see the verdict lines below for exactly what
it proves, and the script's own header comment for exactly how.

## Two upstream findings, both fixed in chant 0.68.1

Both were found by actually running this example against a real floci while
building it against chant 0.63.0 — not read off the docs. Both were filed
upstream (chant#2395, chant#2396) and are now closed; this project's 0.68.1
pin carries both fixes, so neither needs a workaround any more. The
narrative and verbatim output below are kept as a record of what this
example found and how it was diagnosed, not as a live description of the
example's current behavior.

### Finding 1 — `chant lifecycle plan`/`chant components status --live --json` double-print, and `convergeTick` can't parse it — **chant#2395, fixed in 0.68.1**

Against a live/choudoufu root, `chant lifecycle plan <env> --live --json` and
`chant components status <env> --live --json` print two concatenated
JSON documents on stdout, not one:

```
$ chant lifecycle plan dev --live --json
{
  "estate": "converge-operator-example",
  ...
}

{
  "env": "dev",
  "entries": [ ... ]
}
```

The first is the raw stdout of the `choudoufu live-plan -json` subprocess
chant's terraform lexicon shells out to internally
(`@intentius/chant-lexicon-terraform`'s `describe-resources.ts`, via the
`choudoufuLivePlan` Op *activity*), echoed unconditionally by that activity's
own `report(stdout, stderr)` helper — a helper meant for a human-facing
`chant run` step, reused here for what should have been a silent internal
read. The second is chant's own document.

chant's own `convergeTick` activity (`@intentius/chant`'s
`src/op/activities/converge.ts`, `observeChangeSet`/`observeStatusRows`) did
a bare `JSON.parse(stdout)` on exactly these two commands, with no defense
against this. The exact, reproduced failure, against chant 0.63.0, before
chant#2395 was fixed:

```
$ chant run dev-converge --json
...
{"version":1,"id":"local-...","op":"dev-converge","env":"dev",...,
 "status":"fail","phases":[
   {"name":"Observe","status":"ok",...},
   {"name":"Converge","status":"fail","steps":[
     {"fn":"convergeTick","status":"fail","durationMs":105806,
      "error":"Unexpected non-whitespace character after JSON at position 479 (line 22 column 1)"}
   ]}
 ]}
```

This example used to carry `scripts/chant-json-shim`, a PATH-level wrapper
(never a chant edit) that took the *last* top-level JSON object on stdout
for exactly `lifecycle plan`/`components status --json` and passed every
other subcommand through untouched — installed at `node_modules/.bin/chant`
rather than merely earlier on `$PATH`, because `chant run`'s own `bin/chant`
shells to `npx tsx`, and `npx` re-resolves `chant` from `node_modules/.bin`
for every subprocess `convergeTick` shells out to in turn, which shadows a
plain `$PATH` prefix. chant 0.68.1 no longer echoes the subprocess's stdout
ahead of its own document, so the shim is gone from this project entirely
(`scripts/chant-json-shim` deleted, `scripts/demo.sh` no longer installs
it).

### Finding 2 — `classifyDispatchFailure` checks the wrong field name — **chant#2396, fixed in 0.68.1**

A gated dispatch is completely real: `dev-apply`'s own run genuinely stops at
its gate, exit 3, and a genuine pending fact lands on
`_gates/dev-apply.jsonl` — `chant operator status` and `chant run log
dev-apply` both show it. But against chant 0.63.0, `dev-converge`'s own tick
record called the outcome `"reported"` rather than `"gated"`, with an empty
reason:

```
$ chant operator log --op dev-converge
...  converge(dev): drifted=0 remediated=0 reported=1 skipped-budget=0 skipped-flap=0 gated=0 unobserved=5 adopted=0
```

```json
[{"action":"reported","op":"dev-apply","reason":"dispatch of \"dev-apply\" failed: ","ruleId":"recreate-deleted"}]
```

The cause: chant's own `classifyDispatchFailure`
(`@intentius/chant`'s `src/op/activities/converge.ts`) read a dispatched
op's `--json` record looking for `parsed.gate?.gate`, but a gated
`TerraformApplyOp` run's own record carries `gate: { name, since }` — the
field is `gate.name`:

```json
{"version":1, ..., "status":"gated", ...,
 "gate":{"name":"approve-dev-apply","since":"2026-09-11T05:39:16.058Z"},
 "approve":"chant approve dev-apply approve-dev-apply"}
```

`typeof parsed.gate?.gate === "string"` was `typeof undefined === "string"` —
always false — so the check fell through to a regex fallback
(`/is gated on "([^"]+)"/`) that also never matched, because that phrase
belongs to the human-readable render `--json` mode suppresses. The result:
every `run()` dispatch this rule table (or any `ConvergeOp`'s) made to a
gated `TerraformApplyOp` was misclassified as an ordinary failure.

This example did not work around it at the time: patching a dispatched op's
own `--json` output to add a `gate.gate` alias would have been plausible and
narrow, but it would have meant this example silently repairing chant's own
dispatch-classification logic rather than exercising it. Instead this was
filed as chant#2396 and fixed upstream — `classifyDispatchFailure` now reads
`parsed.gate?.name`, keeping the regex fallback for human-mode output. On
this project's 0.68.1 pin, `dev-converge`'s own tick summary reads
`gated=1` for a genuinely gated dispatch, matching what `chant operator
status`/`chant run log dev-apply` already showed independently — see the
verdict lines below. One practical consequence this example ran into and
still works around on its own side: since the ledger's `firedRuleIds` — not
the outcome — drives flap-damping, a rule stuck at an unresolved gate for
more than the default `flapThreshold` (3) of its own ticks reads as "never
clearing" and stops dispatching (`skipped-flap`) regardless of the gate;
`recreate-deleted` raises its own `flapThreshold` to 20 so this demo's own
scripted approval delay doesn't race chant's default (see the Op's doc
comment).

## The verdict lines

One clean run of `bash scripts/demo.sh`, in order (20/20):

```
VERDICT stage=baseline-gate verdict=pass status=gated
VERDICT stage=baseline-approve verdict=pass status=resolved
VERDICT stage=baseline-apply verdict=pass status=ok
VERDICT stage=operator-started verdict=pass status=running
VERDICT stage=observe-clean verdict=pass status=drifted=0
VERDICT stage=drift-deleted verdict=pass status=deleted
VERDICT stage=drift-confirmed-absent verdict=pass status=absent
VERDICT stage=operator-ticked-after-drift verdict=pass status=running
VERDICT stage=classify-fired verdict=pass status=fired
VERDICT stage=gate-pending verdict=pass status=pending
VERDICT stage=gate-run-status verdict=pass status=gated
VERDICT stage=operator-stopped verdict=pass status=stopped
VERDICT stage=gate-held-no-daemon verdict=pass status=unchanged
VERDICT stage=operator-restarted verdict=pass status=running
VERDICT stage=gate-held-after-restart verdict=pass status=same-fact
VERDICT stage=gate-still-pending-after-restart verdict=pass status=pending
VERDICT stage=approve verdict=pass status=resolved
VERDICT stage=operator-ticked-after-approve verdict=pass status=running
VERDICT stage=converged verdict=pass status=recreated
VERDICT stage=apply-run-status verdict=pass status=ok
DEMO total pass=20 fail=0
```

Reading them against the issue's own ask:

- `observe-clean` is the first tick, before any drift: `chant operator log`
  reads `converge(dev): drifted=0 remediated=0 reported=0 skipped-budget=0
  skipped-flap=0 gated=0 unobserved=5 adopted=0`.
- `classify-fired` is the next tick after the CLI deletes `app`: its
  `firedRuleIds`/outcomes name `recreate-deleted`.
- `gate-run-status` is the dispatch itself: `chant run log dev-apply`'s
  newest row reads `gated`, read independently of the tick's own summary —
  which, on this project's 0.68.1 pin (chant#2396 fixed), agrees: `chant
  operator log --op dev-converge` now reads `gated=1` for this tick rather
  than the `reported=1`/`gated=0` chant 0.63.0 quoted above.
- `gate-pending` is the gate: `chant operator status` shows
  `dev-apply gate "approve-dev-apply"` pending, with its `expires:` line.
- `gate-held-no-daemon` and `gate-held-after-restart` are the restart proof:
  the same fact, read with the operator process not even running, and then
  the same fact, unchanged, after a fresh `chant operator` process starts
  and ticks at least once. Both compare the pending gate's own `expires:`
  line byte-for-byte across the kill/restart, proving it is a row on
  `chant/lifecycle`, not state held by the process that found it.
- `approve` is `chant approve dev-apply approve-dev-apply` resolving the
  plan digest the gate was standing on.
- `converged` and `apply-run-status` are the next tick applying for real:
  `dev-apply`'s newest run reads `ok`, and `app` exists again.

## What this example does not do

`adopt-report` is declared and build-checked (`npm test`'s OPS014 red/green
proof covers the same lint machinery it would be caught by) but never fires
in `scripts/demo.sh` — see `dev-converge.op.ts`'s doc comment for exactly
what was tried against a live floci and why `adoptCount` never moved. The
issue names a tag edit as an alternative way to introduce drift; only a
deleted resource moves a count a live root's `ConvergeSymptom` can actually
see, for reasons that doc comment also covers, including what a live root's
own observation channel can and cannot report.

Both upstream findings above were fixed in chant itself (chant#2395,
chant#2396), never patched around from this project's side — this project's
own `scripts/`/`node_modules/.bin` carries no workaround for either any
more; `scripts/chant-json-shim` is gone, and `node_modules/@intentius/chant`
is never touched by this project regardless. And the `chant/lifecycle`
ledger this example writes never reaches a remote: `scripts/demo.sh` builds
its own throwaway git repository with no remote configured, the same reason
`examples/ci-pipelines/scripts/smoke.sh` does — see that script's own doc
comment, quoted in this project's `scripts/demo.sh` too.

## Pinning

`package.json` pins `@intentius/chant` and `@intentius/chant-lexicon-terraform`
to `0.68.1`, which carries the fixes for chant#2395 and chant#2396 (closed);
`examples/ci-pipelines` still pins `0.63.0`, unaffected by this repin.
`@cdktf/hcl2json` is a `devDependency` here (not a chant dependency — chant's
own `terraform/parse.ts` deliberately does not carry the ~1.8MB wasm blob it
needs) because without it `chant build` cannot parse `terraform/*.tf` into
entities at all: `chant.build` warns `Terraform carve-out needs the HCL
parser, which is not installed` and every terraform entity in the project
silently fails to build — measured while building this example, and the
same install line chant's own warning names.
