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

# ── the endpoint lock (#2133): nothing an Azure-lane run starts leaves floci-az ──
#
# A gcp-lane run sent traffic to real Google because two endpoint arguments
# were unset. The Azure equivalent on a Mac is worse: azurerm 5.10.0 turns
# use_cli on by default (internal/provider/provider.go:268-274), so a run
# whose endpoints fell back to the public cloud would sign in with the
# maintainer's own `az login` and create resources in a real subscription.
# Everything below is stated twice, in the provider blocks (an explicit
# argument beats the environment in both providers: azurerm's schema
# EnvDefaultFunc applies only to an unset argument, azapi 2.13.0's Configure
# reads os.Getenv only under model.X.IsNull(), provider.go:363-620) and in
# the exported environment, which is all the azapi endpoints, the stock
# azurerm backend and record_store "azurerm" have where a block says
# nothing. live/azure_endpoints_test.go holds both to the emulator.

# FLOCI_AZ_CLIENT_SECRET is the client secret every block and process
# signs in with. floci-az issues a token for any client id and secret
# unless validate-client-credentials is set; a real secret is never needed.
FLOCI_AZ_CLIENT_SECRET="floci-az-does-not-check-this"

# FLOCI_AZ_SCRUBBED are the credential and endpoint variables the lane's
# clients would read from the operator's shell and that the lane never sets
# to a value of its own: azurerm 5.10.0 (provider.go's EnvDefaultFunc
# entries, helpers.go:60-133), azapi 2.13.0 (provider.go:104-620, and the
# azidentity v1 variables its credential chain reads: environment_credential,
# workload_identity, confidential_client's AZURE_REGIONAL_AUTHORITY_NAME,
# azidentity.go's AZURE_AUTHORITY_HOST), the stock azurerm backend and
# record_store "azurerm" (internal/live/projection/azurerm_store.go's
# azureRMEnv), and managed identity's own endpoints. Any of them left in
# place could add a credential type ahead of the client secret (azurerm
# tries a client certificate first, go-azure-sdk sdk/auth/auth.go:39), or
# move the authority. floci_az_provider_env takes them out of every child's
# environment with `export -n`, which keeps a shell variable of the same
# name (smoke's AZURE_CLIENT_ID and AZURE_TENANT_ID are its own ids, never
# exported for that reason).
FLOCI_AZ_SCRUBBED="
ARM_CLIENT_CERTIFICATE ARM_CLIENT_CERTIFICATE_PATH ARM_CLIENT_CERTIFICATE_PASSWORD
ARM_CLIENT_ID_FILE_PATH ARM_CLIENT_SECRET_FILE_PATH
ARM_OIDC_TOKEN ARM_OIDC_TOKEN_FILE_PATH ARM_OIDC_REQUEST_TOKEN ARM_OIDC_REQUEST_URL
ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID ARM_OIDC_AZURE_SERVICE_CONNECTION_ID AZURESUBSCRIPTION_SERVICE_CONNECTION_ID
ACTIONS_ID_TOKEN_REQUEST_TOKEN ACTIONS_ID_TOKEN_REQUEST_URL SYSTEM_ACCESSTOKEN SYSTEM_OIDCREQUESTURI
ARM_MSI_ENDPOINT ARM_MSI_API_VERSION ARM_AUXILIARY_TENANT_IDS ARM_PARTNER_ID
ARM_ACCESS_KEY ARM_SAS_TOKEN ARM_USE_AZUREAD ARM_STORAGE_USE_AZUREAD
AZURE_CLIENT_ID AZURE_TENANT_ID AZURE_CLIENT_SECRET AZURE_CLIENT_CERTIFICATE_PATH
AZURE_CLIENT_CERTIFICATE_PASSWORD AZURE_CLIENT_SEND_CERTIFICATE_CHAIN AZURE_USERNAME AZURE_PASSWORD
AZURE_FEDERATED_TOKEN_FILE AZURE_AUTHORITY_HOST AZURE_REGIONAL_AUTHORITY_NAME
AZURE_ADDITIONALLY_ALLOWED_TENANTS AZURE_TOKEN_CREDENTIALS AZURE_SUBSCRIPTION_ID
AZURE_POD_IDENTITY_AUTHORITY_HOST IDENTITY_ENDPOINT IDENTITY_HEADER IDENTITY_SERVER_THUMBPRINT
IMDS_ENDPOINT MSI_ENDPOINT MSI_SECRET
"

# floci_az_check_host <host> succeeds when <host> is a loopback host:port,
# the only thing a floci-az start ever publishes (127.0.0.1:<port>, named
# as localhost for the certificate), and says why not otherwise.
floci_az_check_host() {
  case "$1" in
    localhost:[0-9]*|127.0.0.1:[0-9]*) ;;
    *) printf 'floci-az endpoint lock: %s is not a loopback host:port, so it is not floci-az\n' "${1:-(empty)}" >&2; return 1 ;;
  esac
  case "${1#*:}" in
    *[!0-9]*) printf 'floci-az endpoint lock: %s has a port that is not a number\n' "$1" >&2; return 1 ;;
  esac
}

# floci_az_hcl_host <host|var.floci_az_host> prints the HCL expression for
# azurerm's metadata_host: the gauntlet's variable reference as is (its
# value is validated by floci_az_host_variable's rule), a literal host
# checked and quoted.
floci_az_hcl_host() {
  if [ "$1" = "var.floci_az_host" ]; then
    printf '%s\n' "$1"
    return 0
  fi
  floci_az_check_host "$1" || return 1
  printf '"%s"\n' "$1"
}

# floci_az_azurerm_args <host|var.floci_az_host> <subscription> <tenant>
# <client> prints the arguments every Azure-lane azurerm provider block
# carries, two-space indented, without the features block. metadata_host
# alone decides azurerm's cloud: with it set, every endpoint (ARM, the Entra
# login endpoint, Microsoft Graph's, the storage suffix) is read from
# https://<host>/metadata/endpoints and `environment` is never consulted
# (internal/provider/provider.go:405-419; go-azure-sdk
# environments/from_endpoint.go and endpoint_refresh.go). Graph is only
# called when the token carries no oid claim (internal/clients/auth.go:56-75),
# and floci-az's tokens carry one. The four switches off and an explicit
# client secret leave go-azure-sdk's chain (sdk/auth/auth.go) a client
# secret to use and nothing else from the shell.
floci_az_azurerm_args() {
  local host
  host="$(floci_az_hcl_host "$1")" || return 1
  cat <<HCL
  subscription_id                 = "$2"
  tenant_id                       = "$3"
  client_id                       = "$4"
  client_secret                   = "$FLOCI_AZ_CLIENT_SECRET"
  metadata_host                   = $host
  use_cli                         = false
  use_msi                         = false
  use_oidc                        = false
  use_aks_workload_identity       = false
  resource_provider_registrations = "none"
  disable_terraform_partner_id    = true
HCL
}

# floci_az_azapi_args <subscription> <tenant> <client> prints the arguments
# every Azure-lane azapi provider block carries, two-space indented.
#
# environment = "custom" is stated here and its three endpoints are not:
# azapi 2.13.0 reads ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST,
# ARM_RESOURCE_MANAGER_ENDPOINT and ARM_RESOURCE_MANAGER_AUDIENCE into the
# values its "custom" case checks only when the block has no endpoint block
# (provider.go:404-424), so a block that states both fails "Missing value
# for active_directory_authority_host" (provider.go:635-646). The other
# explicit form, environment = "public" with an endpoint block overriding
# the three, leaves every data-plane service at the public cloud's hosts
# (environments.go). "custom" with the variables floci_az_provider_env
# exports fails closed instead: an unset variable is a configure error, and
# the cloud it builds holds Resource Manager alone.
#
# disable_instance_discovery is what keeps the token request home: with
# discovery on, MSAL asks https://login.microsoftonline.com/common/discovery/instance
# about any authority host it does not already trust (microsoft-authentication-
# library-for-go apps/internal/oauth/ops/authority/authority.go:698-704),
# and loopback is not one it trusts.
floci_az_azapi_args() {
  cat <<HCL
  subscription_id              = "$1"
  tenant_id                    = "$2"
  client_id                    = "$3"
  client_secret                = "$FLOCI_AZ_CLIENT_SECRET"
  environment                  = "custom"
  use_cli                      = false
  use_msi                      = false
  use_oidc                     = false
  use_aks_workload_identity    = false
  disable_instance_discovery   = true
  disable_terraform_partner_id = true
HCL
}

# floci_az_provider_blocks <host|var.floci_az_host> <subscription> <tenant>
# <client> prints the lane's two provider blocks: azurerm with its features
# block, azapi with provider registration off (floci-az answers the
# providers listing with an empty list).
floci_az_provider_blocks() {
  local azurerm azapi
  azurerm="$(floci_az_azurerm_args "$@")" || return 1
  azapi="$(floci_az_azapi_args "$2" "$3" "$4")" || return 1
  cat <<HCL
provider "azurerm" {
  features {}
$azurerm
}

provider "azapi" {
$azapi
  skip_provider_registration   = true
}
HCL
}

# floci_az_host_variable prints the variable a gauntlet configuration's
# azurerm metadata_host reads. The gauntlet runs two containers on two
# ports against one written configuration, so the host comes from
# TF_VAR_floci_az_host (floci_az_provider_env exports it, and each
# container's saved environment carries its own). It has no default, so a
# run without one fails at plan, and its validation refuses anything but a
# loopback host:port.
floci_az_host_variable() {
  cat <<'HCL'
variable "floci_az_host" {
  type        = string
  description = "The floci-az container's host:port. Set by the harness through TF_VAR_floci_az_host; never a real Azure host."
  validation {
    condition     = can(regex("^(localhost|127\\.0\\.0\\.1):[0-9]+$", var.floci_az_host))
    error_message = "floci_az_host must be a loopback host:port: the azure lane talks only to floci-az."
  }
}
HCL
}

# floci_az_record_store_args <host> <subscription> <tenant> <client> prints
# a record_store "azurerm" block's connection arguments, six-space indented
# for a block inside a live block: the stock azurerm backend's own names
# (internal/backend/remote-state/azure), so the store reaches floci-az by
# its metadata host and signs in with the client secret whatever the shell
# holds.
floci_az_record_store_args() {
  floci_az_check_host "$1" || return 1
  cat <<HCL
      metadata_host             = "$1"
      subscription_id           = "$2"
      tenant_id                 = "$3"
      client_id                 = "$4"
      client_secret             = "$FLOCI_AZ_CLIENT_SECRET"
      use_cli                   = false
      use_msi                   = false
      use_oidc                  = false
      use_aks_workload_identity = false
HCL
}

# floci_az_provider_env <host> <subscription> <tenant> <client>
# <no-login-dir> aims every process the caller starts afterwards at the
# floci-az on <host>, and takes the operator's own Azure credentials out of
# reach. It runs in the caller's shell, never a subshell, since what it
# exports is the point.
#
#   1. every name in FLOCI_AZ_SCRUBBED stops being exported;
#   2. AZURE_CONFIG_DIR names <no-login-dir>, created empty, so an Azure CLI
#      started by anything finds no login (the CLI credential runs `az`,
#      which reads its token cache from there);
#   3. the ARM_* set below is exported, overriding whatever the shell had:
#      azurerm's ARM_METADATA_HOSTNAME, the stock backend's ARM_METADATA_HOST
#      (a different variable, and a metadata host wins over ARM_ENVIRONMENT
#      there too), azapi's ARM_ENVIRONMENT=custom and its three endpoints,
#      instance discovery off, the client secret, every switch off;
#   4. TF_VAR_floci_az_host, for floci_az_host_variable.
floci_az_provider_env() {
  local host="$1" sub="$2" tenant="$3" client="$4" nologin="$5" v
  floci_az_check_host "$host" || return 1
  [ -n "$sub" ] && [ -n "$tenant" ] && [ -n "$client" ] && [ -n "$nologin" ] \
    || { printf 'floci_az_provider_env: needs <host> <subscription> <tenant> <client> <no-login-dir>\n' >&2; return 1; }
  for v in $FLOCI_AZ_SCRUBBED; do
    export -n "$v" 2>/dev/null || true
  done
  mkdir -p "$nologin" || return 1
  export AZURE_CONFIG_DIR="$nologin"
  export ARM_SUBSCRIPTION_ID="$sub"
  export ARM_TENANT_ID="$tenant"
  export ARM_CLIENT_ID="$client"
  export ARM_CLIENT_SECRET="$FLOCI_AZ_CLIENT_SECRET"
  export ARM_USE_CLI=false ARM_USE_MSI=false ARM_USE_OIDC=false ARM_USE_AKS_WORKLOAD_IDENTITY=false
  export ARM_METADATA_HOSTNAME="$host" ARM_METADATA_HOST="$host"
  export ARM_ENVIRONMENT=custom
  export ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST="https://$host/"
  export ARM_RESOURCE_MANAGER_ENDPOINT="https://$host"
  export ARM_RESOURCE_MANAGER_AUDIENCE="https://$host/"
  export ARM_DISABLE_INSTANCE_DISCOVERY=true
  export ARM_RESOURCE_PROVIDER_REGISTRATIONS=none ARM_SKIP_PROVIDER_REGISTRATION=true
  export ARM_DISABLE_TERRAFORM_PARTNER_ID=true
  export TF_VAR_floci_az_host="$host"
}
