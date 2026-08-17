// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The "Attribute-level residue" section's three figures - write-only,
// sensitive-and-settable, and how many of the latter are unconditionally
// required - were hand-quoted from GitHub issue #126's comment ("10 types /
// 21 attributes", "53 types / 132 attributes", "7 ... unconditionally
// required") and never revisited. The admission table issue #126 measured
// against had 846 types; live/LIMITATIONS.md's own denominator
// (identity.AdmittedTypes()) is 905 today, and the true figures against the
// same rule and the same provider version are 12/23, 60/140 and 8. Same
// probe, same version, different table - the doc just never re-ran it.
//
// These three now render from live/wo-sweep.json, tools/wo-sweep's own
// committed output (`just wo-sweep` regenerates it; it is the one input to
// this generator that needs a provider, which is why it is committed rather
// than computed here). tools/limits-gen/residue_render_test.go is the drift
// guard: it re-renders from the committed artifact and fails if the shipped
// spans disagree, so a table that grows without a `just wo-sweep &&
// just limits` run is caught rather than quoted stale a second time.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

const (
	woSweepRel = "live/wo-sweep.json"

	spanResidueHard       = "residue-hard-count"
	spanResidueSoft       = "residue-soft-count"
	spanResidueSoftReqTop = "residue-soft-required-top-level"
)

// residueSweep is the subset of tools/wo-sweep's output this generator
// needs, decoded straight from the committed artifact rather than from a
// second hand-typed constant.
type residueSweep struct {
	HardTypes  int `json:"types_with_hard"`
	HardAttrs  int `json:"hard_attrs"`
	SoftTypes  int `json:"types_with_sensitive_settable"`
	SoftAttrs  int `json:"sensitive_settable_attrs"`
	SoftReqTop int `json:"sensitive_settable_required_top_level"`
}

// readWoSweep reads the committed sweep artifact. Unlike readCorpus's
// frequency table, this is not optional: the sentences these spans sit in
// need actual numbers to parse, so a missing or unreadable artifact fails
// the generator rather than rendering a dash.
func readWoSweep(path string) (residueSweep, error) {
	src, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return residueSweep{}, fmt.Errorf("%s: %w (run `just wo-sweep` first)", woSweepRel, err)
	}
	var s residueSweep
	if err := json.Unmarshal(src, &s); err != nil {
		return residueSweep{}, fmt.Errorf("%s: %w", woSweepRel, err)
	}
	return s, nil
}

func renderResidueHard(s residueSweep) string {
	return fmt.Sprintf("%s / %s", plural(s.HardTypes, "type"), plural(s.HardAttrs, "attribute"))
}

func renderResidueSoft(s residueSweep) string {
	return fmt.Sprintf("%s / %s", plural(s.SoftTypes, "type"), plural(s.SoftAttrs, "attribute"))
}

func renderResidueSoftReqTop(s residueSweep) string {
	return fmt.Sprintf("%d", s.SoftReqTop)
}

// applyResidueSpans writes all three inline spans into md, in one place so
// main's run() and the drift test render exactly the same way.
func applyResidueSpans(md string, s residueSweep) (string, error) {
	for _, span := range []struct {
		name string
		body string
	}{
		{spanResidueHard, renderResidueHard(s)},
		{spanResidueSoft, renderResidueSoft(s)},
		{spanResidueSoftReqTop, renderResidueSoftReqTop(s)},
	} {
		var err error
		md, err = markers.ReplaceInline(limitationsRel, md, span.name, span.body)
		if err != nil {
			return "", err
		}
	}
	return md, nil
}
