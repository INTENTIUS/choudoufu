# Shared machinery for the smoke scenarios (issue #713). Sourced, never run.

SMOKE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SMOKE_DIR/../.." && pwd)"
SMOKE_VERSION="$(cat "$SMOKE_DIR/VERSION")"

# Every run is fully isolated: its own compose project (so one run's
# cleanup can never tear down another run's emulator - the first
# concurrent invocation proved that the hard way, killing the
# maintainer's apply mid-run) and, unless pinned, its own free port.
SMOKE_ID="${SMOKE_ID:-$$-$RANDOM}"

# Port 0 asks the kernel for a free port at bind time - the only
# race-free answer. A probe-then-bind loop lost the race on its first
# concurrent test: both runs probed during the other's startup window
# and picked the same port. FLOCI_PORT pins one explicitly when wanted.
FLOCI_PORT="${FLOCI_PORT:-0}"
export FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
export OPENTOFU_IMAGE="${OPENTOFU_IMAGE:-ghcr.io/opentofu/opentofu:$(python3 -c "import json;print(json.load(open('$ROOT/live/oracle-versions.json'))['tofu_version'])")}"
export FLOCI_PORT

COMPOSE=(docker compose -p "choudoufu-smoke-${SMOKE_ID}" -f "$SMOKE_DIR/docker-compose.yml")

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
  echo "  floci:     $FLOCI_IMAGE (pinned image, never built here)"
  echo "  opentofu:  $OPENTOFU_IMAGE (stock oracle leg)"
  [ "${SMOKE_INSTRUMENT:-0}" = "1" ] && echo "  instrumentation: ON (TF_LOG=debug capture + request summary)"
  echo
}

stack_up() {
  "${COMPOSE[@]}" up -d floci >/dev/null 2>&1 || fail "stack" "docker compose up floci failed"
  # Resolve the port the kernel actually assigned (or the pinned one).
  # The endpoint host is localhost ON PURPOSE, not 127.0.0.1: the AWS
  # provider composes account-qualified hostnames for a few services
  # (S3 Control: 000000000000.<host>), and subdomains of localhost
  # resolve to loopback by convention where subdomains of a raw IP
  # cannot resolve at all - swapping in 127.0.0.1 broke exactly that.
  FLOCI_PORT="$("${COMPOSE[@]}" port floci 4566 2>/dev/null | sed 's/.*://')"
  [ -n "$FLOCI_PORT" ] || fail "stack" "could not resolve floci's published port"
  export FLOCI_PORT
  SMOKE_ENDPOINT="http://localhost:${FLOCI_PORT}"
  export SMOKE_ENDPOINT
  echo "  emulator up at $SMOKE_ENDPOINT (compose project choudoufu-smoke-$SMOKE_ID)"
  local i
  for i in $(seq 1 30); do
    curl -fsS "${SMOKE_ENDPOINT}/_localstack/health" >/dev/null 2>&1 && return 0
    curl -fsS "${SMOKE_ENDPOINT}/" >/dev/null 2>&1 && return 0
    sleep 1
  done
  fail "stack" "floci never answered on :${FLOCI_PORT}"
}

stack_down() { "${COMPOSE[@]}" down --remove-orphans >/dev/null 2>&1 || true; }

# cluster_up stands up a kind cluster for a Kubernetes scenario (#1057):
# one per run, named by SMOKE_ID like the compose project, its kubeconfig
# under SMOKE_WORKROOT and never the user's own. KUBE_CONFIG_PATH is what
# the hashicorp/kubernetes provider reads, so a fixture needs no
# config_path argument. A kind cluster is a real API server, not an
# emulator: everything a scenario asserts against it is what any cluster
# would answer. Needs kind (https://kind.sigs.k8s.io) and kubectl on PATH.
CLUSTER_NAME=""
cluster_up() {
  command -v kind >/dev/null 2>&1 || fail "cluster" "kind is not installed; this scenario needs a kind cluster (brew install kind)"
  command -v kubectl >/dev/null 2>&1 || fail "cluster" "kubectl is not installed"
  CLUSTER_NAME="chdf-smoke-$(echo "$SMOKE_ID" | tr -c 'a-z0-9-\n' '-' | cut -c1-30)"
  export KUBECONFIG="$SMOKE_WORKROOT/kubeconfig" KUBE_CONFIG_PATH="$SMOKE_WORKROOT/kubeconfig"
  kind create cluster --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG" --wait 120s >"$SMOKE_WORKROOT/logs/kind.log" 2>&1 \
    || fail "cluster" "kind create cluster failed: $(tail -5 "$SMOKE_WORKROOT/logs/kind.log")"
  echo "  cluster up: kind $CLUSTER_NAME ($(kubectl version 2>/dev/null | grep -i server | head -1 || echo 'server version unknown'))"
}

cluster_down() {
  [ -n "$CLUSTER_NAME" ] || return 0
  kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  CLUSTER_NAME=""
}

# k8s_wait_condition waits for a condition on one object in the two steps a
# freshly created object actually needs (#1278).
#
# `kubectl wait --for=condition=X` does NOT wait for the status subresource
# to be populated. It reads .status.conditions, and apiextensions v1 tags
# that field `json:"conditions"` with no omitempty, so a CRD the API server
# has created but whose conditions no controller has written yet is served
# as `"status": {"acceptedNames": ..., "conditions": null, ...}`. kubectl's
# accessor reads that null and errors out - `.status.conditions accessor
# error: <nil> is of the type <nil>, expected []interface{}` - in well under
# a second, instead of retrying. --timeout never comes into play. The CRD
# create response carries exactly that shape, so every `kubectl wait` fired
# at a just-applied CRD is racing the controller that fills conditions in.
#
# So: poll until .status.conditions is there to read, bounded by a
# wall-clock deadline, and only then hand the object to kubectl wait. The
# two ways this gives up are different problems and say so - no conditions
# at all means the server never got round to the object, while conditions
# that never carry the one asked for is the object itself - and only the
# second is ever a scenario's business.
#
#   k8s_wait_condition <scenario> <kind/name> <condition> [status-secs] [condition-secs]
#
# Both legs are bounded on purpose: a wait that can hang here turns one slow
# CI job into a stuck one, which is how #1143's first red arm burned 1800s
# and what #1267 found selftest-kill.sh doing.
k8s_wait_condition() {
  local scenario="$1" target="$2" condition="$3"
  local status_secs="${4:-60}" condition_secs="${5:-60}"
  local deadline conds
  deadline=$(( $(date +%s) + status_secs ))
  while :; do
    # The probe is the condition TYPES, not the conditions list: jsonpath
    # prints the JSON null as the four-character string "null", so a
    # non-empty `{.status.conditions}` is exactly the shape being waited
    # out. `{.status.conditions[*].type}` is empty for null, for absent and
    # for an empty list, and non-empty only when there is a condition to
    # read - the distinction this whole function exists to make. (That was
    # the first draft's bug, caught by driving it against a nulled status.)
    conds="$(kubectl --kubeconfig "$KUBECONFIG" get "$target" -o jsonpath='{.status.conditions[*].type}' 2>/dev/null || true)"
    if [ -n "$conds" ]; then break; fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "$scenario" "$target still has no .status.conditions after ${status_secs}s - nothing has written a status for it, so whether it is $condition was never answered"
    fi
    sleep 1
  done
  kubectl --kubeconfig "$KUBECONFIG" wait --for="condition=$condition" "$target" --timeout="${condition_secs}s" >/dev/null 2>&1 \
    || fail "$scenario" "$target carries conditions [$conds] but none of them reached $condition within ${condition_secs}s: $(kubectl --kubeconfig "$KUBECONFIG" get "$target" -o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}' 2>&1)"
}

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

awsl() { aws --endpoint-url "$SMOKE_ENDPOINT" "$@"; }

# sed_i edits a file in place portably: BSD sed wants `-i ''` and GNU sed
# reads that empty string as the script, so neither spelling runs on the
# other platform, and a paste-and-go scenario has to run on both. A temp
# file and a rename is the crossing scripts' own idiom. $1 is the file, the
# rest is passed to sed as written.
sed_i() {
  local f="$1" t; shift
  t="$(mktemp)"
  sed "$@" "$f" > "$t" && mv "$t" "$f"
}

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
