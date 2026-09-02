// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// The DocDB family batch (#235's demand list, #245's unreached count) admitted
// four types no ratification batch had ever reached: aws_docdb_cluster,
// aws_docdb_cluster_instance, aws_docdb_cluster_parameter_group and
// aws_docdb_subnet_group. All four are client-named on a single argument, and
// row-gen derived every row from the provider's own Import section - the four
// pages state, verbatim, "using the `cluster_identifier`", "using the
// `identifier`" and "using the `name`" twice.
//
// These tests assert the RENDERED identity rather than a predicate: a green
// admission boolean over a wrong marker is the failure mode this repository
// has hit three times, and neither DefaultTable's own shape nor row-gen's
// convergence artifact would catch a component that resolved to the wrong
// string.

// TestDocDBFamilyResolvesToTheDocumentedImportID pins each of the four rows to
// the exact value the provider's Import section documents as that type's ID.
func TestDocDBFamilyResolvesToTheDocumentedImportID(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "docdb-family"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for addr, want := range map[string]struct {
		id    string
		attrs map[string]string
	}{
		"aws_docdb_subnet_group.main": {
			id:    "prod-docdb-subnets",
			attrs: map[string]string{"name": "prod-docdb-subnets"},
		},
		"aws_docdb_cluster_parameter_group.main": {
			id:    "prod-docdb-pg",
			attrs: map[string]string{"name": "prod-docdb-pg"},
		},
		"aws_docdb_cluster.main": {
			id:    "prod-docdb",
			attrs: map[string]string{"cluster_identifier": "prod-docdb"},
		},
		"aws_docdb_cluster_instance.nodes[0]": {
			id:    "prod-docdb-0",
			attrs: map[string]string{"identifier": "prod-docdb-0"},
		},
		"aws_docdb_cluster_instance.nodes[1]": {
			id:    "prod-docdb-1",
			attrs: map[string]string{"identifier": "prod-docdb-1"},
		},
	} {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Errorf("%s resolved %s; every value its identity needs is in configuration", addr, res.Class)
			continue
		}
		if res.ImportID != want.id {
			t.Errorf("%s resolved to import ID %q, want %q", addr, res.ImportID, want.id)
		}
		for name, wantValue := range want.attrs {
			if got := res.IdentityValues[name]; got != wantValue {
				t.Errorf("%s identity attribute %q is %q, want %q (values: %s)",
					addr, name, got, wantValue, showValues(res.IdentityValues))
			}
		}
	}
}

// TestDocDBClusterCollisionStillCaught is the injectivity half. A
// cluster_identifier is unique per account and region, which is what makes the
// row safe - but only checkCollisions enforces that two blocks did not both
// write it. Admitting a type past that check would be worse than the hard
// resolve error these four used to raise, so the check is asserted here rather
// than assumed: one block interpolates the name, the other spells it, and both
// land on docdb-prod.
func TestDocDBClusterCollisionStillCaught(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "docdb-family-collision"), nil)

	_, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("two aws_docdb_cluster blocks writing the same cluster_identifier were both accepted")
	}
	if !hasDiag(diags, "Two resources with the same identity", `"docdb-prod"`) {
		t.Errorf("the refusal is not the duplicate-identity one:\n%s", renderDiags(diags))
	}
}
