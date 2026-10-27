// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPublicPackagesImportNothingInternal is GitHub issue #2281's guard,
// TestImportsNothingInternal widened to every package under pkg/: none of
// them, test dependencies included, depends on anything in this module's
// internal tree, so another module (plunge) can import each one. It asks
// the go tool for each package's whole dependency graph rather than
// reading import lines, so an internal package reached through some other
// import is caught too.
func TestPublicPackagesImportNothingInternal(t *testing.T) {
	const pattern = "github.com/intentius/choudoufu/pkg/..."
	const internalTree = "github.com/intentius/choudoufu/internal/"

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("this guard needs the go tool on PATH to list %s's dependencies: %s", pattern, err)
	}
	pkgsOut, err := exec.Command(goTool, "list", pattern).Output()
	if err != nil {
		t.Fatalf("go list %s: %s", pattern, err)
	}
	pkgs := strings.Fields(string(pkgsOut))
	// The six packages GitHub issue #2281 moved, and the vocabulary they
	// take: a listing without them is not reading the tree it thinks it is.
	for _, want := range []string{"azsweep", "cloudcontrol", "gcpsweep", "kubesweep", "registry", "staterecord", "markerkeys", "k8stest", "adoption"} {
		found := false
		for _, p := range pkgs {
			found = found || p == "github.com/intentius/choudoufu/pkg/"+want
		}
		if !found {
			t.Fatalf("go list %s did not list pkg/%s among %v", pattern, want, pkgs)
		}
	}
	for _, p := range pkgs {
		out, err := exec.Command(goTool, "list", "-deps", "-test", p).Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				t.Fatalf("go list -deps -test %s: %s\n%s", p, err, ee.Stderr)
			}
			t.Fatalf("go list -deps -test %s: %s", p, err)
		}
		for _, dep := range strings.Fields(string(out)) {
			if strings.HasPrefix(dep, internalTree) {
				t.Errorf("%s depends on %s; every package under pkg/ has to stay importable from another module", p, dep)
			}
		}
	}
}
