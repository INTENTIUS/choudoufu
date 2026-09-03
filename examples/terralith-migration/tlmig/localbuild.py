"""Pinning the example to a local build instead of a release.

The pin exists so the numbers a run measures are the numbers a reader can
reproduce. While the engine is moving, the release a reader can download is
not the build a developer needs to demo against, so ``CHOUDOUFU_VERSION=local``
pins the example to this checkout instead: the binary is built from the
repository the example lives in, cached by ``git describe``, and every run
records that label as the build it measured. Nothing about the fences
changes; only which binary preflight accepts.

    CHOUDOUFU_VERSION=local uv run tlmig preflight     # builds once, then runs
    CHOUDOUFU_BIN=/path/to/choudoufu CHOUDOUFU_VERSION=local uv run tlmig ...

Stdlib only; ``go`` and ``git`` are the tools it shells out to.
"""
from __future__ import annotations

import os
import pathlib
import subprocess
import sys

LOCAL = "local"


def repo_root(start: pathlib.Path | None = None) -> pathlib.Path | None:
    """The choudoufu checkout this example lives in: the nearest ancestor
    holding cmd/choudoufu/main.go. None when the example was copied out."""
    here = (start or pathlib.Path(__file__)).resolve()
    for d in [here, *here.parents]:
        if (d / "cmd" / "choudoufu" / "main.go").exists():
            return d
    return None


def describe(root: pathlib.Path) -> str:
    """``git describe --tags --always --dirty`` of the checkout, e.g.
    ``v0.10.0-12-gabc1234-dirty``; ``unknown`` when git cannot answer."""
    try:
        out = subprocess.run(["git", "-C", str(root), "describe", "--tags", "--always", "--dirty"],
                             capture_output=True, text=True, check=False)
    except OSError:
        return "unknown"
    label = out.stdout.strip()
    return label or "unknown"


def cache_dir(root: pathlib.Path) -> pathlib.Path:
    """Builds live beside the example, under .local (gitignored)."""
    return pathlib.Path(__file__).resolve().parent.parent / ".local" / "build"


def cached(root: pathlib.Path | None = None) -> pathlib.Path | None:
    """The cached build for the checkout's current describe, if one exists
    and the tree is clean. A dirty tree never trusts a cache."""
    root = root or repo_root()
    if root is None:
        return None
    label = describe(root)
    if label.endswith("-dirty"):
        return None
    binary = cache_dir(root) / label / "choudoufu"
    return binary if binary.exists() else None


def ensure(root: pathlib.Path | None = None, *, log=print) -> str:
    """Return a path to a choudoufu built from this checkout, building it if
    the cache has no build for the current describe (or the tree is dirty,
    in which case every call rebuilds so a change is never demoed stale)."""
    root = root or repo_root()
    if root is None:
        raise RuntimeError("CHOUDOUFU_VERSION=local needs the example inside a choudoufu checkout (cmd/choudoufu/main.go was not found above it); set CHOUDOUFU_BIN instead")
    label = describe(root)
    binary = cache_dir(root) / label / "choudoufu"
    if binary.exists() and not label.endswith("-dirty"):
        return str(binary)
    binary.parent.mkdir(parents=True, exist_ok=True)
    log(f"building choudoufu from {root} ({label}) ...")
    env = {k: v for k, v in os.environ.items() if k != "PWD"}   # HANDOFF: env -u PWD on every go command
    # The same link-time stamp the release workflow uses (version.Fork), with
    # the describe as the fork version, so `choudoufu version` names this
    # build and preflight can tell it from plain OpenTofu. dev stays "yes".
    ldflags = f"-X github.com/intentius/choudoufu/version.Fork={label}"
    proc = subprocess.run(["go", "build", "-ldflags", ldflags, "-o", str(binary), "./cmd/choudoufu"], cwd=str(root), env=env,
                          capture_output=True, text=True, check=False)
    if proc.returncode != 0:
        raise RuntimeError(f"go build ./cmd/choudoufu failed in {root}:\n{proc.stderr.strip()[-2000:]}")
    log(f"built {binary}")
    return str(binary)


if __name__ == "__main__":
    print(ensure(log=lambda m: print(m, file=sys.stderr)))
