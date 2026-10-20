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
