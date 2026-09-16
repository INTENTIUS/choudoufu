#!/usr/bin/env python3
"""order.py: put back the dependency tfk8s dropped.

tfk8s converts each YAML document into an independent
`resource "kubernetes_manifest"` block and emits no `depends_on` anywhere,
so the converted cert-manager bundle races its own Namespace: measured on
kind, 46 of the 47 objects apply and the 47th fails

    Error: Kubernetes API Error: NotFound namespaces [cert-manager]

which is a property of the CONVERTED ROOT, not of the CRD ordering #1173
rules on, and is fixed here rather than with the pre-apply mechanism.

The rule, and it is narrow on purpose: an object whose OWN
`metadata.namespace` is `cert-manager` depends on the block that creates
that Namespace. Nothing else does. `metadata.namespace` sits at exactly six
spaces of indentation in tfk8s's output - a ClusterRoleBinding's
`subjects[].namespace` is at eight and a webhook's
`clientConfig.service.namespace` at twelve, and neither of those objects is
IN the namespace - so the depth is the discriminator and a bare grep for
the string would be wrong. The four objects in `kube-system` need nothing:
that namespace is the cluster's own.

Reads and rewrites the file named on the command line, in place, and prints
what it changed. Idempotent: a file that already carries the depends_on
lines is left alone.
"""

import re
import sys

NS_BLOCK = "namespace_cert_manager"
DEP = "  depends_on = [kubernetes_manifest.%s]\n" % NS_BLOCK
OWN_NAMESPACE = re.compile(r'^      "namespace" = "cert-manager"$')
BLOCK_START = re.compile(r'^resource "kubernetes_manifest" "([^"]+)" \{$')


def main(path):
    with open(path) as f:
        lines = f.readlines()

    # Split into blocks: a block runs from its `resource ...{` line to the
    # next line that is exactly "}" at column zero.
    out = []
    i = 0
    changed = []
    while i < len(lines):
        m = BLOCK_START.match(lines[i])
        if not m:
            out.append(lines[i])
            i += 1
            continue
        name = m.group(1)
        j = i
        while j < len(lines) and lines[j] != "}\n":
            j += 1
        if j >= len(lines):
            sys.exit("order.py: block %s at line %d is never closed" % (name, i + 1))
        block = lines[i:j]  # without the closing brace
        needs = name != NS_BLOCK and any(OWN_NAMESPACE.match(l) for l in block)
        already = any(l == DEP for l in block)
        out.extend(block)
        if needs and not already:
            out.append(DEP)
            changed.append(name)
        out.append(lines[j])
        i = j + 1

    with open(path, "w") as f:
        f.writelines(out)
    print("order.py: %d block(s) now depend on kubernetes_manifest.%s" % (len(changed), NS_BLOCK))
    for name in changed:
        print("  " + name)
    if not changed:
        print("  (nothing to do; the file already carries them)")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: order.py <cert-manager.tf>")
    main(sys.argv[1])
