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
# KIND_NODE_IMAGE is the single source of truth for the kind cluster's node
# image (#1594), the same role FLOCI_IMAGE plays for the emulator: a bare
# `kind create cluster` uses whatever node image the kind binary on PATH
# happens to default to, and that default moves across kind releases, so a
# scenario run on one laptop's kind and one run on CI's can measure two
# different Kubernetes versions from the same commit.
export KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-$(cat "$ROOT/live/kind-node-image")}"

# FLOCI_AZ_IMAGE is the Azure emulator's pin (#1120), the same role again:
# lex00/floci-az by digest, read from live/floci-az-image. Exported on every
# run, Azure or not, because docker compose interpolates every service in
# the file, profile or no profile, and the floci-az service's image is
# `:?`-required. FLOCI_AZ_PORT is exported here for the same reason
# FLOCI_PORT's comment gives: 0 asks the kernel for a free port at bind
# time, but only if compose can see it.
export FLOCI_AZ_IMAGE="${FLOCI_AZ_IMAGE:-$(cat "$ROOT/live/floci-az-image")}"
export FLOCI_AZ_PORT="${FLOCI_AZ_PORT:-0}"
# FLOCI_GCP_IMAGE is the same for the GCP emulator (#1119): lex00/floci-gcp,
# pinned by digest in live/floci-gcp-image. FLOCI_GCP_PORT pins its host
# port the way FLOCI_PORT pins floci's; 0 asks the kernel for a free one.
export FLOCI_GCP_IMAGE="${FLOCI_GCP_IMAGE:-$(cat "$ROOT/live/floci-gcp-image")}"
FLOCI_GCP_PORT="${FLOCI_GCP_PORT:-0}"
export FLOCI_PORT FLOCI_GCP_PORT

# The oracle's provider plugin cache. By default it is the named volume in
# docker-compose.yml, mounted at /plugins. When the host exports
# TF_PLUGIN_CACHE_DIR (claims-smoke.yml does, since #1646), `chdf init` on
# the host installs the provider as a symlink into that host directory, and
# the oracle's own init installs one into its cache. A scenario that runs
# both in one workdir (roundtrip, stock-when-you-need-it) shares one
# .terraform/providers, and on a runner whose platform is the container's
# (linux_amd64) both write the same entry: a host symlink dangles inside
# the container and a /plugins symlink dangles on the host, and the next
# run on the other side reports "there is no package ... cached in
# .terraform/providers" (claims-smoke run 36339857046). So with a host
# cache set, the oracle mounts that same directory at the same absolute
# path and uses it as its cache: every symlink either side writes then
# resolves on both.
if [ -n "${TF_PLUGIN_CACHE_DIR:-}" ]; then
  mkdir -p "$TF_PLUGIN_CACHE_DIR"
  SMOKE_PLUGINS_SRC="$(cd "$TF_PLUGIN_CACHE_DIR" && pwd)"
  SMOKE_PLUGINS_DST="$SMOKE_PLUGINS_SRC"
  export SMOKE_PLUGINS_SRC SMOKE_PLUGINS_DST
fi
SMOKE_PLUGINS_MOUNT="${SMOKE_PLUGINS_DST:-/plugins}"

COMPOSE=(docker compose -p "choudoufu-smoke-${SMOKE_ID}" -f "$SMOKE_DIR/docker-compose.yml")

# Once a bound has fired (smoke_stall below) the scenario is being killed,
# and whatever command it stood on fails because of the kill. The stall's own
# FAIL line is the verdict, so a fail that arrives after it says nothing.
#
# The verdict file (#1439) is how smoke.sh's EXIT trap knows a FAIL line
# ended the run, so it does not print a second one over it. Only the
# scenario shell's own fail ends the run: one inside a `$(...)` is output
# the scenario reads, and a-wrong-bucket-is-refused's control captures an
# arm's fail on purpose and goes on to pass. So the file is written only
# from that shell, in the portable spelling of "am I that shell".
fail() {
  if [ -n "${SMOKE_WORKROOT:-}" ] && [ -d "$SMOKE_WORKROOT/stalled" ]; then exit 124; fi
  if [ -n "${SMOKE_WORKROOT:-}" ] && [ "$(exec sh -c 'echo $PPID')" = "$$" ]; then : > "$SMOKE_WORKROOT/verdict"; fi
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
#
# A proof line that begins with the word `caught` is a control's verdict
# (#1439): every BREAK=1 and BREAK_<NAME>=1 arm ends with one, and smoke.sh
# refuses to pass a control run that printed none. proof records each such
# line in a file so the EXIT trap can count them; a file and not a variable
# because a control's proof is sometimes printed from a subshell.
explain() { while [ $# -gt 0 ]; do echo "  $1"; shift; done; echo; }
cmd() { echo "  \$ $*"; }
evidence() { sed 's/^/      /'; }
proof() {
  case "${1:-}" in
    caught*) if [ -n "${SMOKE_WORKROOT:-}" ]; then printf '%s\n' "$*" >> "$SMOKE_WORKROOT/caught"; fi ;;
  esac
  echo; echo "  -> $*"; echo
}

# strip_incomplete_sweep_warning removes the estate-wide sweep's own gap
# reporting from a captured CLI transcript, so a scenario's own
# denied()-style grep over "the rest of what a step printed" does not
# false-positive on it (GitHub issue #1636). A sweep gap's Detail sometimes
# quotes the denied read's own raw provider error verbatim - an
# emulator-restricted role missing iam:ListPolicies, say - which reads
# "AccessDenied" or "not authorized to perform" despite naming a read this
# scenario never asked for and a call site (Bob's tag write, on his own
# grant) that has nothing to do with it. Confirmed against a real captured
# run (BREAK=1 against this claim, unfixed), that text leaks in through TWO
# distinct places, both traced to internal/command:
#
#  1. "Not swept for removal: N resource types" - live_plan.go's own
#     pre-diff coverage report, printed as part of every plan/apply that
#     renders a diff, itemizing each [OBJECT_UNTAGGED] and
#     [TAG_INDEX_COVERAGE_UNCONFIRMED] gap with the SAME Detail text. This
#     always ends where format.HorizontalRule's rule of "-" characters
#     opens the diff that follows, since both live only inside the same
#     pre-diff renderer - so everything from this section's own header
#     through that rule is dropped, rule line kept.
#  2. The "Incomplete sweep for undeclared resources" diagnostic itself,
#     raised once more after apply. Every scenario here passes -no-color,
#     which renders a diagnostic through format.DiagnosticPlain rather than
#     format.Diagnostic: no box-drawing rule characters at all, just
#     "Warning: <summary>", a blank line, then the wrapped Detail (one
#     paragraph, so no blank line of its own), ended by the blank line the
#     CLI always prints before whatever comes next.
#
# Both are always Warning severity (SeverityForRefusal returns
# SeverityWarning for SummaryIncompleteSweep unconditionally) - a genuine
# refusal of the call under test is an Error that fails the command outright
# - but that distinction is not exploited here because #1's coverage report
# carries no "Warning:"/"Error:" prefix of its own to key on.
#
# format.Diagnostic's boxed form (the literal characters ╷ and ╵), used when
# a caller omits -no-color, is handled for #2 as well, though it does not
# occur in this file's own captures.
strip_incomplete_sweep_warning() {
  awk '
    /^(Foreign resources: nothing was swept|Not swept for removal: )/ { report = 1; next }
    report { if ($0 ~ /^─+$/) { report = 0 } else { next } }

    /^╷[[:space:]]*$/ { hold = $0; getline nxt
      if (nxt ~ /Incomplete sweep for undeclared resources/) { boxed = 1; next }
      print hold; print nxt; next
    }
    boxed { if ($0 ~ /^╵[[:space:]]*$/) boxed = 0; next }

    /^Warning: Incomplete sweep for undeclared resources[[:space:]]*$/ { plain = 1; next }
    plain == 1 { plain = 2; next }
    plain == 2 { if ($0 ~ /^[[:space:]]*$/) plain = 0; next }

    { print }
  '
}

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

# smoke_log_dir is where a run's side logs go, and it has two answers on
# purpose. ci-run.sh exports SMOKE_LOG_DIR as the directory holding the
# scenario log, which is the directory the CI job uploads whole, so a file
# written there rides out in the artifact next to scenario.log. With nothing
# set - a local `just smoke <name>` - it is the run's own workroot, which
# cleanup deletes on the way out; there the tail printed on failure is the
# copy that lasts, in the terminal where the operator already is.
smoke_log_dir() { printf '%s\n' "${SMOKE_LOG_DIR:-$SMOKE_WORKROOT/logs}"; }

# logged runs a command with its whole output kept in a file under
# smoke_log_dir and, when it fails, prints that file's tail before the FAIL
# line, so the cause is in the job log and not only in the artifact.
#
# Issue #1521 is why it exists. `docker compose up -d floci >/dev/null 2>&1
# || fail "stack" "docker compose up floci failed"` left the smoke job on PR
# #1507 (run 35753172138) reading that one line and nothing else, forty
# seconds in; a re-run of the same job passed. An image that would not pull,
# a published port already taken and a daemon that had gone away all print
# that line, so the failure could not be told from a regression - which is
# the one thing a smoke run is for.
#
#   logged <name> <tag> <message> -- <command> [args...]
#
# <name> names the file (<name>.log) and <tag> is fail's bracketed tag.
SMOKE_LOG_TAIL="${SMOKE_LOG_TAIL:-40}"
logged() {
  local name="$1" tag="$2" msg="$3"; shift 3
  [ "${1:-}" = "--" ] && shift
  local dir file
  dir="$(smoke_log_dir)"; mkdir -p "$dir"
  file="$dir/$name.log"
  "$@" > "$file" 2>&1 && return 0
  smoke_log_tail "$file" "$name"
  fail "$tag" "$msg. Its output is above, and whole in $file"
}

# in_dir runs a command from a directory, in a subshell, so the cd cannot
# leak into the scenario: `logged <name> <tag> <msg> -- in_dir "$D" chdf
# init ...` is `( cd "$D" && chdf init ... ) || fail` with the output kept
# (#1717). The command may be a shell function, which `env -C` would not
# reach.
in_dir() { ( cd "$1" || exit 1; shift; "$@" ); }

# smoke_log_tail prints the end of a kept log to stderr, fenced, so a reader
# scrolling a job log can see where the borrowed output starts and stops.
smoke_log_tail() { # <file> <what>
  {
    echo "--- last $SMOKE_LOG_TAIL lines of $1 ($2) ---"
    tail -n "$SMOKE_LOG_TAIL" "$1" 2>/dev/null || echo "  (nothing was captured)"
    echo "--- end of $1 ---"
  } >&2
}

stack_up() {
  logged compose-up-floci stack "docker compose up floci failed" \
    -- "${COMPOSE[@]}" up -d floci
  # Resolve the port the kernel actually assigned (or the pinned one).
  # The endpoint host is localhost ON PURPOSE, not 127.0.0.1: the AWS
  # provider composes account-qualified hostnames for a few services
  # (S3 Control: 000000000000.<host>), and subdomains of localhost
  # resolve to loopback by convention where subdomains of a raw IP
  # cannot resolve at all - swapping in 127.0.0.1 broke exactly that.
  local portlog
  portlog="$(smoke_log_dir)/compose-port-floci.log"
  FLOCI_PORT="$("${COMPOSE[@]}" port floci 4566 2>"$portlog" | sed 's/.*://')"
  # An empty answer is either a container that is no longer up or a compose
  # that said why on stderr, and that stderr used to go to /dev/null.
  [ -n "$FLOCI_PORT" ] || fail "stack" "could not resolve floci's published port; docker compose port said: $(tr '\n' ' ' < "$portlog")"
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
  # The probes above are discarded on purpose - up to thirty of them are
  # meant to fail while the emulator boots, and their curl errors say only
  # "connection refused". What names the cause is the container itself: a
  # floci that came up and then died, or one still unpacking, reads off its
  # own log and nowhere else (#1521).
  # The container's state goes in the FAIL line and not into the tailed
  # file: "Exited (1)" is the first thing worth knowing, and a chatty image
  # can push 40 lines of its own startup over anything printed above it.
  local upinfo state
  upinfo="$(smoke_log_dir)/floci-container.log"
  state="$("${COMPOSE[@]}" ps -a --format '{{.State}} - {{.Status}}' floci 2>&1 | tr '\n' ';')"
  "${COMPOSE[@]}" logs --no-color --tail 200 floci > "$upinfo" 2>&1 || true
  smoke_log_tail "$upinfo" "the floci container"
  fail "stack" "floci never answered on :${FLOCI_PORT} after 30 probes over 30s; the container is [${state:-unknown}]. Its log is above, and whole in $upinfo"
}

# stack_down and cluster_down keep their `>/dev/null 2>&1 || true`. Both run
# from smoke.sh's EXIT trap on every run, passed or failed, after the verdict
# line has already been printed: they cannot fail a run, so they have no
# cause to report, and a compose teardown's chatter on the happy path would
# print under the PASS line every single time.
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
  # This already kept kind's output; what it did not do was keep it anywhere
  # a CI job could read afterwards - SMOKE_WORKROOT is deleted by cleanup and
  # was never uploaded - or print more than five lines of it. Through logged
  # it lands beside the scenario log (#1521).
  logged kind cluster "kind create cluster failed" \
    -- kind create cluster --image "$KIND_NODE_IMAGE" --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG" --wait 120s
  echo "  cluster up: kind $CLUSTER_NAME ($(kc version 2>/dev/null | grep -i server | head -1 || echo 'server version unknown'))"
}

# See stack_down above for why this one stays quiet.
cluster_down() {
  [ -n "$CLUSTER_NAME" ] || return 0
  kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  CLUSTER_NAME=""
}

# azure_up stands up the Azure emulator for an Azure scenario (#1120 unit
# 7): lex00/floci-az at the live/floci-az-image pin, as the floci-az
# service of this run's own compose project, on a kernel-chosen port. It
# waits for floci-az's /ready, takes the CA its TLS listener serves (the
# operator's certificate from scripts/floci-az-mac-cert.sh when there is
# one, which macOS requires, or the one floci-az generated at start),
# proves one HTTPS read of /metadata/endpoints through that CA, and then
# exports the environment both providers read with the operator's own
# Azure credentials taken out (azure_provider_env below). A fixture's
# provider blocks come from azure_terraform_block, which states the same
# endpoints and credentials again, since an argument in the block wins over
# the environment. live/FLOCI.md's Azure section says what the emulator
# serves and what it does not.
#
# Built for #1120 and not yet booted by anyone: the first scenario that
# calls this is its first run, and the maintainer's.
#
# AZURE_UP is set before the container is asked for, so a start that fails
# half way still has its container removed by azure_down.
AZURE_UP=0
AZURE_HOST=""
AZURE_ENDPOINT=""
AZURE_CA_FILE=""
FLOCI_AZ_READY_SECS="${FLOCI_AZ_READY_SECS:-90}"

# The ids floci-az seeds: its default subscription
# (services.arm.default-subscription-id) and default tenant (Entra's
# default-tenant-id). The client id is any id: floci-az issues a token for
# any client id and secret unless validate-client-credentials is set, and
# accepts any bearer token on ARM.
AZURE_SUBSCRIPTION_ID="00000000-0000-0000-0000-000000000001"
AZURE_TENANT_ID="00000000-0000-0000-0000-000000000002"
AZURE_CLIENT_ID="00000000-0000-0000-0000-000000000003"

# The object id and free port helpers are shared with the gauntlet's
# gauntlet_floci_az_up (#2140), so the two starts of floci-az cannot drift
# apart. azure_up sources them, not this file's top level: only an Azure
# scenario needs them, and the smoke selftests run this library in a
# sandbox tree that carries only what they need.
#
# azure_client_oid <client-id>: floci_az_client_oid, under the name this
# library's callers use.
azure_client_oid() {
  floci_az_client_oid "$1"
}

azure_up() {
  AZURE_UP=1
  # shellcheck source=../e2e/lib/floci-az.sh
  . "$ROOT/live/e2e/lib/floci-az.sh" || fail "azure" "could not source $ROOT/live/e2e/lib/floci-az.sh"
  # The host port is chosen here rather than by the kernel at publish
  # time, because the storage suffix below has to name it before the
  # container starts. A port another process takes between this probe and
  # the publish fails compose up by name.
  if [ "${FLOCI_AZ_PORT:-0}" = "0" ]; then
    FLOCI_AZ_PORT="$(floci_az_free_port)" \
      || fail "azure" "could not pick a free host port for floci-az"
  fi
  export FLOCI_AZ_PORT
  # FLOCI_AZ_STORAGE_SUFFIX (lex00/floci-az c760e26): /metadata/endpoints
  # and storage accounts' primaryEndpoints report this suffix in place of
  # core.windows.net, so the stock azurerm backend and record_store
  # "azurerm" address an account as https://<account>.blob.localhost:<port>,
  # which is this container, instead of leaving for real Azure DNS. The
  # certificate covers *.blob.localhost. That <account>.blob.localhost
  # resolves to loopback is the client's resolver's doing, not measured yet.
  export FLOCI_AZ_STORAGE_SUFFIX="localhost:${FLOCI_AZ_PORT}"
  # RBAC enforcement is on in the compose service (lex00/floci-az bb225ad)
  # so the fence claims can hold a second principal to its role
  # assignments. The principal every provider block and backend here signs
  # in as is listed as an admin and passes unchecked, so a scenario that
  # never assigns a role behaves as before.
  FLOCI_AZ_RBAC_ADMIN_PRINCIPALS="$(azure_client_oid "$AZURE_CLIENT_ID")" \
    || fail "azure" "could not derive floci-az's object id for client $AZURE_CLIENT_ID"
  export FLOCI_AZ_RBAC_ADMIN_PRINCIPALS
  # The certificate floci-az serves: the operator's own from
  # scripts/floci-az-mac-cert.sh when there is one (required on macOS, and
  # checked there to be trusted by the keychain before anything starts), or
  # floci-az's own per-start CA on Linux when there is none
  # (live/e2e/lib/floci-az.sh). The operator's is mounted read-only by the
  # overlay compose file, which is added to COMPOSE so every later call on
  # this project, azure_down's included, sees the same service.
  floci_az_tls_select || fail "azure" "$FLOCI_AZ_TLS_PROBLEM"
  FLOCI_AZ_TLS_CERT_PATH="" FLOCI_AZ_TLS_KEY_PATH=""
  local tlsline
  while IFS= read -r tlsline; do
    case "$tlsline" in
      FLOCI_AZ_TLS_CERT_PATH=*) FLOCI_AZ_TLS_CERT_PATH="${tlsline#*=}" ;;
      FLOCI_AZ_TLS_KEY_PATH=*) FLOCI_AZ_TLS_KEY_PATH="${tlsline#*=}" ;;
    esac
  done < <(floci_az_tls_env)
  export FLOCI_AZ_TLS_CERT_PATH FLOCI_AZ_TLS_KEY_PATH
  if [ -n "$FLOCI_AZ_TLS_CERT_FILE" ]; then
    export FLOCI_AZ_TLS_CERT_FILE FLOCI_AZ_TLS_KEY_FILE
    case " ${COMPOSE[*]} " in
      *" $SMOKE_DIR/docker-compose.floci-az-tls.yml "*) ;;
      *) COMPOSE+=(-f "$SMOKE_DIR/docker-compose.floci-az-tls.yml") ;;
    esac
  fi
  logged compose-up-floci-az azure "docker compose up floci-az failed" \
    -- "${COMPOSE[@]}" --profile azure up -d floci-az
  local portlog
  portlog="$(smoke_log_dir)/compose-port-floci-az.log"
  local published
  published="$("${COMPOSE[@]}" --profile azure port floci-az 4577 2>"$portlog" | sed 's/.*://')"
  [ -n "$published" ] || fail "azure" "could not resolve floci-az's published port; docker compose port said: $(tr '\n' ' ' < "$portlog")"
  [ "$published" = "$FLOCI_AZ_PORT" ] || fail "azure" "floci-az is published on :$published, not the :$FLOCI_AZ_PORT its storage suffix names, so blob calls would miss it"
  # localhost, not 127.0.0.1: the generated certificate names localhost
  # and *.localhost, and no IP the provider would dial by name. The
  # operator's certificate names both.
  AZURE_HOST="localhost:${FLOCI_AZ_PORT}"
  AZURE_ENDPOINT="https://${AZURE_HOST}"
  export AZURE_HOST AZURE_ENDPOINT

  # Readiness is /ready and not /_floci/health: health answers as soon as
  # the HTTP server is up, /ready only once every service has finished its
  # startup work. The image is a JVM build, so this takes seconds, not
  # milliseconds. Bounded by the wall clock and per probe (#1457): a probe
  # that hangs cannot stretch the bound.
  local deadline
  deadline=$(( $(date +%s) + FLOCI_AZ_READY_SECS ))
  until curl -fsS --max-time 2 "http://${AZURE_HOST}/ready" >/dev/null 2>&1; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      local upinfo state
      upinfo="$(smoke_log_dir)/floci-az-container.log"
      state="$("${COMPOSE[@]}" --profile azure ps -a --format '{{.State}} - {{.Status}}' floci-az 2>&1 | tr '\n' ';')"
      "${COMPOSE[@]}" --profile azure logs --no-color --tail 200 floci-az > "$upinfo" 2>&1 || true
      smoke_log_tail "$upinfo" "the floci-az container"
      fail "azure" "floci-az never answered /ready on :${FLOCI_AZ_PORT} within ${FLOCI_AZ_READY_SECS}s; the container is [${state:-unknown}]. Its log is above, and whole in $upinfo. Raise the bound with FLOCI_AZ_READY_SECS=<seconds>."
    fi
    sleep 1
  done

  # The operator's certificate is its own CA. Without one, the CA is
  # generated at container start, so it is fetched per run and never
  # committed; it is served over plain HTTP on the same port.
  if [ -n "$FLOCI_AZ_TLS_CERT_FILE" ]; then
    AZURE_CA_FILE="$FLOCI_AZ_TLS_CERT_FILE"
  else
    AZURE_CA_FILE="$SMOKE_WORKROOT/floci-az-ca.pem"
    curl -fsS --max-time 10 "http://${AZURE_HOST}/_floci/tls-cert" -o "$AZURE_CA_FILE" 2>"$(smoke_log_dir)/floci-az-ca.err" \
      || fail "azure" "could not fetch floci-az's CA from /_floci/tls-cert: $(cat "$(smoke_log_dir)/floci-az-ca.err")"
    grep -q "BEGIN CERTIFICATE" "$AZURE_CA_FILE" \
      || fail "azure" "/_floci/tls-cert answered without a PEM certificate, so TLS is off in the container: $(head -c 300 "$AZURE_CA_FILE")"
  fi
  export AZURE_CA_FILE
  azure_trust_bundle

  # One HTTPS read through the CA, the read azurerm makes first. A failure
  # here names the TLS path before any provider gets to.
  curl -fsS --max-time 10 --cacert "$AZURE_CA_FILE" "${AZURE_ENDPOINT}/metadata/endpoints?api-version=2022-09-01" \
    > "$(smoke_log_dir)/floci-az-metadata.json" 2>"$(smoke_log_dir)/floci-az-metadata.err" \
    || fail "azure" "floci-az is ready but ${AZURE_ENDPOINT}/metadata/endpoints did not answer over TLS with its own CA: $(cat "$(smoke_log_dir)/floci-az-metadata.err")"

  azure_provider_env
  echo "  azure emulator up at $AZURE_ENDPOINT (compose project choudoufu-smoke-$SMOKE_ID, $FLOCI_AZ_IMAGE)"
}

# azure_trust_bundle makes the providers trust floci-az's CA. Go reads
# SSL_CERT_FILE on Linux, where CI runs, and the bundle keeps the system
# roots too, because the same process still downloads providers from the
# public registry during init. macOS's Go ignores SSL_CERT_FILE and asks
# the keychain, which azure_up has already checked trusts the operator's
# certificate (floci_az_tls_select), so there it adds nothing.
azure_trust_bundle() {
  if [ "$(uname -s)" = "Darwin" ]; then
    note "macOS: azurerm and azapi verify floci-az through the keychain, which trusts $AZURE_CA_FILE"
    return 0
  fi
  local system="" f
  for f in "${SSL_CERT_FILE:-}" /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/cert.pem; do
    if [ -n "$f" ] && [ -s "$f" ]; then system="$f"; break; fi
  done
  [ -n "$system" ] || fail "azure" "found no system CA bundle to add floci-az's CA to; set SSL_CERT_FILE to one"
  cat "$system" "$AZURE_CA_FILE" > "$SMOKE_WORKROOT/ca-bundle-with-floci-az.pem"
  export SSL_CERT_FILE="$SMOKE_WORKROOT/ca-bundle-with-floci-az.pem"
}

# azure_provider_env aims stock hashicorp/azurerm 5.10.0, azure/azapi
# 2.13.0, OpenTofu's stock azurerm backend and record_store "azurerm" at
# floci-az, and takes the operator's own Azure credentials out of reach:
# live/e2e/lib/floci-az.sh's floci_az_provider_env (the endpoint lock,
# #2133), which the gauntlet's gauntlet_floci_az_up runs too, and whose
# comment cites each provider's source for every variable it sets or
# unexports. The Azure CLI's config directory becomes an empty one under
# the run's work root, so no `az login` is found even by a client told to
# look. The provider blocks state the same values (azure_terraform_block),
# which win over this environment where the providers read both.
azure_provider_env() {
  floci_az_provider_env "$AZURE_HOST" "$AZURE_SUBSCRIPTION_ID" "$AZURE_TENANT_ID" "$AZURE_CLIENT_ID" \
    "$SMOKE_WORKROOT/azure-config-no-login" \
    || fail "azure" "could not lock the Azure environment to floci-az at $AZURE_HOST"
}

# azure_down removes the one container azure_up started, and its anonymous
# /app/data volume (the image declares one, and stack_down's `down` keeps
# volumes). The compose network goes with stack_down, which smoke.sh's
# cleanup runs after this. Quiet for the reason stack_down gives.
azure_down() {
  [ "$AZURE_UP" = "1" ] || return 0
  "${COMPOSE[@]}" --profile azure rm --stop --force -v floci-az >/dev/null 2>&1 || true
  AZURE_UP=0
}

# arm is one ARM management-plane call against floci-az, the Azure
# scenarios' out-of-band hand: what `az` would send, with no `az` on PATH
# and no login. arm <METHOD> <path-with-api-version> [json-body] prints the
# response body and fails (non-zero) on a non-2xx answer, so `|| fail`
# after it names the call. floci-az accepts any bearer token on ARM
# (live/FLOCI.md, Azure), and this one carries no oid claim, so RBAC
# enforcement does not check it: it is the out-of-band hand that sets an
# estate's role assignments up. Policy evaluation still applies to it.
# Bounded per call like every other request a scenario makes (#1457).
ARM_REQUEST_SECS="${ARM_REQUEST_SECS:-30}"
arm() {
  local method="$1" path="$2" body="${3:-}"
  local args=(-sS --fail-with-body --max-time "$ARM_REQUEST_SECS" --cacert "$AZURE_CA_FILE"
    -X "$method" -H "Authorization: Bearer floci-az" -H "Content-Type: application/json")
  [ -n "$body" ] && args+=(--data "$body")
  curl "${args[@]}" "${AZURE_ENDPOINT}${path}"
}

# arm_tags <resource-id> <api-version> prints the resource's tags as one
# JSON object, keys as ARM stored them. Read through ARM, not through
# choudoufu, so a marker check here is a check of what the cloud holds.
arm_tags() {
  arm GET "$1?api-version=$2" | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin).get("tags") or {}, sort_keys=True))'
}

# arm_set_tag <path-with-api-version> <key> <value|-> sets one tag on an ARM
# resource, or removes it for "-", and keeps every other tag and field: a
# GET, the tag edited in the body read back, and a PUT of that whole body,
# what `az resource update --set tags.k=v` does. The scenarios' out-of-band
# tag edit, as gauntlet_arm_set_tag is the gauntlet's. Not a PATCH:
# floci-az answers 405 to every PATCH on Microsoft.Network (lex00/floci-az#18,
# NetworkService.handleArm at 30000b0). A key that matches another only by
# case replaces it, since ARM rejects two such keys.
arm_set_tag() {
  local path="$1" key="$2" val="$3" body
  body="$(arm GET "$path" | KEY="$key" VAL="$val" python3 -c '
import json, os, sys
r = json.load(sys.stdin)
k, v = os.environ["KEY"], os.environ["VAL"]
t = {a: b for a, b in (r.get("tags") or {}).items() if a.lower() != k.lower()}
if v != "-":
    t[k] = v
r["tags"] = t
print(json.dumps(r))')" || return 1
  arm PUT "$path" "$body"
}

# azure_terraform_block <estate> [record-store-hcl] prints an Azure
# fixture's terraform block and its two provider blocks: azurerm and azapi
# pinned exactly to live/oracle-versions.json's azurerm_provider_version and
# azapi_provider_version (#1122's pins), a live block for <estate>, and the
# record store given (record_store "local" when none is). The provider
# blocks are live/e2e/lib/floci-az.sh's floci_az_provider_blocks: the
# subscription, because identity reads it from there the way AWS reads its
# region (#1120's corrections), and every endpoint and credential argument
# (the endpoint lock, #2133), so the operator's shell cannot aim either
# provider anywhere but this run's floci-az. Called after azure_up, which
# sourced floci-az.sh and knows the host.
azure_terraform_block() {
  local estate="$1" store="${2:-}" azurerm_pin azapi_pin
  azurerm_pin="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["azurerm_provider_version"])' "$ROOT/live/oracle-versions.json")" || return 1
  azapi_pin="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["azapi_provider_version"])' "$ROOT/live/oracle-versions.json")" || return 1
  [ -n "$store" ] || store='record_store "local" {
      path = ".tofu-records"
    }'
  local blocks
  blocks="$(floci_az_provider_blocks "$AZURE_HOST" "$AZURE_SUBSCRIPTION_ID" "$AZURE_TENANT_ID" "$AZURE_CLIENT_ID")" || return 1
  cat <<EOF
terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "= ${azurerm_pin}"
    }
    azapi = {
      source  = "azure/azapi"
      version = "= ${azapi_pin}"
    }
  }
  live {
    estate = "${estate}"
    ${store}
  }
}

$blocks
EOF
}

# gcp_up stands up floci-gcp for a GCP scenario (#1119), the third harness
# beside stack_up (floci) and cluster_up (kind): the pinned image from
# live/floci-gcp-image, in the run's own compose project, on its own port.
# It exports what hashicorp/google and the scenario's own reads need:
#
#   GCP_ENDPOINT               http://localhost:<port>, every emulated API
#   GOOGLE_OAUTH_ACCESS_TOKEN  a fake bearer token; the emulator does not
#                              validate credentials unless IAM enforcement
#                              is on, and then only its own minted tokens
#   GOOGLE_PROJECT, GOOGLE_CLOUD_PROJECT
#                              SMOKE_GCP_PROJECT; the emulator registers a
#                              project the first time a request names it
#   GOOGLE_REGION, GOOGLE_ZONE us-central1 and us-central1-a
#
# The endpoints themselves are not exported: gcp_provider_block writes them
# into the provider block, so a root reads the same way as on any other
# substrate and nothing depends on which environment variables a given
# provider release consults. FLOCI_GCP_IAM_ENFORCEMENT=enforce before
# gcp_up turns on the emulator's allow-policy evaluation, as
# FLOCI_IAM_ENFORCEMENT does for floci.
#
# What the fork's documentation confirms, and what it does not (read
# 2026-10-19 from lex00/floci-gcp's README, docs/services/compute.md,
# docs/services/iam.md and compatibility-tests/compat-opentofu/provider.tf):
#   confirmed  port 4588, GET /health, credentials not validated by default,
#              the storage, iam, pubsub and resource_manager endpoint paths
#              (the fork's own OpenTofu compatibility test sets them), and
#              Compute under /compute/v1/projects/{project}
#   assumed    compute_custom_endpoint = <endpoint>/compute/v1/, the
#              provider's default base path with the emulator's root; no
#              test in the fork sets it
#   read       cloud_asset_custom_endpoint = <endpoint>/cloudasset/v1/; the
#              emulator mounts Cloud Asset under that prefix (its path
#              collides with IAM's v1/projects, lex00/floci-gcp#7, served
#              in the pinned image)
#   assumed    iam_credentials_custom_endpoint = <endpoint>/v1/, where the
#              IAM guide says generateAccessToken lives but gives no path
#   assumed    secret_manager_custom_endpoint = <endpoint>/v1/; Secret
#              Manager is among the services the IAM guide says it
#              evaluates, and its REST paths sit under v1/projects as
#              Pub/Sub's do, but no test in the fork sets this argument
SMOKE_GCP_PROJECT="${SMOKE_GCP_PROJECT:-chdf-smoke-a}"
SMOKE_GCP_REGION="${SMOKE_GCP_REGION:-us-central1}"
SMOKE_GCP_ZONE="${SMOKE_GCP_ZONE:-us-central1-a}"
GCP_UP=0
gcp_up() {
  logged compose-up-floci-gcp stack "docker compose up floci-gcp failed" \
    -- "${COMPOSE[@]}" up -d floci-gcp
  GCP_UP=1
  local portlog
  portlog="$(smoke_log_dir)/compose-port-floci-gcp.log"
  FLOCI_GCP_PORT="$("${COMPOSE[@]}" port floci-gcp 4588 2>"$portlog" | sed 's/.*://')"
  [ -n "$FLOCI_GCP_PORT" ] || fail "stack" "could not resolve floci-gcp's published port; docker compose port said: $(tr '\n' ' ' < "$portlog")"
  export FLOCI_GCP_PORT
  GCP_ENDPOINT="http://localhost:${FLOCI_GCP_PORT}"
  export GCP_ENDPOINT
  # Unconditional: a token already in the environment may be a real one.
  export GOOGLE_OAUTH_ACCESS_TOKEN="chdf-smoke-fake-token"
  export GOOGLE_PROJECT="$SMOKE_GCP_PROJECT" GOOGLE_CLOUD_PROJECT="$SMOKE_GCP_PROJECT"
  export GOOGLE_REGION="$SMOKE_GCP_REGION" GOOGLE_ZONE="$SMOKE_GCP_ZONE"
  echo "  GCP emulator up at $GCP_ENDPOINT (compose project choudoufu-smoke-$SMOKE_ID, project $SMOKE_GCP_PROJECT)"
  local i
  for i in $(seq 1 30); do
    curl -fsS --max-time 5 "${GCP_ENDPOINT}/health" >/dev/null 2>&1 && return 0
    sleep 1
  done
  # The same reasoning as stack_up's: the container's own state and log
  # name the cause, the thirty refused probes do not.
  local upinfo state
  upinfo="$(smoke_log_dir)/floci-gcp-container.log"
  state="$("${COMPOSE[@]}" ps -a --format '{{.State}} - {{.Status}}' floci-gcp 2>&1 | tr '\n' ';')"
  "${COMPOSE[@]}" logs --no-color --tail 200 floci-gcp > "$upinfo" 2>&1 || true
  smoke_log_tail "$upinfo" "the floci-gcp container"
  fail "stack" "floci-gcp never answered /health on :${FLOCI_GCP_PORT} after 30 probes over 30s; the container is [${state:-unknown}]. Its log is above, and whole in $upinfo"
}

# See stack_down above for why this one stays quiet. stack_down's compose
# down removes the container too; this is for a scenario that brings
# floci-gcp down and back up mid-run, and for smoke.sh's EXIT trap.
gcp_down() {
  [ "$GCP_UP" = "1" ] || return 0
  "${COMPOSE[@]}" rm -sf floci-gcp >/dev/null 2>&1 || true
  GCP_UP=0
}

# gcp_endpoint_paths prints one "<provider argument> <path>" pair per
# service a GCP root reaches, the path joined to GCP_ENDPOINT. The reference
# estate's services (Compute, Cloud Storage, IAM, Pub/Sub), Resource
# Manager and Service Usage for project-level resources and the project
# grant, Cloud Asset for the sweep, IAM Credentials for an impersonated
# token, and Secret Manager for claim 51's record-located type. Which of
# these are confirmed is gcp_up's comment above.
gcp_endpoint_paths() {
  cat <<'PATHS'
compute_custom_endpoint compute/v1/
storage_custom_endpoint storage/v1/
iam_custom_endpoint -
iam_beta_custom_endpoint v1/
pubsub_custom_endpoint v1/
resource_manager_custom_endpoint v1/
service_usage_custom_endpoint v1/
cloud_asset_custom_endpoint cloudasset/v1/
iam_credentials_custom_endpoint v1/
cloud_billing_custom_endpoint v1/
secret_manager_custom_endpoint v1/
PATHS
}

# gcp_provider_block [project] prints a provider "google" block pointed at
# the run's emulator, with every endpoint gcp_endpoint_paths names. Call it
# after gcp_up. The project defaults to SMOKE_GCP_PROJECT.
gcp_provider_block() {
  local project="${1:-$SMOKE_GCP_PROJECT}" arg path paths
  : "${GCP_ENDPOINT:?gcp_provider_block needs gcp_up first}"
  paths="$(gcp_all_endpoint_paths)" || return 1
  case "${GOOGLE_OAUTH_ACCESS_TOKEN:-}" in
    chdf-*) ;;
    *) printf 'gcp_provider_block: GOOGLE_OAUTH_ACCESS_TOKEN is not a chdf- fake token\n' >&2; return 1 ;;
  esac
  printf 'provider "google" {\n'
  printf '  project = "%s"\n  region  = "%s"\n  zone    = "%s"\n  access_token = "%s"\n\n' "$project" "$SMOKE_GCP_REGION" "$SMOKE_GCP_ZONE" "$GOOGLE_OAUTH_ACCESS_TOKEN"
  while read -r arg path; do
    [ "$path" = "-" ] && path=""
    printf '  %s = "%s/%s"\n' "$arg" "$GCP_ENDPOINT" "$path"
  done <<< "$paths"
  printf '}\n'
}

# gcp_all_endpoint_paths: gcp_endpoint_paths, then every other
# *_custom_endpoint argument of the pinned provider at v1/, so the run fails
# closed against the emulator rather than reaching googleapis.com. The list
# is live/e2e/lib/gcp-provider-endpoints.txt, shared with the gauntlet.
gcp_all_endpoint_paths() {
  local file
  file="$(cd "$(dirname "${BASH_SOURCE[0]}")/../e2e/lib" && pwd)/gcp-provider-endpoints.txt"
  [ -r "$file" ] || { printf 'gcp_all_endpoint_paths: %s is unreadable\n' "$file" >&2; return 1; }
  local named
  named="$(gcp_endpoint_paths)"
  printf '%s\n' "$named"
  grep -v '^#' "$file" | while read -r arg; do
    [ -n "$arg" ] || continue
    printf '%s\n' "$named" | grep -q "^$arg " || printf '%s v1/\n' "$arg"
  done
}

# gcp_required_provider prints the hashicorp/google entry of a
# required_providers block, pinned to live/oracle-versions.json's
# google_provider_version (8.6.0 under #1121), the way the k8s fixture pins
# hashicorp/kubernetes.
gcp_required_provider() {
  local pin
  pin="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['google_provider_version'])" "$ROOT/live/oracle-versions.json")" \
    || fail "stack" "could not read google_provider_version from live/oracle-versions.json"
  printf '    google = {\n      source  = "hashicorp/google"\n      version = "= %s"\n    }\n' "$pin"
}

# gcp_write_root <dir> <estate> [project] [live-block extra] writes
# <dir>/versions.tf: the terraform block with the pinned hashicorp/google
# requirement and a live block for <estate>, and gcp_provider_block's
# provider. The extra text goes inside the live block (a record_store
# block, say). Scenarios write their resources beside it. A GCP scenario
# writes its root at run time rather than copying a fixture, because the
# endpoints carry the port gcp_up was given.
gcp_write_root() {
  local dir="$1" estate="$2" project="${3:-$SMOKE_GCP_PROJECT}" extra="${4:-}" req
  req="$(gcp_required_provider)" || return 1
  mkdir -p "$dir"
  {
    printf 'terraform {\n  required_providers {\n%s\n  }\n\n  live {\n    estate = "%s"\n' "$req" "$estate"
    [ -z "$extra" ] || printf '%s\n' "$extra"
    printf '  }\n}\n\n'
    gcp_provider_block "$project" || fail "stack" "gcp_provider_block refused (the line above says why); no root is written without the emulator endpoints"
  } > "$dir/versions.tf"
}

# gcpl is the emulator's REST API with the run's token: gcpl <METHOD>
# <path> [json body]. Every GCP scenario reads the cloud through this and
# never through choudoufu, the way an AWS scenario uses awsl. Bounded, for
# kc's reason (#1457).
GCP_REQUEST_TIMEOUT="${GCP_REQUEST_TIMEOUT:-30}"
gcpl() {
  local method="$1" path="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl -fsS --max-time "$GCP_REQUEST_TIMEOUT" -X "$method" \
      -H "Authorization: Bearer ${GCPL_TOKEN:-$GOOGLE_OAUTH_ACCESS_TOKEN}" -H 'Content-Type: application/json' \
      --data "$body" "$GCP_ENDPOINT/$path"
  else
    curl -fsS --max-time "$GCP_REQUEST_TIMEOUT" -X "$method" \
      -H "Authorization: Bearer ${GCPL_TOKEN:-$GOOGLE_OAUTH_ACCESS_TOKEN}" "$GCP_ENDPOINT/$path"
  fi
}

# gcp_field <python expression over d> reads one value out of the JSON on
# stdin, printing nothing for a missing key: gcpl GET ... | gcp_field
# 'd.get("labels", {}).get("tofu-estate", "")'.
gcp_field() {
  python3 -c "import json,sys
d=json.load(sys.stdin)
v=eval(sys.argv[1])
print(json.dumps(v, sort_keys=True) if isinstance(v,(dict,list)) else ('' if v is None else v))" "$1"
}

# gcp_address_label <block address> <instance address> prints the
# tofu-address label choudoufu writes for that instance: two 26-character
# lowercase base32 prefixes of SHA-256, block then instance, joined by "-"
# (#1121; markers.GoogleAddressValue, whose test vectors this reproduces).
# The block is the address without the resource's own instance key.
gcp_address_label() {
  python3 - "$1" "$2" <<'PY'
import base64, hashlib, sys
enc = lambda s: base64.b32encode(hashlib.sha256(s.encode()).digest()).decode().lower().rstrip("=")[:26]
print(enc(sys.argv[1]) + "-" + enc(sys.argv[2]))
PY
}

# gcp_bucket_has_member <bucket> <role> <member> answers whether the
# bucket's IAM policy, read with the REST API, grants <role> to <member>.
# gcp_bucket_drop_member takes that one member out of that one role and
# writes the policy back: the out-of-band delete of a
# google_storage_bucket_iam_member, which carries no label and is found by
# the identity its configuration computes (#2119). GCPL_TOKEN picks whose
# write it is, as for gcpl.
gcp_bucket_has_member() {
  gcpl GET "storage/v1/b/$1/iam" | python3 -c "import json,sys
d=json.load(sys.stdin)
sys.exit(0 if any(b.get('role')==sys.argv[1] and sys.argv[2] in b.get('members',[]) for b in d.get('bindings',[])) else 1)" "$2" "$3"
}
gcp_bucket_drop_member() {
  local policy
  policy="$(gcpl GET "storage/v1/b/$1/iam")" || return 1
  policy="$(python3 -c "import json,sys
d=json.load(sys.stdin)
for b in d.get('bindings',[]):
    if b.get('role')==sys.argv[1]:
        b['members']=[m for m in b.get('members',[]) if m!=sys.argv[2]]
d['bindings']=[b for b in d.get('bindings',[]) if b.get('members')]
print(json.dumps(d))" "$2" "$3" <<< "$policy")" || return 1
  gcpl PUT "storage/v1/b/$1/iam" "$policy" >/dev/null
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
#
# The trap goes on before sleep is forked, not after (#1593, measured under
# CPU pressure that starves this subshell of scheduling): a TERM landing in
# the gap between the fork and installing the trap left the sleep behind as
# a live orphan when this subshell exited without it. Reordering only
# narrows that gap - a TERM can still land between the fork completing and
# `nap=$!` running, since bash only checks for one at a command boundary -
# so the sleep also has SMOKE_ERR_FD explicitly closed: it is smoke.sh's
# real fd 9, inherited into every child by default, and an orphan holding
# it open blocks any `$(...)` capturing smoke.sh's output for up to the
# full bound, seconds after the scenario itself already printed its
# verdict and exited. Measured live under stress-ng: reordering alone still
# left the sleep as a pipe-holding orphan; closing its copy of fd 9 does
# not, however it is left behind. nap is empty until the fork returns;
# killing an empty pid is a no-op.
smoke_timer() { # <secs> <before> <after>, the two halves of smoke_stall's verdict
  (
    nap=""
    trap 'kill "$nap" 2>/dev/null; exit 0' TERM
    sleep "$1" >/dev/null 2>&1 9>&- &
    nap=$!
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
  logged compose-oracle-plugins stack "could not prepare the oracle's plugin volume" \
    -- "${COMPOSE[@]}" run --rm --user 0 --entrypoint sh opentofu -c "chown -R $(id -u):$(id -g) '$SMOKE_PLUGINS_MOUNT'"
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

# k8s_counting_proxy_up <dir> <tag> starts live/smoke/k8sproxy.py in front
# of the run's kind API server with no fault armed, so it only relays and
# writes one line per request to <dir>/proxy.log ("METHOD path?query
# status"). That log counts what ONE client sent, which the API server's own
# apiserver_request_total cannot: the cluster's controllers are on that
# counter too. It sets PROXY_PID and PROXY_KC, a copy of the run's
# kubeconfig pointed at the proxy; a caller runs a measured command under
# KUBECONFIG and KUBE_CONFIG_PATH set to PROXY_KC, and kills PROXY_PID on
# exit. The setup is k8s-records-in-the-cluster's step 12, which is where
# the proxy's TLS arrangement is explained.
PROXY_PID=""
PROXY_KC=""
k8s_counting_proxy_up() {
  local dir="$1" tag="$2" server
  mkdir -p "$dir"
  server="$(kubectl --kubeconfig "$KUBECONFIG" config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')"
  [ -n "$server" ] || fail "$tag" "the kind kubeconfig names no server"
  kubectl --kubeconfig "$KUBECONFIG" config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d > "$dir/upstream-ca.crt"
  kubectl --kubeconfig "$KUBECONFIG" config view --raw --minify -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > "$dir/upstream-client.crt"
  kubectl --kubeconfig "$KUBECONFIG" config view --raw --minify -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > "$dir/upstream-client.key"
  local f
  for f in upstream-ca.crt upstream-client.crt upstream-client.key; do
    [ -s "$dir/$f" ] || fail "$tag" "the kind kubeconfig yielded no $f, so the proxy has nothing to reach the API server with"
  done
  openssl req -x509 -newkey rsa:2048 -nodes -keyout "$dir/proxy.key" -out "$dir/proxy.crt" -days 1 \
    -subj /CN=smoke-proxy -addext subjectAltName=IP:127.0.0.1 >/dev/null 2>&1 \
    || fail "$tag" "openssl could not write the proxy's certificate"
  python3 "$SMOKE_DIR/k8sproxy.py" "$server" "$dir" 2>"$(smoke_log_dir)/$tag-proxy.err" &
  PROXY_PID=$!
  for _ in $(seq 1 50); do [ -s "$dir/proxy.port" ] && break; sleep 0.1; done
  [ -s "$dir/proxy.port" ] || fail "$tag" "the proxy never started: $(cat "$(smoke_log_dir)/$tag-proxy.err")"
  PROXY_KC="$dir/proxy.kubeconfig"
  cp "$KUBECONFIG" "$PROXY_KC"
  kubectl --kubeconfig "$PROXY_KC" config set-cluster "kind-$CLUSTER_NAME" \
    --server="https://127.0.0.1:$(cat "$dir/proxy.port")" --insecure-skip-tls-verify=true >/dev/null
  [ -z "$(kubectl --kubeconfig "$PROXY_KC" config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')" ] \
    || fail "$tag" "the proxy kubeconfig still carries the kind CA beside insecure-skip-tls-verify, which clientcmd refuses"
  kc_as "$PROXY_KC" get namespace kube-system >/dev/null 2>&1 \
    || fail "$tag" "a read through the proxy failed, so it does not relay: $(cat "$(smoke_log_dir)/$tag-proxy.err")"
}
