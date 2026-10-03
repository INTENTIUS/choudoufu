// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package setdigest

import (
	"os/exec"
	"strings"
	"testing"
)

// deps lists pkg's whole import graph outside the standard library, test
// files excluded.
func deps(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
	}
	return strings.Fields(string(out))
}

// TestImportsStdlibOnly: this package joins internal/live/setplan's graph,
// which internal/command/views imports, so it may import nothing of this
// module's (see the package doc).
func TestImportsStdlibOnly(t *testing.T) {
	for _, d := range deps(t, "github.com/intentius/choudoufu/internal/live/setdigest") {
		if d != "github.com/intentius/choudoufu/internal/live/setdigest" {
			t.Errorf("setdigest imports %s; it must import the standard library only", d)
		}
	}
}

// TestSetplanStaysALeaf is the guard #1816's first gate asked for:
// internal/live/setplan is imported by internal/command/views, so if it
// reaches waves, check or discovery, a test in any package those import
// (dataread, the live/ tree) can no longer import views without a cycle.
func TestSetplanStaysALeaf(t *testing.T) {
	forbidden := []string{
		"github.com/intentius/choudoufu/internal/live/waves",
		"github.com/intentius/choudoufu/internal/live/check",
		"github.com/intentius/choudoufu/internal/live/discovery",
		"github.com/intentius/choudoufu/internal/live/dataread",
		"github.com/intentius/choudoufu/live",
	}
	have := map[string]bool{}
	for _, d := range deps(t, "github.com/intentius/choudoufu/internal/live/setplan") {
		have[d] = true
	}
	for _, f := range forbidden {
		if have[f] {
			t.Errorf("internal/live/setplan imports %s (directly or not); keep it a leaf so internal/command/views does not pull the live stages into every test that imports it", f)
		}
	}
}
