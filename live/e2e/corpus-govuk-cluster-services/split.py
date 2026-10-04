#!/usr/bin/env python3
"""split.py: cut a published root into the live slice and its stock half.

corpus-govuk-cluster-services (#1878) crosses alphagov/govuk-infrastructure's
terraform/deployments/cluster-services. Most of that root is not the
estate's to own: 14 helm_release and 3 kubectl_manifest blocks are refused
in a live root (unadmitted-type, ruled in #1105), and the AWS IAM roles and
policies beside them have nothing to talk to on kind. The Kubernetes
compatibility page's answer to a root like this is a split: the Helm and
kubectl blocks go to a stock root of their own, with no live block, beside
the estate, and the estate keeps the objects it can own. This script makes
that split mechanically, so nothing about the live slice is retyped.

    split.py <root> <stock-root>

<root> is a copy of the published root, rewritten in place to the live
slice. <stock-root> is created (it must not exist) holding the stock half:
every block the split took out of <root>, verbatim, in files of the same
name, plus the variables and locals both halves read. The stock half is
recorded, not applied: its charts need IRSA roles, an AWS load balancer
controller and public DNS, which a kind cluster has none of.

Each top-level block is classified by what it declares, never by its name:

  stock  resource helm_release / kubectl_manifest / time_sleep /
         terraform_data (the last two only ever wait on a chart here);
         any aws_* resource or data source; data.tfe_outputs; a module
         from the registry; a local module whose own tree declares nothing
         live; and the root's terraform and provider blocks, which the
         caller writes again for kind
  both   variable, locals, output, and a module's own terraform block
  live   everything else

A local module whose tree declares both is split the same way, in both
copies, so the live root keeps module.gatekeeper with its Namespace and the
stock half keeps the same call with the chart.

Two rewrites are applied to live blocks only, and both are reported:

  depends_on  an entry naming a block that went to the stock half is
              dropped (an empty list drops the argument), since a live
              root cannot order itself after an object it no longer has
  tfe_outputs data.tfe_outputs.<ws>.nonsensitive_values becomes
              var.<ws>; the caller declares those variables and supplies
              placeholder values in terraform.tfvars

Prints one JSON object on stdout: the blocks on each side by type, the
rewrites made, the tfe_outputs workspaces turned into variables, and any
reference a live block still makes to a stock block ("dangling"), which
the caller requires to be empty. Exits non-zero on anything it cannot
classify.
"""

import json
import os
import re
import shutil
import sys

STOCK_RESOURCE_TYPES = {"helm_release", "kubectl_manifest", "time_sleep", "terraform_data"}
BOTH_KINDS = {"variable", "locals", "output"}
BLOCK_START = re.compile(r'^(resource|data|module|provider|terraform|locals|variable|output|moved|import|check|removed)\b(.*)$')
LABELS = re.compile(r'"([^"]*)"')
HEREDOC = re.compile(r'<<-?([A-Za-z_][A-Za-z0-9_]*)\s*$')
SOURCE = re.compile(r'^\s*source\s*=\s*"([^"]+)"', re.M)
DEPENDS_ON = re.compile(r'^(\s*)depends_on\s*=\s*\[')
TFE_REF = re.compile(r'data\.tfe_outputs\.([A-Za-z0-9_]+)\.nonsensitive_values')


def braces(line, state):
    """Net brace depth change of one line, outside strings, comments and
    heredoc bodies. state carries an open heredoc's terminator across lines."""
    if state.get("heredoc"):
        if line.strip() == state["heredoc"]:
            state["heredoc"] = None
        return 0
    depth = 0
    i = 0
    in_str = False
    while i < len(line):
        c = line[i]
        if in_str:
            if c == "\\":
                i += 2
                continue
            if c == '"':
                in_str = False
        elif state.get("comment"):
            if line.startswith("*/", i):
                state["comment"] = False
                i += 2
                continue
        else:
            if c == '"':
                in_str = True
            elif c == "#" or line.startswith("//", i):
                break
            elif line.startswith("/*", i):
                state["comment"] = True
                i += 2
                continue
            elif c == "{":
                depth += 1
            elif c == "}":
                depth -= 1
        i += 1
    m = HEREDOC.search(line)
    if m and not in_str:
        state["heredoc"] = m.group(1)
    return depth


def blocks(text, path):
    """Splits a .tf file into [(kind, labels, text)]; kind is None for the
    loose lines (blank lines and comments) between blocks, which are kept
    with the block that follows them."""
    out = []
    pending = []
    cur = None
    depth = 0
    state = {}
    for line in text.splitlines(keepends=True):
        if cur is None:
            m = BLOCK_START.match(line)
            if m and not state.get("comment"):
                cur = {"kind": m.group(1), "labels": LABELS.findall(m.group(2).split("{")[0]), "lines": pending + [line]}
                pending = []
                depth = braces(line, state)
                if depth == 0:
                    out.append((cur["kind"], cur["labels"], "".join(cur["lines"])))
                    cur = None
                continue
            braces(line, state)
            pending.append(line)
            continue
        cur["lines"].append(line)
        depth += braces(line, state)
        if depth == 0:
            out.append((cur["kind"], cur["labels"], "".join(cur["lines"])))
            cur = None
    if cur is not None:
        sys.exit("split.py: %s: a %s block never closes" % (path, cur["kind"]))
    if pending:
        out.append((None, [], "".join(pending)))
    return out


def tf_files(d):
    return sorted(f for f in os.listdir(d)
                  if f.endswith(".tf") and os.path.isfile(os.path.join(d, f)) and not os.path.islink(os.path.join(d, f)))


def is_local(source):
    return source.startswith("./") or source.startswith("../")


def module_source(text):
    m = SOURCE.search(text)
    return m.group(1) if m else ""


def has_live(d, seen=None):
    """Whether a module directory's own tree declares anything live."""
    seen = seen or set()
    real = os.path.realpath(d)
    if real in seen:
        return False
    seen.add(real)
    for f in tf_files(d):
        with open(os.path.join(d, f)) as fh:
            for kind, labels, body in blocks(fh.read(), os.path.join(d, f)):
                if classify(kind, labels, body, d, seen, top=False) == "live":
                    return True
    return False


def classify(kind, labels, body, d, seen=None, top=True):
    if kind is None or kind in BOTH_KINDS:
        return "both"
    if kind == "terraform":
        return "stock" if top else "both"
    if kind == "provider":
        return "stock"
    if kind in ("resource", "data"):
        t = labels[0] if labels else ""
        if kind == "resource" and t in STOCK_RESOURCE_TYPES:
            return "stock"
        if t.startswith("aws_") or (kind == "data" and t == "tfe_outputs"):
            return "stock"
        return "live"
    if kind == "module":
        src = module_source(body)
        if not is_local(src):
            return "stock"
        return "live" if has_live(os.path.join(d, src), seen) else "stock"
    sys.exit("split.py: %s: a top-level %s block is not one this split knows how to place" % (d, kind))


def address(kind, labels):
    if kind == "resource":
        return "%s.%s" % (labels[0], labels[1])
    if kind == "data":
        return "data.%s.%s" % (labels[0], labels[1])
    if kind == "module":
        return "module.%s" % labels[0]
    return None


def prune_depends_on(body, keep):
    """Drops depends_on entries not in keep. Returns (body, dropped)."""
    lines = body.splitlines(keepends=True)
    out = []
    dropped = []
    i = 0
    while i < len(lines):
        m = DEPENDS_ON.match(lines[i])
        if not m:
            out.append(lines[i])
            i += 1
            continue
        j = i
        chunk = [lines[j]]
        while "]" not in re.sub(r'#.*$', '', lines[j]):
            j += 1
            if j == len(lines):
                sys.exit("split.py: a depends_on list never closes:\n" + "".join(chunk))
            chunk.append(lines[j])
        text = "".join(chunk)
        inner = text[text.index("[") + 1:text.rindex("]")]
        refs = [r.strip() for r in re.sub(r'(#|//)[^\n]*', '', inner).replace("\n", ",").split(",") if r.strip()]
        kept = [r for r in refs if r in keep]
        dropped += [r for r in refs if r not in keep]
        if kept == refs:
            out += chunk
        elif kept:
            out.append("%sdepends_on = [%s]\n" % (m.group(1), ", ".join(kept)))
        i = j + 1
    return "".join(out), dropped


def split_dir(live_dir, stock_dir, rel, report):
    """Splits one module directory's own .tf files in place (live_dir) and
    into stock_dir, then recurses into the local modules the live side
    still calls. A symlinked .tf file (the root's variables-common.tf) is
    left as the link it is in both copies: it declares variables only."""
    where = "root" if rel == "." else rel
    parsed = {}
    live_addrs, stock_addrs = set(), set()
    for f in tf_files(live_dir):
        with open(os.path.join(live_dir, f)) as fh:
            bs = blocks(fh.read(), os.path.join(rel, f))
        placed = []
        for kind, labels, body in bs:
            side = classify(kind, labels, body, live_dir, top=(rel == "."))
            placed.append((side, kind, labels, body))
            a = address(kind, labels)
            if a:
                (live_addrs if side == "live" else stock_addrs).add(a)
        parsed[f] = placed

    recurse = []
    for f, placed in parsed.items():
        live_out, stock_out = [], []
        for side, kind, labels, body in placed:
            if side in ("stock", "both"):
                stock_out.append(body)  # the stock half is the published text
            if side == "stock":
                tally(report["stock"], kind, labels)
                src = module_source(body) if kind == "module" else ""
                if src and is_local(src):
                    count_tree(os.path.join(live_dir, src), report["stock"])
                continue
            if side == "live":
                body, dropped = prune_depends_on(body, live_addrs)
                for d in dropped:
                    report["depends_on_dropped"].append("%s: %s no longer depends on %s" % (where, address(kind, labels), d))
                tally(report["live"], kind, labels)
                if kind == "module":
                    recurse.append(module_source(body))
            report["tfe_vars"].update(TFE_REF.findall(body))
            live_out.append(TFE_REF.sub(lambda m: "var." + m.group(1), body))
        write_or_remove(os.path.join(live_dir, f), live_out)
        write_or_remove(os.path.join(stock_dir, f), stock_out)

    # A reference a live block still makes to a stock block, outside the
    # depends_on lists already pruned, would leave the live root invalid.
    for f in tf_files(live_dir):
        with open(os.path.join(live_dir, f)) as fh:
            text = re.sub(r'(?m)^\s*(#|//).*$', '', fh.read())
        for ref in sorted(stock_addrs):
            if re.search(r'(?<![\w.])' + re.escape(ref) + r'\b', text):
                report["dangling"].append("%s/%s references %s" % (rel, f, ref))

    for src in recurse:
        split_dir(os.path.normpath(os.path.join(live_dir, src)), os.path.normpath(os.path.join(stock_dir, src)),
                  os.path.normpath(os.path.join(rel, src)), report)


def tally(counts, kind, labels):
    key = "%s %s" % (kind, labels[0]) if labels else kind
    counts[key] = counts.get(key, 0) + 1


def count_tree(d, counts, seen=None):
    """Tallies every resource, data source and module call in a stock-only
    local module's tree, so the stock half's totals are its whole content."""
    seen = seen or set()
    real = os.path.realpath(d)
    if real in seen:
        return
    seen.add(real)
    for f in tf_files(d):
        with open(os.path.join(d, f)) as fh:
            for kind, labels, body in blocks(fh.read(), os.path.join(d, f)):
                if kind in ("resource", "data", "module"):
                    tally(counts, kind, labels)
                if kind == "module" and is_local(module_source(body)):
                    count_tree(os.path.join(d, module_source(body)), counts, seen)


def write_or_remove(path, chunks):
    text = "".join(chunks)
    if text.strip():
        with open(path, "w") as fh:
            fh.write(text)
    elif os.path.exists(path):
        os.remove(path)


def main(root, stock):
    if os.path.exists(stock):
        sys.exit("split.py: %s already exists" % stock)
    shutil.copytree(root, stock, symlinks=True, ignore=shutil.ignore_patterns(".terraform"))
    report = {"live": {}, "stock": {}, "depends_on_dropped": [], "tfe_vars": set(), "dangling": []}
    split_dir(root, stock, ".", report)
    report["tfe_vars"] = sorted(report["tfe_vars"])
    json.dump(report, sys.stdout, indent=1, sort_keys=True)
    sys.stdout.write("\n")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: split.py <root> <stock-root>")
    main(sys.argv[1], sys.argv[2])
