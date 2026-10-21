#!/usr/bin/env bash
# scripts/floci-az-mac-cert.sh: one TLS certificate for every floci-az the
# Azure lane starts, so the lane runs on a Mac.
#
# floci-az with TLS on generates a fresh CA each time a container starts
# (lex00/floci-az 30000b0, TlsConfigSource). On Linux the harness adds that
# CA to SSL_CERT_FILE per run. Go on macOS ignores SSL_CERT_FILE and asks the
# keychain, so azurerm and azapi there fail cold_deploy with an x509 error
# against a CA nobody could have trusted in advance. floci-az takes a
# certificate of the operator's own instead (FLOCI_AZ_TLS_CERT_PATH plus
# FLOCI_AZ_TLS_KEY_PATH, both PEM, used as given: floci-az adds no SAN and
# serves the file itself at /_floci/tls-cert). This script makes that
# certificate once; the maintainer trusts it once; live/e2e/lib/floci-az.sh
# mounts it into every floci-az the smoke harness and the gauntlet start.
#
#   bash scripts/floci-az-mac-cert.sh                 make it, or reuse it
#   bash scripts/floci-az-mac-cert.sh --check         verify only, write nothing
#   bash scripts/floci-az-mac-cert.sh --regenerate    replace it in place
#   bash scripts/floci-az-mac-cert.sh --dir <dir>     somewhere else
#
# The directory is $FLOCI_AZ_TLS_DIR, default ~/.config/choudoufu/floci-az-tls,
# holding cert.pem, key.pem and the openssl.cnf they were made from. It is
# mode 0700 and key.pem is 0644: the container runs as uid 1001, which reads
# the key through a single-file read-only bind mount, so the file has to be
# world-readable and the directory is what keeps other host users out.
#
# The certificate is self-signed and is its own trust anchor: CA:true,
# keyCertSign, serverAuth, 825 days (Apple's ceiling for a TLS server
# certificate), RSA 2048, SHA-256. A critical name constraint limits it to
# localhost, its subdomains and the loopback addresses, so a trusted anchor
# whose key leaked still could not vouch for any other name. The trust
# command is printed, never run: it changes the maintainer's keychain.
#
# Exit 1 when the certificate cannot be made or reused, said by name. With
# --check, also exit 1 on macOS when the keychain does not trust it yet;
# without, an untrusted certificate is the expected state after the first
# run, and the trust command is printed.
set -euo pipefail

# SANS is every host a client of floci-az dials, and nothing else. Each
# entry, and why it is here (live/FLOCI.md, Azure, carries the same list):
#
#   localhost           AZURE_HOST / FLOCI_AZ_HOST: ARM, /metadata/endpoints
#                       (ARM_METADATA_HOSTNAME, ARM_METADATA_HOST), the token
#                       endpoint (ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST) and
#                       ARM_RESOURCE_MANAGER_ENDPOINT are all
#                       https://localhost:<port>.
#   *.localhost         floci-az's own generated certificate names it, and
#                       Go's verifier honours it on Linux. macOS's SSL policy
#                       refuses a wildcard directly under a single label, so
#                       on a Mac it covers nothing; no harness host needs it.
#   *.blob.localhost    FLOCI_AZ_STORAGE_SUFFIX=localhost:<port>: the stock
#                       azurerm backend and record_store "azurerm" dial
#                       https://<account>.blob.localhost:<port>
#                       (internal/backend/remote-state/azure/auth/clients.go).
#   *.dfs/.file/.queue/.table.localhost
#                       azurerm 5.10.0 builds a storage account's data-plane
#                       URL as https://<account>.<type>.<suffix> for every
#                       EndpointType it reads (internal/services/storage/
#                       client/helpers.go); floci-az's primaryEndpoints omit
#                       file, so that one is always built from the suffix.
#   127.0.0.1, ::1      what localhost resolves to; a client that dials the
#                       address rather than the name still verifies.
#
# A wildcard covers one label, so each storage service needs its own entry.
# Ports are not part of a SAN. live/floci_az_tls_test.go holds this list to
# the hosts the harness exports: a new host there fails the test until it
# is added here.
SANS=(
  DNS:localhost
  DNS:*.localhost
  DNS:*.blob.localhost
  DNS:*.dfs.localhost
  DNS:*.file.localhost
  DNS:*.queue.localhost
  DNS:*.table.localhost
  IP:127.0.0.1
  IP:::1
)
DAYS=825
# A certificate this close to expiry is reported as needing --regenerate,
# so it never runs out half way through a lane run.
RENEW_SECS=$(( 14 * 86400 ))

DIR="${FLOCI_AZ_TLS_DIR:-$HOME/.config/choudoufu/floci-az-tls}"
MODE="make"
while [ $# -gt 0 ]; do
  case "$1" in
    --dir) DIR="${2:?--dir needs a directory}"; shift 2 ;;
    --dir=*) DIR="${1#--dir=}"; shift ;;
    --regenerate) MODE=regenerate; shift ;;
    --check) MODE=check; shift ;;
    -h|--help) sed -n '2,38p' "$0"; exit 0 ;;
    *) printf 'floci-az-mac-cert: unknown argument %s (see --help)\n' "$1" >&2; exit 1 ;;
  esac
done
CERT="$DIR/cert.pem"
KEY="$DIR/key.pem"
CNF="$DIR/openssl.cnf"
OPENSSL="${OPENSSL:-openssl}"
command -v "$OPENSSL" >/dev/null 2>&1 || { printf 'floci-az-mac-cert: %s is not on PATH\n' "$OPENSSL" >&2; exit 1; }

say() { printf 'floci-az-mac-cert: %s\n' "$*"; }

# canonical_sans reads `openssl x509 -text` on stdin and prints the SAN
# entries one per line in SANS's spelling. OpenSSL and LibreSSL both print
# "IP Address:" and spell ::1 out as 0:0:0:0:0:0:0:1.
canonical_sans() {
  awk '/X509v3 Subject Alternative Name/ { getline; print; exit }' \
    | tr ',' '\n' | sed -e 's/^ *//' -e 's/ *$//' -e 's/^IP Address:/IP:/' \
    | sed -e 's/^IP:0:0:0:0:0:0:0:1$/IP:::1/' -e 's/^IP:0000:0000:0000:0000:0000:0000:0000:0001$/IP:::1/' \
    | sort
}

# cert_problems prints one line per thing wrong with $CERT and $KEY, and
# nothing when they are usable.
cert_problems() {
  if [ ! -r "$CERT" ]; then echo "no readable certificate at $CERT"; return; fi
  if [ ! -r "$KEY" ]; then echo "no readable key at $KEY"; return; fi
  local text want got
  if ! text="$("$OPENSSL" x509 -in "$CERT" -noout -text 2>&1)"; then
    echo "$CERT does not parse as a PEM certificate: $text"; return
  fi
  want="$(printf '%s\n' "${SANS[@]}" | sort)"
  got="$(printf '%s\n' "$text" | canonical_sans)"
  [ "$want" = "$got" ] || echo "its SANs are [$(echo "$got" | paste -sd' ' -)], not the [$(echo "$want" | paste -sd' ' -)] this script makes now"
  printf '%s\n' "$text" | grep -q 'CA:TRUE' || echo "it is not CA:true"
  printf '%s\n' "$text" | grep -q 'TLS Web Server Authentication' || echo "its extended key usage lacks serverAuth"
  "$OPENSSL" x509 -in "$CERT" -noout -checkend "$RENEW_SECS" >/dev/null 2>&1 \
    || echo "it expires within $(( RENEW_SECS / 86400 )) days ($("$OPENSSL" x509 -in "$CERT" -noout -enddate))"
  local cpub kpub
  cpub="$("$OPENSSL" x509 -in "$CERT" -noout -pubkey 2>/dev/null)"
  kpub="$("$OPENSSL" pkey -in "$KEY" -pubout 2>/dev/null)" || true
  [ -n "$kpub" ] && [ "$cpub" = "$kpub" ] || echo "$KEY is not the key of $CERT"
  [ "$(head -1 "$KEY")" = "-----BEGIN PRIVATE KEY-----" ] || echo "$KEY is not an unencrypted PKCS#8 PEM key"
}

# trust_problem prints why macOS would not trust $CERT for a TLS server
# named localhost or <account>.blob.localhost, and nothing when it would.
# verify-cert evaluates the SSL policy against the user's and the admin
# trust settings, the evaluation Go's crypto/x509 asks the platform for on
# macOS. -l because the certificate is its own anchor, so the leaf is a CA;
# -L so the check never fetches anything.
# The same evaluation with the certificate named as its own root (-r)
# answers whether macOS would accept it at all once trusted, without
# touching the keychain, so a certificate the policy refuses outright is
# told apart from one nobody has trusted yet.
trust_problem() {
  [ "$(uname -s)" = "Darwin" ] || return 0
  local name out
  for name in localhost probe.blob.localhost; do
    if ! out="$(security verify-cert -c "$CERT" -r "$CERT" -p ssl -n "$name" -l -L 2>&1)"; then
      echo "macOS's SSL policy refuses $CERT for $name even as its own root ($(echo "$out" | grep -m1 'Cert Verify Result')); --regenerate it"
      return
    fi
    if ! out="$(security verify-cert -c "$CERT" -p ssl -n "$name" -l -L 2>&1)"; then
      echo "the keychain does not trust $CERT for $name ($(echo "$out" | grep -m1 'Cert Verify Result')); trust it with: security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db $CERT"
      return
    fi
  done
}

trust_commands() {
  cat <<EOF
Trust it once (it changes your keychain, so this script never runs it):

  security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db $CERT

or for every user on this Mac:

  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain $CERT

Then \`bash scripts/floci-az-mac-cert.sh --check\` says whether it took.
EOF
}

generate() {
  mkdir -p "$DIR"
  chmod 0700 "$DIR"
  local i alt=""
  for i in "${!SANS[@]}"; do
    alt+="${SANS[$i]%%:*}.$((i + 1)) = ${SANS[$i]#*:}"$'\n'
  done
  cat > "$CNF" <<EOF
# Written by scripts/floci-az-mac-cert.sh. The certificate next to this file
# was made from it; regenerate with --regenerate rather than by hand.
[req]
distinguished_name = dn
prompt = no
x509_extensions = ext
[dn]
O = choudoufu
CN = choudoufu floci-az local TLS ($(hostname -s 2>/dev/null || echo host), $(date -u +%Y-%m-%d))
[ext]
basicConstraints = critical, CA:true
keyUsage = critical, digitalSignature, keyEncipherment, keyCertSign
extendedKeyUsage = serverAuth
subjectKeyIdentifier = hash
nameConstraints = critical, permitted;DNS:localhost, permitted;IP:127.0.0.1/255.255.255.255, permitted;IP:0:0:0:0:0:0:0:1/FFFF:FFFF:FFFF:FFFF:FFFF:FFFF:FFFF:FFFF
subjectAltName = @alt
[alt]
${alt}
EOF
  # Written in place over any earlier pair: nothing is deleted.
  local err
  err="$(umask 022
    "$OPENSSL" req -x509 -config "$CNF" -newkey rsa:2048 -nodes -sha256 -days "$DAYS" \
      -set_serial "0x$("$OPENSSL" rand -hex 16)" -keyout "$KEY" -out "$CERT" 2>&1)" \
    || { say "openssl could not make the certificate: $err" >&2; exit 1; }
  chmod 0644 "$KEY" "$CERT"
}

case "$MODE" in
  check)
    problems="$(cert_problems)"
    [ -n "$problems" ] || problems="$(trust_problem)"
    if [ -n "$problems" ]; then
      say "$CERT is not ready:" >&2
      printf '  %s\n' "$problems" >&2
      exit 1
    fi
    say "$CERT is ready"
    exit 0
    ;;
  make)
    if [ -e "$CERT" ] || [ -e "$KEY" ]; then
      problems="$(cert_problems)"
      if [ -n "$problems" ]; then
        say "$CERT exists and cannot be reused:" >&2
        printf '  %s\n' "$problems" >&2
        say "run it again with --regenerate to replace it in place (and trust the new one)" >&2
        exit 1
      fi
      say "reusing $CERT"
    else
      generate
      say "made $CERT and $KEY"
    fi
    ;;
  regenerate)
    generate
    say "made $CERT and $KEY, over whatever was there"
    ;;
esac

problems="$(cert_problems)"
[ -z "$problems" ] || { say "the certificate just made fails its own checks:" >&2; printf '  %s\n' "$problems" >&2; exit 1; }
say "SANs: ${SANS[*]}"
"$OPENSSL" x509 -in "$CERT" -noout -enddate -fingerprint -sha256
if [ "$(uname -s)" = "Darwin" ]; then
  trust="$(trust_problem)"
  if [ -n "$trust" ]; then
    say "$trust"
    trust_commands
  else
    say "the keychain already trusts it; nothing more to do"
  fi
else
  say "not macOS: the harness adds it to SSL_CERT_FILE per run, no keychain step"
fi
