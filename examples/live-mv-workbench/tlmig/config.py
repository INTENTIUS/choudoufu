"""Configuration for the live-mv workbench (the terralith-migration example).

One place holds every value a run depends on, so a reader sees the whole
surface at a glance and `guard.preflight` can assert the dangerous ones
before a single call reaches AWS. Nothing here talks to the cloud; it only
describes the run.

Three values are load-bearing for safety:

* ACCOUNT_ID, empty by default, optionally pins the one account this example
  may touch.
* CHOUDOUFU_VERSION pins the release the numbers were measured against.
* RESOURCE_PREFIX is stamped on every resource the run creates and is the
  fence every destructive call checks: teardown deletes only names that
  start with it, and the guard refuses to destroy anything that does not.
"""

from __future__ import annotations

import dataclasses
import os
import pathlib
import secrets

# Empty by default: no account has to be entered anywhere to run this
# example, and a run simply uses whatever account the caller's AWS
# credentials resolve to (guard.preflight reads it via `aws sts
# get-caller-identity`), exactly like the AWS CLI or plain OpenTofu would.
# Set this (or export TLMIG_ACCOUNT) to pin a specific account instead: then
# guard.preflight refuses to run if credentials resolve anywhere else, so a
# mis-set AWS_PROFILE fails loud instead of mutating the wrong cloud - a
# safety net worth having for someone re-running this often, not something a
# first run needs.
ACCOUNT_ID = ""

# The choudoufu release this example is pinned to. guard.preflight asserts
# `choudoufu version` reports exactly this fork tag: a drifted binary would
# quietly change the measured numbers, which for a demo is worse than an
# error, so it is treated as one.
CHOUDOUFU_VERSION = "v0.10.1"

REGION = "us-east-1"

# Stamped on every resource the run creates, and the fence for every
# destructive call. Kept short because AWS name limits are tight once the
# run id and a role suffix are added.
RESOURCE_PREFIX = "tlmig"

# The teams the monolith is decomposed into. The first owns the resource the
# live carve moves; the second receives it.
SOURCE_TEAM = "team-a"
DEST_TEAM = "team-b"
TEAMS = (SOURCE_TEAM, DEST_TEAM, "team-c")


@dataclasses.dataclass(frozen=True)
class Config:
    """A resolved run: its id, where its files live, and which binary and
    account it is bound to. Immutable once loaded so a beat cannot move the
    fence out from under a later one."""

    run_id: str
    run_dir: pathlib.Path
    binary: str
    account_id: str = ACCOUNT_ID
    version: str = CHOUDOUFU_VERSION
    region: str = REGION
    # The build a run measured, for the manifest and the event feed: the
    # release tag when pinned to one, else the checkout's git describe.
    build: str = CHOUDOUFU_VERSION

    @property
    def local_build(self) -> bool:
        """True when the pin is CHOUDOUFU_VERSION=local: preflight accepts
        whatever this checkout built and records its describe instead of
        asserting a release tag."""
        return self.version == "local"

    @property
    def prefix(self) -> str:
        """The name prefix every created resource carries, e.g.
        ``tlmig-9f3a1c``. This is the string the destructive fence matches
        on, so it is deliberately unique per run."""
        return f"{RESOURCE_PREFIX}-{self.run_id}"

    def estate(self, team: str) -> str:
        """The tofu-estate name for one team, e.g. ``tlmig-9f3a1c-team-a``."""
        return f"{self.prefix}-{team}"

    @property
    def monolith_estate(self) -> str:
        """The estate the whole terralith starts life under, before the
        decomposition retags its resources into per-team estates."""
        return f"{self.prefix}-monolith"

    @property
    def manifest_path(self) -> pathlib.Path:
        """The ledger of what setup created. teardown deletes by this file,
        never by a blind tag sweep, so it can only ever remove this run's
        own resources."""
        return self.run_dir / "manifest.json"

    @property
    def transcript_path(self) -> pathlib.Path:
        """Every command the run issues is appended here with a timestamp, so
        after the demo there is an exact record of what touched the cloud."""
        return self.run_dir / "transcript.log"

    @property
    def measurements_path(self) -> pathlib.Path:
        """Where the measurement harness writes the numbers it captured, in
        the same shape the docs page and the artifact read them from."""
        return self.run_dir / "measurements.json"

    def workdir(self, estate: str) -> pathlib.Path:
        """The Terraform working directory for one estate. Kept under the run
        directory so the destructive fence can require a choudoufu apply or
        destroy to run inside this run's own tree."""
        return self.run_dir / "estates" / estate


def _mint_run_id() -> str:
    """A short, collision-resistant id. Six hex chars is enough to keep two
    rehearsals on the same account from sharing resource names, and short
    enough to read aloud."""
    return secrets.token_hex(3)


def _default_run_dir(run_id: str) -> pathlib.Path:
    """Run artifacts live under the example directory in ``runs/<id>/`` (git
    ignores ``runs/``), so a checkout stays clean and a reader can find the
    manifest, transcript and captured numbers beside the code that made
    them."""
    return pathlib.Path(__file__).resolve().parent.parent / "runs" / run_id


def load(run_id: str | None = None) -> Config:
    """Resolve a run. Precedence is explicit argument, then environment
    (TLMIG_RUN_ID, TLMIG_RUN_DIR, CHOUDOUFU_BIN), then a freshly minted id
    and the default layout. Reusing a run id re-opens an existing run, which
    is how teardown and status find what setup created.

    Nothing here touches AWS or the filesystem beyond resolving paths;
    creating the run directory is setup's job, and asserting the account and
    binary is guard.preflight's.
    """
    run_id = run_id or os.environ.get("TLMIG_RUN_ID") or _mint_run_id()
    run_dir = pathlib.Path(
        os.environ.get("TLMIG_RUN_DIR") or _default_run_dir(run_id)
    ).resolve()
    # CHOUDOUFU_VERSION=local pins the example to a build of this checkout
    # while the engine moves; anything else is a release tag preflight
    # asserts. See tlmig/localbuild.py.
    version = os.environ.get("CHOUDOUFU_VERSION") or CHOUDOUFU_VERSION
    build = version
    binary = os.environ.get("CHOUDOUFU_BIN")
    if version == "local":
        from . import localbuild
        root = localbuild.repo_root()
        build = f"local {localbuild.describe(root)}" if root else "local"
        binary = binary or localbuild.ensure(root)
    binary = binary or _find_binary(version)
    # Empty by default (no fence; see ACCOUNT_ID above). The Docker demo's
    # compose file sets TLMIG_ACCOUNT=000000000000 (floci's fixed account) as
    # an explicit, harmless pin against the emulator; a real-AWS run is
    # unfenced unless this or ACCOUNT_ID is set.
    account_id = os.environ.get("TLMIG_ACCOUNT", ACCOUNT_ID)
    region = os.environ.get("AWS_REGION", REGION)
    return Config(run_id=run_id, run_dir=run_dir, binary=binary, version=version, build=build,
                  account_id=account_id, region=region)


def _find_binary(version: str = CHOUDOUFU_VERSION) -> str:
    """Where to find the pinned choudoufu. CHOUDOUFU_BIN wins; otherwise prefer
    the exact release the smoke harness already cached (nothing has choudoufu on
    PATH by default), and fall back to a bare name so a PATH install still
    works. guard.preflight asserts the version regardless of which was found."""
    cached = pathlib.Path.home() / ".cache" / "choudoufu-smoke" / version / "choudoufu"
    if cached.exists():
        return str(cached)
    # Not cached: fetch the pinned release the way the smoke harness does, so
    # a fresh machine needs no manual step. A download that fails leaves the
    # bare name for preflight to refuse with a clear message.
    try:
        from . import localbuild
        return localbuild.fetch_release(version)
    except Exception as exc:  # noqa: BLE001 - preflight reports the consequence
        print(f"  could not fetch choudoufu {version}: {exc}")
        return "choudoufu"
