# Handoff

Written 2026-08-15, wound down early, at `f06bd30165` plus this file.
Working tree clean, the fast tier (`just ci`) green, everything pushed.
Read `.claude/agents/live-markers.md` before touching anything. The work
lives in the issue tracker (`gh issue list -R INTENTIUS/choudoufu` - a
bare `gh` hits opentofu/opentofu). Do not copy issue content into this
file.

## The finish line, and how to steer by it

The product is done when a person with an existing OpenTofu
configuration runs it under live markers and it works. That end state
is measurable, and every number below is the distance left:

- **The corpus ladder** (`live/corpus-refusals.json`): 145 real
  published estates, today 0 clean / 11 backend-only / 122
  language-blocked. Finish means the language-blocked class is empty
  and "backend-only" - the documented one-line onboarding edit - is the
  worst ordinary case. This is THE number; everything else serves it.
- **Type parity** (maintainer ruling, in the brief): every rejection,
  needs-hand-separator bucket, evidence-only bucket and fixture gap is
  debt with an obligation attached. Finish means
  `live/rowgen-buckets.json`'s hand and evidence buckets read zero and
  `tools/row-gen/rejected.json` holds only the credential class.
- **The acceptance tier** (`live/cohort-acceptance.json`): finish means
  31 of 31 cohorts round-trip - apply, delete state, replan from
  markers, empty plan. Failures split emulator/fixture; only the
  fixture half is this repo's debt.

When choosing between two pieces of work, take the one that moves an
estate up the ladder or retires parity debt, in that order. The
language wall is 122 of 145 estates; nothing else comes close. Do not
spend a session raising convergence or polishing generators unless a
ladder or parity number moves at the end of it - and recompute that
number, do not predict it.

## What is already done - do not redo it

All four of the 2026-08-15 maintainer decisions on #175 are executed
and pushed (commits `e2d2f935..f06bd301`; the closing session comment
on #175 has the detail):

1. The 29-type PROPOSE batch is ratified, `aws_appstream_directory_config`
   is rejected on the credential ground, and the five unbuildable
   fixtures are recorded in `fixtureGaps` (tools/estate-gen/drift_test.go).
2. Both ledger vetoes are reversed with the work done, not waived:
   `aws_ecs_service`, `aws_cloudwatch_event_rule`,
   `aws_cloudwatch_event_target` and `aws_lambda_permission` all carry
   full rows. The omitted-bus fallback is built as vocabulary
   (`Component.Default`, scraped by importdocs-gen into
   `omitted_fallbacks`, derived by row-gen) - do not add per-type
   fallback hacks; the vocabulary exists.
3. #177's importdocs-gen half is closed on the issue: dropped
   references are recorded, no-Import pages keep their examples,
   element indexes and the list case exist. Its estate-gen half is NOT
   done (below).
4. The floci image is fixed and published:
   `ghcr.io/lex00/floci@sha256:da6298c1...` (lex00/floci#50 has the
   fork agent's report). `live/floci-image` is bumped, the capability
   manifest carries the new digest including four re-probed hand rows.
   The estate fixture pin and acceptance providerPin are both 6.59.0.

## The one in-flight obligation, first

`live/cohort-acceptance.json` still records the OLD image. That lag is
a recorded staleness entry in `live/flociimage_test.go`
(`staleFlociMeasurements`), not an accident. Retire it:

```
TF_FLOCI_TEST=1 TF_FLOCI_ACCEPTANCE_ARTIFACT=1 env -u PWD \
  go test ./internal/live/acceptance -run TestCohortAcceptance -count=1 -v -timeout 6h \
  > /tmp/acceptance.log 2>&1; echo "exit: $?"
```

Needs Docker. Takes an hour or more; run it in the background and DO
NOT touch anything under `live/e2e/estates/` while it runs - it reads
the fixtures as it goes. A cohort's verdict is its
`name: pass/fail (phase ...)` log line, never the subtest checkmark.
The committed artifact is a ratchet: s3, aps and media must still pass;
expect recoveries in databases, dynamodb-elasticache, iam-ecr, rds and
streaming (the five daemonless-waiter fixes) and in apigateway (the
CreateApiKey crash fix). When it finishes: commit the artifact, delete
the `cohort-acceptance.json` entry from `staleFlociMeasurements`,
re-split the remaining failures emulator/fixture, and post the split to
lex00/floci#50. The doubled-slash cohorts (iot, apigateway v2 routes,
storage/Backup, ai-location/Bedrock) will still fail - those operations
are absent from floci, measured, not a routing bug; they are the next
fork work queue item.

## Then, in order

1. **#177's estate-gen half.** The recorded references
   (`example_arguments` entries with `"reference": true`) exist so
   estate-gen can create an incomplete block iff every dropped member
   wires to a rendered (or one-hop supporting) sibling - #174's
   complementary move, spelled in #177's body. The seed currently skips
   reference and element>0 entries (tools/estate-gen/seed.go, the
   `writable` filter); build the move, then bulk-regenerate:

   ```
   ESTATE_REGEN_ALL=1 TF_FLOCI_TEST=1 env -u PWD \
     go test -count=1 -timeout 30m ./tools/estate-gen -run TestRegenerateAllCohorts
   ```

   then validate every cohort (loop `terraform init -backend=false`
   + `terraform validate` per dir - ALWAYS with
   `TF_PLUGIN_CACHE_DIR=~/.terraform.d/plugin-cache` exported and
   `rm -rf .terraform .terraform.lock.hcl` after each, see traps),
   then measure retirements:

   ```
   ESTATE_RETIRE_MEASURE=1 env -u PWD go test -count=1 ./tools/estate-gen -run TestMeasureOverrideRetirements -v
   ```

   Retire what measures byte-identical (#136's bar; the maintainer
   holds it strictly). `aws_dms_s3_endpoint` measures retirable and
   stays - cohort-composition ruling on #136.

2. **Open the language-wall campaign issue.** The largest product
   number in the tree and still un-filed: 122 of 145 real published
   estates are language-blocked (`live/corpus-refusals.json`, the
   `ladder` object; per-estate profiles are in the same artifact).
   Dynamic-value-in-static-context alone dominates. The issue should
   carry the per-refusal breakdown from the ladder profiles and slice
   by REFUSAL RULE, never by estate or resource type.

3. **The demand head, as ratification batches.** From the regenerated
   demand table: `aws_s3_bucket_replication_configuration` (16
   estates), `aws_s3_bucket_object_lock_configuration` (11),
   `aws_security_group_rule` (11). security_group_rule needs a
   maintainer ruling first - provider-discouraged idiom whose successor
   types are admitted; the choice (admit vs name-the-successor in the
   refusal) is posed in #175's first comment.
   `aws_secretsmanager_secret_version` and `aws_iam_access_key` are the
   credential class: never admit, the refusal already stands.

#154 strictly last (maintainer). Open issues: #136 (continuing), #150
(tracker), #154, #175 (execution done - it can close once the
acceptance re-measure lands), #177 (estate-gen half).

## How a row lands (the mechanics nobody wrote down)

- Paste new rows anywhere valid inside the two generated maps
  (`internal/live/identity/table_generated.go` DefaultTable,
  `internal/live/lint/admission_generated.go` admittedTypesV0), then
  `env -u PWD go run ./tools/row-gen -emit` - it rewrites both files
  canonically from the compiled table. Run it TWICE; the second run
  must produce zero diff (the fixed point). The `-propose` output's
  "paste into admission_cohort_<cohort>.go" instruction is stale
  pre-#96 text; those files no longer exist.
- A hand-shaped row (one the classifier cannot reproduce) needs a
  ruling in `tools/row-gen/annotations.json` with reason, evidence and
  exit, or `-emit` refuses. Each such row also needs a reviewed +1 on
  `annotationCountRatchetMax` (tools/row-gen/convergence_test.go),
  with the reason in the comment above the constant.
- Every admitted type needs a taggability pin in a
  `internal/live/stamp/stamp_cohort_*_test.go` file, taken from
  `live/survey-full.json` `signals.taggable` - never guessed.
- Then: cohort roster regen (the `-types` list in the cohort's
  GENERATED.md is the recorded command), `just tables`, `just corpus`,
  `just survey-render`, `just limits`, full fast tier.

## Traps confirmed this session, worth not relearning

- `env -u PWD go test ...` always (symlink trap). `just ci > log;
  echo $?` then read the log - never through a pipe.
- Never pipe a generator into `head`; SIGPIPE kills it before it
  writes and it looks like a no-change run.
- **Disk**: per-cohort `terraform init` copies the ~700MB AWS provider.
  31 cohorts once filled the disk to under 1GB and wedged Docker.
  Export `TF_PLUGIN_CACHE_DIR=~/.terraform.d/plugin-cache` before any
  validate loop and delete each `.terraform` dir after. If Docker
  wedges: the app is `Docker.app` (not "Docker Desktop"), quit, pkill
  com.docker.backend, `open -a Docker`.
- A type override in `tools/estate-gen/overrides_cohort_*.go`
  DISPLACES the doc-example seeding for its whole type - fold any
  example values the type still needs into the override's Apply.
- An identity-bound sibling reference may only land on an attribute in
  the target's own `IdentityAttrs`, and only when that identity IS the
  wanted value; otherwise pair by matching seed literals (the
  observability event_rule/event_target overrides are the worked
  example; `55856b4473` is the rule commit).
- A background acceptance run reads the working tree as it goes; never
  mutate fixtures under a live measurement. Crash cohorts'
  failed_resources lists are nondeterministic; do not diff them.
- `live/rowgen-convergence.json` is NOT coverage. Do not quote it,
  rank work by it, or report progress in it. The user-facing numbers
  are the corpus ladder classes.
- Agents that stop "waiting on background work": send them a message
  to finish from what they already have.

## Instruments, all regenerated and consistent at this commit

`live/corpus-refusals.json` (0 clean / 11 backend-only / 1
remote-state / 10 admissions-only / 122 language-blocked / 1 unreadable
of 145; demand table regenerates inside it), `live/rowgen-buckets.json`
(360 client-named / 106 composite / 9 assembled / 82 needs-hand-sep /
33 evidence-only), `live/rowgen-convergence.json` (876 types, 747
reproduced / 129 ruled), `live/floci-capabilities.json` (fresh for
sha256:da6298c1, four hand rows re-probed), `live/import-grammar.json`
(post-#177 scrape). `live/cohort-acceptance.json` is the one stale
artifact, by recorded decision (see the in-flight section). Recompute
before quoting any of them.
