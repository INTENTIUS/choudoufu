// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/docsref"
	"github.com/intentius/choudoufu/tools/modcompat"
)

// GitHub issue #2292: live/module-compat.json is the module compatibility
// table in two tiers, and the site renders it as two tables
// (site/src/components/ModuleCompat.astro on aws/compatibility). These tests
// hold the file to the tree so the page cannot drift from its generators.
//
// The gauntlet tier is a pure function of committed files, so it is
// re-derived here and compared. The static tier comes from a refusal-probe
// sweep this test cannot re-run, so it is held to its recorded inputs: the
// manifest's terraform-aws-modules sources at the pins it was measured at,
// and rows whose verdicts agree with their own refusals.
//
// The file is an external test package because tools/modcompat reaches
// internal/live/lint, which imports this directory's package; an in-package
// test would close that cycle.
//
// Proving it red: change a tag in live/corpus-manifest.json, hand-edit a
// static verdict, or add a gauntlet row by hand; each fails a check below.

func readModuleCompat(t *testing.T, root string) (*modcompat.File, map[string]json.RawMessage) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(modcompat.Rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", modcompat.Rel, err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("decoding %s: %v", modcompat.Rel, err)
	}
	f, err := modcompat.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	want, err := modcompat.Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(raw) {
		t.Errorf("%s is not in the generator's own encoding; it was edited by hand. Regenerate it with %s.", modcompat.Rel, modcompat.GauntletCommand)
	}
	return f, keys
}

func TestModuleCompatTiersShareNoFigure(t *testing.T) {
	root := moduleCompatRoot(t)
	_, keys := readModuleCompat(t, root)
	want := []string{"_comment", "gauntlet", "schema", "static", "static_measurement"}
	var got []string
	for k := range keys {
		got = append(got, k)
	}
	sortStrings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s has top-level keys %v, want exactly %v: the two tiers never share a total (#2292)", modcompat.Rel, got, want)
	}
}

func TestModuleCompatGauntletTierIsCurrent(t *testing.T) {
	root := moduleCompatRoot(t)
	f, _ := readModuleCompat(t, root)
	want, err := modcompat.GauntletRows(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Gauntlet, want) {
		t.Errorf("%s's gauntlet tier is stale against %s and %s; run %s", modcompat.Rel, modcompat.ArtifactRel, modcompat.BoardRel, modcompat.GauntletCommand)
	}
}

func TestModuleCompatStaticTierMatchesTheManifest(t *testing.T) {
	root := moduleCompatRoot(t)
	f, _ := readModuleCompat(t, root)
	m := f.StaticMeasurement
	if m == nil || len(f.Static) == 0 {
		t.Fatalf("%s has no static tier; run %s", modcompat.Rel, modcompat.StaticCommand)
	}
	if !m.Schemas || !m.SecondPass || m.Form != "onboarded" || m.Commit == "" || m.Date == "" || m.AWSProvider == "" {
		t.Errorf("static measurement %+v is not a schema-backed onboarded sweep with its provenance", *m)
	}

	sources, err := modcompat.ManifestSources(root)
	if err != nil {
		t.Fatal(err)
	}
	type pin struct{ Module, Repo, Version, Commit, Glob string }
	var have, need []pin
	for _, s := range m.Sources {
		have = append(have, pin{s.Module, s.Repo, s.Version, s.Commit, s.Glob})
	}
	for _, s := range sources {
		need = append(need, pin{s.Module, s.Repo, s.Version, s.Commit, s.Glob})
	}
	if !reflect.DeepEqual(have, need) {
		t.Errorf("the static tier was measured over %v, and %s now lists %v; re-run %s", have, modcompat.ManifestRel, need, modcompat.StaticCommand)
	}

	perSource := map[[2]string]int{}
	for _, r := range f.Static {
		perSource[[2]string{r.Module, r.Version}]++
		if r.Tier != modcompat.TierStatic || r.Date != m.Date || r.Commit != m.Commit || r.AWSProvider != m.AWSProvider {
			t.Errorf("%s %s: provenance differs from the static measurement: %+v", r.Module, r.Example, r)
		}
		switch r.Verdict {
		case modcompat.VerdictClear:
			if len(r.Refusals) != 0 {
				t.Errorf("%s %s reads clear with %d refusal(s)", r.Module, r.Example, len(r.Refusals))
			}
		case modcompat.VerdictRefused:
			if len(r.Refusals) == 0 {
				t.Errorf("%s %s reads refused with no refusal", r.Module, r.Example)
			}
		case modcompat.VerdictUnmeasured:
			if r.Reason == "" {
				t.Errorf("%s %s is unmeasured with no reason", r.Module, r.Example)
			}
		default:
			t.Errorf("%s %s has verdict %q", r.Module, r.Example, r.Verdict)
		}
		for _, x := range r.Refusals {
			if x.ID == dataread.SummaryEligibleRead {
				t.Errorf("%s %s lists the eligible read as a refusal; it is a read count (#2295)", r.Module, r.Example)
			}
			if x.Sites <= 0 {
				t.Errorf("%s %s: refusal %q has %d sites", r.Module, r.Example, x.ID, x.Sites)
			}
			if x.DocsRef == "" {
				continue
			}
			ref, err := docsref.Parse(x.DocsRef)
			if err != nil {
				t.Errorf("%s %s: refusal %q: %v", r.Module, r.Example, x.ID, err)
				continue
			}
			if err := ref.Resolve(root); err != nil {
				t.Errorf("%s %s: refusal %q cites %s, which does not resolve: %v", r.Module, r.Example, x.ID, x.DocsRef, err)
			}
			if x.DocsURL != modcompat.DocsURL(x.DocsRef) {
				t.Errorf("%s %s: refusal %q links %s, want %s", r.Module, r.Example, x.ID, x.DocsURL, modcompat.DocsURL(x.DocsRef))
			}
		}
	}
	for _, s := range m.Sources {
		if n := perSource[[2]string{s.Module, s.Version}]; n != s.Examples || n == 0 {
			t.Errorf("%s %s: %d row(s), and the measurement recorded %d example(s)", s.Module, s.Version, n, s.Examples)
		}
	}
}

// moduleCompatCorpus is the terraform-aws-modules population the static tier
// covers (#2292): the 25 most downloaded modules of the namespace on the
// public registry on 2026-10-27, and serverless.tf's module list. eks is
// measured from its v21 source; its v9.0.0 checkout is a gauntlet input.
var moduleCompatCorpus = []string{
	// The 25 most downloaded, in registry order.
	"iam", "s3-bucket", "vpc", "eks", "kms", "security-group", "lambda",
	"rds-aurora", "rds", "acm", "route53", "alb", "ec2-instance", "sqs",
	"dynamodb-table", "ecs", "cloudfront", "cloudwatch", "eventbridge",
	"autoscaling", "sns", "ecr", "secrets-manager", "key-pair",
	"eks-pod-identity",
	// serverless.tf's list, less the ones above.
	"apigateway-v2", "appsync", "appconfig", "step-functions", "rds-proxy",
	"ssm-parameter",
}

// TestModuleCompatCorpusHoldsTheChosenModules fails when a module the static
// tier promises is dropped from live/corpus-manifest.json, or when a source
// is added without the list above (and the comment in the manifest) saying
// why it is there. Proving it red: delete the kms source from the manifest.
func TestModuleCompatCorpusHoldsTheChosenModules(t *testing.T) {
	root := moduleCompatRoot(t)
	sources, err := modcompat.ManifestSources(root)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, s := range sources {
		have[s.Module] = true
	}
	want := map[string]bool{}
	for _, name := range moduleCompatCorpus {
		module := "terraform-aws-modules/" + name + "/aws"
		want[module] = true
		if !have[module] {
			t.Errorf("%s has no terraform-aws-modules source for %s, which the static tier covers (#2292)", modcompat.ManifestRel, module)
		}
	}
	for module := range have {
		if !want[module] {
			t.Errorf("%s measures %s, which moduleCompatCorpus does not list; add it there with the reason it is in the corpus", modcompat.ManifestRel, module)
		}
	}
}

// TestModuleCompatPageRendersTheFile holds the site page to the file: the
// component reads live/module-compat.json, and the compatibility page places
// it. A table typed into the page would pass neither.
func TestModuleCompatPageRendersTheFile(t *testing.T) {
	root := moduleCompatRoot(t)
	comp, err := os.ReadFile(filepath.Join(root, "site", "src", "components", "ModuleCompat.astro"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(comp), "live/module-compat.json") {
		t.Error("site/src/components/ModuleCompat.astro does not import live/module-compat.json")
	}
	page, err := os.ReadFile(filepath.Join(root, "site", "src", "content", "docs", "aws", "compatibility.mdx"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "<ModuleCompat />") {
		t.Error("site/src/content/docs/aws/compatibility.mdx does not place <ModuleCompat />")
	}
}

// moduleCompatRoot is the checkout root: the nearest go.mod above the test's
// working directory.
func moduleCompatRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above the test's working directory")
		}
		dir = parent
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
