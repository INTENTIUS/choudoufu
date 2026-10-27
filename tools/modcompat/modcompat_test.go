// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package modcompat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/dataread"
)

const testCommit = "3ffbd46fb1c7733e1b34d8666893280454e27436"

// testRoot writes a manifest with one terraform-aws-modules source and one
// source of another origin, and returns the root.
func testRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"sources": [
  {"glob": ".corpus/vpc/examples/*", "origin": "terraform-aws-modules",
   "fetch": {"dir": ".corpus/vpc", "repo": "https://github.com/terraform-aws-modules/terraform-aws-vpc.git", "tag": "v6.6.1", "commit": "` + testCommit + `"}},
  {"glob": ".corpus/other/*", "origin": "published deployment"}
]}`
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(ManifestRel)), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func testSweep() *Sweep {
	return &Sweep{
		ProbeVersion:  probeVersion,
		Commit:        "543c68ba3f212445b5289a0d540f23c883d8a9cc",
		Schemas:       true,
		OnboardedForm: true,
		SecondPass:    true,
		PinVersion:    "6.59.0",
		Sources: []SweepSource{
			{Glob: ".corpus/vpc/examples/*", Origin: Origin, Entries: 3, Pin: testCommit, Checkout: testCommit},
			{Glob: ".corpus/other/*", Origin: "published deployment", Entries: 1},
		},
		Layers: map[string]string{
			dataread.SummaryEligibleRead: "dataread",
			"unadmitted-type":            "lint",
		},
		Entries: []SweepEntry{
			{
				Name: ".corpus/vpc/examples/simple", Origin: Origin,
				Providers:  []SweepProvider{{Provider: "hashicorp/aws", Present: true}},
				Onboarding: &SweepOnboarding{Status: "edited"},
				Onboarded:  &SweepForm{Blocked: false, Reads: 37, Refusals: map[string]int{dataread.SummaryEligibleRead: 37}},
			},
			{
				Name: ".corpus/vpc/examples/manage-default-vpc", Origin: Origin,
				Providers:  []SweepProvider{{Provider: "hashicorp/aws", Present: true}, {Provider: "hashicorp/null", Present: false}},
				Onboarding: &SweepOnboarding{Status: "edited"},
				Onboarded:  &SweepForm{Blocked: true, Reads: 2, Refusals: map[string]int{"unadmitted-type": 1, dataread.SummaryEligibleRead: 2}},
			},
			{
				Name: ".corpus/vpc/examples/odd", Origin: Origin,
				Onboarding: &SweepOnboarding{Status: "unmeasurable", Reason: "two backend blocks"},
			},
			{Name: ".corpus/other/x", Origin: "published deployment"},
		},
	}
}

func TestStaticTierRows(t *testing.T) {
	m, rows, err := StaticTier(testRoot(t), testSweep(), StaticOptions{Date: "2026-10-27"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sources) != 1 || m.Sources[0].Module != "terraform-aws-modules/vpc/aws" || m.Sources[0].Examples != 3 {
		t.Fatalf("sources = %+v", m.Sources)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (the other origin stays out)", len(rows))
	}
	byExample := map[string]StaticRow{}
	for _, r := range rows {
		byExample[r.Example] = r
		if r.Tier != TierStatic || r.Version != "v6.6.1" || r.Date != "2026-10-27" || r.AWSProvider != "6.59.0" {
			t.Errorf("%s: provenance %+v", r.Example, r)
		}
	}

	clear := byExample["examples/simple"]
	if clear.Verdict != VerdictClear || clear.Reads != 37 || len(clear.Refusals) != 0 {
		t.Errorf("a read-only row must be clear with its read count and no refusal: %+v", clear)
	}

	refused := byExample["examples/manage-default-vpc"]
	if refused.Verdict != VerdictRefused || len(refused.Refusals) != 1 || refused.Refusals[0].ID != "unadmitted-type" {
		t.Errorf("refused row: %+v", refused)
	}
	if !strings.HasPrefix(refused.Refusals[0].DocsURL, DocsBase+"live/LIMITATIONS.md#") {
		t.Errorf("refusal is not linked to LIMITATIONS.md: %q", refused.Refusals[0].DocsURL)
	}
	if strings.Join(refused.MissingProviders, ",") != "hashicorp/null" {
		t.Errorf("missing providers = %v", refused.MissingProviders)
	}

	odd := byExample["examples/odd"]
	if odd.Verdict != VerdictUnmeasured || odd.Reason != "two backend blocks" {
		t.Errorf("unmeasured row: %+v", odd)
	}
}

func TestStaticTierRefusesASweepThatIsNotTheStaticTier(t *testing.T) {
	cases := map[string]func(*Sweep){
		"no schemas":     func(s *Sweep) { s.Schemas = false },
		"published form": func(s *Sweep) { s.OnboardedForm = false },
		"no second pass": func(s *Sweep) { s.SecondPass = false },
		"dirty tree":     func(s *Sweep) { s.Commit += "+dirty" },
		"other pin":      func(s *Sweep) { s.Sources[0].Checkout = "0000000000000000000000000000000000000000" },
		"partial corpus": func(s *Sweep) {
			s.CorpusProblems = []string{"no configurations: .corpus/vpc/examples/* (terraform-aws-modules)"}
		},
		"filtered entries":  func(s *Sweep) { s.Entries = s.Entries[1:] },
		"verdict disagrees": func(s *Sweep) { s.Entries[0].Onboarded.Blocked = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := testSweep()
			mutate(s)
			if _, _, err := StaticTier(testRoot(t), s, StaticOptions{Date: "2026-10-27"}); err == nil {
				t.Fatal("accepted a sweep that does not stand for the static tier")
			}
		})
	}
}

func TestStaticTierNeedsACommit(t *testing.T) {
	s := testSweep()
	s.Commit = ""
	if _, _, err := StaticTier(testRoot(t), s, StaticOptions{Date: "2026-10-27"}); err == nil {
		t.Fatal("accepted a sweep that names no commit")
	}
	m, _, err := StaticTier(testRoot(t), s, StaticOptions{Date: "2026-10-27", Commit: "abc"})
	if err != nil || m.Commit != "abc" {
		t.Fatalf("commit from options: %v %+v", err, m)
	}
}

func TestModuleFromRepo(t *testing.T) {
	for repo, want := range map[string]string{
		"https://github.com/terraform-aws-modules/terraform-aws-vpc.git":            "terraform-aws-modules/vpc/aws",
		"https://github.com/terraform-aws-modules/terraform-aws-dynamodb-table.git": "terraform-aws-modules/dynamodb-table/aws",
		"https://github.com/terraform-aws-modules/terraform-aws-eks":                "terraform-aws-modules/eks/aws",
	} {
		if got, ok := ModuleFromRepo(repo); !ok || got != want {
			t.Errorf("ModuleFromRepo(%q) = %q, %v; want %q", repo, got, ok, want)
		}
	}
	if _, ok := ModuleFromRepo("https://github.com/rust-lang/simpleinfra.git"); ok {
		t.Error("a repository outside terraform-aws-modules read as a module")
	}
}

func TestAnchor(t *testing.T) {
	for in, want := range map[string]string{
		"unadmitted-type":                          "unadmitted-type",
		"Unmarked apply of a marker-only resource": "unmarked-apply-of-a-marker-only-resource",
		`Invalid "path" attribute`:                 "invalid-path-attribute",
	} {
		if got := Anchor(in); got != want {
			t.Errorf("Anchor(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGauntletRowsKeepOnlyCurrentRuns holds the admission rule: a row
// measured against any superseded pin, or with a changed script, is left
// out, and a current one is published with its stages.
func TestGauntletRowsKeepOnlyCurrentRuns(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	estate := func(name, emulator, aws string) string {
		return `{"name": "` + name + `", "lane": "terraform-popular", "clear": true, "pin": "v6.6.1",
  "url": "https://github.com/terraform-aws-modules/terraform-aws-vpc.git",
  "source": "terraform-aws-modules/terraform-aws-vpc examples/complete (tag v6.6.1)",
  "last_run": {"commit": "c", "date": "2026-10-30T00:00:00Z", "emulator": "` + emulator + `",
    "aws_provider_version": "` + aws + `", "oracle": {"terraform": "1.16.1", "tofu": "1.13.0"}, "upstream_version": "1.13.0"}}`
	}
	art := `{"emulator": "img@new", "oracle": {"terraform": "1.16.1", "tofu": "1.13.0"}, "providers": {"aws": "6.63.0"}, "upstream_version": "1.13.0",
"estates": [` + estate("fresh", "img@new", "6.63.0") + `, ` + estate("old-image", "img@old", "6.63.0") + `, ` + estate("old-aws", "img@new", "6.59.0") + `, ` + estate("old-script", "img@new", "6.63.0") + `]}`
	brd := `{"estates": [
  {"name": "fresh", "stage_rows": [{"id": "cold_deploy", "title": "Cold deploy", "verdict": "pass"}]},
  {"name": "old-image"}, {"name": "old-aws"}, {"name": "old-script", "script_stale": "changed"}]}`
	for rel, body := range map[string]string{ArtifactRel: art, BoardRel: brd} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := GauntletRows(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Estate != "fresh" {
		t.Fatalf("rows = %+v; want only the current run", rows)
	}
	r := rows[0]
	if r.Tier != TierGauntlet || r.Module != "terraform-aws-modules/vpc/aws" || r.Example != "examples/complete" || len(r.Stages) != 1 {
		t.Errorf("row = %+v", r)
	}
}
