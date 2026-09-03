"""The run lifecycle: stand the monolith up, tear the whole run down, and say
what is live right now.

Two rules make this safe to run against a real account over and over:

* Teardown works from the manifest setup wrote, never from a guess. It
  destroys each estate the run applied and then verifies nothing carrying the
  run's prefix is left, refusing to call the run clean while anything remains.
* Every mutation goes through guard, so it is fenced to this run's tree and
  account and lands in the transcript. env orchestrates; guard is the thing
  that actually spawns a process.

The apply/plan/destroy helpers are deliberately thin — the beats compose them
into the story, and a live script can call the same ones.
"""

from __future__ import annotations

import json
import time

from . import config, fixture, guard, ui


# --------------------------------------------------------------------------
# The manifest — the teardown ledger
# --------------------------------------------------------------------------

def _read_manifest(cfg: config.Config) -> dict:
    if cfg.manifest_path.exists():
        return json.loads(cfg.manifest_path.read_text())
    return {"run_id": cfg.run_id, "prefix": cfg.prefix, "region": cfg.region, "estates": []}


def _write_manifest(cfg: config.Config, manifest: dict) -> None:
    cfg.run_dir.mkdir(parents=True, exist_ok=True)
    cfg.manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")


def record_estate(cfg: config.Config, estate: str) -> None:
    """Note that an estate has been applied, so teardown knows to destroy it.
    Called after every apply, including the per-team adoptions a decompose
    beat runs, so a run interrupted mid-demo still tears down completely."""
    manifest = _read_manifest(cfg)
    entry = {"estate": estate, "workdir": str(cfg.workdir(estate))}
    if entry not in manifest["estates"]:
        manifest["estates"].append(entry)
    _write_manifest(cfg, manifest)


# --------------------------------------------------------------------------
# Thin choudoufu helpers the beats and live scripts share
# --------------------------------------------------------------------------

def write_config(cfg: config.Config, estate: str, hcl: str) -> str:
    """Write one estate's config into its own working directory under the run
    tree and return the directory. Nothing is applied yet."""
    workdir = cfg.workdir(estate)
    workdir.mkdir(parents=True, exist_ok=True)
    (workdir / "main.tf").write_text(hcl)
    return str(workdir)


def init(cfg: config.Config, estate: str) -> None:
    guard.chdf(cfg, "init", "-input=false", "-no-color", cwd=str(cfg.workdir(estate)))


def apply(cfg: config.Config, estate: str) -> None:
    """Apply an estate — destructive, fenced to its workdir under the run tree,
    and recorded so teardown will find it."""
    guard.chdf(
        cfg, "apply", "-auto-approve", "-input=false", "-no-color",
        cwd=str(cfg.workdir(estate)), destructive=True,
    )
    record_estate(cfg, estate)


def plan(cfg: config.Config, estate: str, *extra: str, capture: bool = False) -> guard.Result:
    """Plan an estate — a read. Pass -refresh=false via extra for the warm,
    file-speed path. check=False so a nonzero exit is returned for grading
    rather than raised."""
    return guard.chdf(
        cfg, "plan", "-input=false", "-no-color", *extra,
        cwd=str(cfg.workdir(estate)), capture=capture, check=False,
    )


def destroy(cfg: config.Config, estate: str) -> None:
    guard.chdf(
        cfg, "destroy", "-auto-approve", "-input=false", "-no-color",
        cwd=str(cfg.workdir(estate)), destructive=True,
    )


# --------------------------------------------------------------------------
# Lifecycle
# --------------------------------------------------------------------------

def setup(cfg: config.Config) -> None:
    """Stand the monolith up: one estate owning every team's resources, the
    state an org's terralith starts in. Asserts the account and binary first,
    so a mis-set profile stops here rather than half-applying."""
    guard.preflight(cfg)
    ui.rule("setup: standing up the terralith monolith")
    workdir = write_config(cfg, cfg.monolith_estate, fixture.monolith_hcl(cfg))
    ui.say(
        f"One config, estate {cfg.monolith_estate}, "
        f"{len(fixture.FIXTURE_TEAMS)} teams' worth of IAM and log groups — "
        f"the monolith before anyone splits it."
    )
    init(cfg, cfg.monolith_estate)
    apply(cfg, cfg.monolith_estate)
    ui.ok(f"monolith up under {cfg.monolith_estate}; manifest at {cfg.manifest_path}")


def teardown(cfg: config.Config) -> None:
    """Destroy everything the run applied, newest estate first, then verify
    nothing with the run's prefix is left. Idempotent: safe to run twice, and
    the safety net you reach for if a beat crashes mid-demo."""
    ui.rule("teardown: destroying this run")
    manifest = _read_manifest(cfg)
    # Destroy in reverse apply order so a resource carved into a later estate
    # is released before the estate it came from is torn down.
    for entry in reversed(manifest.get("estates", [])):
        estate = entry["estate"]
        workdir = cfg.workdir(estate)
        if not (workdir / "main.tf").exists():
            ui.warn(f"{estate}: no working dir, skipping (already cleaned?)")
            continue
        ui.say(f"destroying estate {estate}")
        destroy(cfg, estate)
    _verify_gone(cfg)


def _verify_gone(cfg: config.Config) -> None:
    """Refuse to call the run clean while anything carrying its prefix remains.
    This is the minimal check — an IAM role listing by prefix; choudoufu-e9's
    sweep.assert_torn_down, when present, replaces it with the fuller
    list-by-tag-and-by-name refusal ad and the pricing page use."""
    try:
        from . import sweep  # provided separately; optional
        sweep.assert_torn_down(cfg)
        return
    except ImportError:
        pass
    res = guard.aws(
        cfg, "iam", "list-roles",
        "--query", f"Roles[?starts_with(RoleName, `{cfg.prefix}`)].RoleName",
        "--output", "text", check=False,
    )
    leftovers = res.stdout.split() if res.ok else []
    if leftovers:
        ui.err(f"teardown left roles behind: {' '.join(leftovers)}")
        raise guard.GuardError(
            f"{len(leftovers)} resource(s) with prefix {cfg.prefix} remain after teardown; "
            f"not calling this run clean"
        )
    ui.ok("nothing with this run's prefix remains — clean")


def status(cfg: config.Config) -> None:
    """What the run has applied and what is live now, read straight off AWS so
    the presenter always knows the ground truth before a beat."""
    ui.rule(f"status: run {cfg.run_id}")
    manifest = _read_manifest(cfg)
    estates = [e["estate"] for e in manifest.get("estates", [])]
    ui.kv("estates applied", ", ".join(estates) or "(none)")
    res = guard.aws(
        cfg, "iam", "list-roles",
        "--query", f"Roles[?starts_with(RoleName, `{cfg.prefix}`)].RoleName",
        "--output", "text", check=False,
    )
    roles = res.stdout.split() if res.ok else []
    ui.kv("live roles", str(len(roles)), good=bool(roles))
    for role in roles:
        tags = guard.aws(cfg, "iam", "list-role-tags", "--role-name", role, "--output", "json", check=False)
        estate = "?"
        if tags.ok:
            from . import verify
            estate = verify.estate_of_role(tags.stdout) or "(untagged)"
        ui.kv(f"  {role}", f"tofu-estate={estate}")


def reset(cfg: config.Config) -> None:
    """Tear down, then stand up again — a fresh rehearsal on the same run id."""
    teardown(cfg)
    time.sleep(1)
    setup(cfg)
