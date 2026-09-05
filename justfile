# choudoufu build targets. `just --list` shows everything.

# Build the choudoufu binary into the current directory
build:
    go build ./cmd/choudoufu

# Unit tests. Integration tiers skip unless their env vars are set.
test:
    go test ./...

# Exactly what .github/workflows/ci.yml runs, in order, so a red main is
# something you find here rather than on GitHub. `env -u PWD` is needed for
# the test step and only locally: /Users/alex/checkouts is a symlink and
# os.Getwd() honours PWD, which the Linux runner does not have to care about.
# TestCIRunsEveryForkOwnedTestPackage (live/ci_coverage_test.go) keeps the
# package list here and in the workflow from drifting apart.
#
# Run exactly what CI runs, in order, before pushing.
ci:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "==> gofmt (fork-owned packages)"
    out="$(gofmt -l internal/live cmd site tools live internal/backend internal/command internal/configs internal/engine/applying internal/plans internal/plugin internal/plugin6 internal/tofu)"
    if [ -n "$out" ]; then echo "gofmt needed on:"; echo "$out"; exit 1; fi
    echo "==> build"
    go build ./cmd/choudoufu
    echo "==> fast test tier"
    env -u PWD go test ./internal/live/... ./tools/... ./live/ ./cmd/... ./internal/command/ ./internal/command/arguments/ ./internal/command/views/ ./internal/command/e2etest/ ./internal/engine/applying/ ./internal/tofu/... ./internal/backend/local/ ./internal/configs/ ./internal/plans/ ./internal/plugin/ ./internal/plugin6/
    echo "==> docs site build"
    cp live/iam-reference.json site/data/iamref.json
    (cd site && hugo --minify --quiet)
    echo "==> CI steps passed"

# Check whether background subagents (dispatched via the Agent tool) are
# still writing, without reading their full transcripts into context.
# Usage: just agent-progress <task-id> [task-id...]
agent-progress *ids:
    bash .claude/scripts/agent-progress.sh {{ids}}

# Floci integration tier: needs Docker and the AWS CLI.
test-floci:
    make test-floci

# Issue #64's estate-scale benchmark against floci. ESTATE_BENCH_N=<n> just bench-estate sets the size (default 200).
bench-estate:
    make bench-estate

# The demo: real estate on a local emulator, state file deleted mid-run, plans stay exact. Needs Docker, ~2 minutes, exit 0 = every claim held.
demo:
    bash live/e2e/run.sh --expect 5

# The smoke stack (issue #713): docker compose (pinned floci + the stock
# opentofu oracle), scenario-per-invocation, versioned. `just smoke` lists
# scenarios; BREAK=1 proves a scenario's assertions can fail;
# SMOKE_INSTRUMENT=1 adds the terralith-style request counters;
# CHOUDOUFU_VERSION=vX.Y.Z runs a pinned release instead of source.
smoke scenario="":
    bash live/smoke/smoke.sh {{scenario}}

# One recipe for every named e2e demo: `just demo-run corpus-vpc-complete`
# runs live/e2e/corpus-vpc-complete/run.sh. This replaced ~54 hand-cloned
# demo-<name> recipes (issue #700), so adding an estate touches zero
# justfile lines; each retired recipe's comment moved into its estate's
# own run.sh header. The name list is `ls live/e2e/*/run.sh`.
demo-run name:
    @test -x "live/e2e/{{name}}/run.sh" || { echo "no live/e2e/{{name}}/run.sh - names: $(ls -d live/e2e/*/run.sh 2>/dev/null | sed 's|live/e2e/||;s|/run.sh||' | tr '\n' ' ')" >&2; exit 1; }
    bash live/e2e/{{name}}/run.sh

# Build the docs site into site/public/. Copies the build-time data inputs
# (site/data/gauntlet.json is committed by tools/gauntlet; iamref.json is
# not, so it's refreshed from live/iam-reference.json here) then runs Hugo.
#
# Build the docs site into site/public/.
site:
    cp live/iam-reference.json site/data/iamref.json
    rm -rf site/public
    cd site && hugo --minify

# Build the docs site and serve it locally with live reload. `just
# site-serve 8001` picks another port, which is what you want when a second
# checkout or worktree is already serving.
site-serve port="8000":
    cp live/iam-reference.json site/data/iamref.json
    cd site && hugo server --bind 127.0.0.1 --port {{port}} --openBrowser

# Lint exactly as upstream CI would (golangci-lint, both GOOS passes)
lint:
    make golangci-lint

# The estate work plan: which estate to onboard next, and what blocks it.
#
# This is the assignment rule. Work is picked per ESTATE, fewest blockers
# first - never per refusal class. A day spent clearing classes moved 1570
# sites and zero estates, because the median blocked estate carries about two
# blockers and clearing one of them leaves it blocked.
estate-plan sweep="/tmp/choudoufu-sweep.json":
    go run ./tools/refusal-probe -schemas -out {{sweep}}
    go run ./tools/estate-plan -in {{sweep}} -schemas

# Re-plan from a sweep you already have (instant, vs ~2.5min to re-measure).
estate-plan-from sweep:
    go run ./tools/estate-plan -in {{sweep}}

# How much of the wall is the estate not having been onboarded.
#
# Everything else here measures the ADOPTION question - can a stranger's
# published configuration be taken over exactly as it stands - because every
# corpus entry is somebody else's published configuration and not one of the
# 250 declares a live block or a record_store. The primary goal is the other
# thing: someone writes ordinary Terraform, adds a live block, applies, and
# the fork manages it with no state file.
#
# This measures both forms of every entry in one sweep. internal/live/onboard
# computes the edit - a live sidecar declaring record_store "local", and the
# backend or cloud block removed - in memory, so nothing is written into
# .corpus, which is shared by every worktree.
#
# It is offline: check.Analyze over edited text, and nothing more. An estate
# reading "cleared by onboarding" has cleared the offline gate, not the real
# one; live/e2e is where "applies, loses its state file, replans empty" is
# still proved one estate at a time.
#
# ~3 min warm. -schemas is not optional: identity.LocatedType fails closed
# without them, so markerless-type reads as surviving onboarding when a
# record_store answers it.
#
# Both forms of every corpus entry: what onboarding clears, and what it does not.
onboarding-gap sweep="/tmp/choudoufu-onboarded.json":
    go run ./tools/refusal-probe -schemas -onboarded -quiet -out {{sweep}}

# Fetch the third-party corpus pinned in live/corpus-manifest.json into .corpus/
# (gitignored), and install each entry's registry modules into its own
# .terraform/modules. Needs network; run once.
#
# The module half is #254. internal/live/check.Load resolves a non-local module
# source through .terraform/modules, exactly as a real user's directory has it
# after init, and nothing here ever created that directory - so 58 of the 250
# entries were measured with a hole where their modules should be, and two
# refusal classes read as zero because the code that trips them was never
# loaded. Registry versions are frozen in live/corpus-module-pins.json so the
# corpus does not float with whatever a module author published today; commit
# that file whenever this run changes it. Go-getter sources (github.com/org/repo)
# are installed and then dropped, because 133 of the corpus's 134 such calls
# carry no ref and there is nothing to pin them to; -remote-modules keeps them
# and gives up reproducibility to do it.
corpus-fetch:
    go run ./tools/corpus-fetch

# The config-language scoreboard (#102): rank which refusals fire across the corpus, into live/corpus-refusals.json. Run corpus-fetch first. No cloud.
#
# The schema flags are not optional decoration. Without them every resource
# type absent from the generated admission table reads as refused, so
# unadmitted-type tops the ranking for a reason belonging to the run rather
# than to the corpus - the single outcome #102 exists to prevent. This recipe
# omitted them, so `just corpus` produced a worse artifact than the one
# committed, which is how a regeneration command silently stops reproducing
# its own output. Provider install needs network once; after that the plugin
# cache serves it.
#
# Rank which refusals fire across the corpus -> live/corpus-refusals.json.
corpus init_bin="terraform":
    go run ./tools/corpus-gen -init-bin {{init_bin}}

# ---------------------------------------------------------------------------
# The generation pipeline (#133). Stages in dependency order; each recipe's
# comment ends with the one-line summary `just --list` shows.
#
# Two rules these recipes exist to encode:
#   - Never pipe a generator into `head`. SIGPIPE kills it before it writes,
#     and the run looks exactly like one that produced no change.
#   - A regenerated artifact IS the measurement. Regenerate, then read the
#     diff; do not reason about what should have moved.
#
# `just tables` on a clean tree must produce no diff. If it does, either a
# recipe is wrong or an artifact was already stale - both worth finding.
#
# `tables` runs the DERIVED stages only. The six source stages - registry,
# importdocs, tagverbs, survey, logical-schemas and wo-sweep - fetch from
# upstream or need a running provider, so re-running one is a deliberate act
# with a pin bump behind it,
# not something a routine regeneration should trigger as a side effect.
# estate-gen is out for the same reason plus its own: it regenerates committed
# fixtures whose acceptance verdicts are a ratchet, and it carries a separate
# provider pin (#137).
# ---------------------------------------------------------------------------

# Regenerate every derived artifact, in dependency order (#133). No network.
tables: mapping row-emit convergence identity-sources survey-render tagverbs-render limits harness toggles
    @git status --porcelain || true

# CloudFormation Registry schemas -> live/registry.json + its embedded copy. Network on a cold cache.
registry:
    env -u PWD go run ./tools/registry-gen
    cp live/registry.json internal/live/registry/registry.json

# registry.json + overlay.json + overlay.d/*.json -> live/mapping.json + its embedded copy.
mapping:
    env -u PWD go run ./tools/mapping-gen
    cp live/mapping.json internal/live/registry/mapping.json

# Provider doc pages -> live/import-grammar.json. Offline: the doc cache is complete.
importdocs:
    env -u PWD go run ./tools/importdocs-gen

# AWS Service Authorization Reference -> live/iam-reference.json (#152). Network on a cold cache.
iamref:
    env -u PWD go run ./tools/iamref-gen

# botocore -> live/tag-verbs.json and reference.md's tagging-verb span.
tagverbs:
    env -u PWD go run ./tools/tagverbs-gen

# The committed live/tag-verbs.json -> reference.md's tagging-verb spans (#421). No network.
tagverbs-render:
    env -u PWD go run ./tools/tagverbs-gen -render

# internal/live/strict.Toggles (registry.go, #365) -> reference.md's strict
# block toggle table. No network, no artifact - rendered straight from the
# already-committed Go source, so it is a derived stage like the rest of
# `tables` rather than a fetch like tagverbs above it.
toggles:
    env -u PWD go run ./tools/toggles-gen

# The record-store effects providers' own schemas -> live/logical-schemas.json,
# the evidence every RecordBacked row is derived from (see
# tools/row-gen/logicalschemas.go). A source stage like `survey`, not a derived
# one: it launches five providers, so it is out of `tables` for the same reason
# `survey` is. All five are small and cache like any other provider.
logical-schemas init_bin="terraform":
    env -u PWD go run ./tools/row-gen -logical-schemas -init-bin {{init_bin}}

# mapping + registry + import-grammar + logical-schemas + the ratified rows -> the two generated tables (a fixed point; see emit.go).
row-emit:
    env -u PWD go run ./tools/row-gen -emit

# hashicorp/aws's own schema, walked for WriteOnly and Sensitive+settable
# arguments -> live/wo-sweep.json, tools/limits-gen's source for the
# "Attribute-level residue" section's figures (#126). A source stage like
# `survey`: it launches the provider, so it is out of `tables`. The plugin
# cache serves it after the first run.
wo-sweep init_bin="terraform":
    env -u PWD go run ./tools/wo-sweep -init-bin {{init_bin}} > live/wo-sweep.json

# Measure the classifier against the shipped table -> rowgen-convergence.json. NOT a coverage metric.
convergence:
    env -u PWD go run ./tools/row-gen -convergence

# Provider schemas -> live/survey.json and live/survey-full.json. Needs the provider.
survey init_bin="terraform":
    env -u PWD go run ./tools/survey-gen -all -init-bin {{init_bin}}

# The committed surveys -> the rendered spans in SURVEY.md, LIMITATIONS.md, MARKERS.md and COVERAGE.md. No provider, no network.
survey-render:
    env -u PWD go run ./tools/survey-gen -render

# Issue #418's partition: live/survey-full.json + mapping + rejected.json ->
# live/readiness.json, every provider type tiered and statused. No provider,
# no network - reads only already-committed artifacts, so it belongs after
# `survey` in the pipeline but needs no provider itself.
readiness:
    env -u PWD go run ./tools/readiness-gen

# The committed live/readiness.json -> COVERAGE.md's and the docs site's
# readiness-tiers/readiness-types spans. No provider, no network.
readiness-render:
    env -u PWD go run ./tools/readiness-gen -render

# Issue #441: re-run survey-gen and row-gen at VERSION (a hashicorp/aws
# release), regenerate live/readiness.json, and print the movement report a
# provider bump's PR should carry - types added/removed, tier movement, the
# #387 schema-precedence delta (rowgen-convergence.json's schema_reproduces),
# the ratified-row convergence headline, and whether the golden identity
# table moved. This is a report, not an event: nothing here bumps
# internal/live/pins.AWSProviderVersion or commits anything, and
# live/pins_drift_test.go stays red on the regenerated artifacts until a
# human bumps that constant too and commits deliberately - review the printed
# report, then decide.
#
# A dry run against the pin already at its current value - `just
# provider-bump 6.59.0` while internal/live/pins.AWSProviderVersion says
# 6.59.0 - exercises the whole pipeline for real (a real provider fetch, a
# real classification pass) and has to report zero movement, since nothing
# changed: that is the self-test that the override plumbing itself works,
# with no network access to a hypothetically newer release required to prove
# it. Needs the provider (network on a cold cache; the plugin cache serves it
# after the first run).
provider-bump version init_bin="terraform":
    env -u PWD go run ./tools/survey-gen -all -provider-version {{version}} -init-bin {{init_bin}}
    env -u PWD go run ./tools/readiness-gen
    env -u PWD go run ./tools/row-gen -convergence
    env -u PWD go run ./tools/provider-bump-report

# Where the sources describing each type's identity disagree (#106), into
# live/identity-sources.json. No provider, no network.
#
# Compare the sources describing each type's identity -> live/identity-sources.json.
identity-sources:
    go run ./tools/row-gen -sources

# Which markerless types with a documented composite import can have their
# exported `id` recorded whole (#337), into live/composite-import-roster.json.
# Reads live/survey-full.json and live/import-grammar.json. No provider, no
# network.
composite-import:
    go run ./tools/row-gen -composite-import

# live/LIMITATIONS.md's per-refusal content (#110), from the three refusal
# registries, the corpus artifact above, and live/wo-sweep.json's residue
# figures (#126). No provider, no network: all three inputs are committed.
#
# Render live/LIMITATIONS.md's per-refusal spans from the refusal registries.
limits:
    go run ./tools/limits-gen

# live/HARNESS.md's two registries: what the fork is driving down, and what it
# believes while it does. Runs every measurement and every assumption check, so
# a successful run is also a run in which the whole harness held. Last in
# `tables` because it reads what the other stages write. No provider, no
# network, well under a second.
#
# Render the burndown and assumptions registries -> live/HARNESS.md.
harness:
    go run ./tools/harness-gen

# Will this configuration work under live markers? (#114) DIR defaults to "."
live-check dir=".":
    go run ./cmd/choudoufu live-check {{dir}}

# ── The gauntlet ─────────────────────────────────────────────────────────
# live/GAUNTLET.md is the contract; tools/gauntlet renders it. The runner
# executes crossing scripts against the pinned emulator side by side with
# stock, records each stage's verdict into live/gauntlet.json, and regenerates
# the spec and the site's progress pages. `just gauntlet` is what CI runs
# nightly; `just gauntlet-run <name>` is one estate.
#
# The stock terraform/tofu on PATH is the ORACLE - live/oracle-versions.json
# pins which release, and CI's setup-terraform/setup-opentofu steps install
# exactly that (issue #544). Locally, nothing installs it for you: check
# `terraform version` / `tofu version` against that file before trusting a
# local verdict against a CI one, especially a failure that looks like a
# schema disagreement rather than a real regression - #498 was exactly a
# local terraform silently a release behind CI's. Every run - local or CI -
# records what it actually found on PATH into last_run.oracle regardless,
# so a rendered estate page (or live/gauntlet.json directly) says which
# oracle produced its verdict rather than assuming the pin was honoured.

# Run the core set (Docker, the AWS CLI and a stock terraform on PATH - see
# live/oracle-versions.json for which release CI pins).
gauntlet:
    env -u PWD go run ./tools/gauntlet run -set core

# Run one or more estates by name.
gauntlet-run +names:
    env -u PWD go run ./tools/gauntlet run {{names}}

# Regenerate the artifact, live/GAUNTLET.md and the site's progress pages.
gauntlet-render:
    env -u PWD go run ./tools/gauntlet render

# Add an estate: writes the manifest entry and a script stub, then renders.
# Example: just gauntlet-add corpus-vpc-minimal https://github.com/x/y v1.2.3 terraform-popular "x/y examples/minimal (tag v1.2.3)"
gauntlet-add name url ref lane source:
    env -u PWD go run ./tools/gauntlet add {{name}} {{url}} {{ref}} -lane {{lane}} -source "{{source}}"

# Snapshot the artifact for a release: live/history/<version>.json.
gauntlet-snapshot version:
    env -u PWD go run ./tools/gauntlet snapshot {{version}}

# Release-highlights markdown from a snapshot diff, paste-ready for a GitHub
# release body. Example: just gauntlet-notes live/history/v0.3.0.json live/history/v0.4.0.json
gauntlet-notes old new:
    env -u PWD go run ./tools/gauntlet notes {{old}} {{new}}

# Issue #544: the stock terraform/tofu binaries every stage compares
# choudoufu's plan against are the ORACLE - an unpinned oracle silently
# changes what "matches stock" means (#498's root cause). Both are pinned at
# live/oracle-versions.json, the single place .github/workflows/gauntlet.yml
# and .github/workflows/contribute.yml both read.
#
# Bumping the pin is a reviewed event, the same shape #441 built for a
# provider bump (re-measure, emit a movement report, land the report with
# the change): hand-edit live/oracle-versions.json to the new version(s)
# first, then run this recipe, which re-runs the gauntlet against the new
# pin and prints what moved - the pin itself, each set's clear/estate
# counts, which estates' stage verdicts or clear flag changed, and which
# rows' last_run.oracle actually reflects the new pin versus which this
# run's -set did not touch. This is a report, not an event: nothing here
# commits anything; review the printed report (paste it into the PR body,
# the same way a provider-bump PR does), then commit
# live/oracle-versions.json together with the regenerated live/gauntlet.json.
#
# A dry run with live/oracle-versions.json unchanged - `just oracle-bump`
# against the pin already in place - exercises the whole pipeline for real
# (a real gauntlet run) and has to report zero movement, since nothing
# changed: the self-test that the plumbing itself works, the same way
# `just provider-bump <version already pinned>` does for a provider bump.
oracle-bump set="all":
    env -u PWD go run ./tools/gauntlet run -set {{set}}
    env -u PWD go run ./tools/oracle-bump-report

# One gauntlet worker run under your own key: picks the next unit, makes a
# worktree, runs Claude Code headless under .claude/agents/gauntlet-worker.md,
# opens a pull request. Never merges. `just contribute 10` caps spend at $10.
contribute max_usd="25":
    bash scripts/contribute.sh {{max_usd}}
