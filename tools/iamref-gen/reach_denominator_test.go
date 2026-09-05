// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// reachMDRel is the page issue #843 is about: it renders a "N of M services"
// sentence from two iamref shortcode calls, and until that issue the M was
// site/layouts/shortcodes/iamref.html's "services-count" field - the total
// AWS services the admission table reaches, 17 of them never resolved to an
// IAM name and so never checked for aws:ResourceTag at all.
const reachMDRel = "site/content/docs/use/governance/reach.md"

// reachConfirmedCountRe finds the "confirmed count" sentence in reach.md and
// captures the two iamref field names it uses as numerator and denominator.
// Anchored on "services explicitly name `aws:ResourceTag`" so a different
// iamref call elsewhere on the page (the "named"/"unnamed" tables) cannot be
// mistaken for it.
var reachConfirmedCountRe = regexp.MustCompile(
	`(?s)\{\{<\s*iamref\s+field="([a-z-]+)"\s*>\}\}\s*of\s*\{\{<\s*iamref\s+field="([a-z-]+)"\s*>\}\}` +
		`.{0,80}?services explicitly name`,
)

// checkedNamedUnnamed mirrors site/layouts/shortcodes/iamref.html's own
// filter, row for row: rows are kept when actions_total > 0, deduped by
// iam_prefix (the exact $filtered slice both the "named" and "unnamed"
// tables are split from), then split on whether
// actions_listing_resource_tag is recorded. checked is len($filtered) -
// what the shortcode's "checked-count" field renders - and always equals
// named + unnamed by construction, in the template and here alike.
//
// This is a second implementation of that filter, not a call into the
// template, because nothing in this repository's fast test tier can execute
// a Hugo template (hugo is installed by CI only after the Go test step -
// see .github/workflows/ci.yml). Keep it in step with iamref.html's range
// block if that ever changes.
func checkedNamedUnnamed(rows []Row) (checked, named, unnamed int) {
	seen := make(map[string]bool)
	for _, r := range rows {
		if r.ActionsTotal <= 0 {
			continue
		}
		if seen[r.IAMPrefix] {
			continue
		}
		seen[r.IAMPrefix] = true
		checked++
		if r.ActionsListingResourceTag > 0 {
			named++
		} else {
			unnamed++
		}
	}
	return checked, named, unnamed
}

// fieldValue resolves one iamref shortcode field name to the value it would
// render, for the fields the confirmed-count sentence could plausibly name.
// services-count and resolved-count come straight off the artifact's Counts,
// exactly as the shortcode's own branches do; the rest are the checked
// population computed above.
func fieldValue(t *testing.T, art Artifact, field string) int {
	t.Helper()
	checked, named, unnamed := checkedNamedUnnamed(art.Rows)
	switch field {
	case "services-count":
		return art.Counts.Services
	case "resolved-count":
		return art.Counts.Resolved
	case "checked-count":
		return checked
	case "named-count":
		return named
	case "unnamed-count":
		return unnamed
	default:
		t.Fatalf("reach.md's confirmed-count sentence names an iamref field %q this test does not know how to resolve", field)
		return 0
	}
}

// TestReachConfirmedCountDenominatorIsTheCheckedPopulation is issue #843's
// guard.
//
// reach.md's "Where the key is confirmed" section renders a "named-count of
// X services explicitly name aws:ResourceTag" sentence. The population that
// fraction was actually computed over is the checked population the
// shortcode's own named/unnamed split filters rows to (actions_total > 0,
// deduped by iam_prefix) - not "every AWS service the admission table
// reaches" (services-count, which includes 17 services never resolved to an
// IAM name and so never checked for this key at all) and not even "every
// service resolved to an IAM name" (resolved-count, which still overcounts
// by the rows resolved but missing a recorded action count).
//
// This asserts the denominator field named in that sentence renders exactly
// named + unnamed, whichever field it is - so pointing the sentence back at
// services-count or resolved-count fails here, by value, not just by name.
func TestReachConfirmedCountDenominatorIsTheCheckedPopulation(t *testing.T) {
	root := repoRootForTest(t)

	raw, err := os.ReadFile(filepath.Join(root, "live", "iam-reference.json"))
	if err != nil {
		t.Fatalf("reading the artifact: %v", err)
	}
	var art Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decoding the artifact: %v", err)
	}
	if len(art.Rows) == 0 {
		t.Fatal("the artifact has no rows; this test measures nothing")
	}

	doc, err := os.ReadFile(filepath.Join(root, reachMDRel))
	if err != nil {
		t.Fatalf("reading %s: %v", reachMDRel, err)
	}

	m := reachConfirmedCountRe.FindStringSubmatch(string(doc))
	if m == nil {
		t.Fatalf("%s no longer has a recognizable confirmed-count sentence "+
			"(pattern: %s). If the sentence moved or was reworded, update this test's regexp with it.",
			reachMDRel, reachConfirmedCountRe.String())
	}
	numeratorField, denominatorField := m[1], m[2]

	_, named, unnamed := checkedNamedUnnamed(art.Rows)
	checked := named + unnamed

	if got := fieldValue(t, art, numeratorField); got != named {
		t.Errorf("%s's confirmed-count sentence numerator is field %q, which renders %d, "+
			"but the shortcode's own named/unnamed split gives %d named services. "+
			"The numerator should be named-count.", reachMDRel, numeratorField, got, named)
	}
	if got := fieldValue(t, art, denominatorField); got != checked {
		t.Errorf("%s's confirmed-count sentence denominator is field %q, which renders %d, "+
			"but the checked population (named + unnamed, the same rows the shortcode's own "+
			"named/unnamed split filters to) is %d. %q overcounts: it includes services never "+
			"examined for aws:ResourceTag at all. Use field=\"checked-count\" instead.",
			reachMDRel, denominatorField, got, checked, denominatorField)
	}
}
