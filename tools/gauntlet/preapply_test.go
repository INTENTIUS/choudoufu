// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The cold-deploy pre-apply, #1173. Four things the ruling asks for, and a
// test apiece:
//
//   - the addresses are DECLARED in the manifest (TestPreApplyValidation);
//   - the stock oracle performs the IDENTICAL pre-apply
//     (TestGauntletPreApplyRefusesASingleSide,
//     TestGauntletPreApplyDrivesEverySideFromOneList);
//   - the verdict line NAMES it (TestPreApplyVerdictGap,
//     TestRunEstatesFailsColdDeployWhenThePreApplyIsNotNamed);
//   - an estate declaring none behaves exactly as today
//     (TestNoUndeclaredEstateIsAffected,
//     TestPreApplyIsOmittedFromEveryUndeclaredEntry,
//     TestRunEstatesIsUnchangedForAnEstateWithNoPreApply).

func TestPreApplyValidation(t *testing.T) {
	base := Estate{Name: "e", Source: "s", Lane: "reference", Set: SetGrowing}
	cases := []struct {
		name    string
		mutate  func(*Estate)
		wantErr string
	}{
		{"no declaration is fine", func(e *Estate) {}, ""},
		{"a declaration with a reason is fine", func(e *Estate) {
			e.PreApply = []string{"kubernetes_manifest.crd"}
			e.PreApplyReason = "the CRD must exist before the object of it can be planned"
		}, ""},
		{"a declaration with no reason is refused", func(e *Estate) {
			e.PreApply = []string{"kubernetes_manifest.crd"}
		}, "needs a pre_apply_reason"},
		{"a reason with no declaration is refused", func(e *Estate) {
			e.PreApplyReason = "explaining nothing"
		}, "pre_apply is empty"},
		{"an empty address is refused", func(e *Estate) {
			e.PreApply = []string{"kubernetes_manifest.crd", ""}
			e.PreApplyReason = "r"
		}, "empty address"},
		{"a duplicate address is refused", func(e *Estate) {
			e.PreApply = []string{"kubernetes_manifest.crd", "kubernetes_manifest.crd"}
			e.PreApplyReason = "r"
		}, "twice"},
		{"an address with a space is refused", func(e *Estate) {
			e.PreApply = []string{"kubernetes_manifest.crd other"}
			e.PreApplyReason = "r"
		}, "whitespace or a control character"},
		{"an address with a newline is refused", func(e *Estate) {
			e.PreApply = []string{"kubernetes_manifest.crd\nkubernetes_manifest.other"}
			e.PreApplyReason = "r"
		}, "whitespace or a control character"},
		{"an address that looks like a flag is refused", func(e *Estate) {
			e.PreApply = []string{"-auto-approve"}
			e.PreApplyReason = "r"
		}, "starts with a dash"},
		{"an indexed address is fine", func(e *Estate) {
			e.PreApply = []string{`kubernetes_manifest.crd["certificates"]`}
			e.PreApplyReason = "r"
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base
			c.mutate(&e)
			m := &Manifest{Estates: []Estate{e}}
			err := m.Validate()
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case c.wantErr != "" && err == nil:
				t.Fatalf("Validate() = nil, want an error naming %q", c.wantErr)
			case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
				t.Fatalf("Validate() = %v, want an error naming %q", err, c.wantErr)
			}
		})
	}
}

func TestPreApplyVerdictGap(t *testing.T) {
	declared := Estate{Name: "e", PreApply: []string{"kubernetes_manifest.crd_a", "kubernetes_manifest.crd_b"}, PreApplyReason: "r"}
	none := Estate{Name: "e"}
	cases := []struct {
		name     string
		estate   Estate
		res      *ProtocolResult
		wantGap  bool
		wantText string
	}{
		{
			name:   "no declaration, nothing checked, even with an empty detail",
			estate: none,
			res:    &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{}},
		},
		{
			name:     "declared and named: no gap",
			estate:   declared,
			res:      &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: "pre-apply of kubernetes_manifest.crd_a, kubernetes_manifest.crd_b then 47 objects"}},
			wantText: "",
		},
		{
			name:     "declared and NOT named: gap, naming both",
			estate:   declared,
			res:      &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: "47 objects from plain terraform"}},
			wantGap:  true,
			wantText: "kubernetes_manifest.crd_a, kubernetes_manifest.crd_b",
		},
		{
			name:     "declared and half named: gap, naming only the missing one",
			estate:   declared,
			res:      &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: "pre-applied kubernetes_manifest.crd_a"}},
			wantGap:  true,
			wantText: "kubernetes_manifest.crd_b",
		},
		{
			name:   "a failing cold_deploy is left alone",
			estate: declared,
			res:    &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictFail}, Detail: map[string]string{StageColdDeploy: "stock could not apply"}},
		},
		{
			name:   "a stage this run never reported is left alone",
			estate: declared,
			res:    &ProtocolResult{Stages: map[string]string{}, Detail: map[string]string{}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := preApplyVerdictGap(c.estate, c.res)
			if c.wantGap && got == "" {
				t.Fatalf("preApplyVerdictGap() = \"\", want a gap")
			}
			if !c.wantGap && got != "" {
				t.Fatalf("preApplyVerdictGap() = %q, want \"\"", got)
			}
			if c.wantGap && !strings.Contains(got, c.wantText) {
				t.Fatalf("preApplyVerdictGap() = %q, want it to name %q", got, c.wantText)
			}
			if c.wantGap && strings.Contains(got, "kubernetes_manifest.crd_a") && c.wantText == "kubernetes_manifest.crd_b" {
				t.Fatalf("preApplyVerdictGap() names an address the detail DID carry: %q", got)
			}
		})
	}
}

// TestRunEstatesFailsColdDeployWhenThePreApplyIsNotNamed drives the whole
// merge path: a script that passes cold_deploy and says nothing about the
// pre-apply it declared must come out of RunEstates as a fail. This is the
// guard shown red - the same script with the addresses in its detail line
// (below) comes out pass.
func TestRunEstatesFailsColdDeployWhenThePreApplyIsNotNamed(t *testing.T) {
	for _, c := range []struct {
		name       string
		detail     string
		wantVerdic string
	}{
		{"silent about it", "7 objects from plain terraform", VerdictFail},
		{"names both addresses", "pre-apply: kubernetes_manifest.crd_a, kubernetes_manifest.crd_b; then 7 objects", VerdictPass},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			scriptPath := filepath.Join("live", "e2e", "x", "run.sh")
			if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
				t.Fatal(err)
			}
			script := "#!/usr/bin/env bash\n" +
				"printf 'GAUNTLET protocol=1\\n'\n" +
				"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=5 detail=" + c.detail + "\\n'\n"
			if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			m := &Manifest{Estates: []Estate{{
				Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath,
				PreApply:       []string{"kubernetes_manifest.crd_a", "kubernetes_manifest.crd_b"},
				PreApplyReason: "the CRD must exist at plan time",
			}}}
			if err := m.Validate(); err != nil {
				t.Fatalf("the fixture manifest does not validate: %v", err)
			}
			a := &Artifact{Schema: 1}
			var out bytes.Buffer
			if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"x"}, Stdout: &out}, "c", "e"); err != nil {
				t.Fatal(err)
			}
			r, ok := a.Result("x")
			if !ok {
				t.Fatal("no result for x")
			}
			if got := r.Stages[StageColdDeploy]; got != c.wantVerdic {
				t.Fatalf("cold_deploy = %q, want %q (detail was %q); runner said: %s", got, c.wantVerdic, c.detail, out.String())
			}
			if c.wantVerdic == VerdictFail && !strings.Contains(r.LastRun.Detail[StageColdDeploy], "RUNNER:") {
				t.Fatalf("the recorded detail does not say the runner wrote it: %q", r.LastRun.Detail[StageColdDeploy])
			}
			if c.wantVerdic == VerdictPass && r.LastRun.Detail[StageColdDeploy] != c.detail {
				t.Fatalf("the script's own detail was replaced: %q", r.LastRun.Detail[StageColdDeploy])
			}
		})
	}
}

// TestRunEstatesIsUnchangedForAnEstateWithNoPreApply is the "prove the 27
// are unaffected" half, run through the real code rather than asserted: the
// identical script and the identical artifact, with and without the new
// field set on the manifest entry, produce identical rows when the field is
// empty - and the ONLY difference the field makes is the one above.
func TestRunEstatesIsUnchangedForAnEstateWithNoPreApply(t *testing.T) {
	run := func(t *testing.T) EstateResult {
		t.Helper()
		root := t.TempDir()
		scriptPath := filepath.Join("live", "e2e", "x", "run.sh")
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/usr/bin/env bash\n" +
			"printf 'GAUNTLET protocol=1\\n'\n" +
			"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=5 detail=7 objects, no pre-apply anywhere\\n'\n" +
			"printf 'GAUNTLET stage=migrate verdict=pass duration_s=2 detail=7 of 7 stamped\\n'\n"
		if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		m := &Manifest{Estates: []Estate{{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath}}}
		a := &Artifact{Schema: 1}
		var out bytes.Buffer
		if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"x"}, Stdout: &out}, "c", "e"); err != nil {
			t.Fatal(err)
		}
		r, _ := a.Result("x")
		return r
	}
	got := run(t)
	if got.Stages[StageColdDeploy] != VerdictPass {
		t.Fatalf("cold_deploy = %q, want pass - the pre-apply check fired on an estate that declares none", got.Stages[StageColdDeploy])
	}
	if got.LastRun.Detail[StageColdDeploy] != "7 objects, no pre-apply anywhere" {
		t.Fatalf("the script's own cold_deploy detail was rewritten: %q", got.LastRun.Detail[StageColdDeploy])
	}
	if got.Stages["migrate"] != VerdictPass {
		t.Fatalf("migrate = %q, want pass", got.Stages["migrate"])
	}
}

// TestNoUndeclaredEstateIsAffected reads the REAL manifest and asserts, per
// estate, that preApplyVerdictGap is inert for every one that declares no
// pre-apply - with a deliberately empty cold_deploy detail, the worst case
// the check could ever fire on.
func TestNoUndeclaredEstateIsAffected(t *testing.T) {
	root := repoRootForTest(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	declared, undeclared := 0, 0
	for _, e := range m.Estates {
		if len(e.PreApply) > 0 {
			declared++
			continue
		}
		undeclared++
		res := &ProtocolResult{
			Stages: map[string]string{StageColdDeploy: VerdictPass},
			Detail: map[string]string{StageColdDeploy: ""},
		}
		if gap := preApplyVerdictGap(e, res); gap != "" {
			t.Errorf("%s declares no pre_apply but the check fired: %s", e.Name, gap)
		}
	}
	if undeclared == 0 {
		t.Fatalf("the manifest has no estate without a pre_apply; this test measured nothing")
	}
	t.Logf("%d estate(s) declare a pre-apply, %d do not and are provably untouched by it", declared, undeclared)
}

// TestPreApplyIsOmittedFromEveryUndeclaredEntry: the new fields must not
// appear in the canonical manifest for an estate that does not use them, so
// adding them changes no committed byte for the estates that were already
// there. Canonical() is what render rewrites the file with, so this is the
// file's own bytes.
func TestPreApplyIsOmittedFromEveryUndeclaredEntry(t *testing.T) {
	root := repoRootForTest(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Estates {
		if len(e.PreApply) > 0 {
			continue
		}
		one := &Manifest{Estates: []Estate{e}}
		b, err := one.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "pre_apply") {
			t.Errorf("%s declares no pre-apply but its canonical entry carries a pre_apply key:\n%s", e.Name, b)
		}
	}
}

// ── the shell half ───────────────────────────────────────────────────────
//
// gauntlet_pre_apply is where "the stock oracle performs the identical
// pre-apply" is actually enforced, so it is tested by running it, not by
// reading it.

func bashLib(t *testing.T, root, body string) (string, int) {
	t.Helper()
	script := "set -uo pipefail\n" +
		"ROOT=" + root + "\n" +
		"source " + filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh") + "\n" +
		body
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running bash: %v", err)
	}
	return string(out), code
}

// manifestWith writes a temporary checkout holding just the library and a
// manifest with the given estate, so the shell tests below do not depend on
// what the real manifest happens to declare today.
func manifestWith(t *testing.T, e Estate) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live", "e2e", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "live", "gauntlet"), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(repoRootForTest(t), "live", "e2e", "lib", "gauntlet.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Estates: []Estate{e}}
	b, err := m.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "live", "gauntlet", "estates.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

var preApplyFixture = Estate{
	Name: "fixture", Source: "s", Lane: "reference", Set: SetGrowing,
	PreApply:       []string{"kubernetes_manifest.crd_a", "kubernetes_manifest.crd_b"},
	PreApplyReason: "a CRD and an object of it cannot be planned in one pass",
}

// TestGauntletPreApplyDrivesEverySideFromOneList: both sides get the same
// -target list, in the same order, read once from the manifest.
func TestGauntletPreApplyDrivesEverySideFromOneList(t *testing.T) {
	root := manifestWith(t, preApplyFixture)
	out, code := bashLib(t, root, `
side_a() { printf 'A got: %s\n' "$*"; }
side_b() { printf 'B got: %s\n' "$*"; }
gauntlet_pre_apply fixture estate:side_a oracle:side_b || exit 1
gauntlet_pre_apply_note
printf '\n'
`)
	if code != 0 {
		t.Fatalf("gauntlet_pre_apply exited %d:\n%s", code, out)
	}
	want := "-target=kubernetes_manifest.crd_a -target=kubernetes_manifest.crd_b"
	if !strings.Contains(out, "A got: "+want) {
		t.Errorf("side A did not get the declared targets:\n%s", out)
	}
	if !strings.Contains(out, "B got: "+want) {
		t.Errorf("side B did not get the identical list:\n%s", out)
	}
	for _, addr := range preApplyFixture.PreApply {
		if !strings.Contains(out, addr) {
			t.Errorf("the note does not name %s:\n%s", addr, out)
		}
	}
	if !strings.Contains(out, preApplyFixture.PreApplyReason) {
		t.Errorf("the note does not carry the declared reason:\n%s", out)
	}
	if !strings.Contains(out, "estate, oracle") {
		t.Errorf("the note does not name the sides it ran on:\n%s", out)
	}
}

// TestGauntletPreApplyRefusesASingleSide: clause 2 of the ruling, proven by
// the refusal. A crossing where only one side is pre-applied is not a
// crossing, so the helper will not perform one.
func TestGauntletPreApplyRefusesASingleSide(t *testing.T) {
	root := manifestWith(t, preApplyFixture)
	out, code := bashLib(t, root, `
side_a() { printf 'A ran\n'; }
gauntlet_pre_apply fixture estate:side_a
printf 'rc=%s\n' "$?"
`)
	if code != 0 {
		t.Fatalf("the harness itself failed (%d):\n%s", code, out)
	}
	if strings.Contains(out, "A ran") {
		t.Errorf("the single side was applied anyway:\n%s", out)
	}
	if !strings.Contains(out, "rc=1") {
		t.Errorf("a single-sided pre-apply did not fail:\n%s", out)
	}
	if !strings.Contains(out, "at least two") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}

// TestGauntletPreApplyNoteRefusesWithoutARun: a script cannot print the
// sentence that satisfies the runner's check without having performed the
// pre-apply the sentence describes.
func TestGauntletPreApplyNoteRefusesWithoutARun(t *testing.T) {
	root := manifestWith(t, preApplyFixture)
	out, code := bashLib(t, root, `
gauntlet_pre_apply_note
printf 'rc=%s\n' "$?"
`)
	if code != 0 {
		t.Fatalf("the harness itself failed (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "rc=1") || strings.Contains(out, "#1173):") {
		t.Errorf("gauntlet_pre_apply_note produced a note with no pre-apply behind it:\n%s", out)
	}
}

// TestGauntletPreApplyFailsLoudlyWhenASideFails: the second side failing
// must not be swallowed - the caller's fail() has to see it.
func TestGauntletPreApplyFailsLoudlyWhenASideFails(t *testing.T) {
	root := manifestWith(t, preApplyFixture)
	out, code := bashLib(t, root, `
side_a() { printf 'A ran\n'; }
side_b() { printf 'B ran and failed\n'; return 7; }
gauntlet_pre_apply fixture estate:side_a oracle:side_b
printf 'rc=%s\n' "$?"
`)
	if code != 0 {
		t.Fatalf("the harness itself failed (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "rc=1") {
		t.Errorf("a failing side did not fail the pre-apply:\n%s", out)
	}
	if !strings.Contains(out, "failed on side oracle") {
		t.Errorf("the failure does not name the side:\n%s", out)
	}
}

// TestGauntletPreApplyRefusesAnUndeclaredEstate: targeting from the script
// rather than the manifest is the shape the ruling rejects, so the helper
// has nothing to offer a script that did not declare.
func TestGauntletPreApplyRefusesAnUndeclaredEstate(t *testing.T) {
	root := manifestWith(t, Estate{Name: "plain", Source: "s", Lane: "reference", Set: SetGrowing})
	out, code := bashLib(t, root, `
side_a() { printf 'A ran\n'; }
side_b() { printf 'B ran\n'; }
gauntlet_pre_apply plain estate:side_a oracle:side_b
printf 'rc=%s\n' "$?"
`)
	if code != 0 {
		t.Fatalf("the harness itself failed (%d):\n%s", code, out)
	}
	if strings.Contains(out, "A ran") || !strings.Contains(out, "rc=1") {
		t.Errorf("an undeclared estate got a pre-apply:\n%s", out)
	}
}

// TestGauntletWaitUntilIsBoundedAndLoud: #1173's readiness clause. A wait
// that never succeeds must end, and must say so.
func TestGauntletWaitUntilIsBoundedAndLoud(t *testing.T) {
	root := manifestWith(t, preApplyFixture)
	out, code := bashLib(t, root, `
never() { return 1; }
gauntlet_wait_until 3 "a webhook that never serves" -- never
printf 'rc=%s\n' "$?"
always() { return 0; }
gauntlet_wait_until 3 "something already ready" -- always
printf 'rc2=%s\n' "$?"
`)
	if code != 0 {
		t.Fatalf("the harness itself failed (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "rc=1") {
		t.Errorf("a wait that never succeeds did not fail:\n%s", out)
	}
	if !strings.Contains(out, "TIMEOUT") || !strings.Contains(out, "a webhook that never serves") {
		t.Errorf("the timeout is not loud or does not name what it waited for:\n%s", out)
	}
	if !strings.Contains(out, "rc2=0") {
		t.Errorf("a condition that is already true did not succeed:\n%s", out)
	}
}
