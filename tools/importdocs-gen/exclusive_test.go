// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestExclusiveGroupsIn exercises the two documented shapes the scrape
// exists for, plus the refusals that keep it narrow. The inputs are the
// docs' own sentences, quoted from the 6.59.0 cache.
func TestExclusiveGroupsIn(t *testing.T) {
	args := func(names ...string) map[string]bool {
		m := map[string]bool{}
		for _, n := range names {
			m[n] = true
		}
		return m
	}

	cases := []struct {
		name string
		span string
		args map[string]bool
		want [][]string
	}{
		{
			// route.html.markdown:131, inside the Import section.
			name: "aws_route's exactly-one-of statement",
			span: "~> Exactly one of of `destination_cidr_block`, `destination_ipv6_cidr_block`, or `destination_prefix_list_id` is required.",
			args: args("destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id", "route_table_id"),
			want: [][]string{{"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id"}},
		},
		{
			// security_group_rule.html.markdown:102: the modal is inside
			// Markdown emphasis ("you _must_ provide one of them"), which a
			// plain word search misses.
			name: "aws_security_group_rule's emphasised must",
			span: "~> **Note** Although `cidr_blocks`, `ipv6_cidr_blocks`, `prefix_list_ids`, and `source_security_group_id` are all marked as optional, you _must_ provide one of them in order to configure the source of the traffic.",
			args: args("cidr_blocks", "ipv6_cidr_blocks", "prefix_list_ids", "source_security_group_id", "type"),
			want: [][]string{{"cidr_blocks", "ipv6_cidr_blocks", "prefix_list_ids", "source_security_group_id"}},
		},
		{
			// A valid-value enumeration says "one of" about VALUES, not
			// arguments, and must contribute nothing: its backticked words
			// are not in the argument pool.
			name: "valid-value list refused",
			span: "* `access_type` - (Required) Valid values: one of `Public`, `Private`.",
			args: args("access_type"),
			want: nil,
		},
		{
			// "one of" with no requirement modal is not an exclusivity
			// statement even when every name is an argument.
			name: "no modal refused",
			span: "The ID is one of `bucket` or `arn`.",
			args: args("bucket", "arn"),
			want: nil,
		},
		{
			// A statement mixing a real argument with a non-argument is
			// refused whole rather than trimmed to the recognised part.
			name: "partial match refused",
			span: "Exactly one of `bucket` or `SomethingElse` is required.",
			args: args("bucket"),
			want: nil,
		},
		{
			// A single recognised name is not a group.
			name: "singleton refused",
			span: "Exactly one of `bucket` must be set.",
			args: args("bucket"),
			want: nil,
		},
	}

	for _, tc := range cases {
		if got := exclusiveGroupsIn(tc.span, tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// exclusiveExpectations pins what the shipped artifact claims, measured at
// the sweep that introduced the field. Counts plus the two rows the scrape
// was built around, rather than a 37-row ledger, for the same reason
// separatorExpectations gives: a full ledger is rewritten wholesale on
// every doc bump, which is how a ledger stops being read.
var exclusiveExpectations = struct {
	rowsWithGroups int
	totalGroups    int
}{
	// 38/41 (2026-08-16): aliasDeclaredFor clones aws_lb's whole row,
	// exclusive_groups included, onto its provider-documented alias
	// aws_alb ("is known as ... The functionality is identical."). +1 row,
	// +1 group, both the same [subnet_mapping, subnets] pair aws_lb already
	// carried - not a new derivation, the same evidence under a second
	// registered type name.
	rowsWithGroups: 38,
	totalGroups:    41,
}

// TestShippedExclusiveGroups is the drift tripwire: a scrape change that
// starts reading more (or fewer) pages as one-of statements has to say so
// here, with the two motivating rows checked exactly. A wrong group in this
// field would put a wrong argument in an import identity once a consumer
// exists, so widening silently is the failure mode this test exists to
// block.
func TestShippedExclusiveGroups(t *testing.T) {
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
			TFType          string     `json:"tf_type"`
			ExclusiveGroups [][]string `json:"exclusive_groups"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decoding artifact: %v", err)
	}

	rows, groups := 0, 0
	byType := map[string][][]string{}
	var withGroups []string
	for _, r := range art.Rows {
		if len(r.ExclusiveGroups) == 0 {
			continue
		}
		rows++
		groups += len(r.ExclusiveGroups)
		byType[r.TFType] = r.ExclusiveGroups
		withGroups = append(withGroups, r.TFType)
	}

	if rows != exclusiveExpectations.rowsWithGroups || groups != exclusiveExpectations.totalGroups {
		t.Errorf("shipped artifact carries exclusive_groups on %d rows (%d groups), pinned %d rows (%d groups); rows: %s",
			rows, groups, exclusiveExpectations.rowsWithGroups, exclusiveExpectations.totalGroups, strings.Join(withGroups, ", "))
	}

	wantExact := map[string][][]string{
		"aws_route":               {{"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id"}},
		"aws_security_group_rule": {{"cidr_blocks", "ipv6_cidr_blocks", "prefix_list_ids", "source_security_group_id"}},
	}
	for tfType, want := range wantExact {
		if got := byType[tfType]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: shipped exclusive_groups %v, want %v", tfType, got, want)
		}
	}
}
