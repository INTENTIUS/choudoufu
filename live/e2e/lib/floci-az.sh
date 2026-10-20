#!/usr/bin/env bash
# live/e2e/lib/floci-az.sh: what both places that start floci-az need to
# agree on (#2140). Sourced, never run, by live/smoke/lib.sh's azure_up and
# live/e2e/lib/gauntlet.sh's gauntlet_floci_az_up, so the smoke compose
# service and the gauntlet's container run the emulator with the same
# storage suffix, Policy evaluation and RBAC enforcement. Pure functions:
# nothing here starts or stops a container.

# floci_az_client_oid <client-id> prints the object id floci-az puts in the
# oid claim of a client-credentials token for a client id its directory
# does not hold: Java's UUID.nameUUIDFromBytes over the id's UTF-8 bytes,
# an MD5 name-based (version 3) UUID with no namespace
# (services/entra/TokenEndpoint.java and TokenIssuer.deterministicGuid at
# lex00/floci-az 30000b0). RBAC enforcement keys on that claim, so the
# principal every provider block and backend signs in as is listed as an
# admin through it and passes unchecked.
floci_az_client_oid() {
  python3 -c 'import hashlib, sys, uuid; print(uuid.UUID(bytes=hashlib.md5(sys.argv[1].encode()).digest(), version=3))' "$1"
}

# floci_az_free_port prints a loopback port free at the moment of asking.
# floci-az's host port is chosen before the container starts, not by the
# kernel at publish time, because FLOCI_AZ_STORAGE_SUFFIX has to name it:
# /metadata/endpoints and storage accounts' primaryEndpoints report the
# suffix in place of core.windows.net (lex00/floci-az c760e26), so the
# stock azurerm backend and record_store "azurerm" address an account as
# https://<account>.blob.localhost:<port>, this container, instead of real
# Azure DNS. A port another process takes between this probe and the
# publish fails the start by name.
floci_az_free_port() {
  python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

# floci_az_service_env <port> <admin-oid> prints, one per line, the
# environment both starts give floci-az beyond TLS, storage mode and
# parity: the storage suffix naming <port>, Policy evaluation on (deny and
# denyAction are evaluated since 30000b0 only with it set), and RBAC
# enforcement on with <admin-oid> as its admin principal (bb225ad).
# live/smoke/docker-compose.yml states the same set for the compose
# service; TestFlociAzServiceEnvMatchesCompose holds the two together.
floci_az_service_env() {
  printf '%s\n' \
    "FLOCI_AZ_STORAGE_SUFFIX=localhost:$1" \
    "FLOCI_AZ_SERVICES_POLICY_EVALUATE=true" \
    "FLOCI_AZ_SERVICES_RBAC_ENFORCE=true" \
    "FLOCI_AZ_SERVICES_RBAC_ADMIN_PRINCIPALS=$2"
}

# ── a certificate of the operator's own (Azure lane on a Mac) ─────────────
#
# With TLS on and no certificate given, each floci-az generates a fresh CA
# when it starts (TlsConfigSource at lex00/floci-az 30000b0). Linux takes
# that CA through SSL_CERT_FILE per run; Go on macOS ignores SSL_CERT_FILE
# and trusts only the keychain, so azurerm and azapi there fail with an
# x509 error on the first call. scripts/floci-az-mac-cert.sh makes one
# certificate the maintainer trusts once; these helpers mount it into every
# floci-az both starts run. floci-az takes it through FLOCI_AZ_TLS_CERT_PATH
# and FLOCI_AZ_TLS_KEY_PATH, both PEM and both required (either blank and it
# generates its own), uses it as given with no SAN of its own added, and
# serves the certificate file itself at /_floci/tls-cert.

# FLOCI_AZ_TLS_IN_CONTAINER is where the certificate and key are mounted,
# read-only, outside /app/data (the image's entrypoint re-owns that tree).
FLOCI_AZ_TLS_IN_CONTAINER=/etc/floci-az-tls

# floci_az_tls_dir prints the directory scripts/floci-az-mac-cert.sh writes
# to: $FLOCI_AZ_TLS_DIR, default ~/.config/choudoufu/floci-az-tls.
floci_az_tls_dir() {
  printf '%s\n' "${FLOCI_AZ_TLS_DIR:-$HOME/.config/choudoufu/floci-az-tls}"
}

# floci_az_tls_select decides which certificate the next floci-az serves.
# It sets FLOCI_AZ_TLS_CERT_FILE and FLOCI_AZ_TLS_KEY_FILE to the host paths
# of the operator's certificate when floci_az_tls_dir holds one that passes
# `floci-az-mac-cert.sh --check` (on macOS that includes the keychain
# trusting it for SSL), or to empty when there is none and floci-az's own
# per-start CA will do, which is Linux with no FLOCI_AZ_TLS_DIR set. It
# returns 1 with the reason in FLOCI_AZ_TLS_PROBLEM when the lane cannot
# verify floci-az: a certificate there that fails its check, a
# FLOCI_AZ_TLS_DIR named with no certificate in it, or macOS with none, so
# the run stops by name before any provider meets an x509 error. Not run in
# a subshell: the caller reads the variables it sets.
# shellcheck disable=SC2034 # set here, read by azure_up and gauntlet_floci_az_up
floci_az_tls_select() {
  local dir out
  dir="$(floci_az_tls_dir)"
  FLOCI_AZ_TLS_CERT_FILE="" FLOCI_AZ_TLS_KEY_FILE="" FLOCI_AZ_TLS_PROBLEM=""
  if [ -e "$dir/cert.pem" ] || [ -e "$dir/key.pem" ]; then
    if ! out="$(bash "$ROOT/scripts/floci-az-mac-cert.sh" --dir "$dir" --check 2>&1)"; then
      FLOCI_AZ_TLS_PROBLEM="$(printf '%s' "$out" | tr '\n' ' ')"
      return 1
    fi
    FLOCI_AZ_TLS_CERT_FILE="$dir/cert.pem"
    FLOCI_AZ_TLS_KEY_FILE="$dir/key.pem"
    return 0
  fi
  if [ -n "${FLOCI_AZ_TLS_DIR:-}" ]; then
    FLOCI_AZ_TLS_PROBLEM="FLOCI_AZ_TLS_DIR names $dir, which holds no cert.pem; run bash scripts/floci-az-mac-cert.sh to make one there, or unset FLOCI_AZ_TLS_DIR"
    return 1
  fi
  if [ "$(uname -s)" = "Darwin" ]; then
    FLOCI_AZ_TLS_PROBLEM="macOS: no floci-az certificate at $dir/cert.pem. Go on macOS trusts only the keychain, so floci-az's own per-start CA fails azurerm and azapi with x509; run bash scripts/floci-az-mac-cert.sh once and trust what it prints"
    return 1
  fi
  return 0
}

# floci_az_tls_env prints, one per line, the environment that makes
# floci-az serve the selected certificate from its mount: nothing when
# floci_az_tls_select chose none. live/smoke/docker-compose.yml states the
# same two keys; TestFlociAzServiceEnvMatchesCompose holds them together.
floci_az_tls_env() {
  [ -n "${FLOCI_AZ_TLS_CERT_FILE:-}" ] || return 0
  printf '%s\n' \
    "FLOCI_AZ_TLS_CERT_PATH=$FLOCI_AZ_TLS_IN_CONTAINER/cert.pem" \
    "FLOCI_AZ_TLS_KEY_PATH=$FLOCI_AZ_TLS_IN_CONTAINER/key.pem"
}
