// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package registry

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const fixtureMapping = `{
  "generated_by": "test fixture",
  "counts": {"types": 5, "mapped": 3, "fold": 1, "none": 1},
  "rows": [
    {"tf_type": "aws_efs_file_system", "cfn_type": "AWS::EFS::FileSystem", "via": "name", "fold_parent": null, "note": null},
    {"tf_type": "aws_route_table_association", "cfn_type": "AWS::EC2::SubnetRouteTableAssociation", "via": "alias", "fold_parent": null, "note": null},
    {"tf_type": "aws_parent_input_thing", "cfn_type": "AWS::Example::ParentInputThing", "via": "name", "fold_parent": null, "note": null},
    {"tf_type": "aws_folded_thing", "cfn_type": null, "via": "fold", "fold_parent": "AWS::Example::Parent", "note": null},
    {"tf_type": "aws_unmapped_thing", "cfn_type": null, "via": "none", "fold_parent": null, "note": "no counterpart"}
  ]
}`

const fixtureRegistry = `{
  "pin": {"digest": "sha256:test", "resources": 3, "accepted": "2026-01-01"},
  "generated_by": "test fixture",
  "counts": {},
  "types": [
    {
      "type_name": "AWS::EFS::FileSystem",
      "primary_identifier": ["FileSystemId"],
      "tagging": {"taggable": true, "tag_on_create": true, "tag_updatable": true},
      "handlers": {"create": true, "read": true, "update": true, "delete": true, "list": true}
    },
    {
      "type_name": "AWS::EC2::SubnetRouteTableAssociation",
      "primary_identifier": ["Id"],
      "tagging": {"taggable": false, "tag_on_create": false, "tag_updatable": false},
      "handlers": {"create": true, "read": true, "update": false, "delete": true, "list": false}
    },
    {
      "type_name": "AWS::Example::ParentInputThing",
      "primary_identifier": ["Arn", "ChildId"],
      "tagging": {"taggable": true, "tag_on_create": true, "tag_updatable": true},
      "handlers": {"create": true, "read": true, "update": true, "delete": true, "list": true, "list_required_input": ["Arn"]}
    }
  ]
}`

func testRoster(t *testing.T) *Roster {
	t.Helper()
	r, err := Parse([]byte(fixtureMapping), []byte(fixtureRegistry))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return r
}

func TestCloudControlTypeMappedRows(t *testing.T) {
	r := testRoster(t)

	tests := []struct {
		tfType      string
		wantCFN     string
		wantMapped  bool
		description string
	}{
		{"aws_efs_file_system", "AWS::EFS::FileSystem", true, "via name"},
		{"aws_route_table_association", "AWS::EC2::SubnetRouteTableAssociation", true, "via alias"},
		{"aws_folded_thing", "", false, "via fold: no CFN type of its own"},
		{"aws_unmapped_thing", "", false, "via none"},
		{"aws_never_seen", "", false, "absent from the mapping artifact entirely"},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			got, ok := r.CloudControlType(tt.tfType)
			if ok != tt.wantMapped || got != tt.wantCFN {
				t.Errorf("CloudControlType(%q) = (%q, %v), want (%q, %v)", tt.tfType, got, ok, tt.wantCFN, tt.wantMapped)
			}
		})
	}
}

func TestListable(t *testing.T) {
	r := testRoster(t)

	tests := []struct {
		cfnType string
		want    bool
	}{
		{"AWS::EFS::FileSystem", true},                   // list, no required input
		{"AWS::EC2::SubnetRouteTableAssociation", false}, // handlers.list is false
		{"AWS::Example::ParentInputThing", false},        // list, but required input
		{"AWS::Not::InRegistry", false},
	}
	for _, tt := range tests {
		if got := r.Listable(tt.cfnType); got != tt.want {
			t.Errorf("Listable(%q) = %v, want %v", tt.cfnType, got, tt.want)
		}
	}
}

func TestEnumerationSource(t *testing.T) {
	r := testRoster(t)

	tests := []struct {
		name    string
		tfType  string
		wantCFN string
		wantOK  bool
	}{
		{"mapped and listable", "aws_efs_file_system", "AWS::EFS::FileSystem", true},
		{"mapped but not listable (no list handler)", "aws_route_table_association", "", false},
		{"mapped but not listable (required input)", "aws_parent_input_thing", "", false},
		{"folded, no CFN type", "aws_folded_thing", "", false},
		{"unmapped", "aws_unmapped_thing", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := r.EnumerationSource(tt.tfType)
			if ok != tt.wantOK || got != tt.wantCFN {
				t.Errorf("EnumerationSource(%q) = (%q, %v), want (%q, %v)", tt.tfType, got, ok, tt.wantCFN, tt.wantOK)
			}
		})
	}
}

func TestIdentifierArityAndTaggable(t *testing.T) {
	r := testRoster(t)

	if got := r.IdentifierArity("AWS::EFS::FileSystem"); got != 1 {
		t.Errorf("IdentifierArity(EFS::FileSystem) = %d, want 1", got)
	}
	if got := r.IdentifierArity("AWS::Example::ParentInputThing"); got != 2 {
		t.Errorf("IdentifierArity(ParentInputThing) = %d, want 2", got)
	}
	if got := r.IdentifierArity("AWS::Not::InRegistry"); got != 0 {
		t.Errorf("IdentifierArity(unknown) = %d, want 0", got)
	}

	if !r.Taggable("AWS::EFS::FileSystem") {
		t.Error("EFS::FileSystem should be taggable per the fixture")
	}
	if r.Taggable("AWS::EC2::SubnetRouteTableAssociation") {
		t.Error("SubnetRouteTableAssociation should not be taggable per the fixture")
	}
}

func TestNilRosterIsInert(t *testing.T) {
	var r *Roster
	if _, ok := r.CloudControlType("aws_vpc"); ok {
		t.Error("a nil Roster should map nothing")
	}
	if r.Listable("AWS::EC2::VPC") {
		t.Error("a nil Roster should list nothing")
	}
	if r.Taggable("AWS::EC2::VPC") {
		t.Error("a nil Roster should tag nothing")
	}
	if r.IdentifierArity("AWS::EC2::VPC") != 0 {
		t.Error("a nil Roster should report zero arity")
	}
	if _, ok := r.EnumerationSource("aws_vpc"); ok {
		t.Error("a nil Roster should offer no enumeration source")
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, err := Parse([]byte("not json"), []byte(fixtureRegistry)); err == nil {
		t.Error("expected an error parsing invalid mapping JSON")
	}
	if _, err := Parse([]byte(fixtureMapping), []byte("not json")); err == nil {
		t.Error("expected an error parsing invalid registry JSON")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/nonexistent/mapping.json", "/nonexistent/registry.json"); err == nil {
		t.Error("expected an error loading a nonexistent file")
	}
}

// repoRoot resolves this checkout's root from this file's own location, the
// same trick internal/live/flocitest.RepoRoot uses.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at internal/live/registry/roster_test.go.
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return root
}

// TestLoadRealArtifacts is the smoke test against the actual committed
// live/mapping.json and live/registry.json: the loader has to survive the
// real files, not just a hand-written fixture, and at least one real type
// should come back mapped and listable so the Cloud Control fallback has
// something to work with.
func TestLoadRealArtifacts(t *testing.T) {
	root := repoRoot(t)
	mappingPath := filepath.Join(root, "live", "mapping.json")
	registryPath := filepath.Join(root, "live", "registry.json")
	if _, err := os.Stat(mappingPath); err != nil {
		t.Skipf("live/mapping.json not present in this checkout: %v", err)
	}
	if _, err := os.Stat(registryPath); err != nil {
		t.Skipf("live/registry.json not present in this checkout: %v", err)
	}

	r, err := Load(mappingPath, registryPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(r.cfnType) == 0 {
		t.Error("no mapped types were loaded from the real mapping artifact")
	}
	if len(r.listable) == 0 {
		t.Error("no types were loaded from the real registry artifact")
	}

	var sawListable bool
	for tf := range r.cfnType {
		if cfn, ok := r.EnumerationSource(tf); ok {
			sawListable = true
			t.Logf("first mapped+listable example: %s -> %s", tf, cfn)
			break
		}
	}
	if !sawListable {
		t.Error("no TF type in the real artifacts is both mapped and listable; the Cloud Control fallback would never fire")
	}
}
