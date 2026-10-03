// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// reference-eks (#1113): the hand-written estate across AWS and the cluster
// its aws_eks_cluster creates. It runs nightly on floci-eks and is certified
// on real AWS by its own live-cert script, whose paid run is the
// maintainer's. Written under the maintainer's no-testing ruling for #1113
// and not run by the change that added it.

func TestReferenceEKSIsDeclaredOnFlociEKS(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m.ByName("reference-eks")
	if !ok {
		t.Fatal("reference-eks is not in the manifest")
	}
	if got := e.Substrate(); got != SubstrateFlociEKS {
		t.Errorf("reference-eks runs on %q, want %q", got, SubstrateFlociEKS)
	}
	for _, rel := range []string{e.ScriptPath(), LiveCertScript(e.Name), filepath.Join("live", "e2e", "reference-eks", "estate.sh")} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if strings.HasSuffix(rel, ".sh") && !strings.HasSuffix(rel, "estate.sh") && info.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable; live/live-cert/run.sh refuses a script it cannot execute", rel)
		}
	}
	// One estate, two scripts: both must source the same configuration, or
	// the estate the emulator measures and the one the maintainer certifies
	// drift apart.
	for _, rel := range []string{e.ScriptPath(), LiveCertScript(e.Name)} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "live/e2e/reference-eks/estate.sh") {
			t.Errorf("%s does not source live/e2e/reference-eks/estate.sh", rel)
		}
	}
}

// TestLiveCertWorkflowOffersEveryLiveCertScript: the dispatch choice in
// live-cert.yml is exactly the set of live/live-cert/<estate>.sh scripts, so
// an estate added for certification can be dispatched and an option cannot
// name a script that does not exist.
func TestLiveCertWorkflowOffersEveryLiveCertScript(t *testing.T) {
	root := testRoot(t)
	scripts, err := filepath.Glob(filepath.Join(root, "live", "live-cert", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, s := range scripts {
		name := strings.TrimSuffix(filepath.Base(s), ".sh")
		if name == "run" || strings.HasPrefix(name, "selftest-") {
			continue
		}
		want = append(want, name)
	}
	sort.Strings(want)

	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "live-cert.yml"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)estate:\s*\n\s*description:[^\n]*\n\s*type: choice\s*\n\s*options:\s*\n((?:\s*- [^\n]+\n)+)`).FindStringSubmatch(string(b))
	if block == nil {
		t.Fatal("could not find the estate choice's options in live-cert.yml")
	}
	var got []string
	for _, line := range strings.Split(block[1], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			got = append(got, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("live-cert.yml offers %v, but live/live-cert has scripts for %v", got, want)
	}
}
