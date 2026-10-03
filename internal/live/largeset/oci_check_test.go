// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package largeset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/check"
)

// The 2026-10-01 thread on #1749 asked whether the live path handles a module
// consumed through a pinned oci:// source at all; nothing under internal/live
// had ever been run on one. The floci run (live/large-set/oci.sh) answers it
// end to end. These tests pin the part that needs no registry: once init has
// installed the package, lint and identity resolution see the module the
// same way they see the relative-path one, and before init they say the
// module was not read rather than reporting a clean, smaller estate.
//
// installOCIModules writes what `choudoufu init` writes for an oci:// module
// call - the package under .terraform/modules/shared and the manifest
// recording the source verbatim - with the manifest shape copied from a real
// init against the local registry (2026-10-02).
func installOCIModules(t *testing.T, fixture string, m Manifest) {
	t.Helper()
	for _, e := range m.Estates {
		mods := filepath.Join(fixture, e.Dir, ".terraform", "modules")
		if err := os.MkdirAll(filepath.Join(mods, "shared"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range ModulePackage(e.ModuleVersion) {
			if err := os.WriteFile(filepath.Join(mods, "shared", name), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		manifest := map[string][]map[string]string{"Modules": {
			{"Key": "", "Source": "", "Dir": "."},
			{"Key": "shared", "Source": e.ModuleSource, "Dir": ".terraform/modules/shared"},
		}}
		b, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mods, "modules.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// checkSummary renders what lint and identity resolution decided for one
// root, in a form two roots can be compared by: every resolved identity with
// its class and rendered value, every refusal ID with its site count, every
// cross-estate reference, and every module call that went unread.
func checkSummary(t *testing.T, dir string) (string, check.Report) {
	t.Helper()
	r := check.Dir(t.Context(), dir, check.Context{})
	if !r.Readable() {
		t.Fatalf("%s did not load: %v", dir, r.Load.Diags)
	}
	var lines []string
	for _, id := range r.Identities {
		attrs := make([]string, 0, len(id.IdentityValues))
		for k, v := range id.IdentityValues {
			attrs = append(attrs, k+"="+v)
		}
		sort.Strings(attrs)
		lines = append(lines, fmt.Sprintf("identity %s %s %q %s", id.Addr, id.Class, id.ImportID, strings.Join(attrs, ",")))
	}
	for _, f := range r.Findings {
		lines = append(lines, fmt.Sprintf("refusal %s x%d", f.ID, len(f.Sites)))
	}
	for _, ref := range r.References {
		lines = append(lines, fmt.Sprintf("reference %s -> %s %s", ref.From, ref.Estate, ref.Address))
	}
	for _, u := range r.Load.UnresolvedModules {
		lines = append(lines, fmt.Sprintf("unread module %s %s", u.Path, u.Source))
	}
	lines = append(lines, fmt.Sprintf("instances %d", r.Instances))
	return strings.Join(lines, "\n"), r
}

// TestOCIModuleChecksLikeALocalOne: after init, every root of the OCI
// variant gets the same identities, refusals and references as the same root
// of the local variant.
func TestOCIModuleChecksLikeALocalOne(t *testing.T) {
	local, oci := filepath.Join(t.TempDir(), "local"), filepath.Join(t.TempDir(), "oci")
	lm, err := Write(local, Options{Estates: 5, ModuleVersion: VersionA})
	if err != nil {
		t.Fatal(err)
	}
	om, err := Write(oci, Options{Estates: 5, ModuleVersion: VersionA, Source: SourceOCI, Registry: "localhost:4890"})
	if err != nil {
		t.Fatal(err)
	}
	installOCIModules(t, oci, om)

	for i, e := range om.Estates {
		want, _ := checkSummary(t, filepath.Join(local, lm.Estates[i].Dir))
		got, rep := checkSummary(t, filepath.Join(oci, e.Dir))
		if got != want {
			t.Errorf("%s: the oci:// root checks differently from the local one.\noci:\n%s\nlocal:\n%s", e.Name, got, want)
		}
		if rep.Instances < 3 {
			t.Errorf("%s: only %d instances resolved; the module's three resources are not being read", e.Name, rep.Instances)
		}
	}
}

// TestOCIPinBumpReachesOnlyThePinnedRoots: with e02 and e04 pinned at B, only
// those two roots resolve the queue B adds.
func TestOCIPinBumpReachesOnlyThePinnedRoots(t *testing.T) {
	oci := filepath.Join(t.TempDir(), "oci")
	om, err := Write(oci, Options{Estates: 5, ModuleVersion: VersionA, Source: SourceOCI, Registry: "localhost:4890", PinB: []string{"e02", "e04"}})
	if err != nil {
		t.Fatal(err)
	}
	installOCIModules(t, oci, om)
	for _, e := range om.Estates {
		got, _ := checkSummary(t, filepath.Join(oci, e.Dir))
		has := strings.Contains(got, "module.shared.aws_sqs_queue.events")
		if want := e.Name == "e02" || e.Name == "e04"; has != want {
			t.Errorf("%s: resolves the B-only events queue = %v, want %v\n%s", e.Name, has, want, got)
		}
	}
}

// TestUninstalledOCIModuleIsReportedUnread: before init, the module's
// resources are absent, and the report must say so rather than present the
// smaller estate as the whole of it.
func TestUninstalledOCIModuleIsReportedUnread(t *testing.T) {
	oci := filepath.Join(t.TempDir(), "oci")
	om, err := Write(oci, Options{Estates: 5, ModuleVersion: VersionA, Source: SourceOCI, Registry: "localhost:4890"})
	if err != nil {
		t.Fatal(err)
	}
	e := om.Estates[4]
	_, rep := checkSummary(t, filepath.Join(oci, e.Dir))
	if len(rep.Load.UnresolvedModules) != 1 || rep.Load.UnresolvedModules[0].Source != e.ModuleSource {
		t.Fatalf("an uninstalled oci:// module was not reported unread: %+v", rep.Load.UnresolvedModules)
	}
	if rep.Instances != 0 {
		t.Errorf("%d instances resolved from a root whose only resources are inside the unread module", rep.Instances)
	}
}
