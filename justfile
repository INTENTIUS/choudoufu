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
    out="$(gofmt -l internal/live cmd site tools live internal/command)"
    if [ -n "$out" ]; then echo "gofmt needed on:"; echo "$out"; exit 1; fi
    echo "==> build"
    go build ./cmd/choudoufu
    echo "==> fast test tier"
    env -u PWD go test ./internal/live/... ./tools/... ./live/ ./cmd/... ./internal/command/
    echo "==> docs site build"
    (cd site && go run . -out public/)
    echo "==> CI steps passed"

# Floci integration tier: needs Docker and the AWS CLI.
test-floci:
    make test-floci

# Issue #64's estate-scale benchmark against floci. ESTATE_BENCH_N=<n> just bench-estate sets the size (default 200).
bench-estate:
    make bench-estate

# The demo: real estate on a local emulator, state file deleted mid-run, plans stay exact. Needs Docker, ~2 minutes, exit 0 = every claim held.
demo:
    bash live/e2e/run.sh --expect 5

# Issue #73's record-backed lifecycle end to end: a record_store declared, the
# four RECORD_ADMITTED types created, re-planned clean, replaced and destroyed.
# No Docker and no AWS - null, time and random are cloud-free providers, so this
# runs against a local directory in well under a minute. It is the only
# end-to-end exercise the record-backed class has, which is why it is a recipe
# rather than a script you have to know about.
demo-records:
    bash live/e2e/record-store/run.sh

# Issue #255's estate-wide tagging sweep end to end: a resource's block
# deleted, the live resource found through ONE Resource Groups Tagging API
# call by the command wiring a user actually runs, and a control run showing
# the per-type fallback cannot see it at all. Needs Docker and the AWS CLI;
# runs on its own port (4601) so it can run beside `just demo`.
demo-tagging-sweep:
    bash live/e2e/tagging-sweep/run.sh

# The create-over-existing defect, end to end and pinned: a needs-discovery
# resource whose type loses its tags on the provider's list path is invisible
# to marker discovery, so a live-plan proposes creating what the estate
# already owns and an apply then creates a second one, once per run. Exit 0
# means the defect is still there; when it goes red the fix has landed and the
# script says which assertions to invert. Needs Docker and the AWS CLI; runs
# on its own port (4602) so it can run beside `just demo`.
demo-create-over:
    bash live/e2e/create-over/run.sh

# Issue #193's managed-argument projection end to end: a data source whose
# argument reads an attribute the resource's own block sets, read against a
# real emulator, with the parameter's live value moved out from under the
# configuration first so a static shortcut cannot pass. Needs Docker and the
# AWS CLI; runs on its own port (4599) so it can run beside `just demo`.
demo-dataread:
    bash live/e2e/dataread-projection/run.sh

# Build the docs site into site/public/. Wipes the directory first, so a
# page removed from the generator stops being served instead of lingering.
#
# Build the docs site into site/public/.
site:
    rm -rf site/public
    cd site && go run . -out public/

# Build the docs site and open it. `just site-serve 8001` picks another port,
# which is what you want when a second checkout or worktree is already serving.
#
# Build the docs site and serve it locally.
site-serve port="8000": site
    @echo "choudoufu docs: http://127.0.0.1:{{port}}/  (serving $(pwd)/site/public)"
    @if lsof -nP -iTCP:{{port}} -sTCP:LISTEN >/dev/null 2>&1; then \
        echo "port {{port}} is already in use - run: just site-serve 8001" >&2; exit 1; \
    fi
    @( sleep 1; command -v open >/dev/null 2>&1 && open "http://127.0.0.1:{{port}}/" ) &
    python3 -m http.server {{port}} --bind 127.0.0.1 --directory site/public

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
    go run ./tools/estate-plan -in {{sweep}}

# Re-plan from a sweep you already have (instant, vs ~2.5min to re-measure).
estate-plan-from sweep:
    go run ./tools/estate-plan -in {{sweep}}

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
# `tables` runs the DERIVED stages only. The five source stages - registry,
# importdocs, tagverbs, survey and logical-schemas - fetch from upstream or
# need a running provider, so re-running one is a deliberate act with a pin
# bump behind it,
# not something a routine regeneration should trigger as a side effect.
# estate-gen is out for the same reason plus its own: it regenerates committed
# fixtures whose acceptance verdicts are a ratchet, and it carries a separate
# provider pin (#137).
# ---------------------------------------------------------------------------

# Regenerate every derived artifact, in dependency order (#133). No network.
tables: mapping row-emit convergence identity-sources survey-render limits harness
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

# Measure the classifier against the shipped table -> rowgen-convergence.json. NOT a coverage metric.
convergence:
    env -u PWD go run ./tools/row-gen -convergence

# Provider schemas -> live/survey.json and live/survey-full.json. Needs the provider.
survey init_bin="terraform":
    env -u PWD go run ./tools/survey-gen -all -init-bin {{init_bin}}

# The committed surveys -> the rendered spans in SURVEY.md, LIMITATIONS.md and COVERAGE.md. No provider, no network.
survey-render:
    env -u PWD go run ./tools/survey-gen -render

# Where the sources describing each type's identity disagree (#106), into
# live/identity-sources.json. No provider, no network.
#
# Compare the sources describing each type's identity -> live/identity-sources.json.
identity-sources:
    go run ./tools/row-gen -sources

# live/LIMITATIONS.md's per-refusal content (#110), from the three refusal
# registries plus the corpus artifact above. No provider, no network.
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
