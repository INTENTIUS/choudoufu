// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestCountIndexScopeForType pins countIndexScopeForType's answer for a
// representative type from each bucket #187's narrowing covers, against
// literal expectations rather than against identity.LookupType's own
// output - so this test can actually catch a regression in either
// countIndexScopeForType or the table row it reads, instead of only ever
// agreeing with whatever the table currently says.
func TestCountIndexScopeForType(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		wantSkip     bool
		wantWalkAll  bool
		wantAttrs    []string // nil when wantSkip or wantWalkAll
	}{
		{
			name:         "null_resource is RECORD_ADMITTED: identity is the record, not any argument",
			resourceType: "null_resource",
			wantSkip:     true,
		},
		{
			name:         "terraform_data is RECORD_ADMITTED: identity is the record, not any argument",
			resourceType: "terraform_data",
			wantSkip:     true,
		},
		{
			name:         "aws_instance is server-assigned: Resolve never reads an argument for it",
			resourceType: "aws_instance",
			wantSkip:     true,
		},
		{
			name:         "aws_vpc is server-assigned: Resolve never reads an argument for it",
			resourceType: "aws_vpc",
			wantSkip:     true,
		},
		{
			name:         "aws_security_group is server-assigned: Resolve never reads an argument for it",
			resourceType: "aws_security_group",
			wantSkip:     true,
		},
		{
			name:         "aws_network_acl_rule: identity-bearing set is exactly its four Components attrs",
			resourceType: "aws_network_acl_rule",
			wantAttrs:    []string{"egress", "network_acl_id", "protocol", "rule_number"},
		},
		{
			name:         "aws_route53_record: identity-bearing set is exactly its three Components attrs",
			resourceType: "aws_route53_record",
			wantAttrs:    []string{"name", "type", "zone_id"},
		},
		{
			name:         "unknown type: no table data, so every argument stays in scope",
			resourceType: "made_up_provider_made_up_type",
			wantWalkAll:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lt, isLogical := ClassifyLogicalType(tt.resourceType)
			scope := countIndexScopeForType(tt.resourceType, lt, isLogical)

			if scope.skip != tt.wantSkip {
				t.Errorf("skip = %v, want %v", scope.skip, tt.wantSkip)
			}
			if scope.walkAll != tt.wantWalkAll {
				t.Errorf("walkAll = %v, want %v", scope.walkAll, tt.wantWalkAll)
			}
			if tt.wantSkip || tt.wantWalkAll {
				return
			}
			var got []string
			for name := range scope.attrs {
				got = append(got, name)
			}
			sort.Strings(got)
			if !equalStrings(got, tt.wantAttrs) {
				t.Errorf("attrs = %v, want %v", got, tt.wantAttrs)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCountIndexRecordBackedAdmittedTypeIsFullyClean end-to-ends #187's
// RECORD_ADMITTED bucket through CheckContext, not just through
// countIndexScopeForType: with a record_store configured (so null_resource
// is admitted at all - internal/live/lint's checkManagedResources, GitHub
// issue #73), count.index inside triggers produces no issue of any rule,
// RuleCountIndex included. If the ordering fix in lint.go regressed - if
// checkCountIndex ran before ClassifyLogicalType's answer were available
// again - this is the test that would catch it.
func TestCountIndexRecordBackedAdmittedTypeIsFullyClean(t *testing.T) {
	const src = `
terraform {
  live {
    estate = "test-estate"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "null_resource" "trigger" {
  count = 2

  triggers = {
    slot = "slot-${count.index}"
  }
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}

	cfg := loadConfigDir(t, dir)
	issues := CheckContext(t.Context(), cfg)
	if len(issues) != 0 {
		t.Fatalf("got %d issues for a RECORD_ADMITTED type with a record_store configured, want 0: %v", len(issues), issues)
	}
}

// TestCountIndexUnknownTypeStillWalksNestedBlocks pins the conservative
// default for a type absent from identity.LookupType entirely: every
// argument at every depth, nested blocks included, stays in scope, exactly
// as the rule behaved before #187. The nested value indexes into a
// collection (#192's narrowing only leaves a pure scalar unrefused) so it
// stays a genuine hit regardless of scope. The type is also outside the v0
// admission table, so RuleUnadmittedType fires too; this test only checks
// that RuleCountIndex is among the issues, which is what a regression in
// the walkAll fallback would remove.
func TestCountIndexUnknownTypeStillWalksNestedBlocks(t *testing.T) {
	const src = `
resource "made_up_provider_made_up_type" "nested" {
  count = 2

  block {
    value = ["a", "b", "c"][count.index]
  }
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}

	cfg := loadConfigDir(t, dir)
	issues := CheckContext(t.Context(), cfg)

	var found bool
	for _, issue := range issues {
		if issue.Rule == RuleCountIndex {
			found = true
		}
	}
	if !found {
		t.Fatalf("got no RuleCountIndex issue for count.index in a nested block of a type absent from identity.LookupType, want one (walkAll fallback): %v", issues)
	}
}
