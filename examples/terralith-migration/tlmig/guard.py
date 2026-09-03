"""The fenced execution layer — every command the example runs goes through
here, and it is the reason this is safe to improvise on top of live.

Four fences, in the order they catch a mistake:

1. Preflight. Before any beat runs, assert the caller's credentials resolve
   to the one allowed account and the binary is exactly the pinned release.
   A mis-set profile or a drifted binary stops the run here, not halfway
   through mutating a cloud.

2. Account, on every call. The credentials are process-wide, so preflight is
   the account gate; nothing below re-checks it, but nothing below can reach
   a different account either.

3. Run scope, on every destructive call. A choudoufu apply or destroy must
   run inside this run's own working tree, and a raw ``aws`` delete must name
   a resource carrying this run's prefix. The fence is on the target, so it
   holds no matter what a live-typed command asks for.

4. A human, on every destructive call, unless --auto. The confirmation is the
   last stop; the fences above already decided WHAT could be destroyed, so a
   fat-fingered "yes" still cannot reach outside the run.

Everything that runs is appended to the transcript with a timestamp, so after
the demo there is an exact, ordered record of what touched the account.

Reads (``aws`` describe/list, ``choudoufu plan``) are not destructive and are
not fenced or confirmed — they are how the governance guard reads its neutral
facts, and gating them would only make the demo slower without making it
safer.
"""

from __future__ import annotations

import dataclasses
import datetime
import pathlib
import shlex
import subprocess
import time

from . import config, ui


class GuardError(Exception):
    """A fence refused, or a required command failed. Always fatal to the run;
    the message says which fence and why."""


@dataclasses.dataclass
class Result:
    argv: list[str]
    returncode: int
    stdout: str
    stderr: str
    seconds: float

    @property
    def ok(self) -> bool:
        return self.returncode == 0

    def lines(self) -> list[str]:
        return self.stdout.splitlines()


def _transcript(cfg: config.Config, argv: list[str], cwd: str | None) -> None:
    cfg.run_dir.mkdir(parents=True, exist_ok=True)
    stamp = datetime.datetime.now().isoformat(timespec="seconds")
    where = f" (in {cwd})" if cwd else ""
    with cfg.transcript_path.open("a") as fh:
        fh.write(f"{stamp}  {shlex.join(argv)}{where}\n")


def _run(
    cfg: config.Config,
    argv: list[str],
    *,
    cwd: str | None = None,
    capture: bool = False,
    env: dict | None = None,
) -> Result:
    """The one place a subprocess is spawned. Always an argument list, never a
    shell string, so a resource name with a space or a glob character is data,
    not syntax."""
    _transcript(cfg, argv, cwd)
    started = time.monotonic()
    proc = subprocess.run(
        argv,
        cwd=cwd,
        capture_output=capture,
        text=True,
        env=env,
    )
    return Result(
        argv=argv,
        returncode=proc.returncode,
        stdout=proc.stdout or "" if capture else "",
        stderr=proc.stderr or "" if capture else "",
        seconds=time.monotonic() - started,
    )


# --------------------------------------------------------------------------
# Fence 1: preflight
# --------------------------------------------------------------------------

def preflight(cfg: config.Config) -> None:
    """Assert the account and the binary before a run touches anything. Raises
    GuardError on any mismatch so setup cannot proceed against the wrong cloud
    or a version whose numbers would not match the docs."""
    who = _run(cfg, ["aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text"], capture=True)
    if not who.ok:
        raise GuardError(f"could not read the AWS caller identity; are credentials configured?\n{who.stderr.strip()}")
    account = who.stdout.strip()
    if account != cfg.account_id:
        raise GuardError(
            f"credentials resolve to account {account}, but this example is fenced to {cfg.account_id}. "
            f"Set the right AWS_PROFILE and try again."
        )

    ver = _run(cfg, [cfg.binary, "version"], capture=True)
    if not ver.ok:
        raise GuardError(f"could not run `{cfg.binary} version`; is choudoufu {cfg.version} on PATH (or CHOUDOUFU_BIN)?")
    first = ver.stdout.splitlines()[0] if ver.stdout else ""
    # e.g. "choudoufu v0.10.0 (based on OpenTofu v1.13.0-dev)". Assert on the
    # "choudoufu <version>" pair only, never the whole line: the OpenTofu base
    # ("v1.13.0-dev") tracks upstream's VERSION and changes under us when the
    # base is bumped, which is not this example's pin.
    tokens = first.split()
    if len(tokens) < 2 or tokens[0] != "choudoufu" or tokens[1] != cfg.version:
        raise GuardError(
            f"binary reports `{first.strip()}`, but this example is pinned to choudoufu {cfg.version}. "
            f"A different build would change the measured numbers."
        )
    ui.ok(f"account {account}, choudoufu {cfg.version}")


# --------------------------------------------------------------------------
# Fence 3: run-scope helpers
# --------------------------------------------------------------------------

def _assert_in_run(cfg: config.Config, cwd: str | None) -> None:
    if cwd is None:
        raise GuardError("a destructive choudoufu command must name the estate working directory it runs in")
    resolved = pathlib.Path(cwd).resolve()
    if cfg.run_dir.resolve() not in resolved.parents and resolved != cfg.run_dir.resolve():
        raise GuardError(
            f"refusing to run a destructive command in {resolved}, which is outside this run's tree {cfg.run_dir}"
        )


def assert_owned_name(cfg: config.Config, name: str) -> None:
    """Every raw-``aws`` delete that names a resource passes through here: the
    name must carry this run's prefix. It is what makes teardown incapable of
    deleting anything but this run's own resources."""
    if not name.startswith(cfg.prefix):
        raise GuardError(
            f"refusing a destructive operation on {name!r}: it does not carry this run's prefix {cfg.prefix!r}"
        )


# --------------------------------------------------------------------------
# The two verbs the rest of the example (and live scripts) call
# --------------------------------------------------------------------------

def chdf(
    cfg: config.Config,
    *args: str,
    cwd: str | None = None,
    destructive: bool = False,
    capture: bool = False,
    check: bool = True,
) -> Result:
    """Run the pinned choudoufu binary. A plan is a read; an apply, destroy or
    move is destructive and must name a cwd inside this run and clear a
    confirmation. Set check=False to inspect a nonzero result instead of
    raising (the guard reads plan output that way)."""
    argv = [cfg.binary, *args]
    if destructive:
        _assert_in_run(cfg, cwd)
        ui.cmd(f"choudoufu {' '.join(args)}")
        if not ui.confirm(f"run this against account {cfg.account_id}?"):
            raise GuardError("declined at the confirmation prompt")
    else:
        ui.cmd(f"choudoufu {' '.join(args)}")
    res = _run(cfg, argv, cwd=cwd, capture=capture)
    if check and not res.ok:
        raise GuardError(f"`choudoufu {' '.join(args)}` failed (exit {res.returncode})\n{res.stderr.strip()}")
    return res


def aws(
    cfg: config.Config,
    *args: str,
    destructive: bool = False,
    owned_name: str | None = None,
    capture: bool = True,
    check: bool = True,
) -> Result:
    """Run the AWS CLI. Reads (the guard's neutral facts, teardown's
    verification) are unfenced. A destructive call must pass owned_name and it
    must carry this run's prefix, plus a confirmation unless --auto."""
    argv = ["aws", *args]
    if destructive:
        if owned_name is None:
            raise GuardError("a destructive aws call must pass owned_name for the run-scope fence")
        assert_owned_name(cfg, owned_name)
        ui.cmd(f"aws {' '.join(args)}")
        if not ui.confirm(f"delete {owned_name} in account {cfg.account_id}?"):
            raise GuardError("declined at the confirmation prompt")
    res = _run(cfg, argv, capture=capture)
    if check and not res.ok:
        raise GuardError(f"`aws {' '.join(args)}` failed (exit {res.returncode})\n{res.stderr.strip()}")
    return res
