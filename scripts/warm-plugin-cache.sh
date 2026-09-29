#!/usr/bin/env bash
# scripts/warm-plugin-cache.sh: put the PINNED hashicorp/aws provider into the
# shared plugin cache for both registries, so a crossing script that consumes
# that directory as a -plugin-dir filesystem mirror finds what it asserts.
#
# Why this exists (#1550's second act). corpus-simpleinfra-dns does not use
# the shared cache as a cache: it passes it to every init as -plugin-dir, a
# read-only filesystem MIRROR, because its two halves resolve hashicorp/aws
# from two different registries and neither half's lock file can satisfy the
# other (see that script's own comment). A mirror is only ever read, so
# something else has to fill it. In the old serial gauntlet run that was an
# accident of ordering - an earlier estate's ordinary init had already
# populated the cache by the time this one ran. One estate per job (#1550)
# removed the accident: run 35896010698's corpus-simpleinfra-dns shard started
# on an empty runner and failed at its stage-0 assertion with
#
#   FAIL: /home/runner/.terraform.d/plugin-cache/registry.terraform.io/hashicorp/aws/6.63.0 is missing
#
# which is the script telling the truth about its own precondition. This is
# that precondition, made explicit and runnable, rather than a dependency on
# what some other job happened to do first.
#
# The version is read from live/oracle-versions.json through the same
# gauntlet_aws_pin_version the crossing scripts read it with, and the
# directory from the same gauntlet_plugin_cache_dir, so a pin bump needs no
# edit here and the path has one spelling (#1300). A literal version in this
# file would be a second place to bump and a silent way to warm the wrong one.
#
# Both binaries are required and neither substitutes for the other: real
# terraform resolves hashicorp/aws from registry.terraform.io and tofu from
# registry.opentofu.org, and the mirror needs a copy under both hostnames.
#
# Safe to run repeatedly: a registry whose directory is already populated is
# skipped, so a warm cache (a CI cache hit, or a laptop that has run the
# gauntlet before) costs a stat rather than a gigabyte.
#
#   bash scripts/warm-plugin-cache.sh
#
# TF_PLUGIN_CACHE_DIR overrides the directory, the same way it does for every
# crossing script (gauntlet_plugin_cache_dir), which is how this is tested
# against an empty cache without touching the real one.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export ROOT

# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"

# REGISTRIES is the list corpus-simpleinfra-dns asserts, in the same order,
# paired with the binary that resolves hashicorp/aws from each.
# live/plugin_mirror_test.go holds the two lists to each other, so a script
# that starts consuming the mirror with a third registry or a second provider
# fails there rather than in CI thirty minutes into a shard.
warm_one() {
  local registry="$1" bin="$2" version="$3" dir="$4" work

  if [ -d "$dir/$registry/hashicorp/aws/$version" ] &&
    [ -n "$(ls -A "$dir/$registry/hashicorp/aws/$version" 2>/dev/null)" ]; then
    printf '  %s: aws %s already present\n' "$registry" "$version"
    return 0
  fi

  command -v "$bin" >/dev/null 2>&1 || {
    printf '%s is not on PATH, and it is the only binary that resolves hashicorp/aws from %s\n' "$bin" "$registry" >&2
    return 1
  }

  work="$(mktemp -d)"
  cat > "$work/main.tf" <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= $version"
    }
  }
}
EOF
  printf '  %s: installing aws %s with %s\n' "$registry" "$version" "$bin"
  # -backend=false: there is no state here and nothing to initialise but the
  # provider. The terraform arm goes through gauntlet_locked_init for the
  # reason the library documents - real terraform unpacks straight into the
  # final cache path with no lock of its own, so a concurrent reader can see
  # a half-written binary. tofu takes its own per-version flock and needs no
  # help.
  if [ "$bin" = "terraform" ]; then
    ( cd "$work" && gauntlet_locked_init "$bin" init -input=false -no-color -backend=false >/dev/null ) || {
      rm -rf "$work"
      printf '%s init failed for %s\n' "$bin" "$registry" >&2
      return 1
    }
  else
    ( cd "$work" && "$bin" init -input=false -no-color -backend=false >/dev/null ) || {
      rm -rf "$work"
      printf '%s init failed for %s\n' "$bin" "$registry" >&2
      return 1
    }
  fi
  rm -rf "$work"
}

main() {
  local version dir rc=0
  version="$(gauntlet_aws_pin_version)"
  [ -n "$version" ] || {
    printf 'could not read aws_provider_version from %s/live/oracle-versions.json\n' "$ROOT" >&2
    exit 1
  }
  # Exports TF_PLUGIN_CACHE_DIR and TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE
  # and creates the directory: the inits below are ordinary cache-populating
  # inits, not mirror reads.
  gauntlet_plugin_cache
  dir="$(gauntlet_plugin_cache_dir)"
  printf 'warming %s with hashicorp/aws %s\n' "$dir" "$version"

  warm_one registry.terraform.io terraform "$version" "$dir" || rc=1
  warm_one registry.opentofu.org tofu "$version" "$dir" || rc=1
  [ "$rc" -eq 0 ] || exit 1

  # The same assertion corpus-simpleinfra-dns makes at its stage 0, made here
  # where it is cheap to fix, so a cache that did not take fails this step
  # instead of that estate.
  for registry in registry.terraform.io registry.opentofu.org; do
    [ -d "$dir/$registry/hashicorp/aws/$version" ] || {
      printf '%s/%s/hashicorp/aws/%s is still missing after the install\n' "$dir" "$registry" "$version" >&2
      exit 1
    }
  done
  printf 'mirror ready: %s has aws %s for both registries\n' "$dir" "$version"
}

main "$@"
