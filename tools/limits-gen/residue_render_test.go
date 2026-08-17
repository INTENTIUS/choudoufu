// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestResidueSpansMatchTheArtifact is the freshness guard for the
// "Attribute-level residue" section's three figures.
//
// It re-renders all three inline spans from the committed
// live/wo-sweep.json and compares them against what live/LIMITATIONS.md
// currently ships. The document once stated 10/21 and 53/132, quoted
// verbatim from GitHub issue #126's comment; the true figures against the
// same rule and the same hashicorp/aws 6.59.0 schema, recomputed once the
// admission table grew past #126's 846 types, are 12/23 and 60/140 - and
// nothing failed when the two drifted apart, because nothing compared
// them. This does.
//
// The markers themselves close the other half: strings.Count in
// mdspan.bounds requires exactly one begin and one end marker, so
// restoring the old unqualified "10 types / 21 attributes" prose without
// also deleting the span markers leaves two numbers in the sentence, and
// deleting the markers to hide that makes markers.Content error out below
// rather than silently reading stale text.
func TestResidueSpansMatchTheArtifact(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	sweep, err := readWoSweep(filepath.Join(root, filepath.FromSlash(woSweepRel)))
	if err != nil {
		t.Fatalf("reading %s: %v", woSweepRel, err)
	}

	md, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(limitationsRel)))
	if err != nil {
		t.Fatalf("reading %s: %v", limitationsRel, err)
	}

	for _, span := range []struct {
		name string
		want string
	}{
		{spanResidueHard, renderResidueHard(sweep)},
		{spanResidueSoft, renderResidueSoft(sweep)},
		{spanResidueSoftReqTop, renderResidueSoftReqTop(sweep)},
	} {
		got, err := markers.ContentInline(limitationsRel, string(md), span.name)
		if err != nil {
			t.Errorf("%s: %v", span.name, err)
			continue
		}
		if got != span.want {
			t.Errorf("%s's %q span is stale: shipped %q, %s recomputes %q.\n"+
				"Run `just wo-sweep && just limits` and commit the result, rather than editing the figure by hand.",
				limitationsRel, span.name, got, woSweepRel, span.want)
		}
	}

	// The whole-file check: applying the same replace the generator runs
	// must be a no-op against what is committed. This is what catches a
	// marker pair going missing or duplicated, which the per-span read above
	// cannot: markers.ContentInline already requires exactly one of each, so
	// a doc with zero or two would have failed above with an error, not a
	// mismatch - this recomputes the whole substitution as belt and braces.
	out, err := applyResidueSpans(string(md), sweep)
	if err != nil {
		t.Fatalf("re-rendering the residue spans: %v", err)
	}
	if out != string(md) {
		t.Errorf("%s differs from its rendered form; run `just limits` and commit the result", limitationsRel)
	}
}

// TestResidueCountsAgreeWithAnIndependentRead defeats the shape where the
// span and its guard are the same derivation twice: if this only re-read
// live/wo-sweep.json's own types_with_hard/hard_attrs/etc. counters and
// echoed them back, a corrupted top-line counter in the artifact - written
// by a future wo-sweep bug - would ship as a document nobody could tell was
// wrong from the artifact alone.
//
// So this walks the artifact's own raw per-attribute findings independently
// and recomputes the four counts from scratch, the way tools/wo-sweep's own
// main loop does, then checks that recount against the top-line counters
// the document actually cites. This is bounded exactly like
// TestGovernanceSplitAgreesWithAnIndependentRead in tools/survey-gen: it
// catches a broken counter, not a wrong sweep. Whether wo-sweep visited the
// right provider schema at all is `just wo-sweep`'s own business - see its
// package doc comment.
func TestResidueCountsAgreeWithAnIndependentRead(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(woSweepRel)))
	if err != nil {
		t.Fatalf("reading %s: %v", woSweepRel, err)
	}

	var artifact struct {
		AdmittedTypes int `json:"admitted_types"`
		Findings      []struct {
			Type              string                  `json:"type"`
			Hard              []struct{ Path string } `json:"hard"`
			SensitiveSettable []struct {
				Path     string `json:"path"`
				Required bool   `json:"required"`
			} `json:"sensitive_settable"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatalf("decoding %s: %v", woSweepRel, err)
	}

	var hardTypes, hardAttrs, softTypes, softAttrs, softReqTop int
	for _, tf := range artifact.Findings {
		if len(tf.Hard) > 0 {
			hardTypes++
			hardAttrs += len(tf.Hard)
		}
		if len(tf.SensitiveSettable) > 0 {
			softTypes++
			softAttrs += len(tf.SensitiveSettable)
			for _, a := range tf.SensitiveSettable {
				topLevel := true
				for _, c := range a.Path {
					if c == '.' {
						topLevel = false
						break
					}
				}
				if a.Required && topLevel {
					softReqTop++
				}
			}
		}
	}

	sweep, err := readWoSweep(filepath.Join(root, filepath.FromSlash(woSweepRel)))
	if err != nil {
		t.Fatalf("reading %s: %v", woSweepRel, err)
	}

	if hardTypes != sweep.HardTypes || hardAttrs != sweep.HardAttrs {
		t.Errorf("independent recount of the write-only bucket (%d types / %d attrs) disagrees with %s's own counters (%d types / %d attrs)",
			hardTypes, hardAttrs, woSweepRel, sweep.HardTypes, sweep.HardAttrs)
	}
	if softTypes != sweep.SoftTypes || softAttrs != sweep.SoftAttrs {
		t.Errorf("independent recount of the sensitive-settable bucket (%d types / %d attrs) disagrees with %s's own counters (%d types / %d attrs)",
			softTypes, softAttrs, woSweepRel, sweep.SoftTypes, sweep.SoftAttrs)
	}
	if softReqTop != sweep.SoftReqTop {
		t.Errorf("independent recount of unconditionally-required sensitive-settable attributes (%d) disagrees with %s's own counter (%d)",
			softReqTop, woSweepRel, sweep.SoftReqTop)
	}

	// An anti-tamper floor, in the spirit of tools/survey-gen's
	// governanceSweepFloor: every figure here can be made to agree by
	// measuring less (an empty findings list makes every count zero and
	// every comparison above pass vacuously). admitted_types below #126's
	// own 846 would mean the denominator shrank below what was already
	// measured once, which has never happened and would be the more
	// interesting finding.
	const admittedFloor = 846
	if artifact.AdmittedTypes < admittedFloor {
		t.Errorf("%s reports %d admitted types, below the floor of %d that GitHub issue #126 already measured.\n"+
			"If the admission table genuinely shrank below that, lower this floor in the commit that shrank it.",
			woSweepRel, artifact.AdmittedTypes, admittedFloor)
	}
	if hardTypes == 0 || softTypes == 0 {
		t.Errorf("the residue sweep found %d write-only types and %d sensitive-settable types; both buckets have always been non-empty, so a zero means the walk found nothing rather than that the class is empty",
			hardTypes, softTypes)
	}
}
