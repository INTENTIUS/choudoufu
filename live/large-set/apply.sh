#!/usr/bin/env bash
# Copyright (c) The OpenTofu Authors
# SPDX-License-Identifier: MPL-2.0
# Copyright (c) 2023 HashiCorp, Inc.
# SPDX-License-Identifier: MPL-2.0
#
# live/large-set/apply.sh - apply every estate of a tools/largeset-gen fixture
# against a scratch floci, in the fixture's own apply order, and leave them
# applied (issue #1750, epic #1749).
#
#   go run ./tools/largeset-gen -estates 5 -module-version A -out /tmp/ls
#   FLOCI_PORT=4880 live/large-set/apply.sh /tmp/ls
#
# Prints exactly ONE line on stdout, the verdict, and everything else on
# stderr:
#
#   LARGESET-APPLY: green - 5/5 estates applied (source local, module A) against http://localhost:4880
#   LARGESET-APPLY: red - e03 failed at apply after 2/5 applied; log /tmp/ls/.largeset-logs/e03.apply.log
#
# Exit status follows the verdict, but read the line: a check is its verdict
# line, never its exit code (CLAUDE.md). internal/live/largeset's
# harness_test.go proves the line red when an apply fails.
#
# Environment:
#   CHOUDOUFU_BIN      the binary to run (default: build ./cmd/choudoufu)
#   LARGESET_ENDPOINT  an emulator already running; when set, no container is
#                      started (the baseline test points this at its counting
#                      proxy)
#   FLOCI_PORT         host port for the scratch floci this script starts when
#                      LARGESET_ENDPOINT is unset (default 4880, lane A's range)
#   FLOCI_NAME         its container name (default cdfa-largeset-floci)
#   LARGESET_LOG_DIR   per-estate logs (default <fixture>/.largeset-logs)
#
# The floci container this script starts is LEFT RUNNING, because the
# estates it applied live in it; the script says how to remove it. An OCI
# fixture's roots must already have their modules installed (oci.sh does
# that), so they are initialised with -get=false.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
FIXTURE="${1:-}"

verdict() {
  # $1 green|red, rest the reason. The only write to stdout.
  local colour="$1"
  shift
  printf 'LARGESET-APPLY: %s - %s\n' "$colour" "$*"
  [ "$colour" = green ]
  exit $?
}

log() { printf 'largeset-apply: %s\n' "$*" >&2; }

[ -n "$FIXTURE" ] || verdict red "usage: apply.sh <fixture-dir>"
[ -f "$FIXTURE/apply-order.txt" ] || verdict red "$FIXTURE holds no apply-order.txt; generate it with tools/largeset-gen first"
FIXTURE="$(cd "$FIXTURE" && pwd -P)"
LOG_DIR="${LARGESET_LOG_DIR:-$FIXTURE/.largeset-logs}"
mkdir -p "$LOG_DIR" || verdict red "cannot create log dir $LOG_DIR"

SOURCE="$(sed -n 's/^  "source": "\(.*\)",$/\1/p' "$FIXTURE/fixture.json")"
MODULE="$(sed -n 's/^  "module_version": "\(.*\)",$/\1/p' "$FIXTURE/fixture.json")"

if [ -z "${CHOUDOUFU_BIN:-}" ]; then
  CHOUDOUFU_BIN="$LOG_DIR/choudoufu"
  log "building ./cmd/choudoufu into $CHOUDOUFU_BIN"
  (cd "$ROOT" && env -u PWD go build -o "$CHOUDOUFU_BIN" ./cmd/choudoufu) >"$LOG_DIR/build.log" 2>&1 \
    || verdict red "go build ./cmd/choudoufu failed; log $LOG_DIR/build.log"
fi

if [ -n "${LARGESET_ENDPOINT:-}" ]; then
  ENDPOINT="$LARGESET_ENDPOINT"
else
  FLOCI_PORT="${FLOCI_PORT:-4880}"
  FLOCI_NAME="${FLOCI_NAME:-cdfa-largeset-floci}"
  FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
  ENDPOINT="http://localhost:$FLOCI_PORT"
  case "$FLOCI_NAME" in cdfa-*) ;; *) verdict red "FLOCI_NAME must start with cdfa- (lane A's container prefix), got $FLOCI_NAME" ;; esac
  if docker inspect "$FLOCI_NAME" >/dev/null 2>&1; then
    verdict red "a container named $FLOCI_NAME already exists; remove it (docker rm -f $FLOCI_NAME) or set FLOCI_NAME - an emulator holding another run's estates is not a scratch floci"
  fi
  log "starting $FLOCI_NAME on :$FLOCI_PORT ($FLOCI_IMAGE)"
  docker run -d --name "$FLOCI_NAME" -p "127.0.0.1:$FLOCI_PORT:4566" "$FLOCI_IMAGE" >"$LOG_DIR/floci.log" 2>&1 \
    || verdict red "docker run for $FLOCI_NAME failed; log $LOG_DIR/floci.log"
  healthy=""
  for _ in $(seq 1 90); do
    if curl -fs "$ENDPOINT/_localstack/health" >/dev/null 2>&1; then
      healthy=1
      break
    fi
    sleep 2
  done
  [ -n "$healthy" ] || verdict red "floci did not become healthy at $ENDPOINT within 180s"
  log "floci is up; it stays up so the estates stay applied - remove it with: docker rm -f $FLOCI_NAME"
fi

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-test}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-test}"
export AWS_REGION="${AWS_REGION:-us-east-1}"
if [ -z "${TF_PLUGIN_CACHE_DIR:-}" ] && [ -d "$HOME/Library/Caches/choudoufu-test-plugins" ]; then
  # The same shared cache internal/live/flocitest.PluginCacheDir uses.
  export TF_PLUGIN_CACHE_DIR="$HOME/Library/Caches/choudoufu-test-plugins"
  export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
fi

init_args=(init -input=false -no-color)
[ "$SOURCE" = oci ] && init_args+=(-get=false)

total=0
while IFS= read -r dir; do
  [ -n "$dir" ] && total=$((total + 1))
done <"$FIXTURE/apply-order.txt"

applied=0
while IFS= read -r dir; do
  [ -n "$dir" ] || continue
  name="$(basename "$dir")"
  log "$name: init"
  if ! (cd "$FIXTURE/$dir" && "$CHOUDOUFU_BIN" "${init_args[@]}") >"$LOG_DIR/$name.init.log" 2>&1; then
    verdict red "$name failed at init after $applied/$total applied; log $LOG_DIR/$name.init.log"
  fi
  log "$name: apply"
  if ! (cd "$FIXTURE/$dir" && "$CHOUDOUFU_BIN" apply -auto-approve -input=false -no-color) >"$LOG_DIR/$name.apply.log" 2>&1; then
    verdict red "$name failed at apply after $applied/$total applied; log $LOG_DIR/$name.apply.log"
  fi
  # An apply that exits 0 without saying so is not an apply this harness can
  # vouch for: read the line, not the status.
  if ! grep -q '^Apply complete!' "$LOG_DIR/$name.apply.log"; then
    verdict red "$name exited 0 from apply but printed no 'Apply complete!' after $applied/$total applied; log $LOG_DIR/$name.apply.log"
  fi
  applied=$((applied + 1))
done <"$FIXTURE/apply-order.txt"

[ "$total" -gt 0 ] || verdict red "apply-order.txt lists no estates"
verdict green "$applied/$total estates applied (source $SOURCE, module $MODULE) against $ENDPOINT"
