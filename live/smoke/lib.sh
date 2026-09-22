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

# Once a bound has fired (smoke_stall below) the scenario is being killed,
# and whatever command it stood on fails because of the kill. The stall's own
# FAIL line is the verdict, so a fail that arrives after it says nothing.
fail() {
  if [ -n "${SMOKE_WORKROOT:-}" ] && [ -d "$SMOKE_WORKROOT/stalled" ]; then exit 124; fi
  echo "FAIL [$1]: $2" >&2; exit 1
}
# step also records where the scenario is, in a file, because the process
# that reports a stall is never the shell that ran the step (#1457).
step() {
  if [ -n "${SMOKE_WORKROOT:-}" ]; then printf '%s\n' "$*" > "$SMOKE_WORKROOT/step"; fi
  echo; echo "=== $* ==="; echo
}
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

# destroyed_exactly <tag> <n> <output> asserts that a destroy reported
# exactly n resources destroyed, and prints the destroy's WHOLE output when
# it did not.
#
# GitHub issue #1355 is why it exists and why it is here rather than written
# out per scenario. One `apply -destroy` of a two-instance record-backed
# estate on real AWS destroyed one instance and exited 0, and the run that
# caught it was filtering output through `grep -E "complete|Error"`, so the
# only surviving evidence was the count line itself and nothing about how the
# plan got that way. Both halves of that are fixed here: the count is the
# assertion rather than "Apply complete", and a failure hands the reader
# every line instead of the one that matched.
#
# n is the estate's instance count, spelled out at the call site, because
# "some resources were destroyed" is exactly the verdict that passed.
destroyed_exactly() {
  local tag="$1" n="$2" out="$3"
  grep -q "Resources: 0 added, 0 changed, $n destroyed" <<< "$out" && return 0
  echo "--- the destroy's whole output ---" >&2
  echo "$out" >&2
  echo "--- end of the destroy's output ---" >&2
  fail "$tag" "the destroy did not report exactly $n destroyed, which is this estate's instance count. A destroy that reports fewer and exits 0 has left something standing (GitHub issue #1355). The whole output is above."
}

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
  echo "  cluster up: kind $CLUSTER_NAME ($(kc version 2>/dev/null | grep -i server | head -1 || echo 'server version unknown'))"
}

cluster_down() {
  [ -n "$CLUSTER_NAME" ] || return 0
  kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  CLUSTER_NAME=""
}

# kc is kubectl against the run's own cluster, and kc_as is the same under
# another kubeconfig (a ServiceAccount's, say). Both carry --request-timeout
# because kubectl's default is to wait on the API server for ever: #1457 was
# one call that never came back, which cost a CI job 35 minutes and left no
# log. Every k8s scenario goes through these two and never calls kubectl
# bare, except for `kubectl config`, which only edits a local file.
# live/smoke/selftest-bounds.sh holds the scenarios to that.
KC_REQUEST_TIMEOUT="${KC_REQUEST_TIMEOUT:-30s}"
kc() { kc_as "$KUBECONFIG" "$@"; }
kc_as() {
  local cfg="$1"; shift
  kubectl --kubeconfig "$cfg" --request-timeout="$KC_REQUEST_TIMEOUT" "$@"
}

# smoke_descendants prints every live descendant of <pid>, leaving out <skip>
# and everything under it. One ps snapshot, read the same way on macOS and
# Linux. A stalled kubectl is usually a grandchild (scenario shell, command
# substitution, kubectl), so killing children alone would miss it.
smoke_descendants() { # <pid> [skip]
  ps -A -o pid=,ppid= | awk -v root="$1" -v skip="${2:-0}" '
    { parent[$1] = $2 }
    END {
      for (p in parent) {
        q = p
        while (q in parent && q != root && q != skip && q > 1) q = parent[q]
        if (q == root && p != root) print p
      }
    }'
}

# smoke_stall is what every bound in here does when it runs out (#1457). It
# is called from a timer subshell, never from the scenario's own shell,
# because that shell is the one blocked. In order: claim the stall so two
# bounds cannot both report it, print the FAIL line while the stalled command
# is still there to name, signal the scenario shell, then kill everything
# under it. The order matters. bash runs a trap only once its foreground
# command returns, so the TERM is left pending and the kill is what lets it
# run; smoke.sh's TERM trap then exits and the EXIT trap deletes the cluster.
#
# macOS ships no timeout(1) and CI is Linux, so this is sleep, ps and kill.
#
# The verdict reads `FAIL [<scenario>]: <before> in step "<step>" <after>`,
# so each bound words its own sentence around the step.
#
#   smoke_stall <before> <after>
smoke_stall() {
  set +e
  local before="$1" after="$2" me pids p left i main_cmd c
  local grace="${SMOKE_KILL_GRACE_SECS:-5}"
  mkdir "$SMOKE_WORKROOT/stalled" 2>/dev/null || return 0
  # The scenario shell stops the watchdog on its way out. This pass has to
  # finish first, or a child that ignores TERM outlives the run.
  trap '' TERM
  # $BASHPID is bash 4; this is the portable spelling of "my own pid".
  me="$(exec sh -c 'echo $PPID')"
  pids="$(smoke_descendants "$$" "$me")"
  main_cmd="$(ps -o command= -p "$$" 2>/dev/null)"
  {
    echo
    for p in $pids; do
      c="$(ps -o command= -p "$p" 2>/dev/null)"
      # A forked subshell carries the scenario shell's own command line.
      if [ -z "$c" ] || [ "$c" = "$main_cmd" ]; then continue; fi
      echo "  still running: $(printf '%s' "$c" | cut -c1-400)"
    done
    echo "FAIL [${SMOKE_SCENARIO:-smoke}]: $before in step \"$(cat "$SMOKE_WORKROOT/step" 2>/dev/null || echo '?')\" $after"
  } >&"${SMOKE_ERR_FD:-2}"
  kill -TERM "$$" 2>/dev/null
  # shellcheck disable=SC2086  # a list of pids, split on purpose
  kill -TERM $pids 2>/dev/null
  i=0
  while [ "$i" -lt "$grace" ]; do
    left=""
    for p in $pids; do kill -0 "$p" 2>/dev/null && left="$left $p"; done
    [ -n "$left" ] || break
    sleep 1; i=$((i+1))
  done
  # shellcheck disable=SC2086
  kill -KILL $pids 2>/dev/null
  return 0
}

# smoke_timer runs smoke_stall after <secs> unless it is stopped first, and
# sets SMOKE_TIMER_PID. Its own output goes to the stderr smoke.sh started
# with and never to a pipe the caller is capturing: a `$(...)` does not
# return while anything still holds its write end.
smoke_timer() { # <secs> <before> <after>, the two halves of smoke_stall's verdict
  (
    sleep "$1" >/dev/null 2>&1 &
    nap=$!
    trap 'kill "$nap" 2>/dev/null; exit 0' TERM
    wait "$nap" || exit 0
    smoke_stall "$2" "$3"
  ) >/dev/null 2>&"${SMOKE_ERR_FD:-2}" &
  SMOKE_TIMER_PID=$!
}
# Both halves are `|| true`: the timer may already be gone, this runs under
# `set -e`, and one caller is smoke.sh's EXIT trap, where a failed kill would
# end the trap before it deleted the cluster (#1378's shape).
smoke_timer_stop() { # <pid>
  kill "$1" 2>/dev/null || true
  wait "$1" 2>/dev/null || true
}

# chdf_bounded is chdf with a bound, for the calls that talk to a cluster
# whose admission chain the step has just broken on purpose: a fail-closed
# webhook with nothing behind it, or a policy that rewrites the write. The
# API server is meant to answer those inside the webhook's own timeout, and
# nothing here used to notice when it did not. The call still runs in the
# foreground, so its exit code, its output and Ctrl-C behave as they do for
# chdf. A call that outlives the bound fails the scenario by name.
CHDF_TIMEOUT_SECS="${CHDF_TIMEOUT_SECS:-300}"
chdf_bounded() {
  local rc=0 timer
  smoke_timer "$CHDF_TIMEOUT_SECS" "choudoufu $1 stalled" \
    "and was killed after ${CHDF_TIMEOUT_SECS}s. Raise the bound with CHDF_TIMEOUT_SECS=<seconds>."
  timer="$SMOKE_TIMER_PID"
  chdf "$@" || rc=$?
  smoke_timer_stop "$timer"
  return "$rc"
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
    conds="$(kc get "$target" -o jsonpath='{.status.conditions[*].type}' 2>/dev/null || true)"
    if [ -n "$conds" ]; then break; fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "$scenario" "$target still has no .status.conditions after ${status_secs}s - nothing has written a status for it, so whether it is $condition was never answered"
    fi
    sleep 1
  done
  # kubectl wait is one long watch, so its request timeout is the wait's own
  # bound plus slack and not kc's per-request default.
  KC_REQUEST_TIMEOUT="$((condition_secs + 15))s" kc wait --for="condition=$condition" "$target" --timeout="${condition_secs}s" >/dev/null 2>&1 \
    || fail "$scenario" "$target carries conditions [$conds] but none of them reached $condition within ${condition_secs}s: $(kc get "$target" -o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}' 2>&1)"
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
