// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"os/exec"
	"strings"
	"testing"
)

// TestImportsNothingInternal is GitHub issue #2244's acceptance guard:
// pkg/identity imports nothing from internal/configs, and - so that another
// module can import it and get no OpenTofu configuration types with it -
// nothing from this module's internal tree at all.
//
// It asks the go tool for the package's whole dependency graph, test
// dependencies included, rather than reading import lines, so an internal
// package reached through some other import is caught too.
func TestImportsNothingInternal(t *testing.T) {
	const self = "github.com/intentius/choudoufu/pkg/identity"
	const internalTree = "github.com/intentius/choudoufu/internal/"
	const configs = "github.com/intentius/choudoufu/internal/configs"

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("this guard needs the go tool on PATH to list %s's dependencies: %s", self, err)
	}
	out, err := exec.Command(goTool, "list", "-deps", "-test", self).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list -deps -test %s: %s\n%s", self, err, ee.Stderr)
		}
		t.Fatalf("go list -deps -test %s: %s", self, err)
	}

	deps := strings.Fields(string(out))
	sawSelf := false
	for _, dep := range deps {
		if dep == self {
			sawSelf = true
		}
		switch {
		case dep == configs || strings.HasPrefix(dep, configs+"/"):
			t.Errorf("%s depends on %s; the acceptance for #2244 is that it imports nothing from internal/configs", self, dep)
		case strings.HasPrefix(dep, internalTree):
			t.Errorf("%s depends on %s; it has to stay importable from another module without this module's internal packages", self, dep)
		}
	}
	// A listing that does not name the package itself listed something
	// else, and every check above would pass over it.
	if !sawSelf {
		t.Fatalf("go list -deps -test %s did not list %s itself among %d packages; this guard is not reading the graph it thinks it is", self, self, len(deps))
	}
}
