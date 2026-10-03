#!/usr/bin/env bash
# Copyright (c) The OpenTofu Authors
# SPDX-License-Identifier: MPL-2.0
# Copyright (c) 2023 HashiCorp, Inc.
# SPDX-License-Identifier: MPL-2.0
#
# live/large-set/oci.sh - the OCI variant of the #1750 fixture, end to end on
# this machine: a local registry, the shared module published at 1.0.0 (A)
# and 1.1.0 (B), every root pinned at 1.0.0 and applied against a scratch
# floci, then a pin bump across a SUBSET of roots and what each root's plan
# does about it. Nothing paid; two containers, both removed on exit unless
# LARGESET_KEEP=1.
#
#   live/large-set/oci.sh                  # N=5, bump e02,e04
#   PIN_B=e02 live/large-set/oci.sh
#
# Prints per-root lines on stderr and ONE verdict line on stdout:
#
#   LARGESET-OCI: green - pin bump e02,e04 moved e02 (1/3/0) e04 (1/4/0); left e01 e03 e05 unmoved
#
# It is red when any apply fails, when a root's plan at 1.0.0 is not empty,
# when a bumped root does not move or an unbumped one does, or when
# live-check reads a bumped root's stale package as current before re-init
# (the #1750 finding, fixed in internal/live/check/load.go).
#
# Environment:
#   CHOUDOUFU_BIN  the binary (default: build ./cmd/choudoufu)
#   OCI_PORT       registry port (default 4890; lane A owns 4800-4899)
#   FLOCI_PORT     floci port (default 4881)
#   ESTATES        N (default 5; above 5 is the maintainer's call)
#   PIN_B          roots to bump (default e02,e04)
#   WORK           work dir (default a mktemp dir)
#   LARGESET_KEEP  1 leaves the registry and floci running
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
OCI_PORT="${OCI_PORT:-4890}"
FLOCI_PORT="${FLOCI_PORT:-4881}"
ESTATES="${ESTATES:-5}"
PIN_B="${PIN_B:-e02,e04}"
WORK="${WORK:-$(mktemp -d "${TMPDIR:-/tmp}/largeset-oci.XXXXXX")}"
REGISTRY_NAME=cdfa-oci-registry
FLOCI_NAME=cdfa-largeset-oci-floci
REGISTRY="localhost:$OCI_PORT"

verdict() {
  local colour="$1"
  shift
  printf 'LARGESET-OCI: %s - %s\n' "$colour" "$*"
  [ "$colour" = green ]
  exit $?
}
log() { printf 'largeset-oci: %s\n' "$*" >&2; }

cleanup() {
  if [ "${LARGESET_KEEP:-}" = 1 ]; then
    log "LARGESET_KEEP=1: leaving $REGISTRY_NAME and $FLOCI_NAME running (docker rm -f $REGISTRY_NAME $FLOCI_NAME)"
    return
  fi
  docker rm -f "$REGISTRY_NAME" "$FLOCI_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

[ "$ESTATES" -le 5 ] || [ "${LARGESET_MAINTAINER_GO:-}" = 1 ] \
  || verdict red "ESTATES=$ESTATES: anything above 5 runs only on the maintainer's go (#1750)"
for c in "$REGISTRY_NAME" "$FLOCI_NAME"; do
  docker inspect "$c" >/dev/null 2>&1 && verdict red "a container named $c already exists; remove it first (docker rm -f $c)"
done
mkdir -p "$WORK/certs" "$WORK/logs"
log "work dir $WORK"

if [ -z "${CHOUDOUFU_BIN:-}" ]; then
  CHOUDOUFU_BIN="$WORK/choudoufu"
  (cd "$ROOT" && env -u PWD go build -o "$CHOUDOUFU_BIN" ./cmd/choudoufu) >"$WORK/logs/build.log" 2>&1 \
    || verdict red "go build ./cmd/choudoufu failed; log $WORK/logs/build.log"
fi

# --- 1. A registry speaking HTTPS. Stock's module getter has no plain-HTTP
# mode, so the registry gets a certificate from a throwaway CA.
(
  cd "$WORK/certs" || exit 1
  openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.pem -days 2 \
    -subj "/CN=cdfa largeset test CA" \
    -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" &&
    openssl req -newkey rsa:2048 -nodes -keyout server.key -out server.csr -subj "/CN=localhost" &&
    printf 'subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' >ext.cnf &&
    openssl x509 -req -in server.csr -CA ca.pem -CAkey ca.key -CAcreateserial -out server.crt -days 2 -extfile ext.cnf &&
    chmod 644 server.key
) >"$WORK/logs/certs.log" 2>&1 || verdict red "creating the registry certificate failed; log $WORK/logs/certs.log"

# SSL_CERT_FILE replaces the system roots for the Go binary, so the bundle
# carries both: the system's (for the provider registry init still asks) and
# the test CA (for the module registry).
if [ -r /etc/ssl/certs/ca-certificates.crt ]; then
  cat /etc/ssl/certs/ca-certificates.crt >"$WORK/certs/bundle.pem"
elif command -v security >/dev/null 2>&1; then
  security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain >"$WORK/certs/bundle.pem"
else
  verdict red "cannot find the system CA roots to build an SSL_CERT_FILE bundle"
fi
cat "$WORK/certs/ca.pem" >>"$WORK/certs/bundle.pem"
export SSL_CERT_FILE="$WORK/certs/bundle.pem"

docker run -d --name "$REGISTRY_NAME" -p "127.0.0.1:$OCI_PORT:$OCI_PORT" \
  -e "REGISTRY_HTTP_ADDR=0.0.0.0:$OCI_PORT" \
  -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/server.crt -e REGISTRY_HTTP_TLS_KEY=/certs/server.key \
  -v "$WORK/certs:/certs:ro" registry:2 >"$WORK/logs/registry.log" 2>&1 \
  || verdict red "docker run for $REGISTRY_NAME failed; log $WORK/logs/registry.log"
up=""
for _ in $(seq 1 30); do
  curl -fs --cacert "$WORK/certs/ca.pem" "https://$REGISTRY/v2/" >/dev/null 2>&1 && up=1 && break
  sleep 1
done
[ -n "$up" ] || verdict red "the registry did not answer at https://$REGISTRY/v2/"

# --- 2. Publish A and B.
(cd "$ROOT" && env -u PWD go run ./tools/largeset-gen publish -registry "$REGISTRY" -ca "$WORK/certs/ca.pem") \
  >"$WORK/logs/publish.log" 2>&1 || verdict red "publishing the module failed; log $WORK/logs/publish.log"
log "$(tr '\n' ' ' <"$WORK/logs/publish.log")"

gen() {
  (cd "$ROOT" && env -u PWD go run ./tools/largeset-gen -estates "$ESTATES" -module-version A \
    -source oci -registry "$REGISTRY" -out "$WORK/fixture" "$@") >>"$WORK/logs/gen.log" 2>&1
}
gen || verdict red "generating the OCI fixture failed; log $WORK/logs/gen.log"

# --- 3. Apply every root at 1.0.0.
applied="$(CHOUDOUFU_BIN="$CHOUDOUFU_BIN" FLOCI_PORT="$FLOCI_PORT" FLOCI_NAME="$FLOCI_NAME" \
  LARGESET_LOG_DIR="$WORK/logs/apply" bash "$ROOT/live/large-set/apply.sh" "$WORK/fixture")"
log "$applied"
case "$applied" in "LARGESET-APPLY: green"*) ;; *) verdict red "the apply at 1.0.0 did not go green: $applied" ;; esac

export AWS_ENDPOINT_URL="http://localhost:$FLOCI_PORT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

summary() { grep -E '^(Plan: |No changes\.)' "$1" | head -1 | sed 's/ Your infrastructure matches the configuration\.//'; }
totals() { sed -n 's/^Plan: \([0-9]*\) to add, \([0-9]*\) to change, \([0-9]*\) to destroy\./\1\/\2\/\3/p' "$1" | head -1; }

roots=()
while IFS= read -r d; do [ -n "$d" ] && roots+=("$d"); done <"$WORK/fixture/apply-order.txt"

# --- 4. The four tools at 1.0.0, every root.
for d in "${roots[@]}"; do
  n="$(basename "$d")"
  (cd "$WORK/fixture/$d" && "$CHOUDOUFU_BIN" live-check) >"$WORK/logs/$n.check-A.log" 2>&1
  rc=$?
  [ $rc -eq 0 ] || verdict red "live-check refused $n at 1.0.0 (exit $rc); log $WORK/logs/$n.check-A.log"
  grep -q 'could not be read without installing' "$WORK/logs/$n.check-A.log" \
    && verdict red "live-check did not read $n's installed oci:// module; log $WORK/logs/$n.check-A.log"
  (cd "$WORK/fixture/$d" && "$CHOUDOUFU_BIN" live-plan -no-color) >"$WORK/logs/$n.plan-A.log" 2>&1 \
    || verdict red "live-plan failed in $n at 1.0.0; log $WORK/logs/$n.plan-A.log"
  s="$(summary "$WORK/logs/$n.plan-A.log")"
  log "$n at 1.0.0: live-check '$(grep -m1 'managed resource instance' "$WORK/logs/$n.check-A.log")' live-plan '$s'"
  [ "$s" = "No changes." ] || verdict red "$n's plan at 1.0.0 after its own apply is '$s', not empty"
done

# --- 5. The pin bump, a subset of roots.
gen -pin-b "$PIN_B" || verdict red "regenerating with -pin-b $PIN_B failed; log $WORK/logs/gen.log"
moved="" left=""
for d in "${roots[@]}"; do
  n="$(basename "$d")"
  bumped=""
  case ",$PIN_B," in *",$n,"*) bumped=1 ;; esac

  # Before re-init: a bumped root's installed package is the old version.
  # live-check must say the module is unread, never report the old one.
  (cd "$WORK/fixture/$d" && "$CHOUDOUFU_BIN" live-check) >"$WORK/logs/$n.check-stale.log" 2>&1
  if [ -n "$bumped" ] && ! grep -q 'could not be read without installing' "$WORK/logs/$n.check-stale.log"; then
    verdict red "live-check read $n's stale 1.0.0 package as the 1.1.0 its configuration pins; log $WORK/logs/$n.check-stale.log"
  fi
  (cd "$WORK/fixture/$d" && "$CHOUDOUFU_BIN" live-plan -no-color) >"$WORK/logs/$n.plan-stale.log" 2>&1
  stale_rc=$?
  if [ -n "$bumped" ] && [ $stale_rc -eq 0 ]; then
    verdict red "live-plan planned $n against its stale 1.0.0 package without asking for init; log $WORK/logs/$n.plan-stale.log"
  fi

  (cd "$WORK/fixture/$d" && "$CHOUDOUFU_BIN" init -input=false -no-color) >"$WORK/logs/$n.init-B.log" 2>&1 \
    || verdict red "re-init failed in $n; log $WORK/logs/$n.init-B.log"
  start=$(date +%s)
  (cd "$WORK/fixture/$d" && "$CHOUDOUFU_BIN" live-plan -no-color) >"$WORK/logs/$n.plan-B.log" 2>&1 \
    || verdict red "live-plan failed in $n after the pin bump; log $WORK/logs/$n.plan-B.log"
  secs=$(($(date +%s) - start))
  s="$(summary "$WORK/logs/$n.plan-B.log")"
  log "$n bumped=${bumped:-no} live-plan ${secs}s '$s' ($(grep -cE '^  # ' "$WORK/logs/$n.plan-B.log") resource lines)"
  if [ -n "$bumped" ]; then
    t="$(totals "$WORK/logs/$n.plan-B.log")"
    [ -n "$t" ] && [ "${t%%/*}/${t#*/}" != "0/0/0" ] || verdict red "$n's pin moved to 1.1.0 and its plan did not move ('$s')"
    moved="$moved $n ($t)"
  else
    [ "$s" = "No changes." ] || verdict red "$n's pin did not move and its plan did ('$s')"
    left="$left $n"
  fi
done

verdict green "pin bump $PIN_B moved${moved}; left${left} unmoved"
