"""Executing a carve plan, and moving the config blocks a move needs.

`execute_carve` runs a whole `carve.json`: for each move, `live-mv
-from-estate=<from>` rewrites the resource's ownership tag into its destination
estate, and a recording apply per destination binds the moved resources into
that estate's store. The carve plan is the fence - the only tags written are
those of the addresses the plan names, into the estates it names, through
live-mv and its own refusals.

`move_block` moves a resource block or module call between configurations,
which a real carve needs because a resource may only carry a destination
estate's marker once its block lives in that estate's configuration. It moves a
top-level `resource` or `module` block automatically and returns the block
untouched for the operator to move by hand when it is anything more tangled;
either way live-mv's refusal is the real check, not this text edit.
"""

from __future__ import annotations

import contextlib
import dataclasses
import pathlib
import re
from collections.abc import Iterator

from . import config, env, govern, guard, moveset, ui


def execute_carve(cfg: config.Config, carve_path: str | pathlib.Path) -> moveset.CarveSet:
    """Run every move in the plan, grouped by destination so each estate applies
    once. live-mv writes only the named resource's tag; the apply records the
    moved resources under the destination."""
    cs = moveset.load_carve(pathlib.Path(carve_path).read_text())
    ui.rule(f"move: executing {len(cs.moves)} move(s) from the carve plan")
    by_dest: dict[str, list[moveset.CarveMove]] = {}
    for m in cs.moves:
        by_dest.setdefault(m.to_estate, []).append(m)
    for dest, moves in by_dest.items():
        for m in moves:
            guard.chdf(
                cfg, "live-mv", "-from-estate", m.from_estate, m.address, m.target,
                cwd=str(cfg.workdir(dest)), destructive=True, capture=True, check=False,
                label=f"move {m.address} into {dest}",
            )
        env.apply(cfg, dest)  # recording apply: bind the moved resources into this estate's store
        govern.read_inventory(cfg, dest)
        ui.ok(f"{len(moves)} move(s) into {dest}")
    return cs


# --------------------------------------------------------------------------
# Config block mover
# --------------------------------------------------------------------------

def _block_span(text: str, header_re: re.Pattern) -> tuple[int, int] | None:
    """The [start, end) span of the first top-level block whose header matches,
    counting braces so nested blocks are included. None when not found."""
    m = header_re.search(text)
    if not m:
        return None
    brace = text.find("{", m.start())
    if brace < 0:
        return None
    depth, i = 0, brace
    while i < len(text):
        if text[i] == "{":
            depth += 1
        elif text[i] == "}":
            depth -= 1
            if depth == 0:
                end = i + 1
                if end < len(text) and text[end] == "\n":
                    end += 1
                return (m.start(), end)
        i += 1
    return None


def _header_re(address: str) -> re.Pattern | None:
    """The block header for an address: `resource "type" "name"` or
    `module "name"`. Returns None for an address this mover does not move
    automatically (an indexed instance, a nested address)."""
    if "[" in address or address.count(".") > 1:
        return None
    if address.startswith("module."):
        name = address.split(".", 1)[1]
        return re.compile(rf'^module\s+"{re.escape(name)}"\s*\{{', re.M)
    if "." in address:
        rtype, name = address.split(".", 1)
        return re.compile(rf'^resource\s+"{re.escape(rtype)}"\s+"{re.escape(name)}"\s*\{{', re.M)
    return None


def move_block(src_text: str, dst_text: str, address: str) -> tuple[str, str, bool]:
    """Move `address`'s block from src_text to dst_text. Returns
    (new_src, new_dst, moved). moved is False - and the texts are unchanged -
    when the block is not a simple top-level resource or module block; the
    operator moves it by hand, and live-mv refuses if it is not where it should
    be, so a missed move is caught, never silently wrong."""
    header = _header_re(address)
    if header is None:
        return src_text, dst_text, False
    span = _block_span(src_text, header)
    if span is None:
        return src_text, dst_text, False
    block = src_text[span[0]:span[1]]
    new_src = src_text[:span[0]] + src_text[span[1]:]
    sep = "" if dst_text.endswith("\n\n") or dst_text == "" else ("\n" if dst_text.endswith("\n") else "\n\n")
    new_dst = dst_text + sep + block
    return new_src, new_dst, True


# --------------------------------------------------------------------------
# Staging: put each move's block in its destination config before the move
# --------------------------------------------------------------------------

@dataclasses.dataclass(frozen=True)
class StageReport:
    """What staging did for a carve set. ``staged`` blocks were carried into
    their destination config by hand; ``already`` were declared there before we
    looked; ``manual`` cannot be carried automatically (indexed, nested, or a
    rename) and are left for the operator - the dry-run and live-mv refuse them
    until they are moved, which is the real check."""
    staged: list[tuple[str, str]]
    already: list[tuple[str, str]]
    manual: list[tuple[str, str]]


def _config_path(cfg: config.Config, estate: str) -> pathlib.Path:
    return cfg.workdir(estate) / "main.tf"


def stage_blocks(cfg: config.Config, carve_path: str | pathlib.Path) -> StageReport:
    """Load a carve plan and stage every move's block into its destination
    config. A local file edit only - no cloud write - so a dry-run of a
    cross-estate carve resolves the address instead of refusing it."""
    cs = moveset.load_carve(pathlib.Path(carve_path).read_text())
    return _stage_set(cfg, cs)


def _stage_set(cfg: config.Config, cs: moveset.CarveSet) -> StageReport:
    staged: list[tuple[str, str]] = []
    already: list[tuple[str, str]] = []
    manual: list[tuple[str, str]] = []
    for m in cs.moves:
        # A rename is not a pure block move; leave it to the operator.
        header = None if m.new_address else _header_re(m.address)
        dst_path = _config_path(cfg, m.to_estate)
        dst_text = dst_path.read_text() if dst_path.exists() else ""
        if header is None:
            manual.append((m.address, m.to_estate))
            continue
        if header.search(dst_text):
            already.append((m.address, m.to_estate))
            continue
        src_path = _config_path(cfg, m.from_estate)
        src_text = src_path.read_text() if src_path.exists() else ""
        new_src, new_dst, moved = move_block(src_text, dst_text, m.address)
        if not moved:
            manual.append((m.address, m.to_estate))
            continue
        dst_path.parent.mkdir(parents=True, exist_ok=True)
        dst_path.write_text(new_dst)
        if src_path.exists():
            src_path.write_text(new_src)
        staged.append((m.address, m.to_estate))
    return StageReport(staged=staged, already=already, manual=manual)


@contextlib.contextmanager
def staged_for_preview(cfg: config.Config, carve_path: str | pathlib.Path) -> Iterator[StageReport]:
    """Stage blocks so a dry-run can resolve them, then restore every config on
    the way out - so a preview leaves nothing changed, not the cloud and not
    even the local files. Moving a block out of its source config can break a
    reference the source keeps (an attachment that names a moved policy), which
    is exactly why a preview must not persist the edit; only a real move does,
    and a real move carries the whole set."""
    est_root = cfg.run_dir / "estates"
    paths = [wd / "main.tf" for wd in sorted(est_root.iterdir())] if est_root.is_dir() else []
    snap = {p: (p.read_text() if p.exists() else None) for p in paths}
    rep = stage_blocks(cfg, carve_path)
    try:
        yield rep
    finally:
        for p, text in snap.items():
            if text is None:
                p.unlink(missing_ok=True)
            else:
                p.write_text(text)
