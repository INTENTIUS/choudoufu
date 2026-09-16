// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
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
	// What the run REPORTS having pre-applied is the source the per-address
	// half reads; the verdict line only has to carry a count and the field.
	both := []string{"kubernetes_manifest.crd_a", "kubernetes_manifest.crd_b"}
	goodDetail := "50 objects; declared pre-apply, #1173: 2 address(es) declared at pre_apply in live/gauntlet/estates.json"
	cases := []struct {
		name     string
		estate   Estate
		res      *ProtocolResult
		wantGap  bool
		wantText string
	}{
		{
			name:   "no declaration, nothing checked, even with an empty detail and no pre_apply line",
			estate: none,
			res:    &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{}},
		},
		{
			name:   "declared, performed, and the line says how many and where: no gap",
			estate: declared,
			res:    &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: goodDetail}, PreApply: both},
		},
		{
			name:     "declared but this run reported performing none",
			estate:   declared,
			res:      &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: goodDetail}},
			wantGap:  true,
			wantText: "no `GAUNTLET pre_apply=` line",
		},
		{
			name:     "performed one of the two declared",
			estate:   declared,
			res:      &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: goodDetail}, PreApply: []string{"kubernetes_manifest.crd_a"}},
			wantGap:  true,
			wantText: "Declared but not performed: kubernetes_manifest.crd_b",
		},
		{
			name:     "performed a target nobody declared",
			estate:   declared,
			res:      &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: goodDetail}, PreApply: []string{"kubernetes_manifest.crd_a", "kubernetes_manifest.crd_b", "kubernetes_manifest.sneaky"}},
			wantGap:  true,
			wantText: "Performed but not declared: kubernetes_manifest.sneaky",
		},
		{
			name:     "performed correctly but the verdict line says nothing at all",
			estate:   declared,
			res:      &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: "50 objects from plain terraform"}, PreApply: both},
			wantGap:  true,
			wantText: "`pre_apply`",
		},
		{
			name:     "the verdict line names the field but not the count",
			estate:   declared,
			res:      &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: "pre_apply was performed, quantity unstated"}, PreApply: both},
			wantGap:  true,
			wantText: "how many addresses were pre-applied (2)",
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
				t.Fatalf("preApplyVerdictGap() = %q, want a gap", got)
			}
			if !c.wantGap && got != "" {
				t.Fatalf("preApplyVerdictGap() = %q, want no gap", got)
			}
			if c.wantGap && !strings.Contains(got, c.wantText) {
				t.Fatalf("preApplyVerdictGap() = %q, want it to name %q", got, c.wantText)
			}
		})
	}
}

// TestPreApplyVerdictLineStaysShort pins what the 2026-09-16 correction is
// about. The ruling first said the verdict line must name every address;
// on cert-manager's 47 that was a 3.3KB sentence, which satisfied the
// words and defeated their reason - a reader must SEE that two applies
// happened, and nobody reads 47 addresses in a verdict. So the guard must
// be satisfiable by a SHORT line, and must still be checking every
// address: the second half of this test keeps the same short line and
// quietly drops one performed address.
func TestPreApplyVerdictLineStaysShort(t *testing.T) {
	var addrs []string
	for i := 0; i < 47; i++ {
		addrs = append(addrs, fmt.Sprintf("kubernetes_manifest.object_with_a_realistically_long_name_%02d", i))
	}
	e := Estate{Name: "big", PreApply: addrs, PreApplyReason: "r"}
	short := "47 objects from plain terraform; declared pre-apply, #1173: 47 address(es) declared at pre_apply in live/gauntlet/estates.json"
	if len(short) > 200 {
		t.Fatalf("the sample verdict line is %d chars; it is meant to be the short one", len(short))
	}
	res := &ProtocolResult{Stages: map[string]string{StageColdDeploy: VerdictPass}, Detail: map[string]string{StageColdDeploy: short}, PreApply: addrs}
	if gap := preApplyVerdictGap(e, res); gap != "" {
		t.Fatalf("a short verdict line naming the count and the field was rejected: %s", gap)
	}
	res.PreApply = addrs[:46]
	gap := preApplyVerdictGap(e, res)
	if gap == "" {
		t.Fatal("dropping one of the 47 performed addresses was not caught - the per-address check went away with the long sentence, which is the wrong half to simplify")
	}
	if !strings.Contains(gap, addrs[46]) {
		t.Fatalf("the gap does not name the address that was not performed: %s", gap)
	}
}

// TestRunEstatesFailsColdDeployWhenThePreApplyIsNotNamed drives the whole
// merge path, one axis at a time: the same script and the same performed
// list, with only the verdict line's wording changing, and then the same
// wording with nothing performed.
func TestRunEstatesFailsColdDeployWhenThePreApplyIsNotNamed(t *testing.T) {
	const reported = "printf 'GAUNTLET pre_apply=kubernetes_manifest.crd_a,kubernetes_manifest.crd_b sides=estate,oracle\\n'\n"
	const named = "declared pre-apply, #1173: 2 address(es) declared at pre_apply in live/gauntlet/estates.json; then 7 objects"
	for _, c := range []struct {
		name       string
		preApplyLn string
		detail     string
		want       string
	}{
		{"performed, but the verdict line is silent about it", reported, "7 objects from plain terraform", VerdictFail},
		{"performed, and the line names the count and the field", reported, named, VerdictPass},
		{"says the right words but never performed it", "", named, VerdictFail},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			scriptPath := filepath.Join("live", "e2e", "x", "run.sh")
			if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
				t.Fatal(err)
			}
			script := "#!/usr/bin/env bash\n" +
				"printf 'GAUNTLET protocol=1\\n'\n" +
				c.preApplyLn +
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
			if got := r.Stages[StageColdDeploy]; got != c.want {
				t.Fatalf("cold_deploy = %q, want %q; runner said: %s", got, c.want, out.String())
			}
			if c.want == VerdictFail && !strings.Contains(r.LastRun.Detail[StageColdDeploy], "RUNNER:") {
				t.Fatalf("the recorded detail does not say the runner wrote it: %q", r.LastRun.Detail[StageColdDeploy])
			}
			if c.want == VerdictPass && r.LastRun.Detail[StageColdDeploy] != c.detail {
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
	// The addresses are reported on the protocol line, by the function that
	// applied them - that is the source the runner checks per address.
	if !strings.Contains(out, "GAUNTLET pre_apply=kubernetes_manifest.crd_a,kubernetes_manifest.crd_b sides=estate,oracle") {
		t.Errorf("gauntlet_pre_apply did not report what it performed on a protocol line:\n%s", out)
	}
	// The note carries a count and the field, and deliberately NOT the
	// addresses (#1173's sentence, corrected 2026-09-16).
	i := strings.Index(out, "declared pre-apply, #1173:")
	if i < 0 {
		t.Fatalf("gauntlet_pre_apply_note printed no note:\n%s", out)
	}
	note := out[i:]
	if !strings.Contains(note, "2 address(es) declared at pre_apply") {
		t.Errorf("the note does not say how many addresses or where they are declared:\n%s", note)
	}
	if strings.Contains(note, "kubernetes_manifest.crd_a") {
		t.Errorf("the note spells the addresses out again; that is the 3.3KB sentence the ruling was corrected away from:\n%s", note)
	}
	if !strings.Contains(note, preApplyFixture.PreApplyReason) {
		t.Errorf("the note does not carry the declared reason:\n%s", note)
	}
	if !strings.Contains(note, "estate,oracle") {
		t.Errorf("the note does not name the sides it ran on:\n%s", note)
	}
}

// TestPreApplyEndToEndThroughTheRealLibrary is the join each unit test
// above sees half of: a script that sources the REAL
// live/e2e/lib/gauntlet.sh, calls gauntlet_pre_apply against a real
// manifest and interpolates gauntlet_pre_apply_note into its own
// cold_deploy detail must come out of RunEstates as a pass. If the
// library's protocol line, the parser and the guard ever stop agreeing on
// a spelling, this is what notices - none of the three unit tests would.
func TestPreApplyEndToEndThroughTheRealLibrary(t *testing.T) {
	root := manifestWith(t, preApplyFixture)
	scriptPath := filepath.Join("live", "e2e", "fixture", "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\n" +
		"set -uo pipefail\n" +
		"ROOT=" + root + "\n" +
		"source " + filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh") + "\n" +
		"gauntlet_begin\n" +
		"pre_estate() { :; }\n" +
		"pre_oracle() { :; }\n" +
		"gauntlet_pre_apply fixture estate:pre_estate oracle:pre_oracle || exit 1\n" +
		"gauntlet_stage cold_deploy pass \"2 objects. $(gauntlet_pre_apply_note)\"\n" +
		"gauntlet_end\n"
	if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e := preApplyFixture
	e.Script = scriptPath
	m := &Manifest{Estates: []Estate{e}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	a := &Artifact{Schema: 1}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"fixture"}, Stdout: &out}, "c", "e"); err != nil {
		t.Fatal(err)
	}
	r, ok := a.Result("fixture")
	if !ok {
		t.Fatal("no result")
	}
	if got := r.Stages[StageColdDeploy]; got != VerdictPass {
		t.Fatalf("cold_deploy = %q, want pass; runner said: %s", got, out.String())
	}
	detail := r.LastRun.Detail[StageColdDeploy]
	if len(detail) > 700 {
		t.Errorf("the verdict line is %d chars; the whole point of the 2026-09-16 correction is that it stays readable:\n%s", len(detail), detail)
	}
	for _, addr := range preApplyFixture.PreApply {
		if strings.Contains(detail, addr) {
			t.Errorf("the verdict line spells out %s; it should carry a count and the field only:\n%s", addr, detail)
		}
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

// TestPreApplyEndToEndForTheRealCertManagerEntry runs the same join against
// the REAL manifest entry - all 47 declared addresses - with stub sides, so
// the estate's own declaration is proven to satisfy its own guard without
// standing up two kind clusters. It is the cheap half of the cold_deploy
// the estate performs for real; the expensive half is the run recorded in
// live/e2e/reference-k8s-cert-manager/README.md.
func TestPreApplyEndToEndForTheRealCertManagerEntry(t *testing.T) {
	const name = "reference-k8s-cert-manager"
	repo := repoRootForTest(t)
	real, err := LoadManifest(repo)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := real.ByName(name)
	if !ok {
		t.Skipf("%s is not in the manifest on this branch", name)
	}
	if len(e.PreApply) == 0 {
		t.Fatalf("%s declares no pre_apply; this test measures nothing", name)
	}

	root := manifestWith(t, e)
	scriptPath := filepath.Join("live", "e2e", name, "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\n" +
		"set -uo pipefail\n" +
		"ROOT=" + root + "\n" +
		"source " + filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh") + "\n" +
		"gauntlet_begin\n" +
		"pre_estate() { :; }\n" +
		"pre_oracle() { :; }\n" +
		"gauntlet_pre_apply " + name + " estate:pre_estate oracle:pre_oracle >/dev/null || exit 1\n" +
		// The real script does not silence the helper; this one does, and
		// then re-emits only the protocol line, so the test also proves the
		// line is what the runner reads rather than the chatter around it.
		"gauntlet_pre_apply " + name + " estate:pre_estate oracle:pre_oracle | grep '^GAUNTLET pre_apply='\n" +
		"gauntlet_stage cold_deploy pass \"50 objects. $(gauntlet_pre_apply_note)\"\n" +
		"gauntlet_end\n"
	if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e.Script = scriptPath
	m := &Manifest{Estates: []Estate{e}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	a := &Artifact{Schema: 1}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{name}, Stdout: &out}, "c", "e"); err != nil {
		t.Fatal(err)
	}
	r, _ := a.Result(name)
	if got := r.Stages[StageColdDeploy]; got != VerdictPass {
		t.Fatalf("cold_deploy = %q, want pass with %d declared addresses; runner said: %s", got, len(e.PreApply), out.String())
	}
	detail := r.LastRun.Detail[StageColdDeploy]
	if len(detail) > 1600 {
		t.Errorf("the verdict line for %d addresses is %d chars; before the 2026-09-16 correction it was 4.5KB and that is what this bounds:\n%s", len(e.PreApply), len(detail), detail)
	}
	t.Logf("%d declared addresses, verdict line %d chars", len(e.PreApply), len(detail))
}
