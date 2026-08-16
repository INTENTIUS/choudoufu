// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// separatorExpectations pins what the shipped artifact claims about
// separators, measured at issue #135.
//
// Counts rather than a per-row ledger, because these move together when the
// provider's docs are re-swept and a 1,600-row list would be rewritten
// wholesale on every bump - which is how a ledger stops being read.
var separatorExpectations = struct {
	withSeparator   int
	notInOwnExample int
}{
	// 436 at issue #135; 435 since issue #132's plain-prose resolver:
	// aws_cognito_identity_pool_roles_attachment's ":" came from
	// separatorFromExample splitting "us-west-2:b64805ad-…", but the page's
	// own sentence says the whole string is one Identity Pool ID, so the
	// colon is internal to the value and the separator claim was wrong.
	//
	// 437 (2026-08-16): aliasDeclaredFor started cloning a canonical type's
	// whole row - separator included - onto its provider-documented
	// TF-side alias (aws_alb is known as aws_lb, "functionality is
	// identical"). Of the six aws_alb* clones, two carry a real separator
	// because their canonical row already did: aws_alb_listener_
	// certificate ("_", cloned from aws_lb_listener_certificate) and
	// aws_alb_target_group_attachment (",", cloned from aws_lb_target_
	// group_attachment). +2, not a scrape regression - the same evidence
	// counted twice under two registered type names, exactly what the
	// alias note claims.
	withSeparator:   437,
	notInOwnExample: 16,
}

// TestShippedSeparatorCounts is the ratchet.
//
// The number that matters is the second one. A separator absent from its own
// example is legitimate and common: aws_s3_bucket_acl's page states a comma
// for the "bucket,expected_bucket_owner" form while its example shows a bare
// bucket name, and the same is true of aws_cognito_risk_configuration and
// aws_dx_gateway_association_proposal. Thirteen of the sixteen predate this
// issue.
//
// It is also exactly where a wrong answer hides, because nothing about the
// row looks odd. So the count is pinned: a scrape change that starts
// asserting separators the examples contradict has to say so here, with the
// rows named, rather than adding them quietly to a field 462 rows already
// carry.
func TestShippedSeparatorCounts(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skipf("repo root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "live", "import-grammar.json"))
	if err != nil {
		t.Skipf("artifact not generated: %v", err)
	}
	var art struct {
		Rows []struct {
			TFType    string  `json:"tf_type"`
			Separator *string `json:"separator"`
			Example   string  `json:"import_id_example"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decoding the artifact: %v", err)
	}

	var withSep int
	var mismatched []string
	for _, r := range art.Rows {
		if r.Separator == nil {
			continue
		}
		withSep++
		example := strings.TrimSpace(r.Example)
		if example != "" && !strings.Contains(example, *r.Separator) {
			mismatched = append(mismatched, r.TFType+" ("+*r.Separator+" not in "+example+")")
		}
	}

	if withSep != separatorExpectations.withSeparator {
		t.Errorf("rows with a separator = %d, expected %d.\n"+
			"A scrape change moved this. Update the number with the diff that justifies it.",
			withSep, separatorExpectations.withSeparator)
	}
	if len(mismatched) != separatorExpectations.notInOwnExample {
		t.Errorf("rows whose separator does not occur in their own example = %d, expected %d:\n  %s\n"+
			"Each of these is a separator asserted against the row's own evidence. That is legitimate "+
			"when the page states a separator for a composite form and shows the simple one, and it is "+
			"also where a wrong answer hides. Name the rows and why before moving this number.",
			len(mismatched), separatorExpectations.notInOwnExample, strings.Join(mismatched, "\n  "))
	}
}
