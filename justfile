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
ci:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "==> gofmt (fork-owned packages)"
    out="$(gofmt -l internal/live cmd site tools live)"
    if [ -n "$out" ]; then echo "gofmt needed on:"; echo "$out"; exit 1; fi
    echo "==> build"
    go build ./cmd/choudoufu
    echo "==> fast test tier"
    env -u PWD go test ./internal/live/... ./tools/... ./live/ ./cmd/...
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

# Build the docs site into site/public/. Wipes the directory first, so a
# page removed from the generator stops being served instead of lingering.
site:
    rm -rf site/public
    cd site && go run . -out public/

# Build the docs site and open it. `just site-serve 8001` picks another port,
# which is what you want when a second checkout or worktree is already serving.
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

# Fetch the third-party corpus pinned in live/corpus-manifest.json into .corpus/ (gitignored). Needs network; run once.
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
corpus init_bin="terraform":
    go run ./tools/corpus-gen -init-bin {{init_bin}}

# Where the sources describing each type's identity disagree (#106), into
# live/identity-sources.json. No provider, no network.
identity-sources:
    go run ./tools/row-gen -sources

# live/LIMITATIONS.md's per-refusal content (#110), from the three refusal
# registries plus the corpus artifact above. No provider, no network.
limits:
    go run ./tools/limits-gen

# Will this configuration work under live markers? (#114) DIR defaults to "."
live-check dir=".":
    go run ./cmd/choudoufu live-check {{dir}}
