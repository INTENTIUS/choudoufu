"""The general seed: adopt an existing, state-backed estate onto live markers.

`seed --config <dir> --estate <name> --state <file>` copies a real
configuration into the run, swaps its backend for a live block, and runs
`live-import` against its state file: verify-only by default (a ratification
report, nothing written), and with `--approve` it stamps the two ownership
markers onto every verified resource. The state file is read once, read-only,
and never rewritten - a marker write is additive, so the estate keeps working
as ordinary state-backed OpenTofu if you stop here.

Adopting does not create resources; it stamps markers on ones that already
exist. So an adopted estate is the user's own, and teardown never touches it -
only the demo seed's own manifest is torn down.
"""

from __future__ import annotations

import dataclasses
import pathlib
import shutil

from . import config, guard, ui


def _strip_block(text: str, header_prefix: str) -> str:
    """Remove the first brace-delimited block whose line starts with
    header_prefix (e.g. `backend "`), matching braces so a nested block inside
    it is removed with it. Returns text unchanged when no such block is
    found."""
    idx = text.find(header_prefix)
    if idx < 0:
        return text
    brace = text.find("{", idx)
    if brace < 0:
        return text
    depth, i = 0, brace
    while i < len(text):
        if text[i] == "{":
            depth += 1
        elif text[i] == "}":
            depth -= 1
            if depth == 0:
                # drop the block and a trailing newline if present
                end = i + 1
                if end < len(text) and text[end] == "\n":
                    end += 1
                return text[:idx].rstrip(" ") + text[end:]
        i += 1
    return text


def to_live_hcl(text: str, estate: str) -> str:
    """Rewrite a state-backed config for the live backend: drop any `backend`
    block and add a `live { estate = ... }` inside the `terraform` block (or a
    fresh terraform block if the file has none). A config cannot carry both a
    backend and a live block, which is why the backend is removed rather than
    left beside it."""
    out = _strip_block(text, 'backend "')
    live = f'  live {{\n    estate = "{estate}"\n  }}\n'
    if "live {" in out or "live{" in out:
        return out  # already a live config; leave it
    tf = out.find("terraform")
    brace = out.find("{", tf) if tf >= 0 else -1
    if brace >= 0:
        return out[: brace + 1] + "\n" + live + out[brace + 1 :]
    return f"terraform {{\n{live}}}\n\n" + out


@dataclasses.dataclass(frozen=True)
class AdoptReport:
    estate: str
    approved: bool
    verified: int
    drifted: int
    missing: int
    untaggable: int
    unadmitted: int
    ok: bool  # verify-only: false when anything is unmatched (MISSING)


def _count(text: str, status: str) -> int:
    return sum(1 for line in text.splitlines() if status in line)


def seed_adopt(cfg: config.Config, config_dir: str, estate: str, state: str, approve: bool) -> AdoptReport:
    """Copy the config into the estate's working directory, swap its backend for
    a live block, init, and run live-import. Verify-only unless approve. Raises
    if the verify report shows any unmatched (MISSING) address, so a bid never
    reports a clean adopt over a resource that is not there."""
    workdir = cfg.workdir(estate)
    workdir.mkdir(parents=True, exist_ok=True)
    src = pathlib.Path(config_dir)
    for f in sorted(src.glob("*.tf")):
        text = f.read_text()
        rewritten = to_live_hcl(text, estate) if ("terraform" in text or "backend " in text) else text
        (workdir / f.name).write_text(rewritten)
    ui.rule(f"seed: adopt {estate} from {src}")
    ui.say(
        "The state file is read once, read-only, and never rewritten. "
        f"live-import verifies every resource it names against the live system"
        + ("; --approve then stamps the two ownership markers." if approve else ", writing nothing.")
    )
    guard.chdf(cfg, "init", "-input=false", "-no-color", cwd=str(workdir))
    args = ["live-import", "-state", str(pathlib.Path(state).resolve()), "-estate", estate, "-no-color"]
    if approve:
        args.append("-approve")
    res = guard.chdf(cfg, *args, cwd=str(workdir), destructive=approve, capture=True, check=False)
    text = res.stdout + ("\n" + res.stderr if res.stderr else "")
    report = AdoptReport(
        estate=estate, approved=approve,
        verified=_count(text, "VERIFIED"), drifted=_count(text, "DRIFTED"),
        missing=_count(text, "MISSING"), untaggable=_count(text, "UNTAGGABLE"),
        unadmitted=_count(text, "UNADMITTED_TYPE"),
        ok=(res.returncode == 0 and _count(text, "MISSING") == 0),
    )
    for label, n, good in [("verified", report.verified, True), ("drifted", report.drifted, None),
                           ("missing", report.missing, False if report.missing else None),
                           ("untaggable", report.untaggable, None), ("unadmitted", report.unadmitted, None)]:
        if n:
            ui.kv(f"  {label}", str(n), good)
    if not approve and not report.ok:
        raise guard.GuardError(
            f"adopt of {estate} is not clean: {report.missing} address(es) the state names are not live. "
            f"Nothing was written. Fix the config or state and rerun before --approve."
        )
    ui.ok(f"{estate} {'adopted (markers stamped)' if approve else 'verified, nothing written'}")
    return report
