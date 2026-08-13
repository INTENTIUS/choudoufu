// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The Contract render (issue #54): website/docs/language/live-markers.mdx's
// "The Contract" section is the one place outside this repository that both
// counts and enumerates the admitted types, so it is the doc a reader
// reaches for from the docs site rather than the source tree. Its count and
// roster used to be hand-updated every wiring batch (37 -> 42 in the Lambda
// pilot); this file renders both from identity.AdmittedTypes, the same
// compiled admission table TestContractMDXRenderedSpans holds them to, no
// provider and no network - the same render/drift pattern SURVEY.md's and
// LIMITATIONS.md's spans already use.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// contractMDXRel is the docs-site concept page whose Contract section this
// file renders spans into.
const contractMDXRel = "website/docs/language/live-markers.mdx"

// The two rendered spans in "The Contract" section, on the same marker
// convention every other survey-gen span uses (see render.go's
// spanMarkers). Both markers sit mid-line, attached to surrounding prose
// rather than starting their own line, so the HTML comments stay inline
// content instead of interrupting the bullet's paragraph.
const (
	// spanContractCount is the resource-type count in "AWS only, N resource
	// types, root module only."
	spanContractCount = "contract-count"

	// spanContractTypes is the backtick-quoted, comma-separated enumeration
	// naming every admitted type.
	spanContractTypes = "contract-types"
)

// renderContractMDX rewrites live-markers.mdx's two Contract spans in
// place, from the compiled admission table.
func renderContractMDX(root string) error {
	mdPath := filepath.Join(root, contractMDXRel)
	md, err := os.ReadFile(mdPath) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return err
	}

	out, err := renderContractSpans(string(md))
	if err != nil {
		return err
	}
	if out == string(md) {
		fmt.Fprintf(os.Stderr, "survey-gen: %s's Contract spans are already current\n", contractMDXRel)
		return nil
	}
	if err := os.WriteFile(mdPath, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "survey-gen: rewrote %s's Contract spans\n", contractMDXRel)
	return nil
}

// renderContractSpans returns the doc with both Contract spans replaced by
// their rendered bodies. The rest of the file, including every other
// enumeration on the page (the classifier's seven content-matchable types,
// the two named orphan cases, the adoption loop), passes through
// byte-for-byte: this render mode is scoped to the Contract's own count and
// roster, the two facts issue #54 names as hand-updated every batch.
func renderContractSpans(md string) (string, error) {
	md, err := replaceSpan(contractMDXRel, md, spanContractCount, renderContractCount())
	if err != nil {
		return "", err
	}
	return replaceSpan(contractMDXRel, md, spanContractTypes, renderContractTypes())
}

// renderContractCount is the admission table's size, the same figure
// SURVEY.md's wired-count span renders (render.go's renderWiredCount).
func renderContractCount() string {
	return fmt.Sprintf("%d", len(identity.AdmittedTypes()))
}

// renderContractTypes enumerates every admitted type, backtick-quoted,
// Oxford-comma joined, and word-wrapped to the bullet's own 2-space
// continuation indent - the convention the hand-written list already used.
func renderContractTypes() string {
	return wrapIndented(joinWithAnd(backtickTypes(identity.AdmittedTypes()), true), 78, "  ")
}
