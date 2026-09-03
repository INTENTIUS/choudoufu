"""Configuration for the terralith-migration example.

One place holds every value a run depends on, so a reader sees the whole
surface at a glance and `guard.preflight` can assert the dangerous ones
before a single call reaches AWS. Nothing here talks to the cloud; it only
describes the run.

Three values are load-bearing for safety:

* ACCOUNT_ID pins the one account this example may touch.
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

# The single AWS account this example is allowed to touch. guard.preflight
# refuses to run if the caller's credentials resolve anywhere else, so a
# mis-set AWS_PROFILE fails loud instead of mutating the wrong cloud.
ACCOUNT_ID = "354867293429"

# The choudoufu release this example is pinned to. guard.preflight asserts
# `choudoufu version` reports exactly this fork tag: a drifted binary would
# quietly change the measured numbers, which for a demo is worse than an
# error, so it is treated as one.
CHOUDOUFU_VERSION = "v0.10.0"

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
    binary = os.environ.get("CHOUDOUFU_BIN") or _find_binary()
    return Config(run_id=run_id, run_dir=run_dir, binary=binary)


def _find_binary() -> str:
    """Where to find the pinned choudoufu. CHOUDOUFU_BIN wins; otherwise prefer
    the exact release the smoke harness already cached (nothing has choudoufu on
    PATH by default), and fall back to a bare name so a PATH install still
    works. guard.preflight asserts the version regardless of which was found."""
    cached = pathlib.Path.home() / ".cache" / "choudoufu-smoke" / CHOUDOUFU_VERSION / "choudoufu"
    if cached.exists():
        return str(cached)
    return "choudoufu"
