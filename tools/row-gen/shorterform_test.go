// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"sort"
	"strings"
	"testing"
)

// TestTryDocumentedShorterForm is the unit half: the shapes rule 1b accepts
// and, more importantly, the four it refuses. Each refusal case is a real
// row from the 6.59.0 scrape, not an invented one, because the rule's whole
// risk is that "the argument list is longer than the example" has several
// causes and only one of them means "the doc showed a shorter form".
func TestTryDocumentedShorterForm(t *testing.T) {
	sep := func(s string) *string { return &s }
	yes := func() *bool { b := true; return &b }

	cases := []struct {
		name     string
		row      importGrammarRow
		wantOK   bool
		wantArgs []string
		because  string
	}{
		{
			name: "two documented forms, the shorter kept, settled by Argument Reference",
			row: importGrammarRow{
				// aws_s3_bucket_object_lock_configuration.
				ImportIDExample:     "bucket-name",
				Separator:           sep(","),
				ComposedOfArguments: yes(),
				Arguments:           []string{"bucket", "expected_bucket_owner"},
				ArgumentsInOrder:    []string{"bucket", "expected_bucket_owner"},
				ArgumentReference: []argumentRefEntry{
					{Name: "bucket", Required: true},
					{Name: "expected_bucket_owner"},
				},
			},
			wantOK:   true,
			wantArgs: []string{"bucket"},
			because:  "the doc's own second form appends an Optional argument the first form omits",
		},
		{
			name: "four documented forms, settled only by the Identity Schema",
			row: importGrammarRow{
				// aws_s3_bucket_acl: bucket; bucket,acl;
				// bucket,expected_bucket_owner; bucket,expected_bucket_owner,acl.
				// The scrape's argument list omits expected_bucket_owner
				// entirely, so the prefix arithmetic alone is not evidence.
				ImportIDExample:     "bucket-name",
				Separator:           sep(","),
				ComposedOfArguments: yes(),
				Arguments:           []string{"acl", "bucket"},
				ArgumentsInOrder:    []string{"bucket", "acl"},
				ArgumentReference: []argumentRefEntry{
					{Name: "bucket", Required: true},
					{Name: "acl"},
				},
				IdentitySchemaRequired: []string{"bucket"},
			},
			wantOK:   true,
			wantArgs: []string{"bucket"},
			because:  "the provider's own Identity Schema names exactly the retained prefix",
		},
		{
			name: "the optional argument LEADS, so the short form is a suffix",
			row: importGrammarRow{
				// aws_account_alternate_contact: "OPERATIONS" alone, or
				// "1234567890/OPERATIONS" for another account.
				ImportIDExample:     "OPERATIONS",
				Separator:           sep("/"),
				ComposedOfArguments: yes(),
				Arguments:           []string{"account_id", "alternate_contact_type"},
				ArgumentsInOrder:    []string{"account_id", "alternate_contact_type"},
				ArgumentReference: []argumentRefEntry{
					{Name: "account_id"},
					{Name: "alternate_contact_type", Required: true},
				},
			},
			wantOK:  false,
			because: "taking the prefix would keep the Optional account_id and drop the Required contact type - the identity backwards",
		},
		{
			name: "Argument Reference says Optional but the Identity Schema says required-for-import",
			row: importGrammarRow{
				// aws_cloudwatch_event_target. event_bus_name is an Optional
				// argument and a required identity attribute; its omission is
				// Component.Default's business, not this rule's.
				ImportIDExample:     "rule-name/target-id",
				Separator:           sep("/"),
				ComposedOfArguments: yes(),
				Arguments:           []string{"event_bus_name", "rule", "target_id"},
				ArgumentsInOrder:    []string{"event_bus_name", "rule", "target_id"},
				ArgumentReference: []argumentRefEntry{
					{Name: "event_bus_name"},
					{Name: "rule", Required: true},
					{Name: "target_id"},
				},
				IdentitySchemaRequired: []string{"event_bus_name", "rule", "target_id"},
			},
			wantOK:  false,
			because: "the schema is consulted alone when present, and it contradicts dropping anything",
		},
		{
			name: "not a two-form page at all; the arity gap is a scrape artifact",
			row: importGrammarRow{
				// aws_lb_target_group_attachment: one documented form, whose
				// prose "and optionally `port` and `availability_zone`" put
				// two extra names into the argument list.
				ImportIDExample:     "arn:aws:elasticloadbalancing:us-west-2:111:targetgroup/abc/123,i-0123,8080",
				Separator:           sep(","),
				ComposedOfArguments: yes(),
				Arguments:           []string{"availability_zone", "port", "target_group_arn", "target_id"},
				ArgumentsInOrder:    []string{"target_group_arn", "target_id", "port", "availability_zone"},
				ArgumentReference: []argumentRefEntry{
					{Name: "target_group_arn", Required: true},
					{Name: "target_id", Required: true},
					{Name: "port"},
					{Name: "availability_zone"},
				},
				IdentitySchemaRequired: []string{"target_group_arn", "target_id"},
			},
			wantOK:  false,
			because: "the schema names two attributes and the example splits into three, so no prefix of the documented order is the identity",
		},
		{
			name: "surplus arguments are alternatives for one segment, not a droppable tail",
			row: importGrammarRow{
				// aws_route: three mutually exclusive destinations.
				ImportIDExample:     "rtb-656C65616E6F72_10.42.0.0/16",
				Separator:           sep("_"),
				ComposedOfArguments: yes(),
				Arguments:           []string{"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id", "route_table_id"},
				ArgumentReference: []argumentRefEntry{
					{Name: "route_table_id", Required: true},
					{Name: "destination_cidr_block"},
					{Name: "destination_ipv6_cidr_block"},
					{Name: "destination_prefix_list_id"},
				},
				IdentitySchemaRequired: []string{"route_table_id"},
			},
			wantOK:  false,
			because: "no arguments_in_doc_order, so there is no documented order to take a prefix of - and the alphabetical Arguments order would have kept the wrong argument",
		},
		{
			name: "arity already satisfied; nothing to truncate",
			row: importGrammarRow{
				ImportIDExample:     "a,b",
				Separator:           sep(","),
				ComposedOfArguments: yes(),
				Arguments:           []string{"one", "two"},
				ArgumentsInOrder:    []string{"one", "two"},
				ArgumentReference: []argumentRefEntry{
					{Name: "one", Required: true},
					{Name: "two", Required: true},
				},
			},
			wantOK:  false,
			because: "rule 1 resolves this; 1b must never touch a row whose arity already checks out",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := proposal{TFType: "aws_test", Bucket: bucketEvidenceOnly}
			ok := tryDocumentedShorterForm(&p, tc.row)
			if ok != tc.wantOK {
				t.Fatalf("tryDocumentedShorterForm = %v, want %v (%s)", ok, tc.wantOK, tc.because)
			}
			if !tc.wantOK {
				if p.Bucket != bucketEvidenceOnly {
					t.Errorf("a refused row was mutated to bucket %s; a refusal must leave the proposal untouched", p.Bucket)
				}
				return
			}
			var got []string
			if p.Bucket == bucketClientNamed {
				got = []string{p.ArgName}
			} else {
				got = p.CompositeArgs
			}
			if strings.Join(got, ",") != strings.Join(tc.wantArgs, ",") {
				t.Errorf("retained %v, want %v (%s)", got, tc.wantArgs, tc.because)
			}
		})
	}
}

// TestDocumentedShorterFormFiresOnTheCommittedArtifact is the external half,
// and the one that would catch a rule quietly widening: it runs 1b over
// every row of the committed live/import-grammar.json and pins the exact set
// of types it fires on. A rule change that reaches one more type fails here
// with that type named, rather than silently reclassifying it.
//
// The list is the measured result over the 6.59.0 scrape, not a wish: 16
// rows have an argument list longer than their example's segment count, and
// these 12 are the ones a source outside the arity arithmetic confirms.
func TestDocumentedShorterFormFiresOnTheCommittedArtifact(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	grammar, err := loadImportGrammar(root + "/" + importGrammarJSONRel)
	if err != nil {
		t.Fatalf("reading %s: %v", importGrammarJSONRel, err)
	}

	var fired []string
	for tfType, g := range grammar {
		p := proposal{TFType: tfType, Bucket: bucketEvidenceOnly}
		if tryDocumentedShorterForm(&p, g) {
			fired = append(fired, tfType)
		}
	}
	sort.Strings(fired)

	want := []string{
		"aws_cloudformation_stack_set",
		"aws_s3_bucket_abac",
		"aws_s3_bucket_accelerate_configuration",
		"aws_s3_bucket_acl",
		"aws_s3_bucket_cors_configuration",
		"aws_s3_bucket_lifecycle_configuration",
		"aws_s3_bucket_logging",
		"aws_s3_bucket_object_lock_configuration",
		"aws_s3_bucket_request_payment_configuration",
		"aws_s3_bucket_server_side_encryption_configuration",
		"aws_s3_bucket_versioning",
		"aws_s3_bucket_website_configuration",
	}
	if strings.Join(fired, "\n") != strings.Join(want, "\n") {
		t.Errorf("rule 1b fires on:\n  %s\nwant:\n  %s", strings.Join(fired, "\n  "), strings.Join(want, "\n  "))
	}
}
