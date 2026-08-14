# choudoufu build targets. `just --list` shows everything.

# Build the choudoufu binary into the current directory
build:
    go build ./cmd/choudoufu

# Unit tests. Integration tiers skip unless their env vars are set.
test:
    go test ./...

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
corpus:
    go run ./tools/corpus-gen

# Will this configuration work under live markers? (#114) DIR defaults to "."
live-check dir=".":
    go run ./cmd/choudoufu live-check {{dir}}
