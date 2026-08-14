// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestContractMDXRenderedSpans holds COVERAGE.md's two admitted-set spans
// - the resource-type count and the type enumeration - byte-for-byte to
// what the renderer produces from the compiled admission table (issue
// #54). This replaces TestContractEnumerationMatchesAdmissionTable's
// two-way string scan: the doc is now generated rather than hand-edited,
// so the test only has to prove the committed bytes are what the generator
// would write, the same drift pattern TestSurveyMDRenderedSpans and
// TestLimitationsMDResidueRosterSpans already apply to their docs. No
// provider and no network, so it is not gated.
func TestContractMDXRenderedSpans(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	mdPath := filepath.Join(root, contractMDXRel)
	mdBytes, err := os.ReadFile(mdPath) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v", contractMDXRel, err)
	}
	md := string(mdBytes)

	for _, span := range []struct {
		name, want string
	}{
		{spanContractCount, renderContractCount()},
		{spanContractTypes, renderContractTypes()},
	} {
		got, err := spanContent(contractMDXRel, md, span.name)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if got != span.want {
			t.Errorf("%s's %q span is stale; run `go run ./tools/survey-gen -render` and commit the result.\n--- committed ---\n%s\n--- rendered ---\n%s",
				contractMDXRel, span.name, got, span.want)
		}
	}

	// The whole-file check catches what the per-span one cannot: a marker
	// pair going missing or duplicated.
	if out, err := renderContractSpans(md); err != nil {
		t.Errorf("rendering %s's Contract spans: %v", contractMDXRel, err)
	} else if out != md {
		t.Errorf("%s differs from its rendered form; run `go run ./tools/survey-gen -render` and commit the result", contractMDXRel)
	}
}
