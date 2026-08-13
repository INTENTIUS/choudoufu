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

# Build the docs site into site/public/
site:
    cd site && go run . -out public/

# Build the docs site and serve it at http://localhost:8000
site-serve: site
    python3 -m http.server 8000 --directory site/public

# Lint exactly as upstream CI would (golangci-lint, both GOOS passes)
lint:
    make golangci-lint
