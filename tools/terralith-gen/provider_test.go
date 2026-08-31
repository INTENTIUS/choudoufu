// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// This file is issue #628's guard.
//
// The defect it exists for: this generator emitted
// skip_requesting_account_id = true, which leaves the AWS provider's account
// id empty, which strips the account segment off every ARN-shaped identity it
// composes, which breaks ECS identity resolution (#572), which since #596
// makes `choudoufu plan` on this generator's own default output exit 1. The
// generator shipped that way for its whole life and nothing in the repository
// could notice, because every harness that actually planned the output
// replaced the provider block on its way past.
//
// The oracle deliberately is not this package. A check that reads
// versionsTF() and asserts what versionsTF() says passes forever and is
// removable by whoever re-introduces the flag: it measures agreement with
// itself. So the rule below is DERIVED from live/e2e/estate/versions.tf - a
// hand-written, hand-owned choudoufu fixture that made the same call first,
// under P2.3, from an unrelated symptom (with the account unresolved, the
// owner-id filter the provider appends to a filtered EC2 list goes out
// empty). Re-adding the flag here now means contradicting that file and the
// reason written inside it, not just deleting an assertion.

// ratifiedProviderFixture is the external oracle: choudoufu's own P0.1 estate
// fixture, whose provider block is wired the same way this generator's is
// (endpoint, region and credentials from the environment; only the flags with
// no environment-variable form written down) and which states its
// skip_requesting_account_id decision in a comment beside it.
const ratifiedProviderFixture = "../../live/e2e/estate/versions.tf"

// suppressiveProviderFlags returns, sorted, the names of every skip_*
// argument the `provider "aws"` block in src sets to a literal true.
//
// It returns an error rather than an empty slice when there is no such block
// or it will not parse, so that a caller cannot silently conclude "no flags
// are set" from having seen nothing at all - the failure shape an audit of
// this repository's completeness checks has caught more than once.
func suppressiveProviderFlags(src []byte, filename string) ([]string, error) {
	f, diags := hclparse.NewParser().ParseHCL(src, filename)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parsing %s: %s", filename, diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("%s did not parse as native HCL syntax", filename)
	}

	found := false
	var flags []string
	for _, blk := range body.Blocks {
		if blk.Type != "provider" || len(blk.Labels) != 1 || blk.Labels[0] != "aws" {
			continue
		}
		found = true
		for name, attr := range blk.Body.Attributes {
			if !strings.HasPrefix(name, "skip_") {
				continue
			}
			v, vDiags := attr.Expr.Value(nil)
			if vDiags.HasErrors() || v.IsNull() || !v.IsKnown() {
				continue
			}
			if v.Type() == cty.Bool && v.True() {
				flags = append(flags, name)
			}
		}
	}
	if !found {
		return nil, fmt.Errorf(`%s has no `+"`"+`provider "aws"`+"`"+` block`, filename)
	}
	sort.Strings(flags)
	return flags, nil
}

// generatedVersionsTF writes a real estate and reads back the versions.tf it
// shipped, rather than calling versionsTF() - the subject is the artifact a
// caller gets, and write() is between the two.
func generatedVersionsTF(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "terralith")
	if err := buildEstate(1, "tl").write(out); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out, "versions.tf")
	src, err := os.ReadFile(path) //nolint:gosec // a path this test just generated
	if err != nil {
		t.Fatalf("reading the generated %s: %v", path, err)
	}
	return src
}

// TestGeneratedProviderBlockSuppressesNothingTheFixtureKeeps is the rule.
//
// Subset, not equality, and the asymmetry is the point: the ratified fixture
// is free to turn a probe off that this generator leaves on, but this
// generator must never turn off a probe the fixture keeps. Account resolution
// is the probe that matters, and it is the one the fixture keeps.
func TestGeneratedProviderBlockSuppressesNothingTheFixtureKeeps(t *testing.T) {
	fixtureSrc, err := os.ReadFile(ratifiedProviderFixture)
	if err != nil {
		t.Fatalf("reading the ratified fixture %s: %v", ratifiedProviderFixture, err)
	}
	ratified, err := suppressiveProviderFlags(fixtureSrc, ratifiedProviderFixture)
	if err != nil {
		t.Fatalf("%v\n"+
			"This test derives its rule from that file; it cannot run without it. If the "+
			"fixture moved, repoint ratifiedProviderFixture at wherever choudoufu's own "+
			"hand-written provider wiring now lives - do not replace it with a literal here.", err)
	}

	generated, err := suppressiveProviderFlags(generatedVersionsTF(t), "versions.tf")
	if err != nil {
		t.Fatalf("the generated versions.tf: %v", err)
	}

	extra := notIn(generated, ratified)
	if len(extra) > 0 {
		t.Errorf("the generated provider block turns off %s, which %s deliberately leaves on.\n"+
			"generated: %v\nratified:  %v\n"+
			"skip_requesting_account_id in particular is issue #628: with it set the provider's "+
			"account id is empty, every ARN-shaped identity it composes loses its account segment, "+
			"ECS identity resolution fails (#572), and since #596 `choudoufu plan` REFUSES on this "+
			"generator's own default output - measured at scale 1 against floci as exit 1 in 137s, "+
			"against exit 0 in 2s and `No changes` with the one line removed.\n"+
			"Fix the template in gen.go's versionsTF, and read that function's doc comment before "+
			"deciding this test is the thing that is wrong.",
			strings.Join(extra, ", "), ratifiedProviderFixture, generated, ratified)
	}
}

// TestSuppressiveProviderFlagsHasTeeth feeds the checker the exact provider
// block this generator shipped before #628 and requires it to report the
// flag, because a rule written from the post-fix output passes on the
// post-fix output by construction.
//
// It also feeds it two shapes that must fail loudly rather than read as
// "nothing suppressed": a file with no aws provider block, and one that does
// not parse.
func TestSuppressiveProviderFlagsHasTeeth(t *testing.T) {
	// Byte-for-byte what versionsTF() emitted at 4c434c45b2, misaligned
	// s3_use_path_style included.
	const preFix = `provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style            = true
}
`
	flags, err := suppressiveProviderFlags([]byte(preFix), "pre-fix.tf")
	if err != nil {
		t.Fatalf("the pre-#628 block did not parse: %v", err)
	}
	if !contains(flags, "skip_requesting_account_id") {
		t.Errorf("fed the pre-#628 provider block, suppressiveProviderFlags reported %v and missed "+
			"skip_requesting_account_id - it would not have caught the defect it exists for", flags)
	}

	fixtureSrc, err := os.ReadFile(ratifiedProviderFixture)
	if err != nil {
		t.Fatalf("reading %s: %v", ratifiedProviderFixture, err)
	}
	ratified, err := suppressiveProviderFlags(fixtureSrc, ratifiedProviderFixture)
	if err != nil {
		t.Fatal(err)
	}
	if extra := notIn(flags, ratified); len(extra) == 0 {
		t.Errorf("the subset rule accepts the pre-#628 block against %s (ratified: %v), so it "+
			"would have passed on the broken generator", ratifiedProviderFixture, ratified)
	}

	if _, err := suppressiveProviderFlags([]byte("terraform {}\n"), "no-provider.tf"); err == nil {
		t.Error("a file with no aws provider block returned no error; a caller would read that as " +
			"\"nothing is suppressed\" having seen nothing at all")
	}
	if _, err := suppressiveProviderFlags([]byte("provider \"aws\" {\n"), "broken.tf"); err == nil {
		t.Error("an unparseable file returned no error")
	}
}

// degenerateOracle reports why a set of flags read off the ratified fixture
// would make the subset rule above say nothing, or the empty string when the
// oracle is usable.
//
// Both failure shapes are silent by construction, which is why they are a
// separate check: a fixture that starts setting skip_requesting_account_id
// makes the subset rule ACCEPT the flag here, and a fixture whose provider
// block stopped being found at all makes it reject flags that are fine.
// Neither shows up as a failure of the rule itself.
func degenerateOracle(flags []string) string {
	if contains(flags, "skip_requesting_account_id") {
		return "the ratified fixture now sets skip_requesting_account_id itself, which silently makes " +
			"TestGeneratedProviderBlockSuppressesNothingTheFixtureKeeps accept it in the generated " +
			"output too. That fixture's own comment explains why it does not set it (P2.3); if the " +
			"decision really changed, #628 needs re-deciding, not this test relaxing."
	}
	if len(flags) == 0 {
		return "the ratified fixture sets no skip_* flag at all, so the subset rule above now rejects " +
			"every flag this generator sets. Either the fixture changed shape or the parse is " +
			"looking at the wrong block."
	}
	return ""
}

// TestRatifiedFixtureStillLetsTheProviderResolveTheAccount guards the oracle
// itself.
func TestRatifiedFixtureStillLetsTheProviderResolveTheAccount(t *testing.T) {
	src, err := os.ReadFile(ratifiedProviderFixture)
	if err != nil {
		t.Fatalf("reading %s: %v", ratifiedProviderFixture, err)
	}
	flags, err := suppressiveProviderFlags(src, ratifiedProviderFixture)
	if err != nil {
		t.Fatal(err)
	}
	if problem := degenerateOracle(flags); problem != "" {
		t.Fatalf("%s: %s (read: %v)", ratifiedProviderFixture, problem, flags)
	}
}

// TestDegenerateOracleHasTeeth proves the oracle guard can fail, on both of
// its branches, without editing a file this package does not own.
func TestDegenerateOracleHasTeeth(t *testing.T) {
	for name, flags := range map[string][]string{
		"fixture adopts the flag":           {"skip_credentials_validation", "skip_requesting_account_id"},
		"fixture parses to no flags at all": {},
	} {
		if degenerateOracle(flags) == "" {
			t.Errorf("%s: degenerateOracle(%v) reported nothing, so the oracle guard would stay green "+
				"while the rule it protects said nothing", name, flags)
		}
	}
	if problem := degenerateOracle([]string{"skip_credentials_validation", "skip_metadata_api_check"}); problem != "" {
		t.Errorf("degenerateOracle rejected the fixture's actual shape: %s", problem)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// notIn returns the members of a that are absent from b, preserving a's order.
func notIn(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}
