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

// TestReferenceEKSRunsEveryActiveStageOnBothTargets: the emulator crossing
// and the live-cert cycle both run every active stage, through one set of
// stage bodies (live/e2e/reference-eks/stages.sh), so the two cannot drift
// apart stage by stage the way two hand-kept copies would. Written under
// the maintainer's no-testing ruling and not run by the change that added
// it.
//
// What it holds, read statically:
//   - every active stage is begun somewhere in the crossing (run.sh plus
//     stages.sh), so no stage is left to the runner's silent not_run;
//   - run.sh reports no stage not_run on purpose any more;
//   - every reference_eks_stage_<id> body stages.sh defines is called by
//     BOTH scripts, and names a registered stage;
//   - the live-cert cycle carries #1524's records check: a record_store
//     "kubernetes" with control_plane "eks", read with live-cluster -json,
//     and encryption_at_rest required to have been answered.
func TestReferenceEKSRunsEveryActiveStageOnBothTargets(t *testing.T) {
	root := testRoot(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	runSh := read(filepath.Join("live", "e2e", "reference-eks", "run.sh"))
	stagesSh := read(filepath.Join("live", "e2e", "reference-eks", "stages.sh"))
	certSh := read(LiveCertScript("reference-eks"))
	estateSh := read(filepath.Join("live", "e2e", "reference-eks", "estate.sh"))

	for _, rel := range []string{"run.sh", "live-cert"} {
		body := runSh
		if rel == "live-cert" {
			body = certSh
		}
		if !strings.Contains(body, "live/e2e/reference-eks/stages.sh") {
			t.Errorf("%s does not source live/e2e/reference-eks/stages.sh", rel)
		}
	}

	begun := regexp.MustCompile(`(?m)^\s*gauntlet_begin_stage ([a-z0-9_]+)\s*$`)
	seen := map[string]bool{}
	for _, m := range begun.FindAllStringSubmatch(runSh+"\n"+stagesSh, -1) {
		seen[m[1]] = true
	}
	for _, s := range ActiveStages() {
		if !seen[s.ID] {
			t.Errorf("no gauntlet_begin_stage %s in run.sh or stages.sh: the crossing does not run active stage %s", s.ID, s.ID)
		}
	}
	certSeen := map[string]bool{}
	for _, m := range begun.FindAllStringSubmatch(certSh+"\n"+stagesSh, -1) {
		certSeen[m[1]] = true
	}
	for _, s := range ActiveStages() {
		if !certSeen[s.ID] {
			t.Errorf("no gauntlet_begin_stage %s in the live-cert script or stages.sh: the live-cert cycle does not run active stage %s", s.ID, s.ID)
		}
	}

	if regexp.MustCompile(`(?m)^[^#\n]*gauntlet_stage\s+"?\$?[a-z0-9_{}]+"?\s+not_run`).MatchString(runSh) {
		t.Errorf("run.sh still reports a stage not_run by hand; every active stage is built for reference-eks")
	}

	def := regexp.MustCompile(`(?m)^(reference_eks_stage_([a-z0-9_]+))\(\) \{`)
	defs := def.FindAllStringSubmatch(stagesSh, -1)
	if len(defs) == 0 {
		t.Fatal("stages.sh defines no reference_eks_stage_<id> body - the pattern or the file moved")
	}
	for _, d := range defs {
		fn, id := d[1], d[2]
		if _, ok := StageByID(id); !ok {
			t.Errorf("stages.sh defines %s, but %s is not a registered stage", fn, id)
		}
		call := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(fn) + `\s*$`)
		if !call.MatchString(runSh) {
			t.Errorf("run.sh never calls %s", fn)
		}
		if !call.MatchString(certSh) {
			t.Errorf("the live-cert script never calls %s", fn)
		}
	}

	for _, want := range []string{
		"reference_eks_kubernetes_store",
		"live-cluster -json",
		"encryption_at_rest",
		"read_isolation",
		"namespace_access",
	} {
		if !strings.Contains(certSh, want) {
			t.Errorf("the live-cert script does not carry #1524's records check: %q is missing", want)
		}
	}
	if !strings.Contains(estateSh, `control_plane "eks"`) {
		t.Error(`estate.sh's record_store "kubernetes" block carries no control_plane "eks" block (#1524)`)
	}
}
