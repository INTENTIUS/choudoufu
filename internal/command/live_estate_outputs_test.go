// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
)

// estateOutputsPlan runs plain "choudoufu plan" over the live-estate-outputs
// fixture, with the builtin terraform provider wired the way production
// wires it (Meta.internalProviders), which is the path under test.
func estateOutputsPlan(t *testing.T, td string) (int, string, string) {
	t.Helper()
	view, done := testView(t)
	meta := Meta{
		WorkingDir:       workdir.NewDir("."),
		View:             view,
		testingOverrides: &testingOverrides{Providers: map[addrs.Provider]providers.Factory{}},
	}
	meta.testingOverrides.Providers[addrs.NewBuiltInProvider("terraform")] = meta.internalProviders()["terraform"]
	c := &PlanCommand{Meta: meta}
	code := c.Run([]string{"-no-color"})
	out := done(t)
	return code, out.Stdout(), out.Stderr()
}

func recordProducerOutput(t *testing.T, td, estate, name string, val cty.Value) {
	t.Helper()
	raw, err := staterecord.NewLocalStore(filepath.Join(td, ".tofu-records"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.NewRootOutputStore(raw, estate).Put(t.Context(), name, val, ""); err != nil {
		t.Fatal(err)
	}
}

// TestEstateOutputsReadCrossesEstates is the green path: estate "network"
// recorded "namespace" at its last apply, and estate "app"'s plan reads it
// and says how old it is.
func TestEstateOutputsReadCrossesEstates(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-estate-outputs"), td)
	t.Chdir(td)
	recordProducerOutput(t, td, "network", "namespace", cty.StringVal("cluster-services"))

	code, stdout, stderr := estateOutputsPlan(t, td)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `+ namespace = "cluster-services"`) {
		t.Errorf("the plan does not show the value read from estate network:\n%s", stdout)
	}
	all := stdout + stderr
	if !strings.Contains(all, projection.SummaryEstateOutputsAsOf) || !strings.Contains(all, `estate "network"`) {
		t.Errorf("the plan does not say the value is as of network's last apply:\n%s", all)
	}
}

// TestEstateOutputsNotRecordedRefusesByName: a declared output the other
// estate never recorded is an error naming that estate and output, not an
// unsupported-attribute error at the reference.
func TestEstateOutputsNotRecordedRefusesByName(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-estate-outputs"), td)
	t.Chdir(td)

	code, stdout, stderr := estateOutputsPlan(t, td)
	if code == 0 {
		t.Fatalf("a read of an output nothing recorded planned cleanly:\n%s", stdout)
	}
	if !strings.Contains(stderr, projection.SummaryEstateOutputNotRecorded) || !strings.Contains(stderr, `Estate "network" has no recorded value for its output "namespace"`) {
		t.Errorf("the refusal does not name estate network's output:\n%s", stderr)
	}
}

// TestEstateOutputsDeniedRefusesNamingTheOtherEstate is the BREAK arm's
// local-store form: the dependency is declared and this run may not read
// the other estate's outputs. The refusal names the other estate. The same
// classification over S3's AccessDenied is pinned without a filesystem by
// internal/live/projection's TestReadEstateOutputsDeniedNamesTheOtherEstate.
func TestEstateOutputsDeniedRefusesNamingTheOtherEstate(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory; TestReadEstateOutputsDeniedNamesTheOtherEstate covers the classification")
	}
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-estate-outputs"), td)
	t.Chdir(td)
	recordProducerOutput(t, td, "network", "namespace", cty.StringVal("cluster-services"))
	// The record file, not its directory: the store's own contract check
	// walks the directory tree when it opens, and that walk is not what
	// this test is about.
	locked := filepath.Join(td, ".tofu-records", filepath.FromSlash(projection.RootOutputKey("network", "namespace")))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	code, stdout, stderr := estateOutputsPlan(t, td)
	if code == 0 {
		t.Fatalf("a denied read planned cleanly:\n%s", stdout)
	}
	if !strings.Contains(stderr, projection.SummaryEstateOutputsDenied) || !strings.Contains(stderr, `estate "network"`) {
		t.Errorf("the refusal does not name estate network as the one it may not read:\n%s", stderr)
	}
}
