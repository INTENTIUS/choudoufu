#!/usr/bin/env bash
# Copyright (c) The OpenTofu Authors
# SPDX-License-Identifier: MPL-2.0
#
# The maintainer's estate check for `choudoufu live-plan -resource`
# (GitHub issue #2245). Run it by hand from a real estate's root, the
# directory you would run `choudoufu plan` in. It reads the live estate
# twice and changes nothing: one -resource drift read of ADDR, then one
# full live-plan saved to a plan file, so the two answers for ADDR can be
# compared. Agents never run it (CLAUDE.md, "Only the maintainer runs the
# gauntlet").
#
#   CHOUDOUFU=/path/to/choudoufu live/plan-resource-check.sh ADDR [live-plan flags...]
#
#   ADDR              a managed resource or instance, e.g. aws_vpc.main or
#                     'module.net.aws_subnet.private[0]'
#   live-plan flags   passed to both runs, e.g. -estate=prod
#
# It prints three verdict lines and exits non-zero if any of them is FAIL:
#
#   reads:  PASS when the drift-settle line's reads say recordListCalls 0
#           and sweepCalls 0
#   status: PASS when ADDR's last drift-record status is what the full
#           plan's action for ADDR maps to (no-op in-sync, create pending,
#           update/replace/delete drifted); when the full plan holds no
#           change for ADDR it prints SEE and both answers, since an
#           omission is reported in live-plan's own output, not the plan
#   settle: the drift-settle line itself, to paste into issue #2245
#
# The NDJSON, the full plan and both stderr logs are kept in the directory
# it names, for the PLAN-RESOURCE.md update the issue asks for afterwards.
set -euo pipefail

if [ $# -lt 1 ]; then
  sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi
addr=$1
shift
choudoufu=${CHOUDOUFU:-choudoufu}
command -v jq >/dev/null || { echo "plan-resource-check: jq is required" >&2; exit 2; }

# A real estate is read, so a person starts this at a terminal and types
# "run", as every estate script asks (live/e2e/lib/gauntlet.sh).
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=live/e2e/lib/gauntlet.sh
. "$here/e2e/lib/gauntlet.sh"
gauntlet_ask_person || exit 1

out=$(mktemp -d "${TMPDIR:-/tmp}/plan-resource-check.XXXXXX")
echo "plan-resource-check: writing to $out"

if ! "$choudoufu" live-plan "$@" -resource="$addr" >"$out/drift.ndjson" 2>"$out/drift.stderr"; then
  echo "plan-resource-check: live-plan -resource=$addr failed; see $out/drift.stderr" >&2
  tail -20 "$out/drift.stderr" >&2
  exit 1
fi

settle=$(jq -c 'select(.type == "drift-settle")' "$out/drift.ndjson")
record=$(jq -c --arg a "$addr" 'select(.type == "drift-record" and .address == $a)' "$out/drift.ndjson" | tail -1)
if [ -z "$settle" ] || [ -z "$record" ]; then
  echo "plan-resource-check: the stream has no drift-settle line or no record for $addr; see $out/drift.ndjson" >&2
  exit 1
fi
status=$(jq -r '.status' <<<"$record")

fail=0
lists=$(jq -r '.reads.recordListCalls' <<<"$settle")
sweeps=$(jq -r '.reads.sweepCalls' <<<"$settle")
if [ "$lists" = "0" ] && [ "$sweeps" = "0" ]; then
  echo "reads:  PASS recordListCalls=$lists sweepCalls=$sweeps"
else
  echo "reads:  FAIL recordListCalls=$lists sweepCalls=$sweeps"
  fail=1
fi

if ! "$choudoufu" live-plan "$@" -out="$out/full.tfplan" >"$out/full.stdout" 2>"$out/full.stderr"; then
  # live-plan exits 2 under -detailed-exitcode only; any failure here is a
  # failed full plan.
  echo "plan-resource-check: the full live-plan failed; see $out/full.stderr" >&2
  tail -20 "$out/full.stderr" >&2
  exit 1
fi
"$choudoufu" show -json "$out/full.tfplan" >"$out/full.json"
actions=$(jq -r --arg a "$addr" '[.resource_changes[]? | select(.address == $a) | .change.actions | join(",")] | first // ""' "$out/full.json")

case "$actions" in
  no-op) want=in-sync ;;
  create) want=pending ;;
  update | delete | delete,create | create,delete) want=drifted ;;
  "") want="" ;;
  *) want="unknown:$actions" ;;
esac

if [ -z "$want" ]; then
  echo "status: SEE -resource says $status; the full plan holds no change for $addr (an omission: read the full run's own output in $out/full.stdout)"
elif [ "$status" = "$want" ]; then
  echo "status: PASS $status (full plan: $actions)"
else
  echo "status: FAIL -resource says $status, the full plan's $actions maps to $want"
  fail=1
fi

echo "settle: $settle"
exit $fail
