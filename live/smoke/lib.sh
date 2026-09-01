# Shared machinery for the smoke scenarios (issue #713). Sourced, never run.

SMOKE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SMOKE_DIR/../.." && pwd)"
SMOKE_VERSION="$(cat "$SMOKE_DIR/VERSION")"

FLOCI_PORT="${FLOCI_PORT:-4650}"
export FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
export OPENTOFU_IMAGE="${OPENTOFU_IMAGE:-ghcr.io/opentofu/opentofu:$(python3 -c "import json;print(json.load(open('$ROOT/live/oracle-versions.json'))['tofu_version'])")}"
export FLOCI_PORT

COMPOSE=(docker compose -p choudoufu-smoke -f "$SMOKE_DIR/docker-compose.yml")

fail() { echo "FAIL [$1]: $2" >&2; exit 1; }
step() { echo; echo "=== $* ==="; echo; }
note() { echo "  $*"; }

# The output contract (issue #713: easy to OBSERVE): every step teaches.
#   explain - what is about to happen and why it matters, before it runs
#   cmd     - the command being run, verbatim, so the watcher could type it
#   evidence- real output lines, indented, so the claim is seen not asserted
#   proof   - what the evidence just proved, one arrow line
explain() { while [ $# -gt 0 ]; do echo "  $1"; shift; done; echo; }
cmd() { echo "  \$ $*"; }
evidence() { sed 's/^/      /'; }
proof() { echo; echo "  -> $*"; echo; }

# resolve_choudoufu answers where the binary under test comes from, in
# priority order, and reports its provenance for the banner:
#   CHOUDOUFU_BIN      an explicit binary, used as-is
#   CHOUDOUFU_VERSION  a release tag; downloaded once into a cache dir
#   (neither)          built from this checkout's source - the default,
#                      and the only leg that supports "from source";
#                      floci is deliberately never built from source here.
resolve_choudoufu() {
  if [ -n "${CHOUDOUFU_BIN:-}" ]; then
    [ -x "$CHOUDOUFU_BIN" ] || fail "resolve" "CHOUDOUFU_BIN=$CHOUDOUFU_BIN is not executable"
    TOFU="$CHOUDOUFU_BIN"; CHOUDOUFU_PROVENANCE="CHOUDOUFU_BIN ($TOFU)"
  elif [ -n "${CHOUDOUFU_VERSION:-}" ]; then
    local cache="$HOME/.cache/choudoufu-smoke/$CHOUDOUFU_VERSION"
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
    if [ ! -x "$cache/choudoufu" ]; then
      mkdir -p "$cache"
      ( cd "$cache" \
        && gh release download "$CHOUDOUFU_VERSION" -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz" --clobber \
        && tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz ) \
        || fail "resolve" "could not download release $CHOUDOUFU_VERSION for ${os}_${arch}"
    fi
    TOFU="$cache/choudoufu"; CHOUDOUFU_PROVENANCE="release $CHOUDOUFU_VERSION"
  else
    TOFU="$SMOKE_WORKROOT/bin/choudoufu"
    mkdir -p "$SMOKE_WORKROOT/bin"
    ( cd "$ROOT" && go build -o "$TOFU" ./cmd/choudoufu ) \
      || fail "resolve" "go build ./cmd/choudoufu failed from $ROOT"
    CHOUDOUFU_PROVENANCE="source ($(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo '?'))"
  fi
}

banner() {
  echo "choudoufu smoke v$SMOKE_VERSION - scenario: $1"
  echo "  choudoufu: $CHOUDOUFU_PROVENANCE"
  echo "  floci:     $FLOCI_IMAGE (host port $FLOCI_PORT; pinned image, never built here)"
  echo "  opentofu:  $OPENTOFU_IMAGE (stock oracle leg)"
  [ "${SMOKE_INSTRUMENT:-0}" = "1" ] && echo "  instrumentation: ON (TF_LOG=debug capture + request summary)"
  echo
}

stack_up() {
  "${COMPOSE[@]}" up -d floci >/dev/null 2>&1 || fail "stack" "docker compose up floci failed"
  local i
  for i in $(seq 1 30); do
    curl -fsS "http://localhost:${FLOCI_PORT}/_localstack/health" >/dev/null 2>&1 && return 0
    curl -fsS "http://localhost:${FLOCI_PORT}/" >/dev/null 2>&1 && return 0
    sleep 1
  done
  fail "stack" "floci never answered on :${FLOCI_PORT}"
}

stack_down() { "${COMPOSE[@]}" down --remove-orphans >/dev/null 2>&1 || true; }

# oracle_up prepares the stock leg: the shared plugin volume is created
# root-owned by docker, and the oracle runs as the invoking user so the
# files it writes into the mounted workdir stay deletable - so the volume
# gets one root-shot chown before first use.
ORACLE_READY=0
oracle_up() {
  [ "$ORACLE_READY" = "1" ] && return 0
  "${COMPOSE[@]}" run --rm --user 0 --entrypoint sh opentofu     -c "chown -R $(id -u):$(id -g) /plugins" >/dev/null 2>&1     || fail "stack" "could not prepare the oracle's plugin volume"
  ORACLE_READY=1
}

# stock runs the pinned opentofu oracle inside the compose network against
# floci, with the scenario workdir mounted at /work.
stock() {
  oracle_up
  "${COMPOSE[@]}" run --rm --user "$(id -u):$(id -g)" opentofu "$@"
}

awsl() { aws --endpoint-url "http://localhost:${FLOCI_PORT}" "$@"; }

# chdf runs the binary under test. Under SMOKE_INSTRUMENT=1 every call's
# TF_LOG=debug stream lands in its own file so the summary can count what
# actually went over the wire - choudoufu's own client requests included,
# which is what #682's logging exists for.
CHDF_CALL=0
chdf() {
  CHDF_CALL=$((CHDF_CALL+1))
  if [ "${SMOKE_INSTRUMENT:-0}" = "1" ]; then
    TF_LOG=debug TF_LOG_PATH="$SMOKE_WORKROOT/logs/call-$CHDF_CALL.log" "$TOFU" "$@"
  else
    "$TOFU" "$@"
  fi
}

instrument_summary() {
  [ "${SMOKE_INSTRUMENT:-0}" = "1" ] || return 0
  step "instrumentation - requests on the wire (terralith-style counters)"
  local logs="$SMOKE_WORKROOT/logs"
  [ -d "$logs" ] || { note "no logs captured"; return 0; }
  local total retries
  total=$(cat "$logs"/*.log 2>/dev/null | grep -c "HTTP Request Sent" || true)
  retries=$(cat "$logs"/*.log 2>/dev/null | grep -c "retrying request" || true)
  note "requests: $total   retries: $retries   (per-call logs: $logs)"
  note "top operations:"
  cat "$logs"/*.log 2>/dev/null | grep "HTTP Request Sent" \
    | grep -oE "rpc.method=[A-Za-z0-9/_-]+" | sort | uniq -c | sort -rn | head -8 \
    | sed 's/^/    /'
}
